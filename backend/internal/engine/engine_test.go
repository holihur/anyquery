package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/anyquery/anyquery/internal/connector"
)

type fakeConn struct {
	tables     map[string][]map[string]any
	lastReq    *connector.FetchRequest
	reqs       []connector.FetchRequest
	systemTime bool
}

func (f *fakeConn) Ping(context.Context) error { return nil }
func (f *fakeConn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true, ProjectionPushdown: true, FilterPushdown: true, SystemTime: f.systemTime}
}
func (f *fakeConn) Close() error { return nil }
func (f *fakeConn) DescribeFetch(req connector.FetchRequest) (string, []any, error) {
	return "EXEC " + req.Table, nil, nil
}
func (f *fakeConn) Introspect(context.Context) ([]connector.Table, error) { return nil, nil }
func (f *fakeConn) Fetch(_ context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
	f.lastReq = &req
	f.reqs = append(f.reqs, req)
	rows := f.tables[req.Table]
	if req.Limit > 0 && len(rows) > req.Limit {
		rows = rows[:req.Limit]
	}
	var cols []connector.Column
	seen := map[string]bool{}
	for _, r := range rows {
		for k := range r {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, connector.Column{Name: k, Type: inferType(r[k])})
			}
		}
	}
	return &connector.FetchResult{Columns: cols, Rows: rows}, nil
}

type fakeCatalog struct {
	metas map[string]SourceMeta
	conns map[string]*fakeConn
}

func (f *fakeCatalog) Sources(context.Context) ([]SourceMeta, error) {
	var out []SourceMeta
	for _, m := range f.metas {
		out = append(out, m)
	}
	return out, nil
}
func (f *fakeCatalog) Open(_ context.Context, name string) (connector.Connector, error) {
	return f.conns[name], nil
}

func testCatalog() *fakeCatalog {
	orders := []map[string]any{
		{"id": int64(1), "customer_id": int64(1), "total": 100.0, "status": "paid", "region": "EU"},
		{"id": int64(2), "customer_id": int64(2), "total": 50.0, "status": "paid", "region": "EU"},
		{"id": int64(3), "customer_id": int64(1), "total": 200.0, "status": "refunded", "region": "US"},
		{"id": int64(4), "customer_id": int64(1), "total": 25.0, "status": "paid", "region": "US"},
	}
	customers := []map[string]any{
		{"id": int64(1), "name": "Acme", "segment": "enterprise", "region": "US"},
		{"id": int64(2), "name": "Globex", "segment": "smb", "region": "EU"},
	}
	products := []map[string]any{
		{"id": int64(10), "customer_id": float64(1), "category": "software"},
		{"id": int64(11), "customer_id": int64(9), "category": "services"},
	}
	mongoConn := &fakeConn{systemTime: true, tables: map[string][]map[string]any{
		"orders":    orders,
		"customers": customers,
	}}
	sqlConn := &fakeConn{tables: map[string][]map[string]any{"public.items": products}}
	col := func(names ...string) []connector.Column {
		var out []connector.Column
		for _, n := range names {
			out = append(out, connector.Column{Name: n, Type: "string"})
		}
		return out
	}
	return &fakeCatalog{
		metas: map[string]SourceMeta{
			"mongo": {Name: "mongo", Type: "mongodb", Tables: []connector.Table{
				{Name: "orders", Columns: col("id", "customer_id", "total", "status", "region")},
				{Name: "customers", Columns: col("id", "name", "segment", "region")},
			}},
			"shop": {Name: "shop", Type: "postgres", Tables: []connector.Table{
				{Name: "public.items", Columns: col("id", "customer_id", "category")},
			}},
		},
		conns: map[string]*fakeConn{"mongo": mongoConn, "shop": sqlConn},
	}
}

func run(t *testing.T, q string) *Result {
	t.Helper()
	e := New(DefaultOptions())
	res, err := e.Execute(context.Background(), q, testCatalog())
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return res
}

func TestFilterAggregateOrder(t *testing.T) {
	res := run(t, `SELECT region, count(*) AS orders, sum(total) AS revenue
FROM mongo.orders WHERE status = 'paid' GROUP BY region ORDER BY revenue DESC`)
	if res.RowCount != 2 {
		t.Fatalf("rows = %d: %+v", res.RowCount, res.Rows)
	}
	if res.Rows[0]["region"] != "EU" || res.Rows[0]["revenue"] != 150.0 {
		t.Fatalf("first row = %+v", res.Rows[0])
	}
}

func TestJoinAcrossSources(t *testing.T) {
	res := run(t, `SELECT c.name, count(*) AS won, sum(o.total) AS revenue
FROM mongo.orders o JOIN mongo.customers c ON o.customer_id = c.id
WHERE o.status = 'paid'
GROUP BY c.name ORDER BY revenue DESC`)
	if res.RowCount != 2 {
		t.Fatalf("rows = %d %+v", res.RowCount, res.Rows)
	}
	if res.Rows[0]["name"] != "Acme" || res.Rows[0]["revenue"] != 125.0 {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestCrossSourceJoinWithQuotedTable(t *testing.T) {
	res := run(t, "SELECT c.name, i.category FROM mongo.customers c JOIN shop.`public.items` i ON c.id = i.customer_id")
	if res.RowCount != 1 || res.Rows[0]["category"] != "software" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestPipeline(t *testing.T) {
	res := run(t, `FROM mongo.orders
|> WHERE status = 'paid'
|> AGGREGATE region, sum(total) AS revenue, count(*) AS orders GROUP BY region
|> ORDER BY revenue DESC
|> LIMIT 1`)
	if res.RowCount != 1 || res.Rows[0]["region"] != "EU" || res.Rows[0]["revenue"] != 150.0 {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestPipelineProjectAndDistinct(t *testing.T) {
	res := run(t, `SELECT region, total FROM mongo.orders |> WHERE total > 20 |> SELECT region, total * 2 AS doubled |> ORDER BY doubled DESC |> SELECT region |> DISTINCT |> ORDER BY region`)
	if res.RowCount != 2 {
		t.Fatalf("rows = %d %+v", res.RowCount, res.Rows)
	}
	if res.Rows[0]["region"] != "EU" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestHavingAndLimit(t *testing.T) {
	res := run(t, `SELECT customer_id, count(*) AS n FROM mongo.orders GROUP BY customer_id HAVING count(*) > 1 ORDER BY n DESC LIMIT 1`)
	if res.RowCount != 1 || res.Rows[0]["n"] != int64(3) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestLeftJoin(t *testing.T) {
	res := run(t, `SELECT c.name, count(o.id) AS n FROM mongo.customers c LEFT JOIN mongo.orders o ON c.id = o.customer_id GROUP BY c.name ORDER BY c.name`)
	if res.RowCount != 2 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["name"] != "Acme" || res.Rows[0]["n"] != int64(3) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestShowTables(t *testing.T) {
	res := run(t, `SHOW TABLES FROM mongo`)
	if res.RowCount != 2 {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestExpressionFunctions(t *testing.T) {
	res := run(t, `SELECT lower(name) AS n, coalesce(segment, 'none') AS s FROM mongo.customers ORDER BY name`)
	if res.Rows[0]["n"] != "acme" || res.Rows[0]["s"] != "enterprise" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestNoFrom(t *testing.T) {
	res := run(t, `SELECT 1 + 2 AS n, upper('ok') AS s`)
	if res.Rows[0]["n"] != int64(3) || res.Rows[0]["s"] != "OK" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestCTE(t *testing.T) {
	res := run(t, `WITH paid AS (SELECT * FROM mongo.orders WHERE status = 'paid')
SELECT region, count(*) AS n FROM paid GROUP BY region ORDER BY region`)
	if res.RowCount != 2 || res.Rows[0]["n"] != int64(2) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestCompositeEquiJoinWithExtraPredicate(t *testing.T) {
	res := run(t, `SELECT count(*) AS n
FROM mongo.orders o
JOIN mongo.customers c ON o.customer_id = c.id AND o.region = c.region
WHERE o.status = 'paid'`)
	if res.Rows[0]["n"] != int64(2) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestCrossSourceFullJoin(t *testing.T) {
	res := run(t, "SELECT c.name, i.category FROM mongo.customers c FULL JOIN shop.`public.items` i ON c.id = i.customer_id ORDER BY c.name")
	if res.RowCount != 3 {
		t.Fatalf("rows = %d %+v", res.RowCount, res.Rows)
	}
	if res.Rows[0]["name"] != "Acme" || res.Rows[0]["category"] != "software" {
		t.Fatalf("first row = %+v", res.Rows[0])
	}
	if res.Rows[2]["name"] != nil || res.Rows[2]["category"] != "services" {
		t.Fatalf("right-unmatched row = %+v", res.Rows[2])
	}
}

func TestNumericCrossSourceJoin(t *testing.T) {
	// Numeric JSON values arrive as float64 while SQL ids are int64; the hash
	// join normalizes both so cross-source ids still match.
	res := run(t, "SELECT count(*) AS n FROM mongo.orders o JOIN shop.`public.items` i ON o.customer_id = i.customer_id")
	if res.Rows[0]["n"] != int64(3) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestUnionAndOrder(t *testing.T) {
	res := run(t, `SELECT region FROM mongo.orders WHERE status = 'paid'
UNION
SELECT region FROM mongo.customers
ORDER BY region`)
	if res.RowCount != 2 {
		t.Fatalf("rows = %d %+v", res.RowCount, res.Rows)
	}
	if res.Rows[0]["region"] != "EU" || res.Rows[1]["region"] != "US" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestUnionAllKeepsDuplicates(t *testing.T) {
	res := run(t, `SELECT region FROM mongo.orders WHERE status = 'paid'
UNION ALL SELECT region FROM mongo.orders WHERE status = 'paid'`)
	if res.RowCount != 6 {
		t.Fatalf("rows = %d", res.RowCount)
	}
}

func TestIntersectAndExcept(t *testing.T) {
	res := run(t, `SELECT region FROM mongo.orders INTERSECT SELECT region FROM mongo.customers ORDER BY region`)
	if res.RowCount != 2 {
		t.Fatalf("intersect rows = %d %+v", res.RowCount, res.Rows)
	}
	res = run(t, `SELECT region FROM mongo.orders EXCEPT SELECT region FROM mongo.customers ORDER BY region`)
	if res.RowCount != 0 {
		t.Fatalf("except rows = %d %+v", res.RowCount, res.Rows)
	}
}

func TestScalarAndInSubqueries(t *testing.T) {
	res := run(t, `SELECT name FROM mongo.customers WHERE id IN (SELECT customer_id FROM mongo.orders WHERE total > 100) ORDER BY name`)
	if res.RowCount != 1 || res.Rows[0]["name"] != "Acme" {
		t.Fatalf("rows = %+v", res.Rows)
	}
	res = run(t, `SELECT name, (SELECT count(*) FROM mongo.orders o WHERE o.customer_id = c.id) AS n FROM mongo.customers c ORDER BY name`)
	if res.RowCount != 2 || res.Rows[0]["n"] != int64(3) || res.Rows[1]["n"] != int64(1) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestExistsSubquery(t *testing.T) {
	res := run(t, `SELECT name FROM mongo.customers c WHERE EXISTS (SELECT 1 FROM mongo.orders o WHERE o.customer_id = c.id AND o.status = 'refunded') ORDER BY name`)
	if res.RowCount != 1 || res.Rows[0]["name"] != "Acme" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestWindowFunctions(t *testing.T) {
	res := run(t, `SELECT region, total,
  row_number() OVER (PARTITION BY region ORDER BY total DESC) AS rn,
  rank() OVER (PARTITION BY region ORDER BY total DESC) AS rnk,
  dense_rank() OVER (PARTITION BY region ORDER BY total DESC) AS dr,
  sum(total) OVER (PARTITION BY region ORDER BY total) AS running,
  lag(total) OVER (PARTITION BY region ORDER BY total) AS prev,
  lead(total) OVER (PARTITION BY region ORDER BY total) AS next
FROM mongo.orders ORDER BY region, total`)
	if res.RowCount != 4 {
		t.Fatalf("rows = %d", res.RowCount)
	}
	// EU rows: 50, 100. US rows: 25, 200.
	eu := res.Rows[0]
	if eu["region"] != "EU" || eu["rn"] != int64(2) || eu["running"] != 50.0 || eu["prev"] != nil || eu["next"] != 100.0 {
		t.Fatalf("eu row = %+v", eu)
	}
	eu2 := res.Rows[1]
	if eu2["running"] != 150.0 || eu2["prev"] != 50.0 || eu2["next"] != nil {
		t.Fatalf("eu2 row = %+v", eu2)
	}
}

func TestWindowNoOrderAndCount(t *testing.T) {
	res := run(t, `SELECT region, count(*) OVER (PARTITION BY region) AS n, count(*) OVER () AS all_n FROM mongo.orders ORDER BY region LIMIT 1`)
	if res.Rows[0]["n"] != int64(2) || res.Rows[0]["all_n"] != int64(4) {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func TestNaturalJoinAndNullsOrder(t *testing.T) {
	res := run(t, `SELECT count(*) AS n FROM mongo.orders NATURAL JOIN mongo.customers`)
	if res.Rows[0]["n"] != int64(1) {
		t.Fatalf("natural join = %+v", res.Rows)
	}
	res = run(t, `SELECT region FROM mongo.orders ORDER BY region NULLS FIRST`)
	if res.Rows[0]["region"] != "EU" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestAggregateFilter(t *testing.T) {
	res := run(t, `SELECT region, count(*) AS all_n, count(*) FILTER (WHERE status = 'paid') AS paid_n, sum(total) FILTER (WHERE status = 'paid') AS paid_sum
FROM mongo.orders GROUP BY region ORDER BY region`)
	if res.Rows[0]["all_n"] != int64(2) || res.Rows[0]["paid_n"] != int64(2) || res.Rows[0]["paid_sum"] != 150.0 {
		t.Fatalf("EU = %+v", res.Rows[0])
	}
	if res.Rows[1]["paid_n"] != int64(1) || res.Rows[1]["paid_sum"] != 25.0 {
		t.Fatalf("US = %+v", res.Rows[1])
	}
}

func TestSQLFunctionsAndFetch(t *testing.T) {
	res := run(t, `SELECT extract(year FROM '2024-05-06T07:08:09Z') AS y,
	date_part('month', '2024-05-06T07:08:09Z') AS m,
	date_add('day', 2, '2024-05-06T07:08:09Z') AS d,
	position('cd' IN 'abcdef') AS pos,
	substring('abcdef' FROM 2 FOR 3) AS sub,
	regexp_replace('a-b-c', '-', '/') AS rep
	FETCH FIRST 1 ROWS ONLY`)
	if res.Rows[0]["y"] != int64(2024) || res.Rows[0]["m"] != int64(5) || res.Rows[0]["pos"] != int64(3) || res.Rows[0]["sub"] != "bcd" || res.Rows[0]["rep"] != "a/b/c" {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func TestOrderByOrdinalAndNulls(t *testing.T) {
	res := run(t, `SELECT region, total, CASE WHEN total > 100 THEN NULL ELSE total END AS maybe
FROM mongo.orders ORDER BY 3 NULLS FIRST, 1`)
	if res.Rows[0]["maybe"] != nil {
		t.Fatalf("expected null first: %+v", res.Rows)
	}
}

func TestIsDistinctFrom(t *testing.T) {
	res := run(t, `SELECT count(*) AS n FROM mongo.orders o
JOIN mongo.customers c ON o.customer_id = c.id
WHERE o.region IS DISTINCT FROM c.region`)
	if res.Rows[0]["n"] != int64(1) {
		t.Fatalf("rows = %+v", res.Rows)
	}
	res = run(t, `SELECT (NULL IS NOT DISTINCT FROM NULL) AS same, (1 IS DISTINCT FROM 2) AS diff`)
	if res.Rows[0]["same"] != true || res.Rows[0]["diff"] != true {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func TestSetOperationPrecedence(t *testing.T) {
	res := run(t, `SELECT region FROM mongo.orders
UNION
SELECT region FROM mongo.customers INTERSECT SELECT region FROM mongo.customers WHERE region = 'EU'
ORDER BY region`)
	if res.RowCount != 2 || res.Rows[0]["region"] != "EU" || res.Rows[1]["region"] != "US" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestGroupByAliasAndOrdinal(t *testing.T) {
	res := run(t, `SELECT region AS r, count(*) AS n FROM mongo.orders GROUP BY r ORDER BY r`)
	if res.RowCount != 2 || res.Rows[0]["r"] != "EU" || res.Rows[0]["n"] != int64(2) {
		t.Fatalf("alias group = %+v", res.Rows)
	}
	res = run(t, `SELECT region, count(*) AS n FROM mongo.orders GROUP BY 1 ORDER BY 1`)
	if res.RowCount != 2 || res.Rows[0]["region"] != "EU" {
		t.Fatalf("ordinal group = %+v", res.Rows)
	}
}

func TestRecursiveCTE(t *testing.T) {
	res := run(t, `WITH RECURSIVE nums(n) AS (
  SELECT 1
  UNION ALL
  SELECT n + 1 FROM nums WHERE n < 5
)
SELECT n FROM nums ORDER BY n`)
	if res.RowCount != 5 || res.Rows[0]["n"] != int64(1) || res.Rows[4]["n"] != int64(5) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestRecursiveCTEUnionDistinct(t *testing.T) {
	res := run(t, `WITH RECURSIVE t(n) AS (
  SELECT 1
  UNION
  SELECT CASE WHEN n < 3 THEN n + 1 ELSE 1 END FROM t
)
SELECT count(*) AS n FROM t`)
	if res.Rows[0]["n"] != int64(3) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestWindowOverAggregates(t *testing.T) {
	res := run(t, `SELECT region, sum(total) AS revenue,
  rank() OVER (ORDER BY revenue DESC) AS rnk,
  sum(sum(total)) OVER () AS total_revenue
FROM mongo.orders GROUP BY region ORDER BY rnk`)
	if res.RowCount != 2 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["region"] != "US" || res.Rows[0]["revenue"] != 225.0 || res.Rows[0]["rnk"] != int64(1) || res.Rows[0]["total_revenue"] != 375.0 {
		t.Fatalf("first = %+v", res.Rows[0])
	}
	if res.Rows[1]["region"] != "EU" || res.Rows[1]["rnk"] != int64(2) {
		t.Fatalf("second = %+v", res.Rows[1])
	}
}

func TestRangePeerFrame(t *testing.T) {
	res := run(t, `SELECT region, sum(total) OVER (PARTITION BY region ORDER BY region) AS s FROM mongo.orders ORDER BY region, total`)
	if res.RowCount != 4 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["region"] != "EU" || res.Rows[0]["s"] != 150.0 || res.Rows[1]["s"] != 150.0 {
		t.Fatalf("EU rows = %+v", res.Rows)
	}
	if res.Rows[2]["region"] != "US" || res.Rows[2]["s"] != 225.0 {
		t.Fatalf("US rows = %+v", res.Rows)
	}
}

func TestUsingStarCoalescing(t *testing.T) {
	res := run(t, `SELECT * FROM mongo.orders o JOIN mongo.customers c USING (id)`)
	if len(res.Columns) != 8 {
		t.Fatalf("columns = %+v", res.Columns)
	}
	seen := map[string]bool{}
	for _, c := range res.Columns {
		if seen[c.Name] {
			t.Fatalf("duplicate column %q in %+v", c.Name, res.Columns)
		}
		seen[c.Name] = true
	}
	res = run(t, `SELECT id FROM mongo.customers c RIGHT JOIN mongo.orders o USING (id) ORDER BY id`)
	if res.RowCount != 4 || res.Rows[0]["id"] != int64(1) || res.Rows[3]["id"] != int64(4) {
		t.Fatalf("coalesced id rows = %+v", res.Rows)
	}
}

func TestHavingBeforeWindow(t *testing.T) {
	res := run(t, `SELECT region, sum(total) AS revenue,
  rank() OVER (ORDER BY sum(total) DESC) AS rnk
FROM mongo.orders GROUP BY region HAVING sum(total) < 200`)
	if res.RowCount != 1 || res.Rows[0]["region"] != "EU" || res.Rows[0]["rnk"] != int64(1) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestQuantifiedSubqueries(t *testing.T) {
	res := run(t, `SELECT name FROM mongo.customers c
WHERE c.id = ANY (SELECT customer_id FROM mongo.orders WHERE total > 100)
ORDER BY name`)
	if res.RowCount != 1 || res.Rows[0]["name"] != "Acme" {
		t.Fatalf("ANY rows = %+v", res.Rows)
	}
	res = run(t, `SELECT name FROM mongo.customers c
WHERE c.id > ALL (SELECT customer_id FROM mongo.orders WHERE total < 30)
ORDER BY name`)
	if res.RowCount != 1 || res.Rows[0]["name"] != "Globex" {
		t.Fatalf("ALL rows = %+v", res.Rows)
	}
	res = run(t, `SELECT 2 = ANY (1, 2, 3) AS a, 2 = ALL (2, 2) AS b`)
	if res.Rows[0]["a"] != true || res.Rows[0]["b"] != true {
		t.Fatalf("list = %+v", res.Rows[0])
	}
}

func TestRangeValueFrame(t *testing.T) {
	res := run(t, `SELECT total, sum(total) OVER (ORDER BY total RANGE BETWEEN 60 PRECEDING AND CURRENT ROW) AS s
FROM mongo.orders ORDER BY total`)
	want := []float64{25, 75, 150, 200}
	for i, w := range want {
		if res.Rows[i]["s"] != w {
			t.Fatalf("row %d = %+v, want %v", i, res.Rows[i], w)
		}
	}
}

func TestValuesTable(t *testing.T) {
	res := run(t, `SELECT id, name FROM (VALUES (2, 'b'), (1, 'a')) AS t(id, name) ORDER BY id`)
	if res.RowCount != 2 || res.Rows[0]["id"] != int64(1) || res.Rows[0]["name"] != "a" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestBindParameters(t *testing.T) {
	e := New(DefaultOptions())
	res, err := e.ExecuteParams(context.Background(), `SELECT name FROM mongo.customers WHERE id = ? OR id = $2 ORDER BY name`, []any{int64(1), int64(2)}, testCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 2 || res.Rows[0]["name"] != "Acme" {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if _, err := e.ExecuteParams(context.Background(), `SELECT ? AS n`, nil, testCatalog()); err == nil {
		t.Fatal("expected missing parameter error")
	}
}

func TestScanPushdownHints(t *testing.T) {
	cat := testCatalog()
	conn := cat.conns["mongo"]
	e := New(DefaultOptions())
	if _, err := e.Execute(context.Background(), `SELECT region FROM mongo.orders WHERE status = 'paid'`, cat); err != nil {
		t.Fatal(err)
	}
	req := conn.lastReq
	if req == nil || len(req.Columns) != 2 || !containsString(req.Columns, "region") || !containsString(req.Columns, "status") {
		t.Fatalf("columns = %+v", req)
	}
	if len(req.Predicates) != 1 || req.Predicates[0].Column != "status" || req.Predicates[0].Op != "eq" {
		t.Fatalf("predicates = %+v", req.Predicates)
	}

	if _, err := e.Execute(context.Background(), `SELECT * FROM mongo.customers LIMIT 1`, cat); err != nil {
		t.Fatal(err)
	}
	if conn.lastReq.Limit != 2 {
		t.Fatalf("limit = %d", conn.lastReq.Limit)
	}
}

func containsString(list []string, value string) bool {
	for _, s := range list {
		if strings.EqualFold(s, value) {
			return true
		}
	}
	return false
}

func TestRollupAndGrouping(t *testing.T) {
	res := run(t, `SELECT region, count(*) AS n, grouping(region) AS g
FROM mongo.orders GROUP BY ROLLUP(region)
ORDER BY g, region`)
	if res.RowCount != 3 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Rows[0]["region"] != "EU" || res.Rows[0]["n"] != int64(2) || res.Rows[0]["g"] != int64(0) {
		t.Fatalf("first = %+v", res.Rows[0])
	}
	if res.Rows[2]["region"] != nil || res.Rows[2]["n"] != int64(4) || res.Rows[2]["g"] != int64(1) {
		t.Fatalf("total = %+v", res.Rows[2])
	}
}

func TestMixedGroupingSets(t *testing.T) {
	res := run(t, `SELECT region, status, count(*) AS n
FROM mongo.orders GROUP BY region, ROLLUP(status)
ORDER BY region, status NULLS LAST`)
	if res.RowCount != 5 {
		t.Fatalf("rows = %d %+v", res.RowCount, res.Rows)
	}
	if res.Rows[1]["region"] != "EU" || res.Rows[1]["status"] != nil || res.Rows[1]["n"] != int64(2) {
		t.Fatalf("EU total = %+v", res.Rows[1])
	}
}

func TestGroupingSets(t *testing.T) {
	res := run(t, `SELECT region, status, count(*) AS n
FROM mongo.orders GROUP BY GROUPING SETS ((region), (status), ())
ORDER BY region NULLS LAST, status NULLS LAST`)
	if res.RowCount != 5 {
		t.Fatalf("rows = %d %+v", res.RowCount, res.Rows)
	}
}

func TestTypedCastsAndTimezones(t *testing.T) {
	res := run(t, `SELECT
  try_cast('bad' AS INT) AS bad,
  cast('2024-01-02' AS DATE) AS d,
  date '2024-01-03' AS d2,
  cast('2024-01-02 03:04:05' AS TIMESTAMP) AS ts,
  to_timestamp(0) AS epoch,
  timezone('UTC', '2024-01-01T00:00:00Z') AS z`)
	row := res.Rows[0]
	if row["bad"] != nil || row["d"] != "2024-01-02" || row["d2"] != "2024-01-03" {
		t.Fatalf("row = %+v", row)
	}
	if row["ts"] != "2024-01-02T03:04:05Z" || row["epoch"] != "1970-01-01T00:00:00Z" || row["z"] != "2024-01-01T00:00:00Z" {
		t.Fatalf("row = %+v", row)
	}
}

func TestGlobalAggregateOverEmptySubquery(t *testing.T) {
	res := run(t, `SELECT (SELECT count(*) FROM mongo.orders WHERE id = 999) AS n`)
	if res.Rows[0]["n"] != int64(0) {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestJoinScanPushdownIncludesJoinColumns(t *testing.T) {
	cat := testCatalog()
	e := New(DefaultOptions())
	if _, err := e.Execute(context.Background(), `SELECT c.name, i.category
FROM mongo.customers c JOIN mongo.orders o ON c.id = o.customer_id
JOIN shop.`+"`public.items`"+` i ON o.customer_id = i.customer_id`, cat); err != nil {
		t.Fatal(err)
	}
	byTable := map[string]connector.FetchRequest{}
	for _, req := range cat.conns["mongo"].reqs {
		byTable[req.Table] = req
	}
	if req := byTable["customers"]; !containsString(req.Columns, "id") || !containsString(req.Columns, "name") {
		t.Fatalf("customer columns = %+v", req)
	}
	if req := byTable["orders"]; !containsString(req.Columns, "customer_id") {
		t.Fatalf("order columns = %+v", req)
	}
	if req := cat.conns["shop"].lastReq; req == nil || !containsString(req.Columns, "customer_id") || !containsString(req.Columns, "category") {
		t.Fatalf("shop columns = %+v", req)
	}
}

func TestExplainPlan(t *testing.T) {
	e := New(DefaultOptions())
	plan, err := e.Explain(context.Background(), `SELECT c.name, count(*) AS n
FROM mongo.orders o JOIN mongo.customers c ON o.customer_id = c.id
WHERE o.status = 'paid' GROUP BY c.name ORDER BY n DESC LIMIT 5`, nil, testCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Operator != "Limit" || len(plan.Children) != 1 {
		t.Fatalf("root = %+v", plan)
	}
	var ops []string
	var walk func(*PlanNode)
	walk = func(n *PlanNode) {
		ops = append(ops, n.Operator)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(plan)
	for _, want := range []string{"Sort", "Aggregate", "Project", "Hash Join", "Source Scan"} {
		found := false
		for _, op := range ops {
			if op == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", want, ops)
		}
	}
	foundStatement := false
	walk = func(n *PlanNode) {
		if n.Operator == "Source Scan" && n.Properties["sourceStatement"] == "EXEC orders" {
			foundStatement = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(plan)
	if !foundStatement {
		t.Fatalf("source statement missing from plan: %+v", plan)
	}
}

func TestMultipleGroupingConstructs(t *testing.T) {
	res := run(t, `SELECT region, status, count(*) AS n
FROM mongo.orders GROUP BY ROLLUP(region), CUBE(status)
ORDER BY region NULLS LAST, status NULLS LAST`)
	if res.RowCount != 8 {
		t.Fatalf("rows = %d %+v", res.RowCount, res.Rows)
	}
}

func TestAggregateRequiresGroupedColumns(t *testing.T) {
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT region, count(*) FROM mongo.orders`, testCatalog()); err == nil {
		t.Fatal("expected non-grouped column error")
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT region, count(*) FROM mongo.orders GROUP BY region`, testCatalog()); err != nil {
		t.Fatal(err)
	}
}

func TestGroupedHavingOrderAndWindowValidation(t *testing.T) {
	qs := []string{
		`SELECT region, count(*) AS n FROM mongo.orders GROUP BY region HAVING total > 100`,
		`SELECT region, count(*) AS n FROM mongo.orders GROUP BY region ORDER BY total DESC`,
		`SELECT region, sum(total) OVER () AS s FROM mongo.orders GROUP BY region`,
	}
	for _, q := range qs {
		if _, err := New(DefaultOptions()).Execute(t.Context(), q, testCatalog()); err == nil {
			t.Errorf("expected grouped validation error for %s", q)
		}
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT region, count(*) AS n FROM mongo.orders GROUP BY region HAVING count(*) > 0 ORDER BY region`, testCatalog()); err != nil {
		t.Fatal(err)
	}
}

func TestHashJoinSpill(t *testing.T) {
	opts := DefaultOptions()
	opts.JoinSpillRows = 1
	eng := New(opts)
	res, err := eng.Execute(t.Context(), `SELECT o.id, c.name FROM mongo.orders o JOIN mongo.customers c ON o.customer_id = c.id ORDER BY o.id`, testCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 4 || res.Rows[0]["name"] != "Acme" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestCostBasedJoinReorder(t *testing.T) {
	res := run(t, `SELECT o.id, c.name, i.category FROM mongo.orders o
		JOIN mongo.customers c ON o.customer_id = c.id
		JOIN shop.public.items i ON c.id = i.customer_id
		ORDER BY o.id`)
	if res.RowCount != 3 || res.Rows[0]["name"] != "Acme" || res.Rows[0]["category"] != "software" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}
