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

	"github.com/redis/go-redis/v9"

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
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Redis URL: %w", err)
	}
	opts.ReadTimeout = 30 * time.Second
	opts.WriteTimeout = 30 * time.Second
	opts.PoolSize = 5
	client := redis.NewClient(opts)
	c := &Conn{
		client:     client,
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
		_ = client.Close()
		return nil, err
	}
	return c, nil
}

type Conn struct {
	client     *redis.Client
	pattern    string
	table      string
	scanLimit  int
	sampleSize int
}

func (c *Conn) Ping(ctx context.Context) error {
	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("Redis ping failed: %w", err)
	}
	return nil
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return c.client.Close() }

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

func (c *Conn) rows(ctx context.Context, limit int) ([]map[string]any, bool, error) {
	keys, truncated, err := c.scanKeys(ctx, limit)
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
		types, ttls, err := c.keyMeta(ctx, chunk)
		if err != nil {
			return nil, false, err
		}
		chunkRows, err := c.keyRows(ctx, chunk, types, ttls)
		if err != nil {
			return nil, false, err
		}
		rows = append(rows, chunkRows...)
	}
	return rows, truncated, nil
}

func (c *Conn) scanKeys(ctx context.Context, limit int) ([]string, bool, error) {
	if limit <= 0 {
		limit = 1
	}
	cursor := uint64(0)
	var keys []string
	for {
		batch, next, err := c.client.Scan(ctx, cursor, c.pattern, 256).Result()
		if err != nil {
			return nil, false, fmt.Errorf("scan keys: %w", err)
		}
		for _, key := range batch {
			if len(keys) >= limit {
				return keys, true, nil
			}
			keys = append(keys, key)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return keys, false, nil
}

func (c *Conn) keyMeta(ctx context.Context, keys []string) ([]string, []int64, error) {
	pipe := c.client.Pipeline()
	typeCmds := make([]*redis.StatusCmd, len(keys))
	ttlCmds := make([]*redis.DurationCmd, len(keys))
	for i, key := range keys {
		typeCmds[i] = pipe.Type(ctx, key)
		ttlCmds[i] = pipe.TTL(ctx, key)
	}
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, fmt.Errorf("read key metadata: %w", err)
	}
	types := make([]string, len(keys))
	ttls := make([]int64, len(keys))
	for i := range keys {
		types[i] = typeCmds[i].Val()
		d := ttlCmds[i].Val()
		if d < 0 {
			ttls[i] = -1
		} else {
			ttls[i] = int64(d.Seconds())
		}
	}
	return types, ttls, nil
}

type fetched struct {
	row map[string]any
	err error
}

func (c *Conn) keyRows(ctx context.Context, keys, types []string, ttls []int64) ([]map[string]any, error) {
	pipe := c.client.Pipeline()
	getters := make([]func() fetched, len(keys))
	for i, key := range keys {
		key := key
		typ := types[i]
		switch typ {
		case "string":
			get := pipe.Get(ctx, key)
			getters[i] = func() fetched {
				raw, err := get.Result()
				if err != nil {
					return fetched{err: err}
				}
				fields, value := flattenString(raw)
				return fetched{row: map[string]any{"fields": fields, "value": value, "size": int64(len(raw))}}
			}
		case "hash":
			size := pipe.HLen(ctx, key)
			get := pipe.HGetAll(ctx, key)
			getters[i] = func() fetched {
				m, err := get.Result()
				if err != nil {
					return fetched{err: err}
				}
				b, _ := json.Marshal(m)
				return fetched{row: map[string]any{"fields": stringMap(m), "value": string(b), "size": size.Val()}}
			}
		case "list":
			size := pipe.LLen(ctx, key)
			get := pipe.LRange(ctx, key, 0, 999)
			getters[i] = func() fetched {
				vals, err := get.Result()
				if err != nil {
					return fetched{err: err}
				}
				b, _ := json.Marshal(vals)
				return fetched{row: map[string]any{"fields": nil, "value": string(b), "size": size.Val()}}
			}
		case "set":
			size := pipe.SCard(ctx, key)
			get := pipe.SScan(ctx, key, 0, "", 1000)
			getters[i] = func() fetched {
				vals, _, err := get.Result()
				if err != nil {
					return fetched{err: err}
				}
				sort.Strings(vals)
				b, _ := json.Marshal(vals)
				return fetched{row: map[string]any{"fields": nil, "value": string(b), "size": size.Val()}}
			}
		case "zset":
			size := pipe.ZCard(ctx, key)
			get := pipe.ZRangeWithScores(ctx, key, 0, 999)
			getters[i] = func() fetched {
				vals, err := get.Result()
				if err != nil {
					return fetched{err: err}
				}
				b, _ := json.Marshal(vals)
				return fetched{row: map[string]any{"fields": nil, "value": string(b), "size": size.Val()}}
			}
		case "stream":
			size := pipe.XLen(ctx, key)
			get := pipe.XRangeN(ctx, key, "-", "+", 1000)
			getters[i] = func() fetched {
				msgs, err := get.Result()
				if err != nil {
					return fetched{err: err}
				}
				type entry struct {
					ID     string         `json:"id"`
					Values map[string]any `json:"values"`
				}
				out := make([]entry, len(msgs))
				for j, m := range msgs {
					out[j] = entry{ID: m.ID, Values: m.Values}
				}
				b, _ := json.Marshal(out)
				return fetched{row: map[string]any{"fields": nil, "value": string(b), "size": size.Val()}}
			}
		default:
			getters[i] = func() fetched { return fetched{err: redis.Nil} }
		}
	}
	_, pipeErr := pipe.Exec(ctx)
	if pipeErr != nil && !errors.Is(pipeErr, redis.Nil) {
		return nil, fmt.Errorf("read key values: %w", pipeErr)
	}

	var rows []map[string]any
	for i, key := range keys {
		f := getters[i]()
		if f.err != nil {
			if errors.Is(f.err, redis.Nil) {
				continue // expired or deleted between SCAN and GET
			}
			return nil, fmt.Errorf("read key %q: %w", key, f.err)
		}
		row := map[string]any{
			"_key": key, "_type": types[i], "_ttl": ttls[i],
			"_size": f.row["size"], "_value": f.row["value"],
		}
		if fields, ok := f.row["fields"].(map[string]any); ok {
			for k, v := range fields {
				if strings.HasPrefix(k, "_") {
					row["field_"+k] = v
				} else {
					row[k] = v
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
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
