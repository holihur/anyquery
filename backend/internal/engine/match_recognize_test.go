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
	if res.Rows[0]["id"] != int64(1) || res.Rows[0]["cls"] != "a" || res.Rows[1]["id"] != int64(2) || res.Rows[1]["cls"] != "b" || res.Rows[2]["id"] != int64(4) || res.Rows[2]["cls"] != "a" {
		t.Fatalf("rows = %+v", res.Rows)
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

func TestMatchRecognizePreservesInputColumnsWithoutMeasures(t *testing.T) {
	res := execParams(t, `SELECT id FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		ALL ROWS PER MATCH
		PATTERN (A B*)
		DEFINE A AS status = 'paid', B AS total > 0
	) AS mr ORDER BY id`, nil)
	if res.RowCount != 3 || res.Rows[0]["id"] != int64(1) || res.Rows[1]["id"] != int64(2) || res.Rows[2]["id"] != int64(4) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestMatchRecognizeUnmatchedRowsAndFinalMeasure(t *testing.T) {
	res := execParams(t, `SELECT id, total, f FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		MEASURES FINAL total AS f
		ALL ROWS PER MATCH WITH UNMATCHED ROWS
		PATTERN (A)
		DEFINE A AS status = 'refunded'
	) AS mr ORDER BY id`, nil)
	if res.RowCount != 4 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[2]["f"] != float64(200) {
		t.Fatalf("final measure = %+v", res.Rows[2])
	}
	if res.Rows[0]["f"] != nil {
		t.Fatalf("unmatched measure = %+v", res.Rows[0])
	}
}

func TestMatchRecognizeRunningFinalPrevSubset(t *testing.T) {
	res := execParams(t, `SELECT id, r, c, p, n FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		MEASURES RUNNING sum(total) AS r, FINAL count(*) AS c, PREV(total) AS p, count(AB.*) AS n
		SUBSET AB = (A, B)
		ALL ROWS PER MATCH
		PATTERN (A B*)
		DEFINE A AS status = 'paid', B AS total > 0
	) AS mr ORDER BY id`, nil)
	if res.RowCount != 3 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["r"] != float64(100) || res.Rows[1]["r"] != float64(150) || res.Rows[1]["p"] != float64(100) {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["c"] != int64(2) {
		t.Fatalf("final count = %+v", res.Rows[0])
	}
}

func TestMatchRecognizePermutePattern(t *testing.T) {
	res := execParams(t, `SELECT id FROM mongo.orders MATCH_RECOGNIZE (
		ORDER BY id PATTERN (PERMUTE(A, B))
		DEFINE A AS status = 'paid', B AS total > 0
	) AS mr`, nil)
	if res.RowCount == 0 {
		t.Fatal("expected permute matches")
	}
}

func TestMatchRecognizeQualifiedPrev(t *testing.T) {
	res := execParams(t, `SELECT id, p FROM mongo.orders MATCH_RECOGNIZE (
		PARTITION BY region ORDER BY id
		MEASURES PREV(A.total) AS p
		ALL ROWS PER MATCH
		PATTERN (A B*)
		DEFINE A AS status = 'paid', B AS total > 0
	) AS mr ORDER BY id`, nil)
	if res.Rows[1]["p"] != float64(100) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestMatchRecognizePrevNextQualified(t *testing.T) {
	res := execParams(t, `SELECT id, p, n FROM (VALUES (1,'a'),(2,'a'),(3,'b')) AS t(id, tag)
		MATCH_RECOGNIZE (ORDER BY id MEASURES PREV(A.id) AS p, NEXT(A.id) AS n
		ALL ROWS PER MATCH PATTERN (A+ B) DEFINE A AS tag='a', B AS tag='b') AS mr ORDER BY id`, nil)
	if res.Rows[0]["p"] != nil || res.Rows[0]["n"] != int64(2) {
		t.Fatalf("first = %+v", res.Rows[0])
	}
	if res.Rows[1]["p"] != int64(1) || res.Rows[2]["p"] != int64(2) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}
