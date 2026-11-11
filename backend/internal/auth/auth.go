// Package auth implements OIDC login, development login and server-side
// sessions. Sessions are opaque random tokens; only their SHA-256 hashes are
// stored in MongoDB.
package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/anyquery/anyquery/internal/config"
	"github.com/anyquery/anyquery/internal/cryptox"
	"github.com/anyquery/anyquery/internal/model"
	"github.com/anyquery/anyquery/internal/store"
)

const CookieName = "aq_session"

type Service struct {
	cfg      config.Config
	store    *store.Store
	provider *oidc.Provider
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func New(ctx context.Context, cfg config.Config, st *store.Store) (*Service, error) {
	s := &Service{cfg: cfg, store: st}
	if !cfg.OIDCEnabled() {
		return s, nil
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(discoveryCtx, cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery for %s: %w", cfg.OIDCIssuer, err)
	}
	s.provider = provider
	s.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})
	s.oauth = &oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.OIDCRedirectURL,
		Scopes:       cfg.OIDCScopes,
	}
	return s, nil
}

func (s *Service) OIDCEnabled() bool { return s.provider != nil }

// LoginURL starts an OIDC authorization code + PKCE flow.
func (s *Service) LoginURL(ctx context.Context, returnTo string) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("OIDC is not configured")
	}
	if !safeReturnTo(returnTo) {
		returnTo = "/"
	}
	state, err := cryptox.RandomToken(24)
	if err != nil {
		return "", err
	}
	nonce, err := cryptox.RandomToken(24)
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()
	if err := s.store.CreateOAuthState(ctx, model.OAuthState{
		ID: state, Nonce: nonce, Verifier: verifier, ReturnTo: returnTo,
		ExpiresAt: store.Now().Add(10 * time.Minute),
	}); err != nil {
		return "", err
	}
	return s.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
		oauth2.AccessTypeOnline,
	), nil
}

// Callback completes the OIDC flow and returns the linked user and safe
// frontend return path.
func (s *Service) Callback(ctx context.Context, code, state string) (*model.User, string, error) {
	if s.provider == nil {
		return nil, "", fmt.Errorf("OIDC is not configured")
	}
	st, err := s.store.TakeOAuthState(ctx, state)
	if err != nil {
		return nil, "", fmt.Errorf("invalid or expired login state")
	}
	token, err := s.oauth.Exchange(ctx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return nil, "", fmt.Errorf("exchange authorization code: %w", err)
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, "", fmt.Errorf("OIDC provider did not return an id_token")
	}
	idToken, err := s.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, "", fmt.Errorf("verify id_token: %w", err)
	}
	if idToken.Nonce != "" && idToken.Nonce != st.Nonce {
		return nil, "", fmt.Errorf("invalid OIDC nonce")
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, "", err
	}
	email := strings.TrimSpace(stringClaim(claims, "email"))
	verified := boolClaim(claims, "email_verified")
	name := firstNonEmpty(stringClaim(claims, "name"), stringClaim(claims, "preferred_username"), email)
	avatar := firstNonEmpty(stringClaim(claims, "picture"), stringClaim(claims, "avatar_url"))
	user, err := s.store.UpsertOIDCUser(ctx, idToken.Issuer, idToken.Subject, email, name, avatar, verified)
	if err != nil {
		return nil, "", err
	}
	return user, st.ReturnTo, nil
}

// CreateSession stores a new session and sets the cookie.
func (s *Service) CreateSession(ctx context.Context, w http.ResponseWriter, userID string) error {
	token, err := cryptox.RandomToken(32)
	if err != nil {
		return err
	}
	hash := cryptox.HashToken(token)
	if err := s.store.CreateSession(ctx, userID, s.cfg.SessionTTL, hash); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
	return nil
}

func (s *Service) ClearSession(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		_ = s.store.DeleteSession(ctx, cryptox.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// SessionUser reads the current session cookie and returns the authenticated user.
func (s *Service) SessionUser(ctx context.Context, r *http.Request) (*model.User, *model.Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, nil, store.ErrNotFound
	}
	hash := cryptox.HashToken(c.Value)
	sess, err := s.store.GetSession(ctx, hash)
	if err != nil {
		return nil, nil, err
	}
	user, err := s.store.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	if time.Since(sess.LastSeenAt) > 5*time.Minute {
		s.store.TouchSession(ctx, hash)
	}
	return user, sess, nil
}

// DevLogin is available only when AUTH_DEV_MODE is enabled.
func (s *Service) DevLogin(ctx context.Context, w http.ResponseWriter, email, name string) (*model.User, error) {
	if !s.cfg.DevAuth {
		return nil, fmt.Errorf("development login is disabled")
	}
	email = strings.TrimSpace(email)
	if !strings.Contains(email, "@") || len(email) < 3 {
		return nil, fmt.Errorf("a valid email is required")
	}
	user, err := s.store.UpsertDevUser(ctx, email, name)
	if err != nil {
		return nil, err
	}
	if err := s.CreateSession(ctx, w, user.ID); err != nil {
		return nil, err
	}
	return user, nil
}

// AuthConfig is returned to the frontend.
type AuthConfig struct {
	OIDCEnabled  bool   `json:"oidcEnabled"`
	DevAuth      bool   `json:"devAuthEnabled"`
	AppName      string `json:"appName"`
	Issuer       string `json:"issuer,omitempty"`
	SessionHours int    `json:"sessionHours"`
}

func (s *Service) PublicConfig() AuthConfig {
	return AuthConfig{
		OIDCEnabled:  s.OIDCEnabled(),
		DevAuth:      s.cfg.DevAuth,
		AppName:      "AnyQuery",
		Issuer:       s.cfg.OIDCIssuer,
		SessionHours: int(s.cfg.SessionTTL.Hours()),
	}
}

func safeReturnTo(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//")
}

func stringClaim(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolClaim(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
