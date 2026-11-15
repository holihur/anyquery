// Package firebase implements read-only access to Firebase Firestore and
// Realtime Database through their REST APIs. It intentionally uses plain HTTP
// instead of a service-account SDK: users provide an OAuth access token, an ID
// token, or a Realtime Database secret.
package firebase

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
	serviceFirestore = "firestore"
	serviceRealtime  = "realtime"
	defaultTable     = "data"
)

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "firebase",
		Name:        "Firebase",
		Category:    connector.CategoryNoSQL,
		Description: "Firebase Firestore and Realtime Database (REST)",
		Icon:        "firebase",
		Maturity:    "native",
		DocsURL:     "https://firebase.google.com/docs/firestore/use-rest-api",
		Fields: []connector.ConfigField{
			{Key: "service", Label: "Service", Kind: connector.FieldSelect, Default: serviceFirestore, Options: []connector.Option{{Value: serviceFirestore, Label: "Firestore"}, {Value: serviceRealtime, Label: "Realtime Database"}}},
			{Key: "project_id", Label: "Project ID", Kind: connector.FieldText, Placeholder: "my-firebase-project", Help: "Required for Firestore. For Realtime Database it can be omitted when database_url is set."},
			{Key: "database_id", Label: "Firestore database", Kind: connector.FieldText, Default: "(default)", Help: "Firestore database ID, usually (default)."},
			{Key: "database_url", Label: "Realtime Database URL", Kind: connector.FieldText, Placeholder: "https://my-project-default-rtdb.firebaseio.com", Help: "Required for Realtime Database unless project_id is set."},
			{Key: "path", Label: "Realtime path", Kind: connector.FieldText, Placeholder: "users", Help: "Path read as the data table for Realtime Database; empty reads the root."},
			{Key: "table_name", Label: "Realtime table name", Kind: connector.FieldText, Default: defaultTable},
			{Key: "collections", Label: "Firestore collections", Kind: connector.FieldText, Placeholder: "users,orders", Help: "Comma-separated collection IDs. When empty, Introspect asks Firestore for collection IDs."},
			{Key: "endpoint", Label: "Firestore documents endpoint", Kind: connector.FieldText, Placeholder: "https://firestore.googleapis.com/v1/projects/p/databases/(default)/documents", Help: "Optional override for the emulator or a proxy. Must point at the documents collection root."},
			{Key: "token", Label: "Access / ID token or DB secret", Kind: connector.FieldPassword, Secret: true, Help: "Firestore uses Authorization: Bearer. Realtime Database sends it as ?auth=."},
			{Key: "page_size", Label: "Page size", Kind: connector.FieldNumber, Default: "100"},
			{Key: "timeout_seconds", Label: "Timeout (seconds)", Kind: connector.FieldNumber, Default: "30"},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	service := strings.ToLower(strings.TrimSpace(cfg["service"]))
	if service == "" {
		service = serviceFirestore
	}
	if service != serviceFirestore && service != serviceRealtime {
		return nil, fmt.Errorf("firebase service must be firestore or realtime")
	}
	c := &Conn{
		service:     service,
		token:       strings.TrimSpace(cfg["token"]),
		pageSize:    atoiDefault(cfg["page_size"], 100),
		table:       strings.TrimSpace(cfg["table_name"]),
		path:        strings.Trim(strings.TrimSpace(cfg["path"]), "/"),
		collections: splitList(cfg["collections"]),
		client:      &http.Client{Timeout: time.Duration(atoiDefault(cfg["timeout_seconds"], 30)) * time.Second},
	}
	if c.pageSize <= 0 {
		c.pageSize = 100
	}
	if c.pageSize > 1000 {
		c.pageSize = 1000
	}
	if c.table == "" {
		c.table = defaultTable
	}
	switch service {
	case serviceFirestore:
		project := strings.TrimSpace(cfg["project_id"])
		if project == "" {
			return nil, fmt.Errorf("Firebase project_id is required for Firestore")
		}
		db := strings.TrimSpace(cfg["database_id"])
		if db == "" {
			db = "(default)"
		}
		base := strings.TrimSpace(cfg["endpoint"])
		if base == "" {
			base = "https://firestore.googleapis.com/v1/projects/" + url.PathEscape(project) + "/databases/" + url.PathEscape(db) + "/documents"
		}
		if err := validateHTTPURL(base); err != nil {
			return nil, fmt.Errorf("Firestore endpoint: %w", err)
		}
		c.base = strings.TrimRight(base, "/")
	case serviceRealtime:
		base := strings.TrimSpace(cfg["database_url"])
		if base == "" {
			project := strings.TrimSpace(cfg["project_id"])
			if project == "" {
				return nil, fmt.Errorf("Firebase database_url or project_id is required for Realtime Database")
			}
			base = "https://" + project + "-default-rtdb.firebaseio.com"
		}
		if err := validateHTTPURL(base); err != nil {
			return nil, fmt.Errorf("Realtime Database URL: %w", err)
		}
		c.base = strings.TrimRight(base, "/")
	}
	if err := c.Ping(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

type Conn struct {
	service     string
	base        string
	token       string
	table       string
	path        string
	collections []string
	pageSize    int
	client      *http.Client
}

func (c *Conn) Ping(ctx context.Context) error {
	if c.service == serviceFirestore {
		_, err := c.listCollectionIDs(ctx)
		return err
	}
	u := c.base + "/" + c.path + ".json?shallow=true"
	if c.token != "" {
		u += "&auth=" + url.QueryEscape(c.token)
	}
	_, _, err := c.get(ctx, u)
	return err
}

func (c *Conn) Capabilities() connector.Capabilities {
	if c.service == serviceFirestore {
		return connector.Capabilities{FilterPushdown: true, ProjectionPushdown: true, LimitPushdown: true}
	}
	return connector.Capabilities{FilterPushdown: true, LimitPushdown: true}
}

func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	if c.service == serviceRealtime {
		rows, cols, _, err := c.fetchRealtime(ctx, connector.FetchRequest{Table: c.table, Limit: 1})
		if err != nil {
			return nil, err
		}
		_ = rows
		if len(cols) == 0 {
			cols = []connector.Column{{Name: "_key", Type: "string"}, {Name: "value", Type: "string", Nullable: true}}
		}
		return []connector.Table{{Name: c.table, DisplayName: c.table, Kind: "collection", Columns: cols}}, nil
	}
	collections := c.collections
	if len(collections) == 0 {
		ids, err := c.listCollectionIDs(ctx)
		if err != nil {
			return nil, err
		}
		collections = ids
	}
	var tables []connector.Table
	for _, collection := range collections {
		rows, cols, _, err := c.fetchFirestore(ctx, collection, connector.FetchRequest{Table: collection, Limit: 1})
		if err != nil {
			return nil, err
		}
		_ = rows
		if len(cols) == 0 {
			cols = baseFirestoreColumns()
		}
		tables = append(tables, connector.Table{Name: collection, DisplayName: collection, Kind: "collection", Columns: cols})
	}
	return tables, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = c.pageSize
	}
	if c.service == serviceRealtime {
		if req.Table != c.table {
			return nil, fmt.Errorf("unknown table %q", req.Table)
		}
		rows, cols, truncated, err := c.fetchRealtime(ctx, connector.FetchRequest{Table: req.Table, Columns: req.Columns, Predicates: req.Predicates, Limit: limit})
		if err != nil {
			return nil, err
		}
		return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: truncated}, nil
	}
	rows, cols, truncated, err := c.fetchFirestore(ctx, req.Table, connector.FetchRequest{Table: req.Table, Columns: req.Columns, Predicates: req.Predicates, Limit: limit})
	if err != nil {
		return nil, err
	}
	return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: truncated}, nil
}

func (c *Conn) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	if c.service == serviceRealtime {
		return fmt.Sprintf("GET %s/%s.json?auth=*** (limit %d)", c.base, c.path, req.Limit), nil, nil
	}
	return fmt.Sprintf("GET %s/%s?pageSize=%d", c.base, url.PathEscape(req.Table), c.pageSize), nil, nil
}

func (c *Conn) listCollectionIDs(ctx context.Context) ([]string, error) {
	body := map[string]any{"pageSize": 100}
	var out struct {
		CollectionIDs []string `json:"collectionIds"`
		NextPageToken string   `json:"nextPageToken"`
	}
	if err := c.postJSON(ctx, c.base+":listCollectionIds", body, &out); err != nil {
		return nil, fmt.Errorf("list Firestore collections: %w", err)
	}
	return out.CollectionIDs, nil
}

func (c *Conn) fetchFirestore(ctx context.Context, collection string, req connector.FetchRequest) ([]map[string]any, []connector.Column, bool, error) {
	if strings.TrimSpace(collection) == "" {
		return nil, nil, false, fmt.Errorf("Firestore collection is required")
	}
	limit := req.Limit
	if supportedFirestoreFilters(req.Predicates) {
		return c.fetchFirestoreQuery(ctx, collection, req)
	}
	var rows []map[string]any
	seen := map[string]string{}
	token := ""
	truncated := false
	for len(rows) < limit {
		remaining := limit - len(rows)
		pageSize := c.pageSize
		if pageSize > remaining {
			pageSize = remaining
		}
		q := url.Values{}
		q.Set("pageSize", strconv.Itoa(pageSize))
		if token != "" {
			q.Set("pageToken", token)
		}
		for _, field := range firestoreFieldMask(req.Columns) {
			q.Add("mask.fieldPaths", field)
		}
		u := c.base + "/" + url.PathEscape(collection) + "?" + q.Encode()
		body, _, err := c.get(ctx, u)
		if err != nil {
			return nil, nil, false, err
		}
		var resp struct {
			Documents     []firestoreDocument `json:"documents"`
			NextPageToken string              `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, nil, false, fmt.Errorf("decode Firestore response: %w", err)
		}
		for _, doc := range resp.Documents {
			row := firestoreRow(doc)
			addRowTypes(seen, row)
			rows = append(rows, row)
			if len(rows) >= limit {
				truncated = resp.NextPageToken != ""
				break
			}
		}
		if len(rows) >= limit {
			break
		}
		token = resp.NextPageToken
		if token == "" {
			break
		}
	}
	return rows, columnsFor(rows, seen, baseFirestoreColumns()), truncated, nil
}

type firestoreDocument struct {
	Name       string                     `json:"name"`
	Fields     map[string]json.RawMessage `json:"fields"`
	CreateTime string                     `json:"createTime"`
	UpdateTime string                     `json:"updateTime"`
}

func decodeValue(raw json.RawMessage) any {
	var v map[string]json.RawMessage
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	for key, data := range v {
		switch key {
		case "nullValue":
			return nil
		case "booleanValue":
			var b bool
			_ = json.Unmarshal(data, &b)
			return b
		case "integerValue":
			var s string
			if err := json.Unmarshal(data, &s); err == nil {
				if n, err := strconv.ParseInt(s, 10, 64); err == nil {
					return n
				}
				return s
			}
			var n int64
			_ = json.Unmarshal(data, &n)
			return n
		case "doubleValue":
			var n float64
			_ = json.Unmarshal(data, &n)
			return n
		case "timestampValue":
			var s string
			_ = json.Unmarshal(data, &s)
			if t := parseTime(s); t != nil {
				return t
			}
			return s
		case "stringValue", "referenceValue":
			var s string
			_ = json.Unmarshal(data, &s)
			return s
		case "bytesValue":
			var s string
			_ = json.Unmarshal(data, &s)
			return s
		case "geoPointValue":
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			return m
		case "arrayValue":
			var a struct {
				Values []json.RawMessage `json:"values"`
			}
			_ = json.Unmarshal(data, &a)
			out := make([]any, 0, len(a.Values))
			for _, item := range a.Values {
				out = append(out, decodeValue(item))
			}
			return out
		case "mapValue":
			var m struct {
				Fields map[string]json.RawMessage `json:"fields"`
			}
			_ = json.Unmarshal(data, &m)
			out := map[string]any{}
			for k, item := range m.Fields {
				out[k] = decodeValue(item)
			}
			return out
		}
	}
	return nil
}

func (c *Conn) fetchFirestoreQuery(ctx context.Context, collection string, req connector.FetchRequest) ([]map[string]any, []connector.Column, bool, error) {
	limit := req.Limit
	var filters []any
	for _, predicate := range req.Predicates {
		if filter, ok := firestoreFilter(predicate); ok {
			filters = append(filters, filter)
		}
	}
	if len(filters) == 0 {
		return c.fetchFirestore(ctx, collection, connector.FetchRequest{Table: collection, Columns: req.Columns, Limit: limit})
	}
	where := any(filters[0])
	if len(filters) > 1 {
		where = map[string]any{"compositeFilter": map[string]any{"op": "AND", "filters": filters}}
	}
	query := map[string]any{
		"from":  []any{map[string]any{"collectionId": collection}},
		"where": where,
		"limit": limit,
	}
	if fields := firestoreSelect(req.Columns); len(fields) > 0 {
		query["select"] = map[string]any{"fields": fields}
	}
	body, _, err := c.post(ctx, c.base+":runQuery", map[string]any{"structuredQuery": query})
	if err != nil {
		return nil, nil, false, err
	}
	var results []struct {
		Document *firestoreDocument `json:"document"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, nil, false, fmt.Errorf("decode Firestore query response: %w", err)
	}
	var rows []map[string]any
	seen := map[string]string{}
	for _, result := range results {
		if result.Document == nil {
			continue
		}
		row := firestoreRow(*result.Document)
		addRowTypes(seen, row)
		rows = append(rows, row)
	}
	return rows, columnsFor(rows, seen, baseFirestoreColumns()), len(rows) >= limit, nil
}

func firestoreFilter(p connector.Predicate) (map[string]any, bool) {
	op := map[string]string{"eq": "EQUAL", "ne": "NOT_EQUAL", "lt": "LESS_THAN", "lte": "LESS_THAN_OR_EQUAL", "gt": "GREATER_THAN", "gte": "GREATER_THAN_OR_EQUAL"}[p.Op]
	if op == "" || p.Column == "" || firestoreSystemColumn(p.Column) {
		return nil, false
	}
	value, ok := firestoreValue(p.Value)
	if !ok {
		return nil, false
	}
	return map[string]any{"fieldFilter": map[string]any{
		"field": map[string]any{"fieldPath": p.Column},
		"op":    op,
		"value": value,
	}}, true
}

func supportedFirestoreFilters(predicates []connector.Predicate) bool {
	for _, p := range predicates {
		if _, ok := firestoreFilter(p); ok {
			return true
		}
	}
	return false
}

func firestoreSystemColumn(column string) bool {
	switch strings.ToLower(column) {
	case "_id", "_create_time", "_update_time":
		return true
	default:
		return false
	}
}

func firestoreValue(v any) (map[string]any, bool) {
	switch x := v.(type) {
	case string:
		return map[string]any{"stringValue": x}, true
	case bool:
		return map[string]any{"booleanValue": x}, true
	case int:
		return map[string]any{"integerValue": strconv.FormatInt(int64(x), 10)}, true
	case int8:
		return map[string]any{"integerValue": strconv.FormatInt(int64(x), 10)}, true
	case int16:
		return map[string]any{"integerValue": strconv.FormatInt(int64(x), 10)}, true
	case int32:
		return map[string]any{"integerValue": strconv.FormatInt(int64(x), 10)}, true
	case int64:
		return map[string]any{"integerValue": strconv.FormatInt(x, 10)}, true
	case float32:
		return map[string]any{"doubleValue": float64(x)}, true
	case float64:
		return map[string]any{"doubleValue": x}, true
	case time.Time:
		return map[string]any{"timestampValue": x.UTC().Format(time.RFC3339Nano)}, true
	default:
		return nil, false
	}
}

func firestoreFieldMask(columns []string) []string {
	if len(columns) == 0 {
		return nil
	}
	var fields []string
	for _, column := range columns {
		if strings.HasPrefix(column, "_") {
			continue
		}
		fields = append(fields, column)
	}
	return fields
}

func firestoreSelect(columns []string) []any {
	fields := firestoreFieldMask(columns)
	out := make([]any, 0, len(fields))
	for _, field := range fields {
		out = append(out, map[string]any{"fieldPath": field})
	}
	return out
}

func firestoreRow(doc firestoreDocument) map[string]any {
	row := map[string]any{
		"_id":          docID(doc.Name),
		"_create_time": parseTime(doc.CreateTime),
		"_update_time": parseTime(doc.UpdateTime),
	}
	for k, v := range doc.Fields {
		row[k] = decodeValue(v)
	}
	return row
}

func firestoreJSONValue(v any) (string, bool) {
	switch v.(type) {
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		data, err := json.Marshal(v)
		if err != nil {
			return "", false
		}
		return string(data), true
	default:
		return "", false
	}
}

func (c *Conn) fetchRealtime(ctx context.Context, req connector.FetchRequest) ([]map[string]any, []connector.Column, bool, error) {
	limit := req.Limit
	q := url.Values{}
	if c.token != "" {
		q.Set("auth", c.token)
	}
	if limit > 0 {
		q.Set("limitToFirst", strconv.Itoa(limit))
	}
	if len(req.Predicates) == 1 && req.Predicates[0].Op == "eq" && req.Predicates[0].Column != "" && !strings.EqualFold(req.Predicates[0].Column, "_key") {
		value, ok := firestoreJSONValue(req.Predicates[0].Value)
		if ok {
			q.Set("orderBy", strconv.Quote(req.Predicates[0].Column))
			q.Set("equalTo", value)
		}
	}
	u := c.base + "/" + c.path + ".json"
	if encoded := q.Encode(); encoded != "" {
		u += "?" + encoded
	}
	body, _, err := c.get(ctx, u)
	if err != nil {
		return nil, nil, false, err
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, nil, false, fmt.Errorf("decode Realtime Database response: %w", err)
	}
	var rows []map[string]any
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			row := map[string]any{"_key": k}
			if m, ok := v[k].(map[string]any); ok {
				for mk, mv := range m {
					row[mk] = mv
				}
			} else {
				row["value"] = v[k]
			}
			rows = append(rows, row)
		}
	case []any:
		for i, item := range v {
			row := map[string]any{"_key": strconv.Itoa(i)}
			if m, ok := item.(map[string]any); ok {
				for mk, mv := range m {
					row[mk] = mv
				}
			} else {
				row["value"] = item
			}
			rows = append(rows, row)
		}
	default:
		if value != nil {
			rows = append(rows, map[string]any{"_key": "value", "value": value})
		}
	}
	truncated := false
	if len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}
	if len(rows) == 0 {
		return rows, []connector.Column{{Name: "_key", Type: "string"}, {Name: "value", Type: "string", Nullable: true}}, truncated, nil
	}
	seen := map[string]string{}
	for _, row := range rows {
		addRowTypes(seen, row)
	}
	return rows, columnsFor(rows, seen, nil), truncated, nil
}

func (c *Conn) get(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	if c.service == serviceFirestore && c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.do(req)
}

func (c *Conn) post(ctx context.Context, u string, body map[string]any) ([]byte, int, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(data)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.do(req)
}

func (c *Conn) postJSON(ctx context.Context, u string, body map[string]any, out any) error {
	raw, _, err := c.post(ctx, u, body)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (c *Conn) do(req *http.Request) ([]byte, int, error) {
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

func baseFirestoreColumns() []connector.Column {
	return []connector.Column{
		{Name: "_id", Type: "string"},
		{Name: "_create_time", Type: "timestamp", Nullable: true},
		{Name: "_update_time", Type: "timestamp", Nullable: true},
	}
}

func columnsFor(rows []map[string]any, seen map[string]string, base []connector.Column) []connector.Column {
	var out []connector.Column
	added := map[string]bool{}
	for _, col := range base {
		out = append(out, col)
		added[col.Name] = true
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		if !added[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, connector.Column{Name: key, Type: seen[key], Nullable: true})
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

func docID(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if id, err := url.PathUnescape(name); err == nil {
		return id
	}
	return name
}

func parseTime(s string) any {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return nil
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("URL must start with http:// or https://")
	}
	return nil
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
