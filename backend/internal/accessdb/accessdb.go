// Package accessdb reads Microsoft Access .mdb/.accdb files via the external
// mdbtools utilities. The AnyQuery host needs `mdb-tables` and `mdb-export`.
package accessdb

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
	return connector.TypeSpec{
		Type:        "access",
		Name:        "Microsoft Access",
		Category:    connector.CategoryFile,
		Description: "Read-only .mdb / .accdb through mdbtools",
		Icon:        "access",
		Fields: []connector.ConfigField{
			{Key: "path", Label: "Database path", Kind: connector.FieldText, Required: true, Placeholder: "/data/legacy.mdb", Help: "Path on the AnyQuery server. Requires mdbtools (mdb-tables, mdb-export). .accdb support depends on the installed mdbtools version."},
		},
	}
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
	path := strings.TrimSpace(cfg["path"])
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open Access database: %w", err)
	}
	if _, err := exec.LookPath("mdb-tables"); err != nil {
		return nil, fmt.Errorf("mdbtools is not installed (mdb-tables not found)")
	}
	if _, err := exec.LookPath("mdb-export"); err != nil {
		return nil, fmt.Errorf("mdbtools is not installed (mdb-export not found)")
	}
	c := &Conn{path: path}
	if err := c.Ping(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

type Conn struct{ path string }

func (c *Conn) Ping(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "mdb-tables", "-1", c.path)
	if out, err := cmd.Output(); err != nil {
		return fmt.Errorf("mdb-tables failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *Conn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}
func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
	names, err := c.tables(ctx)
	if err != nil {
		return nil, err
	}
	tables := make([]connector.Table, 0, len(names))
	for _, name := range names {
		header, _, err := c.readCSV(ctx, name, 21)
		if err != nil {
			tables = append(tables, connector.Table{Name: name, DisplayName: name, Kind: "table"})
			continue
		}
		tables = append(tables, connector.Table{Name: name, DisplayName: name, Kind: "table", Columns: inferColumns(header)})
	}
	return tables, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 1000
	}
	header, rows, err := c.readCSV(ctx, req.Table, limit+1)
	if err != nil {
		return nil, err
	}
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	cols := inferColumns(header)
	out := make([]map[string]any, 0, len(rows))
	for _, values := range rows {
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
	return &connector.FetchResult{Columns: cols, Rows: out, Truncated: truncated}, nil
}

func (c *Conn) tables(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "mdb-tables", "-1", c.path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list Access tables: %w", err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "MSys") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 200 {
		names = names[:200]
	}
	return names, nil
}

func (c *Conn) readCSV(ctx context.Context, table string, maxRows int) ([]string, [][]string, error) {
	cmd := exec.CommandContext(ctx, "mdb-export", c.path, table)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = pipe.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	reader := csv.NewReader(pipe)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	header, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read Access table %s: %w", table, err)
	}
	rows := make([][]string, 0, maxRows)
	for i := 0; i < maxRows; i++ {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read Access table %s: %w", table, err)
		}
		rows = append(rows, row)
	}
	return header, rows, nil
}

func inferColumns(header []string) []connector.Column {
	cols := make([]connector.Column, len(header))
	seen := map[string]int{}
	for i, name := range header {
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("column_%d", i+1)
		}
		if n := seen[strings.ToLower(name)]; n > 0 {
			name = fmt.Sprintf("%s_%d", name, n+1)
		}
		seen[strings.ToLower(name)]++
		cols[i] = connector.Column{Name: name, Type: "string", Nullable: true}
	}
	return cols
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
