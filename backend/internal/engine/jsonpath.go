package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type jsonPathStep struct {
	kind      string // key, index, range, wildcard
	key       string
	index     int
	end       int
	recursive bool
}

type jsonPathQuery struct {
	steps  []jsonPathStep
	strict bool
}

func parseJSONPath(raw string) (jsonPathQuery, error) {
	path := strings.TrimSpace(raw)
	q := jsonPathQuery{}
	if strings.HasPrefix(strings.ToLower(path), "strict ") {
		q.strict = true
		path = strings.TrimSpace(path[7:])
	} else if strings.HasPrefix(strings.ToLower(path), "lax ") {
		path = strings.TrimSpace(path[4:])
	}
	if path == "" {
		return q, errf("json_path", "empty JSON path")
	}
	if path[0] != '$' {
		// Legacy dot path compatibility.
		path = "$." + strings.TrimPrefix(path, ".")
	}
	if path[0] != '$' {
		return q, errf("json_path", "JSON path must start with $")
	}
	i := 1
	for i < len(path) {
		switch path[i] {
		case '.':
			i++
			recursive := false
			if i < len(path) && path[i] == '.' {
				recursive = true
				i++
			}
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			name := strings.TrimSpace(path[start:i])
			if len(name) >= 2 && (name[0] == '"' && name[len(name)-1] == '"' || name[0] == '\'' && name[len(name)-1] == '\'') {
				name = name[1 : len(name)-1]
			}
			if name == "" {
				return q, errf("json_path", "empty JSON path member")
			}
			if name == "*" {
				q.steps = append(q.steps, jsonPathStep{kind: "wildcard", recursive: recursive})
			} else {
				q.steps = append(q.steps, jsonPathStep{kind: "key", key: name, recursive: recursive})
			}
		case '[':
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return q, errf("json_path", "unterminated JSON path subscript")
			}
			body := strings.TrimSpace(path[i+1 : i+end])
			switch {
			case body == "*":
				q.steps = append(q.steps, jsonPathStep{kind: "wildcard"})
			case len(body) >= 2 && (body[0] == '\'' || body[0] == '"'):
				q.steps = append(q.steps, jsonPathStep{kind: "key", key: body[1 : len(body)-1]})
			default:
				if strings.Contains(strings.ToLower(body), " to ") {
					parts := strings.SplitN(strings.ToLower(body), " to ", 2)
					start, err1 := jsonPathIndex(parts[0])
					end, err2 := jsonPathIndex(parts[1])
					if err1 != nil || err2 != nil {
						return q, errf("json_path", "invalid JSON path range %q", body)
					}
					q.steps = append(q.steps, jsonPathStep{kind: "range", index: start, end: end})
				} else {
					n, err := jsonPathIndex(body)
					if err != nil {
						return q, err
					}
					q.steps = append(q.steps, jsonPathStep{kind: "index", index: n})
				}
			}
			i += end + 1
		default:
			return q, errf("json_path", "unexpected character %q in JSON path", path[i])
		}
	}
	return q, nil
}

func decodeJSONValue(v any) (any, error) {
	if s, ok := v.(string); ok {
		var decoded any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return nil, errf("type_error", "invalid JSON: %v", err)
		}
		return decoded, nil
	}
	return v, nil
}

func evalJSONPath(v any, raw string) ([]any, jsonPathQuery, error) {
	q, err := parseJSONPath(raw)
	if err != nil {
		return nil, q, err
	}
	root := v
	if text, ok := v.(string); ok {
		if !json.Valid([]byte(text)) {
			root = text
		} else if decoded, err := decodeJSONValue(text); err == nil {
			root = decoded
		} else {
			return nil, q, err
		}
	}
	cur := []any{root}
	for _, step := range q.steps {
		var next []any
		for _, node := range cur {
			next = append(next, applyJSONPathStep(node, step)...)
		}
		cur = next
	}
	if len(cur) == 0 && q.strict {
		return nil, q, errf("json_path", "strict JSON path matched no value")
	}
	return cur, q, nil
}

func jsonPathIndex(s string) (int, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasPrefix(s, "last") {
		n := -1
		if len(s) > 4 {
			off, err := strconv.Atoi(strings.TrimSpace(s[4:]))
			if err != nil {
				return 0, errf("json_path", "invalid JSON path index %q", s)
			}
			n += off
		}
		return n, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, errf("json_path", "invalid JSON path index %q", s)
	}
	return n, nil
}

func applyJSONPathStep(node any, step jsonPathStep) []any {
	if step.recursive {
		var out []any
		var walk func(any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				keys := make([]string, 0, len(x))
				for k := range x {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					if step.kind == "key" && k == step.key {
						out = append(out, x[k])
					}
					walk(x[k])
				}
			case []any:
				for _, e := range x {
					walk(e)
				}
			}
		}
		walk(node)
		return out
	}
	switch step.kind {
	case "key":
		if m, ok := node.(map[string]any); ok {
			if v, ok := m[step.key]; ok {
				return []any{v}
			}
		}
	case "index":
		if a, ok := node.([]any); ok {
			idx := step.index
			if idx < 0 {
				idx = len(a) + idx
			}
			if idx >= 0 && idx < len(a) {
				return []any{a[idx]}
			}
		}
	case "range":
		if a, ok := node.([]any); ok {
			start, end := step.index, step.end
			if start < 0 {
				start = len(a) + start
			}
			if end < 0 {
				end = len(a) + end
			}
			if start < 0 {
				start = 0
			}
			if end >= len(a) {
				end = len(a) - 1
			}
			if start <= end && start < len(a) {
				return append([]any(nil), a[start:end+1]...)
			}
		}
	case "wildcard":
		switch x := node.(type) {
		case []any:
			return append([]any(nil), x...)
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := make([]any, 0, len(keys))
			for _, k := range keys {
				out = append(out, x[k])
			}
			return out
		}
	}
	return nil
}

func jsonValueText(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", errf("type_error", "cannot serialize JSON: %v", err)
	}
	return string(b), nil
}

func jsonKind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "OBJECT"
	case []any:
		return "ARRAY"
	default:
		return "SCALAR"
	}
}

func jsonIsJSON(v any) (any, bool) {
	decoded, err := decodeJSONValue(v)
	if err != nil {
		return nil, false
	}
	return decoded, true
}

func jsonTypeMatches(v any, typ string) bool {
	if typ == "" || typ == "VALUE" {
		return true
	}
	return jsonKind(v) == typ
}

var _ = fmt.Sprint
