package sql

import "testing"

func TestProfileExtraSyntaxRoundTrip(t *testing.T) {
	queries := []string{
		"SELECT 1 BETWEEN SYMMETRIC 2 AND 0 AS a",
		"SELECT 1 NOT BETWEEN ASYMMETRIC 0 AND 2 AS a",
		"SELECT nth_value(x, 2) FROM FIRST OVER (ORDER BY id) FROM t",
		"SELECT nth_value(x, 2) FROM LAST OVER (ORDER BY id) FROM t",
		`SELECT JSON_QUERY('{"a":"x"}', '$.a' KEEP QUOTES) AS k`,
		`SELECT JSON_QUERY('{"a":"x"}', '$.a' OMIT QUOTES) AS o`,
		`SELECT JSON_ARRAYAGG(DISTINCT region ORDER BY region) AS a FROM mongo.orders`,
		`SELECT JSON_ARRAYAGG(ALL region ORDER BY region) AS a FROM mongo.orders`,
		`SELECT JSON_OBJECTAGG(DISTINCT KEY status VALUE total) AS o FROM mongo.orders`,
		`SELECT JSON_OBJECTAGG(ALL status VALUE total) FILTER (WHERE total > 60) AS o FROM mongo.orders`,
		`SELECT CAST('1 day' AS INTERVAL) AS a, CAST('1 day' AS INTERVAL DAY) AS b`,
		`SELECT CAST(x AS INTERVAL HOUR TO MINUTE) AS c, CAST(x AS INTERVAL YEAR TO MONTH) AS d`,
		`SELECT CAST(x AS INTERVAL SECOND) AS e, CAST(x AS INTERVAL HOUR TO SECOND) AS f`,
		`SELECT * FROM JSON_TABLE('{"a":{"b":1}}', '$' COLUMNS (x VARCHAR(100) FORMAT JSON PATH '$.a')) jt`,
		`SELECT JSON_ARRAY(SELECT id FROM mongo.orders WHERE id=1) AS a`,
		`SELECT JSON_OBJECT('a' VALUE 1 FORMAT JSON) AS o, JSON_ARRAY(1 FORMAT JSON) AS a`,
		`SELECT * FROM JSON_TABLE('{"a":{"b":1}}', '$' COLUMNS (has INT EXISTS PATH '$.a.b')) jt`,
		"SELECT * FROM t MATCH_RECOGNIZE (PATTERN (A B)+? DEFINE A AS x = 1, B AS y = 2) AS m",
		`SELECT JSON_EXISTS('{"a":1}', 'strict $.b' UNKNOWN ON ERROR) AS u`,
		`SELECT JSON_EXISTS('{"a":1}', 'strict $.b' ON ERROR UNKNOWN) AS u`,
		`SELECT JSON_QUERY('{"a":1}', 'strict $.b' ON ERROR EMPTY ARRAY) AS ea`,
		`SELECT JSON_QUERY('{"a":1}', 'strict $.b' EMPTY OBJECT ON ERROR) AS eo`,
		`SELECT * FROM JSON_TABLE('{"a":1}', 'strict $.b' COLUMNS (x INT PATH '$') NULL ON ERROR) AS jt`,
		"SELECT TIMESTAMP WITH TIME ZONE '2024-01-01 12:00:00+05:30' AS ts",
		"SELECT TIME WITH TIME ZONE '12:00:00+05:30' AS tm",
		"SELECT TIMESTAMP '2024-01-01 12:00:00 UTC' AT LOCAL AS local",
		"SELECT JSON_QUERY('{\"a\":1}', 'strict $.b' ON ERROR EMPTY OBJECT) AS eo",
		"SELECT * FROM mongo.orders MATCH_RECOGNIZE (ORDER BY id MEASURES id AS id ONE ROW PER MATCH SHOW EMPTY MATCHES PATTERN (A*) DEFINE A AS total > 0) AS mr",
		"SELECT * FROM mongo.orders MATCH_RECOGNIZE (ORDER BY id MEASURES id AS id ONE ROW PER MATCH OMIT EMPTY MATCHES PATTERN (A*) DEFINE A AS total > 0) AS mr",
	}
	for _, q := range queries {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("parse %q: %v", q, err)
		}
		out := FormatStatement(stmt)
		if _, err := Parse(out); err != nil {
			t.Fatalf("round trip %q -> %q: %v", q, out, err)
		}
	}
}

func TestProfileExtraSyntaxErrors(t *testing.T) {
	cases := []string{
		"SELECT * FROM t MATCH_RECOGNIZE (SHOW PATTERN (A) DEFINE A AS 1=1)",
		"SELECT * FROM t MATCH_RECOGNIZE (SHOW EMPTY PATTERN (A) DEFINE A AS 1=1)",
		"SELECT * FROM t MATCH_RECOGNIZE (OMIT MATCHES PATTERN (A) DEFINE A AS 1=1)",
		"SELECT nth_value(x, 2) FROM OVER (ORDER BY id) FROM t",
		"SELECT JSON_QUERY('{}', '$' KEEP) FROM t",
	}
	for _, q := range cases {
		if _, err := Parse(q); err == nil {
			t.Fatalf("expected error for %q", q)
		} else {
			checkParserError(t, err)
		}
	}
}

func TestProfileExtraDirectBranches(t *testing.T) {
	local := &AtLocalExpr{Expr: &Literal{Kind: LitInt, Value: 1}}
	local.exprNode()
	if s := local.String(); s == "" {
		t.Fatal("empty AT LOCAL formatter")
	}
	for _, src := range []string{
		"TIMESTAMP WITH 'x'",
		"TIMESTAMP WITH TIME 'x'",
		"TIMESTAMP WITH TIME ZONE",
	} {
		if _, err := Parse("SELECT " + src); err == nil {
			t.Errorf("expected time-zone type error for %q", src)
		}
	}
	if _, err := parserAt("JSON_TABLE(x, '$.a' COLUMNS (x INT) NULL ON)").parseJSONTable(); err == nil {
		t.Fatal("expected incomplete NULL ON ERROR")
	}
	if _, err := Parse("SELECT * FROM t MATCH_RECOGNIZE (PATTERN ((A) DEFINE A AS x = 1) AS m"); err == nil {
		t.Fatal("expected malformed grouped pattern error")
	}
	if _, err := parserAt("(A").parseMatchPattern(); err == nil {
		t.Fatal("expected missing pattern close error")
	}
	if _, err := parserAt("=").parseMatchTerm(); err == nil {
		t.Fatal("expected pattern term identifier error")
	}
	for _, q := range []string{
		`SELECT * FROM JSON_TABLE('{}', '$' COLUMNS (has INT EXISTS PATH)) jt`,
		`SELECT JSON_OBJECT('a' VALUE 1 FORMAT)`,
		`SELECT JSON_ARRAY(1 FORMAT)`,
		`SELECT * FROM JSON_TABLE('{}', '$' COLUMNS (x INT FORMAT)) jt`,
		`SELECT JSON_ARRAY(SELECT`,
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("expected format/path error for %q", q)
		}
	}
	for _, src := range []string{
		"JSON_ARRAYAGG(x) FILTER",
		"JSON_ARRAYAGG(x) FILTER (",
		"JSON_ARRAYAGG(x) FILTER (WHERE",
		"JSON_ARRAYAGG(x) FILTER (WHERE x",
	} {
		if _, err := Parse("SELECT " + src); err == nil {
			t.Errorf("expected FILTER error for %q", src)
		}
	}
}
