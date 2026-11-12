// Package connector defines the provider-agnostic contract every AnyQuery
// data source implements. Adding a new data source means:
//
//  1. Implement Provider + Connector (often by reusing sqlconn or httpjson).
//  2. Call connector.Register from an init function.
//  3. Blank-import the package in internal/connectors/all.
//
// The query engine only knows this interface, never a concrete database.
package connector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Category groups data sources in the UI.
type Category string

const (
	CategorySQL        Category = "sql"
	CategoryTimeSeries Category = "timeseries"
	CategoryNoSQL      Category = "nosql"
	CategorySearch     Category = "search"
	CategoryAPI        Category = "api"
	CategoryFile       Category = "file"
	CategoryOther      Category = "other"
	CategoryAll                 = ""
)

// FieldKind controls the form input rendered by the frontend.
type FieldKind string

const (
	FieldText     FieldKind = "text"
	FieldPassword FieldKind = "password"
	FieldNumber   FieldKind = "number"
	FieldTextarea FieldKind = "textarea"
	FieldSelect   FieldKind = "select"
	FieldBool     FieldKind = "bool"
)

// Option is a select option.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ConfigField describes one configurable connection property. The same
// metadata powers the generic connection form and secret redaction.
type ConfigField struct {
	Key         string    `json:"key"`
	Label       string    `json:"label"`
	Kind        FieldKind `json:"kind"`
	Required    bool      `json:"required"`
	Secret      bool      `json:"secret"`
	Placeholder string    `json:"placeholder,omitempty"`
	Help        string    `json:"help,omitempty"`
	Default     string    `json:"default,omitempty"`
	Options     []Option  `json:"options,omitempty"`
}

// TypeSpec is the static description of a connector type.
type TypeSpec struct {
	Type        string        `json:"type"`
	Name        string        `json:"name"`
	Category    Category      `json:"category"`
	Description string        `json:"description"`
	Icon        string        `json:"icon"`
	DocsURL     string        `json:"docsUrl,omitempty"`
	Maturity    string        `json:"maturity,omitempty"` // native, compatible, template
	Fields      []ConfigField `json:"fields"`
}

func (s TypeSpec) Field(key string) (ConfigField, bool) {
	for _, f := range s.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return ConfigField{}, false
}

// Column is a normalized column description.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

// Table is a table/collection/index exposed to SQL.
type Table struct {
	// Name is the key used by SQL, e.g. "customers" or "public.orders".
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName,omitempty"`
	Schema      string   `json:"schema,omitempty"`
	Kind        string   `json:"kind"` // table, view, collection, index, stream, file
	Columns     []Column `json:"columns"`
}

// Capabilities let the engine know which optimizations a connector can honor.
// Filters are always re-evaluated by the engine, so a connector may ignore
// pushdown without changing query semantics.
type Capabilities struct {
	FilterPushdown     bool `json:"filterPushdown"`
	LimitPushdown      bool `json:"limitPushdown"`
	ProjectionPushdown bool `json:"projectionPushdown"`
}

// FetchRequest asks a connector for rows. Limit is a hard safety cap; the
// engine may apply a smaller final limit itself.
type FetchRequest struct {
	Table      string
	Columns    []string
	Limit      int
	Predicates []Predicate
}

// Predicate is a simple column comparison that a connector may push to the
// source. The engine always re-evaluates the original predicate, so connectors
// may ignore unsupported predicates without changing correctness.
type Predicate struct {
	Column string
	Op     string // eq, ne, lt, lte, gt, gte, like
	Value  any
}

// FetchResult is a materialized batch of rows. Values must be JSON-friendly:
// time.Time, string, bool, numbers, nil, map[string]any and []any.
type FetchResult struct {
	Columns   []Column         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated"`
}

// Connector is one live connection to a data source.
type Connector interface {
	// Ping verifies connectivity.
	Ping(ctx context.Context) error
	// Introspect returns the current schema.
	Introspect(ctx context.Context) ([]Table, error)
	// Fetch returns rows from one table. Implementations must respect Limit.
	Fetch(ctx context.Context, req FetchRequest) (*FetchResult, error)
	// Capabilities reports optional pushdown support.
	Capabilities() Capabilities
	// Close releases resources.
	Close() error
}

// Provider opens connectors and describes their configuration.
type Provider interface {
	Spec() TypeSpec
	Open(ctx context.Context, config map[string]string) (Connector, error)
}

// ValidateConfig checks required fields for a type spec.
func ValidateConfig(spec TypeSpec, cfg map[string]string) error {
	var missing []string
	for _, f := range spec.Fields {
		if !f.Required {
			continue
		}
		if strings.TrimSpace(cfg[f.Key]) == "" {
			missing = append(missing, f.Label)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// RedactConfig returns config values with secrets masked. The returned bool
// reports whether the value was already masked (useful on updates).
func RedactConfig(spec TypeSpec, cfg map[string]string) map[string]string {
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		out[k] = v
	}
	for _, f := range spec.Fields {
		if f.Secret && out[f.Key] != "" {
			out[f.Key] = "••••••••"
		}
	}
	return out
}

// MergeSecrets keeps existing secret values when the client sends the mask
// back (or omits the field). Non-secret fields are replaced.
func MergeSecrets(spec TypeSpec, oldCfg, newCfg map[string]string) map[string]string {
	out := make(map[string]string, len(newCfg))
	for k, v := range newCfg {
		out[k] = v
	}
	for _, f := range spec.Fields {
		if !f.Secret {
			continue
		}
		if v, ok := newCfg[f.Key]; !ok || v == "" || v == "••••••••" {
			if old, ok := oldCfg[f.Key]; ok {
				out[f.Key] = old
			}
		}
	}
	return out
}

// NormalizeRows lowercases duplicate handling is intentionally left to the
// engine; connectors only normalize values.
func NormalizeRows(rows []map[string]any) {
	for _, row := range rows {
		for k, v := range row {
			row[k] = NormalizeValue(v)
		}
	}
}

// NormalizeValue converts driver-specific values into JSON-friendly ones.
func NormalizeValue(v any) any {
	switch x := v.(type) {
	case nil, string, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return x
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case error:
		return x.Error()
	}
	return v
}

// SortTables sorts schema output deterministically.
func SortTables(tables []Table) {
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].Schema != tables[j].Schema {
			return tables[i].Schema < tables[j].Schema
		}
		return tables[i].Name < tables[j].Name
	})
}

// DedupeColumns merges column sets from sampled documents.
type columnAccumulator map[string]*Column

func newColumnAccumulator() columnAccumulator { return columnAccumulator{} }

// Add records a value's inferred type.
func (a columnAccumulator) Add(name string, value any) {
	c, ok := a[name]
	if !ok {
		c = &Column{Name: name, Type: inferType(value), Nullable: value == nil}
		a[name] = c
		return
	}
	if value == nil {
		c.Nullable = true
		return
	}
	if c.Type == "null" {
		c.Type = inferType(value)
	}
}

func (a columnAccumulator) Columns() []Column {
	out := make([]Column, 0, len(a))
	for _, c := range a {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
	case string:
		return "string"
	case time.Time:
		return "timestamp"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// ErrNotFound is returned when a requested table does not exist.
var ErrNotFound = errors.New("table not found")

// ErrUnsupported is returned for features a connector cannot provide.
var ErrUnsupported = errors.New("operation not supported by this data source")
