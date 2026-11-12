// Package cassandra exposes Apache Cassandra / ScyllaDB keyspaces via gocql.
package cassandra

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gocql/gocql"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "cassandra",
		Name:        "Apache Cassandra / ScyllaDB",
		Category:    connector.CategoryNoSQL,
		Description: "Cassandra and ScyllaDB keyspaces via CQL",
		Icon:        "cassandra",
		Maturity:    "native",
		Fields: []connector.ConfigField{
			{Key: "host", Label: "Contact points", Kind: connector.FieldText, Required: true, Placeholder: "cassandra.example.com"},
			{Key: "port", Label: "Port", Kind: connector.FieldNumber, Default: "9042"},
			{Key: "keyspace", Label: "Keyspace", Kind: connector.FieldText, Required: true, Placeholder: "analytics"},
			{Key: "username", Label: "Username", Kind: connector.FieldText},
			{Key: "password", Label: "Password", Kind: connector.FieldPassword, Secret: true},
			{Key: "local_dc", Label: "Local datacenter", Kind: connector.FieldText, Placeholder: "datacenter1"},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	hosts := splitHosts(cfg["host"])
	if len(hosts) == 0 || strings.TrimSpace(cfg["keyspace"]) == "" {
		return nil, fmt.Errorf("host and keyspace are required")
	}
	cluster := gocql.NewCluster(hosts...)
	if port, err := strconv.Atoi(cfg["port"]); err == nil && port > 0 {
		cluster.Port = port
	}
	cluster.Keyspace = strings.TrimSpace(cfg["keyspace"])
	cluster.Timeout = 15 * time.Second
	cluster.ConnectTimeout = 15 * time.Second
	cluster.Consistency = gocql.LocalQuorum
	if cfg["username"] != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{Username: cfg["username"], Password: cfg["password"]}
	}
	if cfg["local_dc"] != "" {
		cluster.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(gocql.DCAwareRoundRobinPolicy(cfg["local_dc"]))
	}
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("connect Cassandra: %w", err)
	}
	c := &Conn{session: session, keyspace: cluster.Keyspace}
	if err := c.Ping(ctx); err != nil {
		session.Close()
		return nil, err
	}
	return c, nil
}

type Conn struct {
	session  *gocql.Session
	keyspace string
}

func (c *Conn) Ping(ctx context.Context) error {
	var version string
	if err := c.session.Query("SELECT release_version FROM system.local").WithContext(ctx).Scan(&version); err != nil {
		return fmt.Errorf("Cassandra ping: %w", err)
	}
	return nil
}
func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true, ProjectionPushdown: true}
}
func (c *Conn) Close() error { c.session.Close(); return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	iter := c.session.Query("SELECT table_name FROM system_schema.tables WHERE keyspace_name = ?", c.keyspace).WithContext(ctx).Iter()
	var names []string
	var name string
	for iter.Scan(&name) {
		names = append(names, name)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	out := make([]connector.Table, 0, len(names))
	for _, table := range names {
		cols, err := c.columns(ctx, table)
		if err != nil {
			return nil, err
		}
		out = append(out, connector.Table{Name: table, DisplayName: table, Kind: "table", Columns: cols})
	}
	return out, nil
}

func (c *Conn) columns(ctx context.Context, table string) ([]connector.Column, error) {
	iter := c.session.Query("SELECT column_name, type FROM system_schema.columns WHERE keyspace_name = ? AND table_name = ?", c.keyspace, table).WithContext(ctx).Iter()
	var cols []connector.Column
	var name, typ string
	for iter.Scan(&name, &typ) {
		cols = append(cols, connector.Column{Name: name, Type: cqlType(typ), Nullable: true})
	}
	return cols, iter.Close()
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 || limit > 100_000 {
		limit = 10_000
	}
	cols := "*"
	if len(req.Columns) > 0 {
		quoted := make([]string, len(req.Columns))
		for i, col := range req.Columns {
			quoted[i] = quote(col)
		}
		cols = strings.Join(quoted, ", ")
	}
	table := req.Table
	if i := strings.LastIndexByte(table, '.'); i >= 0 {
		table = table[i+1:]
	}
	q := fmt.Sprintf("SELECT %s FROM %s.%s LIMIT %d", cols, quote(c.keyspace), quote(table), limit)
	iter := c.session.Query(q).WithContext(ctx).Iter()
	var columns []connector.Column
	for _, col := range iter.Columns() {
		columns = append(columns, connector.Column{Name: col.Name, Type: cqlType(col.TypeInfo.Type().String()), Nullable: true})
	}
	var rows []map[string]any
	for {
		row := map[string]any{}
		if !iter.MapScan(row) {
			break
		}
		for k, v := range row {
			row[k] = normalize(v)
		}
		rows = append(rows, row)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("query Cassandra: %w", err)
	}
	return &connector.FetchResult{Columns: columns, Rows: rows, Truncated: len(rows) >= limit}, nil
}

func splitHosts(raw string) []string {
	var out []string
	for _, h := range strings.Split(raw, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		out = append(out, h)
	}
	return out
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func cqlType(t string) string {
	t = strings.ToLower(t)
	switch {
	case strings.Contains(t, "int"), strings.Contains(t, "counter"):
		return "integer"
	case strings.Contains(t, "decimal"), strings.Contains(t, "float"), strings.Contains(t, "double"):
		return "number"
	case strings.Contains(t, "bool"):
		return "boolean"
	case strings.Contains(t, "timestamp"), strings.Contains(t, "date"), strings.Contains(t, "time"):
		return "timestamp"
	case strings.Contains(t, "list"), strings.Contains(t, "set"), strings.Contains(t, "map"), strings.Contains(t, "tuple"):
		return "array"
	default:
		return "string"
	}
}

func normalize(v any) any {
	switch x := v.(type) {
	case nil, bool, string, int, int8, int16, int32, int64, float32, float64:
		return x
	case gocql.UUID:
		return x.String()
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case []byte:
		return string(x)
	case []string:
		return x
	case []int:
		return x
	default:
		return fmt.Sprint(x)
	}
}
