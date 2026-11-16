// Package querycache provides a bounded, TTL-based in-memory cache for
// successful query results. Identical concurrent loads are coalesced so a
// dashboard with duplicate panels only hits the sources once.
package querycache

import (
	"container/list"
	"context"
	"strings"
	"sync"
	"time"

	"github.com/anyquery/anyquery/internal/engine"
)

type entry struct {
	key    string
	value  *engine.Result
	expiry time.Time
	size   int64
}

type call struct {
	done        chan struct{}
	result      *engine.Result
	err         error
	invalidated bool
}

// Cache is safe for concurrent use. A zero or negative TTL disables storage
// and coalescing while keeping the same API.
type Cache struct {
	mu       sync.Mutex
	ttl      time.Duration
	max      int
	maxBytes int64
	bytes    int64
	items    map[string]*list.Element
	lru      *list.List
	inflight map[string]*call
}

func New(ttl time.Duration, max int) *Cache {
	return NewSized(ttl, max, 64<<20)
}

// NewSized creates a cache with both an entry limit and a rough byte budget.
// maxBytes <= 0 means no byte budget.
func NewSized(ttl time.Duration, max int, maxBytes int64) *Cache {
	if max <= 0 {
		max = 200
	}
	return &Cache{
		ttl:      ttl,
		max:      max,
		maxBytes: maxBytes,
		items:    make(map[string]*list.Element),
		lru:      list.New(),
		inflight: make(map[string]*call),
	}
}

// Enabled reports whether results are stored between requests.
func (c *Cache) Enabled() bool { return c != nil && c.ttl > 0 }

// GetOrLoad returns a cached result or executes load. The bool reports whether
// the result came from the cache without invoking load. Concurrent callers for
// the same key wait for the first load (singleflight).
func (c *Cache) GetOrLoad(ctx context.Context, key string, load func() (*engine.Result, error)) (*engine.Result, bool, error) {
	if !c.Enabled() {
		result, err := load()
		return result, false, err
	}
	if result, ok := c.Get(key); ok {
		return result, true, nil
	}

	c.mu.Lock()
	if result, ok := c.getLocked(key); ok {
		c.mu.Unlock()
		return result, true, nil
	}
	if pending, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-pending.done:
			return pending.result, false, pending.err
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	pending := &call{done: make(chan struct{})}
	c.inflight[key] = pending
	c.mu.Unlock()

	result, err := load()

	c.mu.Lock()
	delete(c.inflight, key)
	pending.result, pending.err = result, err
	if err == nil && result != nil && !pending.invalidated {
		c.putLocked(key, result)
	}
	close(pending.done)
	c.mu.Unlock()
	return result, false, err
}

func (c *Cache) Get(key string) (*engine.Result, bool) {
	if !c.Enabled() {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getLocked(key)
}

func (c *Cache) getLocked(key string) (*engine.Result, bool) {
	element, ok := c.items[key]
	if !ok {
		return nil, false
	}
	item := element.Value.(*entry)
	if time.Now().After(item.expiry) {
		c.removeLocked(element)
		return nil, false
	}
	c.lru.MoveToFront(element)
	return item.value, true
}

func (c *Cache) putLocked(key string, result *engine.Result) {
	size := estimateResult(result)
	if c.maxBytes > 0 && size > c.maxBytes {
		return
	}
	if element, ok := c.items[key]; ok {
		item := element.Value.(*entry)
		c.bytes += size - item.size
		item.value = result
		item.size = size
		item.expiry = time.Now().Add(c.ttl)
		c.lru.MoveToFront(element)
	} else {
		element := c.lru.PushFront(&entry{key: key, value: result, expiry: time.Now().Add(c.ttl), size: size})
		c.items[key] = element
		c.bytes += size
	}
	for c.lru.Len() > c.max || (c.maxBytes > 0 && c.bytes > c.maxBytes) {
		c.removeLocked(c.lru.Back())
	}
}

func (c *Cache) removeLocked(element *list.Element) {
	if element == nil {
		return
	}
	item := element.Value.(*entry)
	delete(c.items, item.key)
	c.bytes -= item.size
	if c.bytes < 0 {
		c.bytes = 0
	}
	c.lru.Remove(element)
}

// InvalidateTenant drops every cached result for tenantID. Keys are built with
// tenantID + NUL + SQL + NUL + params.
func (c *Cache) InvalidateTenant(tenantID string) {
	if c == nil {
		return
	}
	prefix := tenantID + "\x00"
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, element := range c.items {
		if strings.HasPrefix(key, prefix) {
			c.removeLocked(element)
		}
	}
	for key, pending := range c.inflight {
		if strings.HasPrefix(key, prefix) {
			pending.invalidated = true
		}
	}
}

// Bytes returns the approximate retained result size.
func (c *Cache) Bytes() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

func estimateResult(result *engine.Result) int64 {
	if result == nil {
		return 0
	}
	size := int64(128)
	for _, col := range result.Columns {
		size += int64(len(col.Name) + len(col.Type) + 32)
	}
	for _, row := range result.Rows {
		size += 96
		for key, value := range row {
			size += int64(len(key)) + estimateValue(value)
		}
	}
	return size
}

func estimateValue(value any) int64 {
	switch v := value.(type) {
	case nil:
		return 8
	case string:
		return int64(len(v))
	case []byte:
		return int64(len(v))
	case time.Time:
		return 24
	case map[string]any:
		size := int64(32)
		for key, item := range v {
			size += int64(len(key)) + estimateValue(item)
		}
		return size
	case []any:
		size := int64(24)
		for _, item := range v {
			size += estimateValue(item)
		}
		return size
	default:
		return 16
	}
}

// Len returns the number of live (including not-yet-expired) entries.
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
