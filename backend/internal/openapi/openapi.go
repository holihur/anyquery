// Package openapi exposes GET operations from an OpenAPI 3 / Swagger 2
// document as SQL tables. The spec can be a URL or a local JSON/YAML file.
package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "openapi",
		Name:        "OpenAPI / Swagger",
		Category:    connector.CategoryAPI,
		Description: "GET operations from an OpenAPI 3 or Swagger 2 document",
		Icon:        "openapi",
		Maturity:    "native",
		Fields: []connector.ConfigField{
			{Key: "spec", Label: "Spec URL or file", Kind: connector.FieldTextarea, Required: true, Placeholder: "https://api.example.com/openapi.yaml", Help: "OpenAPI 3 / Swagger 2 JSON or YAML. Local paths are also supported."},
			{Key: "spec_headers", Label: "Spec request headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"Authorization":"Bearer ..."}`, Help: "Optional headers for fetching a protected spec URL."},
			{Key: "base_url", Label: "Base URL override", Kind: connector.FieldText, Placeholder: "https://api.example.com/v1", Help: "Optional. Defaults to servers[0].url or host/basePath."},
			{Key: "headers", Label: "API headers (JSON)", Kind: connector.FieldTextarea, Secret: true, Placeholder: `{"Authorization":"Bearer ..."}`},
			{Key: "allow_methods", Label: "Allowed methods", Kind: connector.FieldText, Default: "GET", Help: "Comma-separated methods, e.g. GET,POST. Non-GET calls are opt-in because they may mutate data."},
			{Key: "body_template", Label: "Request body template (JSON)", Kind: connector.FieldTextarea, Placeholder: `{"query":"{param}"}`, Help: "Optional JSON body for POST/PUT/PATCH operations. Path/query/header values replace {name} placeholders."},
			{Key: "path_params", Label: "Path params (JSON)", Kind: connector.FieldTextarea, Placeholder: `{"owner":"acme","repo":"data"}`},
			{Key: "query_params", Label: "Query params (JSON)", Kind: connector.FieldTextarea, Placeholder: `{"limit":100}`},
			{Key: "include_tags", Label: "Include tags (comma separated)", Kind: connector.FieldText, Help: "Only expose operations with at least one of these tags."},
			{Key: "exclude_tags", Label: "Exclude tags (comma separated)", Kind: connector.FieldText, Help: "Hide operations with any of these tags."},
			{Key: "max_operations", Label: "Max operations", Kind: connector.FieldNumber, Default: "500"},
			{Key: "auto_paginate", Label: "Auto-paginate GET responses", Kind: connector.FieldBool, Default: "true"},
			{Key: "page_size", Label: "Page size", Kind: connector.FieldNumber, Default: "100"},
			{Key: "max_pages", Label: "Max pages per fetch", Kind: connector.FieldNumber, Default: "10"},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	specHeaders := cfg["spec_headers"]
	if strings.TrimSpace(specHeaders) == "" {
		specHeaders = cfg["headers"]
	}
	raw, err := readSpec(ctx, cfg["spec"], specHeaders)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse OpenAPI document: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("OpenAPI document is empty")
	}
	headers, err := parseObject(cfg["headers"])
	if err != nil {
		return nil, fmt.Errorf("headers: %w", err)
	}
	pathParams, err := parseObject(cfg["path_params"])
	if err != nil {
		return nil, fmt.Errorf("path_params: %w", err)
	}
	queryParams, err := parseObject(cfg["query_params"])
	if err != nil {
		return nil, fmt.Errorf("query_params: %w", err)
	}
	c := &Conn{
		baseURL:      resolveBase(cfg["base_url"], cfg["spec"], doc),
		headers:      headers,
		pathParams:   pathParams,
		queryParams:  queryParams,
		spec:         doc,
		client:       &http.Client{Timeout: 60 * time.Second},
		includeTags:  csvSet(cfg["include_tags"]),
		excludeTags:  csvSet(cfg["exclude_tags"]),
		allowMethods: csvSetDefault(cfg["allow_methods"], "get"),
		bodyTemplate: cfg["body_template"],
		maxOps:       intValue(cfg["max_operations"], 500),
		autoPaginate: boolValue(cfg["auto_paginate"], true),
		pageSize:     intValue(cfg["page_size"], 100),
		maxPages:     intValue(cfg["max_pages"], 10),
	}
	if c.maxOps <= 0 {
		c.maxOps = 500
	}
	if c.pageSize <= 0 {
		c.pageSize = 100
	}
	if c.maxPages <= 0 {
		c.maxPages = 10
	}
	c.introspect()
	return c, nil
}

type Conn struct {
	baseURL      string
	headers      map[string]any
	pathParams   map[string]any
	queryParams  map[string]any
	spec         map[string]any
	tables       []connector.Table
	ops          map[string]operation
	client       *http.Client
	includeTags  map[string]bool
	excludeTags  map[string]bool
	maxOps       int
	autoPaginate bool
	allowMethods map[string]bool
	bodyTemplate string
	pageSize     int
	maxPages     int
}

type operation struct {
	method        string
	path          string
	recordsPath   string
	columns       []connector.Column
	pageSizeParam string
	pageParam     string
	offsetParam   string
	cursorParam   string
}

func (c *Conn) Ping(context.Context) error { return nil }
func (c *Conn) Close() error               { return nil }
func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{}
}

func (c *Conn) Introspect(context.Context) ([]connector.Table, error) {
	return c.tables, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	op, ok := c.ops[req.Table]
	if !ok {
		return nil, fmt.Errorf("unknown OpenAPI operation %q", req.Table)
	}
	limit := req.Limit
	if limit <= 0 || limit > 100_000 {
		limit = 10_000
	}
	var rows []map[string]any
	columns := op.columns
	page, offset := 1, 0
	if n, ok := intFromAny(c.queryParams[op.pageParam]); ok && op.pageParam != "" {
		page = n
	}
	if n, ok := intFromAny(c.queryParams[op.offsetParam]); ok && op.offsetParam != "" {
		offset = n
	}
	cursor, nextURL := "", ""
	if op.cursorParam != "" {
		cursor = stringValue(c.queryParams[op.cursorParam])
	}
	pages := 0
	truncated, more := false, false
	for pages < c.maxPages && len(rows) < limit {
		requestURL, err := c.operationURL(&op, page, offset, cursor, limit)
		if err != nil {
			return nil, err
		}
		if nextURL != "" {
			requestURL = nextURL
		}
		root, headers, err := c.fetchJSON(ctx, requestURL, &op)
		if err != nil {
			return nil, err
		}
		pages++
		records := extractRecords(root, op.recordsPath)
		if len(records) == 0 {
			more = false
			break
		}
		for _, item := range records {
			if len(rows) >= limit {
				truncated = true
				break
			}
			switch x := item.(type) {
			case map[string]any:
				rows = append(rows, x)
			case nil:
				continue
			default:
				key := "value"
				if len(columns) > 0 {
					key = columns[0].Name
				}
				rows = append(rows, map[string]any{key: x})
			}
		}
		if len(rows) >= limit {
			break
		}
		if !c.autoPaginate {
			if _, ok := extractNext(root); ok {
				truncated = true
			}
			break
		}
		nextURL, cursor, page, offset, more = c.nextPage(root, &op, page, offset, cursor, requestURL, len(records), headers.Get("Link"))
		if !more {
			break
		}
	}
	if pages >= c.maxPages && more && len(rows) < limit {
		truncated = true
	}
	if len(rows) >= limit {
		truncated = true
	}
	if len(columns) == 0 {
		columns = inferColumns(rows)
	}
	return &connector.FetchResult{Columns: columns, Rows: rows, Truncated: truncated}, nil
}

// DescribeFetch renders the HTTP request that Fetch would make for the first
// page. It does not perform network I/O.
func (c *Conn) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	op, ok := c.ops[req.Table]
	if !ok {
		return "", nil, fmt.Errorf("unknown OpenAPI operation %q", req.Table)
	}
	page, offset := 1, 0
	if n, ok := intFromAny(c.queryParams[op.pageParam]); ok && op.pageParam != "" {
		page = n
	}
	if n, ok := intFromAny(c.queryParams[op.offsetParam]); ok && op.offsetParam != "" {
		offset = n
	}
	cursor := ""
	if op.cursorParam != "" {
		cursor = stringValue(c.queryParams[op.cursorParam])
	}
	requestURL, err := c.operationURL(&op, page, offset, cursor, req.Limit)
	if err != nil {
		return "", nil, err
	}
	method := strings.ToUpper(op.method)
	if method == "" {
		method = http.MethodGet
	}
	statement := method + " " + requestURL
	if method != http.MethodGet && method != http.MethodHead && strings.TrimSpace(c.bodyTemplate) != "" {
		statement += "\n\n" + c.bodyTemplate
	}
	return statement, nil, nil
}

func (c *Conn) fetchJSON(ctx context.Context, requestURL string, op *operation) (any, http.Header, error) {
	method := strings.ToUpper(op.method)
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if method != http.MethodGet && method != http.MethodHead && strings.TrimSpace(c.bodyTemplate) != "" {
		payload, err := c.renderBody()
		if err != nil {
			return nil, nil, err
		}
		body = bytes.NewReader(payload)
	}
	for attempt := 0; attempt < 3; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, method, requestURL, body)
		if err != nil {
			return nil, nil, err
		}
		httpReq.Header.Set("Accept", "application/json, application/*+json, */*")
		if body != nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}
		for k, v := range c.headers {
			httpReq.Header.Set(k, fmt.Sprint(v))
		}
		resp, err := c.client.Do(httpReq)
		if err != nil {
			return nil, nil, err
		}
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, nil, readErr
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
			if body != nil {
				if _, err := body.(*bytes.Reader).Seek(0, io.SeekStart); err != nil {
					return nil, nil, err
				}
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, nil, fmt.Errorf("OpenAPI HTTP %d: %s", resp.StatusCode, snippet(bodyBytes))
		}
		var root any
		if err := json.Unmarshal(bodyBytes, &root); err != nil {
			return nil, nil, fmt.Errorf("response is not valid JSON: %w", err)
		}
		return root, resp.Header, nil
	}
	return nil, nil, fmt.Errorf("OpenAPI request failed after retries")
}

func (c *Conn) renderBody() ([]byte, error) {
	var root any
	if err := json.Unmarshal([]byte(c.bodyTemplate), &root); err != nil {
		return nil, fmt.Errorf("body_template must be valid JSON: %w", err)
	}
	root = replaceTemplate(root, c.templateValues())
	return json.Marshal(root)
}

func (c *Conn) templateValues() map[string]any {
	out := map[string]any{}
	for k, v := range c.pathParams {
		out[k] = v
	}
	for k, v := range c.queryParams {
		out[k] = v
	}
	for k, v := range c.headers {
		out[k] = v
	}
	return out
}

func replaceTemplate(v any, values map[string]any) any {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "{") && strings.HasSuffix(x, "}") && strings.Count(x, "{") == 1 {
			if val, ok := values[strings.TrimSuffix(strings.TrimPrefix(x, "{"), "}")]; ok {
				return val
			}
		}
		for key, val := range values {
			x = strings.ReplaceAll(x, "{"+key+"}", fmt.Sprint(val))
		}
		return x
	case map[string]any:
		for k, val := range x {
			x[k] = replaceTemplate(val, values)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = replaceTemplate(val, values)
		}
		return x
	default:
		return v
	}
}

func (c *Conn) operationURL(op *operation, page, offset int, cursor string, limit int) (string, error) {
	path := op.path
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			if _, ok := c.pathParams[name]; !ok {
				return "", fmt.Errorf("path parameter %q is required; configure path_params JSON", name)
			}
		}
	}
	for name, value := range c.pathParams {
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(fmt.Sprint(value)))
	}
	base := strings.TrimRight(c.baseURL, "/")
	if base == "" {
		return "", fmt.Errorf("base URL is required")
	}
	u, err := url.Parse(base + "/" + strings.TrimLeft(path, "/"))
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range c.queryParams {
		q.Set(k, fmt.Sprint(v))
	}
	if op.pageSizeParam != "" {
		size := c.pageSize
		if limit > 0 && limit < size {
			size = limit
		}
		if q.Get(op.pageSizeParam) == "" {
			q.Set(op.pageSizeParam, strconv.Itoa(size))
		}
	}
	if op.pageParam != "" {
		q.Set(op.pageParam, strconv.Itoa(page))
	}
	if op.offsetParam != "" {
		q.Set(op.offsetParam, strconv.Itoa(offset))
	}
	if op.cursorParam != "" && cursor != "" {
		q.Set(op.cursorParam, cursor)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Conn) nextPage(root any, op *operation, page, offset int, cursor, currentURL string, count int, linkHeader string) (string, string, int, int, bool) {
	if next := parseLinkNext(linkHeader); next != "" {
		resolved := resolveLink(currentURL, next)
		if sameOrigin(c.baseURL, resolved) {
			return resolved, "", page, offset, true
		}
	}
	if next, ok := extractNext(root); ok {
		if strings.HasPrefix(next, "http://") || strings.HasPrefix(next, "https://") || strings.HasPrefix(next, "/") || strings.HasPrefix(next, "?") {
			resolved := resolveLink(currentURL, next)
			if sameOrigin(c.baseURL, resolved) {
				return resolved, "", page, offset, true
			}
			return "", "", page, offset, false
		}
		if op.cursorParam != "" {
			return "", next, page, offset, true
		}
	}
	if count == 0 {
		return "", "", page, offset, false
	}
	if op.pageParam != "" {
		return "", "", page + 1, offset, true
	}
	if op.offsetParam != "" {
		return "", "", page, offset + count, true
	}
	return "", "", page, offset, false
}

func parseLinkNext(header string) string {
	for _, part := range strings.Split(header, ",") {
		if !strings.Contains(strings.ToLower(part), `rel="next"`) && !strings.Contains(strings.ToLower(part), "rel=next") {
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

func resolveLink(current, next string) string {
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
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	t, err := url.Parse(target)
	if err != nil {
		return false
	}
	return strings.EqualFold(b.Scheme, t.Scheme) && strings.EqualFold(b.Host, t.Host)
}

func (c *Conn) introspect() {
	paths, _ := c.spec["paths"].(map[string]any)
	c.ops = map[string]operation{}
	seen := map[string]int{}
	keys := sortedKeys(paths)
	for _, path := range keys {
		item, ok := paths[path].(map[string]any)
		if !ok {
			continue
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
			if !c.allowMethods[method] {
				continue
			}
			op, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			params := parametersFor(c.spec, item["parameters"], op["parameters"])
			p := detectPagination(params)
			if !matchesTags(op, c.includeTags, c.excludeTags) {
				continue
			}
			if !requiredSatisfied(path, params, c.pathParams, c.queryParams, c.headers, p) {
				continue
			}
			name := operationName(op, path, method)
			if n := seen[name]; n > 0 {
				seen[name] = n + 1
				name = fmt.Sprintf("%s_%d", name, n+1)
			} else {
				seen[name] = 1
			}
			cols, recordsPath := responseColumns(c.spec, op)
			display := stringValue(op["summary"])
			if display == "" {
				display = stringValue(op["operationId"])
			}
			if display == "" {
				display = strings.ToUpper(method) + " " + path
			}
			c.ops[name] = operation{
				method: method, path: path, recordsPath: recordsPath, columns: cols,
				pageSizeParam: p.pageSize, pageParam: p.page, offsetParam: p.offset, cursorParam: p.cursor,
			}
			c.tables = append(c.tables, connector.Table{Name: name, DisplayName: display, Kind: "api", Columns: cols})
			if len(c.tables) >= c.maxOps {
				return
			}
		}
	}
}

type pageParams struct {
	pageSize string
	page     string
	offset   string
	cursor   string
}

func parametersFor(root map[string]any, lists ...any) []map[string]any {
	seen := map[string]int{}
	var out []map[string]any
	add := func(raw any) {
		arr, _ := raw.([]any)
		for _, item := range arr {
			resolved := resolveRef(root, item)
			m, ok := resolved.(map[string]any)
			if !ok {
				continue
			}
			in, name := stringValue(m["in"]), stringValue(m["name"])
			if in == "" || name == "" {
				continue
			}
			key := in + "\x00" + strings.ToLower(name)
			if idx, ok := seen[key]; ok {
				out[idx] = m
				continue
			}
			seen[key] = len(out)
			out = append(out, m)
		}
	}
	for _, l := range lists {
		add(l)
	}
	return out
}

func detectPagination(params []map[string]any) pageParams {
	var p pageParams
	for _, m := range params {
		if !strings.EqualFold(stringValue(m["in"]), "query") {
			continue
		}
		name := stringValue(m["name"])
		lower := strings.ToLower(name)
		switch lower {
		case "limit", "page_size", "page-size", "pagesize", "per_page", "per-page", "perpage", "count", "size":
			if p.pageSize == "" {
				p.pageSize = name
			}
		case "page", "page_number", "page-number", "pagenumber":
			if p.page == "" {
				p.page = name
			}
		case "offset", "skip":
			if p.offset == "" {
				p.offset = name
			}
		case "cursor", "page_token", "page-token", "pagetoken", "next_token", "next-token", "nexttoken":
			if p.cursor == "" {
				p.cursor = name
			}
		}
	}
	return p
}

func requiredSatisfied(path string, params []map[string]any, pathValues, queryValues, headerValues map[string]any, p pageParams) bool {
	for _, m := range params {
		name := stringValue(m["name"])
		required := false
		switch v := m["required"].(type) {
		case bool:
			required = v
		case string:
			required = strings.EqualFold(v, "true")
		}
		if !required {
			continue
		}
		in := strings.ToLower(stringValue(m["in"]))
		switch in {
		case "path":
			if _, ok := pathValues[name]; !ok {
				return false
			}
		case "query":
			if _, ok := queryValues[name]; ok {
				continue
			}
			if p.pageSize == name || p.page == name || p.offset == name || p.cursor == name {
				continue
			}
			return false
		case "header":
			if !headerExists(headerValues, name) {
				return false
			}
		}
	}
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			if _, ok := pathValues[name]; !ok {
				return false
			}
		}
	}
	return true
}

func matchesTags(op map[string]any, include, exclude map[string]bool) bool {
	tags, _ := op["tags"].([]any)
	has := func(set map[string]bool) bool {
		for _, tag := range tags {
			if set[strings.ToLower(stringValue(tag))] {
				return true
			}
		}
		return false
	}
	if len(exclude) > 0 && has(exclude) {
		return false
	}
	if len(include) > 0 {
		return has(include)
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func operationName(op map[string]any, path, method string) string {
	if id := sanitize(stringValue(op["operationId"])); id != "" {
		return id
	}
	p := strings.Trim(strings.ReplaceAll(path, "{", ""), "/")
	p = strings.ReplaceAll(p, "}", "")
	p = strings.NewReplacer("/", "_", "-", "_", ".", "_").Replace(p)
	if p == "" {
		p = "root"
	}
	return strings.ToLower(method) + "_" + strings.ToLower(p)
}

func sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else if r == '-' || r == ' ' || r == '.' {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func responseColumns(root map[string]any, op map[string]any) ([]connector.Column, string) {
	responses, _ := op["responses"].(map[string]any)
	if len(responses) == 0 {
		return nil, ""
	}
	var response map[string]any
	for _, key := range []string{"200", "201", "202", "203", "204", "default"} {
		if r, ok := responses[key].(map[string]any); ok {
			response = r
			break
		}
	}
	if response == nil {
		for _, r := range responses {
			if m, ok := r.(map[string]any); ok {
				response = m
				break
			}
		}
	}
	if response == nil {
		return nil, ""
	}
	var schema any
	if content, ok := response["content"].(map[string]any); ok {
		keys := sortedKeys(content)
		for _, key := range keys {
			if strings.Contains(strings.ToLower(key), "json") {
				if media, ok := content[key].(map[string]any); ok {
					schema = media["schema"]
					break
				}
			}
		}
		if schema == nil && len(keys) > 0 {
			if media, ok := content[keys[0]].(map[string]any); ok {
				schema = media["schema"]
			}
		}
	}
	if schema == nil {
		schema = response["schema"] // Swagger 2
	}
	if schema == nil {
		return nil, ""
	}
	return schemaColumns(root, resolveRef(root, schema))
}

func schemaColumns(root map[string]any, schema any) ([]connector.Column, string) {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil, ""
	}
	m = composeSchema(root, m)
	if items, path, ok := findArraySchema(root, m, "", 0); ok {
		resolved := resolveRef(root, items)
		if obj, ok := resolved.(map[string]any); ok {
			obj = composeSchema(root, obj)
			if props, ok := obj["properties"].(map[string]any); ok {
				return propertiesColumns(root, props), path
			}
			if t := openAPIType(obj); t != "object" && t != "array" {
				return []connector.Column{{Name: "value", Type: t, Nullable: true}}, path
			}
		}
		return []connector.Column{{Name: "value", Type: "string", Nullable: true}}, path
	}
	if props, ok := m["properties"].(map[string]any); ok {
		return propertiesColumns(root, props), ""
	}
	if additional, ok := m["additionalProperties"].(map[string]any); ok {
		if props, ok := additional["properties"].(map[string]any); ok {
			return propertiesColumns(root, props), ""
		}
	}
	return nil, ""
}

func composeSchema(root map[string]any, m map[string]any) map[string]any {
	if resolved, ok := resolveRef(root, m).(map[string]any); ok {
		m = resolved
	}
	if all, ok := m["allOf"].([]any); ok {
		merged := map[string]any{"type": "object", "properties": map[string]any{}}
		props := merged["properties"].(map[string]any)
		for _, item := range all {
			part, ok := resolveRef(root, item).(map[string]any)
			if !ok {
				continue
			}
			part = composeSchema(root, part)
			if p, ok := part["properties"].(map[string]any); ok {
				for k, v := range p {
					props[k] = v
				}
			}
			if t := stringValue(part["type"]); t != "" && t != "object" {
				merged["type"] = t
			}
		}
		for k, v := range m {
			if k != "allOf" {
				merged[k] = v
			}
		}
		m = merged
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		if variants, ok := m[key].([]any); ok && len(variants) > 0 {
			if first, ok := resolveRef(root, variants[0]).(map[string]any); ok {
				return composeSchema(root, first)
			}
		}
	}
	return m
}

func findArraySchema(root map[string]any, schema any, prefix string, depth int) (any, string, bool) {
	m, ok := resolveRef(root, schema).(map[string]any)
	if !ok || depth > 3 {
		return nil, "", false
	}
	if strings.EqualFold(stringValue(m["type"]), "array") || m["items"] != nil {
		return m["items"], prefix, true
	}
	props, _ := m["properties"].(map[string]any)
	if len(props) == 0 {
		return nil, "", false
	}
	priority := []string{"data", "items", "records", "results", "value", "rows", "content", "entries", "payload"}
	seen := map[string]bool{}
	for _, key := range priority {
		raw, ok := props[key]
		if !ok {
			continue
		}
		seen[key] = true
		target := key
		if prefix != "" {
			target = prefix + "." + key
		}
		if items, path, ok := findArraySchema(root, raw, target, depth+1); ok {
			return items, path, true
		}
	}
	for _, key := range sortedKeys(props) {
		if seen[key] {
			continue
		}
		target := key
		if prefix != "" {
			target = prefix + "." + key
		}
		if items, path, ok := findArraySchema(root, props[key], target, depth+1); ok {
			return items, path, true
		}
	}
	return nil, "", false
}

func propertiesColumns(root map[string]any, props map[string]any) []connector.Column {
	keys := sortedKeys(props)
	out := make([]connector.Column, 0, len(keys))
	for _, key := range keys {
		s := resolveRef(root, props[key])
		typ := "string"
		if m, ok := s.(map[string]any); ok {
			typ = openAPIType(m)
		}
		out = append(out, connector.Column{Name: key, Type: typ, Nullable: true})
	}
	return out
}

func openAPIType(s map[string]any) string {
	t := strings.ToLower(stringValue(s["type"]))
	if arr, ok := s["type"].([]any); ok {
		for _, item := range arr {
			v := strings.ToLower(stringValue(item))
			if v != "null" {
				t = v
				break
			}
		}
	}
	format := strings.ToLower(stringValue(s["format"]))
	switch t {
	case "integer":
		return "integer"
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		return "array"
	case "object":
		return "object"
	case "string":
		if format == "date" || format == "date-time" {
			return "timestamp"
		}
	}
	return "string"
}

func resolveRef(root map[string]any, raw any) any {
	seen := map[string]bool{}
	for {
		m, ok := raw.(map[string]any)
		if !ok {
			return raw
		}
		ref := stringValue(m["$ref"])
		if ref == "" {
			return m
		}
		if seen[ref] {
			return m
		}
		seen[ref] = true
		if !strings.HasPrefix(ref, "#/") {
			return m
		}
		var cur any = root
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			obj, ok := cur.(map[string]any)
			if !ok {
				return m
			}
			cur = obj[part]
		}
		raw = cur
	}
}

func resolveBase(override, specRef string, doc map[string]any) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	if servers, ok := doc["servers"].([]any); ok && len(servers) > 0 {
		if s, ok := servers[0].(map[string]any); ok {
			base := stringValue(s["url"])
			if vars, ok := s["variables"].(map[string]any); ok {
				for name, raw := range vars {
					if v, ok := raw.(map[string]any); ok {
						base = strings.ReplaceAll(base, "{"+name+"}", stringValue(v["default"]))
					}
				}
			}
			if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
				return strings.TrimRight(base, "/")
			}
			if u, err := url.Parse(specRef); err == nil && u.Scheme != "" {
				if b, err := url.Parse(base); err == nil {
					return strings.TrimRight(u.ResolveReference(b).String(), "/")
				}
			}
		}
	}
	if host := stringValue(doc["host"]); host != "" {
		scheme := "https"
		if schemes, ok := doc["schemes"].([]any); ok && len(schemes) > 0 {
			scheme = stringValue(schemes[0])
		}
		return scheme + "://" + host + stringValue(doc["basePath"])
	}
	if u, err := url.Parse(specRef); err == nil && u.Scheme != "" {
		u.Path = ""
		u.RawQuery = ""
		return strings.TrimRight(u.String(), "/")
	}
	return ""
}

func extractNext(root any) (string, bool) {
	m, ok := root.(map[string]any)
	if !ok {
		return "", false
	}
	paths := [][]string{
		{"next"}, {"nextLink"}, {"next_link"}, {"nextPageToken"}, {"next_page_token"},
		{"nextCursor"}, {"next_cursor"}, {"@odata.nextLink"}, {"links", "next"}, {"paging", "next"},
	}
	for _, parts := range paths {
		var cur any = m
		for _, part := range parts {
			obj, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			cur = obj[part]
		}
		switch v := cur.(type) {
		case string:
			if v != "" {
				return v, true
			}
		case map[string]any:
			for _, key := range []string{"href", "url", "next"} {
				if s := stringValue(v[key]); s != "" {
					return s, true
				}
			}
		}
	}
	return "", false
}

func extractRecords(root any, path string) []any {
	if path != "" {
		cur := root
		for _, part := range strings.Split(path, ".") {
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = obj[part]
		}
		if arr, ok := cur.([]any); ok {
			return arr
		}
		return nil
	}
	if arr, ok := root.([]any); ok {
		return arr
	}
	if obj, ok := root.(map[string]any); ok {
		for _, key := range []string{"data", "items", "records", "results", "value", "rows"} {
			if arr, ok := obj[key].([]any); ok {
				return arr
			}
		}
		return []any{obj}
	}
	return nil
}

func inferColumns(rows []map[string]any) []connector.Column {
	seen := map[string]bool{}
	var cols []connector.Column
	for _, row := range rows {
		for k, v := range row {
			if seen[k] {
				continue
			}
			seen[k] = true
			cols = append(cols, connector.Column{Name: k, Type: inferType(v), Nullable: true})
		}
	}
	return cols
}

func inferType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer"
	case float32, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "string"
	}
}

func parseObject(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func readSpec(ctx context.Context, ref, headersRaw string) ([]byte, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
		if err != nil {
			return nil, err
		}
		headers, err := parseObject(headersRaw)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, fmt.Sprint(v))
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch OpenAPI spec: HTTP %d", resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	}
	return os.ReadFile(strings.TrimSpace(ref))
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func intFromAny(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return n, err == nil
	}
	return 0, false
}

func headerExists(headers map[string]any, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func csvSetDefault(raw, def string) map[string]bool {
	if strings.TrimSpace(raw) == "" {
		raw = def
	}
	return csvSet(raw)
}

func csvSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out[part] = true
		}
	}
	return out
}

func intValue(raw string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	return n
}

func boolValue(raw string, def bool) bool {
	if strings.TrimSpace(raw) == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}
