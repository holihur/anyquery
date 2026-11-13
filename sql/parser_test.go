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

func TestParseModernSQL(t *testing.T) {
	cases := []string{
		"SELECT ALL region FROM mongo.orders",
		"SELECT region FROM mongo.orders UNION ALL SELECT region FROM mongo.customers",
		"SELECT region FROM mongo.orders INTERSECT SELECT region FROM mongo.customers",
		"SELECT region FROM mongo.orders EXCEPT SELECT region FROM mongo.customers",
		"SELECT extract(year FROM created_at) AS y FROM mongo.orders",
		"SELECT substring(name FROM 2 FOR 3) FROM mongo.customers",
		"SELECT position('ac' IN name) FROM mongo.customers",
		"SELECT count(*) FILTER (WHERE status = 'paid') AS n FROM mongo.orders",
		"SELECT region, row_number() OVER (PARTITION BY region ORDER BY total DESC) AS rn FROM mongo.orders",
		"SELECT sum(total) OVER (PARTITION BY region ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) AS s FROM mongo.orders",
		"SELECT region FROM mongo.orders ORDER BY region DESC NULLS LAST FETCH FIRST 2 ROWS ONLY",
		"SELECT * FROM mongo.orders WHERE total > ? AND region = $2",
		"SELECT try_cast(total AS INT), date '2024-01-01', timestamp '2024-01-01 00:00:00' FROM mongo.orders",
		"SELECT region, count(*) FROM mongo.orders GROUP BY ROLLUP(region)",
		"SELECT region, count(*) FROM mongo.orders GROUP BY ROLLUP(region)",
		"SELECT region, count(*) FROM mongo.orders GROUP BY CUBE(region, status)",
		"SELECT region, count(*) FROM mongo.orders GROUP BY GROUPING SETS ((region), (status), ())",
		"SELECT * FROM mongo.orders NATURAL LEFT JOIN mongo.customers",
		"SELECT * FROM (VALUES (1, 'a'), (2, 'b')) AS t(id, name)",
	}
	for _, q := range cases {
		if _, err := Parse(q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}

func TestFormatModernSQL(t *testing.T) {
	queries := []string{
		"SELECT region FROM mongo.orders UNION ALL SELECT region FROM mongo.customers ORDER BY region",
		"SELECT region, row_number() OVER (PARTITION BY region ORDER BY total DESC) AS rn FROM mongo.orders",
		"SELECT count(*) FILTER (WHERE status = 'paid') AS n FROM mongo.orders",
		"WITH RECURSIVE nums(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM nums WHERE n < 3) SELECT * FROM nums",
		"SELECT region, count(*) AS n, grouping(region) AS g FROM mongo.orders GROUP BY ROLLUP(region) ORDER BY g, region",
		"SELECT * FROM mongo.orders NATURAL LEFT JOIN mongo.customers",
		"SELECT * FROM (VALUES (1, 'a'), (2, 'b')) AS t(id, name)",
		"SELECT extract(year FROM created_at) AS y FROM mongo.orders",
	}
	for _, q := range queries {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		s := stmt.(*SelectStmt)
		formatted := FormatSelect(s)
		if _, err := Parse(formatted); err != nil {
			t.Errorf("round trip %q -> %q: %v", q, formatted, err)
		}
	}
}

func TestParseQuantifiedComparisons(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM mongo.orders WHERE total > ANY (SELECT total FROM mongo.orders)",
		"SELECT * FROM mongo.orders WHERE total >= ALL (1, 2, 3)",
		"SELECT * FROM mongo.orders WHERE total <> SOME (SELECT total FROM mongo.orders)",
	} {
		if _, err := Parse(q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}
