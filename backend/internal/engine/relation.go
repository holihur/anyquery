package engine

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/sql"
)

// field is one addressable column in a relation.
type field struct {
	Name       string
	Key        string   // lowercase internal row key
	Qualifiers []string // lowercase alias/table/source qualifiers
	Alias      string   // display qualifier for star expansion
	Type       string
	StarHidden bool // USING/NATURAL common column from the right side
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
	case *sql.LateralTable:
		return ec.relationForTableExpr(t.Table)
	case *sql.DerivedTable:
		ds, err := ec.executeSelect(t.Query)
		if err != nil {
			return nil, err
		}
		if len(t.Columns) > 0 {
			ds, err = renameDatasetColumns(ds, t.Columns)
			if err != nil {
				return nil, err
			}
		}
		return datasetToRelation(ds, t.Alias, t.Alias), nil
	case *sql.TableFunction:
		return ec.relationForTableFunction(t)
	case *sql.JSONTable:
		return ec.relationForJSONTable(t)
	case *sql.MatchRecognize:
		return ec.relationForMatchRecognize(t)
	case *sql.ValuesTable:
		return ec.relationForValues(t)
	case *sql.JoinExpr:
		left, err := ec.relationForTableExpr(t.Left)
		if err != nil {
			return nil, err
		}
		if isLateralTable(t.Right) {
			return ec.joinLateral(left, t.Right, t.Type, t.On, t.Using, t.Natural)
		}
		right, err := ec.relationForTableExpr(t.Right)
		if err != nil {
			return nil, err
		}
		return ec.joinRelations(left, right, t.Type, t.On, t.Using, t.Natural)
	default:
		return nil, errf("unsupported", "unsupported table expression %T", te)
	}
}

func renameDatasetColumns(ds *dataset, names []string) (*dataset, error) {
	if len(names) != len(ds.columns) {
		return nil, errf("derived_table_error", "derived table declares %d columns but returns %d", len(names), len(ds.columns))
	}
	cols := make([]ResultColumn, len(ds.columns))
	copy(cols, ds.columns)
	for i := range cols {
		cols[i].Name = names[i]
	}
	out := &dataset{columns: cols, rows: make([]map[string]any, len(ds.rows))}
	for i, row := range ds.rows {
		mapped := make(map[string]any, len(cols))
		for j, old := range ds.columns {
			mapped[strings.ToLower(cols[j].Name)] = row[strings.ToLower(old.Name)]
		}
		out.rows[i] = mapped
	}
	return out, nil
}

func (ec *execCtx) relationForTableFunction(t *sql.TableFunction) (*relation, error) {
	switch strings.ToLower(t.Name) {
	case "unnest":
		return ec.relationForUnnest(t)
	default:
		return nil, errf("unknown_table_function", "unknown table function %s", strings.ToUpper(t.Name))
	}
}

func (ec *execCtx) relationForUnnest(t *sql.TableFunction) (*relation, error) {
	if len(t.Args) != 1 {
		return nil, errf("invalid_arguments", "UNNEST expects exactly one argument")
	}
	args := make([]any, len(t.Args))
	for i, expr := range t.Args {
		v, err := evalExpr(&evalCtx{outer: ec.outerEval, exec: ec}, expr)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	var items []any
	switch v := args[0].(type) {
	case nil:
		items = nil
	case []any:
		items = v
	case []map[string]any:
		for _, m := range v {
			items = append(items, m)
		}
	case string:
		var decoded any
		if err := json.Unmarshal([]byte(v), &decoded); err != nil {
			return nil, errf("type_error", "UNNEST expects a JSON array or array value")
		}
		if arr, ok := decoded.([]any); ok {
			items = arr
		} else {
			items = []any{decoded}
		}
	default:
		return nil, errf("type_error", "UNNEST expects a JSON array or array value")
	}
	cols := t.Columns
	if len(cols) == 0 {
		cols = []string{"unnest"}
	}
	if len(cols) != 1 {
		return nil, errf("invalid_arguments", "UNNEST alias must declare exactly one column")
	}
	ds := &dataset{columns: []ResultColumn{{Name: cols[0]}}}
	for _, item := range items {
		ds.rows = append(ds.rows, map[string]any{strings.ToLower(cols[0]): item})
	}
	ds.columns = inferColumns(ds.rows, ds.columns)
	alias := t.Alias
	if alias == "" {
		alias = t.Name
	}
	return datasetToRelation(ds, alias, alias), nil
}

func (ec *execCtx) relationForValues(v *sql.ValuesTable) (*relation, error) {
	ds, err := ec.datasetForValues(v)
	if err != nil {
		return nil, err
	}
	return datasetToRelation(ds, v.Alias, v.Alias), nil
}

func (ec *execCtx) datasetForValues(v *sql.ValuesTable) (*dataset, error) {
	if len(v.Rows) == 0 {
		return nil, errf("values_error", "VALUES requires at least one row")
	}
	width := len(v.Rows[0])
	if width == 0 {
		return nil, errf("values_error", "VALUES rows cannot be empty")
	}
	for _, row := range v.Rows {
		if len(row) != width {
			return nil, errf("values_error", "VALUES rows must all have the same number of columns")
		}
	}
	cols := make([]ResultColumn, width)
	if len(v.Columns) > 0 {
		if len(v.Columns) != width {
			return nil, errf("values_error", "VALUES alias declares %d columns but rows have %d", len(v.Columns), width)
		}
		for i, name := range v.Columns {
			cols[i] = ResultColumn{Name: name}
		}
	} else {
		for i := range cols {
			cols[i] = ResultColumn{Name: fmt.Sprintf("column%d", i+1)}
		}
	}
	ds := &dataset{columns: cols}
	for _, exprs := range v.Rows {
		row := make(map[string]any, width)
		ctx := &evalCtx{outer: ec.outerEval, exec: ec}
		for i, e := range exprs {
			val, err := evalExpr(ctx, e)
			if err != nil {
				return nil, err
			}
			row[strings.ToLower(cols[i].Name)] = val
		}
		ds.rows = append(ds.rows, row)
	}
	ds.columns = inferColumns(ds.rows, ds.columns)
	return ds, nil
}

func isLateralTable(te sql.TableExpr) bool {
	_, ok := te.(*sql.LateralTable)
	return ok
}

// joinLateral evaluates a LATERAL table expression once per left row with the
// left row as the outer evaluation context. Each left row is joined with its
// own right relation, preserving outer-join semantics.
func (ec *execCtx) joinLateral(left *relation, te sql.TableExpr, jt sql.JoinType, on sql.Expr, using []string, natural bool) (*relation, error) {
	wrapped, ok := te.(*sql.LateralTable)
	if !ok {
		return nil, errf("internal_error", "joinLateral called without a LATERAL table")
	}
	inner := wrapped.Table
	out := &relation{fields: left.fields}
	for _, leftRow := range left.rows {
		outer := &evalCtx{rel: left, row: leftRow, outer: ec.outerEval, exec: ec}
		prev := ec.outerEval
		ec.outerEval = outer
		right, err := ec.relationForTableExpr(inner)
		ec.outerEval = prev
		if err != nil {
			return nil, err
		}
		leftOne := &relation{fields: left.fields, rows: []map[string]any{leftRow}}
		joined, err := ec.joinRelations(leftOne, right, jt, on, using, natural)
		if err != nil {
			return nil, err
		}
		if len(joined.rows) > 0 && len(out.rows) == 0 {
			out.fields = joined.fields
		}
		out.rows = append(out.rows, joined.rows...)
	}
	return out, nil
}

func (ec *execCtx) relationForTableRef(ref *sql.TableRef) (*relation, error) {
	alias := ref.Alias
	if ref.Source == "" {
		ds, ok := ec.ctes[strings.ToLower(ref.Table)]
		if !ok {
			return nil, &Error{Code: "unknown_table", Message: fmt.Sprintf("unknown table or CTE %q", ref.Table), Hint: "reference tables as <source>.<table>, or define a CTE"}
		}
		if ref.Temporal != nil {
			return nil, &Error{Code: "temporal_not_supported", Message: "FOR SYSTEM_TIME requires a data-source table, not a CTE"}
		}
		if alias == "" {
			alias = ref.Table
		}
		if ref.Sample != nil {
			ds = &dataset{columns: ds.columns, rows: sampleRows(ds.rows, ref.Sample)}
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
	req := connector.FetchRequest{Table: tm.Name, Limit: fetchLimit}
	pushedLimit := false
	caps := c.Capabilities()
	if ref.Temporal != nil {
		if !caps.SystemTime {
			return nil, &Error{Code: "temporal_not_supported", Message: fmt.Sprintf("data source %q does not support FOR SYSTEM_TIME reads", ref.Source), Hint: "use a source with system-time history support or remove the temporal clause"}
		}
		temporal, err := ec.resolveTemporal(ref.Temporal)
		if err != nil {
			return nil, err
		}
		req.Temporal = temporal
	}
	if hint := ec.scanHintFor(ref); hint != nil {
		if caps.ProjectionPushdown && len(hint.columns) > 0 {
			req.Columns = hint.columns
		}
		if caps.FilterPushdown && len(hint.predicates) > 0 {
			req.Predicates = hint.predicates
		}
		if caps.LimitPushdown && hint.limit > 0 && hint.limit < req.Limit {
			req.Limit = hint.limit
			pushedLimit = true
		}
	}
	start := time.Now()
	fr, err := c.Fetch(ec.ctx, req)
	if err != nil {
		return nil, &Error{Code: "source_query_error", Message: fmt.Sprintf("read %s.%s: %v", ref.Source, tm.Name, err)}
	}
	elapsed := time.Since(start).Milliseconds()
	rows := fr.Rows
	truncated := fr.Truncated
	if ref.Sample != nil {
		rows = sampleRows(rows, ref.Sample)
	}
	if len(rows) > ec.engine.Opts.MaxSourceRows {
		rows = rows[:ec.engine.Opts.MaxSourceRows]
		truncated = true
	}
	ec.stats = append(ec.stats, SourceStat{Source: ref.Source, Table: tm.Name, Rows: len(rows), DurationMS: elapsed})
	if truncated && !pushedLimit {
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

func (ec *execCtx) resolveTemporal(t *sql.TemporalClause) (*connector.TemporalRange, error) {
	if t == nil {
		return nil, nil
	}
	parse := func(e sql.Expr, label string) (time.Time, error) {
		if e == nil {
			return time.Time{}, errf("invalid_temporal", "FOR SYSTEM_TIME %s is missing a timestamp", label)
		}
		v, err := evalExpr(&evalCtx{outer: ec.outerEval, exec: ec}, e)
		if err != nil {
			return time.Time{}, err
		}
		tm, ok := toTime(v)
		if !ok {
			return time.Time{}, errf("invalid_temporal", "FOR SYSTEM_TIME %s expects a timestamp", label)
		}
		return tm.UTC(), nil
	}
	switch t.Mode {
	case "as_of":
		tm, err := parse(t.AsOf, "AS OF")
		if err != nil {
			return nil, err
		}
		return &connector.TemporalRange{Mode: "as_of", AsOf: tm}, nil
	case "from_to", "between":
		from, err := parse(t.From, "FROM")
		if err != nil {
			return nil, err
		}
		to, err := parse(t.To, "TO")
		if err != nil {
			return nil, err
		}
		if to.Before(from) {
			return nil, errf("invalid_temporal", "FOR SYSTEM_TIME end time must not be before start time")
		}
		return &connector.TemporalRange{Mode: t.Mode, From: from, To: to}, nil
	case "all":
		return &connector.TemporalRange{Mode: "all"}, nil
	default:
		return nil, errf("invalid_temporal", "unsupported FOR SYSTEM_TIME mode %q", t.Mode)
	}
}

func sampleRows(rows []map[string]any, sample *sql.TableSample) []map[string]any {
	if sample == nil || sample.Percent >= 100 {
		return rows
	}
	if sample.Percent <= 0 {
		return nil
	}
	seed := time.Now().UnixNano()
	if sample.Seed != nil {
		seed = *sample.Seed
	}
	rng := rand.New(rand.NewSource(seed))
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if rng.Float64()*100 < sample.Percent {
			out = append(out, row)
		}
	}
	return out
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
		if !f.StarHidden && strings.EqualFold(f.Name, col) {
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

func (ec *execCtx) joinRelations(left, right *relation, typ sql.JoinType, on sql.Expr, using []string, natural bool) (*relation, error) {
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

	if natural {
		using = naturalJoinColumns(left, right)
		on = nil
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

	for _, p := range usingPairs {
		for i := range combined.fields {
			if combined.fields[i].Key == p[1].Key {
				combined.fields[i].StarHidden = true
			}
		}
	}

	leftMatched := make([]bool, len(left.rows))
	rightMatched := make([]bool, len(right.rows))

	// Choose a hash join when the join has equi-key columns. This is the
	// common cross-source case (Mongo _id / SQL integer id / JSON number) and
	// avoids an O(n*m) nested loop. Any leftover ON predicates are still
	// evaluated on candidate pairs, and OR conditions fall back to nested loop.
	joinPairs := usingPairs
	if len(joinPairs) == 0 && on != nil {
		joinPairs = equiPairs(on, left, right)
	}
	if len(joinPairs) > 0 {
		buildLeft := typ == sql.JoinInner && len(left.rows) < len(right.rows)
		if err := ec.hashJoin(combined, left, right, typ, on, joinPairs, leftMatched, rightMatched, buildLeft); err != nil {
			return nil, err
		}
	} else {
		for li, lr := range left.rows {
			for ri, rr := range right.rows {
				row := mergeRows(lr, rr, combined.fields)
				match := true
				if len(usingPairs) > 0 {
					for _, p := range usingPairs {
						if lr[p[0].Key] == nil || rr[p[1].Key] == nil || !valuesEqual(lr[p[0].Key], rr[p[1].Key]) {
							match = false
							break
						}
					}
				} else if on != nil {
					ctx := &evalCtx{rel: combined, row: row, outer: ec.outerEval, exec: ec}
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
				if len(combined.rows) > ec.engine.Opts.MaxJoinRows {
					return nil, &Error{Code: "join_too_large", Message: fmt.Sprintf("join produced more than %d intermediate rows", ec.engine.Opts.MaxJoinRows), Hint: "add join or WHERE conditions"}
				}
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
			for _, p := range usingPairs {
				row[p[0].Key] = rr[p[1].Key]
			}
			combined.rows = append(combined.rows, row)
		}
	}
	return combined, nil
}

func mergeRows(left, right map[string]any, fields []field) map[string]any {
	row := make(map[string]any, len(fields))
	for k, v := range left {
		row[k] = v
	}
	for k, v := range right {
		row[k] = v
	}
	return row
}

func naturalJoinColumns(left, right *relation) []string {
	leftNames := map[string]bool{}
	for _, f := range left.fields {
		leftNames[strings.ToLower(f.Name)] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range right.fields {
		name := strings.ToLower(f.Name)
		if leftNames[name] && !seen[name] {
			seen[name] = true
			out = append(out, f.Name)
		}
	}
	return out
}

func (ec *execCtx) hashJoin(combined, left, right *relation, typ sql.JoinType, on sql.Expr, pairs [][2]field, leftMatched, rightMatched []bool, buildLeft bool) error {
	index := make(map[string][]int)
	if buildLeft {
		for li, lr := range left.rows {
			vals := make([]any, len(pairs))
			for i, p := range pairs {
				vals[i] = lr[p[0].Key]
			}
			key, ok := joinKey(vals)
			if !ok {
				continue
			}
			index[key] = append(index[key], li)
		}
	} else {
		for ri, rr := range right.rows {
			vals := make([]any, len(pairs))
			for i, p := range pairs {
				vals[i] = rr[p[1].Key]
			}
			key, ok := joinKey(vals)
			if !ok {
				continue
			}
			index[key] = append(index[key], ri)
		}
	}

	probeCount := len(right.rows)
	if buildLeft {
		probeCount = len(right.rows)
	} else {
		probeCount = len(left.rows)
	}
	for pos := 0; pos < probeCount; pos++ {
		var vals []any
		if buildLeft {
			rr := right.rows[pos]
			vals = make([]any, len(pairs))
			for i, p := range pairs {
				vals[i] = rr[p[1].Key]
			}
		} else {
			lr := left.rows[pos]
			vals = make([]any, len(pairs))
			for i, p := range pairs {
				vals[i] = lr[p[0].Key]
			}
		}
		key, ok := joinKey(vals)
		if !ok {
			continue
		}
		for _, idx := range index[key] {
			li, ri := idx, pos
			if !buildLeft {
				li, ri = pos, idx
			}
			row := mergeRows(left.rows[li], right.rows[ri], combined.fields)
			if on != nil {
				ctx := &evalCtx{rel: combined, row: row, outer: ec.outerEval, exec: ec}
				v, err := evalExpr(ctx, on)
				if err != nil {
					return err
				}
				if !truthy(v) {
					continue
				}
			}
			leftMatched[li] = true
			rightMatched[ri] = true
			combined.rows = append(combined.rows, row)
			if len(combined.rows) > ec.engine.Opts.MaxJoinRows {
				return &Error{Code: "join_too_large", Message: fmt.Sprintf("join produced more than %d intermediate rows", ec.engine.Opts.MaxJoinRows), Hint: "add join or WHERE conditions"}
			}
		}
	}
	return nil
}

// splitAnd flattens AND expressions for equi-key detection.
func splitAnd(e sql.Expr) []sql.Expr {
	if b, ok := e.(*sql.BinaryExpr); ok && strings.EqualFold(b.Op, "AND") {
		return append(splitAnd(b.Left), splitAnd(b.Right)...)
	}
	return []sql.Expr{e}
}

// equiPairs finds simple column = column predicates across the two sides.
func equiPairs(on sql.Expr, left, right *relation) [][2]field {
	var out [][2]field
	for _, condition := range splitAnd(on) {
		cmp, ok := condition.(*sql.CompareExpr)
		if !ok || cmp.Op != "=" || cmp.Negated {
			continue
		}
		lf, lok := columnField(left, cmp.Left)
		rf, rok := columnField(right, cmp.Right)
		if lok && rok {
			out = append(out, [2]field{lf, rf})
			continue
		}
		lf, lok = columnField(left, cmp.Right)
		rf, rok = columnField(right, cmp.Left)
		if lok && rok {
			out = append(out, [2]field{lf, rf})
		}
	}
	return out
}

func columnField(rel *relation, e sql.Expr) (field, bool) {
	ref, ok := e.(*sql.ColumnRef)
	if !ok {
		return field{}, false
	}
	_, f, err := rel.resolve(ref)
	if err != nil {
		return field{}, false
	}
	return f, true
}

// joinKey normalizes values so numeric types from different sources
// (int64 in SQL, float64 in JSON/Mongo) produce the same hash key.
func joinKey(vals []any) (string, bool) {
	parts := make([]string, len(vals))
	for i, v := range vals {
		if v == nil {
			return "", false
		}
		if s, ok := v.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil && strings.TrimSpace(s) != "" {
				parts[i] = "n:" + strconv.FormatFloat(f, 'g', -1, 64)
			} else {
				parts[i] = "s:" + s
			}
			continue
		}
		if f, ok := toFloat(v); ok {
			parts[i] = "n:" + strconv.FormatFloat(f, 'g', -1, 64)
			continue
		}
		parts[i] = fmt.Sprintf("%T:%v", v, v)
	}
	return strings.Join(parts, "\x00"), true
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
	case intervalValue:
		return "interval"
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
