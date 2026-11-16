package querycache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/engine"
)

func TestCacheHitAndExpiry(t *testing.T) {
	c := New(30*time.Millisecond, 2)
	var calls int32
	load := func() (*engine.Result, error) {
		atomic.AddInt32(&calls, 1)
		return &engine.Result{RowCount: 1}, nil
	}
	first, hit, err := c.GetOrLoad(context.Background(), "k", load)
	if err != nil || hit || first.RowCount != 1 {
		t.Fatalf("first=%+v hit=%v err=%v", first, hit, err)
	}
	second, hit, err := c.GetOrLoad(context.Background(), "k", load)
	if err != nil || !hit || second != first {
		t.Fatalf("second=%+v hit=%v err=%v", second, hit, err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d", got)
	}
	time.Sleep(40 * time.Millisecond)
	_, hit, err = c.GetOrLoad(context.Background(), "k", load)
	if err != nil || hit {
		t.Fatalf("expired hit=%v err=%v", hit, err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls after expiry = %d", got)
	}
}

func TestCacheSingleflight(t *testing.T) {
	c := New(time.Minute, 4)
	var calls int32
	start := make(chan struct{})
	load := func() (*engine.Result, error) {
		atomic.AddInt32(&calls, 1)
		<-start
		return &engine.Result{RowCount: 7}, nil
	}
	const workers = 12
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_, _, err := c.GetOrLoad(context.Background(), "same", load)
			if err != nil {
				t.Errorf("load: %v", err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(start)
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
}

func TestCacheLRUAndInvalidate(t *testing.T) {
	c := New(time.Minute, 2)
	c.GetOrLoad(context.Background(), "a\x00x", func() (*engine.Result, error) { return &engine.Result{RowCount: 1}, nil })
	c.GetOrLoad(context.Background(), "a\x00y", func() (*engine.Result, error) { return &engine.Result{RowCount: 2}, nil })
	c.GetOrLoad(context.Background(), "b\x00z", func() (*engine.Result, error) { return &engine.Result{RowCount: 3}, nil })
	if c.Len() != 2 {
		t.Fatalf("len = %d", c.Len())
	}
	c.InvalidateTenant("a")
	if c.Len() != 1 {
		t.Fatalf("len after invalidate = %d", c.Len())
	}
	if _, ok := c.Get("a\x00x"); ok {
		t.Fatal("invalidated entry still present")
	}
	if _, ok := c.Get("b\x00z"); !ok {
		t.Fatal("other tenant entry was removed")
	}
}

func TestCacheDisabled(t *testing.T) {
	c := New(0, 2)
	var calls int32
	load := func() (*engine.Result, error) {
		atomic.AddInt32(&calls, 1)
		return &engine.Result{}, nil
	}
	c.GetOrLoad(context.Background(), "k", load)
	c.GetOrLoad(context.Background(), "k", load)
	if calls != 2 || c.Enabled() {
		t.Fatalf("calls=%d enabled=%v", calls, c.Enabled())
	}
}

func BenchmarkCacheHit(b *testing.B) {
	c := New(time.Minute, 100)
	c.GetOrLoad(context.Background(), "k", func() (*engine.Result, error) { return &engine.Result{}, nil })
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, ok, _ := c.GetOrLoad(context.Background(), "k", func() (*engine.Result, error) { return &engine.Result{}, nil })
		if !ok {
			b.Fatal("expected hit")
		}
	}
}

func TestInvalidateDoesNotStoreInflightResult(t *testing.T) {
	c := New(time.Minute, 4)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_, _, _ = c.GetOrLoad(context.Background(), "t\x00slow", func() (*engine.Result, error) {
			close(started)
			<-release
			return &engine.Result{RowCount: 1}, nil
		})
		close(done)
	}()
	<-started
	c.InvalidateTenant("t")
	close(release)
	<-done
	if _, ok := c.Get("t\x00slow"); ok {
		t.Fatal("inflight result was stored after invalidation")
	}
}

func TestCacheByteBudget(t *testing.T) {
	c := NewSized(time.Minute, 10, 512)
	c.GetOrLoad(context.Background(), "small", func() (*engine.Result, error) {
		return &engine.Result{Rows: []map[string]any{{"x": "y"}}}, nil
	})
	c.GetOrLoad(context.Background(), "large", func() (*engine.Result, error) {
		rows := make([]map[string]any, 20)
		for i := range rows {
			rows[i] = map[string]any{"x": string(make([]byte, 100))}
		}
		return &engine.Result{Rows: rows}, nil
	})
	if c.Len() != 1 || c.Bytes() == 0 {
		t.Fatalf("len=%d bytes=%d", c.Len(), c.Bytes())
	}
	if _, ok := c.Get("large"); ok {
		t.Fatal("oversized result should not be cached")
	}
}
