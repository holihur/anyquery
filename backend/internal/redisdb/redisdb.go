// Package redisdb exposes Redis keys as a single queryable keyspace table.
//
// Redis is not relational, so AnyQuery maps each matching key to one row with
// reserved metadata columns and flattens hash fields / JSON objects into
// dynamic columns:
//
//	_key    string   full Redis key
//	_type   string   string | hash | list | set | zset | stream
//	_ttl    integer  seconds, -1 when no expiry
//	_size   integer  collection length / string length
//	_value  string   raw scalar or JSON representation
//
// Hash fields and top-level JSON object fields become regular columns, which
// makes cross-source joins possible, e.g. joining redis.users with a SQL
// orders table on an id column.
package redisdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "redis",
		Name:        "Redis",
		Category:    connector.CategoryNoSQL,
		Description: "Redis hashes, JSON strings and collections as a keyspace table",
		Icon:        "redis",
		Fields: []connector.ConfigField{
			{Key: "url", Label: "Connection URL", Kind: connector.FieldTextarea, Required: true, Secret: true, Placeholder: "redis://user:password@host:6379/0", Help: "Use rediss:// for TLS. Prefer a read-only user."},
			{Key: "table_name", Label: "Table name", Kind: connector.FieldText, Required: true, Default: "keys"},
			{Key: "key_pattern", Label: "Key pattern", Kind: connector.FieldText, Default: "*", Placeholder: "*", Help: "Redis SCAN glob pattern, e.g. user:* or sessions:*."},
			{Key: "sample_size", Label: "Keys sampled for schema", Kind: connector.FieldNumber, Default: "200", Help: "Used only during schema discovery."},
			{Key: "scan_limit", Label: "Maximum keys per query", Kind: connector.FieldNumber, Default: "5000", Help: "Hard cap on the key scan for one query."},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	rawURL := strings.TrimSpace(cfg["url"])
	if rawURL == "" {
		return nil, fmt.Errorf("Redis URL is required")
	}
	conn, err := redis.DialURL(rawURL,
		redis.DialConnectTimeout(10*time.Second),
		redis.DialReadTimeout(30*time.Second),
		redis.DialWriteTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("connect Redis: %w", err)
	}
	c := &Conn{
		conn:       conn,
		pattern:    cfg["key_pattern"],
		table:      cfg["table_name"],
		scanLimit:  atoiDefault(cfg["scan_limit"], 5000),
		sampleSize: atoiDefault(cfg["sample_size"], 200),
	}
	if c.pattern == "" {
		c.pattern = "*"
	}
	if c.table == "" {
		c.table = "keys"
	}
	if c.scanLimit <= 0 {
		c.scanLimit = 5000
	}
	if c.scanLimit > 50000 {
		c.scanLimit = 50000
	}
	if c.sampleSize <= 0 {
		c.sampleSize = 200
	}
	if c.sampleSize > 1000 {
		c.sampleSize = 1000
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

type Conn struct {
	conn       redis.Conn
	pattern    string
	table      string
	scanLimit  int
	sampleSize int
}

func (c *Conn) Ping(context.Context) error {
	if _, err := c.conn.Do("PING"); err != nil {
		return fmt.Errorf("Redis ping failed: %w", err)
	}
	return nil
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return c.conn.Close() }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	rows, _, err := c.rows(ctx, c.sampleSize)
	if err != nil {
		return nil, err
	}
	return []connector.Table{{
		Name: c.table, DisplayName: c.table, Kind: "keyspace", Columns: buildColumns(rows),
	}}, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = c.scanLimit
	}
	if limit > c.scanLimit {
		limit = c.scanLimit
	}
	rows, truncated, err := c.rows(ctx, limit)
	if err != nil {
		return nil, err
	}
	return &connector.FetchResult{
		Columns:   buildColumns(rows),
		Rows:      rows,
		Truncated: truncated || len(rows) >= limit,
	}, nil
}

func (c *Conn) rows(_ context.Context, limit int) ([]map[string]any, bool, error) {
	keys, truncated, err := c.scanKeys(limit)
	if err != nil {
		return nil, false, err
	}
	var rows []map[string]any
	for start := 0; start < len(keys); start += 200 {
		end := start + 200
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[start:end]
		types, ttls, err := c.keyMeta(chunk)
		if err != nil {
			return nil, false, err
		}
		chunkRows, err := c.keyRows(chunk, types, ttls)
		if err != nil {
			return nil, false, err
		}
		rows = append(rows, chunkRows...)
	}
	return rows, truncated, nil
}

func (c *Conn) scanKeys(limit int) ([]string, bool, error) {
	if limit <= 0 {
		limit = 1
	}
	cursor := "0"
	var keys []string
	for {
		reply, err := redis.Values(c.conn.Do("SCAN", cursor, "MATCH", c.pattern, "COUNT", 256))
		if err != nil {
			return nil, false, fmt.Errorf("scan keys: %w", err)
		}
		if len(reply) != 2 {
			return nil, false, fmt.Errorf("unexpected SCAN response")
		}
		next, err := redis.String(reply[0], nil)
		if err != nil {
			return nil, false, err
		}
		batch, err := redis.Strings(reply[1], nil)
		if err != nil {
			return nil, false, err
		}
		for _, key := range batch {
			if len(keys) >= limit {
				return keys, true, nil
			}
			keys = append(keys, key)
		}
		cursor = next
		if cursor == "0" {
			break
		}
	}
	return keys, false, nil
}

func (c *Conn) keyMeta(keys []string) ([]string, []int64, error) {
	for _, key := range keys {
		if err := c.conn.Send("TYPE", key); err != nil {
			return nil, nil, err
		}
		if err := c.conn.Send("TTL", key); err != nil {
			return nil, nil, err
		}
	}
	if err := c.conn.Flush(); err != nil {
		return nil, nil, err
	}
	types := make([]string, len(keys))
	ttls := make([]int64, len(keys))
	for i := range keys {
		typeReply, err := c.conn.Receive()
		if err != nil {
			return nil, nil, err
		}
		typ, err := redis.String(typeReply, nil)
		if err != nil {
			return nil, nil, err
		}
		ttlReply, err := c.conn.Receive()
		if err != nil {
			return nil, nil, err
		}
		ttl, err := redis.Int64(ttlReply, nil)
		if err != nil {
			return nil, nil, err
		}
		types[i] = typ
		ttls[i] = ttl
		if ttl < 0 {
			ttls[i] = -1
		}
	}
	return types, ttls, nil
}

func (c *Conn) keyRows(keys, types []string, ttls []int64) ([]map[string]any, error) {
	// Pipeline the value commands by type. This keeps key scans practical even
	// when Redis is a network hop away.
	for i, key := range keys {
		switch types[i] {
		case "string":
			if err := c.conn.Send("GET", key); err != nil {
				return nil, err
			}
		case "hash":
			if err := c.conn.Send("HLEN", key); err != nil {
				return nil, err
			}
			if err := c.conn.Send("HGETALL", key); err != nil {
				return nil, err
			}
		case "list":
			if err := c.conn.Send("LLEN", key); err != nil {
				return nil, err
			}
			if err := c.conn.Send("LRANGE", key, 0, 999); err != nil {
				return nil, err
			}
		case "set":
			if err := c.conn.Send("SCARD", key); err != nil {
				return nil, err
			}
			if err := c.conn.Send("SSCAN", key, 0, "COUNT", 1000); err != nil {
				return nil, err
			}
		case "zset":
			if err := c.conn.Send("ZCARD", key); err != nil {
				return nil, err
			}
			if err := c.conn.Send("ZRANGE", key, 0, 999, "WITHSCORES"); err != nil {
				return nil, err
			}
		case "stream":
			if err := c.conn.Send("XLEN", key); err != nil {
				return nil, err
			}
			if err := c.conn.Send("XRANGE", key, "-", "+", "COUNT", 1000); err != nil {
				return nil, err
			}
		}
	}
	if err := c.conn.Flush(); err != nil {
		return nil, err
	}

	var rows []map[string]any
	for i, key := range keys {
		typ := types[i]
		if typ == "none" {
			continue
		}
		var fields map[string]any
		var value string
		var size int64
		switch typ {
		case "string":
			reply, err := c.conn.Receive()
			if err != nil {
				return nil, err
			}
			raw, err := redis.String(reply, nil)
			if errors.Is(err, redis.ErrNil) {
				continue
			}
			if err != nil {
				return nil, err
			}
			fields, value = flattenString(raw)
			size = int64(len(raw))
		case "hash":
			s, err := receiveInt(c.conn)
			if err != nil {
				return nil, err
			}
			reply, err := c.conn.Receive()
			if err != nil {
				return nil, err
			}
			m, err := redis.StringMap(reply, nil)
			if errors.Is(err, redis.ErrNil) {
				continue
			}
			if err != nil {
				return nil, err
			}
			b, _ := json.Marshal(m)
			fields = stringMap(m)
			value = string(b)
			size = s
		case "list":
			s, err := receiveInt(c.conn)
			if err != nil {
				return nil, err
			}
			reply, err := c.conn.Receive()
			if err != nil {
				return nil, err
			}
			vals, err := redis.Strings(reply, nil)
			if errors.Is(err, redis.ErrNil) {
				continue
			}
			if err != nil {
				return nil, err
			}
			b, _ := json.Marshal(vals)
			value, size = string(b), s
		case "set":
			s, err := receiveInt(c.conn)
			if err != nil {
				return nil, err
			}
			reply, err := c.conn.Receive()
			if err != nil {
				return nil, err
			}
			scan, err := redis.Values(reply, nil)
			if err != nil {
				return nil, err
			}
			var vals []string
			if len(scan) == 2 {
				vals, _ = redis.Strings(scan[1], nil)
			}
			sort.Strings(vals)
			b, _ := json.Marshal(vals)
			value, size = string(b), s
		case "zset":
			s, err := receiveInt(c.conn)
			if err != nil {
				return nil, err
			}
			reply, err := c.conn.Receive()
			if err != nil {
				return nil, err
			}
			flat, err := redis.Strings(reply, nil)
			if errors.Is(err, redis.ErrNil) {
				continue
			}
			if err != nil {
				return nil, err
			}
			type z struct {
				Member string `json:"member"`
				Score  string `json:"score"`
			}
			zs := make([]z, 0, len(flat)/2)
			for j := 0; j+1 < len(flat); j += 2 {
				zs = append(zs, z{Member: flat[j], Score: flat[j+1]})
			}
			b, _ := json.Marshal(zs)
			value, size = string(b), s
		case "stream":
			s, err := receiveInt(c.conn)
			if err != nil {
				return nil, err
			}
			reply, err := c.conn.Receive()
			if err != nil {
				return nil, err
			}
			if errors.Is(err, redis.ErrNil) {
				continue
			}
			b, _ := json.Marshal(normalizeRedisValue(reply))
			value, size = string(b), s
		default:
			continue
		}
		row := map[string]any{
			"_key": key, "_type": typ, "_ttl": ttls[i], "_size": size, "_value": value,
		}
		for k, v := range fields {
			if strings.HasPrefix(k, "_") {
				row["field_"+k] = v
			} else {
				row[k] = v
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func receiveInt(conn redis.Conn) (int64, error) {
	reply, err := conn.Receive()
	if err != nil {
		return 0, err
	}
	return redis.Int64(reply, nil)
}

func normalizeRedisValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case int64:
		return x
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalizeRedisValue(item)
		}
		return out
	case nil:
		return nil
	default:
		return fmt.Sprint(x)
	}
}

func flattenString(raw string) (map[string]any, string) {
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, raw
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return nil, raw
	}
	fields := make(map[string]any, len(obj))
	for k, v := range obj {
		switch x := v.(type) {
		case nil, string, bool, float64:
			fields[k] = x
		default:
			b, _ := json.Marshal(x)
			fields[k] = string(b)
		}
	}
	return fields, raw
}

func stringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func buildColumns(rows []map[string]any) []connector.Column {
	reserved := []connector.Column{
		{Name: "_key", Type: "string", Nullable: false},
		{Name: "_type", Type: "string", Nullable: false},
		{Name: "_ttl", Type: "integer", Nullable: false},
		{Name: "_size", Type: "integer", Nullable: false},
		{Name: "_value", Type: "string", Nullable: true},
	}
	types := map[string]string{}
	for _, row := range rows {
		for k, v := range row {
			if strings.HasPrefix(k, "_") {
				continue
			}
			types[k] = mergeType(types[k], inferType(v))
		}
	}
	var names []string
	for k := range types {
		names = append(names, k)
	}
	sort.Strings(names)
	out := append([]connector.Column{}, reserved...)
	for _, n := range names {
		out = append(out, connector.Column{Name: n, Type: types[n], Nullable: true})
	}
	return out
}

func inferType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer"
	case float32, float64:
		return "number"
	case []any, []string:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "string"
	}
}

func mergeType(a, b string) string {
	if a == "" || a == "null" {
		return b
	}
	if b == "" || b == "null" {
		return a
	}
	if a == b {
		return a
	}
	if (a == "integer" && b == "number") || (a == "number" && b == "integer") {
		return "number"
	}
	return "string"
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
