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
