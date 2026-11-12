package openapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
