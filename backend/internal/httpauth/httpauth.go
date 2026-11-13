// Package httpauth implements optional OAuth2 authentication for HTTP-based
// connectors. It deliberately supports only the two machine-to-machine flows
// that are useful for read-only data sources: client credentials and refresh
// token. Static headers remain the simplest option for API keys.
package httpauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// Config is parsed from connector configuration. Empty Type means no OAuth.
type Config struct {
	Type         string
	TokenURL     string
	ClientID     string
	ClientSecret string
	RefreshToken string
	Scope        string
}

// Enabled reports whether an OAuth2 flow is configured.
func (c Config) Enabled() bool { return c.Type != "" && c.Type != "none" }

// ParseConfig reads the shared OAuth fields used by HTTP connectors.
func ParseConfig(cfg map[string]string) (Config, error) {
	c := Config{
		Type:         strings.ToLower(strings.TrimSpace(cfg["auth_type"])),
		TokenURL:     strings.TrimSpace(cfg["token_url"]),
		ClientID:     strings.TrimSpace(cfg["client_id"]),
		ClientSecret: cfg["client_secret"],
		RefreshToken: cfg["refresh_token"],
		Scope:        strings.TrimSpace(cfg["scope"]),
	}
	if !c.Enabled() {
		return Config{}, nil
	}
	if c.Type != "client_credentials" && c.Type != "refresh_token" {
		return Config{}, fmt.Errorf("auth_type must be none, client_credentials or refresh_token")
	}
	if err := validateTokenURL(c.TokenURL); err != nil {
		return Config{}, err
	}
	if c.ClientID == "" {
		return Config{}, fmt.Errorf("client_id is required for OAuth2")
	}
	if c.Type == "client_credentials" && c.ClientSecret == "" {
		return Config{}, fmt.Errorf("client_secret is required for client_credentials")
	}
	if c.Type == "refresh_token" && c.RefreshToken == "" {
		return Config{}, fmt.Errorf("refresh_token is required for auth_type=refresh_token")
	}
	return c, nil
}

func validateTokenURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid token_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("token_url must start with http:// or https://")
	}
	return nil
}

// Provider obtains and caches OAuth2 access tokens for HTTP requests.
type Provider struct {
	cfg    Config
	client *http.Client
	scopes []string

	mu    sync.Mutex
	token *oauth2.Token
}

// NewProvider returns nil for an unconfigured auth block.
func NewProvider(cfg Config) *Provider {
	if !cfg.Enabled() {
		return nil
	}
	var scopes []string
	for _, s := range strings.FieldsFunc(cfg.Scope, func(r rune) bool { return r == ',' || r == ' ' }) {
		if s = strings.TrimSpace(s); s != "" {
			scopes = append(scopes, s)
		}
	}
	return &Provider{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
		scopes: scopes,
	}
}

// Authorization returns a Bearer header value, refreshing the token when it is
// close to expiry.
func (p *Provider) Authorization(ctx context.Context) (string, error) {
	if p == nil {
		return "", nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != nil {
		if p.token.Expiry.IsZero() || time.Until(p.token.Expiry) > 30*time.Second {
			return "Bearer " + p.token.AccessToken, nil
		}
	}
	tok, err := p.fetch(ctx)
	if err != nil {
		return "", err
	}
	p.token = tok
	return "Bearer " + tok.AccessToken, nil
}

// Invalidate drops the cached token after a 401 so the next request refreshes.
func (p *Provider) Invalidate() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.token = nil
	p.mu.Unlock()
}

func (p *Provider) fetch(ctx context.Context) (*oauth2.Token, error) {
	switch p.cfg.Type {
	case "client_credentials":
		cc := clientcredentials.Config{
			ClientID:     p.cfg.ClientID,
			ClientSecret: p.cfg.ClientSecret,
			TokenURL:     p.cfg.TokenURL,
			Scopes:       p.scopes,
			AuthStyle:    oauth2.AuthStyleAutoDetect,
		}
		ctx = context.WithValue(ctx, oauth2.HTTPClient, p.client)
		return cc.Token(ctx)
	case "refresh_token":
		conf := &oauth2.Config{
			ClientID:     p.cfg.ClientID,
			ClientSecret: p.cfg.ClientSecret,
			Endpoint:     oauth2.Endpoint{TokenURL: p.cfg.TokenURL, AuthStyle: oauth2.AuthStyleAutoDetect},
			Scopes:       p.scopes,
		}
		ctx = context.WithValue(ctx, oauth2.HTTPClient, p.client)
		seed := &oauth2.Token{RefreshToken: p.cfg.RefreshToken}
		return conf.TokenSource(ctx, seed).Token()
	default:
		return nil, fmt.Errorf("unsupported auth_type %q", p.cfg.Type)
	}
}
