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
