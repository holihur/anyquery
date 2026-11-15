package otel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

func openOTel(t *testing.T, cfg map[string]string) connector.Connector {
	t.Helper()
	c, err := connector.Open(context.Background(), "otel", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestTempoTracesAndSpans(t *testing.T) {
	var searchQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ready":
			_, _ = w.Write([]byte("ready"))
		case r.URL.Path == "/api/search":
			searchQuery = r.URL.Query()
			_ = json.NewEncoder(w).Encode(map[string]any{"traces": []any{map[string]any{
				"traceID": "t1", "rootServiceName": "checkout", "rootTraceName": "POST /pay",
				"startTimeUnixNano": "1700000000000000000", "durationMs": 12.5,
				"spanSets": []any{map[string]any{"spans": []any{map[string]any{}, map[string]any{}}}},
			}}})
		case r.URL.Path == "/api/traces/t1":
			_ = json.NewEncoder(w).Encode(map[string]any{"batches": []any{map[string]any{
				"resource": map[string]any{"attributes": []any{
					map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "checkout"}},
					map[string]any{"key": "host.name", "value": map[string]any{"stringValue": "h1"}},
				}},
				"scopeSpans": []any{map[string]any{"spans": []any{map[string]any{
					"traceId": "t1", "spanId": "s1", "parentSpanId": "", "name": "POST /pay",
					"kind": "SPAN_KIND_SERVER", "startTimeUnixNano": "1700000000000000000", "endTimeUnixNano": "1700000000050000000",
					"attributes": []any{map[string]any{"key": "http.status_code", "value": map[string]any{"intValue": "200"}}},
					"status":     map[string]any{"code": "STATUS_CODE_OK", "message": "ok"},
					"events":     []any{map[string]any{"name": "exception"}},
					"links":      []any{},
				}}}},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := openOTel(t, map[string]string{"provider": "tempo", "endpoint": srv.URL, "max_traces": "2", "token": "tok"})

	traces, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "traces", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(traces.Rows) != 1 || !traces.Truncated {
		t.Fatalf("traces=%+v truncated=%v", traces.Rows, traces.Truncated)
	}
	tr := traces.Rows[0]
	if tr["trace_id"] != "t1" || tr["root_service_name"] != "checkout" || tr["span_count"] != int64(2) || tr["duration_ms"] != 12.5 {
		t.Fatalf("trace = %+v", tr)
	}
	if _, ok := tr["start_time"].(time.Time); !ok {
		t.Fatalf("start_time = %#v", tr["start_time"])
	}
	if searchQuery.Get("q") != "{}" || searchQuery.Get("limit") != "1" {
		t.Fatalf("search query = %v", searchQuery)
	}

	spans, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "spans", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans.Rows) != 1 || !spans.Truncated {
		t.Fatalf("spans=%+v truncated=%v", spans.Rows, spans.Truncated)
	}
	row := spans.Rows[0]
	if row["trace_id"] != "t1" || row["span_id"] != "s1" || row["service_name"] != "checkout" || row["kind"] != "server" {
		t.Fatalf("span = %+v", row)
	}
	if row["duration_ms"] != float64(50) || row["status_code"] != "ok" {
		t.Fatalf("span timing/status = %+v", row)
	}
	attrs := row["attributes"].(map[string]any)
	if attrs["http.status_code"] != int64(200) {
		t.Fatalf("attrs = %+v", attrs)
	}
	res := row["resource"].(map[string]any)
	if res["service.name"] != "checkout" || res["host.name"] != "h1" {
		t.Fatalf("resource = %+v", res)
	}
	tables, err := c.Introspect(context.Background())
	if err != nil || len(tables) != 2 || tables[0].Name != "traces" || tables[1].Name != "spans" {
		t.Fatalf("tables=%+v err=%v", tables, err)
	}
}

func TestJaegerSpansAndServices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/services":
			_, _ = w.Write([]byte(`{"data":["checkout"]}`))
		case "/api/traces":
			if r.URL.Query().Get("service") != "checkout" || r.URL.Query().Get("lookback") == "" {
				t.Errorf("query = %v", r.URL.Query())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{
				"traceID": "t1",
				"spans": []any{map[string]any{
					"traceID": "t1", "spanID": "s1", "operationName": "GET /pay", "startTime": 1700000000000000, "duration": 5000,
					"references": []any{map[string]any{"refType": "CHILD_OF", "spanID": "s0"}},
					"tags":       []any{map[string]any{"key": "span.kind", "value": "server"}, map[string]any{"key": "otel.status_code", "value": "ERROR"}, map[string]any{"key": "otel.status_description", "value": "boom"}},
					"logs":       []any{map[string]any{"fields": []any{}}},
					"process":    map[string]any{"serviceName": "checkout", "tags": []any{map[string]any{"key": "host", "value": "h1"}}},
				}},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := openOTel(t, map[string]string{"provider": "jaeger", "endpoint": srv.URL})
	spans, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "spans", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans.Rows) != 1 {
		t.Fatalf("spans = %+v", spans.Rows)
	}
	row := spans.Rows[0]
	if row["parent_span_id"] != "s0" || row["service_name"] != "checkout" || row["kind"] != "server" || row["status_code"] != "error" {
		t.Fatalf("row = %+v", row)
	}
	if row["duration_ms"] != float64(5) || row["status_message"] != "boom" {
		t.Fatalf("timing = %+v", row)
	}
	res := row["resource"].(map[string]any)
	if res["host"] != "h1" {
		t.Fatalf("resource = %+v", res)
	}
	services, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "services", Limit: 10})
	if err != nil || len(services.Rows) != 1 || services.Rows[0]["service_name"] != "checkout" {
		t.Fatalf("services=%+v err=%v", services, err)
	}
}

func TestZipkinSpansAndServices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/services":
			_, _ = w.Write([]byte(`["checkout"]`))
		case "/api/v2/traces":
			_ = json.NewEncoder(w).Encode([][]any{{map[string]any{
				"traceId": "t1", "id": "s1", "parentId": "s0", "name": "GET", "timestamp": 1700000000000000, "duration": 2000,
				"kind": "SERVER", "localEndpoint": map[string]any{"serviceName": "checkout"},
				"tags": map[string]any{"otel.status_code": "ERROR", "error": "true"}, "annotations": []any{map[string]any{"value": "x"}},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := openOTel(t, map[string]string{"provider": "zipkin", "endpoint": srv.URL})
	spans, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "spans", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans.Rows) != 1 || spans.Rows[0]["status_code"] != "error" || spans.Rows[0]["duration_ms"] != float64(2) {
		t.Fatalf("spans = %+v", spans.Rows)
	}
	services, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "services", Limit: 10})
	if err != nil || len(services.Rows) != 1 {
		t.Fatalf("services=%+v err=%v", services, err)
	}
}

func TestLokiLogs(t *testing.T) {
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			_, _ = w.Write([]byte("ready"))
			return
		}
		query = r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"result": []any{map[string]any{
			"stream": map[string]any{"job": "api"},
			"values": []any{[]any{"1700000000000000000", `{"level":"warn","msg":"slow"}`}},
		}}}})
	}))
	defer srv.Close()
	c := openOTel(t, map[string]string{"provider": "loki", "endpoint": srv.URL, "query": `{job="api"}`})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "logs", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	row := res.Rows[0]
	if row["level"] != "warn" || row["labels"].(map[string]any)["job"] != "api" || row["fields"].(map[string]any)["msg"] != "slow" {
		t.Fatalf("row = %+v", row)
	}
	if _, ok := row["timestamp"].(time.Time); !ok {
		t.Fatalf("timestamp = %#v", row["timestamp"])
	}
	if query.Get("query") != `{job="api"}` || query.Get("limit") != "5" || query.Get("start") == "" || query.Get("end") == "" {
		t.Fatalf("query = %v", query)
	}
}

func TestPrometheusMetrics(t *testing.T) {
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/ready" {
			_, _ = w.Write([]byte("ready"))
			return
		}
		query = r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"result": []any{map[string]any{
			"metric": map[string]any{"__name__": "up", "job": "api"},
			"values": []any{[]any{"1700000000.5", "1"}},
		}}}})
	}))
	defer srv.Close()
	c := openOTel(t, map[string]string{"provider": "prometheus", "endpoint": srv.URL, "query": "up", "step_seconds": "30"})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "metrics", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	row := res.Rows[0]
	if row["metric"] != "up" || row["value"] != float64(1) || row["labels"].(map[string]any)["job"] != "api" {
		t.Fatalf("row = %+v", row)
	}
	if query.Get("query") != "up" || query.Get("step") != "30" {
		t.Fatalf("query = %v", query)
	}
}

func TestOTelValidationAndAuth(t *testing.T) {
	if _, err := connector.Open(context.Background(), "otel", map[string]string{"provider": "nope", "endpoint": "http://x"}); err == nil {
		t.Fatal("expected provider error")
	}
	if _, err := connector.Open(context.Background(), "otel", map[string]string{"provider": "tempo"}); err == nil {
		t.Fatal("expected endpoint error")
	}
	var header, tenant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("Authorization")
		tenant = r.Header.Get("X-Scope-OrgID")
		_, _ = w.Write([]byte("ready"))
	}))
	defer srv.Close()
	openOTel(t, map[string]string{"provider": "tempo", "endpoint": srv.URL, "token": "token", "tenant": "acme"})
	if header != "Bearer token" || tenant != "acme" {
		t.Fatalf("headers = %q / %q", header, tenant)
	}
	if _, err := openOTelConfig(t, map[string]string{"provider": "tempo", "endpoint": srv.URL, "headers": "[bad"}); err == nil {
		t.Fatal("expected headers error")
	}
}

func openOTelConfig(t *testing.T, cfg map[string]string) (connector.Connector, error) {
	t.Helper()
	return connector.Open(context.Background(), "otel", cfg)
}
