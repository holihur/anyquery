package sql

import (
	"strings"
	"testing"
)

func TestParseSelectBasic(t *testing.T) {
	q := `SELECT o.region, count(*) AS orders, sum(o.total) revenue
FROM mongo.orders o
WHERE o.status = 'paid' AND o.total >= 10
GROUP BY o.region
HAVING count(*) > 2
ORDER BY revenue DESC
LIMIT 10 OFFSET 5`
	stmt, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := stmt.(*SelectStmt)
	if !ok {
		t.Fatalf("got %T", stmt)
	}
	if len(s.Projections) != 3 || s.Projections[1].Alias != "orders" {
		t.Fatalf("unexpected projections: %#v", s.Projections)
	}
	if len(s.From) != 1 {
		t.Fatalf("from: %#v", s.From)
	}
	ref, ok := s.From[0].(*TableRef)
	if !ok || ref.Source != "mongo" || ref.Table != "orders" || ref.Alias != "o" {
		t.Fatalf("table ref: %#v", s.From[0])
	}
	if s.Limit == nil || *s.Limit != 10 || s.Offset == nil || *s.Offset != 5 {
		t.Fatalf("limit/offset wrong")
	}
}

func TestParseJoinAndPipe(t *testing.T) {
	q := "FROM mongo.orders o |> WHERE o.status = 'paid' |> LEFT JOIN shop.products p ON o.product_id = p.id |> AGGREGATE sum(o.total) AS revenue, count(*) AS n GROUP BY p.category |> ORDER BY revenue DESC |> LIMIT 5"
	stmt, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(*SelectStmt)
	if len(s.Pipes) != 5 {
		t.Fatalf("expected 5 pipe stages, got %d: %s", len(s.Pipes), FormatSelect(s))
	}
	if _, ok := s.Pipes[1].(*PipeJoin); !ok {
		t.Fatalf("expected join, got %T", s.Pipes[1])
	}
	round := FormatSelect(s)
	if !strings.Contains(round, "|> AGGREGATE") {
		t.Fatalf("round trip: %s", round)
	}
}

func TestParsePipelineAfterSelect(t *testing.T) {
	q := "SELECT region, total FROM mongo.orders |> WHERE total > 100 |> SELECT region, total * 1.2 AS gross |> DISTINCT |> ORDER BY gross DESC"
	stmt, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(*SelectStmt)
	if len(s.Pipes) != 4 {
		t.Fatalf("pipes = %d", len(s.Pipes))
	}
}

func TestParseExpressions(t *testing.T) {
	cases := []string{
		"a + b * 2 >= 10",
		"name ILIKE '%acme%'",
		"x IN (1, 2, 3)",
		"created_at BETWEEN '2024-01-01' AND '2024-12-31'",
		"deleted_at IS NOT NULL",
		"CASE WHEN total > 100 THEN 'big' ELSE 'small' END",
		"CAST(total AS DECIMAL(10,2))",
		"COALESCE(region, 'unknown')",
		"EXISTS(SELECT 1 FROM mongo.users u WHERE u.id = o.user_id)",
	}
	for _, expr := range cases {
		if _, err := Parse("SELECT " + expr + " FROM mongo.t"); err != nil {
			t.Errorf("%s: %v", expr, err)
		}
	}
}

func TestParseCountDistinct(t *testing.T) {
	stmt, err := Parse("SELECT count(DISTINCT user_id) AS users FROM mongo.orders")
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(*SelectStmt)
	f := s.Projections[0].Expr.(*FuncCall)
	if !f.Distinct {
		t.Fatal("expected DISTINCT aggregate")
	}
}

func TestParseQualifiedNames(t *testing.T) {
	stmt, err := Parse("SELECT u.name FROM postgres.public.users u")
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(*SelectStmt)
	ref := s.From[0].(*TableRef)
	if ref.Source != "postgres" || ref.Table != "public.users" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParseQuotedIdentifiers(t *testing.T) {
	stmt, err := Parse("SELECT `weird.col` FROM `my db`.`collection`")
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(*SelectStmt)
	ref := s.From[0].(*TableRef)
	if ref.Source != "my db" || ref.Table != "collection" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParseShowDescribe(t *testing.T) {
	if _, err := Parse("SHOW TABLES FROM mongo"); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("DESCRIBE mongo.orders"); err != nil {
		t.Fatal(err)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []string{
		"SELECT",
		"SELECT * FROM",
		"SELECT * FROM mongo.orders WHERE",
		"SELECT * FROM mongo.orders LIMIT x",
		"SELECT * FROM mongo.orders WHERE a IN ()",
	}
	for _, q := range cases {
		if _, err := Parse(q); err == nil {
			t.Errorf("expected error for %q", q)
		}
	}
}
