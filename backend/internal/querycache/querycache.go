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
	items    map[string]*list.Element
	lru      *list.List
	inflight map[string]*call
}

func New(ttl time.Duration, max int) *Cache {
	if max <= 0 {
		max = 200
	}
	return &Cache{
		ttl:      ttl,
		max:      max,
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
	if element, ok := c.items[key]; ok {
		item := element.Value.(*entry)
		item.value = result
		item.expiry = time.Now().Add(c.ttl)
		c.lru.MoveToFront(element)
		return
	}
	element := c.lru.PushFront(&entry{key: key, value: result, expiry: time.Now().Add(c.ttl)})
	c.items[key] = element
	for c.lru.Len() > c.max {
		c.removeLocked(c.lru.Back())
	}
}

func (c *Cache) removeLocked(element *list.Element) {
	if element == nil {
		return
	}
	item := element.Value.(*entry)
	delete(c.items, item.key)
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

// Len returns the number of live (including not-yet-expired) entries.
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
