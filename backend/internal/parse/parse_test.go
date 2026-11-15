package parse

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

func openParse(t *testing.T, cfg map[string]string) connector.Connector {
	t.Helper()
	c, err := connector.Open(context.Background(), "parse", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestParseFetchPaginationAndTypes(t *testing.T) {
	var skips, limits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/parse/health" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		if r.URL.Path != "/parse/classes/Post" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Parse-Application-Id") != "app" || r.Header.Get("X-Parse-REST-API-Key") != "rest" {
			t.Errorf("headers = %v", r.Header)
		}
		skips = append(skips, r.URL.Query().Get("skip"))
		limits = append(limits, r.URL.Query().Get("limit"))
		skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
		if skip == 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 3,
				"results": []any{
					map[string]any{
						"objectId": "p1", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-02T00:00:00Z",
						"title": "One", "views": 10, "published": true, "when": map[string]any{"__type": "Date", "iso": "2024-02-03T04:05:06Z"},
						"ACL": map[string]any{"*": map[string]any{"read": true}},
					},
					map[string]any{"objectId": "p2", "title": "Two"},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 3, "results": []any{map[string]any{"objectId": "p3", "title": "Three"}}})
	}))
	defer srv.Close()
	c := openParse(t, map[string]string{"endpoint": srv.URL + "/parse", "app_id": "app", "rest_api_key": "rest", "page_size": "2"})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "Post", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 || res.Truncated {
		t.Fatalf("rows=%d truncated=%v", len(res.Rows), res.Truncated)
	}
	if strings.Join(skips, ",") != "0,2" || strings.Join(limits, ",") != "2,1" {
		t.Fatalf("skips=%v limits=%v", skips, limits)
	}
	row := res.Rows[0]
	if row["_id"] != "p1" || row["title"] != "One" || row["views"] != 10.0 || row["published"] != true {
		t.Fatalf("row = %+v", row)
	}
	if _, ok := row["_created_at"].(time.Time); !ok {
		t.Fatalf("created = %#v", row["_created_at"])
	}
	if when, ok := row["when"].(time.Time); !ok || when.Unix() != 1706933106 {
		t.Fatalf("when = %#v", row["when"])
	}
	if _, ok := row["_acl"].(map[string]any); !ok {
		t.Fatalf("acl = %#v", row["_acl"])
	}
}

func TestParsePushdown(t *testing.T) {
	var where, keys string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		where = r.URL.Query().Get("where")
		keys = r.URL.Query().Get("keys")
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []any{map[string]any{"objectId": "p1", "name": "Ada"}}})
	}))
	defer srv.Close()
	c := openParse(t, map[string]string{"endpoint": srv.URL, "app_id": "app", "classes": "Post"})
	_, err := c.Fetch(context.Background(), connector.FetchRequest{
		Table: "Post", Limit: 5,
		Columns: []string{"name", "_id"},
		Predicates: []connector.Predicate{
			{Column: "age", Op: "gte", Value: 18},
			{Column: "name", Op: "eq", Value: "Ada"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if where != `{"$and":[{"age":{"$gte":18}},{"name":{"$eq":"Ada"}}]}` {
		t.Fatalf("where = %q", where)
	}
	if keys != "name,objectId" {
		t.Fatalf("keys = %q", keys)
	}
}

func TestParseIntrospectSchemas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/parse/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.URL.Path == "/parse/schemas":
			if r.Header.Get("X-Parse-Master-Key") != "master" {
				t.Errorf("master header = %q", r.Header.Get("X-Parse-Master-Key"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"className": "Post"}, map[string]any{"className": "_User"}}})
		case strings.HasPrefix(r.URL.Path, "/parse/classes/Post"):
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []any{map[string]any{"objectId": "p1", "title": "One"}}})
		case strings.HasPrefix(r.URL.Path, "/parse/classes/_User"):
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := openParse(t, map[string]string{"endpoint": srv.URL + "/parse", "app_id": "app", "master_key": "master"})
	tables, err := c.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 || tables[0].Name != "Post" || tables[0].Columns[0].Name != "_id" {
		t.Fatalf("tables = %+v", tables)
	}
}

func TestParseValidationAndErrors(t *testing.T) {
	if _, err := connector.Open(context.Background(), "parse", map[string]string{"app_id": "a"}); err == nil {
		t.Fatal("expected endpoint error")
	}
	if _, err := connector.Open(context.Background(), "parse", map[string]string{"endpoint": "http://x"}); err == nil {
		t.Fatal("expected app id error")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/classes/Missing":
			http.Error(w, `{"code":101,"error":"missing"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := openParse(t, map[string]string{"endpoint": srv.URL, "app_id": "a", "classes": "Missing"})
	if _, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "Missing", Limit: 1}); err == nil {
		t.Fatal("expected HTTP error")
	}
	if _, err := c.(*Conn).listClasses(context.Background()); err == nil {
		t.Fatal("expected master key requirement")
	}
}

func TestParseDecodeValue(t *testing.T) {
	got := decodeValue(map[string]any{"when": map[string]any{"__type": "Date", "iso": "2024-01-01T00:00:00Z"}})
	m := got.(map[string]any)
	if _, ok := m["when"].(time.Time); !ok {
		t.Fatalf("decoded = %#v", m)
	}
}
