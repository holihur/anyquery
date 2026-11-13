package engine

import (
	"context"
	"testing"

	"github.com/anyquery/anyquery/sql"
)

// sql2016ProfileCases is the executable side of
// docs/sql-2016-profile.md. Supported rows must pass the full
// parser -> engine -> formatter -> reparse path. Planned rows are skipped until
// their implementation flips the status.
type sql2016ProfileCase struct {
	ID     string
	Status string
	Query  string
	Params []any
}

var sql2016ProfileCases = []sql2016ProfileCase{
	{ID: "CORE-001", Status: "supported", Query: "SELECT region, total * 2 AS gross FROM mongo.orders"},
	{ID: "CORE-002", Status: "supported", Query: "SELECT o.region FROM mongo.orders o, mongo.customers c WHERE o.customer_id = c.id"},
	{ID: "CORE-003", Status: "supported", Query: "SELECT c.name FROM mongo.orders o LEFT JOIN mongo.customers c ON o.customer_id = c.id"},
	{ID: "CORE-004", Status: "supported", Query: "SELECT id, name FROM (VALUES (1, 'a'), (2, 'b')) AS t(id, name)"},
	{ID: "CORE-005", Status: "supported", Query: "SELECT t.x FROM (SELECT id FROM mongo.orders ORDER BY id) AS t(x) ORDER BY t.x"},
	{ID: "CORE-006", Status: "supported", Query: "VALUES (1, 'a'), (2, 'b')"},
	{ID: "CORE-007", Status: "supported", Query: "SELECT c.name, x.n FROM mongo.customers c, LATERAL (SELECT count(*) AS n FROM mongo.orders o WHERE o.customer_id = c.id) x ORDER BY c.name"},
	{ID: "CORE-008", Status: "supported", Query: "SELECT x FROM UNNEST(json_extract('[1, 2, 3]', '$')) AS u(x) ORDER BY x"},
	{ID: "CORE-009", Status: "supported", Query: "SELECT count(*) AS n FROM mongo.orders TABLESAMPLE SYSTEM (100)"},
	{ID: "CORE-010", Status: "supported", Query: "SELECT region, count(*) AS n FROM mongo.orders GROUP BY region HAVING count(*) > 1"},
	{ID: "CORE-011", Status: "supported", Query: "SELECT region, count(*) AS n FROM mongo.orders GROUP BY 1 ORDER BY 1"},
	{ID: "CORE-012", Status: "supported", Query: "SELECT region, status, count(*) AS n FROM mongo.orders GROUP BY region, ROLLUP(status)"},
	{ID: "CORE-013", Status: "supported", Query: "SELECT region FROM mongo.orders UNION SELECT region FROM mongo.customers"},
	{ID: "CORE-014", Status: "supported", Query: "WITH x AS (SELECT region, total FROM mongo.orders) SELECT region, sum(total) AS n FROM x GROUP BY region"},
	{ID: "CORE-015", Status: "supported", Query: "WITH RECURSIVE nums(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM nums WHERE n < 3) SELECT count(*) AS n FROM nums"},
	{ID: "CORE-016", Status: "supported", Query: "SELECT c.name, (SELECT count(*) FROM mongo.orders o WHERE o.customer_id = c.id) AS n FROM mongo.customers c"},
	{ID: "CORE-017", Status: "supported", Query: "SELECT id FROM mongo.orders ORDER BY id LIMIT 2 OFFSET 1"},
	{ID: "CORE-018", Status: "supported", Query: "SELECT id FROM mongo.orders ORDER BY id LIMIT ? OFFSET ?", Params: []any{2, 1}},
	{ID: "CORE-019", Status: "supported", Query: "SELECT id, region FROM mongo.orders ORDER BY region FETCH FIRST 1 ROWS WITH TIES"},
	{ID: "WIN-001", Status: "supported", Query: "SELECT id, row_number() OVER (PARTITION BY region ORDER BY total DESC) AS rn FROM mongo.orders"},
	{ID: "WIN-002", Status: "supported", Query: "SELECT id, sum(total) OVER (ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) AS s FROM mongo.orders"},
	{ID: "WIN-003", Status: "supported", Query: "SELECT id, sum(total) OVER w AS s FROM mongo.orders WINDOW w AS (PARTITION BY region ORDER BY id) ORDER BY id"},
	{ID: "WIN-004", Status: "supported", Query: "SELECT id, sum(total) OVER (ORDER BY region GROUPS BETWEEN 1 PRECEDING AND CURRENT ROW) AS s FROM mongo.orders ORDER BY id"},
	{ID: "WIN-005", Status: "supported", Query: "SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY total) AS median FROM mongo.orders"},
	{ID: "WIN-006", Status: "supported", Query: "SELECT id, sum(total) FILTER (WHERE status = 'paid') OVER () AS s FROM mongo.orders"},
	{ID: "EXPR-001", Status: "supported", Query: "SELECT id, total > 50 AND status <> 'refunded' AS x FROM mongo.orders"},
	{ID: "EXPR-002", Status: "supported", Query: "SELECT CAST(total AS INT) AS a, total::text AS b FROM mongo.orders"},
	{ID: "EXPR-003", Status: "supported", Query: "SELECT id FROM mongo.orders WHERE region IN ('EU', 'US') AND total BETWEEN 10 AND 200"},
	{ID: "EXPR-004", Status: "supported", Query: "SELECT TRIM(BOTH 'x' FROM 'xxaxx') AS t, OVERLAY('abcdef' PLACING 'XY' FROM 2 FOR 3) AS o"},
	{ID: "EXPR-005", Status: "supported", Query: "SELECT TIMESTAMP '2024-01-01 00:00:00' + INTERVAL '90 minutes' AS ts"},
	{ID: "EXPR-006", Status: "supported", Query: "SELECT TIMESTAMP '2024-01-01T12:00:00Z' AT TIME ZONE 'UTC' AS ts"},
	{ID: "EXPR-007", Status: "supported", Query: "SELECT id FROM mongo.orders WHERE status = ?", Params: []any{"paid"}},
	{ID: "JSON-001", Status: "supported", Query: `SELECT '{"a":1}' IS JSON OBJECT AS ok`},
	{ID: "JSON-002", Status: "supported", Query: `SELECT JSON_VALUE('{"a":1}', '$.a') AS v, JSON_QUERY('{"a":{"b":2}}', '$.a') AS q`},
	{ID: "JSON-003", Status: "supported", Query: `SELECT JSON_EXISTS('{"a":1}', '$.a') AS ok`},
	{ID: "JSON-004", Status: "supported", Query: `SELECT JSON_OBJECT('a': 1) AS o, JSON_ARRAY(1, 2) AS a`},
	{ID: "JSON-005", Status: "supported", Query: `SELECT JSON_ARRAYAGG(total ORDER BY total) AS a FROM mongo.orders`},
	{ID: "JSON-006", Status: "supported", Query: `SELECT id, tag FROM JSON_TABLE('{"items":[{"id":1,"tags":["a"]}]}', '$.items[*]' COLUMNS (id INT PATH '$.id', NESTED PATH '$.tags[*]' COLUMNS (tag VARCHAR(10) PATH '$'))) AS jt`},
	{ID: "TEMP-001", Status: "supported", Query: `SELECT id FROM mongo.orders FOR SYSTEM_TIME AS OF TIMESTAMP '2024-01-01T00:00:00Z'`},
	{ID: "RPR-001", Status: "supported", Query: `SELECT id FROM mongo.orders MATCH_RECOGNIZE (PARTITION BY region ORDER BY id MEASURES id AS id ONE ROW PER MATCH PATTERN (A B*) DEFINE A AS status = 'paid', B AS total > 0) AS mr`},
	{ID: "META-001", Status: "supported", Query: "SHOW TABLES FROM mongo"},
	{ID: "AQ-001", Status: "supported", Query: "FROM mongo.orders |> WHERE status = 'paid' |> AGGREGATE region, count(*) AS n GROUP BY region"},
}

func TestSQL2016ReadOnlyProfileMatrix(t *testing.T) {
	seen := map[string]bool{}
	for _, tc := range sql2016ProfileCases {
		tc := tc
		if tc.ID == "" {
			t.Fatal("profile case has an empty ID")
		}
		if seen[tc.ID] {
			t.Fatalf("duplicate profile case ID %s", tc.ID)
		}
		seen[tc.ID] = true
		switch tc.Status {
		case "supported", "partial", "planned":
		default:
			t.Fatalf("%s has invalid status %q", tc.ID, tc.Status)
		}
		t.Run(tc.ID, func(t *testing.T) {
			if tc.Status != "supported" {
				t.Skipf("%s is %s, not supported yet", tc.ID, tc.Status)
			}
			e := New(DefaultOptions())
			res, err := e.ExecuteParams(context.Background(), tc.Query, tc.Params, testCatalog())
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if res == nil {
				t.Fatal("nil result")
			}
			stmt, err := sql.Parse(tc.Query)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if sel, ok := stmt.(*sql.SelectStmt); ok {
				formatted := sql.FormatSelect(sel)
				if _, err := sql.Parse(formatted); err != nil {
					t.Fatalf("formatted query does not parse: %v\ninput: %s\noutput: %s", err, tc.Query, formatted)
				}
			}
		})
	}
}
