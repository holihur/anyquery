package httpjson

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/anyquery/anyquery/internal/connector"
)

func TestRESTPagePagination(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if size == 0 {
			size = 2
		}
		start := (page - 1) * size
		rows := []map[string]any{}
		for i := start; i < start+size && i < 4; i++ {
			rows = append(rows, map[string]any{"id": i + 1, "name": "item-" + strconv.Itoa(i+1)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
	}))
	defer srv.Close()

	c, err := (restProvider{}).Open(context.Background(), map[string]string{
		"url": srv.URL, "table_name": "items", "pagination": "page",
		"page_size": "2", "max_pages": "5", "records_path": "data",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "items", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 4 || res.Truncated || atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("rows=%d truncated=%v calls=%d", len(res.Rows), res.Truncated, calls)
	}
	if res.Rows[3]["name"] != "item-4" {
		t.Fatalf("rows=%+v", res.Rows)
	}
}

func TestRESTLinkPaginationAndDescribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "" {
			w.Header().Set("Link", "</next?after=2>; rel=\"next\"")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1}})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 2}})
	}))
	defer srv.Close()
	c, err := (restProvider{}).Open(context.Background(), map[string]string{
		"url": srv.URL, "table_name": "items", "pagination": "link",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "items", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	stmt, _, err := c.(connector.StatementDescriber).DescribeFetch(connector.FetchRequest{Limit: 10})
	if err != nil || stmt == "" {
		t.Fatalf("statement=%q err=%v", stmt, err)
	}
}

func TestRESTOAuthClientCredentials(t *testing.T) {
	var tokenCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			atomic.AddInt32(&tokenCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-token", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1}})
	}))
	defer srv.Close()
	c, err := (restProvider{}).Open(context.Background(), map[string]string{
		"url": srv.URL + "/data", "table_name": "items",
		"auth_type": "client_credentials", "token_url": srv.URL + "/token",
		"client_id": "client", "client_secret": "secret", "scope": "read data",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "items", Limit: 10}); err != nil {
			t.Fatal(err)
		}
	}
	if atomic.LoadInt32(&tokenCalls) != 1 {
		t.Fatalf("token calls = %d, want 1", tokenCalls)
	}
}

func TestRESTCursorPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "c2" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": 3}}, "next_cursor": ""})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": 1}, {"id": 2}}, "next_cursor": "c2"})
	}))
	defer srv.Close()
	c, err := (restProvider{}).Open(context.Background(), map[string]string{
		"url": srv.URL, "table_name": "items", "pagination": "cursor",
		"cursor_path": "next_cursor", "records_path": "data",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "items", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 || res.Rows[2]["id"] != float64(3) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestRESTPostBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["query"] != "test" {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1}})
	}))
	defer srv.Close()
	c, err := (restProvider{}).Open(context.Background(), map[string]string{
		"url": srv.URL, "table_name": "items", "http_method": "POST",
		"body_template": `{"query":"test"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "items", Limit: 10})
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("rows=%+v err=%v", res, err)
	}
}
