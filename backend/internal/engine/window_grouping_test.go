package engine

import "testing"

func TestNamedWindows(t *testing.T) {
	res := execParams(t, `SELECT id, sum(total) OVER w AS s
		FROM mongo.orders
		WINDOW w AS (PARTITION BY region ORDER BY id)
		ORDER BY id`, nil)
	if res.RowCount != 4 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	want := []any{float64(100), float64(150), float64(200), float64(225)}
	for i, row := range res.Rows {
		if row["s"] != want[i] {
			t.Fatalf("row %d = %+v, want %v", i, row, want[i])
		}
	}
}

func TestNamedWindowInheritance(t *testing.T) {
	res := execParams(t, `SELECT id, sum(total) OVER w2 AS s
		FROM mongo.orders
		WINDOW w AS (PARTITION BY region), w2 AS (w ORDER BY id)
		ORDER BY id`, nil)
	if res.Rows[1]["s"] != float64(150) || res.Rows[3]["s"] != float64(225) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestGroupsFrame(t *testing.T) {
	res := execParams(t, `SELECT id, sum(total) OVER (ORDER BY region GROUPS BETWEEN 1 PRECEDING AND CURRENT ROW) AS s
		FROM mongo.orders ORDER BY id`, nil)
	want := []any{float64(150), float64(150), float64(375), float64(375)}
	for i, row := range res.Rows {
		if row["s"] != want[i] {
			t.Fatalf("row %d = %+v, want %v", i, row, want[i])
		}
	}
}

func TestWindowExclude(t *testing.T) {
	current := execParams(t, `SELECT id, sum(total) OVER (
		ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW EXCLUDE CURRENT ROW
	) AS s FROM mongo.orders ORDER BY id`, nil)
	want := []any{nil, float64(100), float64(150), float64(350)}
	for i, row := range current.Rows {
		if row["s"] != want[i] {
			t.Fatalf("current row %d = %+v, want %v", i, row, want[i])
		}
	}
	group := execParams(t, `SELECT id, sum(total) OVER (
		ORDER BY region ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING EXCLUDE GROUP
	) AS s FROM mongo.orders ORDER BY id`, nil)
	groupWant := []any{float64(225), float64(225), nil, nil}
	for i, row := range group.Rows {
		if row["s"] != groupWant[i] {
			t.Fatalf("group row %d = %+v, want %v", i, row, groupWant[i])
		}
	}
}

func TestOrderedSetAggregates(t *testing.T) {
	res := execParams(t, `SELECT
		percentile_cont(0.5) WITHIN GROUP (ORDER BY total) AS median,
		percentile_disc(0.5) WITHIN GROUP (ORDER BY total) AS p50,
		mode() WITHIN GROUP (ORDER BY status) AS common
		FROM mongo.orders`, nil)
	row := res.Rows[0]
	if row["median"] != float64(75) || row["p50"] != float64(50) || row["common"] != "paid" {
		t.Fatalf("row = %+v", row)
	}
	filtered := execParams(t, `SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY total)
		FILTER (WHERE status = 'paid') AS median FROM mongo.orders`, nil)
	if filtered.Rows[0]["median"] != float64(50) {
		t.Fatalf("filtered median = %+v", filtered.Rows[0])
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT percentile_cont(2) WITHIN GROUP (ORDER BY total) FROM mongo.orders`, testCatalog()); err == nil {
		t.Fatal("expected invalid fraction error")
	}
}

func TestWindowExcludeTiesAndFilterDistinct(t *testing.T) {
	ties := execParams(t, `SELECT id, count(*) OVER (
		ORDER BY region ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES
	) AS n FROM mongo.orders ORDER BY id`, nil)
	for i, row := range ties.Rows {
		if row["n"] != int64(3) {
			t.Fatalf("ties row %d = %+v", i, row)
		}
	}
	groups := execParams(t, `SELECT id, count(*) OVER (
		ORDER BY region ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE GROUP
	) AS n FROM mongo.orders ORDER BY id`, nil)
	for i, row := range groups.Rows {
		if row["n"] != int64(2) {
			t.Fatalf("group row %d = %+v", i, row)
		}
	}
	filtered := execParams(t, `SELECT count(DISTINCT status) FILTER (WHERE status = 'paid') OVER () AS n FROM mongo.orders`, nil)
	if filtered.Rows[0]["n"] != int64(1) {
		t.Fatalf("filtered distinct = %+v", filtered.Rows[0])
	}
}

func TestIgnoreNullsWindow(t *testing.T) {
	res := execParams(t, `SELECT id, x, lag(x) IGNORE NULLS OVER (ORDER BY id) AS prev,
		first_value(x) IGNORE NULLS OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS first
		FROM (VALUES (1, 10), (2, NULL), (3, 30)) AS t(id, x) ORDER BY id`, nil)
	if res.Rows[1]["prev"] != int64(10) || res.Rows[2]["prev"] != int64(10) || res.Rows[1]["first"] != int64(10) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestRangeIntervalFrame(t *testing.T) {
	res := execParams(t, `SELECT id, sum(x) OVER (
		ORDER BY ts RANGE BETWEEN INTERVAL '1 day' PRECEDING AND CURRENT ROW
	) AS s FROM (VALUES
		(1, TIMESTAMP '2024-01-01 00:00:00', 10),
		(2, TIMESTAMP '2024-01-01 12:00:00', 20),
		(3, TIMESTAMP '2024-01-03 00:00:00', 30)
	) AS t(id, ts, x) ORDER BY id`, nil)
	if res.Rows[0]["s"] != int64(10) || res.Rows[1]["s"] != int64(30) || res.Rows[2]["s"] != int64(30) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestRangeIntervalDescending(t *testing.T) {
	res := execParams(t, `SELECT id, sum(x) OVER (
		ORDER BY ts DESC RANGE BETWEEN INTERVAL '1' DAY PRECEDING AND CURRENT ROW
	) AS s FROM (VALUES
		(1, TIMESTAMP '2024-01-01 00:00:00', 10),
		(2, TIMESTAMP '2024-01-01 12:00:00', 20),
		(3, TIMESTAMP '2024-01-03 00:00:00', 30)
	) AS t(id, ts, x) ORDER BY id`, nil)
	want := []any{int64(30), int64(20), int64(30)}
	for i, row := range res.Rows {
		if row["s"] != want[i] {
			t.Fatalf("row %d = %+v, want %v", i, row, want[i])
		}
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT sum(x) OVER (ORDER BY a, b RANGE BETWEEN 1 PRECEDING AND CURRENT ROW) FROM (VALUES (1,2,3)) AS t(a,b,x)`, testCatalog()); err == nil {
		t.Fatal("expected RANGE offset order error")
	}
}

func TestNthValueFromLast(t *testing.T) {
	res := execParams(t, `SELECT id, nth_value(x, 2) FROM LAST OVER (ORDER BY id) AS n
		FROM (VALUES (1,10),(2,20),(3,30)) AS t(id,x) ORDER BY id`, nil)
	if res.Rows[0]["n"] != nil || res.Rows[1]["n"] != int64(10) || res.Rows[2]["n"] != int64(20) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestNTileDistribution(t *testing.T) {
	res := execParams(t, `SELECT id, ntile(4) OVER (ORDER BY id) AS b
		FROM (VALUES (1),(2),(3),(4),(5),(6)) AS t(id) ORDER BY id`, nil)
	want := []int64{1, 1, 2, 2, 3, 4}
	for i, w := range want {
		if res.Rows[i]["b"] != w {
			t.Fatalf("rows = %+v", res.Rows)
		}
	}
}

func TestInvalidWindowFramesAndOffsets(t *testing.T) {
	qs := []string{
		`SELECT sum(x) OVER (ORDER BY id ROWS BETWEEN 1 FOLLOWING AND 1 PRECEDING) FROM (VALUES (1,10),(2,20)) t(id,x)`,
		`SELECT sum(x) OVER (ORDER BY id RANGE BETWEEN UNBOUNDED FOLLOWING AND CURRENT ROW) FROM (VALUES (1,10),(2,20)) t(id,x)`,
		`SELECT sum(x) OVER (ORDER BY tag RANGE BETWEEN 1 PRECEDING AND CURRENT ROW) FROM (VALUES (1,'a',10),(2,'b',20)) t(id,tag,x)`,
		`SELECT sum(x) OVER (ORDER BY id GROUPS BETWEEN INTERVAL '1' DAY PRECEDING AND CURRENT ROW) FROM (VALUES (1,10),(2,20)) t(id,x)`,
	}
	for _, q := range qs {
		if _, err := New(DefaultOptions()).Execute(t.Context(), q, testCatalog()); err == nil {
			t.Errorf("expected window frame error for %s", q)
		}
	}
}

func TestWindowPositiveArgumentValidation(t *testing.T) {
	for _, q := range []string{
		`SELECT ntile(0) OVER (ORDER BY id) FROM (VALUES (1),(2)) t(id)`,
		`SELECT nth_value(x, 0) OVER (ORDER BY id) FROM (VALUES (1,10)) t(id,x)`,
	} {
		if _, err := New(DefaultOptions()).Execute(t.Context(), q, testCatalog()); err == nil {
			t.Errorf("expected positive argument error for %s", q)
		}
	}
}
