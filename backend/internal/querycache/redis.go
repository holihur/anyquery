package querycache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"

	"github.com/anyquery/anyquery/internal/engine"
)

// Redis is an optional shared result-cache layer. Keys are generation-scoped:
// invalidating a tenant increments a Redis generation counter, so every
// process immediately stops reading old entries without a pub/sub dependency.
type Redis struct {
	pool     *redis.Pool
	prefix   string
	ttl      time.Duration
	maxBytes int64
}

func NewRedis(rawURL, prefix string, ttl time.Duration, maxBytes int64) (*Redis, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("redis URL is required")
	}
	if prefix == "" {
		prefix = "anyquery:q:"
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	pool := &redis.Pool{
		MaxIdle:     4,
		IdleTimeout: 90 * time.Second,
		Dial: func() (redis.Conn, error) {
			conn, err := redis.DialURL(rawURL, redis.DialConnectTimeout(5*time.Second), redis.DialReadTimeout(5*time.Second), redis.DialWriteTimeout(5*time.Second))
			if err != nil {
				return nil, err
			}
			return conn, nil
		},
		TestOnBorrow: func(conn redis.Conn, _ time.Time) error {
			_, err := conn.Do("PING")
			return err
		},
	}
	conn := pool.Get()
	defer conn.Close()
	if _, err := conn.Do("PING"); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Redis{pool: pool, prefix: prefix, ttl: ttl, maxBytes: maxBytes}, nil
}

func (r *Redis) Enabled() bool { return r != nil && r.ttl > 0 }

func (r *Redis) Close() error {
	if r == nil {
		return nil
	}
	return r.pool.Close()
}

// Generation returns the current invalidation generation for a tenant.
func (r *Redis) Generation(ctx context.Context, tenantID string) (uint64, error) {
	conn, err := r.conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	value, err := redis.Uint64(conn.Do("GET", r.generationKey(tenantID)))
	if err == redis.ErrNil {
		return 0, nil
	}
	return value, err
}

func (r *Redis) GetKey(ctx context.Context, key string) (*engine.Result, bool, error) {
	if !r.Enabled() {
		return nil, false, nil
	}
	conn, err := r.conn(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()
	raw, err := redis.Bytes(conn.Do("GET", key))
	if err == redis.ErrNil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var result engine.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, false, err
	}
	return &result, true, nil
}

func (r *Redis) SetKey(ctx context.Context, key string, result *engine.Result) error {
	if !r.Enabled() || result == nil {
		return nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if r.maxBytes > 0 && int64(len(raw)) > r.maxBytes {
		return nil
	}
	conn, err := r.conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	seconds := int(r.ttl / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	_, err = conn.Do("SET", key, raw, "EX", seconds)
	return err
}

// Key combines a tenant generation with the caller's base key. Callers that
// already fetched Generation can use PrefixKey to avoid a second round trip.
func (r *Redis) Key(tenantID string, generation uint64, base string) string {
	return r.prefix + tenantID + ":" + strconv.FormatUint(generation, 10) + ":" + base
}

func (r *Redis) InvalidateTenant(ctx context.Context, tenantID string) error {
	if r == nil {
		return nil
	}
	conn, err := r.conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Do("INCR", r.generationKey(tenantID))
	return err
}

func (r *Redis) conn(ctx context.Context) (redis.Conn, error) {
	if r == nil {
		return nil, fmt.Errorf("redis cache is not configured")
	}
	conn := r.pool.Get()
	if err := conn.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = ctx
	return conn, nil
}

func (r *Redis) generationKey(tenantID string) string {
	return r.prefix + "gen:" + tenantID
}
