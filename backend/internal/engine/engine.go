// Package engine executes the AnyQuery SQL AST as a pipeline of relational
// operators. Connectors are accessed only through a Catalog, so the engine has
// no knowledge of MongoDB, Postgres, HTTP, etc.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/sql"
)

// Error is a user-facing query error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func errf(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// SourceMeta is catalog information about one registered data source.
type SourceMeta struct {
	Name   string
	Type   string
	Tables []connector.Table
}

// Catalog supplies source metadata and live connectors.
type Catalog interface {
	Sources(ctx context.Context) ([]SourceMeta, error)
	Open(ctx context.Context, source string) (connector.Connector, error)
}

// Options configures execution limits.
type Options struct {
	MaxSourceRows int
	MaxResultRows int
	MaxJoinRows   int
	Timeout       time.Duration
}

// DefaultOptions returns sane MVP limits.
func DefaultOptions() Options {
	return Options{
		MaxSourceRows: 100_000,
		MaxResultRows: 10_000,
		MaxJoinRows:   2_000_000,
		Timeout:       30 * time.Second,
	}
}

// Engine executes queries.
type Engine struct {
	Opts Options

	functions *FunctionRegistry
	funcOnce  sync.Once
}

func New(opts Options) *Engine {
	if opts.MaxSourceRows <= 0 {
		opts = DefaultOptions()
	}
	return &Engine{Opts: opts, functions: defaultFunctionRegistry()}
}

// Functions returns the scalar function registry. The returned registry is
// safe for concurrent use; register custom functions before serving queries
// when possible.
func (e *Engine) Functions() *FunctionRegistry {
	e.funcOnce.Do(func() {
		if e.functions == nil {
			e.functions = defaultFunctionRegistry()
		}
	})
	return e.functions
}

// RegisterFunction registers a custom scalar function on this engine. It is a
// convenience wrapper around Functions().Register and rejects invalid names,
// arity ranges, nil implementations and duplicate names.
func (e *Engine) RegisterFunction(name string, minArgs, maxArgs int, fn ScalarFunction) error {
	return e.Functions().Register(FunctionSpec{Name: name, MinArgs: minArgs, MaxArgs: maxArgs, Fn: fn})
}

// ResultColumn is one output column.
type ResultColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// SourceStat describes one source fetch.
type SourceStat struct {
	Source     string `json:"source"`
	Table      string `json:"table"`
	Rows       int    `json:"rows"`
	DurationMS int64  `json:"durationMs"`
}

// Result is a materialized query result.
type Result struct {
	Columns   []ResultColumn   `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	RowCount  int              `json:"rowCount"`
	Truncated bool             `json:"truncated"`
	Warnings  []string         `json:"warnings,omitempty"`
	Stats     ResultStats      `json:"stats"`
}

// ResultStats contains execution telemetry.
type ResultStats struct {
	DurationMS int64        `json:"durationMs"`
	Sources    []SourceStat `json:"sources"`
	Scanned    int          `json:"scannedRows"`
	Stages     []string     `json:"stages"`
}

// dataset is the intermediate relational result. Internal row keys are
// lower-cased output labels; display names live in Columns.
type dataset struct {
	columns []ResultColumn
	rows    []map[string]any
}

func newDataset(cols []ResultColumn) *dataset {
	return &dataset{columns: dedupeResultColumns(cols)}
}

func (d *dataset) evalCtx(row map[string]any) *evalCtx {
	aliases := make(map[string]any, len(row))
	for k, v := range row {
		aliases[strings.ToLower(k)] = v
	}
	return &evalCtx{aliases: aliases}
}

func (ec *execCtx) datasetCtx(d *dataset, row map[string]any) *evalCtx {
	aliases := make(map[string]any, len(row))
	for k, v := range row {
		aliases[strings.ToLower(k)] = v
	}
	return &evalCtx{aliases: aliases, outer: ec.outerEval, exec: ec}
}

// Execute parses and runs one statement without bind parameters.
func (e *Engine) Execute(ctx context.Context, query string, cat Catalog) (*Result, error) {
	return e.ExecuteParams(ctx, query, nil, cat)
}

// ExecuteParams parses and runs one statement with positional bind parameters.
// Parameters are 1-based for both ? and $n placeholders.
func (e *Engine) ExecuteParams(ctx context.Context, query string, params []any, cat Catalog) (*Result, error) {
	start := time.Now()
	if e.Opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Opts.Timeout)
		defer cancel()
	}
	stmt, err := sql.Parse(query)
	if err != nil {
		return nil, &Error{Code: "parse_error", Message: err.Error()}
	}
	ec := &execCtx{
		ctx:      ctx,
		engine:   e,
		catalog:  cat,
		opened:   map[string]connector.Connector{},
		ctes:     map[string]*dataset{},
		warnings: []string{},
		params:   params,
	}
	defer ec.close()

	res, err := ec.run(stmt)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &Error{Code: "timeout", Message: fmt.Sprintf("query exceeded the %s timeout", e.Opts.Timeout)}
		}
		var qe *Error
		if errors.As(err, &qe) {
			return nil, qe
		}
		return nil, &Error{Code: "execution_error", Message: err.Error()}
	}
	res.Stats.DurationMS = time.Since(start).Milliseconds()
	res.Stats.Sources = make([]SourceStat, 0, len(ec.stats))
	res.Stats.Sources = append(res.Stats.Sources, ec.stats...)
	for _, st := range ec.stats {
		res.Stats.Scanned += st.Rows
	}
	res.Stats.Stages = make([]string, 0, len(ec.stageLog))
	res.Stats.Stages = append(res.Stats.Stages, ec.stageLog...)
	res.Warnings = append(res.Warnings, ec.warnings...)
	return res, nil
}

type execCtx struct {
	ctx       context.Context
	engine    *Engine
	catalog   Catalog
	sources   []SourceMeta
	opened    map[string]connector.Connector
	ctes      map[string]*dataset
	warnings  []string
	stats     []SourceStat
	stageLog  []string
	outerEval *evalCtx
	params    []any
	scanHints map[*sql.TableRef]*scanHint
}

func (ec *execCtx) close() {
	for _, c := range ec.opened {
		_ = c.Close()
	}
}

func (ec *execCtx) stage(name string) { ec.stageLog = append(ec.stageLog, name) }

func (ec *execCtx) run(stmt sql.Statement) (*Result, error) {
	switch s := stmt.(type) {
	case *sql.SelectStmt:
		ds, err := ec.executeSelect(s)
		if err != nil {
			return nil, err
		}
		return datasetToResult(ds, ec.engine.Opts.MaxResultRows, ec.warnings), nil
	case *sql.ValuesStmt:
		ds, err := ec.datasetForValues(&sql.ValuesTable{Rows: s.Rows})
		if err != nil {
			return nil, err
		}
		return datasetToResult(ds, ec.engine.Opts.MaxResultRows, ec.warnings), nil
	case *sql.TableStmt:
		ds, err := ec.executeSelect(&sql.SelectStmt{
			From:        []sql.TableExpr{s.Ref},
			Projections: []sql.SelectItem{{Star: true}},
		})
		if err != nil {
			return nil, err
		}
		return datasetToResult(ds, ec.engine.Opts.MaxResultRows, ec.warnings), nil
	case *sql.ShowStmt:
		ds, err := ec.show(s)
		if err != nil {
			return nil, err
		}
		return datasetToResult(ds, ec.engine.Opts.MaxResultRows, ec.warnings), nil
	case *sql.DescribeStmt:
		ds, err := ec.describe(s.Source, s.Table)
		if err != nil {
			return nil, err
		}
		return datasetToResult(ds, ec.engine.Opts.MaxResultRows, ec.warnings), nil
	case *sql.ExplainStmt:
		text := explainText(s.Stmt)
		ds := newDataset([]ResultColumn{{Name: "plan", Type: "string"}})
		ds.rows = []map[string]any{{"plan": text}}
		return datasetToResult(ds, 1, ec.warnings), nil
	default:
		return nil, errf("unsupported", "unsupported statement %T", stmt)
	}
}

func explainText(stmt sql.Statement) string {
	switch s := stmt.(type) {
	case *sql.SelectStmt:
		lines := []string{sql.FormatSelect(s)}
		lines = append(lines, "operators:")
		if len(s.CTEs) > 0 {
			lines = append(lines, "  cte-scan")
		}
		lines = append(lines, "  source-scan")
		if s.Where != nil {
			lines = append(lines, "  filter")
		}
		if len(s.GroupBy) > 0 {
			lines = append(lines, "  hash-aggregate")
		}
		lines = append(lines, "  project")
		if s.Distinct {
			lines = append(lines, "  distinct")
		}
		if len(s.OrderBy) > 0 {
			lines = append(lines, "  sort")
		}
		if s.Limit != nil || s.LimitExpr != nil {
			lines = append(lines, "  limit")
		}
		for _, p := range s.Pipes {
			lines = append(lines, "  |> "+sql.FormatPipe(p))
		}
		return strings.Join(lines, "\n")
	default:
		return fmt.Sprintf("%T", stmt)
	}
}

func (ec *execCtx) loadSources() error {
	if ec.sources != nil {
		return nil
	}
	srcs, err := ec.catalog.Sources(ec.ctx)
	if err != nil {
		return err
	}
	ec.sources = srcs
	return nil
}

func (ec *execCtx) sourceMeta(name string) *SourceMeta {
	for i := range ec.sources {
		if strings.EqualFold(ec.sources[i].Name, name) {
			return &ec.sources[i]
		}
	}
	return nil
}

func (ec *execCtx) tableMeta(source, table string) (*SourceMeta, *connector.Table, error) {
	if err := ec.loadSources(); err != nil {
		return nil, nil, err
	}
	sm := ec.sourceMeta(source)
	if sm == nil {
		var names []string
		for _, s := range ec.sources {
			names = append(names, s.Name)
		}
		return nil, nil, &Error{Code: "unknown_source", Message: fmt.Sprintf("unknown data source %q", source), Hint: "available sources: " + strings.Join(names, ", ")}
	}
	for i := range sm.Tables {
		if strings.EqualFold(sm.Tables[i].Name, table) {
			return sm, &sm.Tables[i], nil
		}
	}
	var names []string
	for _, t := range sm.Tables {
		names = append(names, t.Name)
	}
	hint := "refresh the data source schema"
	if len(names) > 0 {
		hint = "available tables: " + strings.Join(names, ", ")
	}
	return nil, nil, &Error{Code: "unknown_table", Message: fmt.Sprintf("unknown table %q in data source %q", table, source), Hint: hint}
}

func (ec *execCtx) connectorFor(name string) (connector.Connector, error) {
	if c, ok := ec.opened[name]; ok {
		return c, nil
	}
	c, err := ec.catalog.Open(ec.ctx, name)
	if err != nil {
		return nil, &Error{Code: "connection_error", Message: fmt.Sprintf("connect to %q: %v", name, err)}
	}
	ec.opened[name] = c
	return c, nil
}

func (ec *execCtx) show(s *sql.ShowStmt) (*dataset, error) {
	if err := ec.loadSources(); err != nil {
		return nil, err
	}
	switch s.Target {
	case "sources":
		ds := newDataset([]ResultColumn{{Name: "source", Type: "string"}, {Name: "type", Type: "string"}, {Name: "tables", Type: "integer"}})
		srcs := append([]SourceMeta(nil), ec.sources...)
		sort.Slice(srcs, func(i, j int) bool { return srcs[i].Name < srcs[j].Name })
		for _, src := range srcs {
			ds.rows = append(ds.rows, map[string]any{"source": src.Name, "type": src.Type, "tables": int64(len(src.Tables))})
		}
		return ds, nil
	case "tables":
		ds := newDataset([]ResultColumn{{Name: "source", Type: "string"}, {Name: "table", Type: "string"}, {Name: "kind", Type: "string"}, {Name: "columns", Type: "integer"}})
		for _, src := range ec.sources {
			if s.Source != "" && !strings.EqualFold(s.Source, src.Name) {
				continue
			}
			for _, t := range src.Tables {
				ds.rows = append(ds.rows, map[string]any{"source": src.Name, "table": t.Name, "kind": t.Kind, "columns": int64(len(t.Columns))})
			}
		}
		sort.Slice(ds.rows, func(i, j int) bool {
			a := fmt.Sprint(ds.rows[i]["source"], ".", ds.rows[i]["table"])
			b := fmt.Sprint(ds.rows[j]["source"], ".", ds.rows[j]["table"])
			return a < b
		})
		return ds, nil
	case "columns":
		return ec.describe(s.Source, s.Table)
	default:
		return nil, errf("unsupported", "unsupported SHOW target %q", s.Target)
	}
}

func (ec *execCtx) describe(source, table string) (*dataset, error) {
	_, tm, err := ec.tableMeta(source, table)
	if err != nil {
		return nil, err
	}
	ds := newDataset([]ResultColumn{{Name: "column", Type: "string"}, {Name: "type", Type: "string"}, {Name: "nullable", Type: "boolean"}})
	for _, c := range tm.Columns {
		ds.rows = append(ds.rows, map[string]any{"column": c.Name, "type": c.Type, "nullable": c.Nullable})
	}
	return ds, nil
}

func datasetToResult(ds *dataset, maxRows int, warnings []string) *Result {
	res := &Result{Columns: ds.columns}
	if res.Columns == nil {
		res.Columns = []ResultColumn{}
	}
	rows := ds.rows
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
		res.Truncated = true
		warnings = append(warnings, fmt.Sprintf("result truncated at %d rows", maxRows))
	}
	res.Rows = make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out := make(map[string]any, len(ds.columns))
		for _, c := range ds.columns {
			v, _ := r[strings.ToLower(c.Name)]
			out[c.Name] = toJSONValue(v)
		}
		res.Rows = append(res.Rows, out)
	}
	res.RowCount = len(res.Rows)
	res.Warnings = warnings
	return res
}

func toJSONValue(v any) any {
	switch x := v.(type) {
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = toJSONValue(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = toJSONValue(v)
		}
		return out
	default:
		return x
	}
}

func dedupeResultColumns(cols []ResultColumn) []ResultColumn {
	seen := map[string]int{}
	out := make([]ResultColumn, 0, len(cols))
	for _, c := range cols {
		if c.Name == "" {
			c.Name = "column"
		}
		base := c.Name
		if n := seen[strings.ToLower(base)]; n > 0 {
			c.Name = fmt.Sprintf("%s_%d", base, n+1)
		}
		seen[strings.ToLower(base)]++
		out = append(out, c)
	}
	return out
}
