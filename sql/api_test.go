package sql

import (
	"errors"
	"strings"
	"testing"
)

func TestTokenKindString(t *testing.T) {
	if TokEOF.String() != "EOF" || TokIdent.String() != "identifier" || TokPunct.String() != "punctuation" {
		t.Fatalf("unexpected token kind names")
	}
}

func TestErrorHasPosition(t *testing.T) {
	for _, query := range []string{"SELECT * FROM", "SELECT 'unterminated", "SELECT * FROM t WHERE a IN ()"} {
		_, err := Parse(query)
		if err == nil {
			t.Fatalf("%q: expected error", query)
		}
		var se *Error
		if !errors.As(err, &se) {
			t.Fatalf("%q: error type %T is not *sql.Error", query, err)
		}
		if se.Line < 1 || se.Col < 1 || se.Message == "" {
			t.Fatalf("%q: incomplete error %+v", query, se)
		}
	}
}

func TestFormatQualifiedNames(t *testing.T) {
	stmt, err := Parse("SELECT pg.public.users.* FROM pg.public.users AS u")
	if err != nil {
		t.Fatal(err)
	}
	out := FormatSelect(stmt.(*SelectStmt))
	if !strings.Contains(out, "SELECT pg.public.users.* FROM pg.public.users AS u") {
		t.Fatalf("formatted query = %q", out)
	}
	if _, err := Parse(out); err != nil {
		t.Fatalf("round trip failed: %v (%s)", err, out)
	}
	if got := FormatIdent("1bad"); got != `"1bad"` {
		t.Fatalf("FormatIdent(1bad) = %s", got)
	}
	if got := FormatIdent("ok_name"); got != "ok_name" {
		t.Fatalf("FormatIdent(ok_name) = %s", got)
	}
}

func TestSelectWithoutFrom(t *testing.T) {
	stmt, err := Parse("SELECT 1 + 2 AS n")
	if err != nil {
		t.Fatal(err)
	}
	if len(stmt.(*SelectStmt).From) != 0 {
		t.Fatal("unexpected FROM")
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"SELECT 1",
		"SELECT * FROM mongo.orders WHERE total > ?",
		"FROM mongo.orders |> WHERE status = 'paid' |> LIMIT 10",
		"WITH RECURSIVE nums(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM nums WHERE n < 3) SELECT * FROM nums",
		"SELECT sum(total) OVER (PARTITION BY region ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM mongo.orders",
		"SELECT 'x",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		// The parser must reject malformed input without panicking.
		_, _ = Parse(src)
	})
}

func TestMixedGroupingSetsNormalized(t *testing.T) {
	stmt, err := Parse("SELECT region, status, count(*) FROM mongo.orders GROUP BY region, ROLLUP(status)")
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(*SelectStmt)
	if len(s.GroupBy) != 0 || len(s.GroupingSets) != 2 {
		t.Fatalf("groupBy=%v groupingSets=%v", s.GroupBy, s.GroupingSets)
	}
	if s.GroupingSets[0][0].String() != "region" || s.GroupingSets[1][0].String() != "region" {
		t.Fatalf("grouping sets = %v", s.GroupingSets)
	}
}

func TestUnsupportedConstructsAreRejected(t *testing.T) {
	for _, query := range []string{
		"SELECT DISTINCT ON (region) region FROM mongo.orders",
		"SELECT ARRAY[1,2] FROM mongo.orders",
		"SELECT arr[1] FROM mongo.orders",
		"SELECT * FROM mongo.orders NATURAL CROSS JOIN mongo.customers",
		"SELECT * FROM mongo.orders GROUP BY ROLLUP(region), CUBE(status)",
	} {
		if _, err := Parse(query); err == nil {
			t.Errorf("%q unexpectedly parsed", query)
		}
	}
}

func TestPositionAndChainedCast(t *testing.T) {
	for _, query := range []string{
		"SELECT position('a', name) FROM mongo.customers",
		"SELECT total::int::text FROM mongo.orders",
	} {
		if _, err := Parse(query); err != nil {
			t.Errorf("%q: %v", query, err)
		}
	}
}

func TestFormatStatementNewTableExpressions(t *testing.T) {
	cases := []string{
		"SELECT * FROM mongo.orders LIMIT ? OFFSET ?",
		"SELECT * FROM mongo.orders FETCH FIRST 2 ROWS WITH TIES",
		"SELECT * FROM mongo.orders TABLESAMPLE SYSTEM (50) REPEATABLE (7)",
		"SELECT * FROM (SELECT 1) AS t(a)",
		"SELECT * FROM UNNEST(json_extract('[1,2]', '$')) AS u(x)",
		"SELECT * FROM mongo.customers c, LATERAL (SELECT count(*) FROM mongo.orders o WHERE o.customer_id = c.id) x",
		"VALUES (1, 'a'), (2, 'b')",
		"TABLE mongo.orders",
	}
	for _, query := range cases {
		stmt, err := Parse(query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		formatted := FormatStatement(stmt)
		if formatted == "" {
			t.Fatalf("%q: no formatter output", query)
		}
		if _, err := Parse(formatted); err != nil {
			t.Fatalf("%q formatted as %q: %v", query, formatted, err)
		}
	}
}

func TestParseRowCountExpressions(t *testing.T) {
	stmt, err := Parse("SELECT * FROM mongo.orders LIMIT ? OFFSET ?")
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(*SelectStmt)
	if s.LimitExpr == nil || s.OffsetExpr == nil || s.Limit != nil || s.Offset != nil {
		t.Fatalf("limit=%v/%v offset=%v/%v", s.Limit, s.LimitExpr, s.Offset, s.OffsetExpr)
	}
	stmt, err = Parse("SELECT * FROM mongo.orders FETCH FIRST 2 ROWS WITH TIES")
	if err != nil {
		t.Fatal(err)
	}
	if !stmt.(*SelectStmt).FetchWithTies {
		t.Fatal("expected WITH TIES")
	}
}

func TestTableExpressionErrors(t *testing.T) {
	for _, query := range []string{
		"SELECT * FROM LATERAL",
		"SELECT * FROM mongo.orders TABLESAMPLE UNKNOWN (10)",
		"SELECT * FROM mongo.orders TABLESAMPLE SYSTEM (101)",
		"SELECT * FROM mongo.orders TABLESAMPLE SYSTEM (10",
	} {
		if _, err := Parse(query); err == nil {
			t.Errorf("%q unexpectedly parsed", query)
		}
	}
}

func TestNamedWindowParsingAndErrors(t *testing.T) {
	cases := []string{
		"SELECT sum(total) OVER w FROM mongo.orders WINDOW w AS (PARTITION BY region ORDER BY id)",
		"SELECT sum(total) OVER (w ORDER BY id) FROM mongo.orders WINDOW w AS (PARTITION BY region)",
		"SELECT sum(total) OVER (ORDER BY id GROUPS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM mongo.orders",
		"SELECT sum(total) OVER (ORDER BY id ROWS CURRENT ROW EXCLUDE TIES) FROM mongo.orders",
		"SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY total) FROM mongo.orders",
	}
	for _, query := range cases {
		stmt, err := Parse(query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		formatted := FormatStatement(stmt)
		if _, err := Parse(formatted); err != nil {
			t.Fatalf("%q formatted as %q: %v", query, formatted, err)
		}
	}
	for _, query := range []string{
		"SELECT sum(total) OVER missing FROM mongo.orders",
		"SELECT sum(total) OVER w FROM mongo.orders WINDOW w AS (PARTITION BY region), w AS (ORDER BY id)",
		"SELECT sum(total) OVER a FROM mongo.orders WINDOW a AS (b), b AS (a)",
	} {
		if _, err := Parse(query); err == nil {
			t.Errorf("%q unexpectedly parsed", query)
		}
	}
}

func TestIntervalTrimOverlayAndTimeZoneParsing(t *testing.T) {
	cases := []string{
		"SELECT INTERVAL '1 day'",
		"SELECT INTERVAL '1' DAY",
		"SELECT INTERVAL '1-2' YEAR TO MONTH",
		"SELECT INTERVAL '1 02:03:04' DAY TO SECOND",
		"SELECT TRIM(BOTH 'x' FROM 'xxaxx')",
		"SELECT TRIM(LEADING FROM '  x')",
		"SELECT OVERLAY('abcdef' PLACING 'XY' FROM 2 FOR 3)",
		"SELECT TIMESTAMP '2024-01-01T12:00:00Z' AT TIME ZONE 'UTC'",
	}
	for _, query := range cases {
		stmt, err := Parse(query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		formatted := FormatStatement(stmt)
		if _, err := Parse(formatted); err != nil {
			t.Fatalf("%q formatted as %q: %v", query, formatted, err)
		}
	}
	for _, query := range []string{
		"SELECT INTERVAL 1",
		"SELECT OVERLAY('a' FROM 1)",
		"SELECT TRIM(BOTH 'x' FROM)",
	} {
		if _, err := Parse(query); err == nil {
			t.Errorf("%q unexpectedly parsed", query)
		}
	}
}

func TestJSONSyntaxRoundTrip(t *testing.T) {
	cases := []string{
		"SELECT JSON_VALUE(doc, '$.a'), JSON_QUERY(doc, 'strict $.a[0]'), JSON_EXISTS(doc, '$.a') FROM t",
		"SELECT JSON_OBJECT('a': 1, 'b': NULL ABSENT ON NULL) FROM t",
		"SELECT JSON_ARRAY(1, 2, NULL) FROM t",
		"SELECT JSON_ARRAYAGG(x ORDER BY y) FROM t",
		"SELECT JSON_OBJECTAGG(k: v) FROM t",
		"SELECT doc IS JSON OBJECT WITH UNIQUE KEYS, doc IS NOT JSON ARRAY FROM t",
		"SELECT * FROM JSON_TABLE(doc, '$.items[*]' COLUMNS (id INT PATH '$.id', ord FOR ORDINALITY, NESTED PATH '$.tags[*]' COLUMNS (tag VARCHAR(20) PATH '$'))) AS jt",
	}
	for _, query := range cases {
		stmt, err := Parse(query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		formatted := FormatStatement(stmt)
		if _, err := Parse(formatted); err != nil {
			t.Fatalf("%q formatted as %q: %v", query, formatted, err)
		}
	}
	for _, query := range []string{
		"SELECT JSON_VALUE(doc) FROM t",
		"SELECT JSON_TABLE(doc, '$.a' COLUMNS (id INT PATH)) FROM t",
		"SELECT doc IS JSON OBJECT WITH UNIQUE FROM t",
	} {
		if _, err := Parse(query); err == nil {
			t.Errorf("%q unexpectedly parsed", query)
		}
	}
}

func TestTemporalTableSyntax(t *testing.T) {
	cases := []string{
		"SELECT * FROM mongo.orders FOR SYSTEM_TIME AS OF TIMESTAMP '2024-01-01T00:00:00Z'",
		"SELECT * FROM mongo.orders FOR SYSTEM_TIME FROM TIMESTAMP '2024-01-01T00:00:00Z' TO TIMESTAMP '2024-02-01T00:00:00Z'",
		"SELECT * FROM mongo.orders FOR SYSTEM_TIME BETWEEN TIMESTAMP '2024-01-01T00:00:00Z' AND TIMESTAMP '2024-02-01T00:00:00Z'",
		"SELECT * FROM mongo.orders FOR SYSTEM_TIME ALL",
	}
	for _, query := range cases {
		stmt, err := Parse(query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		formatted := FormatStatement(stmt)
		if _, err := Parse(formatted); err != nil {
			t.Fatalf("%q formatted as %q: %v", query, formatted, err)
		}
	}
	for _, query := range []string{
		"SELECT * FROM mongo.orders FOR SYSTEM_TIME",
		"SELECT * FROM mongo.orders FOR SYSTEM_TIME AS OF",
		"SELECT * FROM mongo.orders FOR SYSTEM_TIME BETWEEN 'a'",
	} {
		if _, err := Parse(query); err == nil {
			t.Errorf("%q unexpectedly parsed", query)
		}
	}
}

func TestMatchRecognizeSyntaxRoundTrip(t *testing.T) {
	cases := []string{
		"SELECT * FROM mongo.orders MATCH_RECOGNIZE (PARTITION BY region ORDER BY id MEASURES id AS id, match_number() AS m ONE ROW PER MATCH PATTERN (A B*) DEFINE A AS status = 'paid', B AS total > 0) AS mr",
		"SELECT * FROM mongo.orders MATCH_RECOGNIZE (ORDER BY id MEASURES classifier() AS cls ALL ROWS PER MATCH AFTER MATCH SKIP TO NEXT ROW PATTERN (A+?) DEFINE A AS status = 'paid') AS mr",
	}
	for _, query := range cases {
		stmt, err := Parse(query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		formatted := FormatStatement(stmt)
		if _, err := Parse(formatted); err != nil {
			t.Fatalf("%q formatted as %q: %v", query, formatted, err)
		}
	}
	for _, query := range []string{
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN () DEFINE A AS 1 = 1)",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A) DEFINE A)",
	} {
		if _, err := Parse(query); err == nil {
			t.Errorf("%q unexpectedly parsed", query)
		}
	}
}
