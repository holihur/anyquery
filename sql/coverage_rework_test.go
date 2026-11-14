package sql

import "testing"

func expectParseError(t *testing.T, src string) {
	t.Helper()
	if _, err := Parse(src); err == nil {
		t.Fatalf("expected parse error for %q", src)
	}
}

func TestCoverageReworkBranches(t *testing.T) {
	if got := formatJSONPassingName(&ColumnRef{Parts: []string{"x"}}); got != "x" {
		t.Fatalf("passing name = %q", got)
	}
	col := &ColumnRef{Parts: []string{"x"}}
	lit := &Literal{Kind: LitInt, Value: 1}
	if WalkExpr(&FuncCall{JSONPassing: []JSONPair{{Key: col, Value: lit}}}, func(e Expr) bool { return e != col }) {
		t.Fatal("expected JSONPassing walk to stop")
	}
	if _, err := Parse("SELECT * FROM t LIMIT 1, 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("FROM t |> LIMIT 1 OFFSET 2"); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{
		"MATCH_RECOGNIZE (SUBSET =)",
		"MATCH_RECOGNIZE (SUBSET A)",
		"MATCH_RECOGNIZE (SUBSET A=(B),)",
		"MATCH_RECOGNIZE (PERMUTE)",
		"MATCH_RECOGNIZE (WITH FOO)",
		"MATCH_RECOGNIZE (WITH UNMATCHED FOO)",
	} {
		if _, err := parserAt(src).parseMatchRecognize(&TableRef{Source: "s", Table: "t"}); err == nil {
			t.Errorf("expected MATCH_RECOGNIZE error for %q", src)
		}
	}
	for _, src := range []string{
		"JSON_TABLE(x, '$.a' PASSING)",
		"JSON_TABLE(x, '$.a' PASSING y",
		"JSON_TABLE(x, '$.a' PASSING y AS",
		"JSON_TABLE(x, '$.a' COLUMNS (id INT PATH '$.id') PLAN",
		"JSON_TABLE(x, '$.a' COLUMNS (id INT PATH '$.id') PLAN (",
		"JSON_TABLE(x, '$.a' COLUMNS (id INT PATH '$.id') PLAN (id)",
		"JSON_TABLE(x, '$.a' COLUMNS (id INT PATH '$.id') ON",
	} {
		if _, err := parserAt(src).parseJSONTable(); err == nil {
			t.Errorf("expected JSON_TABLE error for %q", src)
		}
	}
	for _, src := range []string{"LIKE 'x' ESCAPE FROM", "LIKE 'x' ESCAPE", "SIMILAR TO", "SIMILAR 'x'"} {
		if _, err := parserAt(src).parseComparison(col); err == nil {
			t.Errorf("expected comparison error for %q", src)
		}
	}
	for _, src := range []string{"PASSING y AS", "PASSING y, z", "PASSING y,"} {
		var out []JSONPair
		err := parserAt(src).parseJSONPassing(&out)
		if src == "PASSING y, z" && err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	for _, src := range []string{"PATH '$.a'", "NULL ON EMPTY", "ON EMPTY NULL", "DEFAULT 1 ON EMPTY", "ON ERROR NULL", "ON FOO"} {
		col := &JSONTableColumn{Name: "x"}
		err := parserAt(src).parseJSONColumnOptions(col)
		if src == "ON FOO" && err == nil {
			t.Errorf("%s: expected error", src)
		}
	}
	for _, src := range []string{"DEFAULT ON ERROR", "NULL ON", "NULL FOO"} {
		fn := &FuncCall{}
		if err := parserAt(src).parseJSONOptions(fn); err == nil && src != "NULL FOO" {
			t.Errorf("%s: expected error", src)
		}
	}
	for _, src := range []string{"INTERVAL '1 day' PRECEDING", "INTERVAL '1 day' FOLLOWING"} {
		if _, _, _, err := parserAt(src).parseFrameBound(); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	if _, _, _, err := parserAt("INTERVAL").parseFrameBound(); err == nil {
		t.Fatal("expected INTERVAL frame error")
	}
}

func TestCoverageTailBranches(t *testing.T) {
	if _, err := Parse("SELECT * FROM t LIMIT ALL"); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"JSON_TABLE(x, '$.a' COLUMNS (id INT PATH '$.id') PLAN (id"} {
		if _, err := parserAt(src).parseJSONTable(); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
	if _, err := parserAt("f(x) IGNORE").parsePrimary(); err == nil {
		t.Error("expected IGNORE NULLS error")
	}
	if err := parserAt("PATH").parseJSONColumnOptions(&JSONTableColumn{}); err == nil {
		t.Error("expected PATH error")
	}
	for _, src := range []string{"NULL ON FOO", "ERROR ON FOO"} {
		if err := parserAt(src).parseJSONColumnOptions(&JSONTableColumn{}); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
	for _, src := range []string{"DEFAULT", "DEFAULT 1 ON FOO"} {
		if err := parserAt(src).parseJSONOptions(&FuncCall{}); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
}

func TestCoverageNewBranches2(t *testing.T) {
	fn := &FuncCall{Name: "json_value", Args: []Expr{&ColumnRef{Parts: []string{"x"}}}, JSONPath: &Literal{Kind: LitString, Value: "$"}, JSONOnError: "empty_object"}
	if out := formatJSONFunc(fn); out == "" {
		t.Fatal("empty JSON error formatter")
	}
	if got := formatJSONTableColumns([]JSONTableColumn{{Name: "x", Type: "INT", OnError: "default", DefaultError: &Literal{Kind: LitInt, Value: 1}}}); got == "" {
		t.Fatal("empty JSON_TABLE columns")
	}
	for _, src := range []string{"PERMUTE", "PERMUTE A"} {
		if _, err := parserAt(src).parseMatchTerm(); err == nil {
			t.Errorf("expected PERMUTE error for %q", src)
		}
	}
	if _, err := parserAt("JSON_TABLE(x, '$.a' COLUMNS (x EXISTS PATH))").parseJSONTable(); err == nil {
		t.Error("expected EXISTS PATH error")
	}
	for _, src := range []string{"EMPTY ARRAY ON", "EMPTY ARRAY ON FOO", "WITHOUT ARRAY"} {
		fn := &FuncCall{}
		if err := parserAt(src).parseJSONOptions(fn); err == nil {
			t.Errorf("expected JSON option error for %q", src)
		}
	}
}

func TestRowExprCoverage(t *testing.T) {
	row := &RowExpr{Values: []Expr{&ColumnRef{Parts: []string{"a"}}, &ColumnRef{Parts: []string{"b"}}}}
	if row.String() == "" {
		t.Fatal("empty row string")
	}
	if WalkExpr(row, func(e Expr) bool { return e != row.Values[0] }) == true {
		t.Fatal("walk should stop")
	}
	if _, err := Parse("SELECT (1,2,3) OVERLAPS (1,2)"); err == nil {
		t.Error("expected row OVERLAPS error")
	}
	if _, err := Parse("SELECT (a,b) FROM t"); err != nil {
		t.Fatal(err)
	}
}
