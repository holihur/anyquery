package sql

import "testing"

func FuzzFormatRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"SELECT 1",
		"SELECT pg.public.users.* FROM pg.public.users AS u",
		"SELECT region, count(*) FROM mongo.orders GROUP BY region, ROLLUP(status)",
		"SELECT * FROM (VALUES (1, 'a'), (2, 'b')) AS t(id, name)",
		"SELECT sum(total) OVER (PARTITION BY region ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM mongo.orders",
		"FROM mongo.orders |> WHERE status = 'paid' |> LEFT JOIN shop.products p ON o.id = p.id |> LIMIT 5",
		"WITH RECURSIVE nums(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM nums WHERE n < 3) SELECT * FROM nums",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		st, err := Parse(src)
		if err != nil {
			return
		}
		out := FormatStatement(st)
		if out == "" {
			return
		}
		if _, err := Parse(out); err != nil {
			t.Fatalf("formatted query does not parse: %v\ninput: %q\noutput: %q", err, src, out)
		}
	})
}
