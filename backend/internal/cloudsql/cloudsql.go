// Package cloudsql implements Snowflake and Databricks SQL through their
// official HTTP SQL APIs. This avoids pulling Apache Arrow and the AWS/Azure
// SDKs into the AnyQuery binary while keeping both popular sources queryable.
package cloudsql

import (
	"bytes"
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

const maxResultBytes = 128 << 20

func postJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body any) (int, map[string]any, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return doJSON(client, req)
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string) (int, map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return doJSON(client, req)
}

func doJSON(client *http.Client, req *http.Request) (int, map[string]any, error) {
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResultBytes))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	var out map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			snippet := string(body)
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			return resp.StatusCode, nil, fmt.Errorf("HTTP %d: invalid JSON response: %s", resp.StatusCode, snippet)
		}
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, out, apiError(resp.StatusCode, out, body)
	}
	return resp.StatusCode, out, nil
}

func apiError(status int, out map[string]any, body []byte) error {
	for _, key := range []string{"message", "error", "detail"} {
		if s, ok := out[key].(string); ok && s != "" {
			return fmt.Errorf("HTTP %d: %s", status, s)
		}
	}
	if e, ok := out["error"].(map[string]any); ok {
		if s, ok := e["message"].(string); ok {
			return fmt.Errorf("HTTP %d: %s", status, s)
		}
	}
	snippet := string(body)
	if len(snippet) > 300 {
		snippet = snippet[:300]
	}
	return fmt.Errorf("HTTP %d: %s", status, snippet)
}

func valueString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func valueInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

// ---- Snowflake -------------------------------------------------------------

func init() { connector.Register(snowflakeProvider{}) }

type snowflakeProvider struct{}

func (snowflakeProvider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "snowflake",
		Name:        "Snowflake",
		Category:    connector.CategorySQL,
		Description: "Snowflake SQL API (OAuth or programmatic access token)",
		Icon:        "snowflake",
		Fields: []connector.ConfigField{
			{Key: "account", Label: "Account identifier", Kind: connector.FieldText, Required: true, Placeholder: "myorg-myaccount"},
			{Key: "token", Label: "Access token", Kind: connector.FieldPassword, Required: true, Secret: true},
			{Key: "token_type", Label: "Token type", Kind: connector.FieldSelect, Default: "PROGRAMMATIC_ACCESS_TOKEN", Options: []connector.Option{{Value: "PROGRAMMATIC_ACCESS_TOKEN", Label: "Programmatic access token"}, {Value: "OAUTH", Label: "OAuth"}}},
			{Key: "warehouse", Label: "Warehouse", Kind: connector.FieldText, Placeholder: "COMPUTE_WH"},
			{Key: "database", Label: "Database", Kind: connector.FieldText, Placeholder: "ANALYTICS"},
			{Key: "schema", Label: "Schema", Kind: connector.FieldText, Placeholder: "PUBLIC"},
			{Key: "role", Label: "Role", Kind: connector.FieldText},
			{Key: "host", Label: "Host override", Kind: connector.FieldText, Help: "Optional, e.g. myorg-myaccount.snowflakecomputing.com"},
		},
	}
}

func (snowflakeProvider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	account := strings.TrimSpace(cfg["account"])
	host := strings.TrimSpace(cfg["host"])
	if host == "" {
		host = account + ".snowflakecomputing.com"
	}
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	if strings.Contains(host, "/") {
		host = strings.Split(host, "/")[0]
	}
	if account == "" || cfg["token"] == "" {
		return nil, fmt.Errorf("account and access token are required")
	}
	c := &snowflakeConn{cfg: cfg, host: host, client: &http.Client{Timeout: 90 * time.Second}}
	pingCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		return nil, err
	}
	return c, nil
}

type snowflakeConn struct {
	cfg    map[string]string
	host   string
	client *http.Client
}

func (c *snowflakeConn) headers() map[string]string {
	h := map[string]string{
		"Authorization":                        "Bearer " + c.cfg["token"],
		"X-Snowflake-Authorization-Token-Type": c.cfg["token_type"],
	}
	if h["X-Snowflake-Authorization-Token-Type"] == "" {
		h["X-Snowflake-Authorization-Token-Type"] = "PROGRAMMATIC_ACCESS_TOKEN"
	}
	return h
}

func (c *snowflakeConn) Ping(ctx context.Context) error {
	_, _, err := c.execute(ctx, "SELECT 1")
	return err
}

func (c *snowflakeConn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}
func (c *snowflakeConn) Close() error { return nil }

func (c *snowflakeConn) execute(ctx context.Context, statement string) ([]connector.Column, [][]any, error) {
	endpoint := "https://" + c.host + "/api/v2/statements"
	body := map[string]any{
		"statement": statement,
		"timeout":   60,
	}
	for _, key := range []string{"warehouse", "database", "schema", "role"} {
		if c.cfg[key] != "" {
			body[key] = c.cfg[key]
		}
	}
	status, resp, err := postJSON(ctx, c.client, endpoint, c.headers(), body)
	if err != nil {
		return nil, nil, err
	}
	if workspace := valueString(resp, "statementHandle"); workspace != "" && valueString(resp, "status") != "success" {
		resp, err = c.poll(ctx, workspace)
		if err != nil {
			return nil, nil, err
		}
	}
	_ = status
	if msg := valueString(resp, "message"); msg != "" && valueString(resp, "status") != "success" {
		return nil, nil, fmt.Errorf("Snowflake: %s", msg)
	}
	meta, _ := resp["resultSetMetaData"].(map[string]any)
	cols := sfColumns(meta)
	data := sfData(resp)
	if len(data) == 0 {
		// A successful DDL-ish response or an empty result.
		return cols, nil, nil
	}
	return cols, data, nil
}

func (c *snowflakeConn) poll(ctx context.Context, handle string) (map[string]any, error) {
	endpoint := "https://" + c.host + "/api/v2/statements/" + url.PathEscape(handle) + "?partition=0"
	for {
		status, resp, err := getJSON(ctx, c.client, endpoint, c.headers())
		if err != nil {
			return nil, err
		}
		switch valueString(resp, "status") {
		case "success":
			return resp, nil
		case "error", "failed", "aborted":
			return nil, fmt.Errorf("Snowflake: %s", valueString(resp, "message"))
		}
		if status == 200 && resp["resultSetMetaData"] != nil {
			return resp, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func sfColumns(meta map[string]any) []connector.Column {
	raw, _ := meta["rowType"].([]any)
	var cols []connector.Column
	for _, r := range raw {
		m, _ := r.(map[string]any)
		name := valueString(m, "name")
		if name == "" {
			continue
		}
		nullable := true
		if v, ok := m["nullable"].(bool); ok {
			nullable = v
		}
		cols = append(cols, connector.Column{Name: name, Type: sfType(valueString(m, "type")), Nullable: nullable})
	}
	return cols
}

func sfType(t string) string {
	t = strings.ToLower(t)
	switch {
	case strings.Contains(t, "fixed"), strings.Contains(t, "real"), strings.Contains(t, "number"), strings.Contains(t, "decimal"):
		return "number"
	case strings.Contains(t, "boolean"):
		return "boolean"
	case strings.Contains(t, "timestamp"), strings.Contains(t, "date"), strings.Contains(t, "time"):
		return "timestamp"
	case strings.Contains(t, "variant"), strings.Contains(t, "object"), strings.Contains(t, "array"):
		return "json"
	default:
		return "string"
	}
}

func sfData(resp map[string]any) [][]any {
	raw, _ := resp["data"].([]any)
	out := make([][]any, 0, len(raw))
	for _, r := range raw {
		row, _ := r.([]any)
		if row != nil {
			out = append(out, row)
		}
	}
	return out
}

func (c *snowflakeConn) Introspect(ctx context.Context) ([]connector.Table, error) {
	tables, err := c.queryMaps(ctx, `SELECT table_schema, table_name, table_type FROM information_schema.tables
WHERE table_schema <> 'INFORMATION_SCHEMA' ORDER BY table_schema, table_name LIMIT 500`)
	if err != nil {
		return nil, err
	}
	cols, err := c.queryMaps(ctx, `SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
WHERE table_schema <> 'INFORMATION_SCHEMA' ORDER BY table_schema, table_name, ordinal_position LIMIT 20000`)
	if err != nil {
		return nil, err
	}
	return buildTables(tables, cols, "TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"), nil
}

func (c *snowflakeConn) queryMaps(ctx context.Context, statement string) ([]map[string]any, error) {
	cols, data, err := c.execute(ctx, statement)
	if err != nil {
		return nil, err
	}
	return rowsToMaps(cols, data), nil
}

func (c *snowflakeConn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > 100001 {
		limit = 100001
	}
	q := "SELECT * FROM " + quoteQualified(req.Table) + " LIMIT " + strconv.Itoa(limit)
	cols, data, err := c.execute(ctx, q)
	if err != nil {
		return nil, err
	}
	rows := rowsToMaps(cols, data)
	truncated := len(rows) >= limit
	if len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}
	return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: truncated}, nil
}

// ---- Databricks ------------------------------------------------------------

func init() { connector.Register(databricksProvider{}) }

type databricksProvider struct{}

func (databricksProvider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "databricks",
		Name:        "Databricks SQL",
		Category:    connector.CategorySQL,
		Description: "Databricks SQL warehouse via Statement Execution API",
		Icon:        "databricks",
		Fields: []connector.ConfigField{
			{Key: "host", Label: "Workspace host", Kind: connector.FieldText, Required: true, Placeholder: "adb-1234567890.azuredatabricks.net"},
			{Key: "token", Label: "Personal access token", Kind: connector.FieldPassword, Required: true, Secret: true},
			{Key: "warehouse_id", Label: "SQL warehouse ID", Kind: connector.FieldText, Required: true, Placeholder: "0123456789abcdef"},
			{Key: "catalog", Label: "Catalog", Kind: connector.FieldText, Placeholder: "main"},
			{Key: "schema", Label: "Schema", Kind: connector.FieldText, Placeholder: "default"},
		},
	}
}

func (databricksProvider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	host := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(cfg["host"]), "https://"), "http://")
	host = strings.TrimRight(host, "/")
	if host == "" || cfg["token"] == "" || cfg["warehouse_id"] == "" {
		return nil, fmt.Errorf("host, token and warehouse ID are required")
	}
	c := &databricksConn{cfg: cfg, host: host, client: &http.Client{Timeout: 90 * time.Second}}
	pingCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		return nil, err
	}
	return c, nil
}

type databricksConn struct {
	cfg    map[string]string
	host   string
	client *http.Client
}

func (c *databricksConn) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + c.cfg["token"]}
}

func (c *databricksConn) Ping(ctx context.Context) error {
	_, _, err := c.execute(ctx, "SELECT 1")
	return err
}

func (c *databricksConn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}
func (c *databricksConn) Close() error { return nil }

func (c *databricksConn) execute(ctx context.Context, statement string) ([]connector.Column, [][]any, error) {
	endpoint := "https://" + c.host + "/api/2.0/sql/statements/"
	body := map[string]any{
		"warehouse_id":    c.cfg["warehouse_id"],
		"statement":       statement,
		"wait_timeout":    "50s",
		"on_wait_timeout": "CANCEL",
		"disposition":     "INLINE",
		"format":          "JSON_ARRAY",
	}
	if c.cfg["catalog"] != "" {
		body["catalog"] = c.cfg["catalog"]
	}
	if c.cfg["schema"] != "" {
		body["schema"] = c.cfg["schema"]
	}
	_, resp, err := postJSON(ctx, c.client, endpoint, c.headers(), body)
	if err != nil {
		return nil, nil, err
	}
	for {
		state := dbState(resp)
		switch state {
		case "SUCCEEDED":
			return dbResult(resp)
		case "FAILED", "CANCELED", "CLOSED":
			return nil, nil, fmt.Errorf("Databricks: %s", dbError(resp))
		}
		id := valueString(resp, "statement_id")
		if id == "" {
			id = valueString(resp, "statementId")
		}
		if id == "" {
			return nil, nil, fmt.Errorf("Databricks: missing statement id")
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
		_, resp, err = getJSON(ctx, c.client, "https://"+c.host+"/api/2.0/sql/statements/"+url.PathEscape(id), c.headers())
		if err != nil {
			return nil, nil, err
		}
	}
}

func dbState(resp map[string]any) string {
	if st, ok := resp["status"].(map[string]any); ok {
		return valueString(st, "state")
	}
	return ""
}

func dbError(resp map[string]any) string {
	if st, ok := resp["status"].(map[string]any); ok {
		if e, ok := st["error"].(map[string]any); ok {
			if msg := valueString(e, "message"); msg != "" {
				return msg
			}
		}
	}
	return "query failed"
}

func dbResult(resp map[string]any) ([]connector.Column, [][]any, error) {
	manifest, _ := resp["manifest"].(map[string]any)
	schema, _ := manifest["schema"].(map[string]any)
	rawCols, _ := schema["columns"].([]any)
	var cols []connector.Column
	for _, rc := range rawCols {
		m, _ := rc.(map[string]any)
		name := valueString(m, "name")
		if name == "" {
			continue
		}
		typ := valueString(m, "type_name")
		if typ == "" {
			typ = valueString(m, "type_text")
		}
		cols = append(cols, connector.Column{Name: name, Type: dbType(typ), Nullable: true})
	}
	res, _ := resp["result"].(map[string]any)
	rawData, _ := res["data_array"].([]any)
	data := make([][]any, 0, len(rawData))
	for _, r := range rawData {
		row, _ := r.([]any)
		if row != nil {
			data = append(data, row)
		}
	}
	return cols, data, nil
}

func dbType(t string) string {
	t = strings.ToLower(t)
	switch {
	case strings.Contains(t, "int"), strings.Contains(t, "long"), strings.Contains(t, "short"), strings.Contains(t, "byte"):
		return "integer"
	case strings.Contains(t, "decimal"), strings.Contains(t, "double"), strings.Contains(t, "float"):
		return "number"
	case strings.Contains(t, "bool"):
		return "boolean"
	case strings.Contains(t, "date"), strings.Contains(t, "time"):
		return "timestamp"
	case strings.Contains(t, "struct"), strings.Contains(t, "array"), strings.Contains(t, "map"), strings.Contains(t, "variant"):
		return "json"
	default:
		return "string"
	}
}

func (c *databricksConn) Introspect(ctx context.Context) ([]connector.Table, error) {
	tables, err := c.queryMaps(ctx, `SELECT table_schema, table_name, table_type FROM information_schema.tables
WHERE table_schema <> 'information_schema' AND table_catalog = current_catalog() ORDER BY table_schema, table_name LIMIT 500`)
	if err != nil {
		return nil, err
	}
	cols, err := c.queryMaps(ctx, `SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns
WHERE table_schema <> 'information_schema' AND table_catalog = current_catalog() ORDER BY table_schema, table_name, ordinal_position LIMIT 20000`)
	if err != nil {
		return nil, err
	}
	return buildTables(tables, cols, "table_schema", "table_name", "column_name", "data_type", "is_nullable"), nil
}

func (c *databricksConn) queryMaps(ctx context.Context, statement string) ([]map[string]any, error) {
	cols, data, err := c.execute(ctx, statement)
	if err != nil {
		return nil, err
	}
	return rowsToMaps(cols, data), nil
}

func (c *databricksConn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > 100001 {
		limit = 100001
	}
	q := "SELECT * FROM " + quoteQualifiedBacktick(req.Table) + " LIMIT " + strconv.Itoa(limit)
	cols, data, err := c.execute(ctx, q)
	if err != nil {
		return nil, err
	}
	rows := dbRowsToMaps(cols, data)
	truncated := len(rows) >= limit
	if len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}
	return &connector.FetchResult{Columns: cols, Rows: rows, Truncated: truncated}, nil
}

// ---- shared helpers --------------------------------------------------------

func quoteQualified(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
	}
	return strings.Join(parts, ".")
}

func quoteQualifiedBacktick(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = "`" + strings.ReplaceAll(p, "`", "``") + "`"
	}
	return strings.Join(parts, ".")
}

func rowsToMaps(cols []connector.Column, data [][]any) []map[string]any {
	out := make([]map[string]any, 0, len(data))
	for _, raw := range data {
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			if i >= len(raw) {
				row[col.Name] = nil
				continue
			}
			row[col.Name] = convertCloudValue(raw[i], col.Type)
		}
		out = append(out, row)
	}
	return out
}

func dbRowsToMaps(cols []connector.Column, data [][]any) []map[string]any {
	return rowsToMaps(cols, data)
}

func convertCloudValue(v any, typ string) any {
	if v == nil {
		return nil
	}
	switch typ {
	case "integer":
		switch x := v.(type) {
		case float64:
			return int64(x)
		case string:
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				return n
			}
		}
	case "number":
		switch x := v.(type) {
		case float64:
			return x
		case string:
			if f, err := strconv.ParseFloat(x, 64); err == nil {
				return f
			}
		}
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x
		case string:
			if b, err := strconv.ParseBool(x); err == nil {
				return b
			}
		}
	}
	return v
}

func buildTables(tableRows, colRows []map[string]any, schemaKey, tableKey, colKey, typKey, nullableKey string) []connector.Table {
	byName := map[string]*connector.Table{}
	for _, r := range tableRows {
		schema := findKey(r, schemaKey)
		table := findKey(r, tableKey)
		if table == "" {
			continue
		}
		key := table
		if schema != "" {
			key = schema + "." + table
		}
		kind := "table"
		if strings.Contains(strings.ToLower(findKey(r, "table_type")), "view") {
			kind = "view"
		}
		byName[strings.ToLower(key)] = &connector.Table{Name: key, DisplayName: table, Schema: schema, Kind: kind}
	}
	for _, r := range colRows {
		schema := findKey(r, schemaKey)
		table := findKey(r, tableKey)
		key := table
		if schema != "" {
			key = schema + "." + table
		}
		t, ok := byName[strings.ToLower(key)]
		if !ok {
			continue
		}
		nullable := strings.EqualFold(findKey(r, nullableKey), "YES") || findKey(r, nullableKey) == "1"
		t.Columns = append(t.Columns, connector.Column{Name: findKey(r, colKey), Type: cloudType(findKey(r, typKey)), Nullable: nullable})
	}
	out := make([]connector.Table, 0, len(byName))
	for _, t := range byName {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func cloudType(t string) string {
	if typ := dbType(t); typ != "string" {
		return typ
	}
	return sfType(t)
}

func findKey(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprint(v)
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return fmt.Sprint(v)
		}
	}
	return ""
}
