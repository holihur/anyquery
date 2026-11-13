package engine

import (
	"strings"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/sql"
)

type scanHint struct {
	columns    []string
	predicates []connector.Predicate
	limit      int
}

type scanTable struct {
	ref        *sql.TableRef
	alias      string
	qualifiers map[string]bool
	columns    map[string]string
	types      map[string]string
	all        bool
	wanted     map[string]bool
}

// buildScanHints performs conservative projection/predicate/limit pushdown for
// the next FROM tree. It never changes semantics: if anything is ambiguous or
// unsupported, that optimization is simply skipped.
func (ec *execCtx) buildScanHints(s *sql.SelectStmt) map[*sql.TableRef]*scanHint {
	refs := collectScanRefs(s.From)
	if len(refs) == 0 {
		return nil
	}
	tables := make([]*scanTable, 0, len(refs))
	for _, ref := range refs {
		if ref.Source == "" {
			continue
		}
		_, tm, err := ec.tableMeta(ref.Source, ref.Table)
		if err != nil || tm == nil {
			continue
		}
		alias := ref.Alias
		if alias == "" {
			alias = ref.Table
			if i := strings.LastIndexByte(alias, '.'); i >= 0 {
				alias = alias[i+1:]
			}
		}
		st := &scanTable{
			ref:        ref,
			alias:      alias,
			qualifiers: map[string]bool{},
			columns:    map[string]string{},
			types:      map[string]string{},
			wanted:     map[string]bool{},
		}
		for _, q := range []string{alias, ref.Table, ref.Source, ref.Source + "." + ref.Table} {
			if q != "" {
				st.qualifiers[strings.ToLower(q)] = true
			}
		}
		if i := strings.LastIndexByte(ref.Table, '.'); i >= 0 {
			st.qualifiers[strings.ToLower(ref.Table[i+1:])] = true
		}
		for _, c := range tm.Columns {
			st.columns[strings.ToLower(c.Name)] = c.Name
			st.types[strings.ToLower(c.Name)] = strings.ToLower(c.Type)
		}
		tables = append(tables, st)
	}
	if len(tables) == 0 {
		return nil
	}
	hints := make(map[*sql.TableRef]*scanHint, len(tables))
	for _, t := range tables {
		hints[t.ref] = &scanHint{}
	}
	pruneSafe := true
	checkSubquery := func(e sql.Expr) {
		found := false
		sql.WalkExpr(e, func(x sql.Expr) bool {
			switch v := x.(type) {
			case *sql.SubqueryExpr:
				found = true
			case *sql.ExistsExpr:
				found = true
			case *sql.InExpr:
				if v.Subquery != nil {
					found = true
				}
			case *sql.QuantifiedExpr:
				if v.Subquery != nil {
					found = true
				}
			}
			return !found
		})
		if found {
			pruneSafe = false
		}
	}
	findTable := func(qualifier string) *scanTable {
		q := strings.ToLower(qualifier)
		for _, t := range tables {
			if t.qualifiers[q] {
				return t
			}
		}
		return nil
	}
	addColumn := func(t *scanTable, name string) {
		if t == nil {
			return
		}
		if actual, ok := t.columns[strings.ToLower(name)]; ok {
			t.wanted[strings.ToLower(actual)] = true
			return
		}
		pruneSafe = false
	}
	addAll := func(t *scanTable) {
		if t == nil {
			return
		}
		t.all = true
		for k := range t.columns {
			t.wanted[k] = true
		}
	}
	for _, item := range s.Projections {
		if item.Star {
			if item.StarTable == "" {
				pruneSafe = false
			} else if t := findTable(item.StarTable); t != nil {
				addAll(t)
			} else {
				pruneSafe = false
			}
		}
	}
	walkExpr := func(e sql.Expr) {
		if e == nil {
			return
		}
		sql.WalkExpr(e, func(x sql.Expr) bool {
			switch v := x.(type) {
			case *sql.ColumnRef:
				name := v.Parts[len(v.Parts)-1]
				if len(v.Parts) > 1 {
					t := findTable(strings.Join(v.Parts[:len(v.Parts)-1], "."))
					if t == nil {
						pruneSafe = false
						return true
					}
					addColumn(t, name)
					return true
				}
				var matches []*scanTable
				for _, t := range tables {
					if _, ok := t.columns[strings.ToLower(name)]; ok {
						matches = append(matches, t)
					}
				}
				if len(matches) == 0 {
					pruneSafe = false
					return true
				}
				for _, t := range matches {
					addColumn(t, name)
				}
			}
			return true
		})
	}
	for _, item := range s.Projections {
		checkSubquery(item.Expr)
		walkExpr(item.Expr)
	}
	checkSubquery(s.Where)
	walkExpr(s.Where)
	for _, g := range s.GroupBy {
		checkSubquery(g)
		walkExpr(g)
	}
	for _, set := range s.GroupingSets {
		for _, g := range set {
			checkSubquery(g)
			walkExpr(g)
		}
	}
	checkSubquery(s.Having)
	walkExpr(s.Having)
	for _, o := range s.OrderBy {
		checkSubquery(o.Expr)
		walkExpr(o.Expr)
	}
	var walkJoin func(sql.TableExpr)
	walkJoin = func(te sql.TableExpr) {
		switch x := te.(type) {
		case *sql.JoinExpr:
			walkJoin(x.Left)
			walkJoin(x.Right)
			if x.On != nil {
				checkSubquery(x.On)
				walkExpr(x.On)
			}
			for _, name := range x.Using {
				for _, t := range tables {
					if _, ok := t.columns[strings.ToLower(name)]; ok {
						addColumn(t, name)
					}
				}
			}
		}
	}
	for _, te := range s.From {
		walkJoin(te)
	}

	if pruneSafe {
		for _, t := range tables {
			h := hints[t.ref]
			for k := range t.wanted {
				h.columns = append(h.columns, t.columns[k])
			}
		}
	}

	// Push simple AND predicates that reference exactly one table.
	if s.Where != nil {
		for _, cond := range splitAnd(s.Where) {
			t, col, op, value, ok := ec.simplePredicate(cond, tables, findTable)
			if !ok {
				continue
			}
			h := hints[t.ref]
			h.predicates = append(h.predicates, connector.Predicate{Column: col, Op: op, Value: value})
			addColumn(t, col)
			if pruneSafe {
				actual := t.columns[strings.ToLower(col)]
				found := false
				for _, c := range h.columns {
					if strings.EqualFold(c, actual) {
						found = true
						break
					}
				}
				if !found {
					h.columns = append(h.columns, actual)
				}
			}
		}
	}

	// A LIMIT without filtering/sorting/aggregation can cap the source scan too.
	if len(tables) == 1 && s.Limit != nil && s.Where == nil && len(s.GroupBy) == 0 && s.Having == nil && len(s.OrderBy) == 0 && !s.Distinct && len(s.SetOps) == 0 && len(s.Pipes) == 0 {
		simple := true
		for _, item := range s.Projections {
			if sql.ContainsAggregate(item.Expr) || sql.ContainsWindow(item.Expr) {
				simple = false
				break
			}
		}
		if simple {
			n := *s.Limit
			if s.Offset != nil {
				n += *s.Offset
			}
			n++
			if n > 0 {
				hints[tables[0].ref].limit = n
			}
		}
	}
	return hints
}

func collectScanRefs(from []sql.TableExpr) []*sql.TableRef {
	var out []*sql.TableRef
	var walk func(sql.TableExpr)
	walk = func(te sql.TableExpr) {
		switch x := te.(type) {
		case *sql.TableRef:
			out = append(out, x)
		case *sql.JoinExpr:
			walk(x.Left)
			walk(x.Right)
		}
	}
	for _, te := range from {
		walk(te)
	}
	return out
}

func (ec *execCtx) simplePredicate(e sql.Expr, tables []*scanTable, findTable func(string) *scanTable) (*scanTable, string, string, any, bool) {
	cmp, ok := e.(*sql.CompareExpr)
	if !ok || cmp.Negated {
		return nil, "", "", nil, false
	}
	op := ""
	switch strings.ToUpper(cmp.Op) {
	case "=":
		op = "eq"
	case "!=", "<>":
		op = "ne"
	case "<":
		op = "lt"
	case "<=":
		op = "lte"
	case ">":
		op = "gt"
	case ">=":
		op = "gte"
	case "LIKE":
		op = "like"
	default:
		return nil, "", "", nil, false
	}
	col, t := predicateColumn(cmp.Left, tables, findTable)
	val, ok := ec.predicateValue(cmp.Right)
	if !ok {
		col, t = predicateColumn(cmp.Right, tables, findTable)
		val, ok = ec.predicateValue(cmp.Left)
		if ok {
			// Reverse ordering operators when the literal is on the left.
			switch op {
			case "lt":
				op = "gt"
			case "lte":
				op = "gte"
			case "gt":
				op = "lt"
			case "gte":
				op = "lte"
			}
		}
	}
	if t == nil || !ok || val == nil {
		return nil, "", "", nil, false
	}
	colType := t.types[strings.ToLower(col)]
	numeric := false
	stringValue := false
	switch val.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		numeric = true
	case string:
		stringValue = true
	}
	switch op {
	case "eq":
		if strings.Contains(colType, "int") || strings.Contains(colType, "number") || strings.Contains(colType, "float") || strings.Contains(colType, "decimal") {
			if !numeric {
				return nil, "", "", nil, false
			}
		} else if (strings.Contains(colType, "string") || strings.Contains(colType, "char") || strings.Contains(colType, "text")) && !stringValue {
			return nil, "", "", nil, false
		}
	case "ne", "lt", "lte", "gt", "gte":
		// Range/inequality pushdown is only safe for numeric values; string
		// collation can differ between the source and the engine.
		if !numeric {
			return nil, "", "", nil, false
		}
	case "like":
		return nil, "", "", nil, false
	}
	return t, col, op, val, true
}

func predicateColumn(e sql.Expr, tables []*scanTable, findTable func(string) *scanTable) (string, *scanTable) {
	ref, ok := e.(*sql.ColumnRef)
	if !ok || len(ref.Parts) == 0 {
		return "", nil
	}
	name := ref.Parts[len(ref.Parts)-1]
	if len(ref.Parts) > 1 {
		t := findTable(strings.Join(ref.Parts[:len(ref.Parts)-1], "."))
		if t == nil {
			return "", nil
		}
		return name, t
	}
	var match *scanTable
	for _, t := range tables {
		if _, ok := t.columns[strings.ToLower(name)]; ok {
			if match != nil {
				return "", nil
			}
			match = t
		}
	}
	if match == nil {
		return "", nil
	}
	return name, match
}

func (ec *execCtx) predicateValue(e sql.Expr) (any, bool) {
	switch x := e.(type) {
	case *sql.Literal:
		return x.Value, true
	case *sql.Parameter:
		if x.Index < 1 || x.Index > len(ec.params) {
			return nil, false
		}
		return ec.params[x.Index-1], true
	default:
		return nil, false
	}
}

func (ec *execCtx) scanHintFor(ref *sql.TableRef) *scanHint {
	if ec.scanHints == nil {
		return nil
	}
	return ec.scanHints[ref]
}
