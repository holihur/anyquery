package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// TableFunctionSpec describes a registered table-valued function.
//
// A table function receives the already-evaluated SQL argument values and
// returns rows whose width must match Columns. It is intentionally small and
// connector-agnostic; callers can register data-source-specific readers without
// touching the parser or planner.
type TableFunctionSpec struct {
	Name    string
	MinArgs int
	MaxArgs int
	Columns []string
	Fn      func(ctx context.Context, args []any) ([][]any, error)
}

type tableFunctionRegistry struct {
	mu    sync.RWMutex
	funcs map[string]TableFunctionSpec
}

func defaultTableFunctionRegistry() *tableFunctionRegistry {
	return &tableFunctionRegistry{funcs: map[string]TableFunctionSpec{}}
}

func (r *tableFunctionRegistry) register(spec TableFunctionSpec) error {
	name := strings.ToLower(strings.TrimSpace(spec.Name))
	if name == "" || strings.ContainsAny(name, " \t\r\n()") {
		return fmt.Errorf("invalid table function name %q", spec.Name)
	}
	if spec.Fn == nil {
		return fmt.Errorf("table function %s has no implementation", spec.Name)
	}
	if spec.MinArgs < 0 || (spec.MaxArgs >= 0 && spec.MaxArgs < spec.MinArgs) {
		return fmt.Errorf("table function %s has invalid arity", spec.Name)
	}
	if len(spec.Columns) == 0 {
		return fmt.Errorf("table function %s must declare at least one column", spec.Name)
	}
	spec.Name = name
	spec.Columns = append([]string(nil), spec.Columns...)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.funcs[name]; exists {
		return fmt.Errorf("table function %s is already registered", spec.Name)
	}
	r.funcs[name] = spec
	return nil
}

func (r *tableFunctionRegistry) lookup(name string) (TableFunctionSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.funcs[strings.ToLower(name)]
	return spec, ok
}
