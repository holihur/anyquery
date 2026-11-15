// Package memorycache implements a read-only connector for Memcached-compatible
// memory caches. Memcached has no SCAN command, so the connector enumerates
// keys through the optional lru_crawler metadump command (with a cachedump
// fallback) and then fetches values in batches.
package memorycache

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

const (
	defaultTable     = "entries"
	defaultScanLimit = 5000
	maxScanLimit     = 50000
	defaultValueCap  = 1 << 20
)

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "memorycache",
		Name:        "Memory Cache (Memcached)",
		Category:    connector.CategoryNoSQL,
		Description: "Memcached-compatible memory cache keys and values",
		Icon:        "memorycache",
		Maturity:    "native",
		DocsURL:     "https://github.com/memcached/memcached/wiki/Commands",
		Fields: []connector.ConfigField{
			{Key: "address", Label: "Address", Kind: connector.FieldText, Required: true, Placeholder: "localhost:11211", Help: "Memcached host:port. memcached:// and tcp:// prefixes are accepted. TLS and SASL are not supported by the text protocol."},
			{Key: "table_name", Label: "Table name", Kind: connector.FieldText, Default: defaultTable},
			{Key: "key_prefix", Label: "Key prefix", Kind: connector.FieldText, Placeholder: "session:", Help: "Only expose keys with this prefix. Leave empty to expose all enumerable keys."},
			{Key: "scan_limit", Label: "Maximum keys per query", Kind: connector.FieldNumber, Default: "5000", Help: "Hard cap for key enumeration. Memcached has no native SCAN, so this limits crawler/cachedump work."},
			{Key: "sample_size", Label: "Keys sampled for schema", Kind: connector.FieldNumber, Default: "200"},
			{Key: "max_value_bytes", Label: "Maximum value size", Kind: connector.FieldNumber, Default: "1048576", Help: "Larger values are truncated and marked in value_encoding."},
			{Key: "timeout_seconds", Label: "Timeout (seconds)", Kind: connector.FieldNumber, Default: "10"},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	addr, err := parseAddress(cfg["address"])
	if err != nil {
		return nil, err
	}
	c := &Conn{
		addr:      addr,
		table:     strings.TrimSpace(cfg["table_name"]),
		prefix:    cfg["key_prefix"],
		scanLimit: atoiDefault(cfg["scan_limit"], defaultScanLimit),
		sample:    atoiDefault(cfg["sample_size"], 200),
		valueCap:  atoiDefault(cfg["max_value_bytes"], defaultValueCap),
		timeout:   time.Duration(atoiDefault(cfg["timeout_seconds"], 10)) * time.Second,
	}
	if c.table == "" {
		c.table = defaultTable
	}
	if c.scanLimit <= 0 {
		c.scanLimit = defaultScanLimit
	}
	if c.scanLimit > maxScanLimit {
		c.scanLimit = maxScanLimit
	}
	if c.sample <= 0 {
		c.sample = 200
	}
	if c.sample > 1000 {
		c.sample = 1000
	}
	if c.valueCap <= 0 {
		c.valueCap = defaultValueCap
	}
	if c.timeout <= 0 {
		c.timeout = 10 * time.Second
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("connect memory cache: %w", err)
	}
	return c, nil
}

type Conn struct {
	addr      string
	table     string
	prefix    string
	scanLimit int
	sample    int
	valueCap  int
	timeout   time.Duration
}

func (c *Conn) Ping(ctx context.Context) error {
	err := c.withConn(ctx, func(nc net.Conn, br *bufio.Reader) error {
		if err := writeCommand(nc, "version"); err != nil {
			return err
		}
		line, err := readLine(br)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, "VERSION ") {
			return fmt.Errorf("unexpected memcached response: %s", line)
		}
		return nil
	})
	return err
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(context.Context) ([]connector.Table, error) {
	return []connector.Table{{
		Name:        c.table,
		DisplayName: c.table,
		Kind:        "keyspace",
		Columns: []connector.Column{
			{Name: "key", Type: "string"},
			{Name: "value", Type: "string", Nullable: true},
			{Name: "value_type", Type: "string", Nullable: true},
			{Name: "value_encoding", Type: "string", Nullable: true},
			{Name: "flags", Type: "integer", Nullable: true},
			{Name: "size", Type: "integer", Nullable: true},
			{Name: "expires_at", Type: "timestamp", Nullable: true},
			{Name: "last_access", Type: "timestamp", Nullable: true},
			{Name: "cas", Type: "integer", Nullable: true},
		},
	}}, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	if req.Table != c.table {
		return nil, fmt.Errorf("unknown table %q", req.Table)
	}
	limit := req.Limit
	if limit <= 0 || limit > c.scanLimit {
		limit = c.scanLimit
	}
	entries, truncated, err := c.scan(ctx, limit)
	if err != nil {
		return nil, err
	}
	columns := []connector.Column{
		{Name: "key", Type: "string"},
		{Name: "value", Type: "string", Nullable: true},
		{Name: "value_type", Type: "string", Nullable: true},
		{Name: "value_encoding", Type: "string", Nullable: true},
		{Name: "flags", Type: "integer", Nullable: true},
		{Name: "size", Type: "integer", Nullable: true},
		{Name: "expires_at", Type: "timestamp", Nullable: true},
		{Name: "last_access", Type: "timestamp", Nullable: true},
		{Name: "cas", Type: "integer", Nullable: true},
	}
	if len(entries) == 0 {
		return &connector.FetchResult{Columns: columns, Truncated: truncated}, nil
	}
	values, err := c.get(ctx, entries)
	if err != nil {
		return nil, err
	}
	result := &connector.FetchResult{Columns: columns, Truncated: truncated}
	for _, entry := range entries {
		val, ok := values[entry.key]
		if !ok {
			continue
		}
		row := map[string]any{
			"key":            entry.key,
			"flags":          val.flags,
			"size":           entry.size,
			"cas":            entry.cas,
			"value":          val.value,
			"value_type":     val.kind,
			"value_encoding": val.encoding,
		}
		if !entry.expires.IsZero() {
			row["expires_at"] = entry.expires
		}
		if !entry.lastAccess.IsZero() {
			row["last_access"] = entry.lastAccess
		}
		result.Rows = append(result.Rows, row)
	}
	return result, nil
}

func (c *Conn) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	return fmt.Sprintf("SCAN memcached lru_crawler metadump all (prefix=%q, limit=%d) + GET values", c.prefix, req.Limit), nil, nil
}

type entry struct {
	key        string
	size       int64
	expires    time.Time
	lastAccess time.Time
	cas        int64
}

func (c *Conn) scan(ctx context.Context, limit int) ([]entry, bool, error) {
	var entries []entry
	err := c.withConn(ctx, func(nc net.Conn, br *bufio.Reader) error {
		if err := writeCommand(nc, "lru_crawler metadump all"); err != nil {
			return err
		}
		for {
			line, err := readLine(br)
			if err == io.EOF || line == "END" {
				return nil
			}
			if err != nil {
				return err
			}
			if strings.HasPrefix(line, "ERROR") || strings.HasPrefix(line, "CLIENT_ERROR") || strings.HasPrefix(line, "SERVER_ERROR") {
				return errMetadumpUnavailable
			}
			e, ok := parseMetadumpLine(line)
			if !ok {
				continue
			}
			if c.prefix != "" && !strings.HasPrefix(e.key, c.prefix) {
				continue
			}
			entries = append(entries, e)
			if len(entries) >= limit {
				return nil
			}
		}
	})
	if errMetadumpUnavailable == err {
		entries, err = c.scanCachedump(ctx, limit)
	}
	if err != nil {
		return nil, false, err
	}
	truncated := len(entries) >= limit || len(entries) >= c.scanLimit
	return entries, truncated, nil
}

var errMetadumpUnavailable = fmt.Errorf("lru_crawler metadump unavailable")

func (c *Conn) scanCachedump(ctx context.Context, limit int) ([]entry, error) {
	var entries []entry
	err := c.withConn(ctx, func(nc net.Conn, br *bufio.Reader) error {
		if err := writeCommand(nc, "stats items"); err != nil {
			return err
		}
		slabs := map[string]bool{}
		for {
			line, err := readLine(br)
			if line == "END" || err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if strings.HasPrefix(line, "STAT items:") {
				rest := strings.TrimPrefix(line, "STAT items:")
				if i := strings.IndexByte(rest, ':'); i > 0 {
					slabs[rest[:i]] = true
				}
			}
		}
		for slab := range slabs {
			if err := writeCommand(nc, "stats cachedump "+slab+" 0"); err != nil {
				return err
			}
			for {
				line, err := readLine(br)
				if line == "END" || err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
				if !strings.HasPrefix(line, "ITEM ") {
					continue
				}
				rest := strings.TrimPrefix(line, "ITEM ")
				i := strings.IndexByte(rest, ' ')
				if i <= 0 {
					continue
				}
				key := rest[:i]
				if decoded, err := url.QueryUnescape(key); err == nil {
					key = decoded
				}
				if c.prefix != "" && !strings.HasPrefix(key, c.prefix) {
					continue
				}
				entries = append(entries, entry{key: key})
				if len(entries) >= limit {
					return nil
				}
			}
		}
		return nil
	})
	return entries, err
}

func parseMetadumpLine(line string) (entry, bool) {
	var e entry
	for _, field := range strings.Fields(line) {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "key":
			decoded, err := url.QueryUnescape(val)
			if err != nil {
				decoded = val
			}
			e.key = decoded
		case "size":
			n, _ := strconv.ParseInt(val, 10, 64)
			e.size = n
		case "exp":
			n, _ := strconv.ParseInt(val, 10, 64)
			if n > 0 {
				e.expires = time.Unix(n, 0)
			}
		case "la":
			n, _ := strconv.ParseInt(val, 10, 64)
			if n > 0 {
				e.lastAccess = time.Unix(n, 0)
			}
		case "cas":
			n, _ := strconv.ParseInt(val, 10, 64)
			e.cas = n
		}
	}
	return e, e.key != ""
}

type cachedValue struct {
	value    string
	kind     string
	encoding string
	flags    int64
}

func (c *Conn) get(ctx context.Context, entries []entry) (map[string]cachedValue, error) {
	out := map[string]cachedValue{}
	const batch = 100
	for start := 0; start < len(entries); start += batch {
		end := start + batch
		if end > len(entries) {
			end = len(entries)
		}
		keys := make([]string, 0, end-start)
		for _, e := range entries[start:end] {
			if strings.ContainsAny(e.key, " \r\n\t") || e.key == "" {
				continue
			}
			keys = append(keys, e.key)
		}
		if len(keys) == 0 {
			continue
		}
		values, err := c.getBatch(ctx, keys)
		if err != nil {
			return nil, err
		}
		for k, v := range values {
			out[k] = v
		}
	}
	return out, nil
}

func (c *Conn) getBatch(ctx context.Context, keys []string) (map[string]cachedValue, error) {
	out := map[string]cachedValue{}
	err := c.withConn(ctx, func(nc net.Conn, br *bufio.Reader) error {
		if err := writeCommand(nc, "get "+strings.Join(keys, " ")); err != nil {
			return err
		}
		for {
			line, err := readLine(br)
			if line == "END" || err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if !strings.HasPrefix(line, "VALUE ") {
				if strings.HasPrefix(line, "ERROR") {
					return fmt.Errorf("memcached get failed: %s", line)
				}
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 4 {
				return fmt.Errorf("invalid VALUE line: %s", line)
			}
			key := parts[1]
			flags, _ := strconv.ParseInt(parts[2], 10, 64)
			size, err := strconv.Atoi(parts[3])
			if err != nil || size < 0 {
				return fmt.Errorf("invalid value size: %s", line)
			}
			data := make([]byte, size)
			if _, err := io.ReadFull(br, data); err != nil {
				return err
			}
			if _, err := readLine(br); err != nil {
				return err
			}
			val := cachedValue{flags: flags}
			if len(data) > c.valueCap {
				val.encoding = "truncated"
				data = data[:c.valueCap]
			}
			if utf8.Valid(data) {
				val.value = string(data)
			} else {
				val.value = base64.StdEncoding.EncodeToString(data)
				val.encoding = "base64"
			}
			val.kind = valueKind(data)
			out[key] = val
		}
	})
	return out, err
}

func valueKind(data []byte) string {
	if len(data) == 0 {
		return "empty"
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return "string"
	}
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case map[string]any:
		return "json_object"
	case []any:
		return "json_array"
	case string:
		return "json_string"
	default:
		return "json"
	}
}

func (c *Conn) withConn(ctx context.Context, fn func(net.Conn, *bufio.Reader) error) error {
	d := net.Dialer{Timeout: c.timeout}
	nc, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return err
	}
	defer nc.Close()
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = nc.SetDeadline(deadline)
	return fn(nc, bufio.NewReaderSize(nc, 64<<10))
}

func writeCommand(w io.Writer, cmd string) error {
	_, err := io.WriteString(w, cmd+"\r\n")
	return err
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func parseAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("memcached address is required")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("invalid memcached address: %w", err)
		}
		if u.Host == "" {
			return "", fmt.Errorf("invalid memcached address %q", raw)
		}
		raw = u.Host
	}
	if _, _, err := net.SplitHostPort(raw); err != nil {
		return "", fmt.Errorf("memcached address must be host:port: %w", err)
	}
	return raw, nil
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
