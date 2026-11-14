package sql

import (
	"strings"
	"testing"
)

func TestMoreASTFormattingBranches(t *testing.T) {
	col := &ColumnRef{Parts: []string{"x"}}
	lit := &Literal{Kind: LitInt, Value: 1}
	var nilTemporal *TemporalClause
	if nilTemporal.String() != "" {
		t.Fatal("nil temporal")
	}
	ws := &WindowSpec{FrameKind: "ROWS", FrameStart: "current_row"}
	if ws.String() == "" {
		t.Fatal("empty window spec")
	}
	for _, f := range []*FuncCall{
		{Name: "json_value"},
		{Name: "json_query", Args: []Expr{col}},
		{Name: "json_exists", Args: []Expr{col}},
		{Name: "json_object", JSONPairs: []JSONPair{{Key: col, Value: lit}}, JSONReturning: "VARCHAR", JSONUnique: true},
		{Name: "json_array", Args: []Expr{col}, JSONNullBehavior: "absent"},
		{Name: "json_arrayagg", Args: []Expr{col}},
		{Name: "json_arrayagg", Args: []Expr{col, col}},
		{Name: "json_objectagg", JSONPairs: []JSONPair{{Key: col, Value: lit}}},
		{Name: "json_objectagg", JSONPairs: []JSONPair{}},
	} {
		if f.String() == "" {
			t.Fatalf("empty formatter for %s", f.Name)
		}
	}
	if formatRowCount(nil, nil) != "ALL" {
		t.Fatal("formatRowCount")
	}
	// GROUP BY (not grouping sets), empty projections, and a non-lateral join.
	sel := &SelectStmt{Projections: []SelectItem{{Expr: col}}, From: []TableExpr{&JoinExpr{Left: &TableRef{Source: "s", Table: "a"}, Right: &TableRef{Source: "s", Table: "b"}, Type: JoinCross}}, GroupBy: []Expr{col}}
	if FormatSelect(sel) == "" {
		t.Fatal("empty select format")
	}
	if FormatSelect(&SelectStmt{}) == "" {
		t.Fatal("empty empty-select format")
	}
	if FormatShow(&ShowStmt{Target: "tables"}) == "" {
		t.Fatal("empty show format")
	}
	if FormatExplain(&ExplainStmt{Stmt: &ShowStmt{Target: "unknown"}}) == "" {
		t.Fatal("empty explain format")
	}
	if FormatPipe(nil) != "" {
		t.Fatal("unexpected pipe format")
	}
	_ = formatMatchPattern(nil)
	_ = formatMatchPattern(&MatchPattern{Kind: "var", Var: "A", Quantifier: "+", Lazy: true})
	_ = formatMatchPattern(&MatchPattern{Kind: "seq", Quantifier: "*", Lazy: true, Children: []*MatchPattern{{Kind: "var", Var: "A"}}})
}

func TestWalkExprFalseBranches(t *testing.T) {
	col := &ColumnRef{Parts: []string{"x"}}
	lit := &Literal{Kind: LitInt, Value: 1}
	cases := []struct {
		name   string
		expr   Expr
		target Expr
	}{
		{"unary", &UnaryExpr{Expr: col}, col},
		{"binary", &BinaryExpr{Left: col, Right: lit}, lit},
		{"compare", &CompareExpr{Left: col, Right: lit}, lit},
		{"in", &InExpr{Left: col, List: []Expr{lit}}, lit},
		{"quantified", &QuantifiedExpr{Left: col, List: []Expr{lit}}, lit},
		{"between", &BetweenExpr{Left: col, Low: lit, High: lit}, lit},
		{"is", &IsExpr{Left: col, Right: lit}, lit},
		{"func_args", &FuncCall{Args: []Expr{col}}, col},
		{"func_filter", &FuncCall{Args: []Expr{lit}, Filter: col}, col},
		{"func_within", &FuncCall{WithinGroup: []OrderItem{{Expr: col}}}, col},
		{"func_jsonorder", &FuncCall{JSONOrder: []OrderItem{{Expr: col}}}, col},
		{"func_jsonpair_key", &FuncCall{JSONPairs: []JSONPair{{Key: col, Value: lit}}}, col},
		{"func_jsonpair_value", &FuncCall{JSONPairs: []JSONPair{{Key: lit, Value: col}}}, col},
		{"func_jsonpath", &FuncCall{JSONPath: col}, col},
		{"func_partition", &FuncCall{Window: &WindowSpec{PartitionBy: []Expr{col}}}, col},
		{"func_order", &FuncCall{Window: &WindowSpec{OrderBy: []OrderItem{{Expr: col}}}}, col},
		{"case_base", &CaseExpr{Base: col, Whens: []WhenClause{{Cond: lit, Then: lit}}}, col},
		{"case_cond", &CaseExpr{Whens: []WhenClause{{Cond: col, Then: lit}}}, col},
		{"case_then", &CaseExpr{Whens: []WhenClause{{Cond: lit, Then: col}}}, col},
		{"case_else", &CaseExpr{Else: col}, col},
		{"cast", &CastExpr{Expr: col}, col},
	}
	for _, tc := range cases {
		stopped := false
		ok := WalkExpr(tc.expr, func(e Expr) bool {
			if e == tc.target {
				stopped = true
				return false
			}
			return true
		})
		if ok || !stopped {
			t.Errorf("%s: ok=%v stopped=%v", tc.name, ok, stopped)
		}
	}
}

func TestLexerStringAndScanBranches(t *testing.T) {
	for _, k := range []TokenKind{TokEOF, TokIdent, TokNumber, TokString, TokPunct, TokenKind(99)} {
		_ = k.String()
	}
	tokens, err := Tokenize(`'a\n\t\r\\\'x' "a""b" [a]]b] 1e+2 .5`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range tokens {
		if tok.Kind == TokString && !strings.Contains(tok.Value, "\n") {
			t.Fatalf("string escape missing: %q", tok.Value)
		}
	}
}
