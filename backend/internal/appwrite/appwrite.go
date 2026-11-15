// Package appwrite implements a read-only Appwrite Databases connector.
package appwrite

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
		Type:        "appwrite",
		Name:        "Appwrite",
		Category:    connector.CategoryNoSQL,
		Description: "Appwrite Databases collections (REST)",
		Icon:        "appwrite",
		Maturity:    "native",
		DocsURL:     "https://appwrite.io/docs/references/cloud/server-rest/databases",
		Fields: []connector.ConfigField{
			{Key: "endpoint", Label: "API endpoint", Kind: connector.FieldText, Required: true, Placeholder: "https://cloud.appwrite.io/v1", Help: "Include /v1 for self-hosted Appwrite."},
			{Key: "project_id", Label: "Project ID", Kind: connector.FieldText, Required: true},
			{Key: "database_id", Label: "Database ID", Kind: connector.FieldText, Required: true},
			{Key: "collections", Label: "Collections", Kind: connector.FieldText, Placeholder: "users,orders", Help: "Comma-separated collection IDs. When empty, Introspect lists collections from the API."},
			{Key: "api_key", Label: "API key", Kind: connector.FieldPassword, Secret: true, Help: "Server API key with databases.read scope."},
			{Key: "jwt", Label: "JWT", Kind: connector.FieldPassword, Secret: true, Help: "Optional user JWT when no API key is used."},
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
	project := strings.TrimSpace(cfg["project_id"])
	if project == "" {
		return nil, fmt.Errorf("Appwrite project_id is required")
	}
	db := strings.TrimSpace(cfg["database_id"])
	if db == "" {
		return nil, fmt.Errorf("Appwrite database_id is required")
	}
	c := &Conn{
		base:        endpoint,
		project:     project,
		database:    db,
		apiKey:      strings.TrimSpace(cfg["api_key"]),
		jwt:         strings.TrimSpace(cfg["jwt"]),
		pageSize:    atoiDefault(cfg["page_size"], 100),
		collections: splitList(cfg["collections"]),
		client:      &http.Client{Timeout: time.Duration(atoiDefault(cfg["timeout_seconds"], 30)) * time.Second},
	}
	if c.pageSize <= 0 {
		c.pageSize = 100
	}
	if c.pageSize > 500 {
		c.pageSize = 500
	}
	if err := c.Ping(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

type Conn struct {
	base        string
	project     string
	database    string
	apiKey      string
	jwt         string
	collections []string
	pageSize    int
	client      *http.Client
}

func (c *Conn) Ping(ctx context.Context) error {
	_, _, err := c.get(ctx, c.base+"/health/version")
	return err
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	collections := c.collections
	if len(collections) == 0 {
		ids, err := c.listCollections(ctx)
		if err != nil {
			return nil, err
		}
		collections = ids
	}
	var tables []connector.Table
	for _, collection := range collections {
		rows, cols, _, err := c.fetch(ctx, collection, 1)
		if err != nil {
			return nil, err
		}
		_ = rows
		if len(cols) == 0 {
			cols = baseColumns()
		}
		tables = append(tables, connector.Table{Name: collection, DisplayName: collection, Kind: "collection", Columns: cols})
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
	return fmt.Sprintf("GET %s/databases/%s/collections/%s/documents?queries[]=limit(%d)", c.base, url.PathEscape(c.database), url.PathEscape(req.Table), c.pageSize), nil, nil
}

func (c *Conn) listCollections(ctx context.Context) ([]string, error) {
	u := c.base + "/databases/" + url.PathEscape(c.database) + "/collections?queries%5B%5D=" + url.QueryEscape("limit(100)")
	var out struct {
		Collections []struct {
			ID string `json:"$id"`
		} `json:"collections"`
	}
	if err := c.getJSON(ctx, u, &out); err != nil {
		return nil, fmt.Errorf("list Appwrite collections: %w", err)
	}
	ids := make([]string, 0, len(out.Collections))
	for _, collection := range out.Collections {
		if collection.ID != "" {
			ids = append(ids, collection.ID)
		}
	}
	return ids, nil
}

func (c *Conn) fetch(ctx context.Context, collection string, limit int) ([]map[string]any, []connector.Column, bool, error) {
	if strings.TrimSpace(collection) == "" {
		return nil, nil, false, fmt.Errorf("Appwrite collection is required")
	}
	var rows []map[string]any
	seen := map[string]string{}
	offset := 0
	total := -1
	for len(rows) < limit {
		pageSize := c.pageSize
		if pageSize > limit-len(rows) {
			pageSize = limit - len(rows)
		}
		q := url.Values{}
		q.Add("queries[]", fmt.Sprintf("limit(%d)", pageSize))
		q.Add("queries[]", fmt.Sprintf("offset(%d)", offset))
		u := c.base + "/databases/" + url.PathEscape(c.database) + "/collections/" + url.PathEscape(collection) + "/documents?" + q.Encode()
		var resp struct {
			Total     int              `json:"total"`
			Documents []map[string]any `json:"documents"`
		}
		if err := c.getJSON(ctx, u, &resp); err != nil {
			return nil, nil, false, err
		}
		total = resp.Total
		for _, doc := range resp.Documents {
			row := map[string]any{}
			if id, ok := doc["$id"]; ok {
				row["_id"] = id
			}
			if v, ok := doc["$createdAt"]; ok {
				row["_created_at"] = parseTime(v)
			}
			if v, ok := doc["$updatedAt"]; ok {
				row["_updated_at"] = parseTime(v)
			}
			if v, ok := doc["$permissions"]; ok {
				row["_permissions"] = v
			}
			for k, v := range doc {
				if strings.HasPrefix(k, "$") {
					continue
				}
				row[k] = v
			}
			addTypes(seen, row)
			rows = append(rows, row)
			if len(rows) >= limit {
				break
			}
		}
		offset += len(resp.Documents)
		if len(resp.Documents) == 0 {
			break
		}
		if resp.Total > 0 && offset >= resp.Total {
			break
		}
	}
	truncated := (total >= 0 && total > len(rows)) || (total < 0 && len(rows) >= limit)
	return rows, columnsFor(rows, seen), truncated, nil
}

func (c *Conn) getJSON(ctx context.Context, u string, out any) error {
	body, _, err := c.get(ctx, u)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode Appwrite response: %w", err)
	}
	return nil
}

func (c *Conn) get(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-Appwrite-Project", c.project)
	if c.apiKey != "" {
		req.Header.Set("X-Appwrite-Key", c.apiKey)
	} else if c.jwt != "" {
		req.Header.Set("X-Appwrite-JWT", c.jwt)
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

func baseColumns() []connector.Column {
	return []connector.Column{
		{Name: "_id", Type: "string"},
		{Name: "_created_at", Type: "timestamp", Nullable: true},
		{Name: "_updated_at", Type: "timestamp", Nullable: true},
		{Name: "_permissions", Type: "array", Nullable: true},
	}
}

func columnsFor(rows []map[string]any, seen map[string]string) []connector.Column {
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
