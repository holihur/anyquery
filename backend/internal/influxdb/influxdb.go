// Package influxdb connects InfluxDB 1.x (and 2.x InfluxQL compatibility
// endpoints) to AnyQuery. Each measurement is exposed as a table.
package influxdb

import (
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

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "influxdb",
		Name:        "InfluxDB",
		Category:    connector.CategoryTimeSeries,
		Description: "InfluxDB measurements via InfluxQL (1.x / 2.x compatibility)",
		Icon:        "influxdb",
		Fields: []connector.ConfigField{
			{Key: "url", Label: "URL", Kind: connector.FieldText, Required: true, Placeholder: "http://localhost:8086"},
			{Key: "database", Label: "Database", Kind: connector.FieldText, Required: true, Placeholder: "metrics"},
			{Key: "org", Label: "Organization (2.x)", Kind: connector.FieldText, Help: "Used with InfluxDB 2.x InfluxQL compatibility."},
			{Key: "auth_mode", Label: "Authentication", Kind: connector.FieldSelect, Default: "basic", Options: []connector.Option{{Value: "basic", Label: "Username / password"}, {Value: "token", Label: "API token"}}},
			{Key: "username", Label: "Username", Kind: connector.FieldText},
			{Key: "password", Label: "Password", Kind: connector.FieldPassword, Secret: true},
			{Key: "token", Label: "API token", Kind: connector.FieldPassword, Secret: true},
			{Key: "retention_policy", Label: "Retention policy", Kind: connector.FieldText, Help: "Optional, for InfluxDB 1.x."},
			{Key: "lookback_minutes", Label: "Lookback window (minutes)", Kind: connector.FieldNumber, Default: "60"},
			{Key: "insecure", Label: "Skip TLS verification", Kind: connector.FieldBool, Default: "false"},
		},
	}
}

func (Provider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	base, err := url.Parse(strings.TrimSpace(cfg["url"]))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("URL must start with http:// or https://")
	}
	if strings.TrimSpace(cfg["database"]) == "" {
		return nil, fmt.Errorf("database is required")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if strings.EqualFold(cfg["insecure"], "true") {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	c := &Conn{
		base:     strings.TrimRight(base.String(), "/"),
		cfg:      cfg,
		client:   &http.Client{Timeout: 60 * time.Second, Transport: tr},
		lookback: atoiDefault(cfg["lookback_minutes"], 60),
		schemas:  map[string][]connector.Column{},
	}
	if c.lookback <= 0 {
		c.lookback = 60
	}
	if c.lookback > 7*24*60 {
		c.lookback = 7 * 24 * 60
	}
	if err := c.Ping(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

type Conn struct {
	base     string
	cfg      map[string]string
	client   *http.Client
	lookback int
	schemas  map[string][]connector.Column
}

func (c *Conn) Ping(ctx context.Context) error {
	_, err := c.query(ctx, "SHOW MEASUREMENTS LIMIT 1")
	return err
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return nil }

type influxResponse struct {
	Results []influxResult `json:"results"`
	Error   string         `json:"error"`
}

type influxResult struct {
	StatementID int            `json:"statement_id"`
	Series      []influxSeries `json:"series"`
	Error       string         `json:"error"`
}

type influxSeries struct {
	Name    string            `json:"name"`
	Tags    map[string]string `json:"tags"`
	Columns []string          `json:"columns"`
	Values  [][]any           `json:"values"`
}

func (c *Conn) query(ctx context.Context, statement string) ([]influxSeries, error) {
	u, err := url.Parse(c.base + "/query")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("db", c.cfg["database"])
	q.Set("q", statement)
	if c.cfg["retention_policy"] != "" {
		q.Set("rp", c.cfg["retention_policy"])
	}
	if c.cfg["org"] != "" {
		q.Set("org", c.cfg["org"])
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if strings.EqualFold(c.cfg["auth_mode"], "token") && c.cfg["token"] != "" {
		req.Header.Set("Authorization", "Token "+c.cfg["token"])
	} else if c.cfg["username"] != "" {
		req.SetBasicAuth(c.cfg["username"], c.cfg["password"])
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("influx query: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("influx HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	var parsed influxResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("influx response: %w: %s", err, snippet(body))
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("influx: %s", parsed.Error)
	}
	if len(parsed.Results) == 0 {
		return nil, fmt.Errorf("influx: empty response")
	}
	if parsed.Results[0].Error != "" {
		return nil, fmt.Errorf("influx: %s", parsed.Results[0].Error)
	}
	return parsed.Results[0].Series, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	measurements, err := c.measurements(ctx)
	if err != nil {
		return nil, err
	}
	fieldSeries, _ := c.query(ctx, "SHOW FIELD KEYS")
	tagSeries, _ := c.query(ctx, "SHOW TAG KEYS")
	fieldsBy := map[string]map[string]string{}
	for _, s := range fieldSeries {
		fields := map[string]string{}
		for _, row := range s.Values {
			if len(row) >= 2 {
				fields[fmt.Sprint(row[0])] = fmt.Sprint(row[1])
			}
		}
		fieldsBy[s.Name] = fields
	}
	tagsBy := map[string][]string{}
	for _, s := range tagSeries {
		var tags []string
		for _, row := range s.Values {
			if len(row) > 0 {
				tags = append(tags, fmt.Sprint(row[0]))
			}
		}
		tagsBy[s.Name] = tags
	}
	if len(measurements) > 200 {
		measurements = measurements[:200]
	}
	c.schemas = map[string][]connector.Column{}
	tables := make([]connector.Table, 0, len(measurements))
	for _, name := range measurements {
		cols := []connector.Column{{Name: "time", Type: "timestamp", Nullable: false}}
		for _, tag := range tagsBy[name] {
			cols = append(cols, connector.Column{Name: tag, Type: "string", Nullable: true})
		}
		fieldNames := make([]string, 0, len(fieldsBy[name]))
		for field := range fieldsBy[name] {
			fieldNames = append(fieldNames, field)
		}
		sort.Strings(fieldNames)
		for _, field := range fieldNames {
			cols = append(cols, connector.Column{Name: field, Type: influxType(fieldsBy[name][field]), Nullable: true})
		}
		c.schemas[name] = cols
		tables = append(tables, connector.Table{Name: name, DisplayName: name, Kind: "measurement", Columns: cols})
	}
	return tables, nil
}

func (c *Conn) measurements(ctx context.Context) ([]string, error) {
	series, err := c.query(ctx, "SHOW MEASUREMENTS")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range series {
		for _, row := range s.Values {
			if len(row) > 0 {
				out = append(out, fmt.Sprint(row[0]))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func influxType(t string) string {
	switch strings.ToLower(t) {
	case "float", "double":
		return "number"
	case "integer", "int", "unsigned":
		return "integer"
	case "boolean", "bool":
		return "boolean"
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
	statement := fmt.Sprintf(`SELECT * FROM %s WHERE time > now() - %dm ORDER BY time DESC LIMIT %d`,
		quoteIdent(req.Table), c.lookback, limit)
	series, err := c.query(ctx, statement)
	if err != nil {
		return nil, err
	}
	cols := c.schemas[req.Table]
	if cols == nil {
		cols = []connector.Column{{Name: "time", Type: "timestamp"}}
	}
	rows := make([]map[string]any, 0, limit)
	truncated := false
	for _, s := range series {
		colIndex := map[string]int{}
		for i, col := range s.Columns {
			colIndex[col] = i
		}
		for _, values := range s.Values {
			if len(rows) >= limit {
				truncated = true
				break
			}
			row := make(map[string]any, len(s.Columns)+len(s.Tags))
			for k, v := range s.Tags {
				row[k] = v
			}
			for i, col := range s.Columns {
				if i >= len(values) {
					continue
				}
				row[col] = influxValue(col, values[i], cols)
			}
			_ = colIndex
			rows = append(rows, row)
		}
		if truncated {
			break
		}
	}
	return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: truncated || len(rows) >= limit}, nil
}

func influxValue(column string, value any, cols []connector.Column) any {
	for _, col := range cols {
		if col.Name != column {
			continue
		}
		switch col.Type {
		case "integer":
			if f, ok := value.(float64); ok {
				return int64(f)
			}
		case "string":
			if value != nil {
				return fmt.Sprint(value)
			}
		}
		break
	}
	return value
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
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
