package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/anyquery/anyquery/internal/auth"
	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/cryptox"
	"github.com/anyquery/anyquery/internal/model"
	"github.com/anyquery/anyquery/internal/store"
)

type tenantRole struct {
	model.Tenant
	Role model.Role `json:"role"`
}

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.auth.PublicConfig())
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.OIDCEnabled() {
		writeError(w, http.StatusNotFound, "oidc_disabled", "OIDC login is not configured")
		return
	}
	returnTo := r.URL.Query().Get("return_to")
	u, err := s.auth.LoginURL(r.Context(), returnTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "oidc_error", err.Error())
		return
	}
	http.Redirect(w, r, u, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if e := r.URL.Query().Get("error"); e != "" {
		http.Redirect(w, r, s.cfg.BaseURL+"/?auth_error="+url.QueryEscape(e), http.StatusFound)
		return
	}
	user, returnTo, err := s.auth.Callback(r.Context(), r.URL.Query().Get("code"), r.URL.Query().Get("state"))
	if err != nil {
		http.Redirect(w, r, s.cfg.BaseURL+"/?auth_error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	if err := s.auth.CreateSession(r.Context(), w, user.ID); err != nil {
		http.Redirect(w, r, s.cfg.BaseURL+"/?auth_error="+url.QueryEscape("could not create session"), http.StatusFound)
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{
		TenantID: "", ActorID: user.ID, ActorName: user.Name, Action: "auth.login",
		IP: clientIP(r), Metadata: map[string]any{"method": "oidc"},
	})
	if !strings.HasPrefix(returnTo, "/") {
		returnTo = "/"
	}
	http.Redirect(w, r, s.cfg.BaseURL+returnTo, http.StatusFound)
}

func (s *Server) handlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.PasswordAuth {
		writeError(w, http.StatusNotFound, "password_auth_disabled", "Password login is disabled")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	user, err := s.auth.PasswordLogin(r.Context(), w, body.Email, body.Password, clientIP(r))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRateLimited):
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many sign-in attempts. Try again in 15 minutes.")
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password.")
		default:
			writeError(w, http.StatusInternalServerError, "login_failed", err.Error())
		}
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{
		TenantID: "", ActorID: user.ID, ActorName: user.Name, Action: "auth.login",
		IP: clientIP(r), Metadata: map[string]any{"method": "password"},
	})
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DevAuth {
		writeError(w, http.StatusNotFound, "dev_auth_disabled", "Development login is disabled")
		return
	}
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	user, err := s.auth.DevLogin(r.Context(), w, body.Email, body.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "login_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p == nil || p.User == nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to continue")
		return
	}
	tenants, err := s.store.ListTenantsForUser(r.Context(), p.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]tenantRole, 0, len(tenants))
	for _, tm := range tenants {
		if p.APIKey != nil && tm.Tenant.ID != p.APIKey.TenantID {
			continue
		}
		out = append(out, tenantRole{Tenant: tm.Tenant, Role: tm.Role})
	}
	authType := "session"
	if p.APIKey != nil {
		authType = "api_key"
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": p.User, "tenants": out, "authType": authType})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) {
		writeError(w, http.StatusBadRequest, "api_key_session", "API keys cannot create browser sessions")
		return
	}
	s.auth.ClearSession(r.Context(), w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) {
		writeError(w, http.StatusForbidden, "forbidden", "API keys cannot create workspaces")
		return
	}
	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	user := currentUser(r)
	t, err := s.store.CreateTenant(r.Context(), body.Name, body.Slug, user.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{
		TenantID: t.ID, ActorID: user.ID, ActorName: user.Name, Action: "workspace.create",
		TargetType: "workspace", TargetID: t.ID, Metadata: map[string]any{"name": t.Name}, IP: clientIP(r),
	})
	writeJSON(w, http.StatusCreated, tenantRole{Tenant: *t, Role: model.RoleOwner})
}

func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	if isAPIKey(r) {
		writeError(w, http.StatusForbidden, "forbidden", "Sign in to accept an invitation")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &body); err != nil || strings.TrimSpace(body.Token) == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "invitation token is required")
		return
	}
	inv, err := s.store.GetInviteByToken(r.Context(), cryptox.HashToken(body.Token))
	if err != nil {
		writeError(w, http.StatusNotFound, "invite_not_found", "This invitation is invalid or has expired")
		return
	}
	if store.Now().After(inv.ExpiresAt) {
		writeError(w, http.StatusGone, "invite_expired", "This invitation has expired")
		return
	}
	user := currentUser(r)
	if !strings.EqualFold(user.Email, inv.Email) {
		writeError(w, http.StatusForbidden, "invite_email_mismatch", "Sign in with "+inv.Email+" to accept this invitation")
		return
	}
	if err := s.store.UpsertMember(r.Context(), inv.TenantID, user.ID, inv.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	_ = s.store.MarkInviteAccepted(r.Context(), inv.ID)
	tenant, err := s.store.GetTenant(r.Context(), inv.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{
		TenantID: tenant.ID, ActorID: user.ID, ActorName: user.Name, Action: "member.join",
		TargetType: "user", TargetID: user.ID, IP: clientIP(r),
	})
	writeJSON(w, http.StatusOK, tenantRole{Tenant: *tenant, Role: inv.Role})
}

func (s *Server) handleAcceptInvitePassword(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.PasswordAuth {
		writeError(w, http.StatusNotFound, "password_auth_disabled", "Password login is disabled")
		return
	}
	var body struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil || strings.TrimSpace(body.Token) == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "invitation token is required")
		return
	}
	inv, err := s.store.GetInviteByToken(r.Context(), cryptox.HashToken(body.Token))
	if err != nil {
		writeError(w, http.StatusNotFound, "invite_not_found", "This invitation is invalid or has expired")
		return
	}
	if store.Now().After(inv.ExpiresAt) {
		writeError(w, http.StatusGone, "invite_expired", "This invitation has expired")
		return
	}
	hash, err := cryptox.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	user, err := s.store.GetUserByEmail(r.Context(), inv.Email)
	if err == nil && user.PasswordHash != "" {
		writeError(w, http.StatusConflict, "account_exists", "An account already exists for this email. Sign in instead.")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		user, err = s.store.CreateLocalUser(r.Context(), inv.Email, body.Name, hash)
	} else if err == nil {
		err = s.store.SetUserPassword(r.Context(), user.ID, hash)
		user.PasswordHash = hash
	}
	if err != nil {
		if errors.Is(err, store.ErrEmailExists) {
			writeError(w, http.StatusConflict, "account_exists", "An account already exists for this email. Sign in instead.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := s.store.UpsertMember(r.Context(), inv.TenantID, user.ID, inv.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	_ = s.store.MarkInviteAccepted(r.Context(), inv.ID)
	tenant, err := s.store.GetTenant(r.Context(), inv.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := s.auth.CreateSession(r.Context(), w, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.store.Audit(r.Context(), model.AuditLog{
		TenantID: tenant.ID, ActorID: user.ID, ActorName: user.Name, Action: "member.join",
		TargetType: "user", TargetID: user.ID, IP: clientIP(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"tenant": tenant, "role": inv.Role, "user": user})
}

func (s *Server) handleDataSourceTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"types": connector.Specs()})
}

func (s *Server) handleTenantGet(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	savedCount, err := s.store.CountSavedQueries(r.Context(), t.ID)
	if err != nil {
		savedCount = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant": t, "role": currentRole(r),
		"limits": map[string]any{
			"maxResultRows": s.cfg.MaxResultRows, "queryTimeoutSeconds": int(s.cfg.QueryTimeout.Seconds()),
			"maxSavedQueries": model.MaxSavedQueriesPerWorkspace,
		},
		"usage": map[string]any{"savedQueries": savedCount},
	})
}

func (s *Server) handleTenantUpdate(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only workspace admins can change settings")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_name", "Workspace name is required")
		return
	}
	if err := s.store.UpdateTenant(r.Context(), t.ID, body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := currentUser(r)
	s.store.Audit(r.Context(), model.AuditLog{TenantID: t.ID, ActorID: u.ID, ActorName: u.Name, Action: "workspace.update", TargetType: "workspace", TargetID: t.ID, IP: clientIP(r)})
	updated, _ := s.store.GetTenant(r.Context(), t.ID)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	ds, _ := s.store.ListDataSources(r.Context(), t.ID)
	queries, _ := s.store.ListSavedQueries(r.Context(), t.ID, currentUser(r).ID, true)
	runs, _ := s.store.ListRuns(r.Context(), t.ID, 8)
	members, _ := s.store.ListMemberships(r.Context(), t.ID)
	ready := 0
	var tables int
	for _, d := range ds {
		if d.Status == "ready" {
			ready++
		}
		tables += len(d.Tables)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dataSources": len(ds), "readyDataSources": ready, "tables": tables,
		"members": len(members), "savedQueries": len(queries), "recentRuns": runs,
	})
}

func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request) {
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Admin access required")
		return
	}
	logs, err := s.store.ListAudit(r.Context(), currentTenant(r).ID, parseLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

var _ = errors.Is
