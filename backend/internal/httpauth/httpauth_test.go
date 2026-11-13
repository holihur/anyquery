package httpauth

import "testing"

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
