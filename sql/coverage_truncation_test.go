package sql

import "testing"

func TestParserTruncationCoverage(t *testing.T) {
	queries := []string{
		"SELECT a, b AS c, t.* FROM s.t t JOIN u v ON t.id = v.id WHERE a = 1 GROUP BY a, b HAVING count(*) > 1 ORDER BY a DESC NULLS LAST LIMIT 1 OFFSET 2 FETCH FIRST 3 ROWS WITH TIES",
		"WITH RECURSIVE x(a, b) AS (SELECT 1, 2 UNION ALL SELECT a + 1, b FROM x), y AS (SELECT * FROM x) SELECT * FROM y UNION (SELECT * FROM x INTERSECT SELECT * FROM y EXCEPT SELECT * FROM y)",
		"FROM s.t |> WHERE a = 1 |> SELECT a, b |> AGGREGATE count(*) AS n GROUP BY a |> ORDER BY n DESC |> LIMIT 1 |> DISTINCT |> LEFT JOIN u v ON a = v.a",
		"SELECT CASE a WHEN 1 THEN 'x' ELSE 'y' END, CAST(a AS DOUBLE PRECISION), TRY_CAST(b AS CHARACTER VARYING(10)), extract(year FROM d), position('a' IN s), substring(s FROM 1 FOR 2), interval '1-2' YEAR TO MONTH, timestamp '2024-01-01 00:00:00' AT TIME ZONE 'UTC' FROM t",
		"SELECT count(DISTINCT a) FILTER (WHERE a > 1), percentile_cont(0.5) WITHIN GROUP (ORDER BY a), sum(a) OVER (PARTITION BY b ORDER BY c ROWS BETWEEN 1 PRECEDING AND CURRENT ROW EXCLUDE TIES) FROM t WINDOW w AS (PARTITION BY b), w2 AS (w ORDER BY c)",
		"SELECT JSON_VALUE(doc, 'strict $.a[0]' RETURNING INT ON EMPTY NULL ON ERROR DEFAULT 1), JSON_QUERY(doc, '$.a' WRAPPER CONDITIONAL ON EMPTY ERROR), JSON_EXISTS(doc, '$.a'), JSON_OBJECT('a': 1 NULL ON NULL WITH UNIQUE KEYS RETURNING JSON), JSON_ARRAY(1 ABSENT ON NULL), JSON_ARRAYAGG(x ORDER BY y NULL ON NULL), JSON_OBJECTAGG(k: v ORDER BY x) FROM t",
		"SELECT * FROM JSON_TABLE(doc, '$.items[*]' COLUMNS (id INT PATH '$.id', ord FOR ORDINALITY, NESTED PATH '$.tags[*]' COLUMNS (tag VARCHAR(10) PATH '$'))) AS jt",
		"SELECT * FROM t TABLESAMPLE SYSTEM (10 PERCENT) REPEATABLE (7)",
		"SELECT * FROM t FOR SYSTEM_TIME FROM TIMESTAMP '2024-01-01 00:00:00' TO TIMESTAMP '2024-02-01 00:00:00'",
		"SELECT * FROM t MATCH_RECOGNIZE (PARTITION BY a ORDER BY b MEASURES match_number() AS m, classifier() AS c ALL ROWS PER MATCH AFTER MATCH SKIP TO NEXT ROW PATTERN ((A|B)+? C) DEFINE A AS a = 1, B AS b = 2, C AS c = 3) AS mr",
		"SELECT * FROM (VALUES (1, 'a'), (2, 'b')) AS v(x, y)",
		"SELECT INTERVAL '1 02:03:04' DAY TO SECOND, TRIM(BOTH 'x' FROM s), OVERLAY(s PLACING 'x' FROM 1 FOR 2)",
	}
	for _, q := range queries {
		for i := 1; i <= len(q); i++ {
			if err := func() error { _, err := Parse(q[:i]); return err }(); err != nil {
				checkParserPositional(t, err)
			}
		}
	}
}

func TestMoreASTCoverage(t *testing.T) {
	col := &ColumnRef{Parts: []string{"x"}}
	lit := &Literal{Kind: LitInt, Value: 1}
	if got := FormatSelect(&SelectStmt{CTEs: []*CTE{{Name: "a", Query: &SelectStmt{}}, {Name: "b", Query: &SelectStmt{}}}}); got == "" {
		t.Fatal("empty CTE format")
	}
	if got := FormatSelect(&SelectStmt{Windows: []NamedWindow{{Name: "a", Spec: &WindowSpec{}}, {Name: "b", Spec: &WindowSpec{}}}}); got == "" {
		t.Fatal("empty WINDOW format")
	}
	if got := FormatTableExpr(nil); got != "" {
		t.Fatalf("nil table formatted as %q", got)
	}
	if got := FormatExplain(&ExplainStmt{Stmt: noFormatStmt{}}); got != "EXPLAIN" {
		t.Fatalf("unknown explain = %q", got)
	}
	if got := formatMatchPattern(&MatchPattern{Kind: "group", Quantifier: "?", Lazy: true, Children: []*MatchPattern{{Kind: "var", Var: "A"}}}); got == "" {
		t.Fatal("empty pattern format")
	}
	for _, e := range []Expr{
		&InExpr{Left: col, List: []Expr{lit}},
		&QuantifiedExpr{Left: col, List: []Expr{lit}},
		&IsExpr{Left: col, Right: lit},
	} {
		if WalkExpr(e, func(x Expr) bool { return x != col }) {
			t.Fatalf("expected walk to stop for %T", e)
		}
	}
	checkParserError(t, func() error { _, err := Tokenize("'unterminated"); return err }())
	if isNumericToken("1a") || isNumericToken("") {
		t.Fatal("numeric token detection failed")
	}
	if _, err := parserAt("").parseTypeName(); err == nil {
		t.Fatal("expected empty type error")
	}
}

type noFormatStmt struct{}

func (noFormatStmt) stmtNode() { _ = 0 }
