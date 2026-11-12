package engine

import (
	"context"
	"testing"

	"github.com/anyquery/anyquery/internal/connector"
)

type fakeConn struct{ tables map[string][]map[string]any }

func (f *fakeConn) Ping(context.Context) error { return nil }
func (f *fakeConn) Capabilities() connector.Capabilities {
	return connector.Capabilities{LimitPushdown: true}
}
func (f *fakeConn) Close() error                                          { return nil }
func (f *fakeConn) Introspect(context.Context) ([]connector.Table, error) { return nil, nil }
func (f *fakeConn) Fetch(_ context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
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
	mongoConn := &fakeConn{tables: map[string][]map[string]any{
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
