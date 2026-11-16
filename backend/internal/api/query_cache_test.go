package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/engine"
	"github.com/anyquery/anyquery/internal/querycache"
)

func TestQueryCacheKeyIsTenantScopedAndStable(t *testing.T) {
	base := queryCacheKey("tenant-a", "SELECT 1", []any{1, "x"})
	if base != queryCacheKey("tenant-a", "SELECT 1", []any{1, "x"}) {
		t.Fatal("cache key is not deterministic")
	}
	if !strings.HasPrefix(base, "tenant-a\x00") {
		t.Fatalf("key = %q", base)
	}
	if base == queryCacheKey("tenant-b", "SELECT 1", []any{1, "x"}) {
		t.Fatal("cache key is not tenant scoped")
	}
	if base == queryCacheKey("tenant-a", "SELECT 2", []any{1, "x"}) {
		t.Fatal("cache key ignores SQL")
	}
	if base == queryCacheKey("tenant-a", "SELECT 1", []any{1, "y"}) {
		t.Fatal("cache key ignores params")
	}
}

func TestExecuteCachedLocalHit(t *testing.T) {
	s := &Server{
		engine:     engine.New(engine.DefaultOptions()),
		queryCache: querycache.New(time.Minute, 10),
	}
	first, hit, err := s.executeCached(context.Background(), "t", "SELECT 1", nil, nil)
	if err != nil || hit || first == nil {
		t.Fatalf("first=%+v hit=%v err=%v", first, hit, err)
	}
	second, hit, err := s.executeCached(context.Background(), "t", "SELECT 1", nil, nil)
	if err != nil || !hit || second == nil {
		t.Fatalf("second=%+v hit=%v err=%v", second, hit, err)
	}
}
