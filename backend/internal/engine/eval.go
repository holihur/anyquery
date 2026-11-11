package engine

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/sql"
)

type evalCtx struct {
	rel     *relation
	row     map[string]any
	aliases map[string]any
	group   []map[string]any
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
	return nil, &Error{Code: "column_not_found", Message: fmt.Sprintf("column %q not found", ref.String())}
}

func evalExpr(ctx *evalCtx, e sql.Expr) (any, error) {
	switch x := e.(type) {
	case *sql.Literal:
		return x.Value, nil
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
	case *sql.InExpr:
		left, err := evalExpr(ctx, x.Left)
		if err != nil {
			return nil, err
		}
		if x.Subquery != nil {
			return nil, errf("unsupported", "IN subqueries are not supported yet")
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
		return castValue(v, x.Type)
	case *sql.ExistsExpr:
		return nil, errf("unsupported", "EXISTS subqueries are not supported yet")
	case *sql.SubqueryExpr:
		return nil, errf("unsupported", "scalar subqueries are not supported yet")
	default:
		return nil, errf("unsupported", "unsupported expression %T", e)
	}
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
		pattern, err := likePattern(fmt.Sprint(right), op == "ILIKE" || op == "NOT ILIKE")
		if err != nil {
			return nil, err
		}
		matched := pattern.MatchString(fmt.Sprint(left))
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
		return strings.TrimLeft(fmt.Sprint(arg(0)), " "), nil
	case "rtrim":
		if len(args) < 1 || arg(0) == nil {
			return nil, nil
		}
		return strings.TrimRight(fmt.Sprint(arg(0)), " "), nil
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
	case "now", "current_timestamp", "current_time":
		return time.Now().UTC().Format(time.RFC3339Nano), nil
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
	case "json_extract", "json_extract_scalar":
		return jsonExtract(arg(0), arg(1), name == "json_extract_scalar")
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
	if !star && len(f.Args) == 0 {
		return nil, errf("invalid_aggregate", "%s requires an argument", strings.ToUpper(name))
	}
	if star && name == "count" {
		return int64(len(group)), nil
	}

	values := make([]any, 0, len(group))
	for _, row := range group {
		rowCtx := &evalCtx{rel: ctx.rel, row: row}
		if ctx.rel == nil {
			rowCtx.aliases = lowerMap(row)
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

func valuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
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

func parseTimeString(s string) (time.Time, bool) {
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"}
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

func castValue(v any, typ string) (any, error) {
	t := strings.ToLower(strings.TrimSpace(typ))
	if v == nil {
		return nil, nil
	}
	switch {
	case strings.HasPrefix(t, "int"), strings.HasPrefix(t, "bigint"), strings.HasPrefix(t, "smallint"), t == "integer":
		f, ok := toFloat(v)
		if !ok {
			return nil, errf("cast_error", "cannot cast %v to integer", v)
		}
		return int64(f), nil
	case strings.HasPrefix(t, "decimal"), strings.HasPrefix(t, "numeric"), strings.HasPrefix(t, "float"), strings.HasPrefix(t, "double"), strings.HasPrefix(t, "real"):
		f, ok := toFloat(v)
		if !ok {
			return nil, errf("cast_error", "cannot cast %v to number", v)
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
	case strings.HasPrefix(t, "timestamp"), strings.HasPrefix(t, "datetime"):
		tm, ok := toTime(v)
		if !ok {
			return nil, errf("cast_error", "cannot cast %v to timestamp", v)
		}
		return tm.UTC().Format(time.RFC3339Nano), nil
	case strings.HasPrefix(t, "date"):
		tm, ok := toTime(v)
		if !ok {
			return nil, errf("cast_error", "cannot cast %v to date", v)
		}
		return tm.UTC().Format("2006-01-02"), nil
	case strings.HasPrefix(t, "text"), strings.HasPrefix(t, "varchar"), strings.HasPrefix(t, "char"), strings.HasPrefix(t, "string"):
		return fmt.Sprint(v), nil
	default:
		return fmt.Sprint(v), nil
	}
}

func likePattern(pattern string, insensitive bool) (*regexp.Regexp, error) {
	var sb strings.Builder
	if insensitive {
		sb.WriteString("(?is)")
	} else {
		sb.WriteString("(?s)")
	}
	sb.WriteString("^")
	for _, r := range pattern {
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
