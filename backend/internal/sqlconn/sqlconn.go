// Package sqlconn implements one generic database/sql connector. A Dialect
// supplies the driver, DSN construction, identifier quoting, introspection and
// LIMIT syntax. That is how 16 of the 20 built-in sources share one tested
// implementation.
package sqlconn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/sijms/go-ora/v2"
	_ "github.com/trinodb/trino-go-client/trino"
	_ "modernc.org/sqlite"

	"github.com/anyquery/anyquery/internal/connector"
)

// Dialect describes a SQL-speaking data source.
type Dialect struct {
	Type        string
	Name        string
	Description string
	Driver      string
	Icon        string
	DefaultPort string
	Category    connector.Category
	Fields      []connector.ConfigField
	BuildDSN    func(map[string]string) (string, error)
	Quote       func(string) string
	// LimitSQL renders a LIMIT clause for n rows.
	LimitSQL func(int) string
	// Introspect lists tables and columns. Defaults to introspectInformationSchema.
	Introspect func(ctx context.Context, db *sql.DB, cfg map[string]string) ([]connector.Table, error)
	// PingSQL is optional; most databases accept SELECT 1.
	PingSQL string
	// SystemTime enables FOR SYSTEM_TIME temporal reads.
	SystemTime bool
}

// Provider adapts a Dialect to connector.Provider.
type Provider struct{ D Dialect }

func NewProvider(d Dialect) Provider { return Provider{D: d} }

func (p Provider) Spec() connector.TypeSpec {
	fields := p.D.Fields
	if fields == nil {
		fields = hostFields(p.D.DefaultPort)
	}
	return connector.TypeSpec{
		Type:        p.D.Type,
		Name:        p.D.Name,
		Category:    p.D.Category,
		Description: p.D.Description,
		Icon:        p.D.Icon,
		Fields:      fields,
	}
}

func (p Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	dsn, err := p.D.BuildDSN(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(p.D.Driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p.D.Type, err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	c := &Conn{dialect: p.D, db: db}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return c, nil
}

// Conn is a live database/sql connection wrapper.
type Conn struct {
	dialect Dialect
	db      *sql.DB
}

func (c *Conn) Ping(ctx context.Context) error {
	q := c.dialect.PingSQL
	if q == "" {
		q = "SELECT 1"
	}
	var one any
	if err := c.db.QueryRowContext(ctx, q).Scan(&one); err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	return nil
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true, ProjectionPushdown: true, FilterPushdown: true, SystemTime: c.dialect.SystemTime}
}

func (c *Conn) Close() error { return c.db.Close() }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	fn := c.dialect.Introspect
	if fn == nil {
		return nil, errors.New("introspection not implemented")
	}
	return fn(ctx, c.db, nil)
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 1000
	}
	q, args := c.buildFetchQuery(req)
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", req.Table, err)
	}
	defer rows.Close()

	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	types, _ := rows.ColumnTypes()
	result := &connector.FetchResult{}
	for i, n := range names {
		col := connector.Column{Name: n, Type: "string"}
		if i < len(types) {
			t := types[i]
			col.Type = normalizeDBType(t.DatabaseTypeName())
			if nullable, ok := t.Nullable(); ok {
				col.Nullable = nullable
			}
		}
		result.Columns = append(result.Columns, col)
	}

	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(names))
		for i, name := range names {
			row[name] = normalizeScan(vals[i], result.Columns[i].Type)
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result.Rows) >= req.Limit {
		result.Truncated = true
	}
	return result, nil
}

// DescribeFetch renders the SQL sent to the source, including placeholders.
// It is deliberately side-effect free and mirrors buildFetchQuery.
func (c *Conn) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	q, args := c.buildFetchQuery(req)
	return q, args, nil
}

func (c *Conn) buildFetchQuery(req connector.FetchRequest) (string, []any) {
	if req.Limit <= 0 {
		req.Limit = 1000
	}
	schema, table := splitTable(req.Table)
	qualified := c.dialect.Quote(table)
	if schema != "" {
		qualified = c.dialect.Quote(schema) + "." + qualified
	}
	cols := "*"
	if len(req.Columns) > 0 {
		quoted := make([]string, 0, len(req.Columns))
		for _, col := range req.Columns {
			quoted = append(quoted, c.dialect.Quote(col))
		}
		cols = strings.Join(quoted, ", ")
	}
	temporal, temporalArgs := c.buildTemporal(req.Temporal)
	where, whereArgs := c.buildWhereFrom(req.Predicates, len(temporalArgs))
	args := append(temporalArgs, whereArgs...)
	if strings.EqualFold(c.dialect.Type, "sqlserver") {
		return fmt.Sprintf("SELECT TOP (%d) %s FROM %s%s%s", req.Limit, cols, qualified, temporal, where), args
	}
	return fmt.Sprintf("SELECT %s FROM %s%s%s%s", cols, qualified, temporal, where, c.dialect.LimitSQL(req.Limit)), args
}

func (c *Conn) buildTemporal(t *connector.TemporalRange) (string, []any) {
	if t == nil {
		return "", nil
	}
	switch t.Mode {
	case "as_of":
		return " FOR SYSTEM_TIME AS OF " + c.placeholder(1), []any{t.AsOf}
	case "from_to":
		return " FOR SYSTEM_TIME FROM " + c.placeholder(1) + " TO " + c.placeholder(2), []any{t.From, t.To}
	case "between":
		return " FOR SYSTEM_TIME BETWEEN " + c.placeholder(1) + " AND " + c.placeholder(2), []any{t.From, t.To}
	case "all":
		return " FOR SYSTEM_TIME ALL", nil
	default:
		return "", nil
	}
}
func (c *Conn) buildWhere(preds []connector.Predicate) (string, []any) {
	return c.buildWhereFrom(preds, 0)
}

func (c *Conn) buildWhereFrom(preds []connector.Predicate, offset int) (string, []any) {
	if len(preds) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(preds))
	args := make([]any, 0, len(preds))
	for _, p := range preds {
		op := ""
		switch p.Op {
		case "eq":
			op = "="
		case "ne":
			op = "!="
		case "lt":
			op = "<"
		case "lte":
			op = "<="
		case "gt":
			op = ">"
		case "gte":
			op = ">="
		case "like":
			op = "LIKE"
		default:
			continue
		}
		if p.Value == nil {
			continue
		}
		args = append(args, p.Value)
		parts = append(parts, fmt.Sprintf("%s %s %s", c.dialect.Quote(p.Column), op, c.placeholder(offset+len(args))))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func (c *Conn) placeholder(n int) string {
	switch strings.ToLower(c.dialect.Type) {
	case "postgres", "cockroachdb", "redshift", "timescaledb", "yugabytedb":
		return fmt.Sprintf("$%d", n)
	case "sqlserver":
		return fmt.Sprintf("@p%d", n)
	case "oracle":
		return fmt.Sprintf(":%d", n)
	default:
		return "?"
	}
}

func normalizeScan(v any, typ string) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		s := string(x)
		switch typ {
		case "integer":
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n
			}
		case "number":
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
		case "boolean":
			if b, err := strconv.ParseBool(s); err == nil {
				return b
			}
		case "timestamp":
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t.UTC().Format(time.RFC3339Nano)
			}
			if t, err := time.Parse("2006-01-02 15:04:05.999999", s); err == nil {
				return t.UTC().Format(time.RFC3339Nano)
			}
		}
		if utf8.ValidString(s) {
			return s
		}
		return base64Encode(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return connector.NormalizeValue(v)
	}
}

func base64Encode(b []byte) string {
	const table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		rem := len(b) - i
		n = uint32(b[i]) << 16
		if rem > 1 {
			n |= uint32(b[i+1]) << 8
		}
		if rem > 2 {
			n |= uint32(b[i+2])
		}
		sb.WriteByte(table[(n>>18)&63])
		sb.WriteByte(table[(n>>12)&63])
		if rem > 1 {
			sb.WriteByte(table[(n>>6)&63])
		} else {
			sb.WriteByte('=')
		}
		if rem > 2 {
			sb.WriteByte(table[n&63])
		} else {
			sb.WriteByte('=')
		}
	}
	return sb.String()
}

func normalizeDBType(t string) string {
	t = strings.ToLower(t)
	switch {
	case strings.Contains(t, "int"), strings.Contains(t, "serial"):
		return "integer"
	case strings.Contains(t, "numeric"), strings.Contains(t, "decimal"), strings.Contains(t, "float"), strings.Contains(t, "double"), strings.Contains(t, "real"), strings.Contains(t, "money"):
		return "number"
	case strings.Contains(t, "bool"):
		return "boolean"
	case strings.Contains(t, "date"), strings.Contains(t, "time"), strings.Contains(t, "timestamp"):
		return "timestamp"
	case strings.Contains(t, "json"), strings.Contains(t, "array"):
		return "json"
	case strings.Contains(t, "binary"), strings.Contains(t, "blob"), strings.Contains(t, "bytea"):
		return "binary"
	default:
		return "string"
	}
}

// splitTable splits "schema.table" on the first dot.
func splitTable(name string) (string, string) {
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

// ---- DSN helpers -----------------------------------------------------------

func hostFields(defaultPort string) []connector.ConfigField {
	return []connector.ConfigField{
		{Key: "host", Label: "Host", Kind: connector.FieldText, Required: true, Placeholder: "db.example.com"},
		{Key: "port", Label: "Port", Kind: connector.FieldNumber, Placeholder: defaultPort, Default: defaultPort},
		{Key: "database", Label: "Database", Kind: connector.FieldText, Required: true, Placeholder: "analytics"},
		{Key: "username", Label: "Username", Kind: connector.FieldText, Required: true, Placeholder: "readonly_user"},
		{Key: "password", Label: "Password", Kind: connector.FieldPassword, Required: true, Secret: true},
		{Key: "sslmode", Label: "SSL mode", Kind: connector.FieldSelect, Default: "disable", Options: []connector.Option{
			{Value: "disable", Label: "Disable"},
			{Value: "prefer", Label: "Prefer"},
			{Value: "require", Label: "Require"},
			{Value: "verify-ca", Label: "Verify CA"},
			{Value: "verify-full", Label: "Verify full"},
		}},
		{Key: "dsn", Label: "Advanced connection string (optional)", Kind: connector.FieldTextarea, Secret: true, Help: "Overrides the fields above. Use when your provider needs a custom DSN."},
	}
}

func pgDSN(cfg map[string]string) (string, error) {
	if dsn := strings.TrimSpace(cfg["dsn"]); dsn != "" {
		return dsn, nil
	}
	host := strings.TrimSpace(cfg["host"])
	dbname := strings.TrimSpace(cfg["database"])
	if host == "" || dbname == "" {
		return "", errors.New("host and database are required")
	}
	port := cfg["port"]
	if port == "" {
		port = "5432"
	}
	u := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(host, port), Path: "/" + dbname}
	u.User = url.UserPassword(cfg["username"], cfg["password"])
	q := u.Query()
	sslmode := cfg["sslmode"]
	if sslmode == "" {
		sslmode = "disable"
	}
	q.Set("sslmode", sslmode)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func myDSN(defaultPort string) func(map[string]string) (string, error) {
	return func(cfg map[string]string) (string, error) {
		if dsn := strings.TrimSpace(cfg["dsn"]); dsn != "" {
			return dsn, nil
		}
		host := strings.TrimSpace(cfg["host"])
		dbname := strings.TrimSpace(cfg["database"])
		if host == "" || dbname == "" {
			return "", errors.New("host and database are required")
		}
		port := cfg["port"]
		if port == "" {
			port = defaultPort
		}
		mc := mysql.NewConfig()
		mc.User = cfg["username"]
		mc.Passwd = cfg["password"]
		mc.Net = "tcp"
		mc.Addr = net.JoinHostPort(host, port)
		mc.DBName = dbname
		mc.ParseTime = true
		mc.Loc = time.UTC
		switch cfg["sslmode"] {
		case "true", "require":
			mc.TLSConfig = "true"
		case "skip-verify":
			mc.TLSConfig = "skip-verify"
		}
		return mc.FormatDSN(), nil
	}
}

func mssqlDSN(cfg map[string]string) (string, error) {
	if dsn := strings.TrimSpace(cfg["dsn"]); dsn != "" {
		return dsn, nil
	}
	host := strings.TrimSpace(cfg["host"])
	dbname := strings.TrimSpace(cfg["database"])
	if host == "" || dbname == "" {
		return "", errors.New("host and database are required")
	}
	port := cfg["port"]
	if port == "" {
		port = "1433"
	}
	u := &url.URL{Scheme: "sqlserver", Host: net.JoinHostPort(host, port)}
	u.User = url.UserPassword(cfg["username"], cfg["password"])
	q := u.Query()
	q.Set("database", dbname)
	switch cfg["sslmode"] {
	case "require", "verify-full":
		q.Set("encrypt", "true")
	default:
		q.Set("encrypt", "disable")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func sqliteDSN(cfg map[string]string) (string, error) {
	path := strings.TrimSpace(cfg["path"])
	if path == "" {
		return "", errors.New("database file path is required")
	}
	if strings.Contains(path, "?") {
		return path, nil
	}
	return path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", nil
}

func clickhouseDSN(cfg map[string]string) (string, error) {
	if dsn := strings.TrimSpace(cfg["dsn"]); dsn != "" {
		return dsn, nil
	}
	host := strings.TrimSpace(cfg["host"])
	if host == "" {
		return "", errors.New("host is required")
	}
	port := cfg["port"]
	if port == "" {
		port = "9000"
	}
	db := cfg["database"]
	if db == "" {
		db = "default"
	}
	scheme := "clickhouse"
	if cfg["sslmode"] == "require" {
		scheme = "clickhouses"
	}
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port), Path: "/" + db}
	u.User = url.UserPassword(cfg["username"], cfg["password"])
	q := u.Query()
	q.Set("dial_timeout", "10s")
	q.Set("read_timeout", "60s")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func trinoDSN(cfg map[string]string) (string, error) {
	if dsn := strings.TrimSpace(cfg["dsn"]); dsn != "" {
		return dsn, nil
	}
	host := strings.TrimSpace(cfg["host"])
	if host == "" {
		return "", errors.New("host is required")
	}
	port := cfg["port"]
	if port == "" {
		port = "8080"
	}
	scheme := "http"
	if cfg["sslmode"] == "require" {
		scheme = "https"
	}
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	user := cfg["username"]
	if cfg["password"] != "" {
		u.User = url.UserPassword(user, cfg["password"])
	} else if user != "" {
		u.User = url.User(user)
	}
	q := u.Query()
	if cfg["catalog"] != "" {
		q.Set("catalog", cfg["catalog"])
	}
	if cfg["schema"] != "" {
		q.Set("schema", cfg["schema"])
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func oracleDSN(cfg map[string]string) (string, error) {
	if dsn := strings.TrimSpace(cfg["dsn"]); dsn != "" {
		return dsn, nil
	}
	host := strings.TrimSpace(cfg["host"])
	service := strings.TrimSpace(cfg["service"])
	if host == "" || service == "" {
		return "", errors.New("host and service are required")
	}
	port := cfg["port"]
	if port == "" {
		port = "1521"
	}
	return "oracle://" + url.QueryEscape(cfg["username"]) + ":" + url.QueryEscape(cfg["password"]) + "@" + net.JoinHostPort(host, port) + "/" + service, nil
}

func mysqlFields(port string) []connector.ConfigField {
	f := hostFields(port)
	for i := range f {
		if f[i].Key == "sslmode" {
			f[i].Options = []connector.Option{{Value: "", Label: "Disable"}, {Value: "require", Label: "Require"}, {Value: "skip-verify", Label: "Require, skip verify"}}
			f[i].Default = ""
		}
	}
	return f
}

func mssqlFields() []connector.ConfigField {
	f := hostFields("1433")
	for i := range f {
		if f[i].Key == "sslmode" {
			f[i].Options = []connector.Option{{Value: "disable", Label: "Disable"}, {Value: "require", Label: "Encrypt"}}
		}
	}
	return f
}

func trinoFields() []connector.ConfigField {
	return []connector.ConfigField{
		{Key: "host", Label: "Host", Kind: connector.FieldText, Required: true, Placeholder: "trino.example.com"},
		{Key: "port", Label: "Port", Kind: connector.FieldNumber, Default: "8080"},
		{Key: "username", Label: "Username", Kind: connector.FieldText, Required: true},
		{Key: "password", Label: "Password (optional)", Kind: connector.FieldPassword, Secret: true},
		{Key: "catalog", Label: "Catalog", Kind: connector.FieldText, Placeholder: "hive", Required: true},
		{Key: "schema", Label: "Schema", Kind: connector.FieldText, Placeholder: "default"},
		{Key: "sslmode", Label: "TLS", Kind: connector.FieldSelect, Default: "disable", Options: []connector.Option{{Value: "disable", Label: "Disabled"}, {Value: "require", Label: "HTTPS"}}},
		{Key: "dsn", Label: "Advanced DSN (optional)", Kind: connector.FieldTextarea, Secret: true},
	}
}

func timeSeriesPGFields(port string) []connector.ConfigField {
	f := hostFields(port)
	for i := range f {
		if f[i].Key == "database" {
			f[i].Placeholder = "postgres"
		}
	}
	return f
}

// Dialects returns the built-in SQL dialects.
func Dialects() []Dialect {
	return []Dialect{
		{
			Type: "postgres", Name: "PostgreSQL", Driver: "pgx", Icon: "postgres", DefaultPort: "5432",
			Description: "PostgreSQL and compatible databases", Category: connector.CategorySQL,
			BuildDSN: pgDSN, Quote: quoteDouble, LimitSQL: limitStandard,
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema NOT IN ('pg_catalog','information_schema') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema NOT IN ('pg_catalog','information_schema') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name, ordinal_position`)
			},
		},
		{
			Type: "cockroachdb", Name: "CockroachDB", Driver: "pgx", Icon: "cockroachdb", DefaultPort: "26257",
			Description: "Distributed SQL, PostgreSQL wire compatible", Category: connector.CategorySQL,
			BuildDSN: pgDSN, Quote: quoteDouble, LimitSQL: limitStandard,
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema NOT IN ('pg_catalog','information_schema','crdb_internal') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema NOT IN ('pg_catalog','information_schema','crdb_internal') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name, ordinal_position`)
			},
		},
		{
			Type: "redshift", Name: "Amazon Redshift", Driver: "pgx", Icon: "redshift", DefaultPort: "5439",
			Description: "Amazon Redshift data warehouse", Category: connector.CategorySQL,
			BuildDSN: pgDSN, Quote: quoteDouble, LimitSQL: limitStandard,
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema NOT IN ('pg_catalog','information_schema') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema NOT IN ('pg_catalog','information_schema') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name, ordinal_position`)
			},
		},
		{
			Type: "timescaledb", Name: "TimescaleDB", Driver: "pgx", Icon: "timescaledb", DefaultPort: "5432",
			Description: "Time-series extension for PostgreSQL", Category: connector.CategoryTimeSeries,
			BuildDSN: pgDSN, Quote: quoteDouble, LimitSQL: limitStandard,
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema NOT IN ('pg_catalog','information_schema','_timescaledb_internal') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema NOT IN ('pg_catalog','information_schema','_timescaledb_internal') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name, ordinal_position`)
			},
		},
		{
			Type: "yugabytedb", Name: "YugabyteDB", Driver: "pgx", Icon: "yugabytedb", DefaultPort: "5433",
			Description: "Distributed PostgreSQL-compatible database", Category: connector.CategorySQL,
			BuildDSN: pgDSN, Quote: quoteDouble, LimitSQL: limitStandard,
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema NOT IN ('pg_catalog','information_schema') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema NOT IN ('pg_catalog','information_schema') AND table_schema = current_schema()
					 ORDER BY table_schema, table_name, ordinal_position`)
			},
		},
		{
			Type: "mysql", Name: "MySQL", Driver: "mysql", Icon: "mysql", DefaultPort: "3306",
			Description: "MySQL 5.7+ and Aurora MySQL", Category: connector.CategorySQL,
			BuildDSN: myDSN("3306"), Quote: quoteBacktick, LimitSQL: limitStandard, Fields: mysqlFields("3306"),
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema = DATABASE() ORDER BY table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema = DATABASE() ORDER BY table_name, ordinal_position`)
			},
		},
		{
			Type: "mariadb", Name: "MariaDB", Driver: "mysql", Icon: "mariadb", DefaultPort: "3306",
			Description: "MariaDB and compatible services", Category: connector.CategorySQL,
			BuildDSN: myDSN("3306"), Quote: quoteBacktick, LimitSQL: limitStandard, Fields: mysqlFields("3306"),
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema = DATABASE() ORDER BY table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema = DATABASE() ORDER BY table_name, ordinal_position`)
			},
		},
		{
			Type: "singlestore", Name: "SingleStore", Driver: "mysql", Icon: "singlestore", DefaultPort: "3306",
			Description: "SingleStore (MySQL wire compatible)", Category: connector.CategorySQL,
			BuildDSN: myDSN("3306"), Quote: quoteBacktick, LimitSQL: limitStandard, Fields: mysqlFields("3306"),
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema = DATABASE() ORDER BY table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema = DATABASE() ORDER BY table_name, ordinal_position`)
			},
		},
		{
			Type: "sqlserver", Name: "Microsoft SQL Server", Driver: "sqlserver", Icon: "sqlserver", DefaultPort: "1433",
			Description: "SQL Server and Azure SQL", Category: connector.CategorySQL,
			BuildDSN: mssqlDSN, Quote: quoteBracket, LimitSQL: limitTop, Fields: mssqlFields(),
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE FROM INFORMATION_SCHEMA.TABLES
					 WHERE TABLE_SCHEMA = SCHEMA_NAME() ORDER BY TABLE_NAME`,
					`SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, DATA_TYPE, IS_NULLABLE FROM INFORMATION_SCHEMA.COLUMNS
					 WHERE TABLE_SCHEMA = SCHEMA_NAME() ORDER BY TABLE_NAME, ORDINAL_POSITION`)
			},
			PingSQL: "SELECT 1", SystemTime: true,
		},
		{
			Type: "sqlite", Name: "SQLite", Driver: "sqlite", Icon: "sqlite", DefaultPort: "",
			Description: "Local SQLite database file", Category: connector.CategorySQL,
			BuildDSN: sqliteDSN, Quote: quoteDouble, LimitSQL: limitStandard,
			Fields: []connector.ConfigField{
				{Key: "path", Label: "Database file path", Kind: connector.FieldText, Required: true, Placeholder: "/data/analytics.db", Help: "Path on the AnyQuery server. Use :memory: for an in-memory database."},
			},
			Introspect: introspectSQLite,
		},
		{
			Type: "clickhouse", Name: "ClickHouse", Driver: "clickhouse", Icon: "clickhouse", DefaultPort: "9000",
			Description: "ClickHouse analytics database", Category: connector.CategoryTimeSeries,
			BuildDSN: clickhouseDSN, Quote: quoteBacktick, LimitSQL: limitStandard,
			Fields: []connector.ConfigField{
				{Key: "host", Label: "Host", Kind: connector.FieldText, Required: true},
				{Key: "port", Label: "Native port", Kind: connector.FieldNumber, Default: "9000"},
				{Key: "database", Label: "Database", Kind: connector.FieldText, Default: "default"},
				{Key: "username", Label: "Username", Kind: connector.FieldText, Default: "default"},
				{Key: "password", Label: "Password", Kind: connector.FieldPassword, Secret: true},
				{Key: "sslmode", Label: "TLS", Kind: connector.FieldSelect, Default: "disable", Options: []connector.Option{{Value: "disable", Label: "Disabled"}, {Value: "require", Label: "Enabled"}}},
				{Key: "dsn", Label: "Advanced DSN (optional)", Kind: connector.FieldTextarea, Secret: true},
			},
			Introspect: introspectClickHouse,
		},
		{
			Type: "trino", Name: "Trino", Driver: "trino", Icon: "trino", DefaultPort: "8080",
			Description: "Trino distributed SQL query engine", Category: connector.CategorySQL,
			BuildDSN: trinoDSN, Quote: quoteDouble, LimitSQL: limitStandard, Fields: trinoFields(),
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema NOT IN ('information_schema','sys') ORDER BY table_schema, table_name LIMIT 500`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema NOT IN ('information_schema','sys') ORDER BY table_schema, table_name, ordinal_position LIMIT 5000`)
			},
		},
		{
			Type: "presto", Name: "Presto", Driver: "trino", Icon: "presto", DefaultPort: "8080",
			Description: "PrestoSQL / PrestoDB (Trino wire protocol)", Category: connector.CategorySQL,
			BuildDSN: trinoDSN, Quote: quoteDouble, LimitSQL: limitStandard, Fields: trinoFields(),
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema NOT IN ('information_schema','sys') ORDER BY table_schema, table_name LIMIT 500`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema NOT IN ('information_schema','sys') ORDER BY table_schema, table_name, ordinal_position LIMIT 5000`)
			},
		},
		{
			Type: "questdb", Name: "QuestDB", Driver: "pgx", Icon: "questdb", DefaultPort: "8812",
			Description: "QuestDB time-series SQL over the PostgreSQL wire protocol", Category: connector.CategoryTimeSeries,
			BuildDSN: pgDSN, Quote: quoteDouble, LimitSQL: limitStandard,
			Fields: timeSeriesPGFields("8812"),
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema NOT IN ('pg_catalog','information_schema','sys') ORDER BY table_schema, table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema NOT IN ('pg_catalog','information_schema','sys') ORDER BY table_schema, table_name, ordinal_position`)
			},
		},
		{
			Type: "greptimedb", Name: "GreptimeDB", Driver: "pgx", Icon: "greptimedb", DefaultPort: "4003",
			Description: "GreptimeDB time-series database over the PostgreSQL wire protocol", Category: connector.CategoryTimeSeries,
			BuildDSN: pgDSN, Quote: quoteDouble, LimitSQL: limitStandard,
			Fields: timeSeriesPGFields("4003"),
			Introspect: func(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
				return introspectInformationSchema(ctx, db,
					`SELECT table_schema, table_name, table_type FROM information_schema.tables
					 WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name`,
					`SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
					 WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name, ordinal_position`)
			},
		},
		{
			Type: "oracle", Name: "Oracle Database", Driver: "oracle", Icon: "oracle", DefaultPort: "1521",
			Description: "Oracle Database and Autonomous Database", Category: connector.CategorySQL,
			BuildDSN: oracleDSN, Quote: quoteDouble, LimitSQL: limitFetchFirst,
			Fields: []connector.ConfigField{
				{Key: "host", Label: "Host", Kind: connector.FieldText, Required: true},
				{Key: "port", Label: "Port", Kind: connector.FieldNumber, Default: "1521"},
				{Key: "service", Label: "Service name", Kind: connector.FieldText, Required: true, Placeholder: "ORCL"},
				{Key: "username", Label: "Username", Kind: connector.FieldText, Required: true},
				{Key: "password", Label: "Password", Kind: connector.FieldPassword, Required: true, Secret: true},
				{Key: "dsn", Label: "Advanced DSN (optional)", Kind: connector.FieldTextarea, Secret: true},
			},
			Introspect: introspectOracle,
		},
	}
}

// RegisterAll registers every SQL dialect.
func RegisterAll() {
	for _, d := range Dialects() {
		connector.Register(NewProvider(d))
	}
}

// ---- introspection helpers -------------------------------------------------

func introspectInformationSchema(ctx context.Context, db *sql.DB, tablesQuery, columnsQuery string) ([]connector.Table, error) {
	tableRows, err := db.QueryContext(ctx, tablesQuery)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer tableRows.Close()
	tables := map[string]*connector.Table{}
	for tableRows.Next() {
		var schema, name, typ sql.NullString
		if err := tableRows.Scan(&schema, &name, &typ); err != nil {
			return nil, err
		}
		if name.String == "" {
			continue
		}
		key := name.String
		if schema.String != "" {
			key = schema.String + "." + name.String
		}
		kind := "table"
		if strings.Contains(strings.ToLower(typ.String), "view") {
			kind = "view"
		}
		tables[key] = &connector.Table{Name: key, DisplayName: name.String, Schema: schema.String, Kind: kind}
	}
	if err := tableRows.Err(); err != nil {
		return nil, err
	}

	colRows, err := db.QueryContext(ctx, columnsQuery)
	if err != nil {
		return nil, fmt.Errorf("list columns: %w", err)
	}
	defer colRows.Close()
	for colRows.Next() {
		var schema, table, col, typ, nullable sql.NullString
		if err := colRows.Scan(&schema, &table, &col, &typ, &nullable); err != nil {
			return nil, err
		}
		key := table.String
		if schema.String != "" {
			key = schema.String + "." + table.String
		}
		t, ok := tables[key]
		if !ok {
			continue
		}
		n := strings.EqualFold(nullable.String, "yes") || nullable.String == "1" || strings.EqualFold(nullable.String, "true")
		t.Columns = append(t.Columns, connector.Column{Name: col.String, Type: normalizeDBType(typ.String), Nullable: n})
	}
	out := make([]connector.Table, 0, len(tables))
	for _, t := range tables {
		t.Columns = dedupeColumns(t.Columns)
		out = append(out, *t)
	}
	connector.SortTables(out)
	return out, nil
}

func dedupeColumns(cols []connector.Column) []connector.Column {
	seen := make(map[string]bool, len(cols))
	out := cols[:0]
	for _, c := range cols {
		if seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	return out
}

func introspectSQLite(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []connector.Table
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		t := connector.Table{Name: name, DisplayName: name, Kind: typ}
		colRows, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteDouble(name)+`)`)
		if err != nil {
			return nil, err
		}
		for colRows.Next() {
			var cid int
			var colName, colType string
			var notNull, pk int
			var defaultValue any
			if err := colRows.Scan(&cid, &colName, &colType, &notNull, &defaultValue, &pk); err != nil {
				colRows.Close()
				return nil, err
			}
			t.Columns = append(t.Columns, connector.Column{Name: colName, Type: normalizeDBType(colType), Nullable: notNull == 0})
		}
		colRows.Close()
		out = append(out, t)
	}
	return out, rows.Err()
}

func introspectClickHouse(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
	return introspectInformationSchema(ctx, db,
		`SELECT database, name, if(engine LIKE '%View', 'VIEW', 'TABLE') FROM system.tables
		 WHERE database = currentDatabase() AND name NOT LIKE '.%' ORDER BY name`,
		`SELECT database, table, name, type, if(startsWith(type,'Nullable'), 'YES', 'NO') FROM system.columns
		 WHERE database = currentDatabase() ORDER BY table, position`)
}

func introspectOracle(ctx context.Context, db *sql.DB, _ map[string]string) ([]connector.Table, error) {
	return introspectInformationSchema(ctx, db,
		`SELECT owner, table_name, 'TABLE' FROM all_tables WHERE owner = SYS_CONTEXT('USERENV','CURRENT_SCHEMA')
		 UNION ALL SELECT owner, view_name, 'VIEW' FROM all_views WHERE owner = SYS_CONTEXT('USERENV','CURRENT_SCHEMA')`,
		`SELECT owner, table_name, column_name, data_type, nullable FROM all_tab_columns
		 WHERE owner = SYS_CONTEXT('USERENV','CURRENT_SCHEMA') ORDER BY table_name, column_id`)
}

// ---- identifier / limit helpers -------------------------------------------

func quoteDouble(s string) string   { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func quoteBacktick(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
func quoteBracket(s string) string  { return "[" + strings.ReplaceAll(s, "]", "]]") + "]" }

func limitStandard(n int) string {
	if n <= 0 {
		return ""
	}
	return " LIMIT " + strconv.Itoa(n)
}

func limitTop(n int) string {
	// Caller injects this after SELECT; Fetch handles it specially.
	return " LIMIT " + strconv.Itoa(n)
}

func limitFetchFirst(n int) string {
	if n <= 0 {
		return ""
	}
	return " FETCH FIRST " + strconv.Itoa(n) + " ROWS ONLY"
}

// FetchOverride applies SQL Server TOP syntax through a query prefix.
func (c *Conn) fetchPrefix(limit int) string {
	if strings.EqualFold(c.dialect.Type, "sqlserver") {
		return "TOP " + strconv.Itoa(limit) + " "
	}
	return ""
}

type sqlRowStream struct {
	rows  *sql.Rows
	names []string
	types []string
}

func (s *sqlRowStream) Next() (map[string]any, error) {
	if !s.rows.Next() {
		if err := s.rows.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	vals := make([]any, len(s.names))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := s.rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	row := make(map[string]any, len(s.names))
	for i, name := range s.names {
		row[name] = normalizeScan(vals[i], s.types[i])
	}
	return row, nil
}

func (s *sqlRowStream) Close() error { return s.rows.Close() }

// FetchStream exposes database/sql rows incrementally.
func (c *Conn) FetchStream(ctx context.Context, req connector.FetchRequest) (connector.RowStream, error) {
	if req.Limit <= 0 {
		req.Limit = 1000
	}
	q, args := c.buildFetchQuery(req)
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", req.Table, err)
	}
	names, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	types := make([]string, len(names))
	if cols, err := rows.ColumnTypes(); err == nil {
		for i := range names {
			if i < len(cols) {
				types[i] = normalizeDBType(cols[i].DatabaseTypeName())
			} else {
				types[i] = "string"
			}
		}
	} else {
		for i := range types {
			types[i] = "string"
		}
	}
	return &sqlRowStream{rows: rows, names: names, types: types}, nil
}
