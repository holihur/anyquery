package all

import (
	"testing"

	"github.com/anyquery/anyquery/internal/connector"
)

func TestTop100ConnectorsRegistered(t *testing.T) {
	types := connector.RegisteredTypes()
	if len(types) < 100 {
		t.Fatalf("registered connectors = %d, want at least 100", len(types))
	}
}
