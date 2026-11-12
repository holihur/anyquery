package cassandra

import (
	"testing"
	"time"

	"github.com/gocql/gocql"
)

func TestCassandraTypeAndNormalize(t *testing.T) {
	if cqlType("int") != "integer" || cqlType("text") != "string" || cqlType("timestamp") != "timestamp" {
		t.Fatalf("unexpected type mapping")
	}
	u := gocql.MustRandomUUID()
	if got := normalize(u); got != u.String() {
		t.Fatalf("uuid = %#v", got)
	}
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := normalize(now); got != "2024-01-02T03:04:05Z" {
		t.Fatalf("time = %#v", got)
	}
}
