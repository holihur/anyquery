// Package openapi exposes GET operations from an OpenAPI 3 / Swagger 2
// document as SQL tables. The spec can be a URL or a local JSON/YAML file.
package openapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "openapi",
		Name:        "OpenAPI / Swagger",
		Category:    connector.CategoryAPI,
		Description: "GET operations from an OpenAPI 3 or Swagger 2 document",
		Icon:        "openapi",
		Maturity:    "native",
		Fields: []connector.ConfigField{
			{Key: "spec", Label: "Spec URL or file", Kind: connector.FieldTextarea, Required: true, Placeholder: "https://api.example.com/openapi.yaml", Help: "OpenAPI 3 / Swagger 2 JSON or YAML. Local paths are also supported."},
			{Key: "base_url", Label: "Base URL override", Kind: connector.FieldText, Placeholder: "https://api.example.com/v1", Help: "Optional. Defaults to servers[0].url or host/basePath."},
			{Key: "headers", Label: "API headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"Authorization":"Bearer ..."}`},
			{Key: "path_params", Label: "Path params (JSON)", Kind: connector.FieldTextarea, Placeholder: `{"owner":"acme","repo":"data"}`},
			{Key: "query_params", Label: "Query params (JSON)", Kind: connector.FieldTextarea, Placeholder: `{"limit":100}`},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	raw, err := readSpec(ctx, cfg["spec"], cfg["headers"])
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse OpenAPI document: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("OpenAPI document is empty")
	}
	headers, err := parseObject(cfg["headers"])
	if err != nil {
		return nil, fmt.Errorf("headers: %w", err)
	}
	pathParams, err := parseObject(cfg["path_params"])
	if err != nil {
		return nil, fmt.Errorf("path_params: %w", err)
	}
	queryParams, err := parseObject(cfg["query_params"])
	if err != nil {
		return nil, fmt.Errorf("query_params: %w", err)
	}
	c := &Conn{
		baseURL:     resolveBase(cfg["base_url"], cfg["spec"], doc),
		headers:     headers,
		pathParams:  pathParams,
		queryParams: queryParams,
		spec:        doc,
		client:      &http.Client{Timeout: 60 * time.Second},
	}
	c.introspect()
	return c, nil
}

type Conn struct {
	baseURL     string
	headers     map[string]any
	pathParams  map[string]any
	queryParams map[string]any
	spec        map[string]any
	tables      []connector.Table
	ops         map[string]operation
	client      *http.Client
}

type operation struct {
	path        string
	recordsPath string
	columns     []connector.Column
}

func (c *Conn) Ping(context.Context) error { return nil }
func (c *Conn) Close() error               { return nil }
func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{}
}

func (c *Conn) Introspect(context.Context) ([]connector.Table, error) {
	return c.tables, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	op, ok := c.ops[req.Table]
	if !ok {
		return nil, fmt.Errorf("unknown OpenAPI operation %q", req.Table)
	}
	requestURL, err := c.operationURL(op.path)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json, */*")
	for k, v := range c.headers {
		httpReq.Header.Set(k, fmt.Sprint(v))
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenAPI HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("response is not valid JSON: %w", err)
	}
	records := extractRecords(root, op.recordsPath)
	limit := req.Limit
	if limit <= 0 || limit > 100_000 {
		limit = 10_000
	}
	truncated := len(records) > limit
	if truncated {
		records = records[:limit]
	}
	columns := op.columns
	rows := make([]map[string]any, 0, len(records))
	for _, item := range records {
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
	if len(columns) == 0 {
		columns = inferColumns(rows)
	}
	return &connector.FetchResult{Columns: columns, Rows: rows, Truncated: truncated}, nil
}

func (c *Conn) operationURL(path string) (string, error) {
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			if _, ok := c.pathParams[name]; !ok {
				return "", fmt.Errorf("path parameter %q is required; configure path_params JSON", name)
			}
		}
	}
	for name, value := range c.pathParams {
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(fmt.Sprint(value)))
	}
	base := strings.TrimRight(c.baseURL, "/")
	if base == "" {
		return "", fmt.Errorf("base URL is required")
	}
	u, err := url.Parse(base + "/" + strings.TrimLeft(path, "/"))
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range c.queryParams {
		q.Set(k, fmt.Sprint(v))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Conn) introspect() {
	paths, _ := c.spec["paths"].(map[string]any)
	c.ops = map[string]operation{}
	seen := map[string]int{}
	for path, raw := range paths {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		op, ok := item["get"].(map[string]any)
		if !ok {
			continue
		}
		if !hasPathParams(path, c.pathParams) {
			continue
		}
		name := operationName(op, path)
		if n := seen[name]; n > 0 {
			seen[name] = n + 1
			name = fmt.Sprintf("%s_%d", name, n+1)
		} else {
			seen[name] = 1
		}
		cols, recordsPath := responseColumns(c.spec, op)
		display := stringValue(op["summary"])
		if display == "" {
			display = stringValue(op["operationId"])
		}
		if display == "" {
			display = "GET " + path
		}
		c.ops[name] = operation{path: path, recordsPath: recordsPath, columns: cols}
		c.tables = append(c.tables, connector.Table{Name: name, DisplayName: display, Kind: "api", Columns: cols})
	}
}

func hasPathParams(path string, values map[string]any) bool {
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			if _, ok := values[name]; !ok {
				return false
			}
		}
	}
	return true
}

func operationName(op map[string]any, path string) string {
	if id := sanitize(stringValue(op["operationId"])); id != "" {
		return id
	}
	p := strings.Trim(strings.ReplaceAll(path, "{", ""), "/")
	p = strings.ReplaceAll(p, "}", "")
	p = strings.NewReplacer("/", "_", "-", "_", ".", "_").Replace(p)
	if p == "" {
		p = "root"
	}
	return "get_" + strings.ToLower(p)
}

func sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else if r == '-' || r == ' ' || r == '.' {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func responseColumns(root map[string]any, op map[string]any) ([]connector.Column, string) {
	responses, _ := op["responses"].(map[string]any)
	if len(responses) == 0 {
		return nil, ""
	}
	var response map[string]any
	for _, key := range []string{"200", "201", "202", "default"} {
		if r, ok := responses[key].(map[string]any); ok {
			response = r
			break
		}
	}
	if response == nil {
		return nil, ""
	}
	var schema any
	if content, ok := response["content"].(map[string]any); ok {
		if media, ok := content["application/json"].(map[string]any); ok {
			schema = media["schema"]
		}
	}
	if schema == nil {
		schema = response["schema"] // Swagger 2
	}
	if schema == nil {
		return nil, ""
	}
	resolved := resolveRef(root, schema)
	return schemaColumns(root, resolved)
}

func schemaColumns(root map[string]any, schema any) ([]connector.Column, string) {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil, ""
	}
	if items, ok := m["items"]; ok {
		arr, _ := items.(map[string]any)
		resolved := resolveRef(root, arr)
		if obj, ok := resolved.(map[string]any); ok {
			if props, ok := obj["properties"].(map[string]any); ok {
				return propertiesColumns(root, props), ""
			}
			if t := stringValue(obj["type"]); t != "" && t != "object" {
				return []connector.Column{{Name: "value", Type: openAPIType(obj), Nullable: true}}, ""
			}
		}
		return []connector.Column{{Name: "value", Type: "string", Nullable: true}}, ""
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for name, raw := range props {
			if isArray(raw) {
				items := resolveRef(root, raw.(map[string]any)["items"])
				if obj, ok := items.(map[string]any); ok {
					if p, ok := obj["properties"].(map[string]any); ok {
						return propertiesColumns(root, p), name
					}
				}
				return []connector.Column{{Name: "value", Type: "string", Nullable: true}}, name
			}
		}
		return propertiesColumns(root, props), ""
	}
	return nil, ""
}

func propertiesColumns(root map[string]any, props map[string]any) []connector.Column {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := make([]connector.Column, 0, len(keys))
	for _, key := range keys {
		s := resolveRef(root, props[key])
		typ := "string"
		if m, ok := s.(map[string]any); ok {
			typ = openAPIType(m)
		}
		out = append(out, connector.Column{Name: key, Type: typ, Nullable: true})
	}
	return out
}

func openAPIType(s map[string]any) string {
	t := strings.ToLower(stringValue(s["type"]))
	format := strings.ToLower(stringValue(s["format"]))
	switch t {
	case "integer":
		return "integer"
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		return "array"
	case "object":
		return "object"
	case "string":
		if format == "date" || format == "date-time" {
			return "timestamp"
		}
	}
	return "string"
}

func isArray(raw any) bool {
	m, ok := raw.(map[string]any)
	return ok && stringValue(m["type"]) == "array"
}

func resolveRef(root map[string]any, raw any) any {
	seen := map[string]bool{}
	for {
		m, ok := raw.(map[string]any)
		if !ok {
			return raw
		}
		ref := stringValue(m["$ref"])
		if ref == "" {
			return m
		}
		if seen[ref] {
			return m
		}
		seen[ref] = true
		if !strings.HasPrefix(ref, "#/") {
			return m
		}
		var cur any = root
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			obj, ok := cur.(map[string]any)
			if !ok {
				return m
			}
			cur = obj[part]
		}
		raw = cur
	}
}

func resolveBase(override, specRef string, doc map[string]any) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	if servers, ok := doc["servers"].([]any); ok && len(servers) > 0 {
		if s, ok := servers[0].(map[string]any); ok {
			base := stringValue(s["url"])
			if vars, ok := s["variables"].(map[string]any); ok {
				for name, raw := range vars {
					if v, ok := raw.(map[string]any); ok {
						base = strings.ReplaceAll(base, "{"+name+"}", stringValue(v["default"]))
					}
				}
			}
			if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
				return strings.TrimRight(base, "/")
			}
			if u, err := url.Parse(specRef); err == nil && u.Scheme != "" {
				if b, err := url.Parse(base); err == nil {
					return strings.TrimRight(u.ResolveReference(b).String(), "/")
				}
			}
		}
	}
	if host := stringValue(doc["host"]); host != "" {
		scheme := "https"
		if schemes, ok := doc["schemes"].([]any); ok && len(schemes) > 0 {
			scheme = stringValue(schemes[0])
		}
		return scheme + "://" + host + stringValue(doc["basePath"])
	}
	if u, err := url.Parse(specRef); err == nil && u.Scheme != "" {
		u.Path = ""
		u.RawQuery = ""
		return strings.TrimRight(u.String(), "/")
	}
	return ""
}

func extractRecords(root any, path string) []any {
	if path != "" {
		cur := root
		for _, part := range strings.Split(path, ".") {
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = obj[part]
		}
		if arr, ok := cur.([]any); ok {
			return arr
		}
		return nil
	}
	if arr, ok := root.([]any); ok {
		return arr
	}
	if obj, ok := root.(map[string]any); ok {
		for _, key := range []string{"data", "items", "records", "results", "value", "rows"} {
			if arr, ok := obj[key].([]any); ok {
				return arr
			}
		}
		return []any{obj}
	}
	return nil
}

func inferColumns(rows []map[string]any) []connector.Column {
	seen := map[string]bool{}
	var cols []connector.Column
	for _, row := range rows {
		for k, v := range row {
			if seen[k] {
				continue
			}
			seen[k] = true
			cols = append(cols, connector.Column{Name: k, Type: inferType(v), Nullable: true})
		}
	}
	return cols
}

func inferType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer"
	case float32, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "string"
	}
}

func parseObject(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func readSpec(ctx context.Context, ref, headersRaw string) ([]byte, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
		if err != nil {
			return nil, err
		}
		headers, err := parseObject(headersRaw)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, fmt.Sprint(v))
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch OpenAPI spec: HTTP %d", resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	}
	return os.ReadFile(strings.TrimSpace(ref))
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
