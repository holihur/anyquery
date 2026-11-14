package engine

import (
	"github.com/anyquery/anyquery/sql"
)

// costBasedJoinTree reorders a narrow but common shape: a tree of INNER joins
// over base table references, with no SELECT * (so column order is explicit).
// It materializes the leaves first, then greedily joins the smallest relation
// that has an applicable predicate. Outer joins, USING/NATURAL, LATERAL and
// other table expressions keep their written order.
func (ec *execCtx) costBasedJoinTree(from sql.TableExpr, projections []sql.SelectItem) (*relation, bool, error) {
	for _, item := range projections {
		if item.Star {
			return nil, false, nil
		}
	}
	var leaves []*sql.TableRef
	var conditions []sql.Expr
	var walk func(sql.TableExpr) bool
	walk = func(te sql.TableExpr) bool {
		switch t := te.(type) {
		case *sql.TableRef:
			leaves = append(leaves, t)
			return true
		case *sql.JoinExpr:
			if t.Type != sql.JoinInner || t.Natural || len(t.Using) > 0 || t.On == nil {
				return false
			}
			if !walk(t.Left) || !walk(t.Right) {
				return false
			}
			conditions = append(conditions, splitAnd(t.On)...)
			return true
		default:
			return false
		}
	}
	if !walk(from) || len(leaves) < 3 {
		return nil, false, nil
	}
	rels := make([]*relation, len(leaves))
	for i, leaf := range leaves {
		r, err := ec.relationForTableExpr(leaf)
		if err != nil {
			return nil, false, err
		}
		rels[i] = r
	}
	used := make([]bool, len(rels))
	// Start with the smallest leaf.
	start := 0
	for i := 1; i < len(rels); i++ {
		if len(rels[i].rows) < len(rels[start].rows) {
			start = i
		}
	}
	used[start] = true
	current := rels[start]
	remaining := len(rels) - 1
	for remaining > 0 {
		best := -1
		bestConditions := []sql.Expr(nil)
		bestRows := 0
		for i, r := range rels {
			if used[i] {
				continue
			}
			probe := &relation{fields: append(append([]field{}, current.fields...), r.fields...)}
			var applicable []sql.Expr
			for _, cond := range conditions {
				if exprResolves(cond, probe) {
					applicable = append(applicable, cond)
				}
			}
			if best < 0 || len(applicable) > len(bestConditions) || (len(applicable) == len(bestConditions) && len(r.rows) < bestRows) {
				best, bestConditions, bestRows = i, applicable, len(r.rows)
			}
		}
		var on sql.Expr
		for i, cond := range bestConditions {
			if i == 0 {
				on = cond
			} else {
				on = &sql.BinaryExpr{Op: "AND", Left: on, Right: cond}
			}
		}
		joined, err := ec.joinRelations(current, rels[best], sql.JoinInner, on, nil, false)
		if err != nil {
			return nil, false, err
		}
		current = joined
		used[best] = true
		remaining--
		// Remove predicates applied with this join.
		applied := map[string]bool{}
		for _, cond := range bestConditions {
			applied[cond.String()] = true
		}
		if len(applied) > 0 {
			kept := conditions[:0]
			for _, cond := range conditions {
				if !applied[cond.String()] {
					kept = append(kept, cond)
				}
			}
			conditions = kept
		}
	}
	if len(conditions) > 0 {
		filtered := current.rows[:0]
		for _, row := range current.rows {
			ctx := &evalCtx{rel: current, row: row, outer: ec.outerEval, exec: ec}
			ok := true
			for _, cond := range conditions {
				v, err := evalExpr(ctx, cond)
				if err != nil {
					return nil, false, err
				}
				if !truthy(v) {
					ok = false
					break
				}
			}
			if ok {
				filtered = append(filtered, row)
			}
		}
		current.rows = filtered
	}
	return current, true, nil
}

func exprResolves(e sql.Expr, rel *relation) bool {
	ok := true
	sql.WalkExpr(e, func(x sql.Expr) bool {
		if ref, isRef := x.(*sql.ColumnRef); isRef {
			if _, _, err := rel.resolve(ref); err != nil {
				ok = false
				return false
			}
		}
		return true
	})
	return ok
}
