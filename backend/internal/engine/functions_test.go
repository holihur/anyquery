package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestCustomScalarFunction(t *testing.T) {
	e := New(DefaultOptions())
	ctxKey := struct{}{}
	err := e.RegisterFunction("twice", 1, 1, func(ctx context.Context, args []any) (any, error) {
		if ctx.Value(ctxKey) != "ok" {
			t.Fatal("custom function did not receive execution context")
		}
		if len(args) != 1 || args[0] == nil {
			return nil, fmt.Errorf("twice expects one non-NULL argument")
		}
		f, ok := toFloat(args[0])
		if !ok {
			return nil, fmt.Errorf("twice expects a number")
		}
		return f * 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), ctxKey, "ok")
	res, err := e.Execute(ctx, "SELECT twice(total) AS doubled FROM mongo.orders WHERE id = 1", testCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows[0]["doubled"] != float64(200) {
		t.Fatalf("doubled = %#v", res.Rows[0]["doubled"])
	}
}

func TestCustomFunctionIsEngineScoped(t *testing.T) {
	one := New(DefaultOptions())
	if err := one.RegisterFunction("only_here", 0, 0, func(context.Context, []any) (any, error) {
		return int64(7), nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := one.Execute(context.Background(), "SELECT only_here() AS n", testCatalog()); err != nil {
		t.Fatal(err)
	}
	two := New(DefaultOptions())
	if _, err := two.Execute(context.Background(), "SELECT only_here() AS n", testCatalog()); err == nil {
		t.Fatal("custom function leaked to another engine")
	}
}

func TestFunctionRegistrationValidation(t *testing.T) {
	r := NewFunctionRegistry()
	good := func(context.Context, []any) (any, error) { return nil, nil }
	for _, tc := range []struct {
		name string
		spec FunctionSpec
	}{
		{"empty", FunctionSpec{Name: "", Fn: good}},
		{"bad name", FunctionSpec{Name: "bad name", Fn: good}},
		{"leading digit", FunctionSpec{Name: "1fn", Fn: good}},
		{"negative min", FunctionSpec{Name: "f1", MinArgs: -1, Fn: good}},
		{"bad max", FunctionSpec{Name: "f2", MinArgs: 2, MaxArgs: 1, Fn: good}},
		{"nil fn", FunctionSpec{Name: "f3"}},
	} {
		if err := r.Register(tc.spec); err == nil {
			t.Errorf("%s: expected registration error", tc.name)
		}
	}
	if err := r.Register(FunctionSpec{Name: "GoodFunc", MinArgs: 0, MaxArgs: -1, Fn: good}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(FunctionSpec{Name: "goodfunc", MinArgs: 0, MaxArgs: -1, Fn: good}); err == nil {
		t.Fatal("duplicate function registration was accepted")
	}
	if names := r.Names(); len(names) != 1 || names[0] != "goodfunc" {
		t.Fatalf("names = %v", names)
	}
}

func TestCustomFunctionArity(t *testing.T) {
	e := New(DefaultOptions())
	if err := e.RegisterFunction("one_arg", 1, 1, func(context.Context, []any) (any, error) {
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"SELECT one_arg() AS n",
		"SELECT one_arg(1, 2) AS n",
	} {
		if _, err := e.Execute(context.Background(), query, testCatalog()); err == nil {
			t.Errorf("%s: expected arity error", query)
		}
	}
}

func TestCustomFunctionErrorPropagation(t *testing.T) {
	e := New(DefaultOptions())
	if err := e.RegisterFunction("explode", 0, 0, func(context.Context, []any) (any, error) {
		return nil, fmt.Errorf("boom")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), "SELECT explode() AS n", testCatalog()); err == nil {
		t.Fatal("expected custom function error")
	}
}

func TestFunctionRegistryConcurrentRegistration(t *testing.T) {
	e := New(DefaultOptions())
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("custom_%d", i)
			if err := e.RegisterFunction(name, 0, 0, func(context.Context, []any) (any, error) {
				return int64(i), nil
			}); err != nil {
				t.Errorf("register %s: %v", name, err)
			}
			_ = e.Functions().Names()
		}()
	}
	wg.Wait()
	if len(e.Functions().Names()) != len(builtinScalarNames)+32 {
		t.Fatalf("registry size = %d", len(e.Functions().Names()))
	}
	if _, err := e.Execute(context.Background(), "SELECT custom_31() AS n", testCatalog()); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinFunctionRegistry(t *testing.T) {
	e := New(DefaultOptions())
	names := e.Functions().Names()
	if len(names) != len(builtinScalarNames) {
		t.Fatalf("builtin count = %d, want %d", len(names), len(builtinScalarNames))
	}
	for _, want := range []string{"lower", "substring", "json_extract", "date_trunc"} {
		if _, ok := e.Functions().lookup(want); !ok {
			t.Errorf("missing builtin %s", want)
		}
	}
}
