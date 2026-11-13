package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/anyquery/anyquery/sql"
)

type windowRow struct {
	first   map[string]any
	group   []map[string]any
	aliases map[string]any
}

type windowSource struct {
	rel  *relation
	rows []windowRow
}

func scanWindowSource(rel *relation) *windowSource {
	src := &windowSource{rel: rel, rows: make([]windowRow, len(rel.rows))}
	for i, row := range rel.rows {
		src.rows[i] = windowRow{first: row}
	}
	return src
}

func (ec *execCtx) windowCtx(src *windowSource, idx int) *evalCtx {
	r := src.rows[idx]
	return &evalCtx{rel: src.rel, row: r.first, group: r.group, aliases: r.aliases, outer: ec.outerEval, exec: ec}
}

func collectWindowCalls(items []sql.SelectItem, order []sql.OrderItem) ([]*sql.FuncCall, error) {
	seen := map[string]bool{}
	var out []*sql.FuncCall
	add := func(e sql.Expr) error {
		if e == nil {
			return nil
		}
		var walkErr error
		sql.WalkExpr(e, func(x sql.Expr) bool {
			f, ok := x.(*sql.FuncCall)
			if !ok || f.Window == nil {
				return true
			}
			if !isWindowFunction(f.Name) {
				walkErr = &Error{Code: "invalid_window", Message: fmt.Sprintf("unknown window function %s", strings.ToUpper(f.Name))}
				return false
			}
			key := f.String()
			if !seen[key] {
				seen[key] = true
				out = append(out, f)
			}
			return true
		})
		return walkErr
	}
	for _, item := range items {
		if err := add(item.Expr); err != nil {
			return nil, err
		}
	}
	for _, item := range order {
		if err := add(item.Expr); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func isWindowFunction(name string) bool {
	switch strings.ToLower(name) {
	case "row_number", "rank", "dense_rank", "percent_rank", "cume_dist", "ntile",
		"lag", "lead", "first_value", "last_value", "nth_value":
		return true
	}
	return sql.IsAggregate(name)
}

func (ec *execCtx) computeWindows(src *windowSource, calls []*sql.FuncCall) ([]map[string]any, error) {
	out := make([]map[string]any, len(src.rows))
	for i := range out {
		out[i] = map[string]any{}
	}
	for _, f := range calls {
		w := f.Window
		partitions := map[string][]int{}
		var partOrder []string
		for i := range src.rows {
			ctx := ec.windowCtx(src, i)
			vals := make([]any, len(w.PartitionBy))
			for j, e := range w.PartitionBy {
				v, err := evalExpr(ctx, e)
				if err != nil {
					return nil, err
				}
				vals[j] = v
			}
			key := canonicalValues(vals)
			if _, ok := partitions[key]; !ok {
				partOrder = append(partOrder, key)
			}
			partitions[key] = append(partitions[key], i)
		}
		for _, pkey := range partOrder {
			idxs := partitions[pkey]
			orderKeys := make([][]any, len(idxs))
			if len(w.OrderBy) > 0 {
				for n, ri := range idxs {
					ctx := ec.windowCtx(src, ri)
					keys := make([]any, len(w.OrderBy))
					for j, o := range w.OrderBy {
						v, err := evalExpr(ctx, o.Expr)
						if err != nil {
							return nil, err
						}
						keys[j] = v
					}
					orderKeys[n] = keys
				}
				perm := make([]int, len(idxs))
				for i := range perm {
					perm[i] = i
				}
				sort.SliceStable(perm, func(i, j int) bool {
					return lessSortKeys(orderKeys[perm[i]], orderKeys[perm[j]], w.OrderBy)
				})
				sortedIdx := make([]int, len(idxs))
				sortedKeys := make([][]any, len(idxs))
				for i, p := range perm {
					sortedIdx[i] = idxs[p]
					sortedKeys[i] = orderKeys[p]
				}
				idxs, orderKeys = sortedIdx, sortedKeys
			}
			if err := ec.evalWindowPartition(src, f, idxs, orderKeys, out); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func (ec *execCtx) evalWindowPartition(src *windowSource, f *sql.FuncCall, idxs []int, orderKeys [][]any, out []map[string]any) error {
	name := strings.ToLower(f.Name)
	n := len(idxs)
	if n == 0 {
		return nil
	}
	rank, dense := 0, 0
	for pos, ri := range idxs {
		if pos == 0 || !sameOrderKeys(orderKeys[pos], orderKeys[pos-1]) {
			rank = pos + 1
			dense++
		}
		var value any
		var err error
		switch name {
		case "row_number":
			value = int64(pos + 1)
		case "rank":
			value = int64(rank)
		case "dense_rank":
			value = int64(dense)
		case "percent_rank":
			if n == 1 {
				value = float64(0)
			} else {
				value = float64(rank-1) / float64(n-1)
			}
		case "cume_dist":
			cume := pos + 1
			for cume < n && sameOrderKeys(orderKeys[cume], orderKeys[pos]) {
				cume++
			}
			value = float64(cume) / float64(n)
		case "ntile":
			buckets := 1
			if len(f.Args) > 0 {
				v, err := ec.windowEval(src, ri, f.Args[0])
				if err != nil {
					return err
				}
				if b, ok := toInt(v); ok && b > 0 {
					buckets = b
				}
			}
			if buckets > n {
				value = int64(pos + 1)
			} else {
				value = int64(pos*buckets/n + 1)
			}
		case "lag", "lead":
			if len(f.Args) == 0 {
				return errf("invalid_window", "%s requires an expression", strings.ToUpper(name))
			}
			offset := 1
			if len(f.Args) > 1 {
				v, err := ec.windowEval(src, ri, f.Args[1])
				if err != nil {
					return err
				}
				if o, ok := toInt(v); ok {
					offset = o
				}
			}
			target := pos - offset
			if name == "lead" {
				target = pos + offset
			}
			if target >= 0 && target < n {
				value, err = ec.windowEval(src, idxs[target], f.Args[0])
			} else if len(f.Args) > 2 {
				value, err = ec.windowEval(src, ri, f.Args[2])
			}
		case "first_value", "last_value", "nth_value":
			if len(f.Args) == 0 {
				return errf("invalid_window", "%s requires an expression", strings.ToUpper(name))
			}
			start, end := windowFrame(f.Window, pos, n, orderKeys)
			target := -1
			switch name {
			case "first_value":
				for j := start; j <= end && j < n; j++ {
					if !frameExcluded(f.Window, pos, j, orderKeys) {
						target = j
						break
					}
				}
			case "last_value":
				for j := end; j >= start && j >= 0; j-- {
					if !frameExcluded(f.Window, pos, j, orderKeys) {
						target = j
						break
					}
				}
			case "nth_value":
				k := 1
				if len(f.Args) > 1 {
					v, err := ec.windowEval(src, ri, f.Args[1])
					if err != nil {
						return err
					}
					if x, ok := toInt(v); ok {
						k = x
					}
				}
				remaining := k
				for j := start; j <= end && j < n; j++ {
					if frameExcluded(f.Window, pos, j, orderKeys) {
						continue
					}
					remaining--
					if remaining == 0 {
						target = j
						break
					}
				}
			}
			if target >= start && target <= end && target >= 0 && target < n {
				value, err = ec.windowEval(src, idxs[target], f.Args[0])
			}
		default:
			if !sql.IsAggregate(name) {
				return errf("invalid_window", "unsupported window function %s", strings.ToUpper(name))
			}
			start, end := windowFrame(f.Window, pos, n, orderKeys)
			value, err = ec.evalWindowAggregate(src, f, idxs, start, end, pos, orderKeys)
		}
		if err != nil {
			return err
		}
		out[ri][f.String()] = value
	}
	return nil
}

func (ec *execCtx) evalWindowAggregate(src *windowSource, f *sql.FuncCall, idxs []int, start, end, current int, orderKeys [][]any) (any, error) {
	name := strings.ToLower(f.Name)
	star := len(f.Args) == 1
	if star {
		_, star = f.Args[0].(*sql.StarExpr)
	}
	if star && name != "count" {
		return nil, errf("invalid_window", "%s(*) is not supported", strings.ToUpper(name))
	}
	if !star && len(f.Args) == 0 {
		return nil, errf("invalid_window", "%s requires an argument", strings.ToUpper(name))
	}
	values := make([]any, 0, end-start+1)
	sep := ","
	for pos := start; pos <= end && pos < len(idxs); pos++ {
		if pos < 0 || frameExcluded(f.Window, current, pos, orderKeys) {
			continue
		}
		ri := idxs[pos]
		ctx := ec.windowCtx(src, ri)
		if f.Filter != nil {
			fv, err := evalExpr(ctx, f.Filter)
			if err != nil {
				return nil, err
			}
			if !truthy(fv) {
				continue
			}
		}
		if star {
			values = append(values, int64(1))
			continue
		}
		v, err := evalExpr(ctx, f.Args[0])
		if err != nil {
			return nil, err
		}
		if v != nil {
			values = append(values, v)
		}
		if len(f.Args) > 1 {
			sv, err := evalExpr(ctx, f.Args[1])
			if err != nil {
				return nil, err
			}
			if sv != nil {
				sep = fmt.Sprint(sv)
			}
		}
	}
	if f.Distinct {
		seen := map[string]bool{}
		uniq := values[:0]
		for _, v := range values {
			key := canonicalValues([]any{v})
			if seen[key] {
				continue
			}
			seen[key] = true
			uniq = append(uniq, v)
		}
		values = uniq
	}
	switch name {
	case "count":
		return int64(len(values)), nil
	case "sum", "avg":
		var sum float64
		allInt := true
		for _, v := range values {
			fv, ok := toFloat(v)
			if !ok {
				return nil, errf("type_error", "%s expects numeric values, got %T", strings.ToUpper(name), v)
			}
			if !isInteger(v) {
				allInt = false
			}
			sum += fv
		}
		if name == "avg" {
			if len(values) == 0 {
				return nil, nil
			}
			return sum / float64(len(values)), nil
		}
		if allInt {
			return int64(sum), nil
		}
		return sum, nil
	case "min", "max":
		if len(values) == 0 {
			return nil, nil
		}
		best := values[0]
		for _, v := range values[1:] {
			c := compareValues(v, best)
			if (name == "min" && c < 0) || (name == "max" && c > 0) {
				best = v
			}
		}
		return best, nil
	case "array_agg":
		return values, nil
	case "group_concat", "string_agg":
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = fmt.Sprint(v)
		}
		return strings.Join(parts, sep), nil
	case "bool_and", "bool_or":
		if len(values) == 0 {
			return nil, nil
		}
		res := name == "bool_and"
		for _, v := range values {
			if name == "bool_and" {
				res = res && truthy(v)
			} else {
				res = res || truthy(v)
			}
		}
		return res, nil
	case "stddev", "stddev_pop", "stddev_samp", "variance", "var_pop", "var_samp":
		if len(values) == 0 {
			return nil, nil
		}
		nums := make([]float64, 0, len(values))
		for _, v := range values {
			fv, ok := toFloat(v)
			if !ok {
				return nil, errf("type_error", "%s expects numeric values", strings.ToUpper(name))
			}
			nums = append(nums, fv)
		}
		mean := 0.0
		for _, v := range nums {
			mean += v
		}
		mean /= float64(len(nums))
		var ss float64
		for _, v := range nums {
			ss += (v - mean) * (v - mean)
		}
		denom := float64(len(nums))
		if strings.Contains(name, "samp") && len(nums) > 1 {
			denom = float64(len(nums) - 1)
		}
		variance := ss / denom
		if strings.HasPrefix(name, "stddev") {
			return math.Sqrt(variance), nil
		}
		return variance, nil
	default:
		return nil, errf("invalid_window", "unsupported window aggregate %s", strings.ToUpper(name))
	}
}

func (ec *execCtx) windowEval(src *windowSource, rowIdx int, e sql.Expr) (any, error) {
	return evalExpr(ec.windowCtx(src, rowIdx), e)
}

func sameOrderKeys(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !valuesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// windowFrame returns an inclusive [start,end] range. ROWS frames use row
// positions. RANGE frames use peer groups for UNBOUNDED/CURRENT bounds and
// value offsets for a single numeric ORDER BY key; other RANGE offsets fall
// back to row positions.
func windowFrame(w *sql.WindowSpec, pos, n int, orderKeys [][]any) (int, int) {
	if n == 0 {
		return 0, -1
	}
	if pos < 0 {
		pos = 0
	}
	if pos >= n {
		pos = n - 1
	}
	if w == nil || w.FrameKind == "" {
		if w != nil && len(w.OrderBy) > 0 {
			return 0, peerEnd(pos, orderKeys)
		}
		return 0, n - 1
	}
	if w.FrameKind == "RANGE" {
		start := rangeFrameBound(w.FrameStart, w.FrameStartOffset, pos, n, orderKeys, w.OrderBy, true)
		end := rangeFrameBound(w.FrameEnd, w.FrameEndOffset, pos, n, orderKeys, w.OrderBy, false)
		if start < 0 {
			start = 0
		}
		if end >= n {
			end = n - 1
		}
		return start, end
	}
	if w.FrameKind == "GROUPS" {
		start := groupFrameBound(w.FrameStart, w.FrameStartOffset, pos, n, orderKeys, true)
		end := groupFrameBound(w.FrameEnd, w.FrameEndOffset, pos, n, orderKeys, false)
		if start < 0 {
			start = 0
		}
		if end >= n {
			end = n - 1
		}
		return start, end
	}
	start := frameBound(w.FrameStart, w.FrameStartOffset, pos, n, true)
	end := frameBound(w.FrameEnd, w.FrameEndOffset, pos, n, false)
	if start < 0 {
		start = 0
	}
	if end >= n {
		end = n - 1
	}
	return start, end
}

func groupFrameBound(kind string, offset, pos, n int, orderKeys [][]any, start bool) int {
	switch kind {
	case "unbounded_preceding":
		return 0
	case "unbounded_following":
		return n - 1
	case "current_row":
		if start {
			return peerStart(pos, orderKeys)
		}
		return peerEnd(pos, orderKeys)
	case "preceding":
		g := pos
		for i := 0; i < offset; i++ {
			first := peerStart(g, orderKeys)
			if first == 0 {
				g = 0
				break
			}
			g = peerStart(first-1, orderKeys)
		}
		if start {
			return g
		}
		return peerEnd(g, orderKeys)
	case "following":
		g := pos
		for i := 0; i < offset; i++ {
			last := peerEnd(g, orderKeys)
			if last >= n-1 {
				g = n - 1
				break
			}
			g = peerEnd(last+1, orderKeys)
		}
		if start {
			return peerStart(g, orderKeys)
		}
		return g
	}
	if start {
		return 0
	}
	return n - 1
}

func frameExcluded(w *sql.WindowSpec, current, pos int, orderKeys [][]any) bool {
	if w == nil || w.FrameExclusion == "" || w.FrameExclusion == "no_others" {
		return false
	}
	samePeer := pos == current
	if current >= 0 && current < len(orderKeys) && pos >= 0 && pos < len(orderKeys) {
		samePeer = sameOrderKeys(orderKeys[current], orderKeys[pos])
	}
	switch w.FrameExclusion {
	case "current_row":
		return pos == current
	case "group":
		return samePeer
	case "ties":
		return samePeer && pos != current
	}
	return false
}

func peerStart(pos int, orderKeys [][]any) int {
	if len(orderKeys) == 0 {
		return pos
	}
	start := pos
	for start > 0 && sameOrderKeys(orderKeys[start], orderKeys[start-1]) {
		start--
	}
	return start
}

func peerEnd(pos int, orderKeys [][]any) int {
	if len(orderKeys) == 0 {
		return pos
	}
	end := pos
	for end+1 < len(orderKeys) && sameOrderKeys(orderKeys[end], orderKeys[end+1]) {
		end++
	}
	return end
}

func rangeFrameBound(kind string, offset, pos, n int, orderKeys [][]any, order []sql.OrderItem, start bool) int {
	switch kind {
	case "unbounded_preceding":
		return 0
	case "unbounded_following":
		return n - 1
	case "current_row":
		if start {
			return peerStart(pos, orderKeys)
		}
		return peerEnd(pos, orderKeys)
	case "preceding", "following":
		if len(orderKeys) > pos && len(orderKeys[pos]) == 1 && len(order) == 1 {
			cur, ok := toFloat(orderKeys[pos][0])
			if ok {
				target := cur
				desc := order[0].Desc
				if kind == "preceding" {
					if desc {
						target = cur + float64(offset)
					} else {
						target = cur - float64(offset)
					}
				} else {
					if desc {
						target = cur - float64(offset)
					} else {
						target = cur + float64(offset)
					}
				}
				if start {
					for i := 0; i < n; i++ {
						v, ok := toFloat(orderKeys[i][0])
						if !ok {
							break
						}
						if (!desc && v >= target) || (desc && v <= target) {
							return i
						}
					}
					return n - 1
				}
				for i := n - 1; i >= 0; i-- {
					v, ok := toFloat(orderKeys[i][0])
					if !ok {
						break
					}
					if (!desc && v <= target) || (desc && v >= target) {
						return i
					}
				}
				return 0
			}
		}
		// Fallback for non-numeric or multi-key RANGE offsets.
		if kind == "preceding" {
			return pos - offset
		}
		return pos + offset
	}
	if start {
		return 0
	}
	return n - 1
}

func frameBound(kind string, offset, pos, n int, start bool) int {
	switch kind {
	case "unbounded_preceding":
		return 0
	case "unbounded_following":
		return n - 1
	case "current_row":
		return pos
	case "preceding":
		return pos - offset
	case "following":
		return pos + offset
	}
	if start {
		return 0
	}
	return n - 1
}
