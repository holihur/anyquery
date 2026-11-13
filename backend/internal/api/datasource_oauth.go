package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/httpauth"
	"github.com/anyquery/anyquery/internal/model"
)

type dataSourceOAuthState struct {
	TenantID     string    `json:"tenantId"`
	TenantSlug   string    `json:"tenantSlug"`
	DataSourceID string    `json:"dataSourceId"`
	ActorID      string    `json:"actorId"`
	ActorName    string    `json:"actorName"`
	Verifier     string    `json:"verifier"`
	RedirectURI  string    `json:"redirectUri"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func (s *Server) encodeDataSourceOAuthState(st dataSourceOAuthState) (string, error) {
	encrypted, err := s.key.EncryptJSON(st)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(encrypted)), nil
}

func (s *Server) decodeDataSourceOAuthState(raw string) (*dataSourceOAuthState, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid OAuth state")
	}
	var st dataSourceOAuthState
	if err := s.key.DecryptJSON(string(decoded), &st); err != nil {
		return nil, fmt.Errorf("invalid OAuth state")
	}
	if st.ExpiresAt.IsZero() || time.Now().After(st.ExpiresAt) {
		return nil, fmt.Errorf("OAuth state expired")
	}
	return &st, nil
}

func (s *Server) handleDataSourceOAuthStart(w http.ResponseWriter, r *http.Request) {
	t := currentTenant(r)
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins can authorize data sources")
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
	if strings.ToLower(strings.TrimSpace(cfg["auth_type"])) != "authorization_code" {
		writeError(w, http.StatusBadRequest, "oauth_not_configured", "Set auth_type to authorization_code first")
		return
	}
	authCfg, err := httpauth.ParseConfig(cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	authURL := strings.TrimSpace(cfg["authorization_url"])
	if authURL == "" {
		writeError(w, http.StatusBadRequest, "invalid_config", "authorization_url is required")
		return
	}
	redirectURI := strings.TrimSpace(cfg["redirect_uri"])
	if redirectURI == "" {
		redirectURI = s.cfg.BaseURL + "/api/v1/datasources/oauth/callback"
	}
	verifier := oauth2.GenerateVerifier()
	user := currentUser(r)
	state, err := s.encodeDataSourceOAuthState(dataSourceOAuthState{
		TenantID: t.ID, TenantSlug: t.Slug, DataSourceID: ds.ID,
		ActorID: user.ID, ActorName: user.Name,
		Verifier: verifier, RedirectURI: redirectURI,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Could not create OAuth state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authorizationUrl": authCfg.AuthorizationURL(authURL, redirectURI, state, verifier),
		"redirectUri":      redirectURI,
	})
}

func (s *Server) handleDataSourceOAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, err := s.decodeDataSourceOAuthState(q.Get("state"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "oauth_state", err.Error())
		return
	}
	if providerErr := q.Get("error"); providerErr != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = providerErr
		}
		s.redirectDataSourceOAuth(w, r, st.TenantSlug, "error", msg)
		return
	}
	code := strings.TrimSpace(q.Get("code"))
	if code == "" {
		s.redirectDataSourceOAuth(w, r, st.TenantSlug, "error", "The provider did not return an authorization code.")
		return
	}
	ds, err := s.store.GetDataSource(r.Context(), st.TenantID, st.DataSourceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Data source not found")
		return
	}
	cfg, err := s.decryptConfig(ds)
	if err != nil {
		s.redirectDataSourceOAuth(w, r, st.TenantSlug, "error", "Could not decrypt data source credentials.")
		return
	}
	authCfg, err := httpauth.ParseConfig(cfg)
	if err != nil {
		s.redirectDataSourceOAuth(w, r, st.TenantSlug, "error", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	token, err := httpauth.ExchangeCode(ctx, authCfg, code, st.Verifier, st.RedirectURI)
	if err != nil {
		s.redirectDataSourceOAuth(w, r, st.TenantSlug, "error", err.Error())
		return
	}
	if token.RefreshToken == "" {
		s.redirectDataSourceOAuth(w, r, st.TenantSlug, "error", "The provider did not return a refresh token. Request offline_access/offline scope and try again.")
		return
	}
	cfg["refresh_token"] = token.RefreshToken
	encrypted, err := s.key.EncryptJSON(cfg)
	if err != nil {
		s.redirectDataSourceOAuth(w, r, st.TenantSlug, "error", "Could not store OAuth token.")
		return
	}
	if err := s.store.UpdateDataSourceConfig(ctx, ds.TenantID, ds.ID, ds.Name, ds.Type, encrypted); err != nil {
		s.redirectDataSourceOAuth(w, r, st.TenantSlug, "error", "Could not save OAuth token.")
		return
	}
	status, lastErr := "ready", ""
	var tables []connector.Table
	// Introspection is best-effort; the token is already stored.
	if tbs, err := s.introspectConfig(ctx, ds.Type, cfg); err != nil {
		status, lastErr = "error", err.Error()
	} else {
		tables = tbs
	}
	_ = s.store.UpdateDataSourceStatus(ctx, ds.TenantID, ds.ID, tables, status, lastErr)
	s.store.Audit(ctx, model.AuditLog{
		TenantID: ds.TenantID, ActorID: st.ActorID, ActorName: st.ActorName,
		Action: "datasource.oauth", TargetType: "datasource", TargetID: ds.ID,
		Metadata: map[string]any{"status": status},
	})
	s.redirectDataSourceOAuth(w, r, st.TenantSlug, "success", "")
}

func (s *Server) redirectDataSourceOAuth(w http.ResponseWriter, r *http.Request, slug, status, message string) {
	base := strings.TrimRight(s.cfg.BaseURL, "/")
	var target string
	if slug != "" {
		target = base + "/w/" + url.PathEscape(slug) + "/sources"
	} else {
		target = base + "/login"
	}
	u, err := url.Parse(target)
	if err != nil {
		http.Redirect(w, r, base, http.StatusFound)
		return
	}
	q := u.Query()
	q.Set("oauth", status)
	if message != "" {
		q.Set("message", message)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}
