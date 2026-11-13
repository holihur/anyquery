package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/anyquery/anyquery/internal/connector"
)

func TestOpenAPIConnector(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pets":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "name": "Milo"}, {"id": 2, "name": "Luna"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	spec := `openapi: 3.0.0
info: {title: pets, version: "1"}
servers: [{url: "` + api.URL + `"}]
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Pet"
components:
  schemas:
    Pet:
      type: object
      properties:
        id: {type: integer}
        name: {type: string}
`
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := (Provider{}).Open(context.Background(), map[string]string{"spec": path})
	if err != nil {
		t.Fatal(err)
	}
	tables, err := c.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Name != "listpets" || len(tables[0].Columns) != 2 {
		t.Fatalf("tables = %+v", tables)
	}
	res, err := c.Fetch(context.Background(), connectorFetch("listpets", 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Rows[0]["name"] != "Milo" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func connectorFetch(table string, limit int) connector.FetchRequest {
	return connector.FetchRequest{Table: table, Limit: limit}
}

func TestOpenAPIPagination(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		all := []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}, {"id": 4}, {"id": 5}}
		start := 0
		if page == "2" {
			start = 2
		} else if page == "3" {
			start = 4
		}
		end := start + 2
		if end > len(all) {
			end = len(all)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": all[start:end]})
	}))
	defer api.Close()
	spec := `openapi: 3.0.0
servers: [{url: "` + api.URL + `"}]
paths:
  /items:
    get:
      operationId: listItems
      parameters:
        - {name: page, in: query, schema: {type: integer}}
        - {name: page_size, in: query, schema: {type: integer}}
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  data:
                    type: array
                    items:
                      type: object
                      properties:
                        id: {type: integer}
`
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := (Provider{}).Open(context.Background(), map[string]string{"spec": path, "page_size": "2", "max_pages": "10"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Fetch(context.Background(), connectorFetch("listitems", 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 || !res.Truncated {
		t.Fatalf("rows = %d truncated=%v %+v", len(res.Rows), res.Truncated, res.Rows)
	}
	if res.Columns[0].Name != "id" {
		t.Fatalf("columns = %+v", res.Columns)
	}
}

func TestOpenAPIPostBodyAndLinkPagination(t *testing.T) {
	var gotMethod, gotBody string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		if r.URL.Path == "/search" {
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(r.Body)
			gotBody = buf.String()
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1}})
			return
		}
		if r.URL.Path == "/items" {
			if r.URL.Query().Get("after") == "" {
				w.Header().Set("Link", "</items?after=1>; rel=\"next\"")
				_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1}})
			} else {
				_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 2}})
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()
	spec := `openapi: 3.0.0
servers: [{url: "` + api.URL + `"}]
paths:
  /search:
    post:
      operationId: search
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    id: {type: integer}
  /items:
    get:
      operationId: listItems
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    id: {type: integer}
`
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := (Provider{}).Open(context.Background(), map[string]string{
		"spec": path, "allow_methods": "POST,GET", "body_template": `{"query":"{term}"}`, "query_params": `{"term":"pets"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Fetch(context.Background(), connectorFetch("search", 10))
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotBody != `{"query":"pets"}` || len(res.Rows) != 1 {
		t.Fatalf("method=%s body=%s rows=%+v", gotMethod, gotBody, res.Rows)
	}
	res, err = c.Fetch(context.Background(), connectorFetch("listitems", 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Rows[0]["id"] != float64(1) || res.Rows[1]["id"] != float64(2) {
		t.Fatalf("paginated rows = %+v", res.Rows)
	}
}

func TestOpenAPIOAuthClientCredentials(t *testing.T) {
	var tokenCalls int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			atomic.AddInt32(&tokenCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "oa-token", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer oa-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1}})
	}))
	defer api.Close()
	spec := `openapi: 3.0.0
servers: [{url: "` + api.URL + `"}]
paths:
  /items:
    get:
      operationId: listItems
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    id: {type: integer}
`
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := (Provider{}).Open(context.Background(), map[string]string{
		"spec": path, "auth_type": "client_credentials",
		"token_url": api.URL + "/token", "client_id": "client", "client_secret": "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := c.Fetch(context.Background(), connectorFetch("listitems", 10)); err != nil {
			t.Fatal(err)
		}
	}
	if atomic.LoadInt32(&tokenCalls) != 1 {
		t.Fatalf("token calls = %d, want 1", tokenCalls)
	}
}
