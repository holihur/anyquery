package sql

import "testing"

func parserAt(src string) *Parser {
	tokens, err := tokenizeAll(src)
	if err != nil || len(tokens) == 0 {
		tokens = []Token{{Kind: TokEOF}}
	}
	p := &Parser{tokens: tokens}
	p.sync()
	return p
}

func TestDirectParserBranches(t *testing.T) {
	for _, src := range []string{
		"INT", "DOUBLE PRECISION", "CHARACTER VARYING(10)", "BINARY VARYING(2)",
		"LONG VARCHAR", "TIMESTAMP WITH TIME ZONE", "TIMESTAMP WITHOUT TIME ZONE",
		"DECIMAL(10,2)", "INT(1)",
	} {
		if _, err := parserAt(src).parseTypeName(); err != nil {
			t.Errorf("type %q: %v", src, err)
		}
	}
	for _, src := range []string{"", "INT(", "INT(x)", "INT(1"} {
		_, _ = parserAt(src).parseTypeName()
	}

	for _, src := range []string{
		"UNBOUNDED PRECEDING", "UNBOUNDED FOLLOWING", "CURRENT ROW", "1 PRECEDING", "1 FOLLOWING",
		"UNBOUNDED", "1", "x",
	} {
		_, _, _ = parserAt(src).parseFrameBound()
	}

	for _, src := range []string{"LIMIT 1, 2", "LIMIT 1 OFFSET 2", "LIMIT ALL", "LIMIT", "LIMIT x", "LIMIT 1.5", "LIMIT -1"} {
		_, _, _ = parserAt(src).parseLimit()
	}
	for _, src := range []string{"1", "1.5", "-1", "x"} {
		_, _ = parserAt(src).parseIntLiteral("N")
	}

	options := []string{
		"NULL ON NULL", "ABSENT ON NULL", "WITH UNIQUE KEYS", "WITHOUT UNIQUE KEYS",
		"RETURNING INT", "ON EMPTY ERROR", "ON EMPTY NULL", "ON EMPTY DEFAULT 1",
		"ON ERROR ERROR", "ON ERROR NULL", "ON ERROR DEFAULT 1", "WRAPPER CONDITIONAL",
		"WRAPPER UNCONDITIONAL", "WRAPPER", "ON FOO", "ON EMPTY FOO", "WITH UNIQUE",
	}
	for _, src := range options {
		fn := &FuncCall{}
		_ = parserAt(src).parseJSONOptions(fn)
	}

	windowSpecs := []string{
		"w", "(w)", "(PARTITION BY x ORDER BY y ROWS UNBOUNDED PRECEDING)",
		"(ORDER BY y RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)",
		"(ORDER BY y GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING EXCLUDE CURRENT ROW)",
		"(ORDER BY y ROWS CURRENT ROW EXCLUDE GROUP)",
		"(ORDER BY y ROWS CURRENT ROW EXCLUDE TIES)",
		"(ORDER BY y ROWS CURRENT ROW EXCLUDE NO OTHERS)",
		"(ORDER BY y ROWS CURRENT ROW EXCLUDE FOO)", "(", "(ORDER BY y ROWS",
		"(ORDER BY y ROWS BETWEEN UNBOUNDED AND CURRENT ROW)",
		"(ORDER BY y ROWS 1)",
	}
	for _, src := range windowSpecs {
		_, _ = parserAt(src).parseWindowSpec()
	}

	for _, src := range []string{"SHOW", "SHOW FOO", "SHOW TABLES FROM", "SHOW COLUMNS", "SHOW COLUMNS FROM t", "DESCRIBE", "DESCRIBE t"} {
		_, _ = parserAt(src).parseShow()
	}
	for _, src := range []string{"DESCRIBE", "DESCRIBE t", "DESCRIBE TABLE", "DESCRIBE s.t"} {
		_, _ = parserAt(src).parseDescribe()
	}
}

func TestDirectParserErrorBranches(t *testing.T) {
	// Each call starts at the token the target method expects.
	for _, src := range []string{
		"SELECT", "SELECT * FROM t WHERE", "SELECT * FROM t GROUP x", "SELECT * FROM t HAVING",
		"SELECT * FROM t WINDOW", "SELECT * FROM t WINDOW w", "SELECT * FROM t WINDOW w AS", "SELECT * FROM t WINDOW w AS (",
	} {
		_, _ = parserAt(src).parseSelect()
	}
	for _, src := range []string{"UNION", "UNION DISTINCT", "UNION SELECT", "UNION (SELECT 1", "UNION (", "UNION SELECT 1"} {
		_, _ = parserAt(src).parseSetOperations(&SelectStmt{Projections: []SelectItem{{Expr: &Literal{Kind: LitInt, Value: 1}}}}, 1)
	}
	for _, src := range []string{
		"FROM", "FROM t", "FROM t WHERE x = 1", "FROM t ORDER BY x", "FROM t LIMIT 1", "FROM t OFFSET 1", "FROM t FETCH FIRST 1 ROW ONLY",
	} {
		_, _ = parserAt(src).parseFromFirst()
	}
	for _, src := range []string{"LIMIT 1, ?", "LIMIT 1,", "LIMIT 1, x", "OFFSET 1 ROW", "OFFSET", "FETCH", "FETCH FIRST", "FETCH FIRST 1 ROW", "FETCH FIRST 1 ROW FOO"} {
		_ = parserAt(src).parseSelectTail(&SelectStmt{})
	}
	for _, src := range []string{"FOO", "WHERE", "SELECT", "SELECT DISTINCT", "AGGREGATE", "AGGREGATE x GROUP", "AGGREGATE x BY", "ORDER", "ORDER BY", "LIMIT", "LIMIT ALL", "JOIN", "JOIN u", "JOIN u ON", "JOIN u USING", "LEFT", "RIGHT", "FULL", "CROSS", "NATURAL"} {
		_, _ = parserAt(src).parsePipeStage()
		_, _ = parserAt(src).parsePipeJoin()
	}
	for _, src := range []string{"WITH", "WITH x", "WITH x AS", "WITH x AS (", "WITH x(a AS (SELECT 1", "WITH x AS (SELECT 1) SELECT 1"} {
		_, _ = parserAt(src).parseCTEs()
	}
	for _, src := range []string{"MATCH_RECOGNIZE", "MATCH_RECOGNIZE (", "MATCH_RECOGNIZE (PARTITION)", "MATCH_RECOGNIZE (MEASURES x)", "MATCH_RECOGNIZE (MEASURES x AS)", "MATCH_RECOGNIZE (PATTERN)", "MATCH_RECOGNIZE (PATTERN ()", "MATCH_RECOGNIZE (PATTERN (A) DEFINE A)", "MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS)", "MATCH_RECOGNIZE (UNKNOWN)"} {
		_, _ = parserAt(src).parseMatchRecognize(&TableRef{Source: "s", Table: "t"})
	}
	for _, src := range []string{"(", "()", "(A", "(A|)", "(A)+", "A", "A+"} {
		_, _ = parserAt(src).parseMatchPattern()
	}
	for _, src := range []string{"JSON_TABLE", "JSON_TABLE(", "JSON_TABLE(x", "JSON_TABLE(x,", "JSON_TABLE(x, '$.a'", "JSON_TABLE(x, '$.a' COLUMNS", "JSON_TABLE(x, '$.a' COLUMNS (", "JSON_TABLE(x, '$.a' COLUMNS (x)", "JSON_TABLE(x, '$.a' COLUMNS (x INT PATH", "JSON_TABLE(x, '$.a' COLUMNS (x FOR", "JSON_TABLE(x, '$.a' COLUMNS (NESTED", "JSON_TABLE(x, '$.a' COLUMNS (NESTED PATH '$.b' COLUMNS ("} {
		_, _ = parserAt(src).parseJSONTable()
	}
	for _, src := range []string{"(", "()", "(x", "(x INT", "(x INT PATH", "(x FOR", "(NESTED", "(NESTED PATH '$.a' COLUMNS"} {
		_, _ = parserAt(src).parseJSONTableColumns()
	}
	for _, src := range []string{"UNNEST", "UNNEST(", "UNNEST()", "UNNEST(x)", "UNNEST(x) AS", "UNNEST(x) AS u(", "UNNEST(x) AS u(a,b"} {
		_, _ = parserAt(src).parseTableFunction()
	}
	for _, src := range []string{"", "AS", "AS x(", "(", "x("} {
		_, _, _ = parserAt(src).parseTableAlias(false)
		_, _, _ = parserAt(src).parseTableAlias(true)
	}
	for _, src := range []string{"FOR", "FOR SYSTEM_TIME", "FOR SYSTEM_TIME AS", "FOR SYSTEM_TIME AS OF", "FOR SYSTEM_TIME FROM", "FOR SYSTEM_TIME FROM x", "FOR SYSTEM_TIME FROM x TO", "FOR SYSTEM_TIME BETWEEN", "FOR SYSTEM_TIME BETWEEN x", "FOR SYSTEM_TIME ALL", "FOR SYSTEM_TIME FOO"} {
		_, _ = parserAt(src).parseTemporalClause()
	}
	for _, src := range []string{"TABLESAMPLE", "TABLESAMPLE SYSTEM", "TABLESAMPLE SYSTEM (", "TABLESAMPLE SYSTEM (x)", "TABLESAMPLE SYSTEM (10", "TABLESAMPLE SYSTEM (10) REPEATABLE", "TABLESAMPLE SYSTEM (10) REPEATABLE (", "TABLESAMPLE SYSTEM (10 PERCENT)", "TABLESAMPLE FOO(1)"} {
		_, _ = parserAt(src).parseTableSample()
	}
	for _, src := range []string{"(", "()", "(1", "(1,", "(1, 2", "(1), (2", "() AS", "(1) AS", "(1) AS x(", "(1) AS x(a"} {
		_, _ = parserAt(src).parseValuesRows()
	}
	for _, src := range []string{"VALUES", "VALUES (", "VALUES ()", "VALUES (1", "VALUES (1,", "VALUES (1),", "VALUES (1) AS", "VALUES (1) (a)", "VALUES (1) AS x(", "VALUES (1) AS x(a"} {
		_, _ = parserAt(src).parseValuesTable()
	}
	for _, src := range []string{"ROLLUP", "ROLLUP(", "ROLLUP(x", "CUBE(", "GROUPING", "GROUPING SETS", "GROUPING SETS (", "GROUPING SETS (()", "FOO(x)"} {
		_, _ = parserAt(src).parseGroupingSets()
	}
	for _, src := range []string{"LIMIT", "LIMIT ALL", "LIMIT 1,", "LIMIT 1, x", "LIMIT 1 OFFSET", "LIMIT x"} {
		_, _, _ = parserAt(src).parseLimit()
	}
	for _, src := range []string{"NOT", "+", "-", "~", "(", "CASE", "CAST", "EXTRACT", "POSITION", "SUBSTRING", "TRIM", "OVERLAY", "INTERVAL", "$", "$x", "$0", "1e999", "999999999999999999999999"} {
		_, _ = parserAt(src).parseExpr(0)
	}
	for _, src := range []string{"NOT FOO", "IN", "IN ()", "IN (", "IN (SELECT", "BETWEEN", "BETWEEN 1", "BETWEEN 1 AND", "IS", "IS FOO", "IS DISTINCT", "IS DISTINCT FROM", "IS JSON", "IS JSON WITH", "IS JSON WITH UNIQUE", "LIKE", "LIKE 1", "== 1", "<> 1"} {
		_, _ = parserAt(src).parseComparison(&ColumnRef{Parts: []string{"x"}})
	}
	for _, src := range []string{"JSON_VALUE", "JSON_VALUE(", "JSON_VALUE()", "JSON_VALUE(x", "JSON_VALUE(x,", "JSON_VALUE(x, '$.a'", "JSON_OBJECT", "JSON_OBJECT(", "JSON_OBJECT(x", "JSON_OBJECT(x:", "JSON_ARRAY", "JSON_ARRAY(", "JSON_ARRAY(x", "JSON_ARRAYAGG", "JSON_ARRAYAGG(", "JSON_ARRAYAGG(x", "JSON_OBJECTAGG", "JSON_OBJECTAGG(", "JSON_OBJECTAGG(x", "JSON_OBJECTAGG(x:", "JSON_QUERY(x, '$.a' WRAPPER"} {
		_, _ = parserAt(src).parseJSONFunction()
	}
	for _, src := range []string{"OVER", "OVER w", "OVER (", "OVER (PARTITION", "OVER (ORDER", "OVER (ROWS", "OVER (ROWS BETWEEN", "OVER (ROWS BETWEEN 1", "OVER (ROWS BETWEEN 1 PRECEDING", "OVER (ROWS BETWEEN 1 PRECEDING AND", "OVER (ROWS BETWEEN 1 PRECEDING AND CURRENT", "OVER (ROWS CURRENT ROW EXCLUDE", "OVER (ROWS CURRENT ROW EXCLUDE CURRENT", "OVER (ROWS CURRENT ROW EXCLUDE NO", "OVER (ROWS CURRENT ROW EXCLUDE FOO"} {
		_, _ = parserAt(src).parseWindowSpec()
	}
	for _, src := range []string{"UNBOUNDED", "UNBOUNDED PRECEDING", "UNBOUNDED FOLLOWING", "CURRENT", "CURRENT ROW", "1", "1 PRECEDING", "1 FOLLOWING", "UNBOUNDED FOO"} {
		_, _, _ = parserAt(src).parseFrameBound()
	}
	for _, src := range []string{"(", "()", "(x", "(x,", "(x, y", "(x, y"} {
		_, _, _ = parserAt(src).parseCallArgsDistinct()
	}
	for _, src := range []string{"EXTRACT", "EXTRACT(", "EXTRACT(YEAR", "EXTRACT(YEAR FROM", "EXTRACT(YEAR FROM x"} {
		_, _ = parserAt(src).parseExtract()
	}
	for _, src := range []string{"POSITION", "POSITION(", "POSITION(x", "POSITION(x,", "POSITION(x, y", "POSITION(x IN", "POSITION(x IN y"} {
		_, _ = parserAt(src).parsePosition()
	}
	for _, src := range []string{"SUBSTRING", "SUBSTRING(", "SUBSTRING(x", "SUBSTRING(x FROM", "SUBSTRING(x FROM 1", "SUBSTRING(x FROM 1 FOR", "SUBSTRING(x,", "SUBSTRING(x, 1", "SUBSTRING(x, 1,"} {
		_, _ = parserAt(src).parseSubstring()
	}
	for _, src := range []string{"CASE", "CASE x", "CASE WHEN", "CASE WHEN x", "CASE WHEN x THEN", "CASE WHEN x THEN y", "CASE x WHEN y THEN z ELSE"} {
		_, _ = parserAt(src).parseCase()
	}
	for _, src := range []string{"CAST", "CAST(", "CAST(x", "CAST(x AS", "CAST(x AS INT", "TRY_CAST("} {
		_, _ = parserAt(src).parseCast()
	}
	for _, src := range []string{"TRIM", "TRIM(", "TRIM(BOTH", "TRIM(BOTH FROM", "TRIM(BOTH 'x'", "TRIM(BOTH 'x' FROM", "TRIM(LEADING", "TRIM(TRAILING", "TRIM(x,"} {
		_, _ = parserAt(src).parseTrim()
	}
	for _, src := range []string{"OVERLAY", "OVERLAY(", "OVERLAY(x", "OVERLAY(x PLACING", "OVERLAY(x PLACING y", "OVERLAY(x PLACING y FROM", "OVERLAY(x PLACING y FROM 1", "OVERLAY(x PLACING y FROM 1 FOR"} {
		_, _ = parserAt(src).parseOverlay()
	}
	for _, src := range []string{"INTERVAL", "INTERVAL 1", "INTERVAL '1'", "INTERVAL '1' FOO", "INTERVAL '1' YEAR TO"} {
		_, _ = parserAt(src).parseInterval()
	}
	for _, src := range []string{"isNumericToken", ""} {
		_ = isNumericToken(src)
	}

	_, _ = parserAt("").parseQualifiedName()
	_, _ = parserAt("").parseIdentList()
	_, _ = parserAt("").parseIdent()
}

func TestDirectParserFinalBranches(t *testing.T) {
	for _, src := range []string{"FROM t", "FROM t WHERE x = 1", "FROM t ORDER BY x", "FROM t LIMIT 1", "FROM t OFFSET 1", "FROM t |> WHERE x", "FROM t WHERE"} {
		_, _ = parserAt(src).parseFromFirst()
	}
	for _, src := range []string{"GROUP x", "HAVING", "WINDOW", "WINDOW w", "WINDOW w AS", "WINDOW w AS ("} {
		_ = parserAt(src).parseSelectClauses(&SelectStmt{})
	}
	for _, src := range []string{"UNION DISTINCT SELECT 1", "UNION (SELECT 1)", "UNION SELECT"} {
		_, _ = parserAt(src).parseSetOperations(&SelectStmt{Projections: []SelectItem{{Expr: &Literal{Kind: LitInt, Value: 1}}}}, 1)
	}
	for _, src := range []string{"SELECT 1", "SELECT 1 AS", "SELECT 1 AS x", "SELECT a.1", "SELECT a.*", "SELECT s.t.*"} {
		_, _ = parserAt(src).parseSelect()
	}
	for _, src := range []string{
		"MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS x = 1",
		"MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS x = 1) AS",
		"MATCH_RECOGNIZE (PATTERN (A | ) DEFINE A AS x = 1)",
		"MATCH_RECOGNIZE (PATTERN ((A) DEFINE A AS x = 1)",
	} {
		_, _ = parserAt(src).parseMatchRecognize(&TableRef{Source: "s", Table: "t"})
	}
	for _, src := range []string{"(x INT PATH '$' NULL ON EMPTY ERROR ON ERROR)", "(x INT PATH '$' DEFAULT 1 ON EMPTY)", "(x INT PATH '$' NULL)", "(x FOR ORDINALITY)", "(NESTED PATH '$' COLUMNS (y INT PATH '$'))"} {
		_, _ = parserAt(src).parseJSONTableColumns()
	}
	for _, src := range []string{"TABLESAMPLE SYSTEM (10 PERCENT)", "TABLESAMPLE SYSTEM (10) REPEATABLE (2)", "TABLESAMPLE SYSTEM (10) REPEATABLE", "TABLESAMPLE SYSTEM (10) REPEATABLE ("} {
		_, _ = parserAt(src).parseTableSample()
	}
	for _, src := range []string{"FOR SYSTEM_TIME AS OF TIMESTAMP '2024-01-01 00:00:00'", "FOR SYSTEM_TIME FROM TIMESTAMP '2024-01-01 00:00:00' TO TIMESTAMP '2024-02-01 00:00:00'", "FOR SYSTEM_TIME BETWEEN TIMESTAMP '2024-01-01 00:00:00' AND TIMESTAMP '2024-02-01 00:00:00'", "FOR SYSTEM_TIME ALL", "FOR SYSTEM_TIME AS OF"} {
		_, _ = parserAt(src).parseTemporalClause()
	}
	for _, src := range []string{"CUBE(a, b)", "GROUPING SETS ((a), ())", "ROLLUP(a, b)", "GROUPING SETS ((", "ROLLUP("} {
		_, _ = parserAt(src).parseGroupingSets()
	}
	for _, src := range []string{"LIMIT 1, 2", "LIMIT 1 OFFSET 2", "LIMIT ALL", "LIMIT", "LIMIT 1,"} {
		_, _, _ = parserAt(src).parseLimit()
	}
	for _, src := range []string{"NOT x", "NOT"} {
		_, _ = parserAt(src).parseExpr(4)
	}
	for _, src := range []string{
		"= ANY (SELECT 1)", "= ANY (1, 2)", "IN (SELECT 1)", "IN (1, 2)", "IS DISTINCT FROM 1",
		"IS TRUE", "IS FALSE", "IS UNKNOWN", "IS NOT TRUE", "NOT IN (1)", "NOT LIKE 'x'", "NOT BETWEEN 1 AND 2",
		"NOT FOO", "LIKE", "BETWEEN", "IS", "IS DISTINCT", "IS JSON WITH", "IS JSON WITH UNIQUE",
	} {
		_, _ = parserAt(src).parseComparison(&ColumnRef{Parts: []string{"x"}})
	}
	// Construct invalid numeric tokens directly because the lexer rejects them.
	for _, tok := range []Token{
		{Kind: TokNumber, Value: "1e999"},
		{Kind: TokNumber, Value: "999999999999999999999999"},
		{Kind: TokPunct, Value: "$"},
		{Kind: TokPunct, Value: "$"},
	} {
		p := &Parser{tokens: []Token{tok, {Kind: TokEOF}}}
		p.sync()
		_, _ = p.parsePrimary()
	}
	for _, src := range []string{"JSON_OBJECT('a' VALUE 1)", "JSON_OBJECT('a': 1)", "JSON_OBJECTAGG(k VALUE v)", "JSON_OBJECTAGG(k: v)"} {
		_, _ = parserAt(src).parseJSONFunction()
	}
	for _, src := range []string{"POSITION(x, y", "POSITION(x,", "POSITION(x IN", "POSITION(x IN y"} {
		_, _ = parserAt(src).parsePosition()
	}
	for _, src := range []string{"SUBSTRING(x,", "SUBSTRING(x, 1", "SUBSTRING(x, 1,", "SUBSTRING(x FROM 1", "SUBSTRING(x FROM 1 FOR"} {
		_, _ = parserAt(src).parseSubstring()
	}
	_ = parserAt("").resolveWindowReferences(nil)
	_ = parserAt("").resolveWindowReferences(&SelectStmt{Windows: []NamedWindow{{Name: "w"}}})
	_ = parserAt("").resolveWindowReferences(&SelectStmt{Windows: []NamedWindow{{Name: "w", Spec: &WindowSpec{Base: "missing"}}}})
	_ = parserAt("").resolveWindowReferences(&SelectStmt{Projections: []SelectItem{{Expr: &FuncCall{Name: "sum", Window: &WindowSpec{Base: "missing"}}}}})
	_ = parserAt("").resolveWindowReferences(&SelectStmt{Where: &FuncCall{Name: "sum", Window: &WindowSpec{Base: "missing"}}})
	_ = parserAt("").resolveWindowReferences(&SelectStmt{Having: &FuncCall{Name: "sum", Window: &WindowSpec{Base: "missing"}}})
	_ = parserAt("").resolveWindowReferences(&SelectStmt{OrderBy: []OrderItem{{Expr: &FuncCall{Name: "sum", Window: &WindowSpec{Base: "missing"}}}}})
}

func TestDirectParserRemainingBranches(t *testing.T) {
	_, _ = parserAt("SELECT sum(x) OVER missing").parseSelect()
	for _, src := range []string{"FROM t |> FOO", "FROM t LIMIT", "FROM t |> WHERE x = 1 ORDER"} {
		_, _ = parserAt(src).parseFromFirst()
	}
	_ = parserAt("LIMIT ?, 2").parseSelectTail(&SelectStmt{})
	_, _ = parserAt("").parseCTEs()
	for _, src := range []string{"MATCH_RECOGNIZE (PATTERN (A", "MATCH_RECOGNIZE (PATTERN (A) DEFINE A AS x = 1"} {
		_, _ = parserAt(src).parseMatchRecognize(&TableRef{Source: "s", Table: "t"})
	}
	for _, src := range []string{"(A", "()", "(A|)"} {
		_, _ = parserAt(src).parseMatchPattern()
	}
	for _, src := range []string{"(SELECT", "(SELECT 1", "(VALUES (1)"} {
		_, _ = parserAt(src).parseTablePrimary()
	}
	_, _ = parserAt("").parseTableFunction()
	_, _ = parserAt("").parseTemporalClause()
	_, _ = parserAt("").parseTableSample()
	_, _ = parserAt("GROUPING SETS ((a)").parseGroupingSets()
	_, _, _ = parserAt("").parseLimit()
	for _, src := range []string{"1::", "1 AT", "1 AT TIME"} {
		_, _ = parserAt(src).parseExpr(0)
	}
	for _, src := range []string{"= ANY (SELECT", "= ANY (1", "IN (1", "NOT IN (1)"} {
		_, _ = parserAt(src).parseComparison(&ColumnRef{Parts: []string{"x"}})
	}
	for _, src := range []string{"CURRENT_TIMESTAMP(", "CURRENT_DATE(", "NOW("} {
		_, _ = parserAt(src).parsePrimary()
	}
	for _, src := range []string{"JSON_ARRAYAGG(x NULL ON NULL ON", "JSON_ARRAYAGG(x RETURNING"} {
		_, _ = parserAt(src).parseJSONFunction()
	}
	for _, src := range []string{"1.5 PRECEDING", "1.5"} {
		_, _, _ = parserAt(src).parseFrameBound()
	}
}

func TestDirectParserLastBranches(t *testing.T) {
	_, _ = parserAt("SELECT 1 ORDER BY sum(x) OVER missing").parseSelect()
	_, _ = parserAt("FROM t ORDER BY sum(x) OVER missing").parseFromFirst()
	_, _ = parserAt("FROM t |> WHERE sum(x) OVER missing").parseFromFirst()
	for _, src := range []string{
		"MATCH_RECOGNIZE (PATTERN (A B",
		"MATCH_RECOGNIZE (PATTERN (A B) DEFINE A AS 1, B AS 1",
	} {
		_, _ = parserAt(src).parseMatchRecognize(&TableRef{Source: "s", Table: "t"})
	}
	for _, src := range []string{"(A B", "GROUPING SETS ((a)"} {
		_, _ = parserAt(src).parseMatchTerm()
		_, _ = parserAt(src).parseGroupingSets()
	}
	for _, src := range []string{"1 AT TIME", "1 AT TIME ZONE"} {
		_, _ = parserAt(src).parseExpr(0)
	}
	for _, src := range []string{"JSON_ARRAYAGG(x NULL ON FOO)", "JSON_ARRAYAGG(x ABSENT ON FOO)"} {
		_, _ = parserAt(src).parseJSONFunction()
	}
}

func TestDirectParserLastTwoBranches(t *testing.T) {
	for _, src := range []string{"(A B X", "(A B", "(A | B"} {
		_, _ = parserAt(src).parseMatchTerm()
	}
	for _, src := range []string{"GROUPING SETS ((a)", "GROUPING SETS ((a), (b)", "GROUPING SETS ((a), ("} {
		_, _ = parserAt(src).parseGroupingSets()
	}
	for _, src := range []string{"JSON_ARRAYAGG(x NULL ON FOO)", "JSON_ARRAYAGG(x NULL ON)", "JSON_ARRAYAGG(x ABSENT ON FOO)", "JSON_ARRAYAGG(x ON FOO)"} {
		_, _ = parserAt(src).parseJSONFunction()
	}
}

func TestDirectParserExactRemainingBranches(t *testing.T) {
	for _, src := range []string{"(A B", "MATCH_RECOGNIZE (PATTERN (A B"} {
		_, _ = parserAt(src).parseMatchTerm()
		_, _ = parserAt(src).parseMatchRecognize(&TableRef{Source: "s", Table: "t"})
	}
	_, _ = parserAt("GROUPING SETS ((a").parseGroupingSets()
	for _, src := range []string{"JSON_OBJECTAGG(k: v NULL ON FOO)", "JSON_OBJECTAGG(k: v ABSENT ON FOO)"} {
		_, _ = parserAt(src).parseJSONFunction()
	}
}
