package api

import (
	"strings"
	"testing"
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
