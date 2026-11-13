package all

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyquery/anyquery/internal/connector"
)

func openPreset(t *testing.T, typ string, cfg map[string]string) connector.Connector {
	t.Helper()
	c, err := connector.Open(context.Background(), typ, cfg)
	if err != nil {
		t.Fatalf("open %s: %v", typ, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestGitHubRecipeLinkPagination(t *testing.T) {
	calls := 0
	var perPage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		perPage = r.URL.Query().Get("per_page")
		if r.URL.Path == "/next" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": 2}}})
			return
		}
		w.Header().Set("Link", `</next?per_page=100>; rel="next"`)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": 1}}})
	}))
	defer srv.Close()

	c := openPreset(t, "github", map[string]string{"url": srv.URL})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "issues", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || calls != 2 || perPage != "100" {
		t.Fatalf("rows=%d calls=%d per_page=%q", len(res.Rows), calls, perPage)
	}
}

func TestJiraRecipeOffsetPagination(t *testing.T) {
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("startAt")
		starts = append(starts, start)
		if start != "0" {
			_ = json.NewEncoder(w).Encode(map[string]any{"issues": []map[string]any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issues": []map[string]any{{"id": 1}}})
	}))
	defer srv.Close()

	c := openPreset(t, "jira", map[string]string{"url": srv.URL})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "issues", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || len(starts) != 2 || starts[0] != "0" || starts[1] != "1" {
		t.Fatalf("rows=%d starts=%v", len(res.Rows), starts)
	}
}

func TestSlackRecipeCursorPagination(t *testing.T) {
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		cursors = append(cursors, cursor)
		if cursor == "c2" {
			_ = json.NewEncoder(w).Encode(map[string]any{"members": []map[string]any{{"id": 2}}, "response_metadata": map[string]any{"next_cursor": ""}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"members": []map[string]any{{"id": 1}}, "response_metadata": map[string]any{"next_cursor": "c2"}})
	}))
	defer srv.Close()

	c := openPreset(t, "slack", map[string]string{"url": srv.URL})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "members", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || len(cursors) != 2 || cursors[0] != "" || cursors[1] != "c2" {
		t.Fatalf("rows=%d cursors=%v", len(res.Rows), cursors)
	}
}

func TestNotionRecipePostBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusBadRequest)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["query"] != "" {
			http.Error(w, "body", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": 1}}})
	}))
	defer srv.Close()

	c := openPreset(t, "notion", map[string]string{"url": srv.URL})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "pages", Limit: 10})
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("rows=%+v err=%v", res, err)
	}
}
