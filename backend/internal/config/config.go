package config

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the server configuration, loaded from environment variables.
type Config struct {
	Addr           string
	BaseURL        string
	WebDir         string
	MongoURI       string
	MongoDatabase  string
	DevAuth        bool
	SessionTTL     time.Duration
	QueryTimeout   time.Duration
	MaxSourceRows  int
	MaxResultRows  int
	EncryptionKey  []byte
	AllowedOrigins []string
	CookieSecure   bool

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string
	OIDCScopes       []string
}

func (c Config) OIDCEnabled() bool {
	return c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCClientSecret != ""
}

// Load reads configuration from the environment.
func Load() (Config, error) {
	c := Config{
		Addr:             env("PORT", "8080"),
		BaseURL:          strings.TrimRight(env("APP_BASE_URL", "http://localhost:8080"), "/"),
		WebDir:           os.Getenv("WEB_DIR"),
		MongoURI:         env("MONGODB_URI", "mongodb://localhost:27017"),
		MongoDatabase:    env("MONGODB_DB", "anyquery"),
		DevAuth:          envBool("AUTH_DEV_MODE", true),
		SessionTTL:       time.Duration(envInt("SESSION_TTL_HOURS", 168)) * time.Hour,
		QueryTimeout:     time.Duration(envInt("QUERY_TIMEOUT_SECONDS", 30)) * time.Second,
		MaxSourceRows:    envInt("QUERY_MAX_SOURCE_ROWS", 100000),
		MaxResultRows:    envInt("QUERY_MAX_RESULT_ROWS", 10000),
		OIDCIssuer:       strings.TrimRight(os.Getenv("OIDC_ISSUER"), "/"),
		OIDCClientID:     os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:  os.Getenv("OIDC_REDIRECT_URL"),
		OIDCScopes:       splitCSV(env("OIDC_SCOPES", "openid,profile,email")),
	}
	if c.WebDir == "" {
		for _, candidate := range []string{"frontend/dist", "../frontend/dist", "web", "../web"} {
			if _, err := os.Stat(candidate + "/index.html"); err == nil {
				c.WebDir = candidate
				break
			}
		}
		if c.WebDir == "" {
			c.WebDir = "frontend/dist"
		}
	}
	if c.Addr != "" && !strings.Contains(c.Addr, ":") {
		c.Addr = ":" + c.Addr
	}
	if c.OIDCRedirectURL == "" {
		c.OIDCRedirectURL = c.BaseURL + "/api/v1/auth/oidc/callback"
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return c, fmt.Errorf("APP_BASE_URL must be an absolute URL, got %q", c.BaseURL)
	}
	c.CookieSecure = u.Scheme == "https"
	if v := os.Getenv("COOKIE_SECURE"); v != "" {
		c.CookieSecure = parseBool(v, c.CookieSecure)
	}

	keyRaw := os.Getenv("DATA_ENCRYPTION_KEY")
	if keyRaw == "" {
		if !c.DevAuth {
			return c, fmt.Errorf("DATA_ENCRYPTION_KEY is required outside development mode")
		}
		sum := sha256.Sum256([]byte("anyquery-development-encryption-key"))
		c.EncryptionKey = sum[:]
	} else {
		key, err := base64.StdEncoding.DecodeString(keyRaw)
		if err != nil {
			return c, fmt.Errorf("DATA_ENCRYPTION_KEY must be base64: %w", err)
		}
		if len(key) != 32 {
			return c, fmt.Errorf("DATA_ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
		}
		c.EncryptionKey = key
	}

	origins := splitCSV(os.Getenv("APP_ALLOWED_ORIGINS"))
	origins = append(origins, u.Scheme+"://"+u.Host)
	if c.DevAuth {
		origins = append(origins, "http://localhost:5173", "http://127.0.0.1:5173")
	}
	c.AllowedOrigins = unique(origins)
	return c, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return parseBool(v, def)
}

func parseBool(v string, def bool) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func splitCSV(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
