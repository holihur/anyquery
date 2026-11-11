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
		{"id": int64(1), "name": "Acme", "segment": "enterprise"},
		{"id": int64(2), "name": "Globex", "segment": "smb"},
	}
	products := []map[string]any{
		{"id": int64(10), "customer_id": int64(1), "category": "software"},
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
				{Name: "customers", Columns: col("id", "name", "segment")},
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
