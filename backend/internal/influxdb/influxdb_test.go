package influxdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anyquery/anyquery/internal/connector"
)

func TestIntrospectAndFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(q, "SHOW MEASUREMENTS"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{
				"series": []any{map[string]any{"name": "measurements", "columns": []string{"name"}, "values": [][]any{{"cpu"}}}},
			}}})
		case strings.HasPrefix(q, "SHOW FIELD KEYS"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{
				"series": []any{map[string]any{"name": "cpu", "columns": []string{"fieldKey", "fieldType"}, "values": [][]any{{"usage", "float"}, {"count", "integer"}}}},
			}}})
		case strings.HasPrefix(q, "SHOW TAG KEYS"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{
				"series": []any{map[string]any{"name": "cpu", "columns": []string{"tagKey"}, "values": [][]any{{"host"}}}},
			}}})
		case strings.HasPrefix(q, "SELECT"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{
				"series": []any{map[string]any{
					"name": "cpu", "tags": map[string]string{"host": "node1"},
					"columns": []string{"time", "usage", "count"},
					"values":  [][]any{{"2026-01-01T00:00:00Z", 0.5, 3}},
				}},
			}}})
		default:
			http.Error(w, "unexpected query: "+q, 400)
		}
	}))
	defer server.Close()

	provider := Provider{}
	c, err := provider.Open(context.Background(), map[string]string{"url": server.URL, "database": "metrics"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tables, err := c.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Name != "cpu" {
		t.Fatalf("tables = %+v", tables)
	}
	cols := map[string]string{}
	for _, col := range tables[0].Columns {
		cols[col.Name] = col.Type
	}
	if cols["time"] != "timestamp" || cols["host"] != "string" || cols["usage"] != "number" || cols["count"] != "integer" {
		t.Fatalf("columns = %+v", cols)
	}
	res, err := c.Fetch(context.Background(), connectorFetchRequest("cpu", 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["host"] != "node1" || res.Rows[0]["usage"] != 0.5 || res.Rows[0]["count"] != int64(3) {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func connectorFetchRequest(table string, limit int) connector.FetchRequest {
	return connector.FetchRequest{Table: table, Limit: limit}
}
