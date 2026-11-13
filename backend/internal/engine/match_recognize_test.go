package engine

import "testing"

func TestMatchRecognizeOneRow(t *testing.T) {
	res := execParams(t, `SELECT id, m FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		MEASURES id AS id, match_number() AS m
		ONE ROW PER MATCH
		PATTERN (A B*)
		DEFINE A AS status = 'paid', B AS total > 0
	) AS mr ORDER BY id`, nil)
	if res.RowCount != 2 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["id"] != int64(1) || res.Rows[0]["m"] != int64(1) {
		t.Fatalf("first = %+v", res.Rows[0])
	}
	if res.Rows[1]["id"] != int64(4) {
		t.Fatalf("second = %+v", res.Rows[1])
	}
}

func TestMatchRecognizeAllRowsAndClassifier(t *testing.T) {
	res := execParams(t, `SELECT id, cls FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		MEASURES classifier() AS cls
		ALL ROWS PER MATCH
		PATTERN (A B*)
		DEFINE A AS status = 'paid', B AS total > 0
	) AS mr ORDER BY id`, nil)
	if res.RowCount != 3 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["cls"] != "a" || res.Rows[1]["cls"] != "b" || res.Rows[2]["cls"] != "a" {
		t.Fatalf("classes = %+v", res.Rows)
	}
}

func TestMatchRecognizeLazyAndGreedy(t *testing.T) {
	greedy := execParams(t, `SELECT count(*) AS n FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		MEASURES match_number() AS m
		ONE ROW PER MATCH
		PATTERN (A+)
		DEFINE A AS status = 'paid'
	) AS mr`, nil)
	// EU has two paid rows, US has one (id 4); both partitions match.
	if greedy.Rows[0]["n"] != int64(2) {
		t.Fatalf("greedy = %+v", greedy.Rows[0])
	}
	lazy := execParams(t, `SELECT count(*) AS n FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		MEASURES match_number() AS m
		ONE ROW PER MATCH
		PATTERN (A+?)
		DEFINE A AS status = 'paid'
	) AS mr`, nil)
	// Lazy A+ matches one row per start, so EU gives two matches and US one.
	if lazy.Rows[0]["n"] != int64(3) {
		t.Fatalf("lazy = %+v", lazy.Rows[0])
	}
}

func TestMatchRecognizeEmptyMatchFails(t *testing.T) {
	_, err := New(DefaultOptions()).Execute(t.Context(), `SELECT * FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		PATTERN (A*)
		DEFINE A AS status = 'never'
	) AS mr`, testCatalog())
	if err == nil {
		t.Fatal("expected empty-match error")
	}
}

func TestMatchRecognizeMissingDefineFails(t *testing.T) {
	_, err := New(DefaultOptions()).Execute(t.Context(), `SELECT * FROM mongo.orders MATCH_RECOGNIZE (
		ORDER BY id PATTERN (A) DEFINE B AS status = 'paid'
	) AS mr`, testCatalog())
	if err == nil {
		t.Fatal("expected missing DEFINE error")
	}
}
