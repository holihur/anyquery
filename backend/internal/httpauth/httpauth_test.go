package httpauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"auth_type": "client_credentials", "token_url": "https://auth.example.com/token",
		"client_id": "id", "client_secret": "secret", "scope": "read, data",
	})
	if err != nil || !cfg.Enabled() || len(NewProvider(cfg).scopes) != 2 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if _, err := ParseConfig(map[string]string{"auth_type": "client_credentials"}); err == nil {
		t.Fatal("expected missing token_url error")
	}
	if p := NewProvider(Config{}); p != nil {
		t.Fatal("disabled auth should return nil provider")
	}
}

func TestAuthorizationCodeURLAndExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "code-1" || r.Form.Get("code_verifier") != "verifier-1" {
			http.Error(w, "bad exchange", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer srv.Close()
	cfg, err := ParseConfig(map[string]string{
		"auth_type": "authorization_code", "token_url": srv.URL,
		"client_id": "client", "client_secret": "secret", "scope": "openid offline_access",
	})
	if err != nil {
		t.Fatal(err)
	}
	authURL := cfg.AuthorizationURL("https://id.example.com/authorize", "http://localhost/callback", "state-1", "verifier-1")
	if !strings.Contains(authURL, "code_challenge_method=S256") || !strings.Contains(authURL, "state=state-1") || !strings.Contains(authURL, "access_type=offline") {
		t.Fatalf("auth URL = %s", authURL)
	}
	tok, err := ExchangeCode(context.Background(), cfg, "code-1", "verifier-1", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access" || tok.RefreshToken != "refresh" {
		t.Fatalf("token = %+v", tok)
	}
}
