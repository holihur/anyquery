// Package bigquery implements a lightweight BigQuery REST connector. It uses
// an OAuth access token supplied in the data source config, avoiding the large
// Google Cloud SDK dependency.
package bigquery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "bigquery",
		Name:        "Google BigQuery",
		Category:    connector.CategorySQL,
		Description: "BigQuery datasets and tables via the REST API",
		Icon:        "bigquery",
		Maturity:    "native",
		Fields: []connector.ConfigField{
			{Key: "project", Label: "Project ID", Kind: connector.FieldText, Required: true, Placeholder: "my-gcp-project"},
			{Key: "dataset", Label: "Dataset", Kind: connector.FieldText, Required: true, Placeholder: "analytics"},
			{Key: "access_token", Label: "OAuth access token", Kind: connector.FieldPassword, Required: true, Secret: true, Help: "A short-lived Google OAuth token with BigQuery read access."},
			{Key: "location", Label: "Location (optional)", Kind: connector.FieldText, Placeholder: "US"},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	for _, key := range []string{"project", "dataset", "access_token"} {
		if strings.TrimSpace(cfg[key]) == "" {
			return nil, fmt.Errorf("%s is required", key)
		}
	}
	c := &Conn{
		project:  strings.TrimSpace(cfg["project"]),
		dataset:  strings.TrimSpace(cfg["dataset"]),
		token:    strings.TrimSpace(cfg["access_token"]),
		location: strings.TrimSpace(cfg["location"]),
		client:   &http.Client{Timeout: 60 * time.Second},
	}
	if err := c.Ping(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

type Conn struct {
	project  string
	dataset  string
	token    string
	location string
	client   *http.Client
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true, ProjectionPushdown: true}
}
func (c *Conn) Close() error { return nil }

func (c *Conn) Ping(ctx context.Context) error {
	u := fmt.Sprintf("https://bigquery.googleapis.com/bigquery/v2/projects/%s/datasets?maxResults=1", url.PathEscape(c.project))
	var resp struct {
		Datasets []json.RawMessage `json:"datasets"`
	}
	return c.get(ctx, u, &resp)
}

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	u := fmt.Sprintf("https://bigquery.googleapis.com/bigquery/v2/projects/%s/datasets/%s/tables?maxResults=200&fields=tables(tableReference,schema)",
		url.PathEscape(c.project), url.PathEscape(c.dataset))
	var resp struct {
		Tables []struct {
			TableReference struct {
				TableID string `json:"tableId"`
			} `json:"tableReference"`
			Schema *bqSchema `json:"schema"`
		} `json:"tables"`
	}
	if err := c.get(ctx, u, &resp); err != nil {
		return nil, err
	}
	var out []connector.Table
	for i, t := range resp.Tables {
		if t.TableReference.TableID == "" {
			continue
		}
		schema := t.Schema
		if schema == nil && i < 20 {
			detail, err := c.table(ctx, t.TableReference.TableID)
			if err == nil {
				schema = detail.Schema
			}
		}
		cols := schemaColumns(schema)
		out = append(out, connector.Table{
			Name:        t.TableReference.TableID,
			DisplayName: t.TableReference.TableID,
			Schema:      c.dataset,
			Kind:        "table",
			Columns:     cols,
		})
	}
	return out, nil
}

func (c *Conn) table(ctx context.Context, table string) (*bqTable, error) {
	u := fmt.Sprintf("https://bigquery.googleapis.com/bigquery/v2/projects/%s/datasets/%s/tables/%s",
		url.PathEscape(c.project), url.PathEscape(c.dataset), url.PathEscape(table))
	var out bqTable
	if err := c.get(ctx, u, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	if req.Limit <= 0 || req.Limit > 100_000 {
		req.Limit = 10_000
	}
	table := req.Table
	if strings.Contains(table, ".") {
		// The engine exposes dataset.table; use the table suffix.
		table = table[strings.LastIndexByte(table, '.')+1:]
	}
	qualified := "`" + strings.ReplaceAll(c.project+"."+c.dataset+"."+table, "`", "``") + "`"
	cols := "*"
	if len(req.Columns) > 0 {
		quoted := make([]string, len(req.Columns))
		for i, col := range req.Columns {
			quoted[i] = "`" + strings.ReplaceAll(col, "`", "``") + "`"
		}
		cols = strings.Join(quoted, ", ")
	}
	q := fmt.Sprintf("SELECT %s FROM %s LIMIT %d", cols, qualified, req.Limit)
	body := map[string]any{"query": q, "useLegacySql": false, "maxResults": req.Limit, "timeoutMs": 30000}
	var job bqQueryResponse
	if err := c.post(ctx, fmt.Sprintf("https://bigquery.googleapis.com/bigquery/v2/projects/%s/queries", url.PathEscape(c.project)), body, &job); err != nil {
		return nil, err
	}
	for i := 0; !job.JobComplete && i < 30; i++ {
		if job.JobReference.JobID == "" {
			return nil, fmt.Errorf("BigQuery job did not return a job id")
		}
		time.Sleep(500 * time.Millisecond)
		u := fmt.Sprintf("https://bigquery.googleapis.com/bigquery/v2/projects/%s/queries/%s?timeoutMs=10000", url.PathEscape(c.project), url.PathEscape(job.JobReference.JobID))
		if job.JobReference.Location != "" {
			u += "&location=" + url.QueryEscape(job.JobReference.Location)
		}
		var next bqQueryResponse
		if err := c.get(ctx, u, &next); err != nil {
			return nil, err
		}
		job = next
	}
	fields := job.Schema.Fields
	columns := make([]connector.Column, len(fields))
	for i, f := range fields {
		columns[i] = connector.Column{Name: f.Name, Type: bqType(f.Type), Nullable: f.Mode != "REQUIRED"}
	}
	rows := make([]map[string]any, 0, len(job.Rows))
	for _, r := range job.Rows {
		row := make(map[string]any, len(fields))
		for i, f := range fields {
			if i >= len(r.F) {
				row[f.Name] = nil
				continue
			}
			row[f.Name] = bqValue(f, r.F[i].V)
		}
		rows = append(rows, row)
	}
	truncated := len(rows) >= req.Limit
	if n, err := strconv.Atoi(job.TotalRows); err == nil && n > len(rows) {
		truncated = true
	}
	return &connector.FetchResult{Columns: columns, Rows: rows, Truncated: truncated}, nil
}

type bqSchema struct {
	Fields []bqField `json:"fields"`
}
type bqField struct {
	Name   string    `json:"name"`
	Type   string    `json:"type"`
	Mode   string    `json:"mode"`
	Fields []bqField `json:"fields"`
}
type bqTable struct {
	Schema *bqSchema `json:"schema"`
}
type bqQueryResponse struct {
	JobComplete  bool `json:"jobComplete"`
	JobReference struct {
		JobID    string `json:"jobId"`
		Location string `json:"location"`
	} `json:"jobReference"`
	Schema    bqSchema `json:"schema"`
	TotalRows string   `json:"totalRows"`
	Rows      []struct {
		F []struct {
			V any `json:"v"`
		} `json:"f"`
	} `json:"rows"`
}

func schemaColumns(s *bqSchema) []connector.Column {
	if s == nil {
		return nil
	}
	out := make([]connector.Column, len(s.Fields))
	for i, f := range s.Fields {
		out[i] = connector.Column{Name: f.Name, Type: bqType(f.Type), Nullable: f.Mode != "REQUIRED"}
	}
	return out
}

func bqType(t string) string {
	switch strings.ToUpper(t) {
	case "INTEGER", "INT64":
		return "integer"
	case "FLOAT", "FLOAT64", "NUMERIC", "BIGNUMERIC":
		return "number"
	case "BOOLEAN", "BOOL":
		return "boolean"
	case "TIMESTAMP", "DATETIME", "DATE", "TIME":
		return "timestamp"
	case "RECORD", "STRUCT":
		return "object"
	case "REPEATED":
		return "array"
	default:
		return "string"
	}
}

func bqValue(f bqField, v any) any {
	if v == nil {
		return nil
	}
	switch strings.ToUpper(f.Type) {
	case "INTEGER", "INT64":
		if s, ok := v.(string); ok {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n
			}
		}
	case "FLOAT", "FLOAT64", "NUMERIC", "BIGNUMERIC":
		if s, ok := v.(string); ok {
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				return n
			}
		}
	case "BOOLEAN", "BOOL":
		if s, ok := v.(string); ok {
			return s == "true"
		}
		return v
	case "TIMESTAMP":
		if s, ok := v.(string); ok {
			if sec, err := strconv.ParseFloat(s, 64); err == nil {
				return time.Unix(int64(sec), int64((sec-float64(int64(sec)))*1e9)).UTC().Format(time.RFC3339Nano)
			}
			return s
		}
	}
	return v
}

func (c *Conn) get(ctx context.Context, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("BigQuery HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decode BigQuery response: %w", err)
	}
	return nil
}

func (c *Conn) post(ctx context.Context, u string, body any, dst any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("BigQuery HTTP %d: %s", resp.StatusCode, snippet(out))
	}
	if err := json.Unmarshal(out, dst); err != nil {
		return fmt.Errorf("decode BigQuery response: %w", err)
	}
	return nil
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}
