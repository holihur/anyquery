package sql

import "testing"

func TestCoverageFinalSQL(t *testing.T) {
	for _, q := range []string{
		"WITH x(a, b) AS (VALUES (1, 2)) SELECT a, b FROM x",
		"WITH RECURSIVE x(n) AS (VALUES (1) UNION ALL SELECT n + 1 FROM x WHERE n < 3) SELECT * FROM x",
		"SELECT * FROM (TABLE s) AS t",
		"SELECT JSON_VALUE(doc, '$.a' ON ERROR TRUE) FROM t",
		"SELECT JSON_VALUE(doc, '$.a' ON ERROR FALSE) FROM t",
	} {
		if _, err := Parse(q); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
	for _, q := range []string{
		"WITH x(a) AS (VALUES (1",
		"WITH x(a) AS (VALUES (1) UNION SELECT",
		"SELECT * FROM (TABLE ) AS t",
		"SELECT * FROM (TABLE s AS t",
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("%q unexpectedly parsed", q)
		}
	}
}

func TestCoverageFinalBranches(t *testing.T) {
	for _, q := range []string{
		"WITH x(a) AS (VALUES (1) UNION SELECT 2 FROM x ORDER)",
		"SELECT * FROM (TABLE s) AS",
		"SELECT JSON_VALUE(doc, '$.a' EMPTY ARRAY FOO) FROM t",
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("%q unexpectedly parsed", q)
		}
	}
}
