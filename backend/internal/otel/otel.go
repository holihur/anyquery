// Package otel implements read-only SQL access to common OpenTelemetry
// observability backends: Grafana Tempo, Jaeger, Zipkin, Grafana Loki and
// Prometheus. It intentionally uses their query HTTP APIs rather than OTLP
// (which is an ingest protocol).
package otel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

const (
	providerTempo      = "tempo"
	providerJaeger     = "jaeger"
	providerZipkin     = "zipkin"
	providerLoki       = "loki"
	providerPrometheus = "prometheus"
)

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "otel",
		Name:        "OpenTelemetry",
		Category:    connector.CategoryObservability,
		Description: "Traces, logs and metrics from OTel-compatible backends",
		Icon:        "opentelemetry",
		Maturity:    "native",
		DocsURL:     "https://opentelemetry.io/docs/",
		Fields: []connector.ConfigField{
			{Key: "provider", Label: "Provider", Kind: connector.FieldSelect, Default: providerTempo, Options: []connector.Option{
				{Value: providerTempo, Label: "Grafana Tempo"},
				{Value: providerJaeger, Label: "Jaeger"},
				{Value: providerZipkin, Label: "Zipkin"},
				{Value: providerLoki, Label: "Grafana Loki"},
				{Value: providerPrometheus, Label: "Prometheus"},
			}},
			{Key: "endpoint", Label: "Query endpoint", Kind: connector.FieldText, Required: true, Placeholder: "http://localhost:3200", Help: "Base URL of the query API (Tempo 3200, Jaeger 16686, Zipkin 9411, Loki 3100, Prometheus 9090)."},
			{Key: "query", Label: "Default query", Kind: connector.FieldTextarea, Placeholder: "{}", Help: "TraceQL for Tempo, LogQL for Loki, PromQL for Prometheus. Ignored by Jaeger/Zipkin."},
			{Key: "service", Label: "Service", Kind: connector.FieldText, Placeholder: "checkout", Help: "Jaeger/Zipkin service filter. Empty discovers services and queries each one."},
			{Key: "lookback_minutes", Label: "Lookback (minutes)", Kind: connector.FieldNumber, Default: "60"},
			{Key: "max_traces", Label: "Tempo traces per span query", Kind: connector.FieldNumber, Default: "20", Help: "Tempo span queries fetch trace details one by one; this caps that fan-out."},
			{Key: "step_seconds", Label: "Prometheus step (seconds)", Kind: connector.FieldNumber, Default: "60"},
			{Key: "headers", Label: "HTTP headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"X-API-Key":"..."}`},
			{Key: "token", Label: "Bearer token", Kind: connector.FieldPassword, Secret: true},
			{Key: "username", Label: "Basic username", Kind: connector.FieldText},
			{Key: "password", Label: "Basic password", Kind: connector.FieldPassword, Secret: true},
			{Key: "tenant", Label: "Tenant / org ID", Kind: connector.FieldText, Help: "Sent as X-Scope-OrgID for multi-tenant Tempo/Loki/Mimir."},
			{Key: "timeout_seconds", Label: "Timeout (seconds)", Kind: connector.FieldNumber, Default: "60"},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg["provider"]))
	if provider == "" {
		provider = providerTempo
	}
	switch provider {
	case providerTempo, providerJaeger, providerZipkin, providerLoki, providerPrometheus:
	default:
		return nil, fmt.Errorf("unsupported OTel provider %q", provider)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(cfg["endpoint"]), "/")
	if err := validateHTTPURL(endpoint); err != nil {
		return nil, err
	}
	headers := map[string]string{}
	if raw := strings.TrimSpace(cfg["headers"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &headers); err != nil {
			return nil, fmt.Errorf("headers must be a JSON object of strings: %w", err)
		}
	}
	c := &Conn{
		provider:  provider,
		base:      endpoint,
		query:     strings.TrimSpace(cfg["query"]),
		service:   strings.TrimSpace(cfg["service"]),
		headers:   headers,
		token:     strings.TrimSpace(cfg["token"]),
		username:  strings.TrimSpace(cfg["username"]),
		password:  strings.TrimSpace(cfg["password"]),
		tenant:    strings.TrimSpace(cfg["tenant"]),
		lookback:  time.Duration(atoiDefault(cfg["lookback_minutes"], 60)) * time.Minute,
		maxTraces: atoiDefault(cfg["max_traces"], 20),
		step:      time.Duration(atoiDefault(cfg["step_seconds"], 60)) * time.Second,
		client:    &http.Client{Timeout: time.Duration(atoiDefault(cfg["timeout_seconds"], 60)) * time.Second},
	}
	if c.lookback <= 0 {
		c.lookback = time.Hour
	}
	if c.maxTraces <= 0 {
		c.maxTraces = 20
	}
	if c.maxTraces > 200 {
		c.maxTraces = 200
	}
	if c.step <= 0 {
		c.step = time.Minute
	}
	if c.query == "" {
		switch provider {
		case providerTempo:
			c.query = "{}"
		case providerLoki:
			c.query = `{job=~".+"}`
		case providerPrometheus:
			c.query = "up"
		}
	}
	if err := c.Ping(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

type Conn struct {
	provider  string
	base      string
	query     string
	service   string
	headers   map[string]string
	token     string
	username  string
	password  string
	tenant    string
	lookback  time.Duration
	maxTraces int
	step      time.Duration
	client    *http.Client
}

func (c *Conn) Ping(ctx context.Context) error {
	path := map[string]string{
		providerTempo:      "/ready",
		providerJaeger:     "/api/services",
		providerZipkin:     "/api/v2/services",
		providerLoki:       "/ready",
		providerPrometheus: "/-/ready",
	}[c.provider]
	_, _, err := c.get(ctx, path, nil)
	return err
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(context.Context) ([]connector.Table, error) {
	var tables []connector.Table
	switch c.provider {
	case providerTempo:
		tables = []connector.Table{
			{Name: "traces", DisplayName: "traces", Kind: "view", Columns: traceColumns()},
			{Name: "spans", DisplayName: "spans", Kind: "view", Columns: spanColumns()},
		}
	case providerJaeger, providerZipkin:
		tables = []connector.Table{
			{Name: "spans", DisplayName: "spans", Kind: "view", Columns: spanColumns()},
			{Name: "services", DisplayName: "services", Kind: "view", Columns: []connector.Column{{Name: "service_name", Type: "string"}}},
		}
	case providerLoki:
		tables = []connector.Table{{Name: "logs", DisplayName: "logs", Kind: "view", Columns: logColumns()}}
	case providerPrometheus:
		tables = []connector.Table{{Name: "metrics", DisplayName: "metrics", Kind: "view", Columns: metricColumns()}}
	}
	return tables, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	var rows []map[string]any
	var columns []connector.Column
	var truncated bool
	var err error
	switch c.provider {
	case providerTempo:
		switch req.Table {
		case "traces":
			rows, columns, truncated, err = c.fetchTempoTraces(ctx, limit)
		case "spans":
			rows, columns, truncated, err = c.fetchTempoSpans(ctx, limit)
		default:
			err = fmt.Errorf("unknown Tempo table %q", req.Table)
		}
	case providerJaeger:
		switch req.Table {
		case "spans":
			rows, columns, truncated, err = c.fetchJaegerSpans(ctx, limit)
		case "services":
			rows, columns, truncated, err = c.fetchJaegerServices(ctx, limit)
		default:
			err = fmt.Errorf("unknown Jaeger table %q", req.Table)
		}
	case providerZipkin:
		switch req.Table {
		case "spans":
			rows, columns, truncated, err = c.fetchZipkinSpans(ctx, limit)
		case "services":
			rows, columns, truncated, err = c.fetchZipkinServices(ctx, limit)
		default:
			err = fmt.Errorf("unknown Zipkin table %q", req.Table)
		}
	case providerLoki:
		if req.Table != "logs" {
			err = fmt.Errorf("unknown Loki table %q", req.Table)
			break
		}
		rows, columns, truncated, err = c.fetchLoki(ctx, limit)
	case providerPrometheus:
		if req.Table != "metrics" {
			err = fmt.Errorf("unknown Prometheus table %q", req.Table)
			break
		}
		rows, columns, truncated, err = c.fetchPrometheus(ctx, limit)
	}
	if err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		columns = columnsFor(rows, nil)
	}
	return &connector.FetchResult{Columns: columns, Rows: rows, Truncated: truncated}, nil
}

func (c *Conn) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	switch c.provider {
	case providerTempo:
		if req.Table == "traces" {
			return fmt.Sprintf("GET /api/search?q=%s&limit=%d", c.query, req.Limit), nil, nil
		}
		return fmt.Sprintf("GET /api/search?q=%s&limit=%d -> GET /api/traces/{traceID}", c.query, c.maxTraces), nil, nil
	case providerJaeger:
		return fmt.Sprintf("GET /api/traces?service=%s&limit=%d&lookback=%s", c.service, req.Limit, c.lookback), nil, nil
	case providerZipkin:
		return fmt.Sprintf("GET /api/v2/traces?serviceName=%s&limit=%d", c.service, req.Limit), nil, nil
	case providerLoki:
		return fmt.Sprintf("GET /loki/api/v1/query_range?query=%s&limit=%d", c.query, req.Limit), nil, nil
	case providerPrometheus:
		return fmt.Sprintf("GET /api/v1/query_range?query=%s&step=%s", c.query, c.step), nil, nil
	}
	return "", nil, nil
}

// ---- Tempo -----------------------------------------------------------------

type numberString string

func (n *numberString) UnmarshalJSON(data []byte) error {
	*n = numberString(strings.Trim(string(data), `"`))
	return nil
}

func (n numberString) Int64() int64 {
	v, _ := strconv.ParseInt(string(n), 10, 64)
	return v
}

func (n numberString) Float64() float64 {
	v, _ := strconv.ParseFloat(string(n), 64)
	return v
}

type tempoSearchResponse struct {
	Traces []tempoTrace `json:"traces"`
}

type tempoTrace struct {
	TraceID           string       `json:"traceID"`
	RootServiceName   string       `json:"rootServiceName"`
	RootTraceName     string       `json:"rootTraceName"`
	StartTimeUnixNano numberString `json:"startTimeUnixNano"`
	DurationMs        numberString `json:"durationMs"`
	SpanSets          []struct {
		Spans []any `json:"spans"`
	} `json:"spanSets"`
}

func (c *Conn) fetchTempoTraces(ctx context.Context, limit int) ([]map[string]any, []connector.Column, bool, error) {
	var out tempoSearchResponse
	params := url.Values{}
	params.Set("q", c.query)
	params.Set("limit", strconv.Itoa(limit))
	if err := c.getJSON(ctx, "/api/search", params, &out); err != nil {
		return nil, nil, false, err
	}
	var rows []map[string]any
	for _, trace := range out.Traces {
		count := 0
		for _, set := range trace.SpanSets {
			count += len(set.Spans)
		}
		row := map[string]any{
			"trace_id":          trace.TraceID,
			"root_service_name": trace.RootServiceName,
			"root_trace_name":   trace.RootTraceName,
			"duration_ms":       trace.DurationMs.Float64(),
			"span_count":        int64(count),
		}
		if t := nanoTime(trace.StartTimeUnixNano.Int64()); !t.IsZero() {
			row["start_time"] = t
		}
		rows = append(rows, row)
	}
	return rows, traceColumns(), len(rows) >= limit, nil
}

func (c *Conn) fetchTempoSpans(ctx context.Context, limit int) ([]map[string]any, []connector.Column, bool, error) {
	searchLimit := c.maxTraces
	if searchLimit <= 0 {
		searchLimit = 20
	}
	var search tempoSearchResponse
	params := url.Values{}
	params.Set("q", c.query)
	params.Set("limit", strconv.Itoa(searchLimit))
	if err := c.getJSON(ctx, "/api/search", params, &search); err != nil {
		return nil, nil, false, err
	}
	var rows []map[string]any
	seen := map[string]string{}
	for _, trace := range search.Traces {
		var envelope otlpEnvelope
		if err := c.getJSON(ctx, "/api/traces/"+url.PathEscape(trace.TraceID), nil, &envelope); err != nil {
			return nil, nil, false, err
		}
		for _, batch := range envelope.Batches {
			resource := attrMap(batch.Resource.Attributes)
			service := stringOf(resource["service.name"])
			for _, scope := range batch.allSpans() {
				for _, span := range scope.Spans {
					row := tempoSpanRow(span, service, resource)
					addRowTypes(seen, row)
					rows = append(rows, row)
					if len(rows) >= limit {
						return rows, spanColumns(), true, nil
					}
				}
			}
		}
		for _, batch := range envelope.ResourceSpans {
			resource := attrMap(batch.Resource.Attributes)
			service := stringOf(resource["service.name"])
			for _, scope := range batch.allSpans() {
				for _, span := range scope.Spans {
					row := tempoSpanRow(span, service, resource)
					addRowTypes(seen, row)
					rows = append(rows, row)
					if len(rows) >= limit {
						return rows, spanColumns(), true, nil
					}
				}
			}
		}
	}
	return rows, spanColumns(), len(search.Traces) >= searchLimit, nil
}

type otlpEnvelope struct {
	Batches       []otlpBatch `json:"batches"`
	ResourceSpans []otlpBatch `json:"resourceSpans"`
}

type otlpBatch struct {
	Resource struct {
		Attributes []otlpAttribute `json:"attributes"`
	} `json:"resource"`
	ScopeSpans []struct {
		Spans []otlpSpan `json:"spans"`
	} `json:"scopeSpans"`
	InstrumentationLibrarySpans []struct {
		Spans []otlpSpan `json:"spans"`
	} `json:"instrumentationLibrarySpans"`
}

func (b otlpBatch) allSpans() []struct {
	Spans []otlpSpan
} {
	out := make([]struct {
		Spans []otlpSpan
	}, 0, len(b.ScopeSpans)+len(b.InstrumentationLibrarySpans))
	for _, scope := range b.ScopeSpans {
		out = append(out, struct {
			Spans []otlpSpan
		}{Spans: scope.Spans})
	}
	for _, scope := range b.InstrumentationLibrarySpans {
		out = append(out, struct {
			Spans []otlpSpan
		}{Spans: scope.Spans})
	}
	return out
}

type otlpAttribute struct {
	Key   string `json:"key"`
	Value struct {
		StringValue *string      `json:"stringValue"`
		IntValue    numberString `json:"intValue"`
		DoubleValue numberString `json:"doubleValue"`
		BoolValue   *bool        `json:"boolValue"`
		ArrayValue  any          `json:"arrayValue"`
		KvlistValue any          `json:"kvlistValue"`
	} `json:"value"`
}

type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId"`
	Name              string          `json:"name"`
	Kind              any             `json:"kind"`
	StartTimeUnixNano numberString    `json:"startTimeUnixNano"`
	EndTimeUnixNano   numberString    `json:"endTimeUnixNano"`
	Attributes        []otlpAttribute `json:"attributes"`
	Status            struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
	Events []any `json:"events"`
	Links  []any `json:"links"`
}

func tempoSpanRow(span otlpSpan, service string, resource map[string]any) map[string]any {
	start := nanoTime(span.StartTimeUnixNano.Int64())
	end := nanoTime(span.EndTimeUnixNano.Int64())
	duration := float64(0)
	if !start.IsZero() && !end.IsZero() {
		duration = float64(end.Sub(start)) / float64(time.Millisecond)
	}
	row := map[string]any{
		"trace_id":       span.TraceID,
		"span_id":        span.SpanID,
		"parent_span_id": span.ParentSpanID,
		"name":           span.Name,
		"service_name":   service,
		"kind":           normalizeKind(span.Kind),
		"duration_ms":    duration,
		"status_code":    normalizeStatus(span.Status.Code),
		"status_message": span.Status.Message,
		"attributes":     attrMap(span.Attributes),
		"resource":       resource,
		"events":         span.Events,
		"links":          span.Links,
	}
	if !start.IsZero() {
		row["start_time"] = start
	}
	if !end.IsZero() {
		row["end_time"] = end
	}
	return row
}

// ---- Jaeger ----------------------------------------------------------------

type jaegerResponse struct {
	Data []struct {
		TraceID string       `json:"traceID"`
		Spans   []jaegerSpan `json:"spans"`
	} `json:"data"`
}

type jaegerSpan struct {
	TraceID       string `json:"traceID"`
	SpanID        string `json:"spanID"`
	OperationName string `json:"operationName"`
	References    []struct {
		RefType string `json:"refType"`
		TraceID string `json:"traceID"`
		SpanID  string `json:"spanID"`
	} `json:"references"`
	StartTime int64 `json:"startTime"`
	Duration  int64 `json:"duration"`
	Tags      []struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	} `json:"tags"`
	Logs    []any `json:"logs"`
	Process struct {
		ServiceName string `json:"serviceName"`
		Tags        []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"tags"`
	} `json:"process"`
}

func (c *Conn) fetchJaegerSpans(ctx context.Context, limit int) ([]map[string]any, []connector.Column, bool, error) {
	services := []string{c.service}
	if c.service == "" {
		var err error
		services, err = c.jaegerServices(ctx)
		if err != nil {
			return nil, nil, false, err
		}
	}
	var rows []map[string]any
	seen := map[string]string{}
	for _, service := range services {
		params := url.Values{}
		params.Set("service", service)
		params.Set("limit", strconv.Itoa(limit-len(rows)))
		params.Set("lookback", fmt.Sprintf("%dm", int(c.lookback.Minutes())))
		var out jaegerResponse
		if err := c.getJSON(ctx, "/api/traces", params, &out); err != nil {
			return nil, nil, false, err
		}
		for _, trace := range out.Data {
			for _, span := range trace.Spans {
				row := jaegerSpanRow(span, trace.TraceID)
				addRowTypes(seen, row)
				rows = append(rows, row)
				if len(rows) >= limit {
					return rows, spanColumns(), true, nil
				}
			}
		}
	}
	return rows, spanColumns(), false, nil
}

func (c *Conn) jaegerServices(ctx context.Context) ([]string, error) {
	var out struct {
		Data []string `json:"data"`
	}
	if err := c.getJSON(ctx, "/api/services", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func jaegerSpanRow(span jaegerSpan, traceID string) map[string]any {
	if traceID == "" {
		traceID = span.TraceID
	}
	parent := ""
	for _, ref := range span.References {
		if strings.EqualFold(ref.RefType, "CHILD_OF") {
			parent = ref.SpanID
			break
		}
	}
	tags := map[string]any{}
	for _, tag := range span.Tags {
		tags[tag.Key] = tag.Value
	}
	resource := map[string]any{"service.name": span.Process.ServiceName}
	for _, tag := range span.Process.Tags {
		resource[tag.Key] = tag.Value
	}
	start := microsTime(span.StartTime)
	end := start.Add(time.Duration(span.Duration) * time.Microsecond)
	row := map[string]any{
		"trace_id":       traceID,
		"span_id":        span.SpanID,
		"parent_span_id": parent,
		"name":           span.OperationName,
		"service_name":   span.Process.ServiceName,
		"kind":           stringOf(tags["span.kind"]),
		"duration_ms":    float64(span.Duration) / 1000,
		"status_code":    jaegerStatus(tags),
		"status_message": stringOf(tags["otel.status_description"]),
		"attributes":     tags,
		"resource":       resource,
		"events":         span.Logs,
	}
	if !start.IsZero() {
		row["start_time"] = start
		row["end_time"] = end
	}
	return row
}

func (c *Conn) fetchJaegerServices(ctx context.Context, limit int) ([]map[string]any, []connector.Column, bool, error) {
	services, err := c.jaegerServices(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	rows := make([]map[string]any, 0, len(services))
	for _, service := range services {
		rows = append(rows, map[string]any{"service_name": service})
	}
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	return rows, []connector.Column{{Name: "service_name", Type: "string"}}, truncated, nil
}

// ---- Zipkin ----------------------------------------------------------------

type zipkinSpan struct {
	TraceID       string `json:"traceId"`
	ID            string `json:"id"`
	ParentID      string `json:"parentId"`
	Name          string `json:"name"`
	Timestamp     int64  `json:"timestamp"`
	Duration      int64  `json:"duration"`
	Kind          string `json:"kind"`
	LocalEndpoint struct {
		ServiceName string `json:"serviceName"`
	} `json:"localEndpoint"`
	Tags        map[string]string `json:"tags"`
	Annotations []any             `json:"annotations"`
}

func (c *Conn) fetchZipkinSpans(ctx context.Context, limit int) ([]map[string]any, []connector.Column, bool, error) {
	services := []string{c.service}
	if c.service == "" {
		var err error
		services, err = c.zipkinServices(ctx)
		if err != nil {
			return nil, nil, false, err
		}
	}
	var rows []map[string]any
	seen := map[string]string{}
	for _, service := range services {
		params := url.Values{}
		params.Set("serviceName", service)
		params.Set("limit", strconv.Itoa(limit-len(rows)))
		params.Set("endTs", strconv.FormatInt(time.Now().UnixMilli(), 10))
		params.Set("lookback", strconv.FormatInt(c.lookback.Milliseconds(), 10))
		var traces [][]zipkinSpan
		if err := c.getJSON(ctx, "/api/v2/traces", params, &traces); err != nil {
			return nil, nil, false, err
		}
		for _, spans := range traces {
			for _, span := range spans {
				row := zipkinSpanRow(span)
				addRowTypes(seen, row)
				rows = append(rows, row)
				if len(rows) >= limit {
					return rows, spanColumns(), true, nil
				}
			}
		}
	}
	return rows, spanColumns(), false, nil
}

func (c *Conn) zipkinServices(ctx context.Context) ([]string, error) {
	var out []string
	if err := c.getJSON(ctx, "/api/v2/services", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func zipkinSpanRow(span zipkinSpan) map[string]any {
	start := microsTime(span.Timestamp)
	row := map[string]any{
		"trace_id":       span.TraceID,
		"span_id":        span.ID,
		"parent_span_id": span.ParentID,
		"name":           span.Name,
		"service_name":   span.LocalEndpoint.ServiceName,
		"kind":           strings.ToLower(span.Kind),
		"duration_ms":    float64(span.Duration) / 1000,
		"status_code":    zipkinStatus(span.Tags),
		"status_message": span.Tags["otel.status_description"],
		"attributes":     span.Tags,
		"resource":       map[string]any{"service.name": span.LocalEndpoint.ServiceName},
		"events":         span.Annotations,
	}
	if !start.IsZero() {
		row["start_time"] = start
		row["end_time"] = start.Add(time.Duration(span.Duration) * time.Microsecond)
	}
	return row
}

func (c *Conn) fetchZipkinServices(ctx context.Context, limit int) ([]map[string]any, []connector.Column, bool, error) {
	services, err := c.zipkinServices(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	rows := make([]map[string]any, 0, len(services))
	for _, service := range services {
		rows = append(rows, map[string]any{"service_name": service})
	}
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	return rows, []connector.Column{{Name: "service_name", Type: "string"}}, truncated, nil
}

// ---- Loki ------------------------------------------------------------------

func (c *Conn) fetchLoki(ctx context.Context, limit int) ([]map[string]any, []connector.Column, bool, error) {
	now := time.Now()
	params := url.Values{}
	params.Set("query", c.query)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("start", strconv.FormatInt(now.Add(-c.lookback).UnixNano(), 10))
	params.Set("end", strconv.FormatInt(now.UnixNano(), 10))
	params.Set("direction", "backward")
	var out struct {
		Data struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/loki/api/v1/query_range", params, &out); err != nil {
		return nil, nil, false, err
	}
	var rows []map[string]any
	seen := map[string]string{}
	for _, stream := range out.Data.Result {
		for _, value := range stream.Values {
			if len(value) < 2 {
				continue
			}
			ts := nanoTimeString(value[0])
			line := value[1]
			labels := stringMapAny(stream.Stream)
			row := map[string]any{"timestamp": ts, "labels": labels, "line": line, "level": lokiLevel(stream.Stream, line)}
			var fields map[string]any
			if json.Unmarshal([]byte(line), &fields) == nil && fields != nil {
				row["fields"] = fields
				if row["level"] == "" {
					row["level"] = stringOf(fields["level"])
				}
			}
			addRowTypes(seen, row)
			rows = append(rows, row)
			if len(rows) >= limit {
				return rows, logColumns(), true, nil
			}
		}
	}
	return rows, logColumns(), false, nil
}

func lokiLevel(labels map[string]string, line string) string {
	for _, key := range []string{"level", "severity", "severity_text"} {
		if labels[key] != "" {
			return labels[key]
		}
	}
	var fields map[string]any
	if json.Unmarshal([]byte(line), &fields) == nil {
		for _, key := range []string{"level", "severity", "severity_text"} {
			if s := stringOf(fields[key]); s != "" {
				return s
			}
		}
	}
	return ""
}

// ---- Prometheus ------------------------------------------------------------

func (c *Conn) fetchPrometheus(ctx context.Context, limit int) ([]map[string]any, []connector.Column, bool, error) {
	now := time.Now()
	params := url.Values{}
	params.Set("query", c.query)
	params.Set("start", strconv.FormatInt(now.Add(-c.lookback).Unix(), 10))
	params.Set("end", strconv.FormatInt(now.Unix(), 10))
	params.Set("step", strconv.Itoa(int(c.step.Seconds())))
	var out struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Values [][]string        `json:"values"`
				Value  []string          `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/api/v1/query_range", params, &out); err != nil {
		return nil, nil, false, err
	}
	var rows []map[string]any
	for _, series := range out.Data.Result {
		values := series.Values
		if len(values) == 0 && len(series.Value) >= 2 {
			values = [][]string{{series.Value[0], series.Value[1]}}
		}
		for _, value := range values {
			if len(value) < 2 {
				continue
			}
			ts := secondsTime(value[0])
			number, _ := strconv.ParseFloat(value[1], 64)
			rows = append(rows, map[string]any{
				"timestamp": ts,
				"metric":    series.Metric["__name__"],
				"labels":    stringMapAny(series.Metric),
				"value":     number,
			})
			if len(rows) >= limit {
				return rows, metricColumns(), true, nil
			}
		}
	}
	return rows, metricColumns(), false, nil
}

// ---- shared ----------------------------------------------------------------

func (c *Conn) getJSON(ctx context.Context, path string, params url.Values, out any) error {
	body, _, err := c.get(ctx, path, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s response: %w", c.provider, err)
	}
	return nil
}

func (c *Conn) get(ctx context.Context, path string, params url.Values) ([]byte, int, error) {
	u := c.base
	if path != "" {
		u += path
	}
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AnyQuery/otel")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	if c.tenant != "" {
		req.Header.Set("X-Scope-OrgID", c.tenant)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return nil, resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	return body, resp.StatusCode, nil
}

func traceColumns() []connector.Column {
	return []connector.Column{
		{Name: "trace_id", Type: "string"},
		{Name: "root_service_name", Type: "string", Nullable: true},
		{Name: "root_trace_name", Type: "string", Nullable: true},
		{Name: "start_time", Type: "timestamp", Nullable: true},
		{Name: "duration_ms", Type: "number", Nullable: true},
		{Name: "span_count", Type: "integer", Nullable: true},
	}
}

func spanColumns() []connector.Column {
	return []connector.Column{
		{Name: "trace_id", Type: "string"},
		{Name: "span_id", Type: "string"},
		{Name: "parent_span_id", Type: "string", Nullable: true},
		{Name: "name", Type: "string", Nullable: true},
		{Name: "service_name", Type: "string", Nullable: true},
		{Name: "kind", Type: "string", Nullable: true},
		{Name: "start_time", Type: "timestamp", Nullable: true},
		{Name: "end_time", Type: "timestamp", Nullable: true},
		{Name: "duration_ms", Type: "number", Nullable: true},
		{Name: "status_code", Type: "string", Nullable: true},
		{Name: "status_message", Type: "string", Nullable: true},
		{Name: "attributes", Type: "object", Nullable: true},
		{Name: "resource", Type: "object", Nullable: true},
		{Name: "events", Type: "array", Nullable: true},
		{Name: "links", Type: "array", Nullable: true},
	}
}

func logColumns() []connector.Column {
	return []connector.Column{
		{Name: "timestamp", Type: "timestamp"},
		{Name: "labels", Type: "object", Nullable: true},
		{Name: "line", Type: "string", Nullable: true},
		{Name: "level", Type: "string", Nullable: true},
		{Name: "fields", Type: "object", Nullable: true},
	}
}

func metricColumns() []connector.Column {
	return []connector.Column{
		{Name: "timestamp", Type: "timestamp"},
		{Name: "metric", Type: "string", Nullable: true},
		{Name: "labels", Type: "object", Nullable: true},
		{Name: "value", Type: "number", Nullable: true},
	}
}

func stringMapAny(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func columnsFor(rows []map[string]any, base []connector.Column) []connector.Column {
	out := append([]connector.Column(nil), base...)
	seen := map[string]bool{}
	for _, col := range out {
		seen[col.Name] = true
	}
	var keys []string
	for _, row := range rows {
		for key := range row {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, connector.Column{Name: key, Type: "string", Nullable: true})
	}
	return out
}

func addRowTypes(seen map[string]string, row map[string]any) {
	for key, value := range row {
		typ := "string"
		switch value.(type) {
		case nil:
			typ = "null"
		case bool:
			typ = "boolean"
		case int, int64, float64:
			typ = "number"
		case map[string]any:
			typ = "object"
		case []any:
			typ = "array"
		case time.Time:
			typ = "timestamp"
		}
		if old, ok := seen[key]; !ok || old == "null" {
			seen[key] = typ
		}
	}
}

func attrMap(attrs []otlpAttribute) map[string]any {
	out := map[string]any{}
	for _, attr := range attrs {
		switch {
		case attr.Value.StringValue != nil:
			out[attr.Key] = *attr.Value.StringValue
		case attr.Value.BoolValue != nil:
			out[attr.Key] = *attr.Value.BoolValue
		case attr.Value.IntValue != "":
			out[attr.Key] = attr.Value.IntValue.Int64()
		case attr.Value.DoubleValue != "":
			out[attr.Key] = attr.Value.DoubleValue.Float64()
		case attr.Value.ArrayValue != nil:
			out[attr.Key] = attr.Value.ArrayValue
		case attr.Value.KvlistValue != nil:
			out[attr.Key] = attr.Value.KvlistValue
		}
	}
	return out
}

func normalizeKind(v any) string {
	switch x := v.(type) {
	case float64:
		switch int(x) {
		case 1:
			return "internal"
		case 2:
			return "server"
		case 3:
			return "client"
		case 4:
			return "producer"
		case 5:
			return "consumer"
		}
	}
	s := strings.ToLower(strings.TrimPrefix(fmt.Sprint(v), "SPAN_KIND_"))
	if s == "<nil>" {
		return ""
	}
	return s
}

func normalizeStatus(v any) string {
	switch x := v.(type) {
	case string:
		s := strings.ToLower(strings.TrimPrefix(x, "STATUS_CODE_"))
		return s
	case float64:
		switch int(x) {
		case 1:
			return "ok"
		case 2:
			return "error"
		}
	case json.Number:
		switch x.String() {
		case "1":
			return "ok"
		case "2":
			return "error"
		}
	}
	return ""
}

func jaegerStatus(tags map[string]any) string {
	if s := stringOf(tags["otel.status_code"]); s != "" {
		return strings.ToLower(s)
	}
	if v, ok := tags["error"]; ok && fmt.Sprint(v) == "true" {
		return "error"
	}
	return ""
}

func zipkinStatus(tags map[string]string) string {
	if s := tags["otel.status_code"]; s != "" {
		return strings.ToLower(s)
	}
	if tags["error"] == "true" {
		return "error"
	}
	return ""
}

func stringOf(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func nanoTime(n int64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

func microsTime(n int64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	return time.Unix(0, n*int64(time.Microsecond)).UTC()
}

func nanoTimeString(s string) time.Time {
	n, _ := strconv.ParseInt(s, 10, 64)
	return nanoTime(n)
}

func secondsTime(s string) time.Time {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return time.Time{}
	}
	sec := int64(f)
	nsec := int64((f - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC()
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("endpoint must start with http:// or https://")
	}
	return nil
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
