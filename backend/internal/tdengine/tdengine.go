// Package tdengine connects TDengine's REST SQL API.
package tdengine

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
	"strconv"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "tdengine",
		Name:        "TDengine",
		Category:    connector.CategoryTimeSeries,
		Description: "TDengine time-series SQL over the REST API",
		Icon:        "tdengine",
		Fields: []connector.ConfigField{
			{Key: "url", Label: "REST endpoint", Kind: connector.FieldText, Required: true, Placeholder: "http://localhost:6041"},
			{Key: "database", Label: "Database", Kind: connector.FieldText, Required: true, Placeholder: "power"},
			{Key: "username", Label: "Username", Kind: connector.FieldText, Default: "root"},
			{Key: "password", Label: "Password", Kind: connector.FieldPassword, Secret: true, Default: "taosdata"},
			{Key: "token", Label: "Cloud token", Kind: connector.FieldPassword, Secret: true, Help: "If set, sent as a Bearer token instead of Basic auth."},
			{Key: "insecure", Label: "Skip TLS verification", Kind: connector.FieldBool, Default: "false"},
		},
	}
}

func (Provider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	u, err := url.Parse(strings.TrimSpace(cfg["url"]))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("REST endpoint must start with http:// or https://")
	}
	if strings.TrimSpace(cfg["database"]) == "" {
		return nil, fmt.Errorf("database is required")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if strings.EqualFold(cfg["insecure"], "true") {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	c := &Conn{
		base:    strings.TrimRight(u.String(), "/"),
		cfg:     cfg,
		client:  &http.Client{Timeout: 60 * time.Second, Transport: tr},
		schemas: map[string][]connector.Column{},
	}
	if err := c.Ping(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

type Conn struct {
	base    string
	cfg     map[string]string
	client  *http.Client
	schemas map[string][]connector.Column
}

type response struct {
	Code       int     `json:"code"`
	Desc       string  `json:"desc"`
	ColumnMeta [][]any `json:"column_meta"`
	Data       [][]any `json:"data"`
	Rows       int64   `json:"rows"`
}

func (c *Conn) do(ctx context.Context, query string) (*response, error) {
	endpoint := c.base + "/rest/sql/" + url.PathEscape(c.cfg["database"])
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/plain")
	if c.cfg["token"] != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg["token"])
	} else {
		req.SetBasicAuth(c.cfg["username"], c.cfg["password"])
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("TDengine request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("TDengine HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	var parsed response
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("TDengine response: %w: %s", err, snippet(body))
	}
	if parsed.Code != 0 {
		return nil, fmt.Errorf("TDengine error %d: %s", parsed.Code, parsed.Desc)
	}
	return &parsed, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func (c *Conn) Ping(ctx context.Context) error {
	_, err := c.do(ctx, "SELECT SERVER_VERSION()")
	return err
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}
func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	resp, err := c.do(ctx, "SHOW TABLES")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, row := range resp.Data {
		if len(row) > 0 {
			names = append(names, fmt.Sprint(row[0]))
		}
	}
	sort.Strings(names)
	if len(names) > 200 {
		names = names[:200]
	}
	c.schemas = map[string][]connector.Column{}
	tables := make([]connector.Table, 0, len(names))
	for _, name := range names {
		desc, err := c.do(ctx, "DESCRIBE "+quoteIdent(name))
		if err != nil {
			continue
		}
		var cols []connector.Column
		for _, row := range desc.Data {
			if len(row) < 2 {
				continue
			}
			col := connector.Column{Name: fmt.Sprint(row[0]), Type: tdType(fmt.Sprint(row[1])), Nullable: true}
			cols = append(cols, col)
		}
		c.schemas[name] = cols
		tables = append(tables, connector.Table{Name: name, DisplayName: name, Kind: "table", Columns: cols})
	}
	return tables, nil
}

func tdType(t string) string {
	t = strings.ToUpper(t)
	switch {
	case strings.HasPrefix(t, "TIMESTAMP"):
		return "timestamp"
	case strings.HasPrefix(t, "INT"), strings.HasPrefix(t, "BIGINT"), strings.HasPrefix(t, "SMALLINT"), strings.HasPrefix(t, "TINYINT"):
		return "integer"
	case strings.HasPrefix(t, "FLOAT"), strings.HasPrefix(t, "DOUBLE"), strings.HasPrefix(t, "DECIMAL"):
		return "number"
	case strings.HasPrefix(t, "BOOL"):
		return "boolean"
	case strings.HasPrefix(t, "JSON"):
		return "json"
	default:
		return "string"
	}
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > 100000 {
		limit = 100000
	}
	resp, err := c.do(ctx, "SELECT * FROM "+quoteIdent(req.Table)+" LIMIT "+strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	cols := c.schemas[req.Table]
	if cols == nil {
		for _, meta := range resp.ColumnMeta {
			if len(meta) >= 2 {
				cols = append(cols, connector.Column{Name: fmt.Sprint(meta[0]), Type: tdType(fmt.Sprint(meta[1]))})
			}
		}
	}
	rows := make([]map[string]any, 0, len(resp.Data))
	for _, raw := range resp.Data {
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			if i >= len(raw) {
				row[col.Name] = nil
				continue
			}
			row[col.Name] = convertTD(raw[i], col.Type)
		}
		rows = append(rows, row)
	}
	return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: len(rows) >= limit}, nil
}

func convertTD(v any, typ string) any {
	if v == nil {
		return nil
	}
	switch typ {
	case "integer":
		switch x := v.(type) {
		case float64:
			return int64(x)
		case string:
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				return n
			}
		}
	case "number":
		switch x := v.(type) {
		case string:
			if f, err := strconv.ParseFloat(x, 64); err == nil {
				return f
			}
		}
	case "boolean":
		if s, ok := v.(string); ok {
			if b, err := strconv.ParseBool(s); err == nil {
				return b
			}
		}
	case "timestamp":
		if f, ok := v.(float64); ok {
			return time.UnixMilli(int64(f)).UTC().Format(time.RFC3339Nano)
		}
	}
	return v
}

func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}
