package engine

import (
	"testing"
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
