package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/anyquery/anyquery/internal/sql"
)

type projectionItem struct {
	label    string
	key      string
	expr     sql.Expr
	fieldKey string
	star     bool
	typ      string
}

type plannedProjection struct {
	items []projectionItem
	cols  []ResultColumn
}

func (ec *execCtx) executeSelect(s *sql.SelectStmt) (*dataset, error) {
	// CTEs are materialized once and scoped to this query.
	var restore []func()
	for _, cte := range s.CTEs {
		ds, err := ec.executeSelect(cte.Query)
		if err != nil {
			return nil, err
		}
		if len(cte.Columns) > 0 {
			if len(cte.Columns) != len(ds.columns) {
				return nil, errf("cte_error", "CTE %q declares %d columns but returns %d", cte.Name, len(cte.Columns), len(ds.columns))
			}
			for i := range ds.columns {
				ds.columns[i].Name = cte.Columns[i]
			}
		}
		key := strings.ToLower(cte.Name)
		if old, ok := ec.ctes[key]; ok {
			restore = append(restore, func() { ec.ctes[key] = old })
		} else {
			restore = append(restore, func() { delete(ec.ctes, key) })
		}
		ec.ctes[key] = ds
	}
	defer func() {
		for _, fn := range restore {
			fn()
		}
	}()

	// FROM: cross-join comma-separated table expressions.
	var rel *relation
	if len(s.From) == 0 {
		rel = &relation{rows: []map[string]any{{}}}
	} else {
		var err error
		rel, err = ec.relationForTableExpr(s.From[0])
		if err != nil {
			return nil, err
		}
		for _, te := range s.From[1:] {
			right, err := ec.relationForTableExpr(te)
			if err != nil {
				return nil, err
			}
			rel, err = ec.joinRelations(rel, right, sql.JoinCross, nil, nil, false)
			if err != nil {
				return nil, err
			}
		}
	}
	ec.stage("scan")

	if s.Where != nil {
		var filtered []map[string]any
		for _, row := range rel.rows {
			ctx := &evalCtx{rel: rel, row: row, outer: ec.outerEval, exec: ec}
			v, err := evalExpr(ctx, s.Where)
			if err != nil {
				return nil, err
			}
			if truthy(v) {
				filtered = append(filtered, row)
			}
		}
		rel.rows = filtered
		ec.stage("filter")
	}

	hasAgg := len(s.GroupBy) > 0
	for _, item := range s.Projections {
		if item.Expr != nil && sql.ContainsAggregate(item.Expr) {
			hasAgg = true
		}
	}
	if s.Having != nil && sql.ContainsAggregate(s.Having) {
		hasAgg = true
	}
	for _, o := range s.OrderBy {
		if sql.ContainsAggregate(o.Expr) {
			hasAgg = true
		}
	}

	windowCalls, winErr := collectWindowCalls(s.Projections, s.OrderBy)
	if winErr != nil {
		return nil, winErr
	}
	if len(windowCalls) > 0 && hasAgg {
		return nil, &Error{Code: "invalid_window", Message: "window functions cannot be combined with GROUP BY or aggregate projections", Hint: "compute the aggregate in a CTE, then apply the window in an outer SELECT"}
	}
	var windowValues []map[string]any
	if len(windowCalls) > 0 {
		windowValues, winErr = ec.computeWindows(rel, windowCalls)
		if winErr != nil {
			return nil, winErr
		}
		ec.stage("window")
	}

	var plan *plannedProjection
	var err error
	if hasAgg {
		if err := ensureNoStarAggregate(s.Projections); err != nil {
			return nil, err
		}
		plan, err = buildProjection(rel, s.Projections)
		if err != nil {
			return nil, err
		}
		ec.stage("aggregate")
	} else {
		plan, err = buildProjection(rel, s.Projections)
		if err != nil {
			return nil, err
		}
		ec.stage("project")
	}

	var rows []evalRow
	if hasAgg {
		groupBy, err := resolveGroupBy(s)
		if err != nil {
			return nil, err
		}
		groupItems, err := buildGroupPlans(rel, groupBy)
		if err != nil {
			return nil, err
		}
		groups := map[string][]map[string]any{}
		var order []string
		for _, row := range rel.rows {
			ctx := &evalCtx{rel: rel, row: row, outer: ec.outerEval, exec: ec}
			vals := make([]any, len(groupItems))
			for i, g := range groupItems {
				v, err := evalExpr(ctx, g.expr)
				if err != nil {
					return nil, err
				}
				vals[i] = v
			}
			key := canonicalValues(vals)
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			groups[key] = append(groups[key], row)
		}
		if len(s.GroupBy) == 0 {
			// A global aggregate always produces one row, even over no input.
			if len(rel.rows) == 0 {
				order = append(order, "")
				groups[""] = nil
			}
		}
		for _, key := range order {
			groupRows := groups[key]
			var first map[string]any
			if len(groupRows) > 0 {
				first = groupRows[0]
			}
			out := make(map[string]any, len(plan.items))
			ctx := &evalCtx{rel: rel, row: first, group: groupRows, outer: ec.outerEval, exec: ec}
			for _, item := range plan.items {
				v, err := evalExpr(ctx, item.expr)
				if err != nil {
					return nil, err
				}
				out[item.key] = v
			}
			rows = append(rows, evalRow{out: out, first: first, group: groupRows})
		}
	} else {
		for ri, row := range rel.rows {
			out := make(map[string]any, len(plan.items))
			var windows map[string]any
			if windowValues != nil {
				windows = windowValues[ri]
			}
			ctx := &evalCtx{rel: rel, row: row, outer: ec.outerEval, exec: ec, windows: windows}
			for _, item := range plan.items {
				var v any
				if item.star {
					v = row[item.fieldKey]
				} else {
					var err error
					v, err = evalExpr(ctx, item.expr)
					if err != nil {
						return nil, err
					}
				}
				out[item.key] = v
			}
			rows = append(rows, evalRow{out: out, first: row, windows: windows})
		}
	}

	// HAVING (aggregates and/or projected aliases).
	if s.Having != nil {
		filtered := rows[:0]
		for _, r := range rows {
			ctx := &evalCtx{rel: rel, row: r.first, aliases: r.out, group: r.group, outer: ec.outerEval, exec: ec}
			v, err := evalExpr(ctx, s.Having)
			if err != nil {
				return nil, err
			}
			if truthy(v) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
		ec.stage("having")
	}

	// DISTINCT applies per SELECT term, before set operations.
	columns := make([]ResultColumn, len(plan.cols))
	copy(columns, plan.cols)
	if s.Distinct {
		rows = distinctEvalRows(rows, columns)
		ec.stage("distinct")
	}

	// UNION / INTERSECT / EXCEPT combine complete SELECT terms.
	if len(s.SetOps) > 0 {
		var err error
		rows, columns, err = ec.applySetOps(s, rows, columns)
		if err != nil {
			return nil, err
		}
		ec.stage("set-op")
	}

	// ORDER BY applies to the combined result.
	if len(s.OrderBy) > 0 {
		type sortRow struct {
			r    evalRow
			keys []any
		}
		sortable := make([]sortRow, len(rows))
		for i, r := range rows {
			keys := make([]any, len(s.OrderBy))
			for j, o := range s.OrderBy {
				ctx := &evalCtx{rel: rel, row: r.first, aliases: r.out, group: r.group, outer: ec.outerEval, exec: ec, windows: r.windows}
				if n, ok := orderOrdinal(o.Expr); ok {
					if n < 1 || n > len(columns) {
						return nil, errf("invalid_order", "ORDER BY position %d is out of range", n)
					}
					keys[j] = valuesInColumnOrder(r.out, columns)[n-1]
					continue
				}
				v, err := evalExpr(ctx, o.Expr)
				if err != nil {
					return nil, err
				}
				keys[j] = v
			}
			sortable[i] = sortRow{r: r, keys: keys}
		}
		sort.SliceStable(sortable, func(i, j int) bool {
			return lessSortKeys(sortable[i].keys, sortable[j].keys, s.OrderBy)
		})
		for i := range sortable {
			rows[i] = sortable[i].r
		}
		ec.stage("sort")
	}

	// LIMIT / OFFSET.
	if s.Offset != nil && *s.Offset > 0 {
		if *s.Offset >= len(rows) {
			rows = nil
		} else {
			rows = rows[*s.Offset:]
		}
	}
	if s.Limit != nil && *s.Limit >= 0 && len(rows) > *s.Limit {
		rows = rows[:*s.Limit]
		ec.stage("limit")
	}

	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = r.out
	}
	ds := &dataset{columns: inferColumns(out, columns), rows: out}
	if len(s.Pipes) > 0 {
		return ec.applyPipes(ds, s.Pipes)
	}
	return ds, nil
}

type evalRow struct {
	out     map[string]any
	first   map[string]any
	group   []map[string]any
	windows map[string]any
}

func orderOrdinal(e sql.Expr) (int, bool) {
	lit, ok := e.(*sql.Literal)
	if !ok || lit.Kind != sql.LitInt {
		return 0, false
	}
	n, ok := lit.Value.(int64)
	if !ok || n <= 0 {
		return 0, false
	}
	return int(n), true
}

func distinctEvalRows(rows []evalRow, columns []ResultColumn) []evalRow {
	seen := map[string]bool{}
	out := rows[:0]
	for _, r := range rows {
		key := canonicalValues(valuesInColumnOrder(r.out, columns))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

func (ec *execCtx) applySetOps(s *sql.SelectStmt, leftRows []evalRow, columns []ResultColumn) ([]evalRow, []ResultColumn, error) {
	current := leftRows
	for _, op := range s.SetOps {
		right, err := ec.executeSelect(op.Right)
		if err != nil {
			return nil, nil, err
		}
		if len(right.columns) != len(columns) {
			return nil, nil, errf("set_operation_error", "%s requires both sides to return the same number of columns (%d vs %d)", op.Op, len(columns), len(right.columns))
		}
		rightRows := make([]evalRow, 0, len(right.rows))
		for _, rr := range right.rows {
			m := make(map[string]any, len(columns))
			for i, col := range columns {
				m[strings.ToLower(col.Name)] = rr[strings.ToLower(right.columns[i].Name)]
			}
			rightRows = append(rightRows, evalRow{out: m, first: m})
		}
		current, err = combineSetRows(strings.ToUpper(op.Op), op.All, current, rightRows, columns)
		if err != nil {
			return nil, nil, err
		}
	}
	return current, columns, nil
}

func combineSetRows(op string, all bool, left, right []evalRow, columns []ResultColumn) ([]evalRow, error) {
	keyOf := func(r evalRow) string { return canonicalValues(valuesInColumnOrder(r.out, columns)) }
	switch op {
	case "UNION":
		if all {
			return append(append([]evalRow{}, left...), right...), nil
		}
		seen := map[string]bool{}
		out := make([]evalRow, 0, len(left)+len(right))
		for _, r := range append(append([]evalRow{}, left...), right...) {
			k := keyOf(r)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, r)
		}
		return out, nil
	case "INTERSECT":
		rightCount := map[string]int{}
		for _, r := range right {
			rightCount[keyOf(r)]++
		}
		if all {
			out := make([]evalRow, 0)
			for _, r := range left {
				k := keyOf(r)
				if rightCount[k] > 0 {
					rightCount[k]--
					out = append(out, r)
				}
			}
			return out, nil
		}
		seen := map[string]bool{}
		out := make([]evalRow, 0)
		for _, r := range left {
			k := keyOf(r)
			if rightCount[k] > 0 && !seen[k] {
				seen[k] = true
				out = append(out, r)
			}
		}
		return out, nil
	case "EXCEPT":
		rightCount := map[string]int{}
		for _, r := range right {
			rightCount[keyOf(r)]++
		}
		if all {
			out := make([]evalRow, 0)
			for _, r := range left {
				k := keyOf(r)
				if rightCount[k] > 0 {
					rightCount[k]--
					continue
				}
				out = append(out, r)
			}
			return out, nil
		}
		seen := map[string]bool{}
		out := make([]evalRow, 0)
		for _, r := range left {
			k := keyOf(r)
			if rightCount[k] == 0 && !seen[k] {
				seen[k] = true
				out = append(out, r)
			}
		}
		return out, nil
	default:
		return nil, errf("set_operation_error", "unsupported set operation %s", op)
	}
}

func resolveGroupBy(s *sql.SelectStmt) ([]sql.Expr, error) {
	out := make([]sql.Expr, len(s.GroupBy))
	for i, e := range s.GroupBy {
		if n, ok := orderOrdinal(e); ok {
			if n < 1 || n > len(s.Projections) {
				return nil, errf("invalid_group", "GROUP BY position %d is out of range", n)
			}
			item := s.Projections[n-1]
			if item.Star || item.Expr == nil {
				return nil, errf("invalid_group", "GROUP BY position %d does not refer to an expression", n)
			}
			out[i] = item.Expr
			continue
		}
		if ref, ok := e.(*sql.ColumnRef); ok && len(ref.Parts) == 1 {
			found := false
			for _, item := range s.Projections {
				if item.Alias != "" && strings.EqualFold(item.Alias, ref.Parts[0]) {
					out[i] = item.Expr
					found = true
					break
				}
			}
			if found {
				continue
			}
		}
		out[i] = e
	}
	return out, nil
}

func ensureNoStarAggregate(items []sql.SelectItem) error {
	for _, item := range items {
		if item.Star {
			return &Error{Code: "invalid_aggregate", Message: "SELECT * cannot be combined with GROUP BY or aggregate functions", Hint: "list the grouped columns explicitly"}
		}
	}
	return nil
}

type groupPlan struct {
	expr sql.Expr
	key  string
}

func buildGroupPlans(rel *relation, groupBy []sql.Expr) ([]groupPlan, error) {
	out := make([]groupPlan, len(groupBy))
	for i, e := range groupBy {
		if sql.ContainsAggregate(e) {
			return nil, errf("invalid_aggregate", "GROUP BY expression cannot contain an aggregate: %s", e.String())
		}
		out[i] = groupPlan{expr: e, key: fmt.Sprintf("g%d", i)}
	}
	return out, nil
}

func buildProjection(rel *relation, items []sql.SelectItem) (*plannedProjection, error) {
	plan := &plannedProjection{}
	starNameCounts := map[string]int{}
	selectedStarFields := map[int][]field{}
	for i, item := range items {
		if !item.Star {
			continue
		}
		fields, err := fieldsForStar(rel, item.StarTable)
		if err != nil {
			return nil, err
		}
		selectedStarFields[i] = fields
		for _, f := range fields {
			starNameCounts[strings.ToLower(f.Name)]++
		}
	}
	seenLabels := map[string]int{}
	addItem := func(it projectionItem, typ string) {
		base := it.label
		if base == "" {
			base = "column"
		}
		if n := seenLabels[strings.ToLower(base)]; n > 0 {
			it.label = fmt.Sprintf("%s_%d", base, n+1)
		}
		seenLabels[strings.ToLower(base)]++
		it.key = strings.ToLower(it.label)
		it.typ = typ
		plan.items = append(plan.items, it)
		plan.cols = append(plan.cols, ResultColumn{Name: it.label, Type: typ})
	}
	for i, item := range items {
		if item.Star {
			for _, f := range selectedStarFields[i] {
				label := f.Name
				if starNameCounts[strings.ToLower(f.Name)] > 1 && f.Alias != "" {
					label = f.Alias + "." + f.Name
				}
				addItem(projectionItem{label: label, fieldKey: f.Key, star: true}, f.Type)
			}
			continue
		}
		label := item.Alias
		if label == "" {
			label = sql.Label(item)
		}
		addItem(projectionItem{label: label, expr: item.Expr}, "")
	}
	if len(plan.items) == 0 {
		return nil, errf("invalid_projection", "query produces no columns")
	}
	return plan, nil
}

func fieldsForStar(rel *relation, qualifier string) ([]field, error) {
	if len(rel.fields) == 0 {
		return nil, &Error{Code: "invalid_projection", Message: "SELECT * requires a FROM clause"}
	}
	if qualifier == "" {
		return rel.fields, nil
	}
	q := strings.ToLower(qualifier)
	var out []field
	for _, f := range rel.fields {
		for _, fq := range f.Qualifiers {
			if fq == q {
				out = append(out, f)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, &Error{Code: "unknown_table", Message: fmt.Sprintf("no table or alias %q for %s.*", qualifier, qualifier)}
	}
	return out, nil
}

func valuesInColumnOrder(row map[string]any, cols []ResultColumn) []any {
	vals := make([]any, len(cols))
	for i, c := range cols {
		vals[i] = row[strings.ToLower(c.Name)]
	}
	return vals
}

func canonicalValues(vals []any) string {
	norm := make([]any, len(vals))
	for i, v := range vals {
		norm[i] = canonicalValue(v)
	}
	b, _ := json.Marshal(norm)
	return string(b)
}

func canonicalValue(v any) any {
	switch x := v.(type) {
	case nil, bool, string, float64, int64:
		return x
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		return float64(x)
	case float32:
		return float64(x)
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(v)
	}
}

func lessSortKeys(a, b []any, items []sql.OrderItem) bool {
	for i := range items {
		if valuesEqual(a[i], b[i]) {
			continue
		}
		// NULLs always sort last, regardless of direction.
		if a[i] == nil || b[i] == nil {
			if a[i] == nil && b[i] == nil {
				continue
			}
			nullsFirst := items[i].Nulls == "first"
			if a[i] == nil {
				return nullsFirst
			}
			return !nullsFirst
		}
		c := compareValues(a[i], b[i])
		if items[i].Desc {
			return c > 0
		}
		return c < 0
	}
	return false
}

// ---- pipelines -------------------------------------------------------------

func (ec *execCtx) applyPipes(ds *dataset, pipes []sql.PipeStage) (*dataset, error) {
	for _, stage := range pipes {
		switch p := stage.(type) {
		case *sql.PipeWhere:
			filtered := ds.rows[:0]
			for _, row := range ds.rows {
				v, err := evalExpr(ec.datasetCtx(ds, row), p.Expr)
				if err != nil {
					return nil, err
				}
				if truthy(v) {
					filtered = append(filtered, row)
				}
			}
			ds.rows = filtered
			ec.stage("pipe:filter")
		case *sql.PipeSelect:
			out, err := ec.pipeProject(ds, p.Items, p.Distinct)
			if err != nil {
				return nil, err
			}
			ds = out
			ec.stage("pipe:select")
		case *sql.PipeAggregate:
			out, err := ec.pipeAggregate(ds, p)
			if err != nil {
				return nil, err
			}
			ds = out
			ec.stage("pipe:aggregate")
		case *sql.PipeOrder:
			if err := sortDataset(ec, ds, p.Items); err != nil {
				return nil, err
			}
			ec.stage("pipe:sort")
		case *sql.PipeLimit:
			off := p.Offset
			if off < 0 {
				off = 0
			}
			if off >= len(ds.rows) {
				ds.rows = nil
			} else {
				ds.rows = ds.rows[off:]
			}
			if p.Limit >= 0 && len(ds.rows) > p.Limit {
				ds.rows = ds.rows[:p.Limit]
			}
			ec.stage("pipe:limit")
		case *sql.PipeDistinct:
			distinctDataset(ds)
			ec.stage("pipe:distinct")
		case *sql.PipeJoin:
			out, err := ec.pipeJoin(ds, p)
			if err != nil {
				return nil, err
			}
			ds = out
			ec.stage("pipe:join")
		default:
			return nil, errf("unsupported", "unsupported pipeline stage %T", stage)
		}
	}
	return ds, nil
}

func (ec *execCtx) pipeProject(ds *dataset, items []sql.SelectItem, distinct bool) (*dataset, error) {
	for _, item := range items {
		if item.Expr != nil && sql.ContainsAggregate(item.Expr) {
			return nil, &Error{Code: "invalid_aggregate", Message: "aggregate functions are not allowed in a SELECT pipe stage", Hint: "use |> AGGREGATE ... GROUP BY ..."}
		}
	}
	rel := datasetToRelation(ds, "", "")
	plan, err := buildProjection(rel, items)
	if err != nil {
		return nil, err
	}
	out := &dataset{columns: plan.cols}
	for _, row := range ds.rows {
		o := make(map[string]any, len(plan.items))
		ctx := ec.datasetCtx(ds, row)
		for _, item := range plan.items {
			var v any
			if item.star {
				v = row[strings.ToLower(item.fieldKey)]
			} else {
				var err error
				v, err = evalExpr(ctx, item.expr)
				if err != nil {
					return nil, err
				}
			}
			o[item.key] = v
		}
		out.rows = append(out.rows, o)
	}
	out.columns = inferColumns(out.rows, out.columns)
	if distinct {
		distinctDataset(out)
	}
	return out, nil
}

func (ec *execCtx) pipeAggregate(ds *dataset, p *sql.PipeAggregate) (*dataset, error) {
	for _, it := range p.Items {
		if it.Star {
			return nil, &Error{Code: "invalid_aggregate", Message: "SELECT * is not allowed in AGGREGATE", Hint: "list grouped columns explicitly"}
		}
	}
	rel := datasetToRelation(ds, "", "")
	plan, err := buildProjection(rel, p.Items)
	if err != nil {
		return nil, err
	}
	groups := map[string][]map[string]any{}
	var order []string
	for _, row := range ds.rows {
		ctx := ec.datasetCtx(ds, row)
		vals := make([]any, len(p.GroupBy))
		for i, g := range p.GroupBy {
			v, err := evalExpr(ctx, g)
			if err != nil {
				return nil, err
			}
			vals[i] = v
		}
		key := canonicalValues(vals)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], row)
	}
	if len(p.GroupBy) == 0 && len(ds.rows) == 0 {
		order = append(order, "")
		groups[""] = nil
	}
	out := &dataset{columns: plan.cols}
	for _, k := range order {
		groupRows := groups[k]
		var first map[string]any
		if len(groupRows) > 0 {
			first = groupRows[0]
		}
		ctx := ec.datasetCtx(ds, first)
		ctx.group = groupRows
		o := make(map[string]any, len(plan.items))
		for _, item := range plan.items {
			v, err := evalExpr(ctx, item.expr)
			if err != nil {
				return nil, err
			}
			o[item.key] = v
		}
		out.rows = append(out.rows, o)
	}
	out.columns = inferColumns(out.rows, out.columns)
	return out, nil
}

func (ec *execCtx) pipeJoin(ds *dataset, p *sql.PipeJoin) (*dataset, error) {
	left := datasetToRelation(ds, "", "")
	right, err := ec.relationForTableExpr(p.Right)
	if err != nil {
		return nil, err
	}
	joined, err := ec.joinRelations(left, right, p.Type, p.On, p.Using, p.Natural)
	if err != nil {
		return nil, err
	}
	plan, err := buildProjection(joined, []sql.SelectItem{{Star: true}})
	if err != nil {
		return nil, err
	}
	out := &dataset{columns: plan.cols}
	for _, row := range joined.rows {
		o := make(map[string]any, len(plan.items))
		for _, item := range plan.items {
			o[item.key] = row[item.fieldKey]
		}
		out.rows = append(out.rows, o)
	}
	out.columns = inferColumns(out.rows, out.columns)
	return out, nil
}

func sortDataset(ec *execCtx, ds *dataset, items []sql.OrderItem) error {
	keys := make([][]any, len(ds.rows))
	for i, row := range ds.rows {
		ctx := ec.datasetCtx(ds, row)
		keys[i] = make([]any, len(items))
		for j, it := range items {
			v, err := evalExpr(ctx, it.Expr)
			if err != nil {
				return err
			}
			keys[i][j] = v
		}
	}
	idx := make([]int, len(ds.rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		return lessSortKeys(keys[idx[i]], keys[idx[j]], items)
	})
	rows := make([]map[string]any, len(ds.rows))
	for i, n := range idx {
		rows[i] = ds.rows[n]
	}
	ds.rows = rows
	return nil
}

func distinctDataset(ds *dataset) {
	seen := map[string]bool{}
	out := ds.rows[:0]
	for _, r := range ds.rows {
		key := canonicalValues(valuesInColumnOrder(r, ds.columns))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	ds.rows = out
}
