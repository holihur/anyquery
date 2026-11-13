package api

import (
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/cryptox"
)

func TestDataSourceOAuthStateRoundTrip(t *testing.T) {
	key, err := cryptox.NewKey([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{key: key}
	want := dataSourceOAuthState{
		TenantID: "t1", TenantSlug: "acme", DataSourceID: "d1",
		Verifier: "verifier", RedirectURI: "http://localhost/callback",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	raw, err := s.encodeDataSourceOAuthState(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.decodeDataSourceOAuthState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.TenantID != want.TenantID || got.DataSourceID != want.DataSourceID || got.Verifier != want.Verifier {
		t.Fatalf("state = %+v", got)
	}
	if _, err := s.decodeDataSourceOAuthState("not-valid"); err == nil {
		t.Fatal("expected invalid state error")
	}
}
