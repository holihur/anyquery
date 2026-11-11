// Package elasticsearch implements Elasticsearch and OpenSearch connectors over
// their shared HTTP API.
package elasticsearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() {
	connector.Register(Provider{typ: "elasticsearch", name: "Elasticsearch", icon: "elasticsearch"})
	connector.Register(Provider{typ: "opensearch", name: "OpenSearch", icon: "opensearch"})
}

type Provider struct {
	typ  string
	name string
	icon string
}

func (p Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        p.typ,
		Name:        p.name,
		Category:    connector.CategorySearch,
		Description: p.name + " indices queried like tables",
		Icon:        p.icon,
		Fields: []connector.ConfigField{
			{Key: "url", Label: "Cluster URL", Kind: connector.FieldText, Required: true, Placeholder: "https://search.example.com:9200"},
			{Key: "index", Label: "Index / pattern", Kind: connector.FieldText, Required: true, Placeholder: "logs-*"},
			{Key: "username", Label: "Username", Kind: connector.FieldText},
			{Key: "password", Label: "Password", Kind: connector.FieldPassword, Secret: true},
			{Key: "api_key", Label: "API key", Kind: connector.FieldPassword, Secret: true, Help: "Encoded API key; sent as Authorization: ApiKey ..."},
			{Key: "insecure", Label: "Skip TLS verification", Kind: connector.FieldBool, Default: "false"},
		},
	}
}

func (p Provider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	u, err := url.Parse(strings.TrimSpace(cfg["url"]))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("cluster URL must start with http:// or https://")
	}
	if strings.TrimSpace(cfg["index"]) == "" {
		return nil, fmt.Errorf("index or index pattern is required")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if strings.EqualFold(cfg["insecure"], "true") {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Conn{cfg: cfg, client: &http.Client{Timeout: 60 * time.Second, Transport: tr}}, nil
}

type Conn struct {
	cfg    map[string]string
	client *http.Client
}

func (c *Conn) base() string { return strings.TrimRight(c.cfg["url"], "/") }

func (c *Conn) request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := c.cfg["api_key"]; key != "" {
		req.Header.Set("Authorization", "ApiKey "+key)
	} else if c.cfg["username"] != "" {
		req.SetBasicAuth(c.cfg["username"], c.cfg["password"])
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(data)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	return data, nil
}

func (c *Conn) Ping(ctx context.Context) error {
	_, err := c.request(ctx, http.MethodGet, "/", nil)
	return err
}

func (c *Conn) Capabilities() connector.Capabilities { return connector.Capabilities{LimitPushdown: true} }
func (c *Conn) Close() error                        { return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	idx := url.PathEscape(c.cfg["index"])
	data, err := c.request(ctx, http.MethodGet, "/"+idx+"/_mapping", nil)
	if err != nil {
		return nil, err
	}
	var parsed map[string]struct {
		Mappings struct {
			Properties map[string]mappingField `json:"properties"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse mapping: %w", err)
	}
	cols := []connector.Column{{Name: "_id", Type: "string", Nullable: false}, {Name: "_index", Type: "string", Nullable: false}}
	seen := map[string]bool{"_id": true, "_index": true}
	for _, m := range parsed {
		for name, f := range m.Mappings.Properties {
			cols = append(cols, connector.Column{Name: name, Type: mapType(f.Type), Nullable: true})
			seen[name] = true
		}
	}
	sort.Slice(cols, func(i, j int) bool {
		if cols[i].Name == "_id" {
			return true
		}
		if cols[j].Name == "_id" {
			return false
		}
		return cols[i].Name < cols[j].Name
	})
	return []connector.Table{{Name: c.cfg["index"], DisplayName: c.cfg["index"], Kind: "index", Columns: cols}}, nil
}

type mappingField struct {
	Type       string                  `json:"type"`
	Properties map[string]mappingField `json:"properties"`
}

func mapType(t string) string {
	switch t {
	case "long", "integer", "short", "byte", "unsigned_long":
		return "integer"
	case "float", "double", "half_float", "scaled_float":
		return "number"
	case "boolean":
		return "boolean"
	case "date", "date_nanos":
		return "timestamp"
	case "object", "nested":
		return "object"
	case "binary":
		return "binary"
	default:
		return "string"
	}
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 1000
	}
	truncated := false
	if limit > 10000 {
		limit = 10000
		truncated = true
	}
	body, _ := json.Marshal(map[string]any{
		"size":  limit,
		"query": map[string]any{"match_all": map[string]any{}},
	})
	idx := url.PathEscape(req.Table)
	if req.Table == "" {
		idx = url.PathEscape(c.cfg["index"])
	}
	data, err := c.request(ctx, http.MethodPost, "/"+idx+"/_search", body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Hits struct {
			Hits []struct {
				ID     string         `json:"_id"`
				Index  string         `json:"_index"`
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}
	acc := map[string]*connector.Column{}
	var rows []map[string]any
	for _, hit := range parsed.Hits.Hits {
		row := make(map[string]any, len(hit.Source)+2)
		row["_id"] = hit.ID
		row["_index"] = hit.Index
		for k, v := range hit.Source {
			if m, ok := v.(map[string]any); ok {
				b, _ := json.Marshal(m)
				row[k] = string(b)
				continue
			}
			if a, ok := v.([]any); ok {
				b, _ := json.Marshal(a)
				row[k] = string(b)
				continue
			}
			row[k] = v
		}
		for k, v := range row {
			if _, ok := acc[k]; !ok {
				acc[k] = &connector.Column{Name: k, Type: inferType(v), Nullable: true}
			}
		}
		rows = append(rows, row)
	}
	var cols []connector.Column
	for _, col := range acc {
		cols = append(cols, *col)
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })
	return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: truncated || len(rows) >= limit}, nil
}

func inferType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x == float64(int64(x)) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	default:
		return "string"
	}
}
