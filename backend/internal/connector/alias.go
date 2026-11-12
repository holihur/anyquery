package connector

import (
	"context"
	"fmt"
)

// AliasProvider exposes a preset over an existing connector implementation.
// SQL wire-compatible services and REST API templates use this to avoid
// duplicating driver code while still appearing as first-class source types.
type AliasProvider struct {
	Base     string
	SpecData TypeSpec
	Defaults map[string]string
}

func (a AliasProvider) Spec() TypeSpec { return a.SpecData }

func (a AliasProvider) ApplyDefaults(cfg map[string]string) {
	for k, v := range a.Defaults {
		if cfg[k] == "" {
			cfg[k] = v
		}
	}
}

func (a AliasProvider) Open(ctx context.Context, cfg map[string]string) (Connector, error) {
	base, ok := Get(a.Base)
	if !ok {
		return nil, fmt.Errorf("base connector %q is not registered", a.Base)
	}
	a.ApplyDefaults(cfg)
	return base.Open(ctx, cfg)
}

// RegisterAlias registers a preset that delegates to baseType. The alias spec
// is validated at registration time; fields are copied from the base when the
// preset does not override them.
func RegisterAlias(baseType string, spec TypeSpec, defaults map[string]string) {
	if baseType == "" || spec.Type == "" || spec.Name == "" {
		panic("connector: alias requires base type, type and name")
	}
	base, ok := Get(baseType)
	if !ok {
		panic(fmt.Sprintf("connector: alias base %q is not registered", baseType))
	}
	baseSpec := base.Spec()
	if len(spec.Fields) == 0 {
		spec.Fields = append([]ConfigField(nil), baseSpec.Fields...)
	}
	if spec.Category == "" {
		spec.Category = baseSpec.Category
	}
	if spec.Maturity == "" {
		spec.Maturity = "compatible"
	}
	Register(AliasProvider{Base: baseType, SpecData: spec, Defaults: defaults})
}
