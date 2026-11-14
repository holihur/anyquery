package engine

import (
	"testing"
	"time"

	anysql "github.com/anyquery/anyquery/sql"
)

func TestIntervalLiteralsAndArithmetic(t *testing.T) {
	res := execParams(t, `SELECT
		INTERVAL '1 day' AS one_day,
		INTERVAL '1' DAY AS qualified,
		INTERVAL '1-2' YEAR TO MONTH AS ym,
		INTERVAL '1 02:03:04' DAY TO SECOND AS dts,
		INTERVAL '90 minutes' AS mins`, nil)
	row := res.Rows[0]
	if row["one_day"] != "1 days" || row["qualified"] != "1 days" {
		t.Fatalf("day intervals = %+v", row)
	}
	if row["ym"] != "14 mons" || row["dts"] != "1 days 02:03:04" || row["mins"] != "01:30:00" {
		t.Fatalf("intervals = %+v", row)
	}
	add := execParams(t, `SELECT TIMESTAMP '2024-01-01 00:00:00' + INTERVAL '90 minutes' AS ts`, nil)
	if add.Rows[0]["ts"] != "2024-01-01T01:30:00Z" {
		t.Fatalf("timestamp + interval = %+v", add.Rows[0])
	}
	diff := execParams(t, `SELECT TIMESTAMP '2024-01-02 00:00:00' - TIMESTAMP '2024-01-01 00:00:00' AS d`, nil)
	if diff.Rows[0]["d"] != "1 days" {
		t.Fatalf("timestamp diff = %+v", diff.Rows[0])
	}
	scaled := execParams(t, `SELECT INTERVAL '2 hours' * 1.5 AS i`, nil)
	if scaled.Rows[0]["i"] != "03:00:00" {
		t.Fatalf("scaled interval = %+v", scaled.Rows[0])
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT INTERVAL 'nope'`, testCatalog()); err == nil {
		t.Fatal("expected invalid interval error")
	}
}

func TestTrimAndOverlay(t *testing.T) {
	res := execParams(t, `SELECT
		TRIM(BOTH 'x' FROM 'xxaxx') AS both,
		TRIM(LEADING 'x' FROM 'xxaxx') AS leading,
		TRIM(TRAILING 'x' FROM 'xxaxx') AS trailing,
		TRIM('x' FROM 'xxaxx') AS implicit,
		OVERLAY('abcdef' PLACING 'XY' FROM 2 FOR 3) AS ov`, nil)
	row := res.Rows[0]
	if row["both"] != "a" || row["leading"] != "axx" || row["trailing"] != "xxa" || row["implicit"] != "a" || row["ov"] != "aXYef" {
		t.Fatalf("row = %+v", row)
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT OVERLAY('abc' PLACING 'x' FROM 'bad')`, testCatalog()); err == nil {
		t.Fatal("expected OVERLAY start type error")
	}
}

func TestTimezoneParsingAndCasts(t *testing.T) {
	res := execParams(t, `SELECT
		CAST('2024-01-01 12:00:00+02:00' AS TIMESTAMP) AS ts,
		CAST('2024-01-01T12:00:00Z' AS DATE) AS d,
		TIMEZONE('UTC', '2024-01-01T12:00:00+02:00') AS z`, nil)
	row := res.Rows[0]
	if row["ts"] != "2024-01-01T10:00:00Z" || row["d"] != "2024-01-01" || row["z"] != "2024-01-01T10:00:00Z" {
		t.Fatalf("row = %+v", row)
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT CAST('not-a-date' AS TIMESTAMP)`, testCatalog()); err == nil {
		t.Fatal("expected cast error")
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT TRY_CAST('not-a-date' AS TIMESTAMP) AS x`, testCatalog()); err != nil {
		t.Fatal(err)
	}
}

func TestAtTimeZone(t *testing.T) {
	res := execParams(t, `SELECT
		TIMESTAMP '2024-01-01T12:00:00Z' AT TIME ZONE 'UTC' AS wall,
		TIMESTAMP '2024-01-01T12:00:00Z' AT TIME ZONE 'Europe/Berlin' AS berlin`,
		nil)
	row := res.Rows[0]
	if row["wall"] != "2024-01-01 12:00:00" || row["berlin"] != "2024-01-01 13:00:00" {
		t.Fatalf("row = %+v", row)
	}
	naive := execParams(t, `SELECT TIMESTAMP '2024-01-01 12:00:00' AT TIME ZONE 'UTC' AS ts`, nil)
	if naive.Rows[0]["ts"] != "2024-01-01 12:00:00" {
		t.Fatalf("naive = %+v", naive.Rows[0])
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT TIMESTAMP '2024-01-01' AT TIME ZONE 'No/Such'`, testCatalog()); err == nil {
		t.Fatal("expected unknown timezone error")
	}
}

func TestCastOverflowAndNullFunctions(t *testing.T) {
	res := execParams(t, `SELECT
		CAST('1.9' AS INT) AS trunc,
		TRY_CAST('1e30' AS INT) AS overflow,
		TRIM(BOTH 'x' FROM NULL) AS trimmed,
		OVERLAY(NULL PLACING 'x' FROM 1) AS overlaid`, nil)
	row := res.Rows[0]
	if row["trunc"] != int64(1) || row["overflow"] != nil || row["trimmed"] != nil || row["overlaid"] != nil {
		t.Fatalf("row = %+v", row)
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT CAST('1e30' AS INT)`, testCatalog()); err == nil {
		t.Fatal("expected integer overflow error")
	}
}

func TestLikeEscapeAndSimilarTo(t *testing.T) {
	res := execParams(t, `SELECT
		'100%' LIKE '100\%' ESCAPE '\' AS escaped,
		'abc' SIMILAR TO 'a_c' AS similar,
		'abc' NOT SIMILAR TO 'x%' AS not_similar`, nil)
	row := res.Rows[0]
	if row["escaped"] != true || row["similar"] != true || row["not_similar"] != true {
		t.Fatalf("row = %+v", row)
	}
}

func TestLikeEscapeExactAndSQLRegex(t *testing.T) {
	res := execParams(t, `SELECT
		'a_b' LIKE 'a!_b' ESCAPE '!' AS escaped,
		'a!b' LIKE 'a!b' ESCAPE '!' AS literal,
		'abc' SIMILAR TO 'a(b|c)c' AS group,
		'abbc' SIMILAR TO 'a(b)+c' AS plus,
		'abc' SIMILAR TO '[a-z]+' AS class`, nil)
	row := res.Rows[0]
	if row["escaped"] != true || row["literal"] != true || row["group"] != true || row["plus"] != true || row["class"] != true {
		t.Fatalf("row = %+v", row)
	}
}

func TestIntervalMonthEndClamping(t *testing.T) {
	res := execParams(t, `SELECT
		DATE '2024-01-31' + INTERVAL '1' MONTH AS jan,
		DATE '2024-02-29' + INTERVAL '1' YEAR AS leap`, nil)
	if res.Rows[0]["jan"] != "2024-02-29T00:00:00Z" || res.Rows[0]["leap"] != "2025-02-28T00:00:00Z" {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func TestLocalDateTime(t *testing.T) {
	res := execParams(t, "SELECT LOCALTIME AS t, LOCALTIMESTAMP AS ts", nil)
	if res.Rows[0]["t"] == nil || res.Rows[0]["ts"] == nil {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func TestSimilarDotAndIsJSONNull(t *testing.T) {
	res := execParams(t, `SELECT 'abc' SIMILAR TO 'a.c' AS sim, NULL IS JSON AS j`, nil)
	if res.Rows[0]["sim"] != true || res.Rows[0]["j"] != nil {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func TestTimeIntervalAndOverlaps(t *testing.T) {
	res := execParams(t, `SELECT
		TIME '12:00:00' + INTERVAL '1 hour' AS t,
		(DATE '2024-01-01', DATE '2024-02-01') OVERLAPS (DATE '2024-01-15', DATE '2024-03-01') AS o`, nil)
	if res.Rows[0]["t"] != "13:00:00" || res.Rows[0]["o"] != true {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func TestDateTimeExtensions(t *testing.T) {
	res := execParams(t, `SELECT
		EXTRACT(MONTH FROM INTERVAL '1-2' YEAR TO MONTH) AS mon,
		(TIME '10:00:00', INTERVAL '1' HOUR) OVERLAPS (TIME '10:30:00', INTERVAL '1' HOUR) AS ov,
		LENGTH(CURRENT_TIME) AS ct`, nil)
	row := res.Rows[0]
	if row["mon"] != int64(2) || row["ov"] != true || row["ct"] != int64(8) {
		t.Fatalf("row = %+v", row)
	}
}

func TestSumEmptyIsNull(t *testing.T) {
	res := execParams(t, "SELECT sum(total) AS s FROM mongo.orders WHERE id > 100", nil)
	if res.Rows[0]["s"] != nil {
		t.Fatalf("empty sum = %+v", res.Rows[0])
	}
	res = execParams(t, "SELECT sum(x) AS s FROM (VALUES (NULL)) AS t(x)", nil)
	if res.Rows[0]["s"] != nil {
		t.Fatalf("null sum = %+v", res.Rows[0])
	}
}

func TestTimeSubtractInterval(t *testing.T) {
	res := execParams(t, "SELECT TIME '00:30:00' - INTERVAL '1' HOUR AS t", nil)
	if res.Rows[0]["t"] != "23:30:00" {
		t.Fatalf("time = %+v", res.Rows[0])
	}
}

func TestTemporalPrecision(t *testing.T) {
	res := execParams(t, `SELECT
		LENGTH(CURRENT_TIMESTAMP(0)) AS ts0,
		LENGTH(CURRENT_TIMESTAMP(3)) AS ts3,
		LENGTH(CURRENT_TIME(3)) AS t3,
		CAST('2024-01-01 12:00:00.123456' AS TIMESTAMP(3)) AS cast_ts,
		CAST('12:00:00.123456' AS TIME(3)) AS cast_t`, nil)
	row := res.Rows[0]
	if row["ts0"] != int64(20) || row["ts3"] != int64(24) || row["t3"] != int64(12) || row["cast_ts"] != "2024-01-01T12:00:00.123Z" || row["cast_t"] != "12:00:00.123" {
		t.Fatalf("row = %+v", row)
	}
}

func TestBetweenSymmetric(t *testing.T) {
	res := execParams(t, "SELECT 1 BETWEEN SYMMETRIC 2 AND 0 AS s, 1 BETWEEN ASYMMETRIC 0 AND 2 AS a", nil)
	if res.Rows[0]["s"] != true || res.Rows[0]["a"] != true {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func TestIntervalNegationAndTimezoneExtract(t *testing.T) {
	res := execParams(t, `SELECT -INTERVAL '1' DAY AS i, EXTRACT(TIMEZONE_HOUR FROM TIMESTAMP '2024-01-01 12:00:00+05:30') AS zh`, nil)
	if res.Rows[0]["i"] != "-1 days" || res.Rows[0]["zh"] != int64(5) {
		t.Fatalf("row = %+v", res.Rows[0])
	}
}

func TestOrderedSetRanks(t *testing.T) {
	res := execParams(t, `SELECT
		rank(100) WITHIN GROUP (ORDER BY total) AS r,
		dense_rank(100) WITHIN GROUP (ORDER BY total) AS d,
		percent_rank(100) WITHIN GROUP (ORDER BY total) AS p,
		cume_dist(100) WITHIN GROUP (ORDER BY total) AS c FROM mongo.orders`, nil)
	row := res.Rows[0]
	if row["r"] != int64(3) || row["d"] != int64(3) || row["p"] != float64(2)/3 || row["c"] != 0.75 {
		t.Fatalf("row = %+v", row)
	}
}

func TestIntervalFractionalScaling(t *testing.T) {
	res := execParams(t, `SELECT
		INTERVAL '1' DAY * 0.5 AS a,
		INTERVAL '1' DAY / 2 AS b,
		INTERVAL '3' DAY / 2 AS c`, nil)
	row := res.Rows[0]
	if row["a"] != "12:00:00" || row["b"] != "12:00:00" || row["c"] != "1 days 12:00:00" {
		t.Fatalf("row = %+v", row)
	}
}

func TestJSONOnErrorAndZoneTypes(t *testing.T) {
	res := execParams(t, `SELECT
		JSON_VALUE('{"a":1}', 'strict $.b' DEFAULT 'x' ON ERROR) AS dv,
		JSON_QUERY('{"a":1}', 'strict $.b' NULL ON ERROR) AS nq,
		JSON_QUERY('{"a":1}', 'strict $.b' EMPTY ARRAY ON ERROR) AS ea,
		JSON_QUERY('{"a":1}', 'strict $.b' EMPTY OBJECT ON ERROR) AS eo`, nil)
	row := res.Rows[0]
	if row["dv"] != "x" || row["nq"] != nil || len(row["ea"].([]any)) != 0 || len(row["eo"].(map[string]any)) != 0 {
		t.Fatalf("json = %+v", row)
	}
	zones := execParams(t, `SELECT
		TIMESTAMP WITH TIME ZONE '2024-01-01 12:00:00+05:30' AS ts,
		TIME WITH TIME ZONE '12:00:00+05:30' AS tm,
		TIMESTAMP '2024-01-01 12:00:00 UTC' AT LOCAL AS local`, nil)
	if zones.Rows[0]["ts"] != "2024-01-01T12:00:00+05:30" || zones.Rows[0]["tm"] != "12:00:00+05:30" {
		t.Fatalf("zones = %+v", zones.Rows[0])
	}
}

func TestTimeZoneResolutionAndIntervalCast(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("test", 8*3600)
	defer func() { time.Local = oldLocal }()
	res := execParams(t, `SELECT
		TIMESTAMP '2024-01-01 12:00:00' AT LOCAL AS local,
		TIMESTAMP '2024-01-01 12:00:00' AT TIME ZONE 'Europe/Paris' AS paris,
		CAST('1 day' AS INTERVAL) AS iv,
		CAST('1 day' AS INTERVAL DAY) AS ivd,
		CAST(INTERVAL '90' MINUTE AS INTERVAL HOUR TO MINUTE) AS ivq`, nil)
	row := res.Rows[0]
	if row["local"] != "2024-01-01T12:00:00+08:00" || row["paris"] != "2024-01-01 12:00:00" {
		t.Fatalf("zone row = %+v", row)
	}
	if row["iv"] != "1 days" || row["ivd"] != "1 days" || row["ivq"] != "01:30:00" {
		t.Fatalf("interval row = %+v", row)
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT TIME WITH TIME ZONE '12:00:00+05:30' AT LOCAL`, testCatalog()); err == nil {
		t.Fatal("expected AT LOCAL TIME error")
	}
}

func TestTypedZoneLiteralFormatterRoundTrip(t *testing.T) {
	query := `SELECT TIMESTAMPTZ '2024-01-01 12:00:00+05:30' AS x,
		EXTRACT(TIMEZONE_HOUR FROM TIMESTAMPTZ '2024-01-01 12:00:00+05:30') AS th,
		TIMESTAMP '2024-01-01 12:00:00+05:30' AS y`
	stmt, err := anysql.Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	formatted := anysql.FormatStatement(stmt)
	orig := execParams(t, query, nil)
	reparsed := execParams(t, formatted, nil)
	for _, key := range []string{"x", "th", "y"} {
		if orig.Rows[0][key] != reparsed.Rows[0][key] {
			t.Fatalf("%s: original %v formatted %v (%s)", key, orig.Rows[0][key], reparsed.Rows[0][key], formatted)
		}
	}
}
