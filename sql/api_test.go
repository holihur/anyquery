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
		"SELECT * FROM mongo.orders MATCH_RECOGNIZE (PARTITION BY region ORDER BY id MEASURES id AS id ONE ROW PER MATCH PATTERN (A B*) DEFINE A AS status = 'paid', B AS total > 0) AS mr",
		"SELECT * FROM JSON_TABLE('{\"items\":[{\"id\":1}]}', '$.items[*]' COLUMNS (id INT PATH '$.id')) AS jt",
		"SELECT JSON_VALUE('{\"a\":1}', '$.a') IS JSON OBJECT",
		"SELECT INTERVAL '1 day' + TIMESTAMP '2024-01-01 00:00:00'",
		"SHOW TABLES FROM mongo",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		// The parser must reject malformed input without panicking.
		_, _ = Parse(src)
	})
}

func TestMultipleGroupingConstructs(t *testing.T) {
	stmt, err := Parse("SELECT region, status, count(*) FROM mongo.orders GROUP BY ROLLUP(region), CUBE(status)")
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(*SelectStmt)
	if len(s.GroupBy) != 0 || len(s.GroupingSets) != 4 {
		t.Fatalf("groupBy=%v groupingSets=%v", s.GroupBy, s.GroupingSets)
	}
	if _, err := Parse(FormatStatement(stmt)); err != nil {
		t.Fatal(err)
	}
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

func TestFormatterCoversMetadataAndPatternGroups(t *testing.T) {
	cases := []string{
		"SHOW TABLES FROM mongo",
		"SHOW COLUMNS FROM mongo.orders",
		"SHOW SOURCES",
		"DESCRIBE mongo.orders",
		"EXPLAIN SELECT * FROM mongo.orders",
		"EXPLAIN SHOW TABLES FROM mongo",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN ((A | B)+ C) DEFINE A AS x = 1, B AS x = 2, C AS x = 3) AS m",
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

func TestUnsupportedSyntaxIsExplicitlyRejected(t *testing.T) {
	for _, query := range []string{
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET x = 1",
		"DELETE FROM t",
		"CREATE TABLE t (x INT)",
		"MERGE INTO t USING u ON t.id = u.id",
		"SELECT DISTINCT ON (x) x FROM t",
		"SELECT ARRAY[1, 2] FROM t",
		"SELECT TOP 1 * FROM t",
		"SELECT * FROM t QUALIFY row_number() OVER () = 1",
		"SELECT * FROM t PIVOT (sum(x) FOR y IN ('a'))",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS x = 1) AS m PIVOT (sum(x) FOR y IN ('a'))",
	} {
		if _, err := Parse(query); err == nil {
			t.Errorf("unsupported syntax unexpectedly parsed: %q", query)
		}
	}
}

func TestJSONTablePlanAndPassingRoundTrip(t *testing.T) {
	cases := []string{
		"SELECT JSON_EXISTS(doc, '$.a' PASSING x AS y) FROM t",
		"SELECT JSON_QUERY(doc, '$.a' WITHOUT WRAPPER) FROM t",
		"SELECT JSON_VALUE(doc, '$.a' ERROR ON ERROR) FROM t",
		"SELECT JSON_VALUE(doc, '$.a' NULL ON EMPTY) FROM t",
		"SELECT * FROM JSON_TABLE(doc, '$.a' PASSING x AS y COLUMNS (id INT PATH '$.id' NULL ON EMPTY ERROR ON ERROR) PLAN DEFAULT (id) ON ERROR EMPTY) AS jt",
	}
	for _, query := range cases {
		stmt, err := Parse(query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		out := FormatStatement(stmt)
		if _, err := Parse(out); err != nil {
			t.Fatalf("%q formatted as %q: %v", query, out, err)
		}
	}
	if _, err := Parse("SELECT * FROM JSON_TABLE(doc, '$.a' COLUMNS (id INT PATH '$.id') AS jt"); err == nil {
		t.Fatal("expected missing JSON_TABLE closing parenthesis error")
	}
}

func TestJSONTableAndWrapperVariants(t *testing.T) {
	cases := []string{
		"SELECT JSON_QUERY(doc, '$.a' WITH WRAPPER) FROM t",
		"SELECT JSON_QUERY(doc, '$.a' WITH CONDITIONAL WRAPPER) FROM t",
		"SELECT JSON_VALUE(doc, '$.a' DEFAULT 1 ON EMPTY) FROM t",
		"SELECT * FROM JSON_TABLE(doc, '$.a' PASSING x AS y COLUMNS (id INT PATH '$.id' ON EMPTY DEFAULT 1 ON ERROR NULL) PLAN (id) ON ERROR EMPTY) AS jt",
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
		"SELECT JSON_VALUE(doc, '$.a' DEFAULT ON EMPTY)",
		"SELECT * FROM JSON_TABLE(doc, '$.a' PASSING x COLUMNS (id INT PATH '$.id' ON FOO NULL)) AS jt",
		"SELECT * FROM JSON_TABLE(doc, '$.a' COLUMNS (id INT PATH '$.id' ON EMPTY)) AS jt",
	} {
		if _, err := Parse(query); err == nil {
			t.Errorf("%q unexpectedly parsed", query)
		}
	}
}

func TestFetchPercentRoundTrip(t *testing.T) {
	stmt, err := Parse("SELECT id FROM t FETCH FIRST 10 PERCENT ROWS ONLY")
	if err != nil {
		t.Fatal(err)
	}
	if !stmt.(*SelectStmt).FetchPercent {
		t.Fatal("expected percent fetch")
	}
	out := FormatStatement(stmt)
	if _, err := Parse(out); err != nil {
		t.Fatalf("formatted %q: %v", out, err)
	}
}

func TestIgnoreNullsRoundTrip(t *testing.T) {
	for _, q := range []string{
		"SELECT lag(x) IGNORE NULLS OVER (ORDER BY id) FROM t",
		"SELECT first_value(x) RESPECT NULLS OVER () FROM t",
	} {
		st, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(FormatStatement(st)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMatchRecognizeAdvancedSyntax(t *testing.T) {
	query := `SELECT * FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		MEASURES RUNNING sum(total) AS running, FINAL total AS final
		SUBSET U = (A, B)
		PERMUTE (A, B)
		ALL ROWS PER MATCH WITH UNMATCHED ROWS
		PATTERN (A B{1,2})
		DEFINE A AS status = 'paid', B AS total > 0
	) AS mr`
	stmt, err := Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	formatted := FormatStatement(stmt)
	if _, err := Parse(formatted); err != nil {
		t.Fatalf("formatted %q: %v", formatted, err)
	}
}

func TestNewStandardSyntaxRoundTrip(t *testing.T) {
	cases := []string{
		"SELECT '100%' LIKE '100\\\\%' ESCAPE '\\\\' FROM t",
		"SELECT 'abc' SIMILAR TO 'a_c' FROM t",
		"SELECT a FROM t UNION JOIN u",
		"SELECT * FROM t MATCH_RECOGNIZE (MEASURES RUNNING sum(x) AS r, FINAL x AS f SUBSET U = (A, B) PERMUTE (A, B) ALL ROWS PER MATCH WITH UNMATCHED ROWS PATTERN (A B{1,2}) DEFINE A AS x = 1, B AS x = 2) AS m",
		"SELECT JSON_VALUE(doc, '$.a' DEFAULT 1 ON ERROR) FROM t",
		"SELECT JSON_VALUE(doc, '$.a' NULL ON ERROR) FROM t",
	}
	for _, q := range cases {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		out := FormatStatement(stmt)
		if _, err := Parse(out); err != nil {
			t.Fatalf("%q formatted as %q: %v", q, out, err)
		}
	}
}

func TestNewSyntaxEdgeBranches(t *testing.T) {
	cases := []string{
		"FROM t |> UNION JOIN u",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A{1}) DEFINE A AS 1 = 1) AS m",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A{1,}) DEFINE A AS 1 = 1) AS m",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A{1,2}?) DEFINE A AS 1 = 1) AS m",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS 1 = 1 WITH UNMATCHED ROWS) AS m",
		"SELECT sum(x) OVER (ORDER BY ts RANGE BETWEEN CURRENT ROW AND INTERVAL '1 day' FOLLOWING) FROM t",
	}
	for _, q := range cases {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		out := FormatStatement(stmt)
		if _, err := Parse(out); err != nil {
			t.Fatalf("%q formatted as %q: %v", q, out, err)
		}
	}
	invalid := []string{
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A{) DEFINE A AS 1 = 1) AS m",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A{n}) DEFINE A AS 1 = 1) AS m",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A{1) DEFINE A AS 1 = 1) AS m",
		"SELECT * FROM t MATCH_RECOGNIZE (SUBSET U = DEFINE A AS 1 = 1) AS m",
		"SELECT * FROM t MATCH_RECOGNIZE (PERMUTE DEFINE A AS 1 = 1) AS m",
		"SELECT JSON_EXISTS(doc, '$.a' PASSING) FROM t",
		"SELECT JSON_VALUE(doc, '$.a' DEFAULT ON ERROR) FROM t",
		"SELECT JSON_VALUE(doc, '$.a' ON EMPTY FOO) FROM t",
		"SELECT JSON_VALUE(doc, '$.a' WITH CONDITIONAL FROM t)",
		"SELECT 'x' SIMILAR 'x' FROM t",
		"SELECT sum(x) OVER (ORDER BY ts RANGE BETWEEN INTERVAL '1 day' FROM t",
	}
	for _, q := range invalid {
		if _, err := Parse(q); err == nil {
			t.Errorf("%q unexpectedly parsed", q)
		}
	}
}

func TestRemainingSyntaxBranches(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM t LIMIT 1, 2",
		"FROM t |> LIMIT 1 OFFSET 2",
		"TABLE s",
		"SELECT * FROM t ORDER BY x ASC, y NULLS FIRST",
		"SELECT JSON_EXISTS(doc, '$.a' PASSING x, y) FROM t",
	} {
		if _, err := Parse(q); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
}

func TestAdvancedJSONAndMatchRoundTrip(t *testing.T) {
	cases := []string{
		"SELECT JSON_QUERY(doc, '$.a[*]' WITH ARRAY WRAPPER) FROM t",
		"SELECT JSON_QUERY(doc, '$.a[*]' WITHOUT ARRAY WRAPPER) FROM t",
		"SELECT JSON_VALUE(doc, '$.a' EMPTY ARRAY ON EMPTY) FROM t",
		"SELECT * FROM JSON_TABLE(doc, '$.a' COLUMNS (has EXISTS PATH '$.x')) AS jt",
		"SELECT * FROM t MATCH_RECOGNIZE (MEASURES RUNNING sum(x) AS r, FINAL count(*) AS c, PREV(x) AS p, count(AB.*) AS n SUBSET AB = (A, B) PATTERN (PERMUTE(A, B)) DEFINE A AS x = 1, B AS x = 2) AS m",
	}
	for _, q := range cases {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if _, err := Parse(FormatStatement(stmt)); err != nil {
			t.Fatalf("%q roundtrip: %v", q, err)
		}
	}
}

func TestCTEValuesFromTableAndLocalTime(t *testing.T) {
	for _, q := range []string{
		"WITH x(a, b) AS (VALUES (1, 'a')) SELECT * FROM x",
		"WITH RECURSIVE x(n) AS (VALUES (1) UNION ALL SELECT n + 1 FROM x WHERE n < 3) SELECT * FROM x",
		"SELECT * FROM (TABLE mongo.orders) AS t",
		"SELECT LOCALTIME, LOCALTIMESTAMP",
		"SELECT JSON_EXISTS('{\"a\":1}', '$.b' TRUE ON ERROR)",
		"SELECT JSON_EXISTS('{\"a\":1}', '$.b' FALSE ON ERROR)",
	} {
		if _, err := Parse(q); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
}

func TestMoreSQL2016Syntax(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM UNNEST(json_extract('[10,20]', '$')) WITH ORDINALITY AS u(x, ord)",
		"SELECT count(*) FROM t GROUP BY ()",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS 1 = 1 AFTER MATCH SKIP TO FIRST A) AS m",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS 1 = 1 AFTER MATCH SKIP TO LAST A) AS m",
		"SELECT (DATE '2024-01-01', DATE '2024-02-01') OVERLAPS (DATE '2024-01-15', DATE '2024-03-01')",
	} {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if _, err := Parse(FormatStatement(stmt)); err != nil {
			t.Fatalf("%q roundtrip: %v", q, err)
		}
	}
	for _, q := range []string{"SELECT (DATE '2024-01-01',"} {
		if _, err := Parse(q); err == nil {
			t.Errorf("%q unexpectedly parsed", q)
		}
	}
}

func TestOverlapsAndConditionalArrayWrapperEdges(t *testing.T) {
	for _, q := range []string{
		"SELECT JSON_QUERY('{\"a\":[1,2]}', '$.a[*]' WITH CONDITIONAL ARRAY WRAPPER)",
		"SELECT JSON_QUERY('{\"a\":[1,2]}', '$.a[*]' WITH UNCONDITIONAL ARRAY WRAPPER)",
		"SELECT (DATE '2024-01-01', DATE '2024-02-01') OVERLAPS (DATE '2024-01-15', DATE '2024-03-01')",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	for _, q := range []string{
		"SELECT (1,",
		"SELECT (1,2",
		"SELECT (1,2) OVERLAPS",
		"SELECT (1,2) OVERLAPS (",
		"SELECT (1,2) OVERLAPS (3",
		"SELECT (1,2) OVERLAPS (3,",
		"SELECT (1,2) OVERLAPS (3,4",
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("%q unexpectedly parsed", q)
		}
	}
}

func TestPatternTrailingQuantifier(t *testing.T) {
	q := "SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A B)+? DEFINE A AS 1 = 1, B AS 2 = 2) AS m"
	stmt, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(FormatStatement(stmt)); err != nil {
		t.Fatal(err)
	}
}

func TestMatchSkipWithoutVariable(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS 1 = 1 AFTER MATCH SKIP TO FIRST) AS m",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS 1 = 1 AFTER MATCH SKIP TO LAST) AS m",
	} {
		st, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(FormatStatement(st)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocaltimeFormatting(t *testing.T) {
	for _, q := range []string{"SELECT LOCALTIME", "SELECT LOCALTIMESTAMP"} {
		st, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(FormatStatement(st)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExcludeAndSkipFormatterRoundTrip(t *testing.T) {
	for _, q := range []string{
		"SELECT sum(x) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW) FROM t",
		"SELECT sum(x) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE NO OTHERS) FROM t",
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS 1 = 1 AFTER MATCH SKIP TO LAST A) AS m",
	} {
		st, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		out := FormatStatement(st)
		if _, err := Parse(out); err != nil {
			t.Fatalf("%q -> %q: %v", q, out, err)
		}
	}
}

func TestFetchOffsetParameterRoundTrip(t *testing.T) {
	q := "SELECT id FROM t ORDER BY id OFFSET ? ROWS FETCH NEXT ? ROWS ONLY"
	st, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	out := FormatStatement(st)
	if _, err := Parse(out); err != nil {
		t.Fatalf("formatted %q: %v", out, err)
	}
	if !strings.Contains(out, "FETCH FIRST") {
		t.Fatalf("lost FETCH: %s", out)
	}
}

func TestJSONObjectKeyKeyword(t *testing.T) {
	if _, err := Parse("SELECT JSON_OBJECT(KEY 'a' VALUE 1)"); err != nil {
		t.Fatal(err)
	}
}
