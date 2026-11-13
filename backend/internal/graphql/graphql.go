// Package graphql implements a configurable GraphQL data source.
//
// GraphQL endpoints do not expose a universal table shape: a query is needed
// to select both the root field and the fields returned per row. This connector
// therefore exposes one table per configured query. Multiple named queries can
// be declared with the `queries` JSON field, or a single `query` field can be
// used for the common case.
package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/httpauth"
)

const maxResponseBytes = 64 << 20

func init() {
	connector.Register(provider{})
}

type provider struct{}

func (provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "graphql",
		Name:        "GraphQL",
		Category:    connector.CategoryAPI,
		Description: "Query a GraphQL endpoint with one or more configured queries",
		Icon:        "graphql",
		Maturity:    "native",
		Fields: []connector.ConfigField{
			{Key: "endpoint", Label: "GraphQL endpoint", Kind: connector.FieldText, Required: true, Placeholder: "https://api.example.com/graphql"},
			{Key: "headers", Label: "HTTP headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"Authorization":"Bearer ..."}`},
			{Key: "auth_type", Label: "OAuth2 flow", Kind: connector.FieldSelect, Default: "none", Options: []connector.Option{{Value: "none", Label: "None"}, {Value: "client_credentials", Label: "Client credentials"}, {Value: "refresh_token", Label: "Refresh token"}}},
			{Key: "token_url", Label: "OAuth2 token URL", Kind: connector.FieldText, Placeholder: "https://auth.example.com/oauth/token"},
			{Key: "client_id", Label: "OAuth2 client ID", Kind: connector.FieldText},
			{Key: "client_secret", Label: "OAuth2 client secret", Kind: connector.FieldPassword, Secret: true},
			{Key: "refresh_token", Label: "OAuth2 refresh token", Kind: connector.FieldPassword, Secret: true},
			{Key: "scope", Label: "OAuth2 scope", Kind: connector.FieldText, Placeholder: "read:data"},
			{Key: "queries", Label: "Named queries (JSON)", Kind: connector.FieldTextarea, Placeholder: `{"users":{"query":"query($limit:Int){users(limit:$limit){id name}}","data_path":"users","columns":{"id":"integer","name":"string"}}}`, Help: "Optional map of table name to query config. Mutually exclusive with the single-query fields below."},
			{Key: "table_name", Label: "Table name (single query)", Kind: connector.FieldText, Default: "data"},
			{Key: "query", Label: "GraphQL query (single query)", Kind: connector.FieldTextarea, Placeholder: "query($limit:Int) { users(limit:$limit) { id name } }", Help: "Only query operations are supported. Use variables for parameters."},
			{Key: "variables", Label: "Variables (JSON)", Kind: connector.FieldTextarea, Placeholder: `{"limit":100}`},
			{Key: "data_path", Label: "Data path", Kind: connector.FieldText, Placeholder: "users", Help: "Dot path inside the GraphQL data object that contains the rows. Empty auto-detects the first wrapper or single root field."},
			{Key: "columns", Label: "Columns (JSON)", Kind: connector.FieldTextarea, Placeholder: `{"id":"integer","name":"string"}`, Help: "Optional declared columns. Types: string, integer, number, boolean, timestamp, object, array."},
			{Key: "http_method", Label: "HTTP method", Kind: connector.FieldSelect, Default: "POST", Options: []connector.Option{{Value: "POST", Label: "POST"}, {Value: "GET", Label: "GET"}}},
			{Key: "limit_variable", Label: "Limit variable", Kind: connector.FieldText, Default: "limit", Help: "If the query references $limit and no value is configured, AnyQuery sets it to the requested row limit."},
			{Key: "cursor_variable", Label: "Cursor variable", Kind: connector.FieldText, Placeholder: "after", Help: "Optional variable updated after each page for cursor pagination."},
			{Key: "cursor_path", Label: "Next cursor path", Kind: connector.FieldText, Placeholder: "pageInfo.endCursor", Help: "Dot path in the GraphQL data object, or in the last row, that contains the next cursor."},
			{Key: "has_next_path", Label: "Has next page path", Kind: connector.FieldText, Placeholder: "pageInfo.hasNextPage", Help: "Optional boolean path. Pagination stops when this is false."},
			{Key: "max_pages", Label: "Max pages", Kind: connector.FieldNumber, Default: "10"},
		},
	}
}

func (provider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	endpoint := strings.TrimSpace(cfg["endpoint"])
	if err := validateURL(endpoint); err != nil {
		return nil, err
	}
	headers, err := parseStringMap(cfg["headers"], "headers")
	if err != nil {
		return nil, err
	}
	authCfg, err := httpauth.ParseConfig(cfg)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(strings.TrimSpace(cfg["http_method"]))
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodGet && method != http.MethodPost {
		return nil, fmt.Errorf("http_method must be GET or POST")
	}
	defaults := queryConfig{
		Variables:     nil,
		DataPath:      strings.TrimSpace(cfg["data_path"]),
		LimitVariable: strings.TrimSpace(cfg["limit_variable"]),
		CursorVar:     strings.TrimSpace(cfg["cursor_variable"]),
		CursorPath:    strings.TrimSpace(cfg["cursor_path"]),
		HasNextPath:   strings.TrimSpace(cfg["has_next_path"]),
		MaxPages:      intValue(cfg["max_pages"], 10),
	}
	if defaults.LimitVariable == "" {
		defaults.LimitVariable = "limit"
	}
	if raw := strings.TrimSpace(cfg["variables"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &defaults.Variables); err != nil {
			return nil, fmt.Errorf("variables must be a JSON object: %w", err)
		}
		if defaults.Variables == nil {
			defaults.Variables = map[string]any{}
		}
	}
	if raw := strings.TrimSpace(cfg["columns"]); raw != "" {
		cols, err := parseColumnsJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("columns: %w", err)
		}
		defaults.Columns = cols
	}
	queries, err := parseQueries(cfg, defaults)
	if err != nil {
		return nil, err
	}
	g := &Conn{
		endpoint: endpoint,
		headers:  headers,
		auth:     httpauth.NewProvider(authCfg),
		method:   method,
		queries:  queries,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
	for name, q := range queries {
		g.tables = append(g.tables, connector.Table{
			Name:        name,
			DisplayName: name,
			Kind:        "api",
			Columns:     q.Columns,
		})
	}
	sort.Slice(g.tables, func(i, j int) bool { return g.tables[i].Name < g.tables[j].Name })
	return g, nil
}

type Conn struct {
	endpoint string
	headers  map[string]string
	auth     *httpauth.Provider
	method   string
	queries  map[string]queryConfig
	tables   []connector.Table
	client   *http.Client
}

type queryConfig struct {
	Query         string
	Variables     map[string]any
	DataPath      string
	Columns       []connector.Column
	LimitVariable string
	CursorVar     string
	CursorPath    string
	HasNextPath   string
	MaxPages      int
}

func (c *Conn) Ping(ctx context.Context) error {
	_, err := c.execute(ctx, "query { __typename }", nil)
	return err
}

func (c *Conn) Introspect(context.Context) ([]connector.Table, error) {
	out := make([]connector.Table, len(c.tables))
	copy(out, c.tables)
	return out, nil
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{}
}

func (c *Conn) Close() error { return nil }

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	q, ok := c.queries[req.Table]
	if !ok {
		return nil, fmt.Errorf("unknown GraphQL table %q", req.Table)
	}
	limit := req.Limit
	if limit <= 0 || limit > 100_000 {
		limit = 10_000
	}
	maxPages := q.MaxPages
	if maxPages <= 0 {
		maxPages = 10
	}
	if maxPages > 100 {
		maxPages = 100
	}
	vars := cloneMap(q.Variables)
	if q.LimitVariable != "" && strings.Contains(q.Query, "$"+q.LimitVariable) {
		if _, exists := vars[q.LimitVariable]; !exists {
			vars[q.LimitVariable] = limit
		}
	}
	var rows []map[string]any
	columns := q.Columns
	cursor := ""
	truncated := false
	pages := 0
	for pages < maxPages && len(rows) < limit {
		if cursor != "" && q.CursorVar != "" {
			vars[q.CursorVar] = cursor
		}
		data, err := c.execute(ctx, q.Query, vars)
		if err != nil {
			return nil, err
		}
		records := extractRecords(data, q.DataPath)
		if len(records) == 0 {
			break
		}
		pages++
		for _, item := range records {
			if len(rows) >= limit {
				truncated = true
				break
			}
			switch x := item.(type) {
			case map[string]any:
				rows = append(rows, x)
			case nil:
				continue
			default:
				key := "value"
				if len(columns) > 0 {
					key = columns[0].Name
				}
				rows = append(rows, map[string]any{key: x})
			}
		}
		if len(rows) >= limit {
			break
		}
		if q.CursorVar == "" || q.CursorPath == "" {
			break
		}
		if q.HasNextPath != "" {
			if next, ok := pathValue(data, q.HasNextPath); ok {
				if b, ok := next.(bool); ok && !b {
					break
				}
			}
		}
		next, ok := pageValue(data, rows, q.CursorPath)
		if !ok {
			break
		}
		nextCursor := stringValue(next)
		if nextCursor == "" || nextCursor == cursor {
			break
		}
		cursor = nextCursor
		if pages >= maxPages {
			truncated = true
		}
	}
	if len(rows) >= limit {
		truncated = true
	}
	if len(columns) == 0 {
		columns = inferColumns(rows)
	}
	connector.NormalizeRows(rows)
	return &connector.FetchResult{Columns: columns, Rows: rows, Truncated: truncated}, nil
}

// DescribeFetch returns the GraphQL document and variables that Fetch would
// send. It does not perform a request.
func (c *Conn) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	q, ok := c.queries[req.Table]
	if !ok {
		return "", nil, fmt.Errorf("unknown GraphQL table %q", req.Table)
	}
	vars := cloneMap(q.Variables)
	limit := req.Limit
	if limit <= 0 {
		limit = 10_000
	}
	if q.LimitVariable != "" && strings.Contains(q.Query, "$"+q.LimitVariable) {
		if _, exists := vars[q.LimitVariable]; !exists {
			vars[q.LimitVariable] = limit
		}
	}
	if len(vars) == 0 {
		return q.Query, nil, nil
	}
	return q.Query, []any{vars}, nil
}

func parseQueries(cfg map[string]string, defaults queryConfig) (map[string]queryConfig, error) {
	out := map[string]queryConfig{}
	if raw := strings.TrimSpace(cfg["queries"]); raw != "" {
		var declared map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &declared); err != nil {
			return nil, fmt.Errorf("queries must be a JSON object: %w", err)
		}
		for name, value := range declared {
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, fmt.Errorf("queries contains an empty table name")
			}
			q := defaults
			q.Variables = cloneMap(defaults.Variables)
			var asString string
			if err := json.Unmarshal(value, &asString); err == nil {
				q.Query = asString
			} else {
				var m map[string]any
				if err := json.Unmarshal(value, &m); err != nil {
					return nil, fmt.Errorf("query %q must be a string or object", name)
				}
				if v, ok := m["query"].(string); ok {
					q.Query = v
				}
				if v, ok := m["variables"]; ok {
					if obj, ok := v.(map[string]any); ok {
						q.Variables = obj
					} else {
						return nil, fmt.Errorf("query %q variables must be an object", name)
					}
				}
				if v, ok := m["data_path"].(string); ok {
					q.DataPath = v
				}
				if v, ok := m["columns"]; ok {
					cols, err := parseColumnsValue(v)
					if err != nil {
						return nil, fmt.Errorf("query %q columns: %w", name, err)
					}
					q.Columns = cols
				}
				if v, ok := m["limit_variable"].(string); ok {
					q.LimitVariable = v
				}
				if v, ok := m["cursor_variable"].(string); ok {
					q.CursorVar = v
				}
				if v, ok := m["cursor_path"].(string); ok {
					q.CursorPath = v
				}
				if v, ok := m["has_next_path"].(string); ok {
					q.HasNextPath = v
				}
				if v, ok := m["max_pages"]; ok {
					q.MaxPages = intValue(fmt.Sprint(v), q.MaxPages)
				}
			}
			if strings.TrimSpace(q.Query) == "" {
				return nil, fmt.Errorf("query %q is empty", name)
			}
			if err := validateQuery(q.Query); err != nil {
				return nil, fmt.Errorf("query %q: %w", name, err)
			}
			out[name] = q
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("queries must contain at least one table")
		}
		return out, nil
	}
	if strings.TrimSpace(cfg["query"]) == "" {
		return nil, fmt.Errorf("either queries or query is required")
	}
	name := strings.TrimSpace(cfg["table_name"])
	if name == "" {
		name = "data"
	}
	defaults.Query = cfg["query"]
	if defaults.LimitVariable == "" {
		defaults.LimitVariable = "limit"
	}
	if err := validateQuery(defaults.Query); err != nil {
		return nil, err
	}
	out[name] = defaults
	return out, nil
}

func validateQuery(query string) error {
	trimmed := strings.TrimSpace(strings.TrimPrefix(query, "\ufeff"))
	for strings.HasPrefix(trimmed, "#") {
		if i := strings.IndexByte(trimmed, '\n'); i >= 0 {
			trimmed = strings.TrimSpace(trimmed[i+1:])
		} else {
			trimmed = ""
		}
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "mutation") || strings.HasPrefix(lower, "subscription") {
		return fmt.Errorf("only GraphQL query operations are supported")
	}
	if !strings.HasPrefix(lower, "query") && !strings.HasPrefix(trimmed, "{") {
		return fmt.Errorf("query must start with query or {")
	}
	return nil
}

func (c *Conn) execute(ctx context.Context, query string, variables map[string]any) (any, error) {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var req *http.Request
		if c.method == http.MethodGet {
			u, err := url.Parse(c.endpoint)
			if err != nil {
				return nil, err
			}
			qv := u.Query()
			qv.Set("query", query)
			if len(variables) > 0 {
				vb, _ := json.Marshal(variables)
				qv.Set("variables", string(vb))
			}
			u.RawQuery = qv.Encode()
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
			if err != nil {
				return nil, err
			}
		} else {
			req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		if c.auth != nil {
			authHeader, err := c.auth.Authorization(ctx)
			if err != nil {
				return nil, err
			}
			if authHeader != "" {
				req.Header.Set("Authorization", authHeader)
			}
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode == http.StatusUnauthorized && c.auth != nil && attempt == 0 {
			c.auth.Invalidate()
			continue
		}
		if (resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504) && attempt < 2 {
			wait := time.Duration(500*(attempt+1)) * time.Millisecond
			if retry := resp.Header.Get("Retry-After"); retry != "" {
				if seconds, err := strconv.Atoi(strings.TrimSpace(retry)); err == nil && seconds > 0 && seconds <= 10 {
					wait = time.Duration(seconds) * time.Second
				}
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("GraphQL HTTP %d: %s", resp.StatusCode, snippet(body))
		}
		var env struct {
			Data   any              `json:"data"`
			Errors []map[string]any `json:"errors"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("GraphQL response is not valid JSON: %w", err)
		}
		if len(env.Errors) > 0 {
			var messages []string
			for _, e := range env.Errors {
				if m := stringValue(e["message"]); m != "" {
					messages = append(messages, m)
				}
			}
			if len(messages) == 0 {
				messages = append(messages, "unknown GraphQL error")
			}
			lastErr = fmt.Errorf("GraphQL error: %s", strings.Join(messages, "; "))
			return nil, lastErr
		}
		return env.Data, nil
	}
	return nil, lastErr
}

func extractRecords(data any, dataPath string) []any {
	value := data
	if p := normalizePath(dataPath); p != "" {
		if v, ok := pathValue(data, p); ok {
			value = v
			if obj, ok := value.(map[string]any); ok {
				if arr, ok := wrapperArray(obj); ok {
					value = arr
				} else if len(obj) == 1 {
					for _, v := range obj {
						if arr, ok := v.([]any); ok {
							value = arr
						}
					}
				}
			}
		} else {
			return nil
		}
	} else {
		// Auto-detect the common single-root-field response shape.
		for i := 0; i < 4; i++ {
			obj, ok := value.(map[string]any)
			if !ok {
				break
			}
			if arr, ok := firstArray(obj); ok {
				value = arr
				break
			}
			if len(obj) == 1 {
				for _, v := range obj {
					value = v
				}
				continue
			}
			break
		}
	}
	switch x := value.(type) {
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			if obj, ok := item.(map[string]any); ok {
				if node, ok := obj["node"].(map[string]any); ok && len(obj) <= 2 {
					out = append(out, node)
					continue
				}
			}
			out = append(out, item)
		}
		return out
	case map[string]any:
		return []any{x}
	case nil:
		return nil
	default:
		return []any{x}
	}
}

func firstArray(obj map[string]any) ([]any, bool) {
	if arr, ok := wrapperArray(obj); ok {
		return arr, true
	}
	// A common GraphQL wrapper is { users: [...], pageInfo: {...} }; when
	// there is exactly one array field, use it without requiring data_path.
	var found []any
	for _, key := range sortedKeys(obj) {
		if arr, ok := obj[key].([]any); ok {
			if found != nil {
				return nil, false
			}
			found = arr
		}
	}
	return found, found != nil
}

func wrapperArray(obj map[string]any) ([]any, bool) {
	for _, key := range []string{"nodes", "items", "records", "results", "rows", "data", "edges"} {
		if arr, ok := obj[key].([]any); ok {
			return arr, true
		}
	}
	return nil, false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func pageValue(data any, rows []map[string]any, path string) (any, bool) {
	if v, ok := pathValue(data, path); ok {
		return v, true
	}
	if len(rows) > 0 {
		return pathValue(rows[len(rows)-1], path)
	}
	return nil, false
}

func pathValue(root any, path string) (any, bool) {
	cur := root
	for _, part := range strings.Split(normalizePath(path), ".") {
		if part == "" {
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func normalizePath(path string) string {
	p := strings.TrimSpace(path)
	p = strings.TrimPrefix(p, "data.")
	p = strings.TrimPrefix(p, ".")
	return p
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func parseColumnsJSON(raw string) ([]connector.Column, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("must be a JSON object or array: %w", err)
	}
	return parseColumnsValue(v)
}

func parseColumnsValue(v any) ([]connector.Column, error) {
	switch x := v.(type) {
	case map[string]any:
		names := make([]string, 0, len(x))
		for name := range x {
			names = append(names, name)
		}
		sort.Strings(names)
		out := make([]connector.Column, 0, len(names))
		for _, name := range names {
			out = append(out, connector.Column{Name: name, Type: strings.ToLower(stringValue(x[name])), Nullable: true})
		}
		return out, nil
	case []any:
		out := make([]connector.Column, 0, len(x))
		for _, item := range x {
			if name := stringValue(item); name != "" {
				out = append(out, connector.Column{Name: name, Type: "string", Nullable: true})
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("must be an object mapping column names to types, or an array of names")
	}
}

func inferColumns(rows []map[string]any) []connector.Column {
	types := map[string]string{}
	nullable := map[string]bool{}
	for _, row := range rows {
		for k, v := range row {
			if v == nil {
				nullable[k] = true
				continue
			}
			t := valueType(v)
			if prev, ok := types[k]; ok && prev != t {
				types[k] = "string"
			} else if !ok {
				types[k] = t
			}
		}
	}
	names := make([]string, 0, len(types))
	for name := range types {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]connector.Column, 0, len(names))
	for _, name := range names {
		out = append(out, connector.Column{Name: name, Type: types[name], Nullable: nullable[name]})
	}
	return out
}

func valueType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer"
	case float32, float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "string"
	}
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint must start with http:// or https://")
	}
	return nil
}

func parseStringMap(raw, name string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object of strings: %w", name, err)
	}
	return out, nil
}

func intValue(raw string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		return n
	}
	return def
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
