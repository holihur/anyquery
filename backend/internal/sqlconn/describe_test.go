package sqlconn

import (
	"strings"
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

func TestDescribeFetch(t *testing.T) {
	c := &Conn{dialect: Dialect{
		Type:     "postgres",
		Quote:    func(s string) string { return `"` + s + `"` },
		LimitSQL: func(n int) string { return " LIMIT " + itoaTest(n) },
	}}
	stmt, args, err := c.DescribeFetch(connector.FetchRequest{
		Table:      "public.orders",
		Columns:    []string{"id", "total"},
		Limit:      25,
		Predicates: []connector.Predicate{{Column: "status", Op: "eq", Value: "paid"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id", "total" FROM "public"."orders" WHERE "status" = $1 LIMIT 25`
	if stmt != want {
		t.Fatalf("statement = %q, want %q", stmt, want)
	}
	if len(args) != 1 || args[0] != "paid" {
		t.Fatalf("args = %#v", args)
	}
	if strings.Contains(stmt, "?") {
		t.Fatalf("unexpected placeholder: %s", stmt)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestDescribeTemporalFetch(t *testing.T) {
	c := &Conn{dialect: Dialect{
		Type:       "sqlserver",
		Quote:      quoteBracket,
		LimitSQL:   limitTop,
		SystemTime: true,
	}}
	asOf := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	stmt, args, err := c.DescribeFetch(connector.FetchRequest{
		Table:    "dbo.orders",
		Limit:    10,
		Temporal: &connector.TemporalRange{Mode: "as_of", AsOf: asOf},
		Predicates: []connector.Predicate{
			{Column: "status", Op: "eq", Value: "paid"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT TOP (10) * FROM [dbo].[orders] FOR SYSTEM_TIME AS OF @p1 WHERE [status] = @p2`
	if stmt != want {
		t.Fatalf("statement = %q, want %q", stmt, want)
	}
	if len(args) != 2 || args[0] != asOf || args[1] != "paid" {
		t.Fatalf("args = %#v", args)
	}
}
