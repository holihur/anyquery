// Package connectorpool stores a small number of idle connectors per
// workspace/source. Connectors are leased exclusively: a pooled connector is
// never used by two queries at once, so connectors that are not concurrency
// safe remain safe. This removes repeated Mongo/SQL handshakes from dashboard
// panels and repeated queries while keeping query semantics unchanged.
package connectorpool

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

type idle struct {
	key        string
	generation uint64
	conn       connector.Connector
	lastUsed   time.Time
}

type Pool struct {
	mu          sync.Mutex
	maxIdle     int
	idleTTL     time.Duration
	generations map[string]uint64
	entries     map[string][]*idle
	closed      bool
}

func New(maxIdle int, idleTTL time.Duration) *Pool {
	if maxIdle <= 0 {
		maxIdle = 4
	}
	if idleTTL <= 0 {
		idleTTL = 5 * time.Minute
	}
	return &Pool{maxIdle: maxIdle, idleTTL: idleTTL, generations: map[string]uint64{}, entries: map[string][]*idle{}}
}

// Acquire returns a leased connector. Close releases it back to the pool.
func (p *Pool) Acquire(ctx context.Context, tenantID, sourceID string, open func(context.Context) (connector.Connector, error)) (connector.Connector, error) {
	if p == nil {
		return open(ctx)
	}
	tenantKey := tenantID
	p.mu.Lock()
	generation := p.generations[tenantKey]
	key := poolKey(tenantID, sourceID, generation)
	now := time.Now()
	p.purgeExpiredLocked(now)
	for i := len(p.entries[key]) - 1; i >= 0; i-- {
		item := p.entries[key][i]
		if now.Sub(item.lastUsed) > p.idleTTL {
			p.entries[key] = append(p.entries[key][:i], p.entries[key][i+1:]...)
			_ = item.conn.Close()
			continue
		}
		p.entries[key] = append(p.entries[key][:i], p.entries[key][i+1:]...)
		p.mu.Unlock()
		return &lease{pool: p, key: key, generation: generation, tenant: tenantID, conn: item.conn}, nil
	}
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return open(ctx)
	}
	conn, err := open(ctx)
	if err != nil {
		return nil, err
	}
	return &lease{pool: p, key: key, generation: generation, tenant: tenantID, conn: conn}, nil
}

func (p *Pool) release(l *lease) {
	if p == nil || l == nil || l.conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.generations[l.tenant] != l.generation || len(p.entries[l.key]) >= p.maxIdle {
		_ = l.conn.Close()
		return
	}
	p.entries[l.key] = append(p.entries[l.key], &idle{key: l.key, generation: l.generation, conn: l.conn, lastUsed: time.Now()})
	l.conn = nil
}

// InvalidateTenant closes idle connectors for the workspace and advances its
// generation so active leases close on return instead of being reused.
func (p *Pool) InvalidateTenant(tenantID string) {
	if p == nil {
		return
	}
	prefix := tenantID + "\x00"
	p.mu.Lock()
	p.generations[tenantID]++
	var closeList []connector.Connector
	for key, items := range p.entries {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		for _, item := range items {
			closeList = append(closeList, item.conn)
		}
		delete(p.entries, key)
	}
	p.mu.Unlock()
	for _, conn := range closeList {
		_ = conn.Close()
	}
}

// CloseIdle closes all idle connectors. Active leases remain responsible for
// closing themselves when released.
func (p *Pool) CloseIdle() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	var closeList []connector.Connector
	for _, items := range p.entries {
		for _, item := range items {
			closeList = append(closeList, item.conn)
		}
	}
	p.entries = map[string][]*idle{}
	p.mu.Unlock()
	for _, conn := range closeList {
		_ = conn.Close()
	}
}

// Len returns the number of idle connectors (for tests/metrics).
func (p *Pool) Len() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	total := 0
	for _, items := range p.entries {
		total += len(items)
	}
	return total
}

func (p *Pool) purgeExpiredLocked(now time.Time) {
	for key, items := range p.entries {
		kept := items[:0]
		for _, item := range items {
			if now.Sub(item.lastUsed) > p.idleTTL {
				_ = item.conn.Close()
				continue
			}
			kept = append(kept, item)
		}
		if len(kept) == 0 {
			delete(p.entries, key)
		} else {
			p.entries[key] = kept
		}
	}
}

func poolKey(tenantID, sourceID string, generation uint64) string {
	return tenantID + "\x00" + sourceID + "\x00" + strconvUint(generation)
}

func strconvUint(v uint64) string {
	// Avoid fmt in the hot path; uint64 -> decimal.
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

type lease struct {
	pool       *Pool
	key        string
	tenant     string
	generation uint64
	mu         sync.Mutex
	conn       connector.Connector
}

func (l *lease) Ping(ctx context.Context) error {
	l.mu.Lock()
	conn := l.conn
	l.mu.Unlock()
	if conn == nil {
		return context.Canceled
	}
	return conn.Ping(ctx)
}

func (l *lease) Introspect(ctx context.Context) ([]connector.Table, error) {
	l.mu.Lock()
	conn := l.conn
	l.mu.Unlock()
	if conn == nil {
		return nil, context.Canceled
	}
	return conn.Introspect(ctx)
}

func (l *lease) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	l.mu.Lock()
	conn := l.conn
	l.mu.Unlock()
	if conn == nil {
		return nil, context.Canceled
	}
	return conn.Fetch(ctx, req)
}

func (l *lease) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	l.mu.Lock()
	conn := l.conn
	l.mu.Unlock()
	if conn == nil {
		return "", nil, context.Canceled
	}
	if describer, ok := conn.(connector.StatementDescriber); ok {
		return describer.DescribeFetch(req)
	}
	return "", nil, fmt.Errorf("connector does not describe fetches")
}

func (l *lease) Capabilities() connector.Capabilities {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return connector.Capabilities{}
	}
	return l.conn.Capabilities()
}

func (l *lease) Close() error {
	l.mu.Lock()
	if l.conn == nil {
		l.mu.Unlock()
		return nil
	}
	conn := l.conn
	l.conn = nil
	l.mu.Unlock()
	_ = conn
	l.pool.release(&lease{pool: l.pool, key: l.key, tenant: l.tenant, generation: l.generation, conn: conn})
	return nil
}
