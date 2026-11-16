// Package metrics keeps small, bounded in-process query telemetry for the
// workspace performance view. It intentionally avoids a Prometheus dependency:
// the snapshot is cheap to compute and is exposed through the authenticated API.
package metrics

import (
	"math"
	"sort"
	"sync"
	"time"
)

const maxSamples = 2048

type tenantMetrics struct {
	total      int64
	success    int64
	errors     int64
	cacheHits  int64
	scanned    int64
	samples    []float64
	sampleNext int
}

// Registry is safe for concurrent use.
type Registry struct {
	mu      sync.Mutex
	started time.Time
	all     tenantMetrics
	tenants map[string]*tenantMetrics
}

func New() *Registry {
	return &Registry{started: time.Now().UTC(), tenants: map[string]*tenantMetrics{}}
}

// Observe records one completed query. cacheHit is true when the result came
// from the local/query cache without executing sources.
func (r *Registry) Observe(tenantID string, duration time.Duration, scannedRows int, cacheHit bool, failed bool) {
	if r == nil {
		return
	}
	ms := float64(duration.Microseconds()) / 1000
	r.mu.Lock()
	defer r.mu.Unlock()
	observe(&r.all, ms, scannedRows, cacheHit, failed)
	if tenantID != "" {
		m := r.tenants[tenantID]
		if m == nil {
			m = &tenantMetrics{}
			r.tenants[tenantID] = m
		}
		observe(m, ms, scannedRows, cacheHit, failed)
	}
}

func observe(m *tenantMetrics, durationMS float64, scanned int, cacheHit, failed bool) {
	m.total++
	if failed {
		m.errors++
	} else {
		m.success++
	}
	if cacheHit {
		m.cacheHits++
	}
	if scanned > 0 {
		m.scanned += int64(scanned)
	}
	if len(m.samples) < maxSamples {
		m.samples = append(m.samples, durationMS)
	} else {
		m.samples[m.sampleNext] = durationMS
		m.sampleNext = (m.sampleNext + 1) % maxSamples
	}
}

type Snapshot struct {
	Total       int64   `json:"total"`
	Success     int64   `json:"success"`
	Errors      int64   `json:"errors"`
	CacheHits   int64   `json:"cacheHits"`
	CacheMisses int64   `json:"cacheMisses"`
	CacheHitPct float64 `json:"cacheHitPct"`
	ScannedRows int64   `json:"scannedRows"`
	P50MS       float64 `json:"p50Ms"`
	P95MS       float64 `json:"p95Ms"`
	UptimeSec   int64   `json:"uptimeSec"`
}

func snapshot(m tenantMetrics, started time.Time) Snapshot {
	out := Snapshot{
		Total:       m.total,
		Success:     m.success,
		Errors:      m.errors,
		CacheHits:   m.cacheHits,
		CacheMisses: m.total - m.cacheHits,
		ScannedRows: m.scanned,
		UptimeSec:   int64(time.Since(started).Seconds()),
	}
	if m.total > 0 {
		out.CacheHitPct = float64(m.cacheHits) / float64(m.total) * 100
	}
	out.P50MS, out.P95MS = percentiles(m.samples)
	return out
}

func percentiles(samples []float64) (float64, float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	values := append([]float64(nil), samples...)
	sort.Float64s(values)
	pick := func(p float64) float64 {
		index := int(math.Ceil(float64(len(values))*p)) - 1
		if index < 0 {
			index = 0
		}
		if index >= len(values) {
			index = len(values) - 1
		}
		return values[index]
	}
	return pick(0.50), pick(0.95)
}

// Tenant returns the snapshot for one workspace.
func (r *Registry) Tenant(tenantID string) Snapshot {
	if r == nil {
		return Snapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.tenants[tenantID]
	if m == nil {
		return Snapshot{UptimeSec: int64(time.Since(r.started).Seconds())}
	}
	return snapshot(*m, r.started)
}

// Global returns aggregate server metrics.
func (r *Registry) Global() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return snapshot(r.all, r.started)
}
