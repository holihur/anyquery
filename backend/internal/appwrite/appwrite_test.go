package appwrite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

func openAppwrite(t *testing.T, cfg map[string]string) connector.Connector {
	t.Helper()
	c, err := connector.Open(context.Background(), "appwrite", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestAppwriteFetchPagination(t *testing.T) {
	var limits, offsets []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health/version" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "1.6.0"})
			return
		}
		if r.URL.Path != "/v1/databases/db/collections/users/documents" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Appwrite-Project") != "project" || r.Header.Get("X-Appwrite-Key") != "key" {
			t.Errorf("headers = %v", r.Header)
		}
		queries := r.URL.Query()["queries[]"]
		var limit, offset string
		for _, q := range queries {
			if strings.HasPrefix(q, "limit(") {
				limit = strings.TrimSuffix(strings.TrimPrefix(q, "limit("), ")")
			}
			if strings.HasPrefix(q, "offset(") {
				offset = strings.TrimSuffix(strings.TrimPrefix(q, "offset("), ")")
			}
		}
		limits = append(limits, limit)
		offsets = append(offsets, offset)
		if offset == "0" {
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 2, "documents": []any{
				map[string]any{"$id": "u1", "$createdAt": "2024-01-01T00:00:00Z", "$updatedAt": "2024-01-02T00:00:00Z", "$permissions": []string{"read()"}, "name": "Ada", "age": 36},
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": 2, "documents": []any{
			map[string]any{"$id": "u2", "$createdAt": "2024-02-01T00:00:00Z", "name": "Bob"},
		}})
	}))
	defer srv.Close()

	c := openAppwrite(t, map[string]string{"endpoint": srv.URL + "/v1", "project_id": "project", "database_id": "db", "api_key": "key", "page_size": "1"})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "users", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Truncated {
		t.Fatalf("rows=%d truncated=%v", len(res.Rows), res.Truncated)
	}
	if got := strings.Join(limits, ","); got != "1,1" {
		t.Fatalf("limits = %s", got)
	}
	if got := strings.Join(offsets, ","); got != "0,1" {
		t.Fatalf("offsets = %s", got)
	}
	row := res.Rows[0]
	if row["_id"] != "u1" || row["name"] != "Ada" || row["age"] != 36.0 {
		t.Fatalf("row = %+v", row)
	}
	if _, ok := row["_created_at"].(time.Time); !ok {
		t.Fatalf("created = %#v", row["_created_at"])
	}
	if perms, ok := row["_permissions"].([]any); !ok || len(perms) != 1 || perms[0] != "read()" {
		t.Fatalf("permissions = %#v", row["_permissions"])
	}
	if _, ok := row["$id"]; ok {
		t.Fatalf("system field leaked: %+v", row)
	}
}

func TestAppwriteIntrospectAndListCollections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/health/version":
			_, _ = w.Write([]byte(`{"version":"1.6.0"}`))
		case r.URL.Path == "/v1/databases/db/collections":
			_ = json.NewEncoder(w).Encode(map[string]any{"collections": []any{map[string]any{"$id": "users"}, map[string]any{"$id": "orders"}}})
		case strings.HasPrefix(r.URL.Path, "/v1/databases/db/collections/users/documents"):
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 1, "documents": []any{map[string]any{"$id": "u1", "name": "Ada", "nested": map[string]any{"x": 1}}}})
		case strings.HasPrefix(r.URL.Path, "/v1/databases/db/collections/orders/documents"):
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 0, "documents": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := openAppwrite(t, map[string]string{"endpoint": srv.URL + "/v1", "project_id": "project", "database_id": "db"})
	tables, err := c.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 || tables[0].Name != "users" || tables[0].Columns[0].Name != "_id" {
		t.Fatalf("tables = %+v", tables)
	}
}

func TestAppwriteValidationAndJWT(t *testing.T) {
	if _, err := connector.Open(context.Background(), "appwrite", map[string]string{"project_id": "p", "database_id": "d"}); err == nil {
		t.Fatal("expected endpoint error")
	}
	if _, err := connector.Open(context.Background(), "appwrite", map[string]string{"endpoint": "http://x", "database_id": "d"}); err == nil {
		t.Fatal("expected project error")
	}
	if _, err := connector.Open(context.Background(), "appwrite", map[string]string{"endpoint": "http://x", "project_id": "p"}); err == nil {
		t.Fatal("expected database error")
	}
	var gotJWT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotJWT = r.Header.Get("X-Appwrite-JWT")
		_, _ = w.Write([]byte(`{"version":"1.6.0"}`))
	}))
	defer srv.Close()
	openAppwrite(t, map[string]string{"endpoint": srv.URL, "project_id": "p", "database_id": "d", "jwt": "jwt-token"})
	if gotJWT != "jwt-token" {
		t.Fatalf("jwt header = %q", gotJWT)
	}
}

func TestAppwriteQueryEncoding(t *testing.T) {
	q := url.Values{}
	q.Add("queries[]", "limit(10)")
	q.Add("queries[]", "offset(20)")
	if got := q.Encode(); !strings.Contains(got, "queries%5B%5D=limit%2810%29") {
		t.Fatalf("encoded = %s", got)
	}
}
