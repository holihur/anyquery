package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// ScalarFunction is a user-registerable scalar SQL function. Arguments have
// already been evaluated by the engine. Returning (nil, nil) represents SQL
// NULL. Implementations should be safe for concurrent calls if the engine is
// shared.
type ScalarFunction func(ctx context.Context, args []any) (any, error)

// FunctionSpec describes a scalar function registration.
//
// MinArgs is the minimum arity. MaxArgs is the maximum arity, or -1 for a
// variadic function. Both are validated before Fn is called.
type FunctionSpec struct {
	Name    string
	MinArgs int
	MaxArgs int
	Fn      ScalarFunction
}

type functionEntry struct {
	min int
	max int
	fn  ScalarFunction
}

func (e functionEntry) checkArity(name string, n int) error {
	if n < e.min {
		return errf("invalid_arguments", "%s expects at least %d argument(s), got %d", strings.ToUpper(name), e.min, n)
	}
	if e.max >= 0 && n > e.max {
		return errf("invalid_arguments", "%s expects at most %d argument(s), got %d", strings.ToUpper(name), e.max, n)
	}
	return nil
}

// FunctionRegistry is a concurrency-safe registry of scalar SQL functions.
// Built-in functions and custom functions use the same lookup path.
type FunctionRegistry struct {
	mu    sync.RWMutex
	funcs map[string]functionEntry
}

// NewFunctionRegistry returns an empty registry. Use DefaultFunctionRegistry
// for the built-in AnyQuery functions.
func NewFunctionRegistry() *FunctionRegistry {
	return &FunctionRegistry{funcs: map[string]functionEntry{}}
}

// Register adds a custom scalar function. Names are case-insensitive and may
// contain Unicode letters, digits, underscores and dollar signs, but may not
// start with a digit. Duplicate names are rejected.
func (r *FunctionRegistry) Register(spec FunctionSpec) error {
	name := strings.ToLower(strings.TrimSpace(spec.Name))
	if !validFunctionName(name) {
		return fmt.Errorf("invalid function name %q", spec.Name)
	}
	if spec.MinArgs < 0 {
		return fmt.Errorf("function %s: MinArgs must be >= 0", name)
	}
	if spec.MaxArgs < -1 {
		return fmt.Errorf("function %s: MaxArgs must be >= -1", name)
	}
	if spec.MaxArgs >= 0 && spec.MaxArgs < spec.MinArgs {
		return fmt.Errorf("function %s: MaxArgs must be >= MinArgs", name)
	}
	if spec.Fn == nil {
		return fmt.Errorf("function %s: Fn is required", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.funcs == nil {
		r.funcs = map[string]functionEntry{}
	}
	if _, exists := r.funcs[name]; exists {
		return fmt.Errorf("function %s is already registered", name)
	}
	r.funcs[name] = functionEntry{min: spec.MinArgs, max: spec.MaxArgs, fn: spec.Fn}
	return nil
}

func (r *FunctionRegistry) registerBuiltin(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.funcs == nil {
		r.funcs = map[string]functionEntry{}
	}
	if _, exists := r.funcs[name]; exists {
		return
	}
	r.funcs[name] = functionEntry{min: 0, max: -1, fn: func(_ context.Context, args []any) (any, error) {
		return evalBuiltinFunction(name, args)
	}}
}

func (r *FunctionRegistry) lookup(name string) (functionEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.funcs[strings.ToLower(name)]
	return entry, ok
}

// Names returns the registered function names in sorted order.
func (r *FunctionRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.funcs))
	for name := range r.funcs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func validFunctionName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		ok := r == '_' || r == '$' || unicode.IsLetter(r)
		if i > 0 {
			ok = ok || unicode.IsDigit(r)
		}
		if !ok {
			return false
		}
	}
	return true
}

var builtinScalarNames = []string{
	"lower", "lcase", "upper", "ucase", "length", "char_length", "character_length",
	"trim", "btrim", "ltrim", "rtrim", "substring", "substr", "concat", "concat_ws",
	"coalesce", "ifnull", "nvl", "nullif", "abs", "ceil", "ceiling", "floor", "round",
	"sqrt", "power", "pow", "mod", "greatest", "least", "replace", "split_part",
	"now", "current_timestamp", "current_time", "current_date", "date_trunc", "md5",
	"sha256", "random", "rand", "timezone", "to_timestamp", "json_extract",
	"json_extract_scalar", "extract", "date_part", "date_add", "date_sub", "dateadd",
	"datesub", "timestampadd", "date_diff", "datediff", "timestampdiff", "left",
	"right", "reverse", "repeat", "position", "strpos", "instr", "starts_with",
	"startswith", "ends_with", "endswith", "contains", "initcap", "lpad", "rpad",
	"regexp_replace", "regexp_extract", "if", "iff",
}

func defaultFunctionRegistry() *FunctionRegistry {
	r := NewFunctionRegistry()
	for _, name := range builtinScalarNames {
		r.registerBuiltin(name)
	}
	return r
}
