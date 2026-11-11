// Package httpjson implements generic REST/JSON and CSV URL connectors. These
// make it possible to query internal APIs and hosted files without writing a
// new connector: the endpoint and a small amount of config are enough.
package httpjson

import (
	"context"
	"encoding/csv"
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
)

func init() {
	connector.Register(restProvider{})
	connector.Register(csvProvider{})
}

const maxResponseBytes = 64 << 20

func httpClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL must start with http:// or https://")
	}
	return nil
}

func parseHeaders(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("headers must be a JSON object of strings: %w", err)
	}
	return out, nil
}

func doRequest(ctx context.Context, rawURL, headersRaw string) ([]byte, error) {
	if err := validateURL(rawURL); err != nil {
		return nil, err
	}
	headers, err := parseHeaders(headersRaw)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/csv;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", "AnyQuery/0.1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(body)
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	return body, nil
}

// ---- REST ------------------------------------------------------------------

type restProvider struct{}

func (restProvider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "rest",
		Name:        "REST / JSON API",
		Category:    connector.CategoryAPI,
		Description: "Any JSON HTTP endpoint (one table per endpoint)",
		Icon:        "rest",
		Fields: []connector.ConfigField{
			{Key: "url", Label: "Endpoint URL", Kind: connector.FieldTextarea, Required: true, Placeholder: "https://api.example.com/v1/orders", Help: "GET should return a JSON array, or an object containing one (set Records path)."},
			{Key: "records_path", Label: "Records path", Kind: connector.FieldText, Placeholder: "data.items", Help: "Dot path to the array inside the JSON response. Leave empty for a root array."},
			{Key: "table_name", Label: "Table name", Kind: connector.FieldText, Required: true, Default: "records"},
			{Key: "headers", Label: "HTTP headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"Authorization":"Bearer ..."}`},
		},
	}
}

func (restProvider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	if err := validateURL(cfg["url"]); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg["table_name"]) == "" {
		cfg["table_name"] = "records"
	}
	return &restConn{cfg: cfg}, nil
}

type restConn struct{ cfg map[string]string }

func (c *restConn) Ping(ctx context.Context) error {
	_, err := c.rows(ctx, 10)
	return err
}

func (c *restConn) Capabilities() connector.Capabilities { return connector.Capabilities{LimitPushdown: true} }
func (c *restConn) Close() error                        { return nil }

func (c *restConn) Introspect(ctx context.Context) ([]connector.Table, error) {
	rows, err := c.rows(ctx, 100)
	if err != nil {
		return nil, err
	}
	cols := inferColumns(rows)
	return []connector.Table{{Name: c.cfg["table_name"], DisplayName: c.cfg["table_name"], Kind: "api", Columns: cols}}, nil
}

func (c *restConn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	rows, err := c.rows(ctx, req.Limit)
	if err != nil {
		return nil, err
	}
	truncated := false
	if req.Limit > 0 && len(rows) > req.Limit {
		rows = rows[:req.Limit]
		truncated = true
	}
	cols := inferColumns(rows)
	return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: truncated}, nil
}

func (c *restConn) rows(ctx context.Context, limit int) ([]map[string]any, error) {
	body, err := doRequest(ctx, c.cfg["url"], c.cfg["headers"])
	if err != nil {
		return nil, err
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("response is not valid JSON: %w", err)
	}
	arr, err := extractArray(root, c.cfg["records_path"])
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100_000 {
		limit = 100_000
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if len(out) >= limit {
			break
		}
		switch x := item.(type) {
		case map[string]any:
			out = append(out, flattenJSON(x))
		default:
			out = append(out, map[string]any{"value": x})
		}
	}
	return out, nil
}

func extractArray(root any, path string) ([]any, error) {
	cur := root
	if strings.TrimSpace(path) != "" {
		p := strings.TrimPrefix(strings.TrimSpace(path), "$")
		p = strings.TrimPrefix(p, ".")
		for _, part := range strings.Split(p, ".") {
			if part == "" {
				continue
			}
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("records path %q does not point to an array", path)
			}
			cur, ok = m[part]
			if !ok {
				return nil, fmt.Errorf("records path %q not found in response", path)
			}
		}
	}
	if arr, ok := cur.([]any); ok {
		return arr, nil
	}
	if m, ok := cur.(map[string]any); ok {
		for _, key := range []string{"data", "items", "records", "results", "rows", "values"} {
			if arr, ok := m[key].([]any); ok {
				return arr, nil
			}
		}
	}
	return nil, fmt.Errorf("response does not contain a JSON array; set the records path")
}

func flattenJSON(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case nil, string, bool, float64:
			out[k] = x
		case json.Number:
			out[k] = x
		case map[string]any, []any:
			b, _ := json.Marshal(x)
			out[k] = string(b)
		default:
			out[k] = fmt.Sprint(x)
		}
	}
	return out
}

func inferColumns(rows []map[string]any) []connector.Column {
	type acc struct {
		col      connector.Column
		nonNull  bool
	}
	m := map[string]*acc{}
	for _, row := range rows {
		for k, v := range row {
			a, ok := m[k]
			if !ok {
				a = &acc{col: connector.Column{Name: k, Nullable: true}}
				m[k] = a
			}
			if v == nil {
				continue
			}
			a.nonNull = true
			t := valueType(v)
			if a.col.Type == "" || a.col.Type == "null" {
				a.col.Type = t
			} else if a.col.Type != t {
				a.col.Type = "string"
			}
		}
	}
	var cols []connector.Column
	for _, a := range m {
		a.col.Nullable = !a.nonNull
		cols = append(cols, a.col)
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })
	return cols
}

func valueType(v any) string {
	switch x := v.(type) {
	case bool:
		return "boolean"
	case float64:
		if x == float64(int64(x)) {
			return "integer"
		}
		return "number"
	case json.Number:
		if _, err := x.Int64(); err == nil {
			return "integer"
		}
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

// ---- CSV -------------------------------------------------------------------

type csvProvider struct{}

func (csvProvider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "csv",
		Name:        "CSV / URL",
		Category:    connector.CategoryFile,
		Description: "A CSV/TSV file served over HTTP",
		Icon:        "csv",
		Fields: []connector.ConfigField{
			{Key: "url", Label: "File URL", Kind: connector.FieldTextarea, Required: true, Placeholder: "https://example.com/data/orders.csv"},
			{Key: "table_name", Label: "Table name", Kind: connector.FieldText, Required: true, Default: "data"},
			{Key: "delimiter", Label: "Delimiter", Kind: connector.FieldSelect, Default: ",", Options: []connector.Option{{Value: ",", Label: "Comma"}, {Value: ";", Label: "Semicolon"}, {Value: "\t", Label: "Tab"}, {Value: "|", Label: "Pipe"}}},
			{Key: "headers", Label: "HTTP headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"Authorization":"Bearer ..."}`},
		},
	}
}

func (csvProvider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	if err := validateURL(cfg["url"]); err != nil {
		return nil, err
	}
	if cfg["table_name"] == "" {
		cfg["table_name"] = "data"
	}
	if cfg["delimiter"] == "" {
		cfg["delimiter"] = ","
	}
	return &csvConn{cfg: cfg}, nil
}

type csvConn struct{ cfg map[string]string }

func (c *csvConn) Ping(ctx context.Context) error {
	_, err := c.rows(ctx, 1)
	return err
}

func (c *csvConn) Capabilities() connector.Capabilities { return connector.Capabilities{} }
func (c *csvConn) Close() error                        { return nil }

func (c *csvConn) Introspect(ctx context.Context) ([]connector.Table, error) {
	rows, err := c.rows(ctx, 1000)
	if err != nil {
		return nil, err
	}
	return []connector.Table{{Name: c.cfg["table_name"], DisplayName: c.cfg["table_name"], Kind: "file", Columns: inferColumns(rows)}}, nil
}

func (c *csvConn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	rows, err := c.rows(ctx, req.Limit)
	if err != nil {
		return nil, err
	}
	truncated := false
	if req.Limit > 0 && len(rows) > req.Limit {
		rows = rows[:req.Limit]
		truncated = true
	}
	return &connector.FetchResult{Columns: inferColumns(rows), Rows: rows, Truncated: truncated}, nil
}

func (c *csvConn) rows(ctx context.Context, limit int) ([]map[string]any, error) {
	body, err := doRequest(ctx, c.cfg["url"], c.cfg["headers"])
	if err != nil {
		return nil, err
	}
	delim := c.cfg["delimiter"]
	if delim == "" {
		delim = ","
	}
	r := csv.NewReader(strings.NewReader(string(body)))
	r.Comma = []rune(delim)[0]
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := records[0]
	if limit <= 0 || limit > 500_000 {
		limit = 500_000
	}
	var out []map[string]any
	for _, rec := range records[1:] {
		if len(out) >= limit {
			break
		}
		row := make(map[string]any, len(header))
		for i, name := range header {
			if i < len(rec) {
				row[name] = parseCSVValue(rec[i])
			} else {
				row[name] = nil
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func parseCSVValue(s string) any {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	if i, err := strconv.ParseInt(t, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(t); err == nil {
		return b
	}
	return s
}
