// Package httpjson implements generic REST/JSON and CSV URL connectors. These
// make it possible to query internal APIs and hosted files without writing a
// new connector: the endpoint and a small amount of config are enough.
package httpjson

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
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
	"github.com/anyquery/anyquery/internal/httpauth"
)

func init() {
	connector.Register(restProvider{})
	connector.Register(csvProvider{})
	connector.Register(jsonlProvider{})
}

const maxResponseBytes = 64 << 20

func httpClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL must start with http:// or https://")
	}
	return nil
}

func parseHeaders(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("headers must be a JSON object of strings: %w", err)
	}
	return out, nil
}

func doRequest(ctx context.Context, rawURL, headersRaw string) ([]byte, error) {
	if err := validateURL(rawURL); err != nil {
		return nil, err
	}
	headers, err := parseHeaders(headersRaw)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/csv;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", "AnyQuery/0.1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(body)
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	return body, nil
}

// ---- REST ------------------------------------------------------------------

type restProvider struct{}

func (restProvider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "rest",
		Name:        "REST / JSON API",
		Category:    connector.CategoryAPI,
		Description: "Any JSON HTTP endpoint (one table per endpoint)",
		Icon:        "rest",
		Fields: []connector.ConfigField{
			{Key: "url", Label: "Endpoint URL", Kind: connector.FieldTextarea, Required: true, Placeholder: "https://api.example.com/v1/orders", Help: "GET should return a JSON array, or an object containing one (set Records path)."},
			{Key: "records_path", Label: "Records path", Kind: connector.FieldText, Placeholder: "data.items", Help: "Dot path to the array inside the JSON response. Leave empty for a root array."},
			{Key: "table_name", Label: "Table name", Kind: connector.FieldText, Required: true, Default: "records"},
			{Key: "headers", Label: "HTTP headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"Authorization":"Bearer ..."}`},
			{Key: "pagination", Label: "Pagination", Kind: connector.FieldSelect, Default: "none", Options: []connector.Option{{Value: "none", Label: "None"}, {Value: "page", Label: "Page number"}, {Value: "offset", Label: "Offset"}, {Value: "cursor", Label: "Cursor"}, {Value: "link", Label: "Link header"}}, Help: "Use page/offset/cursor/Link when the endpoint returns bounded pages."},
			{Key: "page_param", Label: "Page parameter", Kind: connector.FieldText, Default: "page"},
			{Key: "page_size_param", Label: "Page size parameter", Kind: connector.FieldText, Default: "page_size"},
			{Key: "page_start", Label: "First page number", Kind: connector.FieldNumber, Default: "1"},
			{Key: "page_size", Label: "Page size", Kind: connector.FieldNumber, Default: "100"},
			{Key: "offset_param", Label: "Offset parameter", Kind: connector.FieldText, Default: "offset"},
			{Key: "cursor_param", Label: "Cursor parameter", Kind: connector.FieldText, Default: "cursor"},
			{Key: "cursor_path", Label: "Next cursor path", Kind: connector.FieldText, Placeholder: "next_cursor", Help: "Dot path in the response containing the next cursor."},
			{Key: "next_path", Label: "Next URL path", Kind: connector.FieldText, Placeholder: "links.next", Help: "Optional dot path to a next-page URL; Link mode also honors the HTTP Link header."},
			{Key: "max_pages", Label: "Max pages", Kind: connector.FieldNumber, Default: "10"},
			{Key: "auto_paginate", Label: "Auto paginate", Kind: connector.FieldBool, Default: "true"},
			{Key: "auth_type", Label: "OAuth2 flow", Kind: connector.FieldSelect, Default: "none", Options: []connector.Option{{Value: "none", Label: "None"}, {Value: "client_credentials", Label: "Client credentials"}, {Value: "refresh_token", Label: "Refresh token"}}},
			{Key: "token_url", Label: "OAuth2 token URL", Kind: connector.FieldText, Placeholder: "https://auth.example.com/oauth/token"},
			{Key: "client_id", Label: "OAuth2 client ID", Kind: connector.FieldText},
			{Key: "client_secret", Label: "OAuth2 client secret", Kind: connector.FieldPassword, Secret: true},
			{Key: "refresh_token", Label: "OAuth2 refresh token", Kind: connector.FieldPassword, Secret: true},
			{Key: "scope", Label: "OAuth2 scope", Kind: connector.FieldText, Placeholder: "read:data"},
		},
	}
}

func (restProvider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	if err := validateURL(cfg["url"]); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg["table_name"]) == "" {
		cfg["table_name"] = "records"
	}
	headers, err := parseHeaders(cfg["headers"])
	if err != nil {
		return nil, err
	}
	authCfg, err := httpauth.ParseConfig(cfg)
	if err != nil {
		return nil, err
	}
	page, err := parseRestPage(cfg)
	if err != nil {
		return nil, err
	}
	return &restConn{cfg: cfg, headers: headers, auth: httpauth.NewProvider(authCfg), page: page}, nil
}

type restConn struct {
	cfg     map[string]string
	headers map[string]string
	auth    *httpauth.Provider
	page    restPage
}

type restPage struct {
	mode        string
	pageParam   string
	sizeParam   string
	offsetParam string
	cursorParam string
	cursorPath  string
	nextPath    string
	pageStart   int
	pageSize    int
	maxPages    int
	auto        bool
}

func parseRestPage(cfg map[string]string) (restPage, error) {
	p := restPage{
		mode:        strings.ToLower(strings.TrimSpace(cfg["pagination"])),
		pageParam:   strings.TrimSpace(cfg["page_param"]),
		sizeParam:   strings.TrimSpace(cfg["page_size_param"]),
		offsetParam: strings.TrimSpace(cfg["offset_param"]),
		cursorParam: strings.TrimSpace(cfg["cursor_param"]),
		cursorPath:  strings.TrimSpace(cfg["cursor_path"]),
		nextPath:    strings.TrimSpace(cfg["next_path"]),
		pageStart:   intValue(cfg["page_start"], 1),
		pageSize:    intValue(cfg["page_size"], 100),
		maxPages:    intValue(cfg["max_pages"], 10),
		auto:        cfg["auto_paginate"] != "false",
	}
	if p.mode == "" {
		p.mode = "none"
	}
	switch p.mode {
	case "none", "page", "offset", "cursor", "link":
	default:
		return p, fmt.Errorf("pagination must be none, page, offset, cursor or link")
	}
	if p.pageParam == "" {
		p.pageParam = "page"
	}
	if p.sizeParam == "" {
		p.sizeParam = "page_size"
	}
	if p.offsetParam == "" {
		p.offsetParam = "offset"
	}
	if p.cursorParam == "" {
		p.cursorParam = "cursor"
	}
	if p.pageSize <= 0 {
		p.pageSize = 100
	}
	if p.maxPages <= 0 {
		p.maxPages = 10
	}
	if p.maxPages > 100 {
		p.maxPages = 100
	}
	return p, nil
}

func (c *restConn) Ping(ctx context.Context) error {
	_, _, err := c.rows(ctx, 10)
	return err
}

func (c *restConn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}
func (c *restConn) Close() error { return nil }

func (c *restConn) Introspect(ctx context.Context) ([]connector.Table, error) {
	rows, _, err := c.rows(ctx, 100)
	if err != nil {
		return nil, err
	}
	cols := inferColumns(rows)
	return []connector.Table{{Name: c.cfg["table_name"], DisplayName: c.cfg["table_name"], Kind: "api", Columns: cols}}, nil
}

func (c *restConn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	rows, truncated, err := c.rows(ctx, req.Limit)
	if err != nil {
		return nil, err
	}
	cols := inferColumns(rows)
	return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: truncated}, nil
}

func (c *restConn) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	rawURL, err := c.page.url(c.cfg["url"], c.page.pageStart, 0, "", c.page.pageSize)
	if err != nil {
		return "", nil, err
	}
	return "GET " + rawURL, nil, nil
}

func (c *restConn) rows(ctx context.Context, limit int) ([]map[string]any, bool, error) {
	if limit <= 0 || limit > 100_000 {
		limit = 100_000
	}
	var out []map[string]any
	page, offset, cursor, nextURL := c.page.pageStart, 0, "", ""
	truncated := false
	pages := 0
	for pages < c.page.maxPages && len(out) < limit {
		size := c.page.pageSize
		requestURL := nextURL
		if requestURL == "" {
			var err error
			requestURL, err = c.page.url(c.cfg["url"], page, offset, cursor, size)
			if err != nil {
				return nil, truncated, err
			}
		}
		body, header, err := c.get(ctx, requestURL)
		if err != nil {
			return nil, truncated, err
		}
		var root any
		if err := json.Unmarshal(body, &root); err != nil {
			return nil, truncated, fmt.Errorf("response is not valid JSON: %w", err)
		}
		arr, err := extractArray(root, c.cfg["records_path"])
		if err != nil {
			return nil, truncated, err
		}
		pages++
		for _, item := range arr {
			if len(out) >= limit {
				truncated = true
				break
			}
			switch x := item.(type) {
			case map[string]any:
				out = append(out, flattenJSON(x))
			default:
				out = append(out, map[string]any{"value": x})
			}
		}
		if len(out) >= limit {
			break
		}
		if !c.page.auto || c.page.mode == "none" {
			break
		}

		next := false
		switch c.page.mode {
		case "page":
			if len(arr) == 0 {
				break
			}
			page++
			next = true
		case "offset":
			if len(arr) == 0 {
				break
			}
			offset += len(arr)
			next = true
		case "cursor":
			nextCursor, ok := pathString(root, c.page.cursorPath)
			if !ok || nextCursor == "" || nextCursor == cursor {
				break
			}
			cursor = nextCursor
			next = true
		case "link":
			nextLink := parseLinkNext(header.Get("Link"))
			if nextLink == "" {
				if v, ok := pathString(root, c.page.nextPath); ok {
					nextLink = v
				}
			}
			if nextLink == "" {
				break
			}
			resolved := resolveURL(requestURL, nextLink)
			if !sameOrigin(c.cfg["url"], resolved) {
				break
			}
			nextURL = resolved
			next = true
		}
		if !next {
			break
		}
		if pages >= c.page.maxPages {
			truncated = true
			break
		}
	}
	if len(out) >= limit {
		truncated = true
	}
	return out, truncated, nil
}

func (c *restConn) get(ctx context.Context, rawURL string) ([]byte, http.Header, error) {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Accept", "application/json, text/csv;q=0.9, */*;q=0.8")
		req.Header.Set("User-Agent", "AnyQuery/0.1")
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		if c.auth != nil {
			authHeader, err := c.auth.Authorization(ctx)
			if err != nil {
				return nil, nil, err
			}
			if authHeader != "" {
				req.Header.Set("Authorization", authHeader)
			}
		}
		resp, err := httpClient().Do(req)
		if err != nil {
			return nil, nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			return nil, nil, readErr
		}
		if resp.StatusCode == http.StatusUnauthorized && c.auth != nil && attempt == 0 {
			c.auth.Invalidate()
			continue
		}
		if (resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504) && attempt < 2 {
			wait := time.Duration(500*(attempt+1)) * time.Millisecond
			if retry := resp.Header.Get("Retry-After"); retry != "" {
				if seconds, err := strconv.Atoi(strings.TrimSpace(retry)); err == nil && seconds > 0 && seconds <= 10 {
					wait = time.Duration(seconds) * time.Second
				}
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(body))
		}
		return body, resp.Header, nil
	}
	return nil, nil, fmt.Errorf("request failed after retries")
}

func (p restPage) url(base string, page, offset int, cursor string, size int) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if p.mode == "page" {
		q.Set(p.pageParam, strconv.Itoa(page))
	}
	if p.mode == "offset" {
		q.Set(p.offsetParam, strconv.Itoa(offset))
	}
	if p.mode == "cursor" && cursor != "" {
		q.Set(p.cursorParam, cursor)
	}
	if p.mode == "page" || p.mode == "offset" || p.mode == "cursor" {
		if p.sizeParam != "" {
			q.Set(p.sizeParam, strconv.Itoa(size))
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func pathString(root any, path string) (string, bool) {
	p := strings.TrimSpace(path)
	p = strings.TrimPrefix(p, "$")
	p = strings.TrimPrefix(p, ".")
	if p == "" {
		return "", false
	}
	cur := root
	for _, part := range strings.Split(p, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = m[part]
		if !ok {
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	default:
		return "", false
	}
}

func parseLinkNext(header string) string {
	for _, part := range strings.Split(header, ",") {
		low := strings.ToLower(part)
		if !strings.Contains(low, `rel="next"`) && !strings.Contains(low, "rel=next") {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			return part[start+1 : end]
		}
	}
	return ""
}

func resolveURL(current, next string) string {
	u, err := url.Parse(current)
	if err != nil {
		return next
	}
	ref, err := url.Parse(next)
	if err != nil {
		return next
	}
	return u.ResolveReference(ref).String()
}

func sameOrigin(base, target string) bool {
	b, err1 := url.Parse(base)
	t, err2 := url.Parse(target)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(b.Scheme, t.Scheme) && strings.EqualFold(b.Host, t.Host)
}

func intValue(raw string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		return n
	}
	return def
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func extractArray(root any, path string) ([]any, error) {
	cur := root
	if strings.TrimSpace(path) != "" {
		p := strings.TrimPrefix(strings.TrimSpace(path), "$")
		p = strings.TrimPrefix(p, ".")
		for _, part := range strings.Split(p, ".") {
			if part == "" {
				continue
			}
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("records path %q does not point to an array", path)
			}
			cur, ok = m[part]
			if !ok {
				return nil, fmt.Errorf("records path %q not found in response", path)
			}
		}
	}
	if arr, ok := cur.([]any); ok {
		return arr, nil
	}
	if m, ok := cur.(map[string]any); ok {
		for _, key := range []string{"data", "items", "records", "results", "rows", "values"} {
			if arr, ok := m[key].([]any); ok {
				return arr, nil
			}
		}
	}
	return nil, fmt.Errorf("response does not contain a JSON array; set the records path")
}

func flattenJSON(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case nil, string, bool, float64:
			out[k] = x
		case json.Number:
			out[k] = x
		case map[string]any, []any:
			b, _ := json.Marshal(x)
			out[k] = string(b)
		default:
			out[k] = fmt.Sprint(x)
		}
	}
	return out
}

func inferColumns(rows []map[string]any) []connector.Column {
	type acc struct {
		col     connector.Column
		nonNull bool
	}
	m := map[string]*acc{}
	for _, row := range rows {
		for k, v := range row {
			a, ok := m[k]
			if !ok {
				a = &acc{col: connector.Column{Name: k, Nullable: true}}
				m[k] = a
			}
			if v == nil {
				continue
			}
			a.nonNull = true
			t := valueType(v)
			if a.col.Type == "" || a.col.Type == "null" {
				a.col.Type = t
			} else if a.col.Type != t {
				a.col.Type = "string"
			}
		}
	}
	var cols []connector.Column
	for _, a := range m {
		a.col.Nullable = !a.nonNull
		cols = append(cols, a.col)
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })
	return cols
}

func valueType(v any) string {
	switch x := v.(type) {
	case bool:
		return "boolean"
	case float64:
		if x == float64(int64(x)) {
			return "integer"
		}
		return "number"
	case json.Number:
		if _, err := x.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "string"
	}
}

// ---- CSV -------------------------------------------------------------------

type csvProvider struct{}

func (csvProvider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "csv",
		Name:        "CSV / URL",
		Category:    connector.CategoryFile,
		Description: "A CSV/TSV file served over HTTP",
		Icon:        "csv",
		Fields: []connector.ConfigField{
			{Key: "url", Label: "File URL", Kind: connector.FieldTextarea, Required: true, Placeholder: "https://example.com/data/orders.csv"},
			{Key: "table_name", Label: "Table name", Kind: connector.FieldText, Required: true, Default: "data"},
			{Key: "delimiter", Label: "Delimiter", Kind: connector.FieldSelect, Default: ",", Options: []connector.Option{{Value: ",", Label: "Comma"}, {Value: ";", Label: "Semicolon"}, {Value: "\t", Label: "Tab"}, {Value: "|", Label: "Pipe"}}},
			{Key: "headers", Label: "HTTP headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"Authorization":"Bearer ..."}`},
		},
	}
}

func (csvProvider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	if err := validateURL(cfg["url"]); err != nil {
		return nil, err
	}
	if cfg["table_name"] == "" {
		cfg["table_name"] = "data"
	}
	if cfg["delimiter"] == "" {
		cfg["delimiter"] = ","
	}
	return &csvConn{cfg: cfg}, nil
}

type csvConn struct{ cfg map[string]string }

func (c *csvConn) Ping(ctx context.Context) error {
	_, err := c.rows(ctx, 1)
	return err
}

func (c *csvConn) Capabilities() connector.Capabilities { return connector.Capabilities{} }
func (c *csvConn) Close() error                         { return nil }

func (c *csvConn) Introspect(ctx context.Context) ([]connector.Table, error) {
	rows, err := c.rows(ctx, 1000)
	if err != nil {
		return nil, err
	}
	return []connector.Table{{Name: c.cfg["table_name"], DisplayName: c.cfg["table_name"], Kind: "file", Columns: inferColumns(rows)}}, nil
}

func (c *csvConn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	rows, err := c.rows(ctx, req.Limit)
	if err != nil {
		return nil, err
	}
	truncated := false
	if req.Limit > 0 && len(rows) > req.Limit {
		rows = rows[:req.Limit]
		truncated = true
	}
	return &connector.FetchResult{Columns: inferColumns(rows), Rows: rows, Truncated: truncated}, nil
}

func (c *csvConn) rows(ctx context.Context, limit int) ([]map[string]any, error) {
	body, err := doRequest(ctx, c.cfg["url"], c.cfg["headers"])
	if err != nil {
		return nil, err
	}
	delim := c.cfg["delimiter"]
	if delim == "" {
		delim = ","
	}
	r := csv.NewReader(strings.NewReader(string(body)))
	r.Comma = []rune(delim)[0]
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := records[0]
	if limit <= 0 || limit > 500_000 {
		limit = 500_000
	}
	var out []map[string]any
	for _, rec := range records[1:] {
		if len(out) >= limit {
			break
		}
		row := make(map[string]any, len(header))
		for i, name := range header {
			if i < len(rec) {
				row[name] = parseCSVValue(rec[i])
			} else {
				row[name] = nil
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func parseCSVValue(s string) any {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	if i, err := strconv.ParseInt(t, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(t); err == nil {
		return b
	}
	return s
}

// ---- JSON Lines ------------------------------------------------------------

type jsonlProvider struct{}

func (jsonlProvider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "jsonl",
		Name:        "JSON Lines / URL",
		Category:    connector.CategoryFile,
		Description: "Newline-delimited JSON (NDJSON) served over HTTP",
		Icon:        "jsonl",
		Fields: []connector.ConfigField{
			{Key: "url", Label: "File URL", Kind: connector.FieldTextarea, Required: true, Placeholder: "https://example.com/events.jsonl"},
			{Key: "table_name", Label: "Table name", Kind: connector.FieldText, Required: true, Default: "events"},
			{Key: "headers", Label: "HTTP headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"Authorization":"Bearer ..."}`},
		},
	}
}

func (jsonlProvider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	if err := validateURL(cfg["url"]); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg["table_name"]) == "" {
		cfg["table_name"] = "events"
	}
	return &jsonlConn{cfg: cfg}, nil
}

type jsonlConn struct{ cfg map[string]string }

func (c *jsonlConn) Ping(ctx context.Context) error {
	_, _, err := c.rows(ctx, 1)
	return err
}

func (c *jsonlConn) Capabilities() connector.Capabilities { return connector.Capabilities{} }
func (c *jsonlConn) Close() error                         { return nil }

func (c *jsonlConn) Introspect(ctx context.Context) ([]connector.Table, error) {
	rows, _, err := c.rows(ctx, 200)
	if err != nil {
		return nil, err
	}
	return []connector.Table{{Name: c.cfg["table_name"], DisplayName: c.cfg["table_name"], Kind: "file", Columns: inferColumns(rows)}}, nil
}

func (c *jsonlConn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	rows, truncated, err := c.rows(ctx, req.Limit)
	if err != nil {
		return nil, err
	}
	return &connector.FetchResult{Columns: inferColumns(rows), Rows: rows, Truncated: truncated}, nil
}

func (c *jsonlConn) rows(ctx context.Context, limit int) ([]map[string]any, bool, error) {
	body, err := doRequest(ctx, c.cfg["url"], c.cfg["headers"])
	if err != nil {
		return nil, false, err
	}
	if limit <= 0 || limit > 500_000 {
		limit = 500_000
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	out := make([]map[string]any, 0, min(limit, 1024))
	truncated := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if len(out) >= limit {
			truncated = true
			break
		}
		var value any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			return nil, false, fmt.Errorf("invalid JSONL at line %d: %w", len(out)+1, err)
		}
		if obj, ok := value.(map[string]any); ok {
			out = append(out, flattenJSON(obj))
		} else {
			out = append(out, map[string]any{"value": value})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return out, truncated, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
