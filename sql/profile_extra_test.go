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
		`SELECT JSON_EXISTS('{"a":1}', 'strict $.b' UNKNOWN ON ERROR) AS u`,
		`SELECT JSON_EXISTS('{"a":1}', 'strict $.b' ON ERROR UNKNOWN) AS u`,
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
