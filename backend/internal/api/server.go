package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anyquery/anyquery/internal/auth"
	"github.com/anyquery/anyquery/internal/config"
	"github.com/anyquery/anyquery/internal/cryptox"
	"github.com/anyquery/anyquery/internal/engine"
	"github.com/anyquery/anyquery/internal/model"
	"github.com/anyquery/anyquery/internal/store"
)

type Server struct {
	cfg    config.Config
	store  *store.Store
	auth   *auth.Service
	engine *engine.Engine
	key    *cryptox.Key
}

func New(cfg config.Config, st *store.Store, authSvc *auth.Service) (*Server, error) {
	key, err := cryptox.NewKey(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg: cfg, store: st, auth: authSvc, key: key,
		engine: engine.New(engine.Options{
			MaxSourceRows: cfg.MaxSourceRows,
			MaxResultRows: cfg.MaxResultRows,
			MaxJoinRows:   2_000_000,
			Timeout:       cfg.QueryTimeout,
		}),
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/healthz", s.handleHealth)
	mux.HandleFunc("POST /mcp", s.authenticatePAT(s.handleMCP))
	mux.HandleFunc("GET /api/v1/auth/config", s.handleAuthConfig)
	mux.HandleFunc("GET /api/v1/auth/oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("GET /api/v1/auth/oidc/callback", s.handleOIDCCallback)
	mux.HandleFunc("POST /api/v1/auth/dev-login", s.handleDevLogin)
	mux.HandleFunc("POST /api/v1/auth/password-login", s.handlePasswordLogin)

	protected := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, s.authenticate(h))
	}
	protected("GET /api/v1/auth/me", s.handleMe)
	protected("POST /api/v1/auth/logout", s.handleLogout)
	protected("POST /api/v1/tenants", s.handleCreateTenant)
	protected("POST /api/v1/invites/accept", s.handleAcceptInvite)
	mux.HandleFunc("POST /api/v1/invites/accept-password", s.handleAcceptInvitePassword)
	protected("GET /api/v1/datasource-types", s.handleDataSourceTypes)

	tenant := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, s.authenticate(s.requireTenant(h)))
	}
	tenant("GET /api/v1/tenants/{tenant}", s.handleTenantGet)
	tenant("PATCH /api/v1/tenants/{tenant}", s.handleTenantUpdate)
	tenant("GET /api/v1/tenants/{tenant}/overview", s.handleOverview)

	tenant("GET /api/v1/tenants/{tenant}/members", s.handleMembersList)
	tenant("PATCH /api/v1/tenants/{tenant}/members/{userID}", s.handleMemberUpdate)
	tenant("DELETE /api/v1/tenants/{tenant}/members/{userID}", s.handleMemberDelete)
	tenant("POST /api/v1/tenants/{tenant}/members/{userID}/password", s.handleMemberPasswordReset)
	tenant("GET /api/v1/tenants/{tenant}/invites", s.handleInvitesList)
	tenant("POST /api/v1/tenants/{tenant}/invites", s.handleInviteCreate)
	tenant("DELETE /api/v1/tenants/{tenant}/invites/{id}", s.handleInviteDelete)

	tenant("GET /api/v1/tenants/{tenant}/datasources", s.handleDataSourcesList)
	tenant("POST /api/v1/tenants/{tenant}/datasources", s.handleDataSourceCreate)
	tenant("POST /api/v1/tenants/{tenant}/datasources/test", s.handleDataSourceTestConfig)
	tenant("GET /api/v1/tenants/{tenant}/datasources/{id}", s.handleDataSourceGet)
	tenant("PATCH /api/v1/tenants/{tenant}/datasources/{id}", s.handleDataSourceUpdate)
	tenant("DELETE /api/v1/tenants/{tenant}/datasources/{id}", s.handleDataSourceDelete)
	tenant("POST /api/v1/tenants/{tenant}/datasources/{id}/test", s.handleDataSourceTest)
	tenant("POST /api/v1/tenants/{tenant}/datasources/{id}/refresh", s.handleDataSourceRefresh)
	tenant("GET /api/v1/tenants/{tenant}/catalog", s.handleCatalog)

	tenant("POST /api/v1/tenants/{tenant}/queries/execute", s.handleExecute)
	tenant("POST /api/v1/tenants/{tenant}/queries/explain", s.handleExplain)
	tenant("GET /api/v1/tenants/{tenant}/queries", s.handleSavedQueriesList)
	tenant("POST /api/v1/tenants/{tenant}/queries", s.handleSavedQueryCreate)
	tenant("GET /api/v1/tenants/{tenant}/queries/{id}", s.handleSavedQueryGet)
	tenant("PATCH /api/v1/tenants/{tenant}/queries/{id}", s.handleSavedQueryUpdate)
	tenant("DELETE /api/v1/tenants/{tenant}/queries/{id}", s.handleSavedQueryDelete)
	tenant("GET /api/v1/tenants/{tenant}/runs", s.handleRunsList)
	tenant("GET /api/v1/tenants/{tenant}/audit", s.handleAuditList)

	tenant("GET /api/v1/tenants/{tenant}/apikeys", s.handleAPIKeysList)
	tenant("POST /api/v1/tenants/{tenant}/apikeys", s.handleAPIKeyCreate)
	tenant("DELETE /api/v1/tenants/{tenant}/apikeys/{id}", s.handleAPIKeyDelete)

	// SPA / fallback.
	mux.Handle("/", s.spaHandler())

	var h http.Handler = mux
	h = s.compress(h)
	h = s.cors(h)
	h = s.securityHeaders(h)
	h = s.recoverer(h)
	h = s.logger(h)
	return h
}

// ---- middleware ------------------------------------------------------------

type ctxKey int

const (
	principalKey ctxKey = iota
	tenantKey
	roleKey
	apiKeyAuthKey
)

type principal struct {
	User    *model.User
	Session *model.Session
	APIKey  *model.APIKey
}

func (s *Server) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// API key bearer takes precedence.
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			raw := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
			if strings.HasPrefix(raw, "aq_") {
				k, err := s.store.GetAPIKeyByHash(ctx, cryptox.HashToken(raw))
				if err != nil {
					writeError(w, http.StatusUnauthorized, "invalid_api_key", "API key is invalid or revoked")
					return
				}
				user, err := s.store.GetUser(ctx, k.CreatedBy)
				if err != nil {
					writeError(w, http.StatusUnauthorized, "invalid_api_key", "API key owner no longer exists")
					return
				}
				s.store.TouchAPIKey(ctx, k.ID)
				ctx = context.WithValue(ctx, principalKey, &principal{User: user, APIKey: k})
				next(w, r.WithContext(ctx))
				return
			}
		}
		user, sess, err := s.auth.SessionUser(ctx, r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to continue")
			return
		}
		if isUnsafe(r.Method) {
			if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowedForRequest(origin, r) {
				writeError(w, http.StatusForbidden, "csrf", fmt.Sprintf("Request origin %q is not allowed for host %q. Set APP_BASE_URL to the public URL, or add the origin to APP_ALLOWED_ORIGINS.", origin, r.Host))
				return
			}
		}
		ctx = context.WithValue(ctx, principalKey, &principal{User: user, Session: sess})
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) requireTenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := currentPrincipal(r)
		if p == nil || p.User == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to continue")
			return
		}
		ref := r.PathValue("tenant")
		tenant, err := s.store.GetTenant(r.Context(), ref)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "Workspace not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		var role model.Role
		if p.APIKey != nil {
			if p.APIKey.TenantID != tenant.ID {
				writeError(w, http.StatusForbidden, "forbidden", "This API key belongs to another workspace")
				return
			}
			role = model.RoleMember
		} else {
			m, err := s.store.GetMembership(r.Context(), tenant.ID, p.User.ID)
			if err != nil {
				writeError(w, http.StatusForbidden, "forbidden", "You are not a member of this workspace")
				return
			}
			role = m.Role
		}
		ctx := context.WithValue(r.Context(), tenantKey, tenant)
		ctx = context.WithValue(ctx, roleKey, role)
		next(w, r.WithContext(ctx))
	}
}

func currentPrincipal(r *http.Request) *principal {
	p, _ := r.Context().Value(principalKey).(*principal)
	return p
}

func currentUser(r *http.Request) *model.User {
	if p := currentPrincipal(r); p != nil {
		return p.User
	}
	return nil
}

func currentTenant(r *http.Request) *model.Tenant {
	t, _ := r.Context().Value(tenantKey).(*model.Tenant)
	return t
}

func currentRole(r *http.Request) model.Role {
	role, _ := r.Context().Value(roleKey).(model.Role)
	return role
}

func isAPIKey(r *http.Request) bool {
	p := currentPrincipal(r)
	return p != nil && p.APIKey != nil
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowedForRequest(origin, r) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			if origin != "" && !s.originAllowedForRequest(origin, r) {
				writeError(w, http.StatusForbidden, "csrf", "Origin is not allowed")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originAllowed(origin string) bool {
	for _, o := range s.cfg.AllowedOrigins {
		if strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

// originAllowedForRequest accepts explicitly configured origins and the
// browser's own origin when it matches the host serving this request. This
// keeps cookie-auth CSRF protection working after changing ports or putting
// the app behind a same-origin reverse proxy without silently allowing every
// origin.
func (s *Server) originAllowedForRequest(origin string, r *http.Request) bool {
	if s.originAllowed(origin) {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if !strings.EqualFold(u.Host, host) {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = strings.ToLower(strings.TrimSpace(strings.Split(forwarded, ",")[0]))
	}
	return strings.EqualFold(u.Scheme, scheme)
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				writeError(w, http.StatusInternalServerError, "internal", "Unexpected server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *Server) logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

// ---- response helpers ------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": apiError{Code: code, Message: message}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": "AnyQuery", "time": store.Now()})
}

// ---- SPA -------------------------------------------------------------------

func (s *Server) spaHandler() http.Handler {
	webDir := s.cfg.WebDir
	index := filepath.Join(webDir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusNotFound, "not_found", "Not found")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not_found", "API route not found")
			return
		}
		if _, err := os.Stat(index); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"name": "AnyQuery", "message": "API is running. Build the frontend or use the Vite dev server.",
				"health": "/api/v1/healthz",
			})
			return
		}
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." {
			clean = "index.html"
		}
		candidate := filepath.Join(webDir, clean)
		if rel, err := filepath.Rel(webDir, candidate); err != nil || strings.HasPrefix(rel, "..") {
			http.NotFound(w, r)
			return
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/favicon.svg" {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFile(w, r, candidate)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}
