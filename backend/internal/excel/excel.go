// Package excel reads local .xlsx workbooks. Each worksheet is exposed as a
// table; the first row is used as the header row.
package excel

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "excel",
		Name:        "Microsoft Excel",
		Category:    connector.CategoryFile,
		Description: "Local .xlsx workbook worksheets",
		Icon:        "excel",
		Fields: []connector.ConfigField{
			{Key: "path", Label: "Workbook path", Kind: connector.FieldText, Required: true, Placeholder: "/data/report.xlsx", Help: "Path on the AnyQuery server. Old binary .xls files are not supported; save as .xlsx."},
			{Key: "sheet", Label: "Sheet (optional)", Kind: connector.FieldText, Help: "Leave empty to expose every worksheet."},
		},
	}
}

func (Provider) Open(_ context.Context, cfg map[string]string) (connector.Connector, error) {
	path := strings.TrimSpace(cfg["path"])
	if path == "" {
		return nil, fmt.Errorf("workbook path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open workbook: %w", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open workbook: %w", err)
	}
	c := &Conn{file: f, sheet: strings.TrimSpace(cfg["sheet"])}
	if err := c.Ping(context.Background()); err != nil {
		_ = f.Close()
		return nil, err
	}
	return c, nil
}

type Conn struct {
	file  *excelize.File
	sheet string
}

func (c *Conn) Ping(context.Context) error {
	sheets := c.file.GetSheetList()
	if len(sheets) == 0 {
		return fmt.Errorf("workbook has no worksheets")
	}
	if c.sheet != "" && !contains(sheets, c.sheet) {
		return fmt.Errorf("worksheet %q not found", c.sheet)
	}
	return nil
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}
func (c *Conn) Close() error { return c.file.Close() }

func (c *Conn) Introspect(context.Context) ([]connector.Table, error) {
	sheets := c.sheets()
	tables := make([]connector.Table, 0, len(sheets))
	for _, sheet := range sheets {
		rows, err := c.file.GetRows(sheet, excelize.Options{RawCellValue: true})
		if err != nil {
			continue
		}
		header, sample := splitHeader(rows)
		cols := inferColumns(header, sample)
		tables = append(tables, connector.Table{Name: sheet, DisplayName: sheet, Kind: "sheet", Columns: cols})
	}
	return tables, nil
}

func (c *Conn) Fetch(_ context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	rows, err := c.file.GetRows(req.Table, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("read worksheet %s: %w", req.Table, err)
	}
	header, data := splitHeader(rows)
	cols := inferColumns(header, data)
	limit := req.Limit
	if limit <= 0 {
		limit = 1000
	}
	if len(data) > limit {
		data = data[:limit]
	}
	out := make([]map[string]any, 0, len(data))
	for _, values := range data {
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			if i < len(values) {
				row[col.Name] = parseValue(values[i])
			} else {
				row[col.Name] = nil
			}
		}
		out = append(out, row)
	}
	return &connector.FetchResult{Columns: cols, Rows: out, Truncated: len(data) >= limit}, nil
}

func (c *Conn) sheets() []string {
	if c.sheet != "" {
		return []string{c.sheet}
	}
	sheets := c.file.GetSheetList()
	sort.Strings(sheets)
	if len(sheets) > 100 {
		sheets = sheets[:100]
	}
	return sheets
}

func splitHeader(rows [][]string) ([]string, [][]string) {
	if len(rows) == 0 {
		return nil, nil
	}
	header := rows[0]
	for i, h := range header {
		header[i] = strings.TrimSpace(h)
	}
	return header, rows[1:]
}

func inferColumns(header []string, rows [][]string) []connector.Column {
	seen := map[string]int{}
	cols := make([]connector.Column, len(header))
	for i, name := range header {
		if name == "" {
			name = fmt.Sprintf("column_%d", i+1)
		}
		base := name
		if n := seen[strings.ToLower(base)]; n > 0 {
			name = fmt.Sprintf("%s_%d", base, n+1)
		}
		seen[strings.ToLower(base)]++
		typ := "string"
		for _, row := range rows {
			if i >= len(row) || strings.TrimSpace(row[i]) == "" {
				continue
			}
			typ = mergeType(typ, valueType(row[i]))
		}
		cols[i] = connector.Column{Name: name, Type: typ, Nullable: true}
	}
	return cols
}

func valueType(raw string) string {
	v := strings.TrimSpace(raw)
	if _, err := strconv.ParseInt(v, 10, 64); err == nil {
		return "integer"
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return "number"
	}
	if _, err := strconv.ParseBool(v); err == nil {
		return "boolean"
	}
	return "string"
}

func mergeType(a, b string) string {
	if a == "string" || b == "string" {
		return "string"
	}
	if a == b {
		return a
	}
	if (a == "integer" && b == "number") || (a == "number" && b == "integer") {
		return "number"
	}
	return "string"
}

func parseValue(raw string) any {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return raw
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
