// Package sls connects Alibaba Cloud Log Service (SLS) projects to AnyQuery.
//
// Each logstore is exposed as a table. With an index configured, rows come
// from SLS SQL (`* | SELECT * LIMIT n`); otherwise search mode uses the plain
// `*` search API (max 100 rows per request). The connector is read-only.
package sls

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	slssdk "github.com/aliyun/aliyun-log-go-sdk"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "sls",
		Name:        "Alibaba Cloud SLS",
		Category:    connector.CategorySearch,
		Description: "Alibaba Cloud Log Service projects and logstores via SQL/search",
		Icon:        "sls",
		Fields: []connector.ConfigField{
			{Key: "endpoint", Label: "Endpoint", Kind: connector.FieldText, Required: true, Placeholder: "cn-hangzhou.log.aliyuncs.com", Help: "Region endpoint without a path."},
			{Key: "access_key_id", Label: "AccessKey ID", Kind: connector.FieldText, Required: true},
			{Key: "access_key_secret", Label: "AccessKey secret", Kind: connector.FieldPassword, Required: true, Secret: true},
			{Key: "security_token", Label: "STS token", Kind: connector.FieldPassword, Secret: true, Help: "Optional for temporary STS credentials."},
			{Key: "project", Label: "Project", Kind: connector.FieldText, Required: true, Placeholder: "my-log-project"},
			{Key: "logstore", Label: "Logstore", Kind: connector.FieldText, Help: "Leave empty to expose every logstore in the project."},
			{Key: "query_mode", Label: "Query mode", Kind: connector.FieldSelect, Default: "sql", Options: []connector.Option{{Value: "sql", Label: "SQL (index required)"}, {Value: "search", Label: "Search (max 100 rows/request)"}}},
			{Key: "lookback_minutes", Label: "Lookback window (minutes)", Kind: connector.FieldNumber, Default: "60"},
			{Key: "max_rows", Label: "Maximum rows per query", Kind: connector.FieldNumber, Default: "10000"},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	endpoint := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(cfg["endpoint"]), "https://"), "http://")
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" || cfg["project"] == "" || cfg["access_key_id"] == "" || cfg["access_key_secret"] == "" {
		return nil, fmt.Errorf("endpoint, project, AccessKey ID and secret are required")
	}
	client := slssdk.CreateNormalInterface(endpoint, cfg["access_key_id"], cfg["access_key_secret"], cfg["security_token"])
	if c, ok := client.(*slssdk.Client); ok {
		c.RequestTimeOut = 60 * time.Second
		c.RetryTimeOut = 2 * time.Minute
	}
	c := &Conn{
		client:   client,
		project:  cfg["project"],
		logstore: strings.TrimSpace(cfg["logstore"]),
		mode:     strings.ToLower(cfg["query_mode"]),
		lookback: atoiDefault(cfg["lookback_minutes"], 60),
		maxRows:  atoiDefault(cfg["max_rows"], 10000),
		schemas:  map[string][]connector.Column{},
	}
	if c.mode == "" {
		c.mode = "sql"
	}
	if c.lookback <= 0 {
		c.lookback = 60
	}
	if c.lookback > 7*24*60 {
		c.lookback = 7 * 24 * 60
	}
	if c.maxRows <= 0 || c.maxRows > 100000 {
		c.maxRows = 10000
	}
	pingCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return c, nil
}

type Conn struct {
	client   slssdk.ClientInterface
	project  string
	logstore string
	mode     string
	lookback int
	maxRows  int
	schemas  map[string][]connector.Column
}

func (c *Conn) Ping(context.Context) error {
	ok, err := c.client.CheckProjectExist(c.project)
	if err != nil {
		return fmt.Errorf("SLS project check failed: %w", err)
	}
	if !ok {
		return fmt.Errorf("SLS project %q not found or not accessible", c.project)
	}
	return nil
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return c.client.Close() }

func (c *Conn) Introspect(context.Context) ([]connector.Table, error) {
	names := []string{}
	if c.logstore != "" {
		names = []string{c.logstore}
	} else {
		list, err := c.client.ListLogStore(c.project)
		if err != nil {
			return nil, fmt.Errorf("list logstores: %w", err)
		}
		names = list
		sort.Strings(names)
	}
	if len(names) > 200 {
		names = names[:200]
	}
	c.schemas = map[string][]connector.Column{}
	tables := make([]connector.Table, 0, len(names))
	for _, name := range names {
		cols := c.columnsFor(name)
		c.schemas[name] = cols
		tables = append(tables, connector.Table{Name: name, DisplayName: name, Kind: "logstore", Columns: cols})
	}
	return tables, nil
}

func (c *Conn) columnsFor(logstore string) []connector.Column {
	base := []connector.Column{
		{Name: "__time__", Type: "timestamp", Nullable: false},
		{Name: "__source__", Type: "string", Nullable: true},
		{Name: "__topic__", Type: "string", Nullable: true},
	}
	idx, err := c.client.GetIndex(c.project, logstore)
	if err != nil || idx == nil || len(idx.Keys) == 0 {
		return base
	}
	seen := map[string]bool{"__time__": true, "__source__": true, "__topic__": true}
	cols := append([]connector.Column{}, base...)
	for key, spec := range idx.Keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		cols = append(cols, connector.Column{Name: key, Type: slsType(spec.Type), Nullable: true})
	}
	sort.Slice(cols[3:], func(i, j int) bool { return cols[3+i].Name < cols[3+j].Name })
	return cols
}

func slsType(t string) string {
	switch strings.ToLower(t) {
	case "long", "int", "integer":
		return "integer"
	case "double", "float":
		return "number"
	case "bool", "boolean":
		return "boolean"
	case "json":
		return "json"
	default:
		return "string"
	}
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	limit := req.Limit
	if limit <= 0 || limit > c.maxRows {
		limit = c.maxRows
	}
	cols := c.schemas[req.Table]
	if cols == nil {
		cols = c.columnsFor(req.Table)
		c.schemas[req.Table] = cols
	}
	now := time.Now().Unix()
	from := now - int64(c.lookback*60)
	query := "*"
	lines := int64(100)
	offset := int64(0)
	if c.mode == "sql" {
		query = fmt.Sprintf("* | SELECT * LIMIT %d", limit)
		lines = int64(limit)
	} else if limit < 100 {
		lines = int64(limit)
	}
	response, err := c.client.GetLogsV2(c.project, req.Table, &slssdk.GetLogRequest{
		From: from, To: now, Query: query, Lines: lines, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("query SLS logstore %s: %w", req.Table, err)
	}
	rows := make([]map[string]any, 0, len(response.Logs))
	for _, log := range response.Logs {
		row := make(map[string]any, len(log))
		for key, value := range log {
			row[key] = c.convertValue(key, value, cols)
		}
		rows = append(rows, row)
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return &connector.FetchResult{
		Columns:   cols,
		Rows:      rows,
		Truncated: int64(len(rows)) >= lines || response.Count > int64(len(rows)),
	}, nil
}

func (c *Conn) convertValue(key, value string, cols []connector.Column) any {
	if key == "__time__" {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return time.Unix(n, 0).UTC().Format(time.RFC3339Nano)
		}
		return value
	}
	for _, col := range cols {
		if col.Name != key {
			continue
		}
		switch col.Type {
		case "integer":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				return n
			}
		case "number":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				return f
			}
		case "boolean":
			if b, err := strconv.ParseBool(value); err == nil {
				return b
			}
		}
		break
	}
	return value
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
