package all

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anyquery/anyquery/internal/connector"
)

func TestConnectorSpecContract(t *testing.T) {
	specs := connector.Specs()
	if len(specs) < 100 {
		t.Fatalf("registered specs = %d, want at least 100", len(specs))
	}
	allowedMaturity := map[string]bool{"": true, "native": true, "compatible": true, "template": true}
	allowedCategory := map[connector.Category]bool{
		connector.CategorySQL: true, connector.CategoryTimeSeries: true,
		connector.CategoryNoSQL: true, connector.CategorySearch: true,
		connector.CategoryAPI: true, connector.CategoryFile: true,
		connector.CategoryOther: true,
	}
	for _, spec := range specs {
		if strings.TrimSpace(spec.Type) == "" || strings.TrimSpace(spec.Name) == "" {
			t.Fatalf("invalid spec: %+v", spec)
		}
		if strings.ToLower(spec.Type) != spec.Type || strings.ContainsAny(spec.Type, " \t/") {
			t.Fatalf("type %q must be a lowercase identifier", spec.Type)
		}
		if !allowedCategory[spec.Category] {
			t.Fatalf("type %q has invalid category %q", spec.Type, spec.Category)
		}
		if strings.TrimSpace(spec.Icon) == "" {
			t.Fatalf("type %q has no icon", spec.Type)
		}
		if !allowedMaturity[spec.Maturity] {
			t.Fatalf("type %q has invalid maturity %q", spec.Type, spec.Maturity)
		}
		seen := map[string]bool{}
		for _, field := range spec.Fields {
			if strings.TrimSpace(field.Key) == "" || strings.TrimSpace(field.Label) == "" {
				t.Fatalf("type %q has invalid field %+v", spec.Type, field)
			}
			if seen[field.Key] {
				t.Fatalf("type %q has duplicate field %q", spec.Type, field.Key)
			}
			seen[field.Key] = true
			if field.Kind == connector.FieldSelect && len(field.Options) == 0 {
				t.Fatalf("type %q field %q selects without options", spec.Type, field.Key)
			}
			if field.Kind == connector.FieldSelect {
				values := map[string]bool{}
				for _, option := range field.Options {
					if values[option.Value] {
						t.Fatalf("type %q field %q has duplicate option %q", spec.Type, field.Key, option.Value)
					}
					values[option.Value] = true
				}
			}
		}
		if len(spec.Fields) == 0 {
			t.Fatalf("type %q has no config fields", spec.Type)
		}
		if _, err := json.Marshal(spec); err != nil {
			t.Fatalf("type %q spec is not serializable: %v", spec.Type, err)
		}
	}
}

func TestPresetRecipesApplyDefaults(t *testing.T) {
	for typ, want := range map[string]string{
		"github": "link", "jira": "offset", "notion": "POST",
	} {
		p, ok := connector.Get(typ)
		if !ok {
			t.Fatalf("missing preset %q", typ)
		}
		cfg := map[string]string{}
		if d, ok := p.(connector.ConfigDefaulter); ok {
			d.ApplyDefaults(cfg)
		}
		if typ == "notion" {
			if cfg["http_method"] != want || cfg["body_template"] == "" {
				t.Fatalf("notion defaults = %+v", cfg)
			}
			continue
		}
		if cfg["pagination"] != want {
			t.Fatalf("%s pagination = %q, want %q", typ, cfg["pagination"], want)
		}
	}
}
