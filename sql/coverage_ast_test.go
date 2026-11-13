package sql

import (
	"strings"
	"testing"
)

func TestASTMarkerInterfaces(t *testing.T) {
	for _, n := range []Statement{&SelectStmt{}, &ShowStmt{}, &DescribeStmt{}, &ExplainStmt{}, &ValuesStmt{}, &TableStmt{}} {
		n.stmtNode()
	}
	for _, n := range []TableExpr{&TableRef{}, &DerivedTable{}, &LateralTable{}, &TableFunction{}, &ValuesTable{}, &JSONTable{}, &MatchRecognize{}, &JoinExpr{}} {
		n.tableNode()
	}
	for _, n := range []PipeStage{&PipeWhere{}, &PipeSelect{}, &PipeAggregate{}, &PipeOrder{}, &PipeLimit{}, &PipeJoin{}, &PipeDistinct{}} {
		n.pipeNode()
	}
	for _, n := range []Expr{
		&Parameter{}, &Literal{}, &ColumnRef{}, &StarExpr{}, &UnaryExpr{}, &BinaryExpr{}, &CompareExpr{},
		&InExpr{}, &QuantifiedExpr{}, &BetweenExpr{}, &IsExpr{}, &FuncCall{}, &CaseExpr{}, &CastExpr{},
		&IntervalExpr{}, &ExistsExpr{}, &SubqueryExpr{}, &AtTimeZoneExpr{},
	} {
		n.exprNode()
	}
}

func TestASTStringBranches(t *testing.T) {
	lit := &Literal{Kind: LitInt, Value: 1}
	col := &ColumnRef{Parts: []string{"t", "x"}}
	null := &Literal{Kind: LitNull}
	str := &Literal{Kind: LitString, Value: "a'b"}
	boolTrue := &Literal{Kind: LitBool, Value: true}
	boolFalse := &Literal{Kind: LitBool, Value: false}
	star := &StarExpr{}
	qualifiedStar := &StarExpr{Table: "s.t"}
	paramQ := &Parameter{Index: 1, Style: "?"}
	paramD := &Parameter{Index: 2, Style: "$"}
	cmp := &CompareExpr{Op: "=", Left: col, Right: lit}
	cmpNeg := &CompareExpr{Op: "LIKE", Left: col, Right: str, Negated: true}
	in := &InExpr{Left: col, List: []Expr{lit, str}}
	inSub := &InExpr{Left: col, Subquery: &SelectStmt{Projections: []SelectItem{{Expr: lit}}}, Negated: true}
	quant := &QuantifiedExpr{Op: ">", Left: col, List: []Expr{lit}}
	quantSub := &QuantifiedExpr{Op: "=", Left: col, Subquery: &SelectStmt{Projections: []SelectItem{{Expr: lit}}}, All: true}
	between := &BetweenExpr{Left: col, Low: lit, High: lit, Negated: true}
	isNull := &IsExpr{Left: col, What: "null"}
	isDistinct := &IsExpr{Left: col, Right: lit, What: "distinct", Negated: true}
	isJSON := &IsExpr{Left: col, What: "json", JSONType: "ARRAY", JSONUniqueSet: true, JSONUnique: true}
	isJSONNoUnique := &IsExpr{Left: col, What: "json", JSONType: "OBJECT", JSONUniqueSet: true}
	wsBase := &WindowSpec{Base: "w"}
	wsFull := &WindowSpec{
		PartitionBy: []Expr{col}, OrderBy: []OrderItem{{Expr: col, Desc: true, Nulls: "first"}},
		FrameKind: "ROWS", FrameStart: "unbounded_preceding", FrameEnd: "following", FrameEndOffset: 2,
		FrameExclusion: "current_row",
	}
	wsStart := &WindowSpec{FrameKind: "GROUPS", FrameStart: "preceding", FrameStartOffset: 3, FrameEnd: "current_row"}
	if (&TemporalClause{Mode: "bad"}).String() != "" {
		t.Fatal("invalid temporal clause should have no format")
	}
	for i, s := range []string{
		(&TemporalClause{Mode: "all"}).String(),
		(&TemporalClause{Mode: "as_of", AsOf: col}).String(),
		(&TemporalClause{Mode: "from_to", From: col, To: col}).String(),
		(&TemporalClause{Mode: "between", From: col, To: col}).String(),
		(&TemporalClause{Mode: "all"}).String(),
		paramQ.String(), paramD.String(),
		null.String(), boolTrue.String(), boolFalse.String(), str.String(), lit.String(),
		col.String(), star.String(), qualifiedStar.String(),
		(&UnaryExpr{Op: "NOT", Expr: col}).String(),
		(&BinaryExpr{Op: "+", Left: col, Right: lit}).String(),
		cmp.String(), cmpNeg.String(), in.String(), inSub.String(), quant.String(), quantSub.String(),
		between.String(), isNull.String(), isDistinct.String(), isJSON.String(), isJSONNoUnique.String(),
		(&IntervalExpr{Value: "1 day"}).String(),
		(&IntervalExpr{Value: "1", Qualifier: "DAY"}).String(),
		(&AtTimeZoneExpr{Expr: col, Zone: str}).String(),
		(&CaseExpr{Base: col, Whens: []WhenClause{{Cond: col, Then: lit}}, Else: null}).String(),
		(&CastExpr{Expr: col, Type: "INT"}).String(),
		(&CastExpr{Expr: col, Type: "INT", Safe: true}).String(),
		(&ExistsExpr{Query: &SelectStmt{Projections: []SelectItem{{Expr: lit}}}}).String(),
		(&SubqueryExpr{Query: &SelectStmt{Projections: []SelectItem{{Expr: lit}}}}).String(),
		wsBase.String(), wsFull.String(), wsStart.String(),
		formatFrameBound("unbounded_preceding", 0), formatFrameBound("current_row", 0),
		formatFrameBound("unbounded_following", 0), formatFrameBound("preceding", 1),
		formatFrameBound("following", 1), formatFrameBound("other", 0),
	} {
		if s == "" {
			t.Fatalf("unexpected empty formatter output at %d", i)
		}
	}
	for _, f := range []*FuncCall{
		{Name: "lower", Args: []Expr{col}},
		{Name: "count", Star: true},
		{Name: "count", Args: []Expr{col}, Distinct: true},
		{Name: "extract", Args: []Expr{str, col}},
		{Name: "extract", Args: []Expr{col, col}},
		{Name: "position", Args: []Expr{col, col}},
		{Name: "overlay", Args: []Expr{col, col, lit}},
		{Name: "overlay", Args: []Expr{col, col, lit, lit}},
		{Name: "sum", Args: []Expr{col}, WithinGroup: []OrderItem{{Expr: col}}, Filter: col, Window: wsFull},
		{Name: "json_value", Args: []Expr{col}, JSONPath: str, JSONOnError: "error", JSONOnEmpty: "null"},
		{Name: "json_query", Args: []Expr{col}, JSONPath: str, JSONReturning: "VARCHAR"},
		{Name: "json_exists", Args: []Expr{col}, JSONPath: str, JSONUnique: true},
		{Name: "json_object", JSONPairs: []JSONPair{{Key: str, Value: lit}}, JSONNullBehavior: "absent", JSONUnique: true},
		{Name: "json_array", Args: []Expr{col}, JSONNullBehavior: "null", JSONReturning: "JSON"},
		{Name: "json_arrayagg", Args: []Expr{col}, JSONOrder: []OrderItem{{Expr: col}}},
		{Name: "json_objectagg", JSONPairs: []JSONPair{{Key: str, Value: lit}}, JSONOrder: []OrderItem{{Expr: col}}},
	} {
		_ = f.String()
	}
}

func TestASTSelectFormattingBranches(t *testing.T) {
	col := &ColumnRef{Parts: []string{"x"}}
	lit := &Literal{Kind: LitInt, Value: 1}
	sel := &SelectStmt{
		Distinct:    true,
		Projections: []SelectItem{{Expr: col, Alias: "a"}, {Star: true, StarTable: "s.t"}},
		From: []TableExpr{
			&TableRef{Source: "s", Table: "t", Alias: "t", Temporal: &TemporalClause{Mode: "all"}, Sample: &TableSample{Method: "SYSTEM", Percent: 10, Seed: int64Ptr(1)}},
			&DerivedTable{Query: &SelectStmt{Projections: []SelectItem{{Expr: lit}}}, Alias: "d", Columns: []string{"x"}},
			&ValuesTable{Rows: [][]Expr{{lit}}, Alias: "v", Columns: []string{"x"}},
			&LateralTable{Table: &TableFunction{Name: "unnest", Args: []Expr{col}, Alias: "u", Columns: []string{"x"}}},
			&JSONTable{Expr: col, Path: lit, Columns: []JSONTableColumn{{Name: "x", Type: "INT", Path: lit, Ordinality: true}, {NestedPath: lit, Nested: []JSONTableColumn{{Name: "y", Type: "TEXT", Path: lit}}}}, Alias: "j"},
			&MatchRecognize{Input: &TableRef{Source: "s", Table: "t"}, Pattern: &MatchPattern{Kind: "group", Children: []*MatchPattern{{Kind: "alt", Children: []*MatchPattern{{Kind: "var", Var: "A"}, {Kind: "var", Var: "B", Quantifier: "+", Lazy: true}}}}, Quantifier: "?"}, Alias: "m"},
			&JoinExpr{Left: &TableRef{Source: "s", Table: "a"}, Right: &TableRef{Source: "s", Table: "b"}, Type: JoinLeft, On: col, Natural: true, Using: []string{"id"}},
		},
		Where: col, GroupBy: []Expr{col}, GroupingSets: [][]Expr{{col}}, Having: col,
		Windows: []NamedWindow{{Name: "w", Spec: &WindowSpec{PartitionBy: []Expr{col}}}},
		OrderBy: []OrderItem{{Expr: col, Desc: true, Nulls: "last"}},
		Limit:   intPtr(1), Offset: intPtr(0), FetchWithTies: true,
		SetOps: []SetOperation{{Op: "UNION", All: true, Right: &SelectStmt{Projections: []SelectItem{{Expr: lit}}}}},
		Pipes: []PipeStage{
			&PipeWhere{Expr: col}, &PipeSelect{Items: []SelectItem{{Expr: col}}, Distinct: true},
			&PipeAggregate{Items: []SelectItem{{Expr: col}}, GroupBy: []Expr{col}}, &PipeOrder{Items: []OrderItem{{Expr: col}}},
			&PipeLimit{Limit: 1, Offset: 1}, &PipeJoin{Right: &TableRef{Source: "s", Table: "t"}, Type: JoinLeft, On: col},
			&PipeJoin{Right: &TableRef{Source: "s", Table: "t"}, Type: JoinInner, Using: []string{"id"}, Natural: true}, &PipeDistinct{},
		},
	}
	out := FormatSelect(sel)
	if !strings.Contains(out, "SELECT DISTINCT") || !strings.Contains(out, "WINDOW") {
		t.Fatalf("format = %s", out)
	}
	for _, item := range []SelectItem{{Alias: "a"}, {Star: true}, {Expr: col}, {Expr: &FuncCall{Name: "LOWER"}}, {Expr: lit}} {
		_ = Label(item)
	}
}

func TestASTWalkAndPredicates(t *testing.T) {
	col := &ColumnRef{Parts: []string{"x"}}
	lit := &Literal{Kind: LitInt, Value: 1}
	query := &SelectStmt{Projections: []SelectItem{{Expr: lit}}}
	exprs := []Expr{
		&UnaryExpr{Expr: col}, &BinaryExpr{Left: col, Right: lit}, &CompareExpr{Left: col, Right: lit},
		&InExpr{Left: col, List: []Expr{lit}}, &QuantifiedExpr{Left: col, List: []Expr{lit}},
		&BetweenExpr{Left: col, Low: lit, High: lit}, &IsExpr{Left: col, Right: lit},
		&FuncCall{Args: []Expr{col}, Filter: col, WithinGroup: []OrderItem{{Expr: col}}, JSONOrder: []OrderItem{{Expr: col}}, JSONPairs: []JSONPair{{Key: col, Value: lit}}, JSONPath: col, Window: &WindowSpec{PartitionBy: []Expr{col}, OrderBy: []OrderItem{{Expr: col}}}},
		&CaseExpr{Base: col, Whens: []WhenClause{{Cond: col, Then: lit}}, Else: lit},
		&CastExpr{Expr: col}, &ExistsExpr{Query: query}, &SubqueryExpr{Query: query}, &AtTimeZoneExpr{Expr: col, Zone: lit},
	}
	for _, e := range exprs {
		if !WalkExpr(e, func(Expr) bool { return true }) {
			t.Fatalf("walk failed for %T", e)
		}
		_ = ContainsWindow(e)
		_ = ContainsAggregate(e)
	}
	_ = WalkExpr(nil, func(Expr) bool { return true })
	_ = WalkExpr(col, func(Expr) bool { return false })
	_ = WalkExpr(&BinaryExpr{Left: col, Right: lit}, func(e Expr) bool { _, ok := e.(*ColumnRef); return !ok })
	if !ContainsWindow(&FuncCall{Name: "sum", Window: &WindowSpec{}}) || ContainsWindow(col) {
		t.Fatal("ContainsWindow failed")
	}
	if !ContainsAggregate(&FuncCall{Name: "sum"}) || ContainsAggregate(&FuncCall{Name: "sum", Window: &WindowSpec{}}) {
		t.Fatal("ContainsAggregate failed")
	}
	if !IsWindow(&FuncCall{Window: &WindowSpec{}}) || IsWindow(nil) {
		t.Fatal("IsWindow failed")
	}
	if !IsAggregate("SUM") || IsAggregate("lower") {
		t.Fatal("IsAggregate failed")
	}
}

func TestASTFormatStatementBranches(t *testing.T) {
	col := &ColumnRef{Parts: []string{"x"}}
	stmt := &SelectStmt{Projections: []SelectItem{{Expr: col}}}
	show := &ShowStmt{Target: "tables", Source: "s"}
	showCols := &ShowStmt{Target: "columns", Source: "s", Table: "t"}
	showSources := &ShowStmt{Target: "sources"}
	showUnknown := &ShowStmt{Target: "other"}
	desc := &DescribeStmt{Source: "s", Table: "t"}
	for _, s := range []Statement{stmt, &ValuesStmt{Rows: [][]Expr{{col}}}, &TableStmt{Ref: &TableRef{Source: "s", Table: "t"}}, show, showCols, showSources, showUnknown, desc, &ExplainStmt{Stmt: stmt}, &ExplainStmt{Stmt: show}} {
		if FormatStatement(s) == "" {
			t.Fatalf("no format for %T", s)
		}
	}
	_ = FormatShow(nil)
	_ = FormatDescribe(nil)
	_ = FormatExplain(nil)
	_ = FormatExplain(&ExplainStmt{})
	if FormatStatement(nil) != "" {
		t.Fatal("nil statement formatted")
	}
	if FormatTable(nil) != "TABLE" {
		t.Fatal("nil table")
	}
	_ = FormatTableSample(nil)
	_ = FormatTableRef(&TableRef{Source: "s", Table: "t", Temporal: &TemporalClause{Mode: "all"}})
	if FormatIdent("") != `""` || FormatIdent("1x") != `"1x"` || FormatIdent("ok") != "ok" {
		t.Fatal("FormatIdent failed")
	}
}

func intPtr(n int) *int       { return &n }
func int64Ptr(n int64) *int64 { return &n }
