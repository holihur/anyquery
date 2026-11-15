package pocketbase

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

func openPocketBase(t *testing.T, cfg map[string]string) connector.Connector {
	t.Helper()
	c, err := connector.Open(context.Background(), "pocketbase", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestPocketBaseFetchPagination(t *testing.T) {
	var pages, perPages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"code":200,"message":"healthy","data":{}}`))
			return
		}
		if r.URL.Path != "/api/collections/posts/records" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pages = append(pages, r.URL.Query().Get("page"))
		perPages = append(perPages, r.URL.Query().Get("perPage"))
		if page == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 1, "perPage": 2, "totalItems": 3, "totalPages": 2,
				"items": []any{
					map[string]any{"id": "p1", "created": "2024-01-01T00:00:00Z", "updated": "2024-01-02T00:00:00Z", "collectionId": "c1", "collectionName": "posts", "title": "One", "views": 10},
					map[string]any{"id": "p2", "created": "2024-01-03T00:00:00Z", "title": "Two", "views": 20},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"page": 2, "perPage": 2, "totalItems": 3, "totalPages": 2,
			"items": []any{map[string]any{"id": "p3", "title": "Three", "views": 30}},
		})
	}))
	defer srv.Close()
	c := openPocketBase(t, map[string]string{"endpoint": srv.URL, "token": "token", "page_size": "2"})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "posts", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 || res.Truncated {
		t.Fatalf("rows=%d truncated=%v", len(res.Rows), res.Truncated)
	}
	if strings.Join(pages, ",") != "1,2" || strings.Join(perPages, ",") != "2,2" {
		t.Fatalf("pages=%v perPages=%v", pages, perPages)
	}
	row := res.Rows[0]
	if row["_id"] != "p1" || row["title"] != "One" || row["views"] != 10.0 || row["_collection_name"] != "posts" {
		t.Fatalf("row = %+v", row)
	}
	if _, ok := row["_created_at"].(time.Time); !ok {
		t.Fatalf("created = %#v", row["_created_at"])
	}
	if _, ok := row["id"]; ok {
		t.Fatalf("id leaked: %+v", row)
	}
}

func TestPocketBasePushdown(t *testing.T) {
	var filter, fields string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"code":200}`))
			return
		}
		filter = r.URL.Query().Get("filter")
		fields = r.URL.Query().Get("fields")
		_ = json.NewEncoder(w).Encode(map[string]any{"page": 1, "perPage": 5, "totalItems": 1, "totalPages": 1, "items": []any{map[string]any{"id": "p1", "name": "Ada"}}})
	}))
	defer srv.Close()
	c := openPocketBase(t, map[string]string{"endpoint": srv.URL})
	_, err := c.Fetch(context.Background(), connector.FetchRequest{
		Table: "posts", Limit: 5,
		Columns: []string{"name", "_id"},
		Predicates: []connector.Predicate{
			{Column: "age", Op: "gte", Value: 18},
			{Column: "name", Op: "eq", Value: "Ada"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filter != `age>=18 && name="Ada"` {
		t.Fatalf("filter = %q", filter)
	}
	if fields != "name,id" {
		t.Fatalf("fields = %q", fields)
	}
}

func TestPocketBaseIntrospectAndListCollections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/health":
			_, _ = w.Write([]byte(`{"code":200}`))
		case r.URL.Path == "/api/collections":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"name": "posts"}, map[string]any{"name": "users"}}})
		case strings.HasPrefix(r.URL.Path, "/api/collections/posts/records"):
			_ = json.NewEncoder(w).Encode(map[string]any{"page": 1, "perPage": 1, "totalItems": 1, "totalPages": 1, "items": []any{map[string]any{"id": "p1", "title": "One"}}})
		case strings.HasPrefix(r.URL.Path, "/api/collections/users/records"):
			_ = json.NewEncoder(w).Encode(map[string]any{"page": 1, "perPage": 1, "totalItems": 0, "totalPages": 0, "items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := openPocketBase(t, map[string]string{"endpoint": srv.URL})
	tables, err := c.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 || tables[0].Name != "posts" || tables[0].Columns[0].Name != "_id" {
		t.Fatalf("tables = %+v", tables)
	}
}

func TestPocketBaseErrors(t *testing.T) {
	if _, err := connector.Open(context.Background(), "pocketbase", map[string]string{}); err == nil {
		t.Fatal("expected endpoint error")
	}
	if _, err := connector.Open(context.Background(), "pocketbase", map[string]string{"endpoint": "ftp://x"}); err == nil {
		t.Fatal("expected URL error")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"code":200}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := openPocketBase(t, map[string]string{"endpoint": srv.URL, "collections": "posts"})
	if _, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "missing", Limit: 1}); err == nil {
		t.Fatal("expected HTTP error")
	}
	stmt, args, err := c.(connector.StatementDescriber).DescribeFetch(connector.FetchRequest{Table: "posts", Limit: 1})
	if err != nil || !strings.Contains(stmt, "perPage") || args != nil {
		t.Fatalf("stmt=%q args=%v err=%v", stmt, args, err)
	}
}
