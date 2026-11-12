package api

import (
	"net/http/httptest"
	"testing"

	"github.com/anyquery/anyquery/internal/config"
)

func TestOriginAllowedForRequest(t *testing.T) {
	s := &Server{cfg: config.Config{AllowedOrigins: []string{"https://console.example.com"}}}
	cases := []struct {
		name   string
		origin string
		host   string
		proto  string
		want   bool
	}{
		{"configured", "https://console.example.com", "internal:8080", "https", true},
		{"same origin custom port", "http://localhost:101", "localhost:101", "", true},
		{"same host different scheme", "https://localhost:101", "localhost:101", "", false},
		{"forwarded proto", "https://anyquery.example.com", "internal:8080", "https", true},
		{"unrelated", "http://evil.example.com", "localhost:101", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://"+tc.host+"/api/v1/auth/dev-login", nil)
			r.Host = tc.host
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			// Forwarded host is used for proxied deployments; ensure the Host
			// header test still resolves to the external origin when supplied.
			if tc.name == "forwarded proto" {
				r.Header.Set("X-Forwarded-Host", "anyquery.example.com")
			}
			if got := s.originAllowedForRequest(tc.origin, r); got != tc.want {
				t.Fatalf("originAllowedForRequest() = %v, want %v", got, tc.want)
			}
		})
	}
}
