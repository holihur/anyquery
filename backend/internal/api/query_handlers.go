package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/cryptox"
	"github.com/anyquery/anyquery/internal/engine"
	"github.com/anyquery/anyquery/internal/model"
	"github.com/anyquery/anyquery/internal/store"
)

// tenantCatalog bridges persisted data sources to the query engine.
type tenantCatalog struct {
	server   *Server
	tenantID string
	opened   map[string]connector.Connector
	metas    []engine.SourceMeta
}

func (c *tenantCatalog) Sources(ctx context.Context) ([]engine.SourceMeta, error) {
	if c.metas != nil {
		return c.metas, nil
	}
	list, err := c.server.store.ListDataSources(ctx, c.tenantID)
	if err != nil {
		return nil, err
	}
	for _, ds := range list {
		c.metas = append(c.metas, engine.SourceMeta{Name: ds.Name, Type: ds.Type, Tables: ds.Tables})
	}
	return c.metas, nil
}

func (c *tenantCatalog) Open(ctx context.Context, name string) (connector.Connector, error) {
	for key, conn := range c.opened {
		if strings.EqualFold(key, name) {
			return conn, nil
		}
	}
	list, err := c.server.store.ListDataSources(ctx, c.tenantID)
	if err != nil {
		return nil, err
	}
	for _, ds := range list {
		if !strings.EqualFold(ds.Name, name) {
			continue
		}
		cfg, err := c.server.decryptConfig(&ds)
		if err != nil {
			return nil, fmt.Errorf("decrypt credentials: %w", err)
		}
		conn, err := connector.Open(ctx, ds.Type, cfg)
		if err != nil {
			return nil, err
		}
		c.opened[name] = conn
		return conn, nil
	}
	return nil, fmt.Errorf("unknown data source %q", name)
}

func (c *tenantCatalog) Close() {
	for _, conn := range c.opened {
		_ = conn.Close()
	}
}

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, model.ScopeQueryExecute) {
		return
	}
	t := currentTenant(r)
	if !currentRole(r).CanQuery() {
		writeError(w, http.StatusForbidden, "forbidden", "You do not have query access")
		return
	}
	var body struct {
		SQL          string `json:"sql"`
		SavedQueryID string `json:"savedQueryId"`
		Params       []any  `json:"params"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	sqlText := strings.TrimSpace(body.SQL)
	if sqlText == "" {
		writeError(w, http.StatusBadRequest, "empty_query", "Enter a SQL query")
		return
	}
	if len(sqlText) > 200_000 {
		writeError(w, http.StatusBadRequest, "query_too_long", "Query text is too long")
		return
	}
	var saved *model.SavedQuery
	if body.SavedQueryID != "" {
		q, err := s.store.GetSavedQuery(r.Context(), t.ID, body.SavedQueryID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "Saved query not found")
			return
		}
		if q.Visibility == "private" && q.CreatedBy != currentUser(r).ID && !currentRole(r).CanAdmin() {
			writeError(w, http.StatusForbidden, "forbidden", "This saved query is private")
			return
		}
		saved = q
	}

	cat := &tenantCatalog{server: s, tenantID: t.ID, opened: map[string]connector.Connector{}}
	defer cat.Close()
	start := time.Now()
	res, execErr := s.engine.ExecuteParams(r.Context(), sqlText, body.Params, cat)
	duration := time.Since(start).Milliseconds()
	run := &model.QueryRun{
		ID: store.NewID(), TenantID: t.ID, UserID: currentUser(r).ID,
		SavedQueryID: body.SavedQueryID, SQL: sqlText, Sources: []string{},
		CreatedAt: store.Now(), DurationMS: duration,
	}
	if execErr == nil {
		seen := map[string]bool{}
		for _, st := range res.Stats.Sources {
			if !seen[st.Source] {
				seen[st.Source] = true
				run.Sources = append(run.Sources, st.Source)
			}
		}
	}
	status := http.StatusOK
	if execErr != nil {
		run.Status, run.Error = "error", execErr.Error()
		status = statusForQueryError(execErr)
	} else {
		run.Status, run.RowCount = "success", res.RowCount
		if saved != nil {
			now := store.Now()
			_ = s.store.UpdateSavedQuery(r.Context(), t.ID, saved.ID, map[string]any{"lastRunAt": now})
		}
	}
	_ = s.store.CreateRun(r.Context(), run)
	if execErr != nil {
		var qe *engine.Error
		if errors.As(execErr, &qe) {
			writeJSON(w, status, map[string]any{"error": apiError{Code: qe.Code, Message: qe.Message, Hint: qe.Hint}, "run": run})
			return
		}
		writeJSON(w, status, map[string]any{"error": apiError{Code: "execution_error", Message: execErr.Error()}, "run": run})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "run": run})
}

func statusForQueryError(err error) int {
	var qe *engine.Error
	if !errors.As(err, &qe) {
		return http.StatusBadRequest
	}
	switch qe.Code {
	case "timeout":
		return http.StatusGatewayTimeout
	case "connection_error", "source_query_error":
		return http.StatusBadGateway
	case "internal", "execution_error":
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}

func (s *Server) handleSavedQueriesList(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, model.ScopeQueryRead) {
		return
	}
	t := currentTenant(r)
	seeAll := currentRole(r).CanAdmin()
	list, err := s.store.ListSavedQueries(r.Context(), t.ID, currentUser(r).ID, seeAll)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queries": list})
}

func (s *Server) handleSavedQueryCreate(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) || !currentRole(r).CanEdit() {
		writeError(w, http.StatusForbidden, "forbidden", "You cannot create saved queries")
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SQL         string `json:"sql"`
		Visibility  string `json:"visibility"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.SQL) == "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "Name and SQL are required")
		return
	}
	if body.Visibility == "" {
		body.Visibility = "team"
	}
	if body.Visibility != "team" && body.Visibility != "private" {
		writeError(w, http.StatusBadRequest, "invalid_visibility", "Visibility must be team or private")
		return
	}
	t := currentTenant(r)
	count, err := s.store.CountSavedQueries(r.Context(), t.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if count >= model.MaxSavedQueriesPerWorkspace {
		writeJSON(w, http.StatusConflict, map[string]any{"error": apiError{
			Code:    "saved_query_limit",
			Message: fmt.Sprintf("This workspace has reached its limit of %d saved queries", model.MaxSavedQueriesPerWorkspace),
			Hint:    "Delete an older saved query to free a slot.",
		}})
		return
	}
	u := currentUser(r)
	now := store.Now()
	q := &model.SavedQuery{
		ID: store.NewID(), TenantID: t.ID, Name: strings.TrimSpace(body.Name),
		Description: strings.TrimSpace(body.Description), SQL: body.SQL,
		Visibility: body.Visibility, CreatedBy: u.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateSavedQuery(r.Context(), q); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "query.save", TargetType: "saved_query", TargetID: q.ID, Metadata: map[string]any{"name": q.Name}, IP: clientIP(r)})
	writeJSON(w, http.StatusCreated, q)
}

func (s *Server) handleSavedQueryGet(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, model.ScopeQueryRead) {
		return
	}
	q, err := s.store.GetSavedQuery(r.Context(), currentTenant(r).ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Saved query not found")
		return
	}
	if q.Visibility == "private" && q.CreatedBy != currentUser(r).ID && !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "This saved query is private")
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) handleSavedQueryUpdate(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) {
		writeError(w, http.StatusForbidden, "forbidden", "API keys cannot edit saved queries")
		return
	}
	t := currentTenant(r)
	q, err := s.store.GetSavedQuery(r.Context(), t.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Saved query not found")
		return
	}
	if q.CreatedBy != currentUser(r).ID && !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only the author or an admin can edit this query")
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SQL         string `json:"sql"`
		Visibility  string `json:"visibility"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.SQL) == "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "Name and SQL are required")
		return
	}
	if body.Visibility != "team" && body.Visibility != "private" {
		writeError(w, http.StatusBadRequest, "invalid_visibility", "Visibility must be team or private")
		return
	}
	err = s.store.UpdateSavedQuery(r.Context(), t.ID, q.ID, map[string]any{
		"name": strings.TrimSpace(body.Name), "description": strings.TrimSpace(body.Description),
		"sql": body.SQL, "visibility": body.Visibility,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "query.update", TargetType: "saved_query", TargetID: q.ID, IP: clientIP(r)})
	updated, _ := s.store.GetSavedQuery(r.Context(), t.ID, q.ID)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleSavedQueryDelete(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) {
		writeError(w, http.StatusForbidden, "forbidden", "API keys cannot delete saved queries")
		return
	}
	t := currentTenant(r)
	q, err := s.store.GetSavedQuery(r.Context(), t.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Saved query not found")
		return
	}
	if q.CreatedBy != currentUser(r).ID && !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only the author or an admin can delete this query")
		return
	}
	if err := s.store.DeleteSavedQuery(r.Context(), t.ID, q.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "query.delete", TargetType: "saved_query", TargetID: q.ID, Metadata: map[string]any{"name": q.Name}, IP: clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunsList(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, model.ScopeQueryRead) {
		return
	}
	runs, err := s.store.ListRuns(r.Context(), currentTenant(r).ID, parseLimit(r, 50))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleAPIKeysList(w http.ResponseWriter, r *http.Request) {
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can view API keys")
		return
	}
	keys, err := s.store.ListAPIKeys(r.Context(), currentTenant(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"apiKeys": keys})
}

func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) || !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can create API keys")
		return
	}
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_name", "Key name is required")
		return
	}
	scopes, err := normalizeScopes(body.Scopes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_scopes", err.Error())
		return
	}
	secret, err := cryptox.RandomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	raw := "aq_" + secret
	t := currentTenant(r)
	u := currentUser(r)
	key := &model.APIKey{
		ID: store.NewID(), TenantID: t.ID, Name: strings.TrimSpace(body.Name),
		Prefix: raw[:11], KeyHash: cryptox.HashToken(raw), CreatedBy: u.ID, CreatedAt: store.Now(), Scopes: scopes,
	}
	if _, err := s.store.APIKeys().InsertOne(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "apikey.create", TargetType: "api_key", TargetID: key.ID, Metadata: map[string]any{"name": key.Name, "scopes": scopes}, IP: clientIP(r)})
	writeJSON(w, http.StatusCreated, map[string]any{"key": raw, "apiKey": key})
}

func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) || !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can revoke API keys")
		return
	}
	t := currentTenant(r)
	if err := s.store.RevokeAPIKey(r.Context(), t.ID, r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "apikey.revoke", TargetType: "api_key", TargetID: r.PathValue("id"), IP: clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

var _ = store.ErrNotFound
