package engine

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/anyquery/anyquery/sql"
)

type evalCtx struct {
	rel      *relation
	row      map[string]any
	aliases  map[string]any
	group    []map[string]any
	outer    *evalCtx
	exec     *execCtx
	windows  map[string]any
	omit     map[string]bool
	grouping map[string]bool
}

func (c *evalCtx) resolve(ref *sql.ColumnRef) (any, error) {
	col := strings.ToLower(ref.Parts[len(ref.Parts)-1])
	if len(ref.Parts) == 1 && c.aliases != nil {
		if v, ok := c.aliases[col]; ok {
			return v, nil
		}
	}
	if c.rel != nil {
		_, f, err := c.rel.resolve(ref)
		if err == nil {
			if c.row == nil {
				return nil, nil
			}
			return c.row[f.Key], nil
		}
		// An alias may still resolve an unqualified reference in ORDER BY.
		if len(ref.Parts) == 1 && c.aliases != nil {
			if v, ok := c.aliases[col]; ok {
				return v, nil
			}
		}
		if c.outer != nil {
			if v, outerErr := c.outer.resolve(ref); outerErr == nil {
				return v, nil
			}
		}
		return nil, err
	}
	if c.aliases != nil {
		if len(ref.Parts) == 1 {
			if v, ok := c.aliases[col]; ok {
				return v, nil
			}
		}
		qualifier := strings.ToLower(strings.Join(ref.Parts[:len(ref.Parts)-1], "."))
		if v, ok := c.aliases[qualifier+"."+col]; ok {
			return v, nil
		}
	}
	if c.outer != nil {
		if v, err := c.outer.resolve(ref); err == nil {
			return v, nil
		}
	}
	return nil, &Error{Code: "column_not_found", Message: fmt.Sprintf("column %q not found", ref.String())}
}

func evalExpr(ctx *evalCtx, e sql.Expr) (any, error) {
	if ctx.omit != nil && ctx.omit[e.String()] {
		return nil, nil
	}
	switch x := e.(type) {
	case *sql.Literal:
		return x.Value, nil
	case *sql.RowExpr:
		values := make(rowValue, len(x.Values))
		for i, e := range x.Values {
			v, err := evalExpr(ctx, e)
			if err != nil {
				return nil, err
			}
			values[i] = v
		}
		return values, nil
	case *sql.IntervalExpr:
		return parseInterval(x.Value, x.Qualifier)
	case *sql.Parameter:
		if ctx.exec == nil {
			return nil, errf("parameter_error", "bind parameters are not available")
		}
		if x.Index < 1 || x.Index > len(ctx.exec.params) {
			return nil, errf("parameter_error", "missing value for parameter %d", x.Index)
		}
		return ctx.exec.params[x.Index-1], nil
	case *sql.ColumnRef:
		return ctx.resolve(x)
	case *sql.StarExpr:
		return nil, errf("invalid_expression", "* is only valid in a projection or count(*)")
	case *sql.UnaryExpr:
		v, err := evalExpr(ctx, x.Expr)
		if err != nil {
			return nil, err
		}
		switch strings.ToUpper(x.Op) {
		case "NOT":
			if v == nil {
				return nil, nil
			}
			return !truthy(v), nil
		case "-":
			if v == nil {
				return nil, nil
			}
			if iv, ok := v.(intervalValue); ok {
				return iv.neg(), nil
			}
			f, ok := toFloat(v)
			if !ok {
				return nil, errf("type_error", "cannot negate %T", v)
			}
			return -f, nil
		case "+":
			return v, nil
		case "~":
			i, ok := toInt(v)
			if !ok {
				return nil, errf("type_error", "cannot bitwise-negate %T", v)
			}
			return ^i, nil
		}
		return nil, errf("unsupported", "unsupported unary operator %s", x.Op)
	case *sql.BinaryExpr:
		return evalBinary(ctx, x)
	case *sql.CompareExpr:
		return evalCompare(ctx, x)
	case *sql.QuantifiedExpr:
		return evalQuantified(ctx, x)
	case *sql.InExpr:
		left, err := evalExpr(ctx, x.Left)
		if err != nil {
			return nil, err
		}
		if x.Subquery != nil {
			return evalInSubquery(ctx, x, left)
		}
		matched := false
		sawNull := false
		for _, le := range x.List {
			v, err := evalExpr(ctx, le)
			if err != nil {
				return nil, err
			}
			if left == nil || v == nil {
				sawNull = true
				continue
			}
			if valuesEqual(left, v) {
				matched = true
				break
			}
		}
		if !matched && sawNull {
			return nil, nil
		}
		if x.Negated {
			return !matched, nil
		}
		return matched, nil
	case *sql.BetweenExpr:
		v, err := evalExpr(ctx, x.Left)
		if err != nil {
			return nil, err
		}
		lo, err := evalExpr(ctx, x.Low)
		if err != nil {
			return nil, err
		}
		hi, err := evalExpr(ctx, x.High)
		if err != nil {
			return nil, err
		}
		if v == nil || lo == nil || hi == nil {
			return nil, nil
		}
		if x.Symmetric && compareValues(lo, hi) > 0 {
			lo, hi = hi, lo
		}
		within := compareValues(v, lo) >= 0 && compareValues(v, hi) <= 0
		if x.Negated {
			return !within, nil
		}
		return within, nil
	case *sql.IsExpr:
		v, err := evalExpr(ctx, x.Left)
		if err != nil {
			return nil, err
		}
		if x.What == "json" {
			if v == nil {
				return nil, nil
			}
			result := evalIsJSON(v, x.JSONType, x.JSONUniqueSet && x.JSONUnique)
			if x.Negated {
				result = !result
			}
			return result, nil
		}
		if x.What == "distinct" {
			rv, err := evalExpr(ctx, x.Right)
			if err != nil {
				return nil, err
			}
			distinct := !valuesEqual(v, rv)
			if x.Negated {
				return !distinct, nil
			}
			return distinct, nil
		}
		var result bool
		switch x.What {
		case "null":
			result = v == nil
		case "true":
			result = v != nil && truthy(v)
		case "false":
			result = v != nil && !truthy(v)
		}
		if x.Negated {
			result = !result
		}
		return result, nil
	case *sql.FuncCall:
		if x.Window != nil {
			if ctx.windows == nil {
				return nil, errf("invalid_window", "window function %s is not allowed here", strings.ToUpper(x.Name))
			}
			v, ok := ctx.windows[x.String()]
			if !ok {
				return nil, errf("invalid_window", "window function %s was not computed", strings.ToUpper(x.Name))
			}
			return v, nil
		}
		if isJSONScalarFunction(x.Name) {
			return evalJSONFunction(ctx, x)
		}
		if sql.IsAggregate(x.Name) {
			if ctx.group == nil {
				return nil, errf("invalid_aggregate", "aggregate function %s is not allowed here", strings.ToUpper(x.Name))
			}
			return evalAggregate(ctx, x)
		}
		return evalFunction(ctx, x)
	case *sql.CaseExpr:
		return evalCase(ctx, x)
	case *sql.CastExpr:
		v, err := evalExpr(ctx, x.Expr)
		if err != nil {
			return nil, err
		}
		var out any
		if x.PreserveZone {
			out, err = castPreservingZone(v, x.Type)
		} else {
			out, err = castValue(v, x.Type)
		}
		if err != nil && x.Safe {
			return nil, nil
		}
		return out, err
	case *sql.ExistsExpr:
		ds, err := evalSubquery(ctx, x.Query)
		if err != nil {
			return nil, err
		}
		return len(ds.rows) > 0, nil
	case *sql.SubqueryExpr:
		ds, err := evalSubquery(ctx, x.Query)
		if err != nil {
			return nil, err
		}
		if len(ds.rows) == 0 {
			return nil, nil
		}
		if len(ds.rows) > 1 {
			return nil, errf("subquery_error", "scalar subquery returned more than one row")
		}
		if len(ds.columns) == 1 {
			return ds.rows[0][strings.ToLower(ds.columns[0].Name)], nil
		}
		row := make(rowValue, len(ds.columns))
		for i, c := range ds.columns {
			row[i] = ds.rows[0][strings.ToLower(c.Name)]
		}
		return row, nil
	case *sql.AtLocalExpr:
		v, err := evalExpr(ctx, x.Expr)
		if err != nil {
			return nil, err
		}
		return atLocal(v)
	case *sql.AtTimeZoneExpr:
		v, err := evalExpr(ctx, x.Expr)
		if err != nil {
			return nil, err
		}
		zone, err := evalExpr(ctx, x.Zone)
		if err != nil {
			return nil, err
		}
		return atTimeZone(v, zone)
	default:
		return nil, errf("unsupported", "unsupported expression %T", e)
	}
}

func evalSubquery(ctx *evalCtx, q *sql.SelectStmt) (*dataset, error) {
	if ctx.exec == nil {
		return nil, errf("subquery_error", "subquery execution context is unavailable")
	}
	prev := ctx.exec.outerEval
	ctx.exec.outerEval = ctx
	defer func() { ctx.exec.outerEval = prev }()
	return ctx.exec.executeSelect(q)
}

func evalInSubquery(ctx *evalCtx, x *sql.InExpr, left any) (any, error) {
	ds, err := evalSubquery(ctx, x.Subquery)
	if err != nil {
		return nil, err
	}
	if left == nil {
		if len(ds.rows) == 0 {
			return x.Negated, nil
		}
		return nil, nil
	}
	rowLeft, isRow := left.(rowValue)
	if isRow {
		if len(ds.columns) != len(rowLeft) {
			return nil, errf("subquery_error", "row IN subquery has %d columns, expected %d", len(ds.columns), len(rowLeft))
		}
	} else if len(ds.columns) != 1 {
		return nil, errf("subquery_error", "IN subquery must return exactly one column")
	}
	matched, sawNull := false, false
	for _, row := range ds.rows {
		var v any
		if isRow {
			values := make(rowValue, len(ds.columns))
			for i, c := range ds.columns {
				values[i] = row[strings.ToLower(c.Name)]
			}
			v = values
		} else {
			v = row[strings.ToLower(ds.columns[0].Name)]
		}
		if v == nil {
			sawNull = true
			continue
		}
		if valuesEqual(left, v) {
			matched = true
			break
		}
	}
	if !matched && sawNull {
		return nil, nil
	}
	if x.Negated {
		return !matched, nil
	}
	return matched, nil
}

func evalQuantified(ctx *evalCtx, q *sql.QuantifiedExpr) (any, error) {
	left, err := evalExpr(ctx, q.Left)
	if err != nil {
		return nil, err
	}
	var values []any
	if q.Subquery != nil {
		ds, err := evalSubquery(ctx, q.Subquery)
		if err != nil {
			return nil, err
		}
		if len(ds.columns) != 1 {
			return nil, errf("subquery_error", "ANY/ALL subquery must return exactly one column")
		}
		col := strings.ToLower(ds.columns[0].Name)
		for _, row := range ds.rows {
			values = append(values, row[col])
		}
	} else {
		for _, e := range q.List {
			v, err := evalExpr(ctx, e)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return q.All, nil
	}
	if left == nil {
		return nil, nil
	}
	sawNull := false
	matched := q.All
	for _, v := range values {
		if v == nil {
			sawNull = true
			continue
		}
		cmp := false
		switch q.Op {
		case "=", "==":
			cmp = valuesEqual(left, v)
		case "!=", "<>":
			cmp = !valuesEqual(left, v)
		case "<":
			cmp = compareValues(left, v) < 0
		case "<=":
			cmp = compareValues(left, v) <= 0
		case ">":
			cmp = compareValues(left, v) > 0
		case ">=":
			cmp = compareValues(left, v) >= 0
		default:
			return nil, errf("unsupported", "unsupported quantified comparison %s", q.Op)
		}
		if q.All && !cmp {
			return false, nil
		}
		if !q.All && cmp {
			return true, nil
		}
		if q.All {
			matched = true
		}
	}
	if sawNull {
		return nil, nil
	}
	return matched, nil
}

func evalBinary(ctx *evalCtx, b *sql.BinaryExpr) (any, error) {
	op := strings.ToUpper(b.Op)
	// AND / OR use three-valued logic.
	if op == "AND" || op == "OR" {
		left, err := evalExpr(ctx, b.Left)
		if err != nil {
			return nil, err
		}
		right, err := evalExpr(ctx, b.Right)
		if err != nil {
			return nil, err
		}
		if op == "AND" {
			if left != nil && !truthy(left) || right != nil && !truthy(right) {
				return false, nil
			}
			if left == nil || right == nil {
				return nil, nil
			}
			return true, nil
		}
		if left != nil && truthy(left) || right != nil && truthy(right) {
			return true, nil
		}
		if left == nil || right == nil {
			return nil, nil
		}
		return false, nil
	}
	left, err := evalExpr(ctx, b.Left)
	if err != nil {
		return nil, err
	}
	right, err := evalExpr(ctx, b.Right)
	if err != nil {
		return nil, err
	}
	if left == nil || right == nil {
		return nil, nil
	}
	if op == "||" {
		return toString(+0, left) + toString(0, right), nil
	}
	if op == "+" || op == "-" {
		if str, ok := left.(string); ok {
			if iv, ok := right.(intervalValue); ok {
				if op == "-" {
					iv = iv.neg()
				}
				if out, ok := addTimeOnlyInterval(str, iv); ok {
					return out, nil
				}
			}
		}
		if op == "+" {
			if str, ok := right.(string); ok {
				if iv, ok := left.(intervalValue); ok {
					if out, ok := addTimeOnlyInterval(str, iv); ok {
						return out, nil
					}
				}
			}
		}
	}
	if iv, ok := left.(intervalValue); ok {
		switch op {
		case "+", "-":
			if ov, ok := right.(intervalValue); ok {
				if op == "-" {
					ov = ov.neg()
				}
				return iv.add(ov), nil
			}
			if op == "+" {
				if t, ok := toTime(right); ok {
					return addInterval(t, iv).UTC().Format(time.RFC3339Nano), nil
				}
			}
		case "*":
			if f, ok := toFloat(right); ok {
				return iv.scale(f)
			}
		case "/":
			if f, ok := toFloat(right); ok {
				if f == 0 {
					return nil, errf("division_by_zero", "division by zero")
				}
				return iv.scale(1 / f)
			}
		}
		return nil, errf("type_error", "unsupported interval operator %s", b.Op)
	}
	if iv, ok := right.(intervalValue); ok {
		switch op {
		case "+":
			if t, ok := toTime(left); ok {
				return addInterval(t, iv).UTC().Format(time.RFC3339Nano), nil
			}
			if lv, ok := left.(intervalValue); ok {
				return lv.add(iv), nil
			}
		case "-":
			if t, ok := toTime(left); ok {
				return addInterval(t, iv.neg()).UTC().Format(time.RFC3339Nano), nil
			}
		case "*":
			if f, ok := toFloat(left); ok {
				return iv.scale(f)
			}
		}
		return nil, errf("type_error", "unsupported interval operator %s", b.Op)
	}
	if lt, ok := toTime(left); ok && op == "-" {
		if rt, ok := toTime(right); ok {
			return intervalBetween(rt, lt), nil
		}
	}
	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if !lok || !rok {
		return nil, errf("type_error", "operator %s needs numbers, got %T and %T", b.Op, left, right)
	}
	bothInt := isInteger(left) && isInteger(right)
	switch op {
	case "+":
		if bothInt {
			return toInt64(left) + toInt64(right), nil
		}
		return lf + rf, nil
	case "-":
		if bothInt {
			return toInt64(left) - toInt64(right), nil
		}
		return lf - rf, nil
	case "*":
		if bothInt {
			return toInt64(left) * toInt64(right), nil
		}
		return lf * rf, nil
	case "/":
		if rf == 0 {
			return nil, errf("division_by_zero", "division by zero")
		}
		return lf / rf, nil
	case "%":
		if rf == 0 {
			return nil, errf("division_by_zero", "modulo by zero")
		}
		if bothInt {
			return toInt64(left) % toInt64(right), nil
		}
		return math.Mod(lf, rf), nil
	case "|":
		return toInt64(left) | toInt64(right), nil
	case "&":
		return toInt64(left) & toInt64(right), nil
	default:
		return nil, errf("unsupported", "unsupported operator %s", b.Op)
	}
}

func evalCompare(ctx *evalCtx, c *sql.CompareExpr) (any, error) {
	left, err := evalExpr(ctx, c.Left)
	if err != nil {
		return nil, err
	}
	right, err := evalExpr(ctx, c.Right)
	if err != nil {
		return nil, err
	}
	op := strings.ToUpper(c.Op)
	if op == "LIKE" || op == "ILIKE" || op == "NOT LIKE" || op == "NOT ILIKE" {
		if left == nil || right == nil {
			return nil, nil
		}
		escape := ""
		if c.Escape != nil {
			ev, err := evalExpr(ctx, c.Escape)
			if err != nil {
				return nil, err
			}
			escape = fmt.Sprint(ev)
		}
		pattern, err := likePatternEsc(fmt.Sprint(right), op == "ILIKE" || op == "NOT ILIKE", escape)
		if err != nil {
			return nil, err
		}
		matched := pattern.MatchString(fmt.Sprint(left))
		if c.Negated {
			matched = !matched
		}
		return matched, nil
	}
	if op == "SIMILAR TO" || op == "NOT SIMILAR TO" {
		if left == nil || right == nil {
			return nil, nil
		}
		re, err := similarPattern(fmt.Sprint(right))
		if err != nil {
			return nil, err
		}
		matched := re.MatchString(fmt.Sprint(left))
		if c.Negated {
			matched = !matched
		}
		return matched, nil
	}
	if op == "REGEXP" || op == "NOT REGEXP" {
		if left == nil || right == nil {
			return nil, nil
		}
		re, err := regexp.Compile(fmt.Sprint(right))
		if err != nil {
			return nil, errf("invalid_expression", "invalid regular expression: %v", err)
		}
		matched := re.MatchString(fmt.Sprint(left))
		if c.Negated {
			matched = !matched
		}
		return matched, nil
	}
	if left == nil || right == nil {
		return nil, nil
	}
	if op == "=" || op == "!=" {
		eq := valuesEqual(left, right)
		if c.Negated || op == "!=" {
			return !eq, nil
		}
		return eq, nil
	}
	cmp := compareValues(left, right)
	switch op {
	case "<":
		return cmp < 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case ">=":
		return cmp >= 0, nil
	}
	return nil, errf("unsupported", "unsupported comparison operator %s", c.Op)
}

func evalCase(ctx *evalCtx, c *sql.CaseExpr) (any, error) {
	var base any
	var err error
	if c.Base != nil {
		base, err = evalExpr(ctx, c.Base)
		if err != nil {
			return nil, err
		}
	}
	for _, w := range c.Whens {
		cond, err := evalExpr(ctx, w.Cond)
		if err != nil {
			return nil, err
		}
		match := false
		if c.Base != nil {
			match = base != nil && cond != nil && valuesEqual(base, cond)
		} else {
			match = truthy(cond)
		}
		if match {
			return evalExpr(ctx, w.Then)
		}
	}
	if c.Else != nil {
		return evalExpr(ctx, c.Else)
	}
	return nil, nil
}

func evalFunction(ctx *evalCtx, f *sql.FuncCall) (any, error) {
	name := strings.ToLower(f.Name)
	args := make([]any, len(f.Args))
	for i, a := range f.Args {
		if _, ok := a.(*sql.StarExpr); ok {
			return nil, errf("invalid_expression", "%s(*) is only valid for count", strings.ToUpper(name))
		}
		v, err := evalExpr(ctx, a)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	// GROUPING() depends on the current grouping-set state, not just argument
	// values, so it is resolved by the engine rather than the scalar registry.
	if name == "grouping" || name == "grouping_id" {
		return evalGrouping(ctx, f)
	}
	registry := ctx.exec.engine.Functions()
	spec, ok := registry.lookup(name)
	if !ok {
		return nil, errf("unknown_function", "unknown function %s", strings.ToUpper(name))
	}
	if err := spec.checkArity(name, len(args)); err != nil {
		return nil, err
	}
	return spec.fn(ctx.exec.ctx, args)
}

func evalGrouping(ctx *evalCtx, f *sql.FuncCall) (any, error) {
	name := strings.ToLower(f.Name)
	if len(f.Args) == 0 {
		return nil, errf("invalid_arguments", "%s expects at least one argument", strings.ToUpper(name))
	}
	result := int64(0)
	for i, a := range f.Args {
		if ctx.grouping[a.String()] {
			result |= 1 << uint(len(f.Args)-1-i)
		}
	}
	return result, nil
}

func evalBuiltinFunction(name string, args []any) (any, error) {
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	requireArgs := func(n int) error {
		if len(args) != n {
			return errf("invalid_arguments", "%s expects %d argument(s), got %d", strings.ToUpper(name), n, len(args))
		}
		return nil
	}

	switch name {
	case "lower", "lcase":
		if err := requireArgs(1); err != nil {
			return nil, err
		}
		if arg(0) == nil {
			return nil, nil
		}
		return strings.ToLower(fmt.Sprint(arg(0))), nil
	case "upper", "ucase":
		if err := requireArgs(1); err != nil {
			return nil, err
		}
		if arg(0) == nil {
			return nil, nil
		}
		return strings.ToUpper(fmt.Sprint(arg(0))), nil
	case "length", "char_length", "character_length":
		if err := requireArgs(1); err != nil {
			return nil, err
		}
		if arg(0) == nil {
			return nil, nil
		}
		return int64(len([]rune(fmt.Sprint(arg(0))))), nil
	case "trim", "btrim":
		if len(args) < 1 {
			return nil, errf("invalid_arguments", "%s expects a string", strings.ToUpper(name))
		}
		if arg(0) == nil {
			return nil, nil
		}
		cutset := " "
		if len(args) > 1 {
			cutset = fmt.Sprint(arg(1))
		}
		return strings.Trim(fmt.Sprint(arg(0)), cutset), nil
	case "ltrim":
		if len(args) < 1 || arg(0) == nil {
			return nil, nil
		}
		cutset := " "
		if len(args) > 1 && arg(1) != nil {
			cutset = fmt.Sprint(arg(1))
		}
		return strings.TrimLeft(fmt.Sprint(arg(0)), cutset), nil
	case "rtrim":
		if len(args) < 1 || arg(0) == nil {
			return nil, nil
		}
		cutset := " "
		if len(args) > 1 && arg(1) != nil {
			cutset = fmt.Sprint(arg(1))
		}
		return strings.TrimRight(fmt.Sprint(arg(0)), cutset), nil
	case "substring", "substr":
		if len(args) < 2 {
			return nil, errf("invalid_arguments", "SUBSTRING expects (string, start[, length])")
		}
		if arg(0) == nil || arg(1) == nil {
			return nil, nil
		}
		s := []rune(fmt.Sprint(arg(0)))
		start, ok := toInt(arg(1))
		if !ok {
			return nil, errf("type_error", "SUBSTRING start must be an integer")
		}
		start--
		if start < 0 {
			start = 0
		}
		if start >= len(s) {
			return "", nil
		}
		if len(args) >= 3 && arg(2) != nil {
			n, ok := toInt(arg(2))
			if !ok {
				return nil, errf("type_error", "SUBSTRING length must be an integer")
			}
			if n < 0 {
				n = 0
			}
			if start+n < len(s) {
				s = s[:start+n]
			}
		}
		return string(s[start:]), nil
	case "concat":
		var sb strings.Builder
		for _, a := range args {
			if a == nil {
				return nil, nil
			}
			sb.WriteString(fmt.Sprint(a))
		}
		return sb.String(), nil
	case "concat_ws":
		if len(args) < 1 || arg(0) == nil {
			return nil, nil
		}
		sep := fmt.Sprint(arg(0))
		var parts []string
		for _, a := range args[1:] {
			if a == nil {
				continue
			}
			parts = append(parts, fmt.Sprint(a))
		}
		return strings.Join(parts, sep), nil
	case "coalesce":
		for _, a := range args {
			if a != nil {
				return a, nil
			}
		}
		return nil, nil
	case "ifnull", "nvl":
		if len(args) != 2 {
			return nil, errf("invalid_arguments", "%s expects 2 arguments", strings.ToUpper(name))
		}
		if args[0] != nil {
			return args[0], nil
		}
		return args[1], nil
	case "nullif":
		if len(args) != 2 {
			return nil, errf("invalid_arguments", "NULLIF expects 2 arguments")
		}
		if args[0] != nil && args[1] != nil && valuesEqual(args[0], args[1]) {
			return nil, nil
		}
		return args[0], nil
	case "abs":
		if err := requireArgs(1); err != nil {
			return nil, err
		}
		f, ok := toFloat(arg(0))
		if !ok {
			return nil, errf("type_error", "ABS expects a number")
		}
		if isInteger(arg(0)) {
			return int64(math.Abs(f)), nil
		}
		return math.Abs(f), nil
	case "ceil", "ceiling":
		f, ok := toFloat(arg(0))
		if !ok {
			return nil, errf("type_error", "CEIL expects a number")
		}
		return math.Ceil(f), nil
	case "floor":
		f, ok := toFloat(arg(0))
		if !ok {
			return nil, errf("type_error", "FLOOR expects a number")
		}
		return math.Floor(f), nil
	case "round":
		f, ok := toFloat(arg(0))
		if !ok {
			return nil, errf("type_error", "ROUND expects a number")
		}
		digits := 0
		if len(args) > 1 {
			if n, ok := toInt(arg(1)); ok {
				digits = n
			}
		}
		pow := math.Pow(10, float64(digits))
		return math.Round(f*pow) / pow, nil
	case "sqrt":
		f, ok := toFloat(arg(0))
		if !ok || f < 0 {
			return nil, errf("type_error", "SQRT expects a non-negative number")
		}
		return math.Sqrt(f), nil
	case "power", "pow":
		if len(args) != 2 {
			return nil, errf("invalid_arguments", "POWER expects 2 arguments")
		}
		a, aok := toFloat(arg(0))
		b, bok := toFloat(arg(1))
		if !aok || !bok {
			return nil, errf("type_error", "POWER expects numbers")
		}
		return math.Pow(a, b), nil
	case "mod":
		if len(args) != 2 {
			return nil, errf("invalid_arguments", "MOD expects 2 arguments")
		}
		a, aok := toInt(arg(0))
		b, bok := toInt(arg(1))
		if !aok || !bok || b == 0 {
			return nil, errf("type_error", "MOD expects non-zero integers")
		}
		return a % b, nil
	case "greatest", "least":
		if len(args) == 0 {
			return nil, nil
		}
		best := args[0]
		for _, a := range args[1:] {
			if a == nil {
				continue
			}
			if best == nil {
				best = a
				continue
			}
			c := compareValues(a, best)
			if (name == "greatest" && c > 0) || (name == "least" && c < 0) {
				best = a
			}
		}
		return best, nil
	case "replace":
		if len(args) != 3 {
			return nil, errf("invalid_arguments", "REPLACE expects 3 arguments")
		}
		if arg(0) == nil || arg(1) == nil || arg(2) == nil {
			return nil, nil
		}
		return strings.ReplaceAll(fmt.Sprint(arg(0)), fmt.Sprint(arg(1)), fmt.Sprint(arg(2))), nil
	case "split_part":
		if len(args) != 3 {
			return nil, errf("invalid_arguments", "SPLIT_PART expects 3 arguments")
		}
		if arg(0) == nil || arg(1) == nil || arg(2) == nil {
			return nil, nil
		}
		parts := strings.Split(fmt.Sprint(arg(0)), fmt.Sprint(arg(1)))
		n, ok := toInt(arg(2))
		if !ok || n == 0 {
			return nil, errf("type_error", "SPLIT_PART index must be a non-zero integer")
		}
		if n < 0 {
			n = len(parts) + n + 1
		}
		if n < 1 || n > len(parts) {
			return "", nil
		}
		return parts[n-1], nil
	case "now", "current_timestamp", "localtimestamp":
		now := time.Now().UTC()
		if len(args) > 0 {
			if p, ok := toInt(arg(0)); ok {
				return formatTimestampPrecision(now, p), nil
			}
		}
		return now.Format(time.RFC3339Nano), nil
	case "current_time", "localtime":
		now := time.Now().UTC()
		if len(args) > 0 {
			if p, ok := toInt(arg(0)); ok {
				return formatClockPrecision(now, p), nil
			}
		}
		return now.Format("15:04:05"), nil
	case "current_date":
		return time.Now().UTC().Format("2006-01-02"), nil
	case "date_trunc":
		if len(args) != 2 {
			return nil, errf("invalid_arguments", "DATE_TRUNC expects (unit, timestamp)")
		}
		if arg(0) == nil || arg(1) == nil {
			return nil, nil
		}
		return dateTrunc(fmt.Sprint(arg(0)), arg(1))
	case "md5":
		if arg(0) == nil {
			return nil, nil
		}
		h := md5.Sum([]byte(fmt.Sprint(arg(0))))
		return hex.EncodeToString(h[:]), nil
	case "sha256":
		if arg(0) == nil {
			return nil, nil
		}
		h := sha256.Sum256([]byte(fmt.Sprint(arg(0))))
		return hex.EncodeToString(h[:]), nil
	case "random", "rand":
		return math.Mod(float64(time.Now().UnixNano())/1e9, 1), nil
	case "timezone":
		if len(args) != 2 || arg(0) == nil || arg(1) == nil {
			return nil, nil
		}
		tm, ok := toTime(arg(1))
		if !ok {
			return nil, errf("type_error", "TIMEZONE expects a timestamp")
		}
		loc, err := time.LoadLocation(fmt.Sprint(arg(0)))
		if err != nil {
			return nil, errf("invalid_arguments", "unknown time zone %q", fmt.Sprint(arg(0)))
		}
		return tm.In(loc).Format(time.RFC3339Nano), nil
	case "to_timestamp":
		if len(args) != 1 || arg(0) == nil {
			return nil, nil
		}
		if s, ok := arg(0).(string); ok {
			if tm, ok := toTime(s); ok {
				return tm.UTC().Format(time.RFC3339Nano), nil
			}
		}
		if f, ok := toFloat(arg(0)); ok {
			return time.Unix(int64(f), 0).UTC().Format(time.RFC3339Nano), nil
		}
		return nil, errf("type_error", "TO_TIMESTAMP expects a timestamp string or epoch")
	case "json_extract", "json_extract_scalar":
		return jsonExtract(arg(0), arg(1), name == "json_extract_scalar")
	case "extract", "date_part":
		if len(args) != 2 {
			return nil, errf("invalid_arguments", "%s expects (part, timestamp)", strings.ToUpper(name))
		}
		if arg(0) == nil || arg(1) == nil {
			return nil, nil
		}
		return extractPart(fmt.Sprint(arg(0)), arg(1))
	case "date_add", "date_sub", "dateadd", "datesub", "timestampadd":
		if len(args) != 3 {
			return nil, errf("invalid_arguments", "%s expects (unit, amount, timestamp)", strings.ToUpper(name))
		}
		if arg(1) == nil || arg(2) == nil {
			return nil, nil
		}
		n, ok := toInt(arg(1))
		if !ok {
			return nil, errf("type_error", "%s amount must be an integer", strings.ToUpper(name))
		}
		if name == "date_sub" || name == "datesub" {
			n = -n
		}
		return dateAdd(fmt.Sprint(arg(0)), n, arg(2))
	case "date_diff", "datediff", "timestampdiff":
		if len(args) != 3 {
			return nil, errf("invalid_arguments", "%s expects (unit, start, end)", strings.ToUpper(name))
		}
		if arg(1) == nil || arg(2) == nil {
			return nil, nil
		}
		return dateDiff(fmt.Sprint(arg(0)), arg(1), arg(2))
	case "left":
		if len(args) != 2 || arg(0) == nil {
			return nil, nil
		}
		n, _ := toInt(arg(1))
		r := []rune(fmt.Sprint(arg(0)))
		if n < 0 {
			n = 0
		}
		if n > len(r) {
			n = len(r)
		}
		return string(r[:n]), nil
	case "right":
		if len(args) != 2 || arg(0) == nil {
			return nil, nil
		}
		n, _ := toInt(arg(1))
		r := []rune(fmt.Sprint(arg(0)))
		if n < 0 {
			n = 0
		}
		if n > len(r) {
			n = len(r)
		}
		return string(r[len(r)-n:]), nil
	case "reverse":
		if len(args) != 1 || arg(0) == nil {
			return nil, nil
		}
		r := []rune(fmt.Sprint(arg(0)))
		for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
			r[i], r[j] = r[j], r[i]
		}
		return string(r), nil
	case "repeat":
		if len(args) != 2 || arg(0) == nil {
			return nil, nil
		}
		n, _ := toInt(arg(1))
		if n < 0 {
			n = 0
		}
		return strings.Repeat(fmt.Sprint(arg(0)), n), nil
	case "position", "strpos", "instr":
		if len(args) != 2 || arg(0) == nil || arg(1) == nil {
			return nil, nil
		}
		idx := strings.Index(fmt.Sprint(arg(1)), fmt.Sprint(arg(0)))
		if idx < 0 {
			return int64(0), nil
		}
		return int64(len([]rune(fmt.Sprint(arg(1))[:idx])) + 1), nil
	case "starts_with", "startswith":
		if len(args) != 2 || arg(0) == nil || arg(1) == nil {
			return nil, nil
		}
		return strings.HasPrefix(fmt.Sprint(arg(0)), fmt.Sprint(arg(1))), nil
	case "ends_with", "endswith":
		if len(args) != 2 || arg(0) == nil || arg(1) == nil {
			return nil, nil
		}
		return strings.HasSuffix(fmt.Sprint(arg(0)), fmt.Sprint(arg(1))), nil
	case "contains":
		if len(args) != 2 || arg(0) == nil || arg(1) == nil {
			return nil, nil
		}
		return strings.Contains(fmt.Sprint(arg(0)), fmt.Sprint(arg(1))), nil
	case "initcap":
		if len(args) != 1 || arg(0) == nil {
			return nil, nil
		}
		s := strings.Fields(strings.ToLower(fmt.Sprint(arg(0))))
		for i, w := range s {
			r := []rune(w)
			if len(r) > 0 {
				r[0] = unicode.ToUpper(r[0])
			}
			s[i] = string(r)
		}
		return strings.Join(s, " "), nil
	case "lpad", "rpad":
		if len(args) < 2 || arg(0) == nil {
			return nil, nil
		}
		n, _ := toInt(arg(1))
		pad := " "
		if len(args) > 2 && arg(2) != nil {
			pad = fmt.Sprint(arg(2))
		}
		s := fmt.Sprint(arg(0))
		r := []rune(s)
		if n <= len(r) {
			return string(r[:maxInt(n, 0)]), nil
		}
		need := n - len(r)
		if pad == "" {
			pad = " "
		}
		fill := strings.Repeat(pad, need/len([]rune(pad))+1)
		fillRunes := []rune(fill)[:need]
		if name == "lpad" {
			return string(fillRunes) + s, nil
		}
		return s + string(fillRunes), nil
	case "regexp_replace":
		if len(args) < 3 || arg(0) == nil || arg(1) == nil || arg(2) == nil {
			return nil, nil
		}
		re, err := regexp.Compile(fmt.Sprint(arg(1)))
		if err != nil {
			return nil, errf("invalid_expression", "invalid regular expression: %v", err)
		}
		return re.ReplaceAllString(fmt.Sprint(arg(0)), fmt.Sprint(arg(2))), nil
	case "regexp_extract":
		if len(args) < 2 || arg(0) == nil || arg(1) == nil {
			return nil, nil
		}
		re, err := regexp.Compile(fmt.Sprint(arg(1)))
		if err != nil {
			return nil, errf("invalid_expression", "invalid regular expression: %v", err)
		}
		m := re.FindStringSubmatch(fmt.Sprint(arg(0)))
		idx := 0
		if len(args) > 2 {
			if n, ok := toInt(arg(2)); ok {
				idx = n
			}
		}
		if idx < 0 || idx >= len(m) {
			return "", nil
		}
		return m[idx], nil
	case "overlaps":
		if len(args) != 4 {
			return nil, errf("invalid_arguments", "OVERLAPS expects two periods")
		}
		period := func(start, end any) (time.Time, time.Time, bool) {
			a, aok := toTime(start)
			if !aok {
				return time.Time{}, time.Time{}, false
			}
			if iv, ok := end.(intervalValue); ok {
				return a, addInterval(a, iv), true
			}
			b, bok := toTime(end)
			if !bok {
				return time.Time{}, time.Time{}, false
			}
			if a.After(b) {
				a, b = b, a
			}
			return a, b, true
		}
		a, b, ok1 := period(args[0], args[1])
		c, d, ok2 := period(args[2], args[3])
		if !ok1 || !ok2 {
			return nil, errf("type_error", "OVERLAPS expects timestamps or timestamp/interval pairs")
		}
		return !a.After(d) && !c.After(b), nil

	case "if", "iff":
		if len(args) != 3 {
			return nil, errf("invalid_arguments", "%s expects 3 arguments", strings.ToUpper(name))
		}
		if truthy(arg(0)) {
			return arg(1), nil
		}
		return arg(2), nil
	case "overlay":
		if len(args) != 3 && len(args) != 4 {
			return nil, errf("invalid_arguments", "OVERLAY expects (string, replacement, start[, length])")
		}
		if arg(0) == nil || arg(1) == nil || arg(2) == nil {
			return nil, nil
		}
		s := []rune(fmt.Sprint(arg(0)))
		repl := []rune(fmt.Sprint(arg(1)))
		start, ok := toInt(arg(2))
		if !ok {
			return nil, errf("type_error", "OVERLAY start must be an integer")
		}
		start--
		if start < 0 {
			start = 0
		}
		length := len(repl)
		if len(args) == 4 && arg(3) != nil {
			length, ok = toInt(arg(3))
			if !ok || length < 0 {
				return nil, errf("type_error", "OVERLAY length must be a non-negative integer")
			}
		}
		if start > len(s) {
			start = len(s)
		}
		end := start + length
		if end > len(s) {
			end = len(s)
		}
		out := append([]rune{}, s[:start]...)
		out = append(out, repl...)
		out = append(out, s[end:]...)
		return string(out), nil
	default:
		return nil, errf("unknown_function", "unknown function %s", strings.ToUpper(name))
	}
}

func jsonExtract(v, path any, scalar bool) (any, error) {
	if v == nil || path == nil {
		return nil, nil
	}
	var cur any
	if s, ok := v.(string); ok {
		if err := json.Unmarshal([]byte(s), &cur); err != nil {
			return nil, errf("type_error", "invalid JSON")
		}
	} else {
		cur = v
	}
	p := strings.TrimPrefix(fmt.Sprint(path), "$")
	p = strings.TrimPrefix(p, ".")
	for _, part := range strings.Split(p, ".") {
		if part == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, nil
		}
		cur = m[part]
	}
	if scalar && cur != nil {
		switch x := cur.(type) {
		case string:
			return x, nil
		case float64:
			return x, nil
		case bool:
			return x, nil
		default:
			b, _ := json.Marshal(x)
			return string(b), nil
		}
	}
	return cur, nil
}

func extractPart(part string, v any) (any, error) {
	if iv, ok := v.(intervalValue); ok {
		switch strings.ToLower(part) {
		case "year":
			return int64(iv.months / 12), nil
		case "month":
			return int64(iv.months % 12), nil
		case "day":
			return int64(iv.days), nil
		case "hour":
			return int64(iv.nanos / int64(time.Hour)), nil
		case "minute":
			return int64((iv.nanos % int64(time.Hour)) / int64(time.Minute)), nil
		case "second":
			return float64(iv.nanos%int64(time.Minute)) / float64(time.Second), nil
		default:
			return nil, errf("invalid_arguments", "unsupported EXTRACT field %q for interval", part)
		}
	}
	t, ok := toTime(v)
	if !ok {
		return nil, errf("type_error", "EXTRACT expects a timestamp")
	}
	switch strings.ToLower(part) {
	case "year":
		return int64(t.Year()), nil
	case "quarter":
		return int64((int(t.Month())-1)/3 + 1), nil
	case "month":
		return int64(t.Month()), nil
	case "week":
		_, week := t.ISOWeek()
		return int64(week), nil
	case "day":
		return int64(t.Day()), nil
	case "dow", "dayofweek":
		return int64(t.Weekday()), nil
	case "doy", "dayofyear":
		return int64(t.YearDay()), nil
	case "hour":
		return int64(t.Hour()), nil
	case "minute":
		return int64(t.Minute()), nil
	case "second":
		return int64(t.Second()), nil
	case "epoch":
		return float64(t.UnixNano()) / 1e9, nil
	case "timezone_hour":
		_, offset := t.Zone()
		return int64(offset / 3600), nil
	case "timezone_minute":
		_, offset := t.Zone()
		return int64((offset % 3600) / 60), nil
	default:
		return nil, errf("invalid_arguments", "unsupported EXTRACT field %q", part)
	}
}

func dateAdd(unit string, amount int, v any) (any, error) {
	t, ok := toTime(v)
	if !ok {
		return nil, errf("type_error", "DATE_ADD expects a timestamp")
	}
	switch strings.ToLower(unit) {
	case "year":
		t = addMonthsClamped(t, amount*12)
	case "quarter":
		t = addMonthsClamped(t, amount*3)
	case "month":
		t = addMonthsClamped(t, amount)
	case "week":
		t = t.AddDate(0, 0, amount*7)
	case "day":
		t = t.AddDate(0, 0, amount)
	case "hour":
		t = t.Add(time.Duration(amount) * time.Hour)
	case "minute":
		t = t.Add(time.Duration(amount) * time.Minute)
	case "second":
		t = t.Add(time.Duration(amount) * time.Second)
	default:
		return nil, errf("invalid_arguments", "unsupported DATE_ADD unit %q", unit)
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}

func dateDiff(unit string, a, b any) (any, error) {
	start, ok := toTime(a)
	if !ok {
		return nil, errf("type_error", "DATE_DIFF expects timestamps")
	}
	end, ok := toTime(b)
	if !ok {
		return nil, errf("type_error", "DATE_DIFF expects timestamps")
	}
	switch strings.ToLower(unit) {
	case "year":
		return int64(end.Year() - start.Year()), nil
	case "month":
		return int64((end.Year()-start.Year())*12 + int(end.Month()) - int(start.Month())), nil
	case "day":
		return int64(end.Sub(start).Hours() / 24), nil
	case "hour":
		return int64(end.Sub(start).Hours()), nil
	case "minute":
		return int64(end.Sub(start).Minutes()), nil
	case "second":
		return int64(end.Sub(start).Seconds()), nil
	default:
		return nil, errf("invalid_arguments", "unsupported DATE_DIFF unit %q", unit)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func dateTrunc(unit string, v any) (any, error) {
	t, ok := toTime(v)
	if !ok {
		return nil, errf("type_error", "DATE_TRUNC expects a timestamp")
	}
	switch strings.ToLower(unit) {
	case "year":
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), nil
	case "quarter":
		q := time.Month((int(t.Month())-1)/3*3 + 1)
		return time.Date(t.Year(), q, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), nil
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), nil
	case "week":
		wd := (int(t.Weekday()) + 6) % 7
		return time.Date(t.Year(), t.Month(), t.Day()-wd, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), nil
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), nil
	case "hour":
		return t.Truncate(time.Hour).Format(time.RFC3339Nano), nil
	case "minute":
		return t.Truncate(time.Minute).Format(time.RFC3339Nano), nil
	case "second":
		return t.Truncate(time.Second).Format(time.RFC3339Nano), nil
	default:
		return nil, errf("invalid_arguments", "unsupported DATE_TRUNC unit %q", unit)
	}
}

func evalOrderedSetAggregate(ctx *evalCtx, f *sql.FuncCall, group []map[string]any) (any, error) {
	name := strings.ToLower(f.Name)
	if len(f.WithinGroup) != 1 {
		return nil, errf("invalid_aggregate", "%s requires WITHIN GROUP (ORDER BY expr)", strings.ToUpper(name))
	}
	if name != "percentile_cont" && name != "percentile_disc" && name != "mode" && name != "rank" && name != "dense_rank" && name != "percent_rank" && name != "cume_dist" {
		return nil, errf("invalid_aggregate", "%s does not support WITHIN GROUP", strings.ToUpper(name))
	}
	type pair struct {
		key any
	}
	pairs := make([]pair, 0, len(group))
	for _, row := range group {
		rowCtx := &evalCtx{rel: ctx.rel, row: row, outer: ctx.outer, exec: ctx.exec}
		if ctx.rel == nil {
			rowCtx.aliases = lowerMap(row)
		}
		if f.Filter != nil {
			v, err := evalExpr(rowCtx, f.Filter)
			if err != nil {
				return nil, err
			}
			if !truthy(v) {
				continue
			}
		}
		v, err := evalExpr(rowCtx, f.WithinGroup[0].Expr)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, pair{key: v})
	}
	order := []sql.OrderItem{{Expr: f.WithinGroup[0].Expr, Desc: f.WithinGroup[0].Desc, Nulls: f.WithinGroup[0].Nulls}}
	sort.SliceStable(pairs, func(i, j int) bool {
		return lessSortKeys([]any{pairs[i].key}, []any{pairs[j].key}, order)
	})
	if len(pairs) == 0 {
		return nil, nil
	}
	if name == "rank" || name == "dense_rank" || name == "percent_rank" || name == "cume_dist" {
		if len(f.Args) != 1 {
			return nil, errf("invalid_aggregate", "%s expects one argument", strings.ToUpper(name))
		}
		hyp, err := evalExpr(ctx, f.Args[0])
		if err != nil {
			return nil, err
		}
		less, lessEq, distinctLess := 0, 0, 0
		var previous any
		hasPrev := false
		for _, pair := range pairs {
			if lessSortKeys([]any{pair.key}, []any{hyp}, order) {
				less++
			} else if valuesEqual(pair.key, hyp) {
				lessEq++
			}
			if !hasPrev || !valuesEqual(previous, pair.key) {
				distinctLess++
				previous = pair.key
				hasPrev = true
			}
		}
		rank := int64(less + 1)
		dense := int64(0)
		seen := map[string]bool{}
		for _, pair := range pairs {
			if !lessSortKeys([]any{pair.key}, []any{hyp}, order) {
				continue
			}
			key := canonicalValues([]any{pair.key})
			if !seen[key] {
				seen[key] = true
				dense++
			}
		}
		dense++
		switch name {
		case "rank":
			return rank, nil
		case "dense_rank":
			return dense, nil
		case "percent_rank":
			if len(pairs) == 1 {
				return float64(0), nil
			}
			return float64(rank-1) / float64(len(pairs)-1), nil
		case "cume_dist":
			return float64(less+lessEq) / float64(len(pairs)), nil
		}
	}
	if name == "mode" {
		bestKey := ""
		bestCount := 0
		var best any
		for i := 0; i < len(pairs); {
			j := i + 1
			for j < len(pairs) && valuesEqual(pairs[i].key, pairs[j].key) {
				j++
			}
			if j-i > bestCount {
				bestCount = j - i
				best = pairs[i].key
				bestKey = canonicalValues([]any{best})
			}
			_ = bestKey
			i = j
		}
		return best, nil
	}
	if len(f.Args) != 1 {
		return nil, errf("invalid_aggregate", "%s expects one fraction argument", strings.ToUpper(name))
	}
	fv, err := evalExpr(ctx, f.Args[0])
	if err != nil {
		return nil, err
	}
	fraction, ok := toFloat(fv)
	if !ok || fraction < 0 || fraction > 1 {
		return nil, errf("invalid_aggregate", "%s fraction must be between 0 and 1", strings.ToUpper(name))
	}
	if name == "percentile_disc" {
		idx := int(math.Ceil(fraction*float64(len(pairs)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(pairs) {
			idx = len(pairs) - 1
		}
		return pairs[idx].key, nil
	}
	pos := fraction * float64(len(pairs)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo < 0 {
		lo = 0
	}
	if hi >= len(pairs) {
		hi = len(pairs) - 1
	}
	lv, lok := toFloat(pairs[lo].key)
	hv, hok := toFloat(pairs[hi].key)
	if !lok || !hok {
		return nil, errf("type_error", "%s expects numeric ORDER BY values", strings.ToUpper(name))
	}
	if lo == hi {
		return pairs[lo].key, nil
	}
	return lv + (hv-lv)*(pos-float64(lo)), nil
}

func evalAggregate(ctx *evalCtx, f *sql.FuncCall) (any, error) {
	name := strings.ToLower(f.Name)
	group := ctx.group
	star := len(f.Args) == 1
	if star {
		_, star = f.Args[0].(*sql.StarExpr)
	}
	if star && name != "count" {
		return nil, errf("invalid_aggregate", "%s(*) is not supported", strings.ToUpper(name))
	}
	if !star && len(f.Args) == 0 && name != "mode" && name != "json_objectagg" {
		return nil, errf("invalid_aggregate", "%s requires an argument", strings.ToUpper(name))
	}
	if star && name == "count" {
		if f.Filter == nil {
			return int64(len(group)), nil
		}
		n := int64(0)
		for _, row := range group {
			rowCtx := &evalCtx{rel: ctx.rel, row: row, outer: ctx.outer, exec: ctx.exec}
			if ctx.rel == nil {
				rowCtx.aliases = lowerMap(row)
			}
			v, err := evalExpr(rowCtx, f.Filter)
			if err != nil {
				return nil, err
			}
			if truthy(v) {
				n++
			}
		}
		return n, nil
	}
	if name == "json_arrayagg" || name == "json_objectagg" {
		return evalJSONAggregate(ctx, f, group)
	}
	if len(f.WithinGroup) > 0 || name == "percentile_cont" || name == "percentile_disc" || name == "mode" {
		return evalOrderedSetAggregate(ctx, f, group)
	}

	values := make([]any, 0, len(group))
	for _, row := range group {
		rowCtx := &evalCtx{rel: ctx.rel, row: row, outer: ctx.outer, exec: ctx.exec}
		if ctx.rel == nil {
			rowCtx.aliases = lowerMap(row)
		}
		if f.Filter != nil {
			fv, err := evalExpr(rowCtx, f.Filter)
			if err != nil {
				return nil, err
			}
			if !truthy(fv) {
				continue
			}
		}
		v, err := evalExpr(rowCtx, f.Args[0])
		if err != nil {
			return nil, err
		}
		if v != nil {
			values = append(values, v)
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
		if len(values) == 0 {
			return nil, nil
		}
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
	case "group_concat", "string_agg":
		sep := ","
		if len(f.Args) > 1 {
			sv, err := evalExpr(ctx, f.Args[1])
			if err != nil {
				return nil, err
			}
			if sv != nil {
				sep = fmt.Sprint(sv)
			}
		}
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = fmt.Sprint(v)
		}
		return strings.Join(parts, sep), nil
	case "array_agg":
		return values, nil
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
		return nil, errf("unsupported", "unsupported aggregate %s", strings.ToUpper(name))
	}
}

// ---- type helpers ----------------------------------------------------------

func truthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, err := strconv.ParseBool(x)
		if err == nil {
			return b
		}
		return x != ""
	default:
		if f, ok := toFloat(v); ok {
			return f != 0
		}
		return true
	}
}

func lowerMap(row map[string]any) map[string]any {
	if row == nil {
		return nil
	}
	out := make(map[string]any, len(row))
	for k, v := range row {
		out[strings.ToLower(k)] = v
	}
	return out
}

func toString(_ int, v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func isInteger(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case uint:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		return int64(x)
	case float64:
		return int64(x)
	case float32:
		return int64(x)
	case json.Number:
		i, _ := x.Int64()
		return i
	}
	return 0
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int8:
		return int(x), true
	case int16:
		return int(x), true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case uint:
		return int(x), true
	case uint8:
		return int(x), true
	case uint16:
		return int(x), true
	case uint32:
		return int(x), true
	case uint64:
		return int(x), true
	case float64:
		if x == float64(int(x)) {
			return int(x), true
		}
	case float32:
		if float64(x) == float64(int(x)) {
			return int(x), true
		}
	case string:
		i, err := strconv.Atoi(x)
		return i, err == nil
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

type rowValue []any

func valuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if ar, ok := a.(rowValue); ok {
		br, ok := b.(rowValue)
		if !ok || len(ar) != len(br) {
			return false
		}
		for i := range ar {
			if !valuesEqual(ar[i], br[i]) {
				return false
			}
		}
		return true
	}
	if ai, ok := a.(intervalValue); ok {
		if bi, ok := b.(intervalValue); ok {
			return ai.compare(bi) == 0
		}
	}
	if isNumeric(a) && isNumeric(b) {
		af, _ := toFloat(a)
		bf, _ := toFloat(b)
		return af == bf
	}
	if isNumeric(a) || isNumeric(b) {
		// Numeric-looking strings compare numerically when the other side is a
		// real number (common with CSV and Mongo sources).
		if af, aok := toFloat(a); aok {
			if bf, bok := toFloat(b); bok {
				return af == bf
			}
		}
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		if at, ok := parseTimeString(as); ok {
			if bt, ok := parseTimeString(bs); ok {
				return at.Equal(bt)
			}
		}
		return as == bs
	}
	ab, aok := a.(bool)
	bb, bok := b.(bool)
	if aok && bok {
		return ab == bb
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func compareValues(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	if ai, ok := a.(intervalValue); ok {
		if bi, ok := b.(intervalValue); ok {
			return ai.compare(bi)
		}
	}
	if isNumeric(a) || isNumeric(b) {
		af, aok := toFloat(a)
		bf, bok := toFloat(b)
		if aok && bok {
			if af < bf {
				return -1
			}
			if af > bf {
				return 1
			}
			return 0
		}
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			if at, ok := parseTimeString(as); ok {
				if bt, ok := parseTimeString(bs); ok {
					if at.Before(bt) {
						return -1
					}
					if at.After(bt) {
						return 1
					}
					return 0
				}
			}
			return strings.Compare(as, bs)
		}
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func isNumeric(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return true
	}
	return false
}

func atTimeZone(v, zone any) (any, error) {
	if v == nil || zone == nil {
		return nil, nil
	}
	loc, err := time.LoadLocation(fmt.Sprint(zone))
	if err != nil {
		return nil, errf("invalid_arguments", "unknown time zone %q", fmt.Sprint(zone))
	}
	var t time.Time
	var hasZone bool
	if tm, ok := v.(time.Time); ok {
		t = tm
		hasZone = true
	} else {
		s := fmt.Sprint(v)
		hasZone = hasExplicitZone(s)
		if hasZone {
			tm, ok := parseTimeString(s)
			if !ok {
				return nil, errf("type_error", "AT TIME ZONE expects a timestamp")
			}
			t = tm
		} else {
			tm, ok := parseWallInLocation(s, loc)
			if !ok {
				return nil, errf("type_error", "AT TIME ZONE expects a timestamp")
			}
			t, hasZone = tm, true
		}
	}
	return t.In(loc).Format("2006-01-02 15:04:05.999999999"), nil
}

var timeOnlyRE = regexp.MustCompile(`^\s*\d{1,2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:\s*(?:Z|[+-]\d{2}:?\d{2}|UTC|GMT))?\s*$`)

func hasExplicitZone(s string) bool {
	u := strings.ToUpper(strings.TrimSpace(s))
	return strings.HasSuffix(u, "Z") || zoneOffsetSuffix(s) ||
		strings.HasSuffix(u, " UTC") || strings.HasSuffix(u, " GMT")
}

func parseWallInLocation(s string, loc *time.Location) (time.Time, bool) {
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02",
	} {
		if tm, err := time.ParseInLocation(layout, s, loc); err == nil {
			return tm, true
		}
	}
	return time.Time{}, false
}

func atLocal(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok && timeOnlyRE.MatchString(s) {
		return nil, errf("type_error", "AT LOCAL is undefined for TIME values")
	}
	tm, ok := toTime(v)
	if !ok {
		return nil, errf("type_error", "AT LOCAL expects a timestamp")
	}
	if s, ok := v.(string); ok && !hasExplicitZone(s) {
		if wall, ok := parseWallInLocation(s, time.Local); ok {
			tm = wall
		}
	}
	return tm.In(time.Local).Format(time.RFC3339Nano), nil
}

func zoneOffsetSuffix(s string) bool {
	if len(s) < 6 {
		return false
	}
	tail := s[len(s)-6:]
	return (tail[0] == '+' || tail[0] == '-') && tail[3] == ':'
}

func parseTimeString(s string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999 MST", "2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02",
		"15:04:05.999999999Z07:00", "15:04:05Z07:00",
		"15:04:05.999999999", "15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func toTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		return parseTimeString(x)
	}
	return time.Time{}, false
}

func typeParams(t string) ([]int, bool) {
	open := strings.IndexByte(t, '(')
	close := strings.LastIndexByte(t, ')')
	if open < 0 || close <= open+1 {
		return nil, false
	}
	parts := strings.Split(t[open+1:close], ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, len(out) > 0
}

func typeScale(t string) (int, bool) {
	p, ok := typeParams(t)
	if !ok || len(p) < 2 {
		return 0, false
	}
	return p[1], true
}

func typeLength(t string) (int, bool) {
	p, ok := typeParams(t)
	if !ok || len(p) == 0 {
		return 0, false
	}
	return p[0], true
}

func typePrecision(t string) (int, bool) {
	p, ok := typeParams(t)
	if !ok || len(p) == 0 {
		return 0, false
	}
	if p[0] < 0 || p[0] > 9 {
		return 9, true
	}
	return p[0], true
}

func formatTimestampPrecision(t time.Time, p int) string {
	if p < 0 {
		p = 0
	}
	if p > 9 {
		p = 9
	}
	if p == 0 {
		return t.Format("2006-01-02T15:04:05Z07:00")
	}
	base := t.Format("2006-01-02T15:04:05")
	frac := fmt.Sprintf("%09d", t.Nanosecond())
	zone := t.Format("Z07:00")
	return base + "." + frac[:p] + zone
}

func formatClockPrecision(t time.Time, p int) string {
	if p < 0 {
		p = 0
	}
	if p > 9 {
		p = 9
	}
	base := t.Format("15:04:05")
	if p == 0 {
		return base
	}
	frac := fmt.Sprintf("%09d", t.Nanosecond())
	return base + "." + frac[:p]
}

func castPreservingZone(v any, typ string) (any, error) {
	s, ok := v.(string)
	if !ok {
		return castValue(v, typ)
	}
	tm, ok := parseTimeString(s)
	if !ok {
		return castValue(v, typ)
	}
	// A timestamp literal with an explicit numeric offset keeps that offset so
	// TIMEZONE_HOUR/MINUTE and display semantics remain observable. A literal
	// without a zone stays naive so AT LOCAL/AT TIME ZONE can resolve it in the
	// intended wall-clock zone.
	if hasExplicitZone(s) {
		if strings.HasPrefix(strings.ToLower(typ), "time ") {
			return tm.Format("15:04:05Z07:00"), nil
		}
		return tm.Format(time.RFC3339Nano), nil
	}
	return s, nil
}

func castValue(v any, typ string) (any, error) {
	t := strings.ToLower(strings.TrimSpace(typ))
	if v == nil {
		return nil, nil
	}
	switch {
	case strings.HasPrefix(t, "interval"):
		if iv, ok := v.(intervalValue); ok {
			return iv, nil
		}
		q := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(t, "interval")))
		if q != "" {
			if iv, err := parseInterval(fmt.Sprint(v), q); err == nil {
				return iv, nil
			}
		}
		return parseInterval(fmt.Sprint(v), "")
	case strings.HasPrefix(t, "int"), strings.HasPrefix(t, "bigint"), strings.HasPrefix(t, "smallint"), t == "integer":
		f, ok := toFloat(v)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errf("cast_error", "cannot cast %v to integer", v)
		}
		if f > float64(1<<63-1) || f < -float64(1<<63) {
			return nil, errf("cast_error", "integer value out of range: %v", v)
		}
		return int64(f), nil
	case strings.HasPrefix(t, "decimal"), strings.HasPrefix(t, "numeric"), strings.HasPrefix(t, "float"), strings.HasPrefix(t, "double"), strings.HasPrefix(t, "real"):
		f, ok := toFloat(v)
		if !ok {
			return nil, errf("cast_error", "cannot cast %v to number", v)
		}
		if strings.HasPrefix(t, "decimal") || strings.HasPrefix(t, "numeric") {
			if scale, ok := typeScale(t); ok {
				pow := math.Pow10(scale)
				f = math.Round(f*pow) / pow
			}
		}
		return f, nil
	case strings.HasPrefix(t, "bool"):
		if b, ok := v.(bool); ok {
			return b, nil
		}
		b, err := strconv.ParseBool(fmt.Sprint(v))
		if err != nil {
			return nil, errf("cast_error", "cannot cast %v to boolean", v)
		}
		return b, nil
	case strings.HasPrefix(t, "interval"):
		return parseInterval(fmt.Sprint(v), "")
	case strings.HasPrefix(t, "timestamp"), strings.HasPrefix(t, "datetime"):
		tm, ok := toTime(v)
		if !ok {
			return nil, errf("cast_error", "cannot cast %v to timestamp", v)
		}
		if p, ok := typePrecision(t); ok {
			return formatTimestampPrecision(tm.UTC(), p), nil
		}
		return tm.UTC().Format(time.RFC3339Nano), nil
	case strings.HasPrefix(t, "date"):
		tm, ok := toTime(v)
		if !ok {
			return nil, errf("cast_error", "cannot cast %v to date", v)
		}
		return tm.UTC().Format("2006-01-02"), nil
	case strings.HasPrefix(t, "time"):
		tm, ok := toTime(v)
		if !ok {
			return nil, errf("cast_error", "cannot cast %v to time", v)
		}
		if p, ok := typePrecision(t); ok {
			return formatClockPrecision(tm.UTC(), p), nil
		}
		return tm.UTC().Format("15:04:05"), nil
	case strings.HasPrefix(t, "text"), strings.HasPrefix(t, "varchar"), strings.HasPrefix(t, "char"), strings.HasPrefix(t, "string"):
		out := fmt.Sprint(v)
		if n, ok := typeLength(t); ok {
			r := []rune(out)
			if len(r) > n {
				out = string(r[:n])
			}
		}
		return out, nil
	default:
		return fmt.Sprint(v), nil
	}
}

func similarPattern(pattern string) (*regexp.Regexp, error) {
	var sb strings.Builder
	sb.WriteString("^")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '%':
			sb.WriteString(".*")
		case '_':
			sb.WriteString(".")
		case '.', '|', '*', '+', '?', '(', ')', '[', ']', '{', '}':
			sb.WriteRune(r)
		default:
			sb.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}

func likePattern(pattern string, insensitive bool) (*regexp.Regexp, error) {
	return likePatternEsc(pattern, insensitive, "")
}

func likePatternEsc(pattern string, insensitive bool, escape string) (*regexp.Regexp, error) {
	var sb strings.Builder
	if insensitive {
		sb.WriteString("(?is)")
	} else {
		sb.WriteString("(?s)")
	}
	sb.WriteString("^")
	runes := []rune(pattern)
	esc := rune(0)
	if escape != "" {
		esc = []rune(escape)[0]
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if esc != 0 && r == esc {
			if i+1 < len(runes) {
				next := runes[i+1]
				if next == '%' || next == '_' || next == esc {
					sb.WriteString(regexp.QuoteMeta(string(next)))
					i++
					continue
				}
			}
			sb.WriteString(regexp.QuoteMeta(string(r)))
			continue
		}
		switch r {
		case '%':
			sb.WriteString(".*")
		case '_':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}
