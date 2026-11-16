package metrics

import (
	"testing"
	"time"
)

func TestRegistrySnapshot(t *testing.T) {
	r := New()
	r.Observe("a", 10*time.Millisecond, 100, false, false)
	r.Observe("a", 20*time.Millisecond, 200, true, false)
	r.Observe("a", 30*time.Millisecond, 0, false, true)
	r.Observe("b", 40*time.Millisecond, 50, false, false)
	a := r.Tenant("a")
	if a.Total != 3 || a.Success != 2 || a.Errors != 1 || a.CacheHits != 1 || a.ScannedRows != 300 {
		t.Fatalf("snapshot = %+v", a)
	}
	if a.P50MS != 20 || a.P95MS != 30 {
		t.Fatalf("percentiles = %v/%v", a.P50MS, a.P95MS)
	}
	if a.CacheHitPct < 33 || a.CacheHitPct > 34 {
		t.Fatalf("hit pct = %v", a.CacheHitPct)
	}
	g := r.Global()
	if g.Total != 4 || g.ScannedRows != 350 {
		t.Fatalf("global = %+v", g)
	}
}

func TestEmptyAndMissingTenant(t *testing.T) {
	r := New()
	if got := r.Tenant("missing"); got.Total != 0 || got.UptimeSec < 0 {
		t.Fatalf("missing = %+v", got)
	}
	if got := r.Global(); got.Total != 0 {
		t.Fatalf("global = %+v", got)
	}
}

func TestSampleRingIsBounded(t *testing.T) {
	r := New()
	for i := 0; i < maxSamples+100; i++ {
		r.Observe("a", time.Duration(i)*time.Millisecond, 0, false, false)
	}
	if got := r.Tenant("a"); got.Total != int64(maxSamples+100) || got.P95MS <= 0 {
		t.Fatalf("snapshot = %+v", got)
	}
}
