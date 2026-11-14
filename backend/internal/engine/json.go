package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/anyquery/anyquery/sql"
)

var _ context.Context

func isJSONScalarFunction(name string) bool {
	switch strings.ToLower(name) {
	case "json_value", "json_query", "json_exists", "json_object", "json_array":
		return true
	}
	return false
}

func evalJSONFunction(ctx *evalCtx, f *sql.FuncCall) (any, error) {
	switch strings.ToLower(f.Name) {
	case "json_value", "json_query", "json_exists":
		return evalJSONLookup(ctx, f)
	case "json_object":
		return evalJSONObject(ctx, f)
	case "json_array":
		return evalJSONArray(ctx, f)
	default:
		return nil, errf("unknown_function", "unknown JSON function %s", strings.ToUpper(f.Name))
	}
}

func evalJSONLookup(ctx *evalCtx, f *sql.FuncCall) (any, error) {
	if len(f.Args) != 1 || f.JSONPath == nil {
		return nil, errf("invalid_arguments", "%s expects (value, path)", strings.ToUpper(f.Name))
	}
	value, err := evalExpr(ctx, f.Args[0])
	if err != nil {
		return nil, err
	}
	if value == nil {
		return jsonLookupEmpty(ctx, f)
	}
	pathValue, err := evalExpr(ctx, f.JSONPath)
	if err != nil {
		return nil, err
	}
	if pathValue == nil {
		return jsonLookupEmpty(ctx, f)
	}
	pathText := fmt.Sprint(pathValue)
	basePath, filter := splitJSONPathFilter(pathText)
	matches, query, err := evalJSONPath(value, basePath)
	if err != nil {
		if query.strict && !strings.EqualFold(f.Name, "json_exists") {
			return nil, err
		}
		return jsonExistsOrEmpty(ctx, f, err)
	}
	if filter != "" {
		matches, err = filterJSONMatches(ctx, f, matches, filter)
		if err != nil {
			return jsonExistsOrEmpty(ctx, f, err)
		}
	}
	if len(matches) == 0 {
		if strings.EqualFold(f.Name, "json_exists") {
			return jsonExistsDefault(f), nil
		}
		return jsonLookupEmpty(ctx, f)
	}
	switch strings.ToLower(f.Name) {
	case "json_exists":
		return true, nil
	case "json_value":
		v := matches[0]
		switch v.(type) {
		case map[string]any, []any:
			if f.JSONOnError == "error" {
				return nil, errf("json_error", "JSON_VALUE cannot return an object or array")
			}
			return nil, nil
		}
		if f.JSONReturning != "" {
			cast, err := castValue(v, f.JSONReturning)
			if err != nil {
				if f.JSONOnError == "error" {
					return nil, err
				}
				return nil, nil
			}
			v = cast
		}
		return v, nil
	default: // json_query
		upper := strings.ToUpper(f.JSONWrapper)
		wrap := strings.HasPrefix(upper, "WITH") && !strings.HasPrefix(upper, "WITHOUT")
		conditional := strings.Contains(upper, "CONDITIONAL")
		v := matches[0]
		if f.JSONQuotes != "" {
			if f.JSONQuotes == "omit" && v != nil {
				return fmt.Sprint(v), nil
			}
			text, err := jsonValueText(v)
			if err != nil {
				return nil, err
			}
			return text, nil
		}
		if wrap && (!conditional || !isJSONArray(v)) {
			if len(matches) > 1 || isJSONArray(v) {
				v = matches
			} else {
				v = []any{v}
			}
		}
		if !wrap {
			if len(matches) > 1 {
				return nil, errf("json_error", "JSON_QUERY matched multiple values without a wrapper")
			}
			if strings.HasPrefix(upper, "WITHOUT") {
				// WITHOUT wrapper returns the scalar/object text directly.
			} else if _, ok := v.(map[string]any); !ok {
				if !isJSONArray(v) {
					return nil, errf("json_error", "JSON_QUERY requires an object or array result without a wrapper")
				}
			}
		}
		text, err := jsonValueText(v)
		if err != nil {
			return nil, err
		}
		return text, nil
	}
}

func isJSONArray(v any) bool { _, ok := v.([]any); return ok }

func jsonExistsDefault(f *sql.FuncCall) any {
	switch f.JSONOnError {
	case "true":
		return true
	case "false":
		return false
	case "unknown":
		return nil
	}
	return false
}

func jsonExistsOrEmpty(ctx *evalCtx, f *sql.FuncCall, err error) (any, error) {
	if strings.EqualFold(f.Name, "json_exists") {
		switch f.JSONOnError {
		case "error":
			return nil, err
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "unknown":
			return nil, nil
		}
		return false, nil
	}
	if f.JSONOnError == "error" {
		return nil, err
	}
	return jsonLookupEmpty(ctx, f)
}

func jsonLookupEmpty(ctx *evalCtx, f *sql.FuncCall) (any, error) {
	if f.JSONOnEmpty == "error" {
		return nil, errf("json_error", "%s found no JSON value", strings.ToUpper(f.Name))
	}
	if f.JSONOnEmpty == "default" && f.JSONDefaultEmpty != nil {
		return evalExpr(ctx, f.JSONDefaultEmpty)
	}
	if f.JSONOnEmpty == "empty_array" {
		return []any{}, nil
	}
	if f.JSONOnEmpty == "empty_object" {
		return map[string]any{}, nil
	}
	if f.JSONOnError == "error" {
		return nil, errf("json_error", "%s found no JSON value", strings.ToUpper(f.Name))
	}
	if f.JSONOnError == "default" && f.JSONDefaultError != nil {
		return evalExpr(ctx, f.JSONDefaultError)
	}
	return nil, nil
}

func evalJSONObject(ctx *evalCtx, f *sql.FuncCall) (any, error) {
	out := map[string]any{}
	for _, pair := range f.JSONPairs {
		key, err := evalExpr(ctx, pair.Key)
		if err != nil {
			return nil, err
		}
		value, err := evalExpr(ctx, pair.Value)
		if err != nil {
			return nil, err
		}
		if value == nil && f.JSONNullBehavior == "absent" {
			continue
		}
		k := fmt.Sprint(key)
		if _, exists := out[k]; exists && f.JSONUnique {
			return nil, errf("json_error", "duplicate JSON object key %q", k)
		}
		out[k] = value
	}
	return out, nil
}

func evalJSONArray(ctx *evalCtx, f *sql.FuncCall) (any, error) {
	out := make([]any, 0, len(f.Args))
	for _, arg := range f.Args {
		v, err := evalExpr(ctx, arg)
		if err != nil {
			return nil, err
		}
		if v == nil && f.JSONNullBehavior == "absent" {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func evalJSONAggregate(ctx *evalCtx, f *sql.FuncCall, group []map[string]any) (any, error) {
	name := strings.ToLower(f.Name)
	type rowValue struct {
		key    any
		value  any
		keys   []any
		rowIdx int
	}
	rows := make([]rowValue, 0, len(group))
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
		rv := rowValue{}
		if name == "json_objectagg" {
			if len(f.JSONPairs) != 1 {
				return nil, errf("invalid_aggregate", "JSON_OBJECTAGG expects a key/value pair")
			}
			k, err := evalExpr(rowCtx, f.JSONPairs[0].Key)
			if err != nil {
				return nil, err
			}
			v, err := evalExpr(rowCtx, f.JSONPairs[0].Value)
			if err != nil {
				return nil, err
			}
			rv.key, rv.value = k, v
		} else {
			if len(f.Args) != 1 {
				return nil, errf("invalid_aggregate", "JSON_ARRAYAGG expects one argument")
			}
			v, err := evalExpr(rowCtx, f.Args[0])
			if err != nil {
				return nil, err
			}
			rv.value = v
		}
		if len(f.JSONOrder) > 0 {
			keys := make([]any, len(f.JSONOrder))
			for i, o := range f.JSONOrder {
				v, err := evalExpr(rowCtx, o.Expr)
				if err != nil {
					return nil, err
				}
				keys[i] = v
			}
			rv.keys = keys
		}
		rows = append(rows, rv)
	}
	if len(f.JSONOrder) > 0 {
		sort.SliceStable(rows, func(i, j int) bool {
			return lessSortKeys(rows[i].keys, rows[j].keys, f.JSONOrder)
		})
	}
	if name == "json_objectagg" {
		out := map[string]any{}
		for _, rv := range rows {
			if rv.value == nil && f.JSONNullBehavior == "absent" {
				continue
			}
			k := fmt.Sprint(rv.key)
			if _, exists := out[k]; exists && f.JSONUnique {
				return nil, errf("json_error", "duplicate JSON object key %q", k)
			}
			out[k] = rv.value
		}
		return out, nil
	}
	out := make([]any, 0, len(rows))
	for _, rv := range rows {
		if rv.value == nil && f.JSONNullBehavior == "absent" {
			continue
		}
		out = append(out, rv.value)
	}
	return out, nil
}

func evalIsJSON(v any, typ string, unique bool) bool {
	if v == nil {
		return false
	}
	decoded, ok := jsonIsJSON(v)
	if !ok {
		return false
	}
	if unique {
		if text, ok := v.(string); ok && !jsonUniqueKeys(text) {
			return false
		}
	}
	return jsonTypeMatches(decoded, typ)
}

func jsonUniqueKeys(text string) bool {
	dec := json.NewDecoder(strings.NewReader(text))
	ok, err := uniqueJSONValue(dec)
	return err == nil && ok
}

func uniqueJSONValue(dec *json.Decoder) (bool, error) {
	tok, err := dec.Token()
	if err != nil {
		return false, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return true, nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return false, err
			}
			key := fmt.Sprint(kt)
			if seen[key] {
				return false, nil
			}
			seen[key] = true
			if ok, err := uniqueJSONValue(dec); err != nil || !ok {
				return ok, err
			}
		}
		_, err = dec.Token()
		return true, err
	case '[':
		for dec.More() {
			if ok, err := uniqueJSONValue(dec); err != nil || !ok {
				return ok, err
			}
		}
		_, err = dec.Token()
		return true, err
	}
	return true, nil
}

func (ec *execCtx) relationForJSONTable(t *sql.JSONTable) (*relation, error) {
	value, err := evalExpr(&evalCtx{outer: ec.outerEval, exec: ec}, t.Expr)
	if err != nil {
		return nil, err
	}
	pathValue, err := evalExpr(&evalCtx{outer: ec.outerEval, exec: ec}, t.Path)
	if err != nil {
		return nil, err
	}
	if pathValue == nil {
		return nil, errf("json_error", "JSON_TABLE path is NULL")
	}
	basePath, filter := splitJSONPathFilter(fmt.Sprint(pathValue))
	roots, _, err := evalJSONPath(value, basePath)
	if err == nil && filter != "" {
		roots, err = filterJSONMatches(&evalCtx{outer: ec.outerEval, exec: ec}, &sql.FuncCall{JSONPassing: t.Passing}, roots, filter)
	}
	if err != nil {
		if t.OnError == "empty" || t.OnError == "null" {
			ds := &dataset{columns: jsonTableColumns(t.Columns)}
			if t.OnError == "null" {
				row := map[string]any{}
				for _, c := range ds.columns {
					row[strings.ToLower(c.Name)] = nil
				}
				ds.rows = []map[string]any{row}
			}
			return datasetToRelation(ds, aliasForJSONTable(t), aliasForJSONTable(t)), nil
		}
		return nil, err
	}
	ds := &dataset{columns: jsonTableColumns(t.Columns)}
	for i, root := range roots {
		rows, err := expandJSONTableRoot(root, t.Columns, i+1, t.Passing)
		if err != nil {
			return nil, err
		}
		ds.rows = append(ds.rows, rows...)
	}
	alias := t.Alias
	if alias == "" {
		alias = "json_table"
	}
	return datasetToRelation(ds, alias, alias), nil
}

func aliasForJSONTable(t *sql.JSONTable) string {
	if t.Alias != "" {
		return t.Alias
	}
	return "json_table"
}

func jsonTableColumns(cols []sql.JSONTableColumn) []ResultColumn {
	var out []ResultColumn
	for _, c := range cols {
		if c.NestedPath != nil {
			out = append(out, jsonTableColumns(c.Nested)...)
			continue
		}
		typ := strings.ToLower(c.Type)
		if c.Ordinality {
			typ = "integer"
		}
		out = append(out, ResultColumn{Name: c.Name, Type: typ})
	}
	return dedupeResultColumns(out)
}

func expandJSONTableRoot(root any, cols []sql.JSONTableColumn, ordinal int, passing []sql.JSONPair) ([]map[string]any, error) {
	return expandJSONTableRows([]jsonTableRow{{values: map[string]any{}, node: root, ordinal: ordinal}}, cols, passing)
}

type jsonTableRow struct {
	values  map[string]any
	node    any
	ordinal int
}

func expandJSONTableRows(rows []jsonTableRow, cols []sql.JSONTableColumn, passing []sql.JSONPair) ([]map[string]any, error) {
	for _, col := range cols {
		if col.NestedPath != nil {
			var expanded []jsonTableRow
			for _, base := range rows {
				pathValue, err := evalExpr(&evalCtx{}, col.NestedPath)
				if err != nil {
					return nil, err
				}
				basePath, filter := splitJSONPathFilter(fmt.Sprint(pathValue))
				matches, _, err := evalJSONPath(base.node, basePath)
				if err != nil {
					return nil, err
				}
				if filter != "" {
					matches, err = filterJSONMatches(&evalCtx{}, &sql.FuncCall{JSONPassing: passing}, matches, filter)
					if err != nil {
						return nil, err
					}
				}
				for i, match := range matches {
					next := jsonTableRow{values: copyJSONValues(base.values), node: match, ordinal: i + 1}
					sub, err := expandJSONTableRows([]jsonTableRow{next}, col.Nested, passing)
					if err != nil {
						return nil, err
					}
					for _, r := range sub {
						expanded = append(expanded, jsonTableRow{values: r, node: match, ordinal: next.ordinal})
					}
				}
			}
			rows = expanded
			continue
		}
		for i := range rows {
			if col.Ordinality {
				rows[i].values[strings.ToLower(col.Name)] = int64(rows[i].ordinal)
				continue
			}
			if col.Exists {
				if col.Path == nil {
					rows[i].values[strings.ToLower(col.Name)] = false
					continue
				}
				pathValue, err := evalExpr(&evalCtx{}, col.Path)
				if err != nil {
					return nil, err
				}
				basePath, filter := splitJSONPathFilter(fmt.Sprint(pathValue))
				matches, _, err := evalJSONPath(rows[i].node, basePath)
				if err != nil {
					return nil, err
				}
				if filter != "" {
					matches, err = filterJSONMatches(&evalCtx{}, &sql.FuncCall{JSONPassing: passing}, matches, filter)
					if err != nil {
						return nil, err
					}
				}
				rows[i].values[strings.ToLower(col.Name)] = len(matches) > 0
				continue
			}
			if col.Path == nil {
				rows[i].values[strings.ToLower(col.Name)] = nil
				continue
			}
			pathValue, err := evalExpr(&evalCtx{}, col.Path)
			if err != nil {
				return nil, err
			}
			basePath, filter := splitJSONPathFilter(fmt.Sprint(pathValue))
			matches, _, err := evalJSONPath(rows[i].node, basePath)
			if err != nil {
				return nil, err
			}
			if filter != "" {
				matches, err = filterJSONMatches(&evalCtx{}, &sql.FuncCall{JSONPassing: passing}, matches, filter)
				if err != nil {
					return nil, err
				}
			}
			if len(matches) == 0 {
				v, err := jsonColumnEmpty(col)
				if err != nil {
					return nil, err
				}
				rows[i].values[strings.ToLower(col.Name)] = v
				continue
			}
			v := matches[0]
			if col.Type != "" {
				cast, castErr := castValue(v, col.Type)
				if castErr != nil {
					if col.OnError == "default" && col.DefaultError != nil {
						cast, castErr = evalExpr(&evalCtx{}, col.DefaultError)
					}
					if castErr != nil {
						if col.OnError == "null" {
							v = nil
						} else {
							return nil, castErr
						}
					} else {
						v = cast
					}
				} else {
					v = cast
				}
			}
			rows[i].values[strings.ToLower(col.Name)] = v
		}
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.values)
	}
	return out, nil
}

func splitJSONPathFilter(raw string) (string, string) {
	if i := strings.Index(raw, "?"); i >= 0 {
		base := strings.TrimSpace(raw[:i])
		filter := strings.TrimSpace(raw[i+1:])
		filter = strings.TrimSpace(strings.TrimPrefix(filter, "("))
		filter = strings.TrimSpace(strings.TrimSuffix(filter, ")"))
		return base, filter
	}
	return raw, ""
}

func filterJSONMatches(ctx *evalCtx, f *sql.FuncCall, matches []any, filter string) ([]any, error) {
	vars := map[string]any{}
	for _, pair := range f.JSONPassing {
		key := pair.Key.String()
		if lit, ok := pair.Key.(*sql.Literal); ok {
			key = fmt.Sprint(lit.Value)
		}
		v, err := evalExpr(ctx, pair.Value)
		if err != nil {
			return nil, err
		}
		vars[key] = v
	}
	out := make([]any, 0, len(matches))
	for _, v := range matches {
		ok, err := evalJSONFilter(v, filter, vars)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func evalJSONFilter(node any, expr string, vars map[string]any) (bool, error) {
	expr = strings.TrimSpace(expr)
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	if parts := splitTopLevel(expr, "||"); len(parts) > 1 {
		for _, part := range parts {
			ok, err := evalJSONFilter(node, part, vars)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if parts := splitTopLevel(expr, "&&"); len(parts) > 1 {
		for _, part := range parts {
			ok, err := evalJSONFilter(node, part, vars)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	if strings.HasPrefix(strings.ToLower(expr), "exists") {
		value, err := evalJSONFilterOperand(node, strings.TrimSpace(expr[len("exists"):]), vars)
		if err != nil {
			return false, err
		}
		return value != nil, nil
	}
	for _, op := range []string{"starts with", "like_regex", "==", "!=", ">=", "<=", ">", "<"} {
		idx := indexTopLevel(expr, op)
		if idx < 0 {
			continue
		}
		left, err := evalJSONFilterOperand(node, expr[:idx], vars)
		if err != nil {
			return false, err
		}
		if op == "starts with" {
			right, err := evalJSONFilterOperand(node, expr[idx+len(op):], vars)
			if err != nil {
				return false, err
			}
			return strings.HasPrefix(fmt.Sprint(left), fmt.Sprint(right)), nil
		}
		if op == "like_regex" {
			raw := strings.TrimSpace(expr[idx+len(op):])
			flags := ""
			if fi := strings.Index(strings.ToLower(raw), " flag "); fi >= 0 {
				flags = raw[fi+len(" flag "):]
				raw = strings.TrimSpace(raw[:fi])
			}
			right, err := evalJSONFilterOperand(node, raw, vars)
			if err != nil {
				return false, err
			}
			pattern := fmt.Sprint(right)
			if strings.Contains(flags, "i") {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return false, err
			}
			return re.MatchString(fmt.Sprint(left)), nil
		}
		right, err := evalJSONFilterOperand(node, expr[idx+len(op):], vars)
		if err != nil {
			return false, err
		}
		cmp := compareValues(left, right)
		switch op {
		case "==":
			return valuesEqual(left, right), nil
		case "!=":
			return !valuesEqual(left, right), nil
		case ">":
			return cmp > 0, nil
		case ">=":
			return cmp >= 0, nil
		case "<":
			return cmp < 0, nil
		case "<=":
			return cmp <= 0, nil
		}
	}
	return false, errf("json_path", "unsupported JSON path filter %q", expr)
}

func evalJSONFilterOperand(node any, raw string, vars map[string]any) (any, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "@") {
		path := "$" + strings.TrimPrefix(raw, "@")
		if path == "$" {
			return node, nil
		}
		matches, _, err := evalJSONPath(node, path)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, nil
		}
		return matches[0], nil
	}
	if strings.HasPrefix(raw, "$") {
		v, ok := vars[strings.TrimPrefix(raw, "$")]
		if !ok {
			return nil, errf("json_path", "unknown PASSING variable %s", raw)
		}
		return v, nil
	}
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return raw[1 : len(raw)-1], nil
	}
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1], nil
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, errf("json_path", "invalid JSON filter literal %q", raw)
	}
	return f, nil
}

func splitTopLevel(s, sep string) []string {
	var out []string
	depth := 0
	quote := byte(0)
	start := 0
	for i := 0; i+len(sep) <= len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && strings.HasPrefix(s[i:], sep) {
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
	return out
}

func indexTopLevel(s, needle string) int {
	depth := 0
	quote := byte(0)
	for i := 0; i+len(needle) <= len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && strings.HasPrefix(s[i:], needle) {
			return i
		}
	}
	return -1
}

func jsonColumnEmpty(col sql.JSONTableColumn) (any, error) {
	switch col.OnEmpty {
	case "error":
		return nil, errf("json_error", "JSON_TABLE column %s is empty", col.Name)
	case "default":
		if col.DefaultEmpty != nil {
			return evalExpr(&evalCtx{}, col.DefaultEmpty)
		}
	}
	return nil, nil
}

func copyJSONValues(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// keep encoding/json linked for callers that build JSON values via this file.
var _ = json.Valid
