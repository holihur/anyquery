package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/sql"
)

// field is one addressable column in a relation.
type field struct {
	Name       string
	Key        string   // lowercase internal row key
	Qualifiers []string // lowercase alias/table/source qualifiers
	Alias      string   // display qualifier for star expansion
	Type       string
}

type relation struct {
	fields []field
	rows   []map[string]any
}

func (r *relation) nullRow() map[string]any {
	row := make(map[string]any, len(r.fields))
	for _, f := range r.fields {
		row[f.Key] = nil
	}
	return row
}

func (ec *execCtx) relationForTableExpr(te sql.TableExpr) (*relation, error) {
	switch t := te.(type) {
	case *sql.TableRef:
		return ec.relationForTableRef(t)
	case *sql.DerivedTable:
		ds, err := ec.executeSelect(t.Query)
		if err != nil {
			return nil, err
		}
		return datasetToRelation(ds, t.Alias, t.Alias), nil
	case *sql.JoinExpr:
		left, err := ec.relationForTableExpr(t.Left)
		if err != nil {
			return nil, err
		}
		right, err := ec.relationForTableExpr(t.Right)
		if err != nil {
			return nil, err
		}
		return ec.joinRelations(left, right, t.Type, t.On, t.Using)
	default:
		return nil, errf("unsupported", "unsupported table expression %T", te)
	}
}

func (ec *execCtx) relationForTableRef(ref *sql.TableRef) (*relation, error) {
	alias := ref.Alias
	if ref.Source == "" {
		ds, ok := ec.ctes[strings.ToLower(ref.Table)]
		if !ok {
			return nil, &Error{Code: "unknown_table", Message: fmt.Sprintf("unknown table or CTE %q", ref.Table), Hint: "reference tables as <source>.<table>, or define a CTE"}
		}
		if alias == "" {
			alias = ref.Table
		}
		return datasetToRelation(ds, alias, ref.Table), nil
	}
	_, tm, err := ec.tableMeta(ref.Source, ref.Table)
	if err != nil {
		return nil, err
	}
	if alias == "" {
		alias = ref.Table
		if i := strings.LastIndexByte(alias, '.'); i >= 0 {
			alias = alias[i+1:]
		}
	}
	c, err := ec.connectorFor(ref.Source)
	if err != nil {
		return nil, err
	}
	fetchLimit := ec.engine.Opts.MaxSourceRows + 1
	start := time.Now()
	fr, err := c.Fetch(ec.ctx, connector.FetchRequest{Table: tm.Name, Limit: fetchLimit})
	if err != nil {
		return nil, &Error{Code: "source_query_error", Message: fmt.Sprintf("read %s.%s: %v", ref.Source, tm.Name, err)}
	}
	elapsed := time.Since(start).Milliseconds()
	rows := fr.Rows
	truncated := fr.Truncated
	if len(rows) > ec.engine.Opts.MaxSourceRows {
		rows = rows[:ec.engine.Opts.MaxSourceRows]
		truncated = true
	}
	ec.stats = append(ec.stats, SourceStat{Source: ref.Source, Table: tm.Name, Rows: len(rows), DurationMS: elapsed})
	if truncated {
		ec.warnings = append(ec.warnings, fmt.Sprintf("source %s.%s was truncated at %d rows; add filters or a LIMIT", ref.Source, tm.Name, ec.engine.Opts.MaxSourceRows))
	}

	qualifiers := []string{
		strings.ToLower(alias),
		strings.ToLower(tm.Name),
		strings.ToLower(ref.Source),
		strings.ToLower(ref.Source + "." + tm.Name),
	}
	if i := strings.LastIndexByte(tm.Name, '.'); i >= 0 {
		qualifiers = append(qualifiers, strings.ToLower(tm.Name[i+1:]))
	}
	qualifiers = uniqueStrings(qualifiers)

	rel := &relation{}
	colTypes := map[string]connector.Column{}
	for _, c := range tm.Columns {
		colTypes[strings.ToLower(c.Name)] = c
	}
	// Some sources (REST/CSV/Mongo sampling) may discover extra fields in rows.
	seenCols := map[string]bool{}
	addField := func(name, typ string) {
		lower := strings.ToLower(name)
		if seenCols[lower] {
			// Disambiguate a genuinely duplicate column name.
			lower = fmt.Sprintf("%s_%d", lower, len(rel.fields))
			name = fmt.Sprintf("%s_%d", name, len(rel.fields))
		}
		seenCols[lower] = true
		rel.fields = append(rel.fields, field{
			Name:       name,
			Key:        strings.ToLower(alias + "." + lower),
			Qualifiers: qualifiers,
			Alias:      alias,
			Type:       typ,
		})
	}
	if len(tm.Columns) > 0 {
		for _, c := range tm.Columns {
			addField(c.Name, c.Type)
		}
	} else if len(rows) > 0 {
		var names []string
		for k := range rows[0] {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, n := range names {
			addField(n, inferType(rows[0][n]))
		}
	}
	for _, row := range rows {
		for k, v := range row {
			if !seenCols[strings.ToLower(k)] {
				addField(k, inferType(v))
			}
			_ = v
		}
	}
	for i := range rel.fields {
		rel.fields[i].Key = strings.ToLower(alias + "." + strings.ToLower(rel.fields[i].Name))
	}
	// Build rows keyed by qualified field keys.
	for _, row := range rows {
		out := make(map[string]any, len(rel.fields))
		for _, f := range rel.fields {
			v, ok := row[f.Name]
			if !ok {
				v = caseInsensitiveLookup(row, f.Name)
			}
			out[f.Key] = v
		}
		rel.rows = append(rel.rows, out)
	}
	rel = ensureUniqueKeys(rel)
	return rel, nil
}

func caseInsensitiveLookup(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return nil
}

func datasetToRelation(ds *dataset, alias, cteName string) *relation {
	alias = strings.ToLower(alias)
	qualifiers := uniqueStrings([]string{alias, strings.ToLower(cteName)})
	rel := &relation{}
	for _, c := range ds.columns {
		key := strings.ToLower(c.Name)
		if alias != "" {
			key = strings.ToLower(alias + "." + c.Name)
		}
		rel.fields = append(rel.fields, field{
			Name:       c.Name,
			Key:        key,
			Qualifiers: qualifiers,
			Alias:      alias,
			Type:       c.Type,
		})
	}
	rel.rows = make([]map[string]any, 0, len(ds.rows))
	for _, r := range ds.rows {
		out := make(map[string]any, len(rel.fields))
		for _, f := range rel.fields {
			out[f.Key] = r[strings.ToLower(f.Name)]
		}
		rel.rows = append(rel.rows, out)
	}
	return ensureUniqueKeys(rel)
}

func ensureUniqueKeys(rel *relation) *relation {
	seen := map[string]int{}
	for i := range rel.fields {
		base := rel.fields[i].Key
		if n := seen[base]; n > 0 {
			rel.fields[i].Key = fmt.Sprintf("%s_%d", base, n+1)
		}
		seen[base]++
	}
	return rel
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// resolve column references against a relation.
func (r *relation) resolve(ref *sql.ColumnRef) (any, field, error) {
	parts := make([]string, len(ref.Parts))
	for i, p := range ref.Parts {
		parts[i] = strings.ToLower(p)
	}
	col := parts[len(parts)-1]
	if len(parts) > 1 {
		qualifier := strings.Join(parts[:len(parts)-1], ".")
		if f, ok := r.fieldByQualifier(qualifier, col); ok {
			return nil, f, nil
		}
		// Try treating the final part as the column and a shorter qualifier.
		for i := 1; i < len(parts)-1; i++ {
			q := strings.Join(parts[:i], ".")
			if f, ok := r.fieldByQualifier(q, strings.Join(parts[i:len(parts)-1], ".")); ok {
				return nil, f, nil
			}
		}
		return nil, field{}, &Error{Code: "column_not_found", Message: fmt.Sprintf("column %q not found", ref.String()), Hint: "available columns: " + strings.Join(r.columnNames(), ", ")}
	}
	var matches []field
	for _, f := range r.fields {
		if strings.EqualFold(f.Name, col) {
			matches = append(matches, f)
		}
	}
	if len(matches) == 0 {
		// Maybe the qualifier itself is dotted but parsed as one part? Try
		// matching against a qualified key (needed for quoted names).
		return nil, field{}, &Error{Code: "column_not_found", Message: fmt.Sprintf("column %q not found", ref.String()), Hint: "available columns: " + strings.Join(r.columnNames(), ", ")}
	}
	if len(matches) > 1 {
		return nil, field{}, &Error{Code: "ambiguous_column", Message: fmt.Sprintf("column %q is ambiguous", ref.String()), Hint: "qualify it with a table alias"}
	}
	return nil, matches[0], nil
}

func (r *relation) fieldByQualifier(qualifier, col string) (field, bool) {
	for _, f := range r.fields {
		if !strings.EqualFold(f.Name, col) {
			continue
		}
		for _, q := range f.Qualifiers {
			if q == qualifier {
				return f, true
			}
		}
	}
	return field{}, false
}

func (r *relation) columnNames() []string {
	var names []string
	for _, f := range r.fields {
		names = append(names, f.Alias+"."+f.Name)
	}
	if len(names) > 20 {
		names = names[:20]
		names = append(names, "...")
	}
	return names
}

func (ec *execCtx) joinRelations(left, right *relation, typ sql.JoinType, on sql.Expr, using []string) (*relation, error) {
	combined := &relation{
		fields: append(append([]field{}, left.fields...), right.fields...),
	}
	combined = ensureUniqueKeys(combined)
	// Detect duplicate alias qualifiers which would make references ambiguous.
	leftAliases, rightAliases := map[string]bool{}, map[string]bool{}
	for _, f := range left.fields {
		if f.Alias != "" {
			leftAliases[f.Alias] = true
		}
	}
	for _, f := range right.fields {
		if f.Alias != "" {
			rightAliases[f.Alias] = true
		}
	}
	for a := range leftAliases {
		if rightAliases[a] {
			return nil, &Error{Code: "duplicate_alias", Message: fmt.Sprintf("table alias %q is used more than once", a), Hint: "give each table a unique alias"}
		}
	}

	var usingPairs [][2]field
	for _, name := range using {
		lf, lok := findFieldByName(left, name)
		rf, rok := findFieldByName(right, name)
		if !lok || !rok {
			return nil, &Error{Code: "column_not_found", Message: fmt.Sprintf("USING column %q must exist on both sides", name)}
		}
		usingPairs = append(usingPairs, [2]field{lf, rf})
	}

	leftMatched := make([]bool, len(left.rows))
	rightMatched := make([]bool, len(right.rows))
	pairs := 0
	for li, lr := range left.rows {
		for ri, rr := range right.rows {
			row := make(map[string]any, len(combined.fields))
			for k, v := range lr {
				row[k] = v
			}
			for k, v := range rr {
				row[k] = v
			}
			match := true
			if len(usingPairs) > 0 {
				for _, p := range usingPairs {
					if !valuesEqual(lr[p[0].Key], rr[p[1].Key]) {
						match = false
						break
					}
				}
			} else if on != nil {
				ctx := &evalCtx{rel: combined, row: row}
				v, err := evalExpr(ctx, on)
				if err != nil {
					return nil, err
				}
				match = truthy(v)
			}
			if !match {
				continue
			}
			leftMatched[li] = true
			rightMatched[ri] = true
			combined.rows = append(combined.rows, row)
			pairs++
			if len(combined.rows) > ec.engine.Opts.MaxJoinRows {
				return nil, &Error{Code: "join_too_large", Message: fmt.Sprintf("join produced more than %d intermediate rows", ec.engine.Opts.MaxJoinRows), Hint: "add join or WHERE conditions"}
			}
		}
	}
	switch typ {
	case sql.JoinLeft, sql.JoinFull:
		for i, lr := range left.rows {
			if leftMatched[i] {
				continue
			}
			row := make(map[string]any, len(combined.fields))
			for k, v := range lr {
				row[k] = v
			}
			for _, f := range right.fields {
				if _, ok := row[f.Key]; !ok {
					row[f.Key] = nil
				}
			}
			combined.rows = append(combined.rows, row)
		}
	}
	if typ == sql.JoinRight || typ == sql.JoinFull {
		for i, rr := range right.rows {
			if rightMatched[i] {
				continue
			}
			row := make(map[string]any, len(combined.fields))
			for _, f := range left.fields {
				row[f.Key] = nil
			}
			for k, v := range rr {
				row[k] = v
			}
			combined.rows = append(combined.rows, row)
		}
	}
	return combined, nil
}

func findFieldByName(rel *relation, name string) (field, bool) {
	for _, f := range rel.fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return field{}, false
}

func inferType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer"
	case float32, float64:
		return "number"
	case time.Time:
		return "timestamp"
	case []any, []string:
		return "array"
	case map[string]any:
		return "object"
	case string:
		return "string"
	default:
		return "string"
	}
}

func mergeTypes(a, b string) string {
	if a == "" || a == "null" {
		return b
	}
	if b == "" || b == "null" {
		return a
	}
	if a == b {
		return a
	}
	if (a == "integer" && b == "number") || (a == "number" && b == "integer") {
		return "number"
	}
	return "string"
}

func inferColumns(rows []map[string]any, cols []ResultColumn) []ResultColumn {
	out := make([]ResultColumn, len(cols))
	copy(out, cols)
	for i := range out {
		key := strings.ToLower(out[i].Name)
		typ := ""
		for _, r := range rows {
			typ = mergeTypes(typ, inferType(r[key]))
		}
		if typ != "" {
			out[i].Type = typ
		}
	}
	return out
}
