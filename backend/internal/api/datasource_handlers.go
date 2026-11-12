package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/model"
	"github.com/anyquery/anyquery/internal/store"
)

var dsNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,39}$`)

type dataSourceDTO struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	TypeName     string            `json:"typeName"`
	Icon         string            `json:"icon"`
	Config       map[string]string `json:"config"`
	Tables       []connector.Table `json:"tables"`
	Status       string            `json:"status"`
	LastError    string            `json:"lastError,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	LastSyncedAt *time.Time        `json:"lastSyncedAt,omitempty"`
}

func (s *Server) toDTO(ds model.DataSource) dataSourceDTO {
	tables := make([]connector.Table, len(ds.Tables))
	for i, t := range ds.Tables {
		if t.Columns == nil {
			t.Columns = []connector.Column{}
		}
		tables[i] = t
	}
	dto := dataSourceDTO{
		ID: ds.ID, Name: ds.Name, Type: ds.Type, Config: map[string]string{},
		Tables: tables, Status: ds.Status, LastError: ds.LastError,
		CreatedAt: ds.CreatedAt, UpdatedAt: ds.UpdatedAt, LastSyncedAt: ds.LastSyncedAt,
	}
	if spec, ok := connector.Get(ds.Type); ok {
		dto.TypeName = spec.Spec().Name
		dto.Icon = spec.Spec().Icon
		cfg, err := s.decryptConfig(&ds)
		if err == nil {
			dto.Config = connector.RedactConfig(spec.Spec(), cfg)
		}
	} else {
		dto.TypeName = ds.Type
	}
	return dto
}

func (s *Server) decryptConfig(ds *model.DataSource) (map[string]string, error) {
	cfg := map[string]string{}
	if ds.Config == "" {
		return cfg, nil
	}
	if err := s.key.DecryptJSON(ds.Config, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *Server) handleDataSourcesList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListDataSources(r.Context(), currentTenant(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]dataSourceDTO, len(list))
	for i, ds := range list {
		out[i] = s.toDTO(ds)
	}
	writeJSON(w, http.StatusOK, map[string]any{"dataSources": out})
}

func (s *Server) handleDataSourceGet(w http.ResponseWriter, r *http.Request) {
	ds, err := s.store.GetDataSource(r.Context(), currentTenant(r).ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Data source not found")
		return
	}
	writeJSON(w, http.StatusOK, s.toDTO(*ds))
}

func (s *Server) handleDataSourceCreate(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can add data sources")
		return
	}
	var body struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Config map[string]string `json:"config"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !dsNameRE.MatchString(body.Name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "Name must start with a letter and contain only letters, numbers and underscores")
		return
	}
	spec, ok := connector.Get(body.Type)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_type", "Unknown data source type")
		return
	}
	if err := connector.ValidateConfig(spec.Spec(), body.Config); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	encrypted, err := s.key.EncryptJSON(body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	now := store.Now()
	u := currentUser(r)
	ds := &model.DataSource{
		ID: store.NewID(), TenantID: t.ID, Name: body.Name, Type: body.Type,
		Config: encrypted, Status: "error", CreatedBy: u.ID, CreatedAt: now, UpdatedAt: now,
	}
	tables, syncErr := s.introspectConfig(r.Context(), body.Type, body.Config)
	if syncErr == nil {
		ds.Tables = tables
		ds.Status = "ready"
		ds.LastSyncedAt = &now
	} else {
		ds.LastError = syncErr.Error()
	}
	if err := s.store.CreateDataSource(r.Context(), ds); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			writeError(w, http.StatusConflict, "name_taken", "A data source with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "datasource.create", TargetType: "datasource", TargetID: ds.ID, Metadata: map[string]any{"name": ds.Name, "type": ds.Type}, IP: clientIP(r)})
	writeJSON(w, http.StatusCreated, s.toDTO(*ds))
}

func (s *Server) handleDataSourceUpdate(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can edit data sources")
		return
	}
	ds, err := s.store.GetDataSource(r.Context(), t.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Data source not found")
		return
	}
	var body struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	name := ds.Name
	if strings.TrimSpace(body.Name) != "" {
		name = strings.TrimSpace(body.Name)
	}
	if !dsNameRE.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "Invalid data source name")
		return
	}
	oldCfg, err := s.decryptConfig(ds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Could not decrypt stored credentials")
		return
	}
	spec, ok := connector.Get(ds.Type)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_type", "Connector type is no longer available")
		return
	}
	cfg := connector.MergeSecrets(spec.Spec(), oldCfg, body.Config)
	if err := connector.ValidateConfig(spec.Spec(), cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	encrypted, err := s.key.EncryptJSON(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := s.store.UpdateDataSourceConfig(r.Context(), t.ID, ds.ID, name, ds.Type, encrypted); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	status, lastError := "ready", ""
	var tables []connector.Table
	if tbs, err := s.introspectConfig(r.Context(), ds.Type, cfg); err != nil {
		status, lastError = "error", err.Error()
	} else {
		tables = tbs
	}
	_ = s.store.UpdateDataSourceStatus(r.Context(), t.ID, ds.ID, tables, status, lastError)
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "datasource.update", TargetType: "datasource", TargetID: ds.ID, Metadata: map[string]any{"name": name}, IP: clientIP(r)})
	updated, _ := s.store.GetDataSource(r.Context(), t.ID, ds.ID)
	writeJSON(w, http.StatusOK, s.toDTO(*updated))
}

func (s *Server) handleDataSourceDelete(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can delete data sources")
		return
	}
	ds, err := s.store.GetDataSource(r.Context(), t.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Data source not found")
		return
	}
	if err := s.store.DeleteDataSource(r.Context(), t.ID, ds.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "datasource.delete", TargetType: "datasource", TargetID: ds.ID, Metadata: map[string]any{"name": ds.Name}, IP: clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDataSourceTestConfig(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can test data sources")
		return
	}
	var body struct {
		Type         string            `json:"type"`
		Config       map[string]string `json:"config"`
		DataSourceID string            `json:"datasourceId"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if body.Config == nil {
		body.Config = map[string]string{}
	}
	typ := body.Type
	if body.DataSourceID != "" {
		ds, err := s.store.GetDataSource(r.Context(), t.ID, body.DataSourceID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "Data source not found")
			return
		}
		typ = ds.Type
		oldCfg, err := s.decryptConfig(ds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "Could not decrypt stored credentials")
			return
		}
		spec, ok := connector.Get(typ)
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown_type", "Connector type is no longer available")
			return
		}
		body.Config = connector.MergeSecrets(spec.Spec(), oldCfg, body.Config)
	}
	spec, ok := connector.Get(typ)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_type", "Unknown data source type")
		return
	}
	if err := connector.ValidateConfig(spec.Spec(), body.Config); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	c, err := connector.Open(ctx, typ, body.Config)
	if err == nil {
		err = c.Ping(ctx)
		_ = c.Close()
	}
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "latencyMs": latency})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "latencyMs": latency})
}

func (s *Server) handleDataSourceTest(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can test data sources")
		return
	}
	ds, err := s.store.GetDataSource(r.Context(), t.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Data source not found")
		return
	}
	cfg, err := s.decryptConfig(ds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Could not decrypt stored credentials")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	c, err := connector.Open(ctx, ds.Type, cfg)
	if err == nil {
		err = c.Ping(ctx)
		_ = c.Close()
	}
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "latencyMs": latency})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "latencyMs": latency})
}

func (s *Server) handleDataSourceRefresh(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can refresh schemas")
		return
	}
	ds, err := s.store.GetDataSource(r.Context(), t.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Data source not found")
		return
	}
	cfg, err := s.decryptConfig(ds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Could not decrypt stored credentials")
		return
	}
	tables, syncErr := s.introspectConfig(r.Context(), ds.Type, cfg)
	status, lastError := "ready", ""
	if syncErr != nil {
		status, lastError = "error", syncErr.Error()
	}
	if err := s.store.UpdateDataSourceStatus(r.Context(), t.ID, ds.ID, tables, status, lastError); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "datasource.refresh", TargetType: "datasource", TargetID: ds.ID, Metadata: map[string]any{"status": status}, IP: clientIP(r)})
	updated, _ := s.store.GetDataSource(r.Context(), t.ID, ds.ID)
	writeJSON(w, http.StatusOK, s.toDTO(*updated))
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListDataSources(r.Context(), currentTenant(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	type source struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Status string            `json:"status"`
		Error  string            `json:"error,omitempty"`
		Tables []connector.Table `json:"tables"`
	}
	out := make([]source, 0, len(list))
	for _, ds := range list {
		tables := make([]connector.Table, len(ds.Tables))
		for i, t := range ds.Tables {
			if t.Columns == nil {
				t.Columns = []connector.Column{}
			}
			tables[i] = t
		}
		out = append(out, source{Name: ds.Name, Type: ds.Type, Status: ds.Status, Error: ds.LastError, Tables: tables})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": out})
}

func (s *Server) introspectConfig(ctx context.Context, typ string, cfg map[string]string) ([]connector.Table, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	c, err := connector.Open(ctx, typ, cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Introspect(ctx)
}

var _ = errors.Is
var _ = store.ErrNotFound
