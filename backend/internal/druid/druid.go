// Package druid connects Apache Druid's SQL HTTP endpoint.
package druid

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
		Type:        "druid",
		Name:        "Apache Druid",
		Category:    connector.CategoryTimeSeries,
		Description: "Apache Druid datasources via the SQL endpoint",
		Icon:        "druid",
		Fields: []connector.ConfigField{
			{Key: "url", Label: "Broker URL", Kind: connector.FieldText, Required: true, Placeholder: "http://localhost:8888"},
			{Key: "username", Label: "Username", Kind: connector.FieldText},
			{Key: "password", Label: "Password", Kind: connector.FieldPassword, Secret: true},
			{Key: "insecure", Label: "Skip TLS verification", Kind: connector.FieldBool, Default: "false"},
		},
	}
}

func (Provider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	u, err := url.Parse(strings.TrimSpace(cfg["url"]))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("broker URL must start with http:// or https://")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if strings.EqualFold(cfg["insecure"], "true") {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	c := &Conn{base: strings.TrimRight(u.String(), "/"), cfg: cfg, client: &http.Client{Timeout: 60 * time.Second, Transport: tr}, schemas: map[string][]connector.Column{}}
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

func (c *Conn) query(ctx context.Context, sql string) ([]map[string]any, error) {
	body, _ := json.Marshal(map[string]any{"query": sql, "resultFormat": "object"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/druid/v2/sql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg["username"] != "" {
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
		return nil, fmt.Errorf("Druid HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("Druid response: %w: %s", err, snippet(data))
	}
	return rows, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func (c *Conn) Ping(ctx context.Context) error {
	_, err := c.query(ctx, "SELECT 1 AS ok")
	return err
}
func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}
func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	rows, err := c.query(ctx, `SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, DATA_TYPE FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA NOT IN ('INFORMATION_SCHEMA') ORDER BY TABLE_SCHEMA, TABLE_NAME, ORDINAL_POSITION`)
	if err != nil {
		return nil, err
	}
	byName := map[string]*connector.Table{}
	for _, row := range rows {
		schema := fmt.Sprint(row["TABLE_SCHEMA"])
		table := fmt.Sprint(row["TABLE_NAME"])
		key := table
		if schema != "" && !strings.EqualFold(schema, "druid") {
			key = schema + "." + table
		}
		t, ok := byName[key]
		if !ok {
			t = &connector.Table{Name: key, DisplayName: table, Schema: schema, Kind: "datasource"}
			byName[key] = t
		}
		typeName := fmt.Sprint(row["DATA_TYPE"])
		t.Columns = append(t.Columns, connector.Column{Name: fmt.Sprint(row["COLUMN_NAME"]), Type: druidType(typeName), Nullable: true})
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 200 {
		names = names[:200]
	}
	out := make([]connector.Table, 0, len(names))
	for _, name := range names {
		t := byName[name]
		c.schemas[name] = t.Columns
		out = append(out, *t)
	}
	return out, nil
}

func druidType(t string) string {
	t = strings.ToLower(t)
	switch {
	case strings.Contains(t, "bigint"), strings.Contains(t, "integer"), strings.Contains(t, "smallint"), strings.Contains(t, "tinyint"):
		return "integer"
	case strings.Contains(t, "double"), strings.Contains(t, "float"), strings.Contains(t, "decimal"), strings.Contains(t, "real"):
		return "number"
	case strings.Contains(t, "boolean"):
		return "boolean"
	case strings.Contains(t, "timestamp"), strings.Contains(t, "date"), strings.Contains(t, "time"):
		return "timestamp"
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
	sql := "SELECT * FROM " + quoteQualified(req.Table) + " LIMIT " + strconv.Itoa(limit)
	rows, err := c.query(ctx, sql)
	if err != nil {
		return nil, err
	}
	cols := c.schemas[req.Table]
	if cols == nil {
		cols = inferColumns(rows)
	}
	out := make([]map[string]any, 0, len(rows))
	for i, row := range rows {
		if i >= limit {
			break
		}
		out = append(out, stringifyNested(row))
	}
	return &connector.FetchResult{Columns: cols, Rows: out, Truncated: len(rows) >= limit}, nil
}

func quoteQualified(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
	}
	return strings.Join(parts, ".")
}

func stringifyNested(row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	for k, v := range row {
		switch x := v.(type) {
		case map[string]any, []any:
			b, _ := json.Marshal(x)
			out[k] = string(b)
		default:
			out[k] = v
		}
	}
	return out
}

func inferColumns(rows []map[string]any) []connector.Column {
	seen := map[string]string{}
	for _, row := range rows {
		for k, v := range row {
			switch v.(type) {
			case float64:
				seen[k] = "number"
			case bool:
				seen[k] = "boolean"
			default:
				if seen[k] == "" {
					seen[k] = "string"
				}
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	cols := make([]connector.Column, len(names))
	for i, name := range names {
		cols[i] = connector.Column{Name: name, Type: seen[name]}
	}
	return cols
}
