package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/anyquery/anyquery/sql"
)

type mrRow struct {
	row map[string]any
}

type mrState struct {
	pos      int
	bindings map[string][]int
}

func copyBindings(in map[string][]int) map[string][]int {
	out := make(map[string][]int, len(in))
	for k, v := range in {
		out[k] = append([]int(nil), v...)
	}
	return out
}

func mergeBindings(a, b map[string][]int) map[string][]int {
	out := copyBindings(a)
	for k, v := range b {
		out[k] = append(out[k], v...)
	}
	return out
}

func (ec *execCtx) relationForMatchRecognize(m *sql.MatchRecognize) (*relation, error) {
	input, err := ec.relationForTableExpr(m.Input)
	if err != nil {
		return nil, err
	}
	defs := map[string]sql.Expr{}
	for _, d := range m.Define {
		defs[strings.ToLower(d.Name)] = d.Expr
	}
	subsets := map[string][]string{}
	for _, sub := range m.Subsets {
		subsets[strings.ToLower(sub.Name)] = sub.Vars
	}
	type partition struct {
		rows []mrRow
		keys [][]any
	}
	parts := map[string]*partition{}
	var order []string
	for _, row := range input.rows {
		ctx := &evalCtx{rel: input, row: row, outer: ec.outerEval, exec: ec}
		vals := make([]any, len(m.PartitionBy))
		for i, e := range m.PartitionBy {
			vals[i], err = evalExpr(ctx, e)
			if err != nil {
				return nil, err
			}
		}
		key := canonicalValues(vals)
		p := parts[key]
		if p == nil {
			p = &partition{}
			parts[key] = p
			order = append(order, key)
		}
		p.rows = append(p.rows, mrRow{row: row})
	}
	cols := append([]ResultColumn(nil), input.fieldsToColumns()...)
	for _, measure := range m.Measures {
		cols = append(cols, ResultColumn{Name: measure.Alias, Type: "string"})
	}
	ds := &dataset{columns: dedupeResultColumns(cols)}
	for _, key := range order {
		matchNo := 0
		p := parts[key]
		if len(m.OrderBy) > 0 {
			keys := make([][]any, len(p.rows))
			for i, r := range p.rows {
				ctx := &evalCtx{rel: input, row: r.row, outer: ec.outerEval, exec: ec}
				ks := make([]any, len(m.OrderBy))
				for j, o := range m.OrderBy {
					ks[j], err = evalExpr(ctx, o.Expr)
					if err != nil {
						return nil, err
					}
				}
				keys[i] = ks
			}
			idx := make([]int, len(p.rows))
			for i := range idx {
				idx[i] = i
			}
			sort.SliceStable(idx, func(i, j int) bool {
				return lessSortKeys(keys[idx[i]], keys[idx[j]], m.OrderBy)
			})
			rows := make([]mrRow, len(p.rows))
			sortedKeys := make([][]any, len(p.rows))
			for i, at := range idx {
				rows[i] = p.rows[at]
				sortedKeys[i] = keys[at]
			}
			p.rows, p.keys = rows, sortedKeys
		} else {
			p.keys = make([][]any, len(p.rows))
		}
		matched := make([]bool, len(p.rows))
		for start := 0; start < len(p.rows); {
			states, err := matchPattern(m.Pattern, start, p.rows, input, defs, ec)
			if err != nil {
				return nil, err
			}
			if len(states) == 0 {
				start++
				continue
			}
			state := states[0]
			if state.pos <= start {
				return nil, errf("match_recognize_error", "pattern matched an empty row sequence")
			}
			matchNo++
			end := state.pos
			for i := start; i < end; i++ {
				matched[i] = true
			}
			if m.RowsMode == "all" {
				for i := start; i < end; i++ {
					row := mrOutputRow(input, p.rows[i].row)
					if err := ec.addMRMeasures(row, m.Measures, p.rows, start, end, i, false, state.bindings, matchNo, input, subsets); err != nil {
						return nil, err
					}
					ds.rows = append(ds.rows, row)
				}
			} else {
				row := mrOutputRow(input, p.rows[start].row)
				if err := ec.addMRMeasures(row, m.Measures, p.rows, start, end, start, true, state.bindings, matchNo, input, subsets); err != nil {
					return nil, err
				}
				ds.rows = append(ds.rows, row)
			}
			switch {
			case m.AfterSkip == "next_row":
				start++
			case strings.HasPrefix(m.AfterSkip, "first:") || strings.HasPrefix(m.AfterSkip, "last:"):
				varName := m.AfterSkip[strings.IndexByte(m.AfterSkip, ':')+1:]
				idx := -1
				for _, at := range state.bindings[strings.ToLower(varName)] {
					if at > start && (idx < 0 || (strings.HasPrefix(m.AfterSkip, "first:") && at < idx) || (strings.HasPrefix(m.AfterSkip, "last:") && at > idx)) {
						idx = at
					}
				}
				if idx >= 0 {
					start = idx
				} else {
					start = end
				}
			default:
				start = end
			}
		}
		if m.Unmatched {
			for i, r := range p.rows {
				if matched[i] {
					continue
				}
				ds.rows = append(ds.rows, mrOutputRow(input, r.row))
			}
		}
	}
	alias := m.Alias
	if alias == "" {
		alias = "match_recognize"
	}
	return datasetToRelation(ds, alias, alias), nil
}

func (r *relation) fieldsToColumns() []ResultColumn {
	out := make([]ResultColumn, len(r.fields))
	for i, f := range r.fields {
		out[i] = ResultColumn{Name: f.Name, Type: f.Type}
	}
	return out
}

// mrOutputRow projects an input row keyed by qualified relation keys into the
// output column names. MATCH_RECOGNIZE must preserve input columns, not only
// MEASURES aliases.
func mrOutputRow(input *relation, in map[string]any) map[string]any {
	out := make(map[string]any, len(input.fields))
	for _, f := range input.fields {
		out[strings.ToLower(f.Name)] = in[f.Key]
	}
	return out
}

func matchPattern(p *sql.MatchPattern, pos int, rows []mrRow, rel *relation, defs map[string]sql.Expr, ec *execCtx) ([]mrState, error) {
	if p == nil {
		return []mrState{{pos: pos, bindings: map[string][]int{}}}, nil
	}
	if p.Quantifier != "" {
		min, max := 0, -1
		switch p.Quantifier {
		case "+":
			min = 1
		case "?":
			max = 1
		default:
			if strings.HasPrefix(p.Quantifier, "{") && strings.HasSuffix(p.Quantifier, "}") {
				body := strings.TrimSuffix(strings.TrimPrefix(p.Quantifier, "{"), "}")
				parts := strings.SplitN(body, ",", 2)
				if n, err := strconv.Atoi(parts[0]); err == nil {
					min = n
				}
				if len(parts) == 1 {
					max = min
				} else if parts[1] != "" {
					if n, err := strconv.Atoi(parts[1]); err == nil {
						max = n
					}
				}
			}
		}
		return matchQuantified(p, min, max, pos, rows, rel, defs, ec)
	}
	return matchBase(p, pos, rows, rel, defs, ec)
}

func matchBase(p *sql.MatchPattern, pos int, rows []mrRow, rel *relation, defs map[string]sql.Expr, ec *execCtx) ([]mrState, error) {
	switch p.Kind {
	case "var":
		if pos >= len(rows) {
			return nil, nil
		}
		pred, ok := defs[strings.ToLower(p.Var)]
		if !ok {
			return nil, errf("match_recognize_error", "pattern variable %s has no DEFINE predicate", p.Var)
		}
		ctx := &evalCtx{rel: rel, row: rows[pos].row, outer: ec.outerEval, exec: ec}
		v, err := evalExpr(ctx, pred)
		if err != nil {
			return nil, err
		}
		if !truthy(v) {
			return nil, nil
		}
		return []mrState{{pos: pos + 1, bindings: map[string][]int{strings.ToLower(p.Var): {pos}}}}, nil
	case "seq":
		states := []mrState{{pos: pos, bindings: map[string][]int{}}}
		for _, child := range p.Children {
			var next []mrState
			for _, st := range states {
				sub, err := matchPattern(child, st.pos, rows, rel, defs, ec)
				if err != nil {
					return nil, err
				}
				for _, s := range sub {
					next = append(next, mrState{pos: s.pos, bindings: mergeBindings(st.bindings, s.bindings)})
				}
			}
			states = next
			if len(states) == 0 {
				return nil, nil
			}
		}
		return states, nil
	case "group":
		if len(p.Children) == 0 {
			return nil, errf("match_recognize_error", "empty MATCH_RECOGNIZE group")
		}
		return matchPattern(p.Children[0], pos, rows, rel, defs, ec)
	case "permute":
		var out []mrState
		var walk func(order []string, at int, bindings map[string][]int) error
		walk = func(order []string, at int, bindings map[string][]int) error {
			if len(order) == 0 {
				out = append(out, mrState{pos: at, bindings: bindings})
				return nil
			}
			for i, v := range order {
				term := &sql.MatchPattern{Kind: "var", Var: v}
				sub, err := matchBase(term, at, rows, rel, defs, ec)
				if err != nil {
					return err
				}
				next := append(append([]string{}, order[:i]...), order[i+1:]...)
				for _, st := range sub {
					if err := walk(next, st.pos, mergeBindings(bindings, st.bindings)); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := walk(append([]string{}, p.Vars...), pos, map[string][]int{}); err != nil {
			return nil, err
		}
		return out, nil
	case "alt":
		var out []mrState
		for _, child := range p.Children {
			sub, err := matchPattern(child, pos, rows, rel, defs, ec)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		}
		return out, nil
	}
	return nil, errf("match_recognize_error", "unsupported pattern node %q", p.Kind)
}

func matchQuantified(p *sql.MatchPattern, min, max, pos int, rows []mrRow, rel *relation, defs map[string]sql.Expr, ec *execCtx) ([]mrState, error) {
	base := *p
	base.Quantifier = ""
	base.Lazy = false
	var results [][]mrState
	var rec func(at, count int, states []mrState) error
	rec = func(at, count int, states []mrState) error {
		if count >= min {
			results = append(results, states)
		}
		if max >= 0 && count >= max {
			return nil
		}
		var next []mrState
		for _, st := range states {
			sub, err := matchBase(&base, st.pos, rows, rel, defs, ec)
			if err != nil {
				return err
			}
			for _, s := range sub {
				if s.pos == st.pos {
					return errf("match_recognize_error", "pattern repetition matched empty")
				}
				next = append(next, mrState{pos: s.pos, bindings: mergeBindings(st.bindings, s.bindings)})
			}
		}
		if len(next) == 0 {
			return nil
		}
		return rec(next[0].pos, count+1, next)
	}
	if err := rec(pos, 0, []mrState{{pos: pos, bindings: map[string][]int{}}}); err != nil {
		return nil, err
	}
	if p.Lazy {
		for i := 0; i < len(results); i++ {
			if len(results[i]) > 0 {
				return results[i], nil
			}
		}
		_ = max
		var out []mrState
		for _, r := range results {
			out = append(out, r...)
		}
		return out, nil
	}
	var out []mrState
	for i := len(results) - 1; i >= 0; i-- {
		out = append(out, results[i]...)
	}
	return out, nil
}

func (ec *execCtx) addMRMeasures(row map[string]any, measures []sql.MatchMeasure, rows []mrRow, start, end, current int, single bool, bindings map[string][]int, matchNo int, rel *relation, subsets map[string][]string) error {
	for _, measure := range measures {
		at := current
		if measure.Mode == "final" && end > start {
			at = end - 1
		}
		if single && measure.Mode == "running" && end > start {
			at = end - 1
		}
		v, err := ec.evalMRMeasure(measure.Expr, rows, start, end, at, bindings, matchNo, rel, measure.Mode, subsets)
		if err != nil {
			return err
		}
		row[strings.ToLower(measure.Alias)] = v
	}
	return nil
}

func (ec *execCtx) evalMRMeasure(e sql.Expr, rows []mrRow, start, end, current int, bindings map[string][]int, matchNo int, rel *relation, mode string, subsets map[string][]string) (any, error) {
	if f, ok := e.(*sql.FuncCall); ok {
		name := strings.ToLower(f.Name)
		switch name {
		case "match_number":
			return int64(matchNo), nil
		case "classifier":
			if current >= 0 && current < len(rows) {
				for varName, indices := range bindings {
					for _, idx := range indices {
						if idx == current {
							return varName, nil
						}
					}
				}
			}
			for varName := range bindings {
				return varName, nil
			}
			return nil, nil
		case "prev":
			if len(f.Args) != 1 {
				return nil, errf("match_recognize_error", "PREV expects one argument")
			}
			arg := f.Args[0]
			at := current - 1
			if ref, ok := arg.(*sql.ColumnRef); ok && len(ref.Parts) > 1 {
				if idxs := bindings[strings.ToLower(ref.Parts[0])]; len(idxs) > 0 {
					at = -1
					for _, idx := range idxs {
						if idx < current && idx > at {
							at = idx
						}
					}
					arg = &sql.ColumnRef{Parts: ref.Parts[1:]}
				}
			}
			if at < start || at >= end {
				return nil, nil
			}
			ctx := &evalCtx{rel: rel, row: rows[at].row, outer: ec.outerEval, exec: ec}
			return evalExpr(ctx, arg)
		case "next":
			if len(f.Args) != 1 {
				return nil, errf("match_recognize_error", "NEXT expects one argument")
			}
			arg := f.Args[0]
			at := current + 1
			if ref, ok := arg.(*sql.ColumnRef); ok && len(ref.Parts) > 1 {
				if idxs := bindings[strings.ToLower(ref.Parts[0])]; len(idxs) > 0 {
					at = -1
					for _, idx := range idxs {
						if idx > current && (at < 0 || idx < at) {
							at = idx
						}
					}
					arg = &sql.ColumnRef{Parts: ref.Parts[1:]}
				}
			}
			if at < start || at >= end {
				return nil, nil
			}
			ctx := &evalCtx{rel: rel, row: rows[at].row, outer: ec.outerEval, exec: ec}
			return evalExpr(ctx, arg)
		case "first", "last":
			if len(f.Args) != 1 {
				return nil, errf("match_recognize_error", "%s expects one argument", strings.ToUpper(name))
			}
			at := start
			if name == "last" {
				at = end - 1
			}
			arg := f.Args[0]
			if ref, ok := arg.(*sql.ColumnRef); ok && len(ref.Parts) > 1 {
				varName := strings.ToLower(ref.Parts[0])
				if idxs := bindings[varName]; len(idxs) > 0 {
					at = idxs[0]
					if name == "last" {
						at = idxs[len(idxs)-1]
					}
					arg = &sql.ColumnRef{Parts: ref.Parts[1:]}
				}
			}
			if at < start || at >= end {
				return nil, nil
			}
			ctx := &evalCtx{rel: rel, row: rows[at].row, outer: ec.outerEval, exec: ec}
			return evalExpr(ctx, arg)
		}
		if sql.IsAggregate(name) {
			groupRows := rows[start:end]
			if mode == "running" && current >= start {
				groupRows = rows[start : current+1]
			}
			if len(f.Args) == 1 {
				if star, ok := f.Args[0].(*sql.StarExpr); ok && star.Table != "" {
					vars := subsets[strings.ToLower(star.Table)]
					if vars != nil {
						seen := map[int]bool{}
						var selected []mrRow
						for _, v := range vars {
							for _, idx := range bindings[strings.ToLower(v)] {
								if idx >= start && idx < end && !seen[idx] {
									seen[idx] = true
									selected = append(selected, rows[idx])
								}
							}
						}
						groupRows = selected
					}
				}
			}
			group := make([]map[string]any, len(groupRows))
			for i, r := range groupRows {
				group[i] = r.row
			}
			return evalAggregate(&evalCtx{rel: rel, group: group, outer: ec.outerEval, exec: ec}, f)
		}
	}
	at := current
	if at < start || at >= end {
		at = start
	}
	ctx := &evalCtx{rel: rel, row: rows[at].row, outer: ec.outerEval, exec: ec}
	return evalExpr(ctx, e)
}

var _ = fmt.Sprint
