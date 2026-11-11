package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	registryMu sync.RWMutex
	registry   = map[string]Provider{}
)

// Register makes a provider available by its declared type. It panics on
// duplicate registrations or invalid specs so mistakes surface at startup.
func Register(p Provider) {
	if p == nil {
		panic("connector: nil provider")
	}
	spec := p.Spec()
	if spec.Type == "" || spec.Name == "" {
		panic("connector: provider must have a type and name")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[spec.Type]; exists {
		panic(fmt.Sprintf("connector: duplicate provider for %q", spec.Type))
	}
	registry[spec.Type] = p
}

// Get returns a provider by type.
func Get(typ string) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[typ]
	return p, ok
}

// Specs returns all type specs sorted by category then name.
func Specs() []TypeSpec {
	registryMu.RLock()
	out := make([]TypeSpec, 0, len(registry))
	for _, p := range registry {
		out = append(out, p.Spec())
	}
	registryMu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Open validates config then opens a connector.
func Open(ctx context.Context, typ string, cfg map[string]string) (Connector, error) {
	p, ok := Get(typ)
	if !ok {
		return nil, fmt.Errorf("unknown data source type %q", typ)
	}
	if err := ValidateConfig(p.Spec(), cfg); err != nil {
		return nil, err
	}
	return p.Open(ctx, cfg)
}

// RegisteredTypes returns the registered type keys.
func RegisteredTypes() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
