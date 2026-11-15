package api

import (
	"strings"
	"testing"

	"github.com/anyquery/anyquery/internal/model"
)

func TestNormalizeDashboardInputDefaults(t *testing.T) {
	in, err := normalizeDashboardInput(dashboardInput{
		Name: "  Revenue  ",
		Panels: []model.DashboardPanel{{
			Title: "  ",
			SQL:   " SELECT 1 ",
			Visualization: model.DashboardVisualization{
				Y: []string{" revenue ", "revenue", ""},
			},
			Layout: model.DashboardPanelLayout{W: 20, H: 0, X: 10},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != "Revenue" || in.Panels[0].Title != "Panel 1" || in.Panels[0].SQL != "SELECT 1" {
		t.Fatalf("normalized = %+v", in)
	}
	if in.Panels[0].ID == "" {
		t.Fatal("panel id was not generated")
	}
	v := in.Panels[0].Visualization
	if v.Type != "table" || len(v.Y) != 1 || v.Y[0] != "revenue" {
		t.Fatalf("visualization = %+v", v)
	}
	l := in.Panels[0].Layout
	if l.W != 2 || l.H != 4 || l.X != 10 {
		// x+w is clamped to the 12-column grid.
		t.Fatalf("layout = %+v", l)
	}
}

func TestNormalizeDashboardInputRejectsInvalid(t *testing.T) {
	cases := []dashboardInput{
		{Name: ""},
		{Name: strings.Repeat("x", 121)},
		{Name: "ok", Panels: []model.DashboardPanel{{SQL: "select 1", Visualization: model.DashboardVisualization{Type: "radar"}}}},
		{Name: "ok", Panels: []model.DashboardPanel{{SQL: ""}}},
		{Name: "ok", Panels: []model.DashboardPanel{{ID: "same", SQL: "select 1"}, {ID: "same", SQL: "select 2"}}},
	}
	for i, input := range cases {
		if _, err := normalizeDashboardInput(input); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

func TestNormalizeDashboardInputPanelLimit(t *testing.T) {
	panels := make([]model.DashboardPanel, model.MaxPanelsPerDashboard+1)
	for i := range panels {
		panels[i] = model.DashboardPanel{SQL: "select 1"}
	}
	if _, err := normalizeDashboardInput(dashboardInput{Name: "ok", Panels: panels}); err == nil {
		t.Fatal("expected panel limit error")
	}
}
