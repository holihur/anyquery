package api

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/anyquery/anyquery/internal/model"
	"github.com/anyquery/anyquery/internal/store"
)

type dashboardInput struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Panels      []model.DashboardPanel `json:"panels"`
}

var dashboardVisualizationTypes = map[string]bool{
	"table": true, "bar": true, "line": true, "area": true,
	"pie": true, "scatter": true, "stat": true,
}

func normalizeDashboardInput(in dashboardInput) (dashboardInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return in, fmt.Errorf("dashboard name is required")
	}
	if utf8.RuneCountInString(in.Name) > 120 {
		return in, fmt.Errorf("dashboard name must be at most 120 characters")
	}
	in.Description = strings.TrimSpace(in.Description)
	if utf8.RuneCountInString(in.Description) > 1000 {
		return in, fmt.Errorf("dashboard description must be at most 1000 characters")
	}
	if in.Panels == nil {
		in.Panels = []model.DashboardPanel{}
	}
	if len(in.Panels) > model.MaxPanelsPerDashboard {
		return in, fmt.Errorf("a dashboard can have at most %d panels", model.MaxPanelsPerDashboard)
	}
	seen := map[string]bool{}
	for i := range in.Panels {
		p := &in.Panels[i]
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			p.ID = store.NewID()
		}
		if seen[p.ID] {
			return in, fmt.Errorf("duplicate panel id %q", p.ID)
		}
		seen[p.ID] = true
		p.Title = strings.TrimSpace(p.Title)
		if p.Title == "" {
			p.Title = fmt.Sprintf("Panel %d", i+1)
		}
		if utf8.RuneCountInString(p.Title) > 120 {
			return in, fmt.Errorf("panel title must be at most 120 characters")
		}
		p.SQL = strings.TrimSpace(p.SQL)
		if p.SQL == "" {
			return in, fmt.Errorf("panel %q needs SQL", p.Title)
		}
		if len(p.SQL) > 200_000 {
			return in, fmt.Errorf("panel %q SQL is too long", p.Title)
		}
		p.Visualization.Type = strings.ToLower(strings.TrimSpace(p.Visualization.Type))
		if p.Visualization.Type == "" {
			p.Visualization.Type = "table"
		}
		if !dashboardVisualizationTypes[p.Visualization.Type] {
			return in, fmt.Errorf("unsupported visualization type %q", p.Visualization.Type)
		}
		p.Visualization.X = strings.TrimSpace(p.Visualization.X)
		p.Visualization.Y = normalizeStringList(p.Visualization.Y, 12)
		if p.Layout.W <= 0 {
			p.Layout.W = 6
		}
		if p.Layout.W > 12 {
			p.Layout.W = 12
		}
		if p.Layout.H <= 0 {
			p.Layout.H = 4
		}
		if p.Layout.H > 12 {
			p.Layout.H = 12
		}
		if p.Layout.X < 0 {
			p.Layout.X = 0
		}
		if p.Layout.X > 11 {
			p.Layout.X = 11
		}
		if p.Layout.X+p.Layout.W > 12 {
			p.Layout.W = 12 - p.Layout.X
		}
		if p.Layout.Y < 0 {
			p.Layout.Y = 0
		}
	}
	return in, nil
}

func normalizeStringList(in []string, max int) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if len(out) >= max {
			break
		}
	}
	return out
}

func (s *Server) handleDashboardsList(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, model.ScopeQueryRead) {
		return
	}
	t := currentTenant(r)
	list, err := s.store.ListDashboards(r.Context(), t.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dashboards": list})
}

func (s *Server) handleDashboardGet(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, model.ScopeQueryRead) {
		return
	}
	t := currentTenant(r)
	d, err := s.store.GetDashboard(r.Context(), t.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Dashboard not found")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDashboardCreate(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) || !currentRole(r).CanEdit() {
		writeError(w, http.StatusForbidden, "forbidden", "You cannot create dashboards")
		return
	}
	t := currentTenant(r)
	count, err := s.store.CountDashboards(r.Context(), t.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if count >= model.MaxDashboardsPerWorkspace {
		writeError(w, http.StatusConflict, "dashboard_limit", fmt.Sprintf("This workspace has reached its dashboard limit (%d)", model.MaxDashboardsPerWorkspace))
		return
	}
	var body dashboardInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	body, err = normalizeDashboardInput(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_dashboard", err.Error())
		return
	}
	u := currentUser(r)
	now := store.Now()
	d := &model.Dashboard{
		ID: store.NewID(), TenantID: t.ID, Name: body.Name, Description: body.Description,
		Panels: body.Panels, CreatedBy: u.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateDashboard(r.Context(), d); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "dashboard.create", TargetType: "dashboard", TargetID: d.ID, Metadata: map[string]any{"name": d.Name, "panels": len(d.Panels)}, IP: clientIP(r)})
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleDashboardUpdate(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) || !currentRole(r).CanEdit() {
		writeError(w, http.StatusForbidden, "forbidden", "You cannot edit dashboards")
		return
	}
	t := currentTenant(r)
	id := r.PathValue("id")
	if _, err := s.store.GetDashboard(r.Context(), t.ID, id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Dashboard not found")
		return
	}
	var body dashboardInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	body, err := normalizeDashboardInput(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_dashboard", err.Error())
		return
	}
	if err := s.store.UpdateDashboard(r.Context(), t.ID, id, body.Name, body.Description, body.Panels); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "dashboard.update", TargetType: "dashboard", TargetID: id, Metadata: map[string]any{"name": body.Name, "panels": len(body.Panels)}, IP: clientIP(r)})
	d, err := s.store.GetDashboard(r.Context(), t.ID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDashboardDelete(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) || !currentRole(r).CanEdit() {
		writeError(w, http.StatusForbidden, "forbidden", "You cannot delete dashboards")
		return
	}
	t := currentTenant(r)
	id := r.PathValue("id")
	if _, err := s.store.GetDashboard(r.Context(), t.ID, id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Dashboard not found")
		return
	}
	if err := s.store.DeleteDashboard(r.Context(), t.ID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "dashboard.delete", TargetType: "dashboard", TargetID: id, IP: clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}
