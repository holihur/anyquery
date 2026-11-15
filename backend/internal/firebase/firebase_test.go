package firebase

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

func openFirebase(t *testing.T, cfg map[string]string) connector.Connector {
	t.Helper()
	c, err := connector.Open(context.Background(), "firebase", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestFirestoreFetchPaginationAndValues(t *testing.T) {
	var tokens, sizes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":listCollectionIds") {
			_ = json.NewEncoder(w).Encode(map[string]any{"collectionIds": []string{"users"}})
			return
		}
		if r.URL.Path == "/v1/projects/p/databases/(default)/documents" {
			_ = json.NewEncoder(w).Encode(map[string]any{"documents": []any{}})
			return
		}
		if r.URL.Path != "/v1/projects/p/databases/(default)/documents/users" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("auth = %q", got)
		}
		tokens = append(tokens, r.URL.Query().Get("pageToken"))
		sizes = append(sizes, r.URL.Query().Get("pageSize"))
		if r.URL.Query().Get("pageToken") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"documents": []any{
					map[string]any{
						"name": "projects/p/databases/(default)/documents/users/u1",
						"fields": map[string]any{
							"name":   map[string]any{"stringValue": "Ada"},
							"age":    map[string]any{"integerValue": "36"},
							"score":  map[string]any{"doubleValue": 9.5},
							"active": map[string]any{"booleanValue": true},
							"seen":   map[string]any{"timestampValue": "2024-01-02T03:04:05Z"},
							"tags":   map[string]any{"arrayValue": map[string]any{"values": []any{map[string]any{"stringValue": "a"}, map[string]any{"stringValue": "b"}}}},
							"profile": map[string]any{"mapValue": map[string]any{"fields": map[string]any{
								"city": map[string]any{"stringValue": "Paris"},
							}}},
							"optional": map[string]any{"nullValue": nil},
						},
						"createTime": "2024-01-01T00:00:00Z",
						"updateTime": "2024-01-02T00:00:00Z",
					},
				},
				"nextPageToken": "next",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"documents": []any{map[string]any{
				"name":   "projects/p/databases/(default)/documents/users/u2",
				"fields": map[string]any{"name": map[string]any{"stringValue": "Bob"}},
			}},
			"nextPageToken": "more",
		})
	}))
	defer srv.Close()

	c := openFirebase(t, map[string]string{
		"service": "firestore", "project_id": "p", "token": "token",
		"endpoint": srv.URL + "/v1/projects/p/databases/(default)/documents",
	})
	// Ping already consumed the first page.
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "users", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || !res.Truncated {
		t.Fatalf("rows=%d truncated=%v", len(res.Rows), res.Truncated)
	}
	row := res.Rows[0]
	if row["_id"] != "u1" || row["name"] != "Ada" || row["age"] != int64(36) || row["score"] != 9.5 || row["active"] != true || row["optional"] != nil {
		t.Fatalf("row = %+v", row)
	}
	if _, ok := row["seen"].(time.Time); !ok {
		t.Fatalf("seen = %#v", row["seen"])
	}
	if tags, ok := row["tags"].([]any); !ok || len(tags) != 2 || tags[1] != "b" {
		t.Fatalf("tags = %#v", row["tags"])
	}
	if profile, ok := row["profile"].(map[string]any); !ok || profile["city"] != "Paris" {
		t.Fatalf("profile = %#v", row["profile"])
	}
	if got := strings.Join(tokens, ","); got != ",next" {
		t.Fatalf("page tokens = %q", got)
	}
	if got := strings.Join(sizes, ","); got != "2,1" {
		t.Fatalf("page sizes = %q", got)
	}
}

func TestFirestoreIntrospectCollections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":listCollectionIds"):
			_ = json.NewEncoder(w).Encode(map[string]any{"collectionIds": []string{"users", "orders"}})
		case strings.HasSuffix(r.URL.Path, "/documents"):
			_ = json.NewEncoder(w).Encode(map[string]any{"documents": []any{}})
		case strings.HasSuffix(r.URL.Path, "/documents/users"):
			_ = json.NewEncoder(w).Encode(map[string]any{"documents": []any{map[string]any{
				"name":   "projects/p/databases/(default)/documents/users/u1",
				"fields": map[string]any{"name": map[string]any{"stringValue": "Ada"}},
			}}})
		case strings.HasSuffix(r.URL.Path, "/documents/orders"):
			_ = json.NewEncoder(w).Encode(map[string]any{"documents": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := openFirebase(t, map[string]string{
		"service": "firestore", "project_id": "p", "collections": "users,orders",
		"endpoint": srv.URL + "/v1/projects/p/databases/(default)/documents",
	})
	tables, err := c.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 || tables[0].Name != "users" || tables[1].Name != "orders" {
		t.Fatalf("tables = %+v", tables)
	}
	if len(tables[0].Columns) < 2 || len(tables[1].Columns) != 3 {
		t.Fatalf("columns = %+v / %+v", tables[0].Columns, tables[1].Columns)
	}
}

func TestFirestoreListCollections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":listCollectionIds") {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"collectionIds": []string{"users"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"documents": []any{}})
	}))
	defer srv.Close()
	c := openFirebase(t, map[string]string{
		"service": "firestore", "project_id": "p",
		"endpoint": srv.URL + "/v1/projects/p/databases/(default)/documents",
	})
	tables, err := c.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Name != "users" {
		t.Fatalf("tables = %+v", tables)
	}
}

func TestRealtimeDatabaseFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users.json" || r.URL.Query().Get("auth") != "secret" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"u2": map[string]any{"name": "Bob", "age": 40},
			"u1": map[string]any{"name": "Ada", "age": 36},
		})
	}))
	defer srv.Close()
	c := openFirebase(t, map[string]string{
		"service": "realtime", "database_url": srv.URL, "path": "users", "token": "secret",
	})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "data", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || !res.Truncated || res.Rows[0]["_key"] != "u1" || res.Rows[0]["name"] != "Ada" {
		t.Fatalf("rows=%+v truncated=%v", res.Rows, res.Truncated)
	}
	tables, err := c.Introspect(context.Background())
	if err != nil || len(tables) != 1 || tables[0].Name != "data" {
		t.Fatalf("tables=%+v err=%v", tables, err)
	}
	if _, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "wrong", Limit: 1}); err == nil {
		t.Fatal("expected unknown table error")
	}
}

func TestFirebaseValidation(t *testing.T) {
	if _, err := connector.Open(context.Background(), "firebase", map[string]string{"service": "nope"}); err == nil {
		t.Fatal("expected service error")
	}
	if _, err := connector.Open(context.Background(), "firebase", map[string]string{"service": "firestore"}); err == nil {
		t.Fatal("expected project error")
	}
	if _, err := connector.Open(context.Background(), "firebase", map[string]string{"service": "realtime"}); err == nil {
		t.Fatal("expected URL/project error")
	}
	if _, err := connector.Open(context.Background(), "firebase", map[string]string{"service": "realtime", "database_url": "ftp://x"}); err == nil {
		t.Fatal("expected URL validation error")
	}
}
