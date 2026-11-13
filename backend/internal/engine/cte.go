package engine

import (
	"fmt"
	"strings"

	"github.com/anyquery/anyquery/sql"
)

const (
	recursiveCTEMaxRows       = 10_000
	recursiveCTEMaxIterations = 100
)

// executeRecursiveCTE evaluates the SQL-standard recursive form:
//
//	WITH RECURSIVE t(n) AS (
//	  anchor
//	  UNION [ALL]
//	  recursive
//	)
//
// The recursive term sees only the rows produced by the previous iteration.
// UNION de-duplicates globally; UNION ALL has an iteration/row guard to avoid
// unbounded cycles.
func (ec *execCtx) executeRecursiveCTE(cte *sql.CTE) (*dataset, error) {
	if cte.Query == nil {
		return nil, errf("cte_error", "CTE %q has no query", cte.Name)
	}
	q := cte.Query
	if len(q.SetOps) == 0 {
		// A WITH RECURSIVE entry that does not reference itself is just a CTE.
		return ec.executeSelect(q)
	}
	if len(q.SetOps) > 1 {
		return nil, errf("cte_error", "recursive CTE %q must use a single UNION term", cte.Name)
	}
	op := q.SetOps[0]
	if !strings.EqualFold(op.Op, "UNION") {
		return nil, errf("cte_error", "recursive CTE %q must use UNION or UNION ALL", cte.Name)
	}

	anchor := *q
	anchor.SetOps = nil
	anchor.Pipes = nil
	anchor.OrderBy = nil
	anchor.Limit = nil
	anchor.LimitExpr = nil
	anchor.Offset = nil
	anchor.OffsetExpr = nil
	anchor.FetchWithTies = false
	anchorDS, err := ec.executeSelect(&anchor)
	if err != nil {
		return nil, err
	}
	columns := make([]ResultColumn, len(anchorDS.columns))
	copy(columns, anchorDS.columns)
	if len(cte.Columns) > 0 {
		if len(cte.Columns) != len(columns) {
			return nil, errf("cte_error", "CTE %q declares %d columns but returns %d", cte.Name, len(cte.Columns), len(columns))
		}
		for i := range columns {
			columns[i].Name = cte.Columns[i]
		}
	}

	seen := map[string]bool{}
	var accRows, deltaRows []map[string]any
	for _, row := range anchorDS.rows {
		mapped := remapCTERow(row, anchorDS.columns, columns)
		key := canonicalValues(valuesInColumnOrder(mapped, columns))
		if !op.All {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		accRows = append(accRows, mapped)
		deltaRows = append(deltaRows, mapped)
		if len(accRows) > recursiveCTEMaxRows {
			return nil, errf("recursion_limit", "recursive CTE %q exceeded %d rows", cte.Name, recursiveCTEMaxRows)
		}
	}

	key := strings.ToLower(cte.Name)
	old, hadOld := ec.ctes[key]
	defer func() {
		if hadOld {
			ec.ctes[key] = old
		} else {
			delete(ec.ctes, key)
		}
	}()

	delta := &dataset{columns: columns, rows: deltaRows}
	for iteration := 0; ; iteration++ {
		if err := ec.ctx.Err(); err != nil {
			return nil, err
		}
		if iteration >= recursiveCTEMaxIterations {
			return nil, errf("recursion_limit", "recursive CTE %q exceeded %d iterations", cte.Name, recursiveCTEMaxIterations)
		}
		ec.ctes[key] = delta
		next, err := ec.executeSelect(op.Right)
		if err != nil {
			return nil, err
		}
		if len(next.columns) != len(columns) {
			return nil, errf("cte_error", "recursive term of %q returns %d columns, expected %d", cte.Name, len(next.columns), len(columns))
		}
		var newDelta []map[string]any
		for _, row := range next.rows {
			mapped := remapCTERow(row, next.columns, columns)
			rk := canonicalValues(valuesInColumnOrder(mapped, columns))
			if !op.All {
				if seen[rk] {
					continue
				}
				seen[rk] = true
			}
			if len(accRows) >= recursiveCTEMaxRows {
				return nil, errf("recursion_limit", "recursive CTE %q exceeded %d rows", cte.Name, recursiveCTEMaxRows)
			}
			accRows = append(accRows, mapped)
			newDelta = append(newDelta, mapped)
		}
		if len(newDelta) == 0 {
			break
		}
		delta = &dataset{columns: columns, rows: newDelta}
	}
	result := &dataset{columns: columns, rows: accRows}
	if len(q.OrderBy) > 0 {
		if err := sortDataset(ec, result, q.OrderBy); err != nil {
			return nil, err
		}
	}
	offset, hasOffset, err := ec.resolveRowCount(q.Offset, q.OffsetExpr)
	if err != nil {
		return nil, err
	}
	limit, hasLimit, err := ec.resolveRowCount(q.Limit, q.LimitExpr)
	if err != nil {
		return nil, err
	}
	if hasOffset && offset > 0 {
		if offset >= len(result.rows) {
			result.rows = nil
		} else {
			result.rows = result.rows[offset:]
		}
	}
	if hasLimit && q.FetchWithTies {
		return nil, errf("invalid_fetch", "FETCH ... WITH TIES is not supported in recursive CTE output")
	}
	if hasLimit && limit >= 0 && len(result.rows) > limit {
		result.rows = result.rows[:limit]
	}
	ec.ctes[key] = result
	ec.stage(fmt.Sprintf("recursive-cte:%s", cte.Name))
	return result, nil
}

func remapCTERow(row map[string]any, src, dst []ResultColumn) map[string]any {
	out := make(map[string]any, len(dst))
	for i, col := range dst {
		if i < len(src) {
			out[strings.ToLower(col.Name)] = row[strings.ToLower(src[i].Name)]
		} else {
			out[strings.ToLower(col.Name)] = nil
		}
	}
	return out
}
