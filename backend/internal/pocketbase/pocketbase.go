// Package pocketbase implements a read-only PocketBase records connector.
package pocketbase

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
		Type:        "pocketbase",
		Name:        "PocketBase",
		Category:    connector.CategoryNoSQL,
		Description: "PocketBase collections and records (REST)",
		Icon:        "pocketbase",
		Maturity:    "native",
		DocsURL:     "https://pocketbase.io/docs/api-records/",
		Fields: []connector.ConfigField{
			{Key: "endpoint", Label: "API endpoint", Kind: connector.FieldText, Required: true, Placeholder: "http://127.0.0.1:8090"},
			{Key: "token", Label: "Admin or user token", Kind: connector.FieldPassword, Secret: true, Help: "Bearer token with read access to the collections."},
			{Key: "collections", Label: "Collections", Kind: connector.FieldText, Placeholder: "posts,users", Help: "Comma-separated collection names/IDs. When empty, Introspect lists collections."},
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
	c := &Conn{
		base:        endpoint,
		token:       strings.TrimSpace(cfg["token"]),
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
	token       string
	collections []string
	pageSize    int
	client      *http.Client
}

func (c *Conn) Ping(ctx context.Context) error {
	_, _, err := c.get(ctx, c.base+"/api/health")
	return err
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	collections := c.collections
	if len(collections) == 0 {
		names, err := c.listCollections(ctx)
		if err != nil {
			return nil, err
		}
		collections = names
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
	return fmt.Sprintf("GET %s/api/collections/%s/records?page=N&perPage=%d", c.base, url.PathEscape(req.Table), c.pageSize), nil, nil
}

func (c *Conn) listCollections(ctx context.Context) ([]string, error) {
	var out struct {
		Items []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, c.base+"/api/collections?perPage=200", &out); err != nil {
		return nil, fmt.Errorf("list PocketBase collections: %w", err)
	}
	var names []string
	for _, collection := range out.Items {
		if collection.Name != "" {
			names = append(names, collection.Name)
		} else if collection.ID != "" {
			names = append(names, collection.ID)
		}
	}
	return names, nil
}

func (c *Conn) fetch(ctx context.Context, collection string, limit int) ([]map[string]any, []connector.Column, bool, error) {
	if strings.TrimSpace(collection) == "" {
		return nil, nil, false, fmt.Errorf("PocketBase collection is required")
	}
	var rows []map[string]any
	seen := map[string]string{}
	page := 1
	total := 0
	perPage := c.pageSize
	if perPage > limit {
		perPage = limit
	}
	for len(rows) < limit {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("perPage", strconv.Itoa(perPage))
		u := c.base + "/api/collections/" + url.PathEscape(collection) + "/records?" + q.Encode()
		var resp struct {
			Page       int              `json:"page"`
			PerPage    int              `json:"perPage"`
			TotalItems int              `json:"totalItems"`
			TotalPages int              `json:"totalPages"`
			Items      []map[string]any `json:"items"`
		}
		if err := c.getJSON(ctx, u, &resp); err != nil {
			return nil, nil, false, err
		}
		total = resp.TotalItems
		for _, item := range resp.Items {
			row := map[string]any{}
			if v, ok := item["id"]; ok {
				row["_id"] = v
			}
			if v, ok := item["created"]; ok {
				row["_created_at"] = parseTime(v)
			}
			if v, ok := item["updated"]; ok {
				row["_updated_at"] = parseTime(v)
			}
			if v, ok := item["collectionId"]; ok {
				row["_collection_id"] = v
			}
			if v, ok := item["collectionName"]; ok {
				row["_collection_name"] = v
			}
			for k, v := range item {
				switch k {
				case "id", "created", "updated", "collectionId", "collectionName":
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
		if len(resp.Items) == 0 {
			break
		}
		if len(rows) >= limit {
			break
		}
		if resp.TotalPages > 0 && page >= resp.TotalPages {
			break
		}
		page++
	}
	truncated := total > len(rows) || (total == 0 && len(rows) >= limit)
	return rows, columnsFor(rows, seen), truncated, nil
}

func (c *Conn) getJSON(ctx context.Context, u string, out any) error {
	body, _, err := c.get(ctx, u)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode PocketBase response: %w", err)
	}
	return nil
}

func (c *Conn) get(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
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
		{Name: "_collection_id", Type: "string", Nullable: true},
		{Name: "_collection_name", Type: "string", Nullable: true},
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
