// Package parse implements a read-only Parse Platform (and Back4App) REST
// connector. Parse classes map to tables and Parse object fields map to columns.
package parse

import (
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
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "parse",
		Name:        "Parse Platform / Back4App",
		Category:    connector.CategoryNoSQL,
		Description: "Parse Platform classes and objects (REST)",
		Icon:        "parse",
		Maturity:    "native",
		DocsURL:     "https://docs.parseplatform.org/rest/guide/",
		Fields: []connector.ConfigField{
			{Key: "endpoint", Label: "Parse endpoint", Kind: connector.FieldText, Required: true, Placeholder: "https://parseapi.back4app.com", Help: "Parse Server mount path, e.g. http://localhost:1337/parse or Back4App's parseapi host."},
			{Key: "app_id", Label: "Application ID", Kind: connector.FieldText, Required: true},
			{Key: "rest_api_key", Label: "REST API key", Kind: connector.FieldPassword, Secret: true},
			{Key: "master_key", Label: "Master key", Kind: connector.FieldPassword, Secret: true, Help: "Optional; enables /schemas introspection. Prefer a read-only key."},
			{Key: "session_token", Label: "Session token", Kind: connector.FieldPassword, Secret: true, Help: "Optional user session token for ACL-protected classes."},
			{Key: "classes", Label: "Classes", Kind: connector.FieldText, Placeholder: "_User,Post", Help: "Comma-separated Parse class names. When empty, Introspect uses /schemas (master key required)."},
			{Key: "page_size", Label: "Page size", Kind: connector.FieldNumber, Default: "100"},
			{Key: "timeout_seconds", Label: "Timeout (seconds)", Kind: connector.FieldNumber, Default: "30"},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg["endpoint"]), "/")
	if err := validateHTTPURL(endpoint); err != nil {
		return nil, err
	}
	appID := strings.TrimSpace(cfg["app_id"])
	if appID == "" {
		return nil, fmt.Errorf("Parse application ID is required")
	}
	c := &Conn{
		base:      endpoint,
		appID:     appID,
		restKey:   strings.TrimSpace(cfg["rest_api_key"]),
		masterKey: strings.TrimSpace(cfg["master_key"]),
		session:   strings.TrimSpace(cfg["session_token"]),
		classes:   splitList(cfg["classes"]),
		pageSize:  atoiDefault(cfg["page_size"], 100),
		client:    &http.Client{Timeout: time.Duration(atoiDefault(cfg["timeout_seconds"], 30)) * time.Second},
	}
	if c.pageSize <= 0 {
		c.pageSize = 100
	}
	if c.pageSize > 1000 {
		c.pageSize = 1000
	}
	if err := c.Ping(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

type Conn struct {
	base      string
	appID     string
	restKey   string
	masterKey string
	session   string
	classes   []string
	pageSize  int
	client    *http.Client
}

func (c *Conn) Ping(ctx context.Context) error {
	_, _, err := c.get(ctx, c.base+"/health")
	return err
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	classes := c.classes
	if len(classes) == 0 {
		names, err := c.listClasses(ctx)
		if err != nil {
			return nil, err
		}
		classes = names
	}
	var tables []connector.Table
	for _, class := range classes {
		rows, cols, _, err := c.fetch(ctx, class, 1)
		if err != nil {
			return nil, err
		}
		_ = rows
		if len(cols) == 0 {
			cols = baseColumns()
		}
		tables = append(tables, connector.Table{Name: class, DisplayName: class, Kind: "collection", Columns: cols})
	}
	return tables, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = c.pageSize
	}
	rows, cols, truncated, err := c.fetch(ctx, req.Table, limit)
	if err != nil {
		return nil, err
	}
	return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: truncated}, nil
}

func (c *Conn) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	return fmt.Sprintf("GET %s/classes/%s?limit=%d&skip=N", c.base, url.PathEscape(req.Table), c.pageSize), nil, nil
}

func (c *Conn) listClasses(ctx context.Context) ([]string, error) {
	if c.masterKey == "" {
		return nil, fmt.Errorf("Parse classes are required unless master_key is set for /schemas")
	}
	var out struct {
		Results []struct {
			ClassName string `json:"className"`
		} `json:"results"`
	}
	if err := c.getJSON(ctx, c.base+"/schemas", &out); err != nil {
		return nil, fmt.Errorf("list Parse classes: %w", err)
	}
	names := make([]string, 0, len(out.Results))
	for _, result := range out.Results {
		if result.ClassName != "" {
			names = append(names, result.ClassName)
		}
	}
	return names, nil
}

func (c *Conn) fetch(ctx context.Context, class string, limit int) ([]map[string]any, []connector.Column, bool, error) {
	if strings.TrimSpace(class) == "" {
		return nil, nil, false, fmt.Errorf("Parse class is required")
	}
	var rows []map[string]any
	seen := map[string]string{}
	skip := 0
	total := -1
	for len(rows) < limit {
		pageSize := c.pageSize
		if pageSize > limit-len(rows) {
			pageSize = limit - len(rows)
		}
		q := url.Values{}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("skip", strconv.Itoa(skip))
		q.Set("count", "1")
		u := c.base + "/classes/" + url.PathEscape(class) + "?" + q.Encode()
		var resp struct {
			Results []map[string]any `json:"results"`
			Count   int              `json:"count"`
		}
		if err := c.getJSON(ctx, u, &resp); err != nil {
			return nil, nil, false, err
		}
		if resp.Count > 0 {
			total = resp.Count
		}
		for _, item := range resp.Results {
			row := map[string]any{}
			if v, ok := item["objectId"]; ok {
				row["_id"] = v
			}
			if v, ok := item["createdAt"]; ok {
				row["_created_at"] = parseTime(v)
			}
			if v, ok := item["updatedAt"]; ok {
				row["_updated_at"] = parseTime(v)
			}
			if v, ok := item["ACL"]; ok {
				row["_acl"] = v
			}
			for k, v := range item {
				switch k {
				case "objectId", "createdAt", "updatedAt", "ACL":
					continue
				}
				row[k] = decodeValue(v)
			}
			addTypes(seen, row)
			rows = append(rows, row)
			if len(rows) >= limit {
				break
			}
		}
		skip += len(resp.Results)
		if len(resp.Results) == 0 {
			break
		}
		if total >= 0 && skip >= total {
			break
		}
	}
	truncated := total > len(rows) || (total < 0 && len(rows) >= limit)
	return rows, columnsFor(seen), truncated, nil
}

func (c *Conn) getJSON(ctx context.Context, u string, out any) error {
	body, _, err := c.get(ctx, u)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode Parse response: %w", err)
	}
	return nil
}

func (c *Conn) get(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-Parse-Application-Id", c.appID)
	if c.restKey != "" {
		req.Header.Set("X-Parse-REST-API-Key", c.restKey)
	}
	if c.masterKey != "" {
		req.Header.Set("X-Parse-Master-Key", c.masterKey)
	}
	if c.session != "" {
		req.Header.Set("X-Parse-Session-Token", c.session)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return nil, resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	return body, resp.StatusCode, nil
}

func decodeValue(v any) any {
	m, ok := v.(map[string]any)
	if ok {
		if typ, _ := m["__type"].(string); typ == "Date" {
			if t, ok := parseTime(m["iso"]).(time.Time); ok {
				return t
			}
		}
		out := make(map[string]any, len(m))
		for k, item := range m {
			out[k] = decodeValue(item)
		}
		return out
	}
	if a, ok := v.([]any); ok {
		out := make([]any, len(a))
		for i, item := range a {
			out[i] = decodeValue(item)
		}
		return out
	}
	return v
}

func baseColumns() []connector.Column {
	return []connector.Column{
		{Name: "_id", Type: "string"},
		{Name: "_created_at", Type: "timestamp", Nullable: true},
		{Name: "_updated_at", Type: "timestamp", Nullable: true},
		{Name: "_acl", Type: "object", Nullable: true},
	}
}

func columnsFor(seen map[string]string) []connector.Column {
	out := append([]connector.Column(nil), baseColumns()...)
	added := map[string]bool{}
	for _, col := range out {
		added[col.Name] = true
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		if !added[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, connector.Column{Name: key, Type: seen[key], Nullable: true})
	}
	return out
}

func addTypes(seen map[string]string, row map[string]any) {
	for key, value := range row {
		typ := "string"
		switch value.(type) {
		case nil:
			typ = "null"
		case bool:
			typ = "boolean"
		case int, int64, float64:
			typ = "number"
		case map[string]any:
			typ = "object"
		case []any:
			typ = "array"
		case time.Time:
			typ = "timestamp"
		}
		if old, ok := seen[key]; !ok || old == "null" {
			seen[key] = typ
		}
	}
}

func parseTime(v any) any {
	s, ok := v.(string)
	if !ok || s == "" {
		return v
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return s
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("endpoint must start with http:// or https://")
	}
	return nil
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
