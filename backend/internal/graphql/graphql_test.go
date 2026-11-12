package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyquery/anyquery/internal/connector"
)

func TestGraphQLPaginationAndVariables(t *testing.T) {
	var calls int
	var gotLimit any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Query == "query { __typename }" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"__typename": "Query"}})
			return
		}
		calls++
		gotLimit = body.Variables["limit"]
		if body.Variables["after"] == "cursor-2" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"users":    []map[string]any{{"id": 3, "name": "C"}},
				"pageInfo": map[string]any{"hasNextPage": false, "endCursor": "cursor-3"},
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"users":    []map[string]any{{"id": 1, "name": "A"}, {"id": 2, "name": nil}},
			"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "cursor-2"},
		}})
	}))
	defer srv.Close()
	c, err := (provider{}).Open(context.Background(), map[string]string{
		"endpoint":        srv.URL,
		"query":           "query($limit:Int,$after:String){users(limit:$limit,after:$after){id name}}",
		"table_name":      "users",
		"data_path":       "users",
		"columns":         `{"id":"integer","name":"string"}`,
		"cursor_variable": "after",
		"cursor_path":     "pageInfo.endCursor",
		"has_next_path":   "pageInfo.hasNextPage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "users", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || gotLimit != float64(10) {
		t.Fatalf("calls=%d limit=%v", calls, gotLimit)
	}
	if len(res.Rows) != 3 || res.Rows[2]["id"] != float64(3) || res.Truncated {
		t.Fatalf("rows=%+v truncated=%v", res.Rows, res.Truncated)
	}
	if len(res.Columns) != 2 || res.Columns[0].Name != "id" || res.Columns[0].Type != "integer" {
		t.Fatalf("columns=%+v", res.Columns)
	}
}

func TestGraphQLNamedQueriesAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Query == "query { bad }" {
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"message": "field bad not found"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []map[string]any{{"value": 1}}}})
	}))
	defer srv.Close()
	c, err := (provider{}).Open(context.Background(), map[string]string{
		"endpoint": srv.URL,
		"queries":  `{"ok":{"query":"query { ok { value } }"},"bad":{"query":"query { bad }","data_path":"items"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	tables, err := c.Introspect(context.Background())
	if err != nil || len(tables) != 2 {
		t.Fatalf("tables=%+v err=%v", tables, err)
	}
	if _, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "bad", Limit: 10}); err == nil {
		t.Fatal("expected GraphQL error")
	}
}

func TestGraphQLRejectsMutations(t *testing.T) {
	_, err := (provider{}).Open(context.Background(), map[string]string{
		"endpoint": "https://example.com/graphql",
		"query":    "mutation { deleteUser(id: 1) }",
	})
	if err == nil {
		t.Fatal("expected mutation rejection")
	}
}
