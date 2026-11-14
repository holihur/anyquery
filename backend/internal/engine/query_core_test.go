package engine

import (
	"context"
	"testing"
)

func execParams(t *testing.T, query string, params []any) *Result {
	t.Helper()
	res, err := New(DefaultOptions()).ExecuteParams(context.Background(), query, params, testCatalog())
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return res
}

func TestDerivedTableColumnAliases(t *testing.T) {
	res := execParams(t, "SELECT t.x FROM (SELECT id FROM mongo.orders ORDER BY id) AS t(x) ORDER BY t.x", nil)
	if res.RowCount != 4 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Columns[0].Name != "x" || res.Rows[0]["x"] != int64(1) {
		t.Fatalf("columns=%+v rows=%+v", res.Columns, res.Rows)
	}
	if _, err := New(DefaultOptions()).Execute(context.Background(), "SELECT * FROM (SELECT 1, 2) AS t(a)", testCatalog()); err == nil {
		t.Fatal("expected derived-table column count error")
	}
}

func TestUnnestTableFunction(t *testing.T) {
	res := execParams(t, "SELECT x FROM UNNEST(json_extract('[1, 2, 3]', '$')) AS u(x) ORDER BY x", nil)
	if res.RowCount != 3 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	for i, row := range res.Rows {
		if row["x"] != float64(i+1) && row["x"] != int64(i+1) {
			t.Fatalf("row %d = %+v", i, row)
		}
	}
	if _, err := New(DefaultOptions()).Execute(context.Background(), "SELECT * FROM UNNEST(NULL) AS u(a,b)", testCatalog()); err == nil {
		t.Fatal("expected UNNEST alias column error")
	}
	if _, err := New(DefaultOptions()).Execute(context.Background(), "SELECT * FROM generate_series(1, 2) AS g(x)", testCatalog()); err == nil {
		t.Fatal("expected unknown table function error")
	}
}

func TestLateralDerivedTable(t *testing.T) {
	res := execParams(t, `SELECT c.name, x.n
		FROM mongo.customers c,
		LATERAL (SELECT count(*) AS n FROM mongo.orders o WHERE o.customer_id = c.id) x
		ORDER BY c.name`, nil)
	if res.RowCount != 2 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["name"] != "Acme" || res.Rows[0]["n"] != int64(3) {
		t.Fatalf("Acme = %+v", res.Rows[0])
	}
	if res.Rows[1]["name"] != "Globex" || res.Rows[1]["n"] != int64(1) {
		t.Fatalf("Globex = %+v", res.Rows[1])
	}
}

func TestLateralJoin(t *testing.T) {
	res := execParams(t, `SELECT c.name, x.n
		FROM mongo.customers c
		JOIN LATERAL (SELECT count(*) AS n FROM mongo.orders o WHERE o.customer_id = c.id) x ON true
		ORDER BY c.name`, nil)
	if res.RowCount != 2 || res.Rows[0]["n"] != int64(3) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestTablesample(t *testing.T) {
	all := execParams(t, "SELECT count(*) AS n FROM mongo.orders TABLESAMPLE SYSTEM (100)", nil)
	if all.Rows[0]["n"] != int64(4) {
		t.Fatalf("100%% sample = %+v", all.Rows[0])
	}
	none := execParams(t, "SELECT count(*) AS n FROM mongo.orders TABLESAMPLE SYSTEM (0)", nil)
	if none.Rows[0]["n"] != int64(0) {
		t.Fatalf("0%% sample = %+v", none.Rows[0])
	}
	a := execParams(t, "SELECT id FROM mongo.orders TABLESAMPLE BERNOULLI (50) REPEATABLE (7)", nil)
	b := execParams(t, "SELECT id FROM mongo.orders TABLESAMPLE BERNOULLI (50) REPEATABLE (7)", nil)
	if a.RowCount != b.RowCount {
		t.Fatalf("repeatable samples differ: %d vs %d", a.RowCount, b.RowCount)
	}
	if _, err := New(DefaultOptions()).Execute(context.Background(), "SELECT * FROM mongo.orders TABLESAMPLE SYSTEM (101)", testCatalog()); err == nil {
		t.Fatal("expected invalid sample percentage error")
	}
}

func TestTopLevelValuesAndTable(t *testing.T) {
	values := execParams(t, "VALUES (1, 'a'), (2, 'b')", nil)
	if values.RowCount != 2 || values.Columns[0].Name != "column1" || values.Rows[1]["column2"] != "b" {
		t.Fatalf("values = columns=%+v rows=%+v", values.Columns, values.Rows)
	}
	table := execParams(t, "TABLE mongo.orders", nil)
	if table.RowCount != 4 {
		t.Fatalf("table rows = %+v", table.Rows)
	}
}

func TestParameterizedLimitAndOffset(t *testing.T) {
	res := execParams(t, "SELECT id FROM mongo.orders ORDER BY id LIMIT ? OFFSET ?", []any{2, 1})
	if res.RowCount != 2 || res.Rows[0]["id"] != int64(2) || res.Rows[1]["id"] != int64(3) {
		t.Fatalf("rows = %+v", res.Rows)
	}
	fetch := execParams(t, "SELECT id FROM mongo.orders ORDER BY id FETCH FIRST ? ROWS ONLY", []any{2})
	if fetch.RowCount != 2 || fetch.Rows[1]["id"] != int64(2) {
		t.Fatalf("fetch rows = %+v", fetch.Rows)
	}
	if _, err := New(DefaultOptions()).ExecuteParams(context.Background(), "SELECT id FROM mongo.orders LIMIT ?", []any{"bad"}, testCatalog()); err == nil {
		t.Fatal("expected invalid limit type error")
	}
}

func TestFetchWithTies(t *testing.T) {
	res := execParams(t, "SELECT id, region FROM mongo.orders ORDER BY region FETCH FIRST 1 ROWS WITH TIES", nil)
	if res.RowCount != 2 {
		t.Fatalf("ties rows = %+v", res.Rows)
	}
	if res.Rows[0]["region"] != "EU" || res.Rows[1]["region"] != "EU" {
		t.Fatalf("ties = %+v", res.Rows)
	}
	unique := execParams(t, "SELECT id FROM mongo.orders ORDER BY id FETCH FIRST 1 ROWS WITH TIES", nil)
	if unique.RowCount != 1 {
		t.Fatalf("unique rows = %+v", unique.Rows)
	}
	if _, err := New(DefaultOptions()).Execute(context.Background(), "SELECT id FROM mongo.orders FETCH FIRST 1 ROWS WITH TIES", testCatalog()); err == nil {
		t.Fatal("expected WITH TIES without ORDER BY error")
	}
}

func TestLateralUnnestAndLeftJoin(t *testing.T) {
	res := execParams(t, `SELECT c.name, x.v
		FROM mongo.customers c, LATERAL UNNEST(json_extract('["a","b"]', '$')) x(v)
		ORDER BY c.name, x.v`, nil)
	if res.RowCount != 4 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	left := execParams(t, `SELECT c.name, x.n
		FROM mongo.customers c
		LEFT JOIN LATERAL (SELECT count(*) AS n FROM mongo.orders o WHERE o.customer_id = c.id AND 1 = 0) x ON true
		ORDER BY c.name`, nil)
	if left.RowCount != 2 || left.Rows[0]["n"] != int64(0) {
		t.Fatalf("left lateral rows = %+v", left.Rows)
	}
}

func TestTablesampleOnCTE(t *testing.T) {
	res := execParams(t, "WITH x AS (SELECT id FROM mongo.orders) SELECT count(*) AS n FROM x TABLESAMPLE SYSTEM (100)", nil)
	if res.Rows[0]["n"] != int64(4) {
		t.Fatalf("cte sample = %+v", res.Rows[0])
	}
}

func TestLimitExpression(t *testing.T) {
	res := execParams(t, "SELECT id FROM mongo.orders ORDER BY id LIMIT 1 + 1", nil)
	if res.RowCount != 2 || res.Rows[0]["id"] != int64(1) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestTopLevelValuesWidthError(t *testing.T) {
	if _, err := New(DefaultOptions()).Execute(context.Background(), "VALUES (1), (2, 3)", testCatalog()); err == nil {
		t.Fatal("expected VALUES width error")
	}
}

func TestOffsetWithFetch(t *testing.T) {
	res := execParams(t, "SELECT id FROM mongo.orders ORDER BY id OFFSET ? ROWS FETCH FIRST 2 ROWS ONLY", []any{1})
	if res.RowCount != 2 || res.Rows[0]["id"] != int64(2) || res.Rows[1]["id"] != int64(3) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestFetchPercent(t *testing.T) {
	res := execParams(t, "SELECT id FROM mongo.orders ORDER BY id FETCH FIRST 50 PERCENT ROWS ONLY", nil)
	if res.RowCount != 2 {
		t.Fatalf("50 percent rows = %+v", res.Rows)
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), "SELECT id FROM mongo.orders FETCH FIRST 10 PERCENT ROWS ONLY", testCatalog()); err != nil {
		t.Fatal(err)
	}
}

func TestUnionJoin(t *testing.T) {
	res := execParams(t, `SELECT c.name, o.id FROM mongo.customers c UNION JOIN mongo.orders o`, nil)
	if res.RowCount != 6 {
		t.Fatalf("union join rows = %d %+v", res.RowCount, res.Rows)
	}
}

func TestCTEColumnAliases(t *testing.T) {
	res := execParams(t, `WITH x(a, b) AS (SELECT id, region FROM mongo.orders)
		SELECT a, b FROM x ORDER BY a`, nil)
	if res.RowCount != 4 || res.Rows[0]["a"] != int64(1) || res.Rows[0]["b"] != "EU" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestCTEValuesAndFromTable(t *testing.T) {
	res := execParams(t, "WITH x(a, b) AS (VALUES (1, 'a'), (2, 'b')) SELECT a, b FROM x ORDER BY a", nil)
	if res.RowCount != 2 || res.Rows[0]["a"] != int64(1) || res.Rows[1]["b"] != "b" {
		t.Fatalf("rows = %+v", res.Rows)
	}
	res = execParams(t, "WITH RECURSIVE x(n) AS (VALUES (1) UNION ALL SELECT n + 1 FROM x WHERE n < 3) SELECT count(*) AS n FROM x", nil)
	if res.Rows[0]["n"] != int64(3) {
		t.Fatalf("recursive values = %+v", res.Rows)
	}
	res = execParams(t, "SELECT t.id FROM (TABLE mongo.orders) AS t ORDER BY t.id LIMIT 2", nil)
	if res.RowCount != 2 || res.Rows[0]["id"] != int64(1) {
		t.Fatalf("table derived = %+v", res.Rows)
	}
}

func TestUnnestOrdinalityAndMultipleArrays(t *testing.T) {
	res := execParams(t, `SELECT x, ord FROM UNNEST(json_extract('[10,20]', '$')) WITH ORDINALITY AS u(x, ord) ORDER BY ord`, nil)
	if res.RowCount != 2 || res.Rows[0]["x"] != float64(10) || res.Rows[0]["ord"] != int64(1) {
		t.Fatalf("ordinality rows = %+v", res.Rows)
	}
	multi := execParams(t, `SELECT x, y FROM UNNEST(json_extract('[10,20]', '$'), json_extract('["a","b"]', '$')) AS u(x, y)`, nil)
	if multi.RowCount != 2 || multi.Rows[1]["x"] != float64(20) || multi.Rows[1]["y"] != "b" {
		t.Fatalf("multi rows = %+v", multi.Rows)
	}
}

func TestEmptyGroupingSet(t *testing.T) {
	res := execParams(t, "SELECT count(*) AS n FROM mongo.orders GROUP BY ()", nil)
	if res.RowCount != 1 || res.Rows[0]["n"] != int64(4) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestRowValuePredicates(t *testing.T) {
	res := execParams(t, `SELECT id FROM mongo.orders
		WHERE (region, status) = ('EU', 'paid') ORDER BY id`, nil)
	if res.RowCount != 2 || res.Rows[0]["id"] != int64(1) {
		t.Fatalf("row equality = %+v", res.Rows)
	}
	res = execParams(t, `SELECT id FROM mongo.orders
		WHERE (region, status) IN (('EU','paid'), ('US','refunded')) ORDER BY id`, nil)
	if res.RowCount != 3 || res.Rows[2]["id"] != int64(3) {
		t.Fatalf("row IN = %+v", res.Rows)
	}
}

func TestRowValueSubqueries(t *testing.T) {
	res := execParams(t, `SELECT id FROM mongo.orders
		WHERE (region,status) IN (SELECT region,status FROM mongo.orders WHERE id=1) ORDER BY id`, nil)
	if res.RowCount != 2 || res.Rows[0]["id"] != int64(1) {
		t.Fatalf("row IN subquery = %+v", res.Rows)
	}
	res = execParams(t, `SELECT id FROM mongo.orders
		WHERE (region,status) = (SELECT region,status FROM mongo.orders WHERE id=1) ORDER BY id`, nil)
	if res.RowCount != 2 || res.Rows[1]["id"] != int64(2) {
		t.Fatalf("row = subquery = %+v", res.Rows)
	}
}

func TestRegisteredTableFunction(t *testing.T) {
	eng := New(DefaultOptions())
	err := eng.RegisterTableFunction(TableFunctionSpec{
		Name: "series", MinArgs: 1, MaxArgs: 1, Columns: []string{"n"},
		Fn: func(_ context.Context, args []any) ([][]any, error) {
			n, _ := toInt(args[0])
			var rows [][]any
			for i := 1; i <= n; i++ {
				rows = append(rows, []any{int64(i)})
			}
			return rows, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Execute(t.Context(), `SELECT n, ord FROM series(2) WITH ORDINALITY AS s(n, ord) ORDER BY n`, testCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 2 || res.Rows[1]["n"] != int64(2) || res.Rows[1]["ord"] != int64(2) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}
