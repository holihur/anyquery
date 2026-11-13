package engine

import (
	"context"
	"testing"
	"time"
)

func TestTemporalTableSelection(t *testing.T) {
	cat := testCatalog()
	e := New(DefaultOptions())
	query := "SELECT id FROM mongo.orders FOR SYSTEM_TIME AS OF TIMESTAMP '2024-01-01T00:00:00Z'"
	if _, err := e.Execute(context.Background(), query, cat); err != nil {
		t.Fatal(err)
	}
	req := cat.conns["mongo"].lastReq
	if req == nil || req.Temporal == nil || req.Temporal.Mode != "as_of" {
		t.Fatalf("request = %+v", req)
	}
	want, _ := time.Parse(time.RFC3339, "2024-01-01T00:00:00Z")
	if !req.Temporal.AsOf.Equal(want) {
		t.Fatalf("asOf = %s", req.Temporal.AsOf)
	}
}

func TestTemporalTableRangeAndAll(t *testing.T) {
	cases := []struct {
		query string
		mode  string
	}{
		{"SELECT id FROM mongo.orders FOR SYSTEM_TIME FROM TIMESTAMP '2024-01-01T00:00:00Z' TO TIMESTAMP '2024-02-01T00:00:00Z'", "from_to"},
		{"SELECT id FROM mongo.orders FOR SYSTEM_TIME BETWEEN TIMESTAMP '2024-01-01T00:00:00Z' AND TIMESTAMP '2024-02-01T00:00:00Z'", "between"},
		{"SELECT id FROM mongo.orders FOR SYSTEM_TIME ALL", "all"},
	}
	for _, tc := range cases {
		cat := testCatalog()
		if _, err := New(DefaultOptions()).Execute(context.Background(), tc.query, cat); err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		req := cat.conns["mongo"].lastReq
		if req == nil || req.Temporal == nil || req.Temporal.Mode != tc.mode {
			t.Fatalf("%s: request = %+v", tc.query, req)
		}
	}
}

func TestTemporalUnsupportedSourceFailsExplicitly(t *testing.T) {
	_, err := New(DefaultOptions()).Execute(context.Background(),
		"SELECT * FROM shop.`public.items` FOR SYSTEM_TIME ALL", testCatalog())
	if err == nil {
		t.Fatal("expected temporal capability error")
	}
	qe, ok := err.(*Error)
	if !ok || qe.Code != "temporal_not_supported" {
		t.Fatalf("error = %#v", err)
	}
}

func TestTemporalCTEIsRejected(t *testing.T) {
	_, err := New(DefaultOptions()).Execute(context.Background(),
		"WITH x AS (SELECT * FROM mongo.orders) SELECT * FROM x FOR SYSTEM_TIME ALL", testCatalog())
	if err == nil {
		t.Fatal("expected CTE temporal error")
	}
}
