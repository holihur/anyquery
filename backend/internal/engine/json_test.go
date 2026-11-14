package engine

import (
	"fmt"
	"testing"

	anysql "github.com/anyquery/anyquery/sql"
)

func TestJSONScalarFunctions(t *testing.T) {
	res := execParams(t, `SELECT
		JSON_VALUE('{"a": 1}', '$.a') AS v,
		JSON_QUERY('{"a": {"b": 2}}', '$.a') AS q,
		JSON_EXISTS('{"a": 1}', '$.a') AS e,
		JSON_VALUE('{"a": null}', '$.a') AS n`, nil)
	row := res.Rows[0]
	if row["v"] != float64(1) || row["q"] != `{"b":2}` || row["e"] != true || row["n"] != nil {
		t.Fatalf("row = %+v", row)
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT JSON_VALUE('{"a":1}', 'strict $.b')`, testCatalog()); err == nil {
		t.Fatal("expected strict JSON path error")
	}
	lax := execParams(t, `SELECT JSON_VALUE('{"a":1}', 'lax $.b') AS v`, nil)
	if lax.Rows[0]["v"] != nil {
		t.Fatalf("lax = %+v", lax.Rows[0])
	}
}

func TestJSONConstructors(t *testing.T) {
	res := execParams(t, `SELECT
		JSON_OBJECT('a': 1, 'b': 2) AS obj,
		JSON_OBJECT('a': 1, 'b': NULL ABSENT ON NULL) AS obj_absent,
		JSON_ARRAY(1, NULL, 2) AS arr,
		JSON_ARRAY(1, NULL ABSENT ON NULL) AS arr_absent`, nil)
	row := res.Rows[0]
	obj, ok := row["obj"].(map[string]any)
	if !ok || fmt.Sprint(obj["a"]) != "1" || fmt.Sprint(obj["b"]) != "2" {
		t.Fatalf("obj = %+v", row["obj"])
	}
	absent := row["obj_absent"].(map[string]any)
	if _, ok := absent["b"]; ok {
		t.Fatalf("absent = %+v", absent)
	}
	if arr := row["arr"].([]any); len(arr) != 3 || arr[1] != nil {
		t.Fatalf("arr = %+v", arr)
	}
	if arr := row["arr_absent"].([]any); len(arr) != 1 {
		t.Fatalf("arr_absent = %+v", arr)
	}
}

func TestJSONAggregates(t *testing.T) {
	arr := execParams(t, `SELECT JSON_ARRAYAGG(total ORDER BY total) AS a FROM mongo.orders`, nil)
	got := arr.Rows[0]["a"].([]any)
	want := []any{float64(25), float64(50), float64(100), float64(200)}
	if len(got) != len(want) {
		t.Fatalf("arrayagg = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arrayagg = %+v", got)
		}
	}
	obj := execParams(t, `SELECT JSON_OBJECTAGG(status: total) AS o FROM mongo.orders`, nil)
	m := obj.Rows[0]["o"].(map[string]any)
	if m["paid"] != float64(25) || m["refunded"] != float64(200) {
		t.Fatalf("objectagg = %+v", m)
	}
}

func TestIsJSON(t *testing.T) {
	res := execParams(t, `SELECT
		'{"a":1}' IS JSON OBJECT AS obj,
		'[1,2]' IS JSON ARRAY AS arr,
		'1' IS JSON SCALAR AS scalar,
		'not-json' IS JSON AS bad`, nil)
	row := res.Rows[0]
	if row["obj"] != true || row["arr"] != true || row["scalar"] != true || row["bad"] != false {
		t.Fatalf("row = %+v", row)
	}
}

func TestJSONTable(t *testing.T) {
	res := execParams(t, `SELECT id, tag FROM JSON_TABLE(
		'{"items":[{"id":1,"tags":["a","b"]},{"id":2,"tags":[]}]}',
		'$.items[*]' COLUMNS (
			id INT PATH '$.id',
			NESTED PATH '$.tags[*]' COLUMNS (tag VARCHAR(10) PATH '$')
		)
	) AS jt ORDER BY id, tag`, nil)
	if res.RowCount != 2 || res.Rows[0]["id"] != int64(1) || res.Rows[0]["tag"] != "a" || res.Rows[1]["tag"] != "b" {
		t.Fatalf("rows = %+v", res.Rows)
	}
	ord := execParams(t, `SELECT id, ord FROM JSON_TABLE(
		'{"items":[{"id":1},{"id":2}]}', '$.items[*]' COLUMNS (
			ord FOR ORDINALITY, id INT PATH '$.id'
		)
	) AS jt ORDER BY id`, nil)
	if ord.RowCount != 2 || ord.Rows[0]["ord"] != int64(1) || ord.Rows[1]["ord"] != int64(2) {
		t.Fatalf("ordinality = %+v", ord.Rows)
	}
}

func TestIsJSONUniqueKeys(t *testing.T) {
	res := execParams(t, `SELECT
		'{"a":1,"a":2}' IS JSON WITH UNIQUE KEYS AS dup,
		'{"a":1,"b":2}' IS JSON WITH UNIQUE KEYS AS ok`, nil)
	row := res.Rows[0]
	if row["dup"] != false || row["ok"] != true {
		t.Fatalf("row = %+v", row)
	}
}

func TestJSONValueReturningAndClauses(t *testing.T) {
	res := execParams(t, `SELECT
		JSON_VALUE('{"a":1.9}', '$.a' RETURNING INT) AS r,
		JSON_VALUE('{}', '$.a' NULL ON EMPTY) AS empty_null,
		JSON_QUERY('{"a":1}', '$.a' WITH WRAPPER) AS wrapped`, nil)
	row := res.Rows[0]
	if row["r"] != int64(1) || row["empty_null"] != nil || row["wrapped"] != `[1]` {
		t.Fatalf("row = %+v", row)
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT JSON_VALUE('{}', '$.a' ERROR ON ERROR)`, testCatalog()); err == nil {
		t.Fatal("expected ERROR ON ERROR")
	}
}

func TestJSONTableColumnOptions(t *testing.T) {
	res := execParams(t, `SELECT id, name FROM JSON_TABLE(
		'{"items":[{"id":1},{"id":2,"name":"x"}]}', '$.items[*]' COLUMNS (
			id INT PATH '$.id',
			name VARCHAR(10) PATH '$.name' DEFAULT 'missing' ON EMPTY NULL ON ERROR
		)) AS jt ORDER BY id`, nil)
	if res.RowCount != 2 || res.Rows[0]["name"] != "missing" || res.Rows[1]["name"] != "x" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestJSONTableFormatterPreservesDefaults(t *testing.T) {
	query := `SELECT id FROM JSON_TABLE('{"a":[{}]}', '$.a[*]' COLUMNS (id INT PATH '$.id' DEFAULT 7 ON EMPTY)) AS jt`
	res, err := New(DefaultOptions()).Execute(t.Context(), query, testCatalog())
	if err != nil || res.Rows[0]["id"] != int64(7) {
		t.Fatalf("original: %v %+v", err, res)
	}
	stmt, err := anysql.Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	formatted := anysql.FormatStatement(stmt)
	res, err = New(DefaultOptions()).Execute(t.Context(), formatted, testCatalog())
	if err != nil || res.Rows[0]["id"] != int64(7) {
		t.Fatalf("formatted %q: %v %+v", formatted, err, res)
	}
}

func TestJSONQueryWrappersAndReturning(t *testing.T) {
	res := execParams(t, `SELECT
		JSON_QUERY('{"a":[1,2]}', '$.a[*]' WITH WRAPPER) AS wrapped,
		JSON_VALUE('{"a":1.96}', '$.a' RETURNING DECIMAL(10,1)) AS rounded,
		JSON_VALUE('{"a":"abcdef"}', '$.a' RETURNING CHAR(3)) AS shortened`, nil)
	row := res.Rows[0]
	if row["wrapped"] != `[1,2]` || row["rounded"] != 2.0 || row["shortened"] != "abc" {
		t.Fatalf("row = %+v", row)
	}
}

func TestJSONTableExistsPath(t *testing.T) {
	res := execParams(t, `SELECT id, has FROM JSON_TABLE(
		'{"items":[{"id":1,"x":1},{"id":2}]}', '$.items[*]' COLUMNS (
			id INT PATH '$.id', has EXISTS PATH '$.x'
		)) AS jt ORDER BY id`, nil)
	if res.Rows[0]["has"] != true || res.Rows[1]["has"] != false {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestJSONPathPredicatesAndPassing(t *testing.T) {
	res := execParams(t, `SELECT
		JSON_EXISTS('{"a":1}', '$.a ? (@ == $x)' PASSING 1 AS x) AS passing,
		JSON_EXISTS('{"a":[1,2]}', '$.a[*] ? (@ > 1)') AS filtered,
		JSON_QUERY('{"a":[1,2]}', '$.a[*] ? (@ > 1)' WITH WRAPPER) AS wrapped`, nil)
	row := res.Rows[0]
	if row["passing"] != true || row["filtered"] != true || row["wrapped"] != `[2]` {
		t.Fatalf("row = %+v", row)
	}
}

func TestJSONExistsFalseAndWrapperEdges(t *testing.T) {
	res := execParams(t, `SELECT
		JSON_EXISTS('{"a":1}', '$.b') AS missing,
		JSON_EXISTS('{"a":1}', '$.a ? (@ == $x)' PASSING 2 AS x) AS passing_false,
		JSON_QUERY('{"a":1}', '$.a' WITHOUT ARRAY WRAPPER) AS scalar_text,
		JSON_QUERY('{"a":[1,2]}', '$.a' WITH CONDITIONAL WRAPPER) AS conditional,
		JSON_QUERY('{"a":[1,2]}', '$.a' WITH ARRAY WRAPPER) AS array_wrapper`, nil)
	row := res.Rows[0]
	if row["missing"] != false || row["passing_false"] != false || row["scalar_text"] != "1" || row["conditional"] != `[1,2]` || row["array_wrapper"] != `[[1,2]]` {
		t.Fatalf("row = %+v", row)
	}
	if _, err := New(DefaultOptions()).Execute(t.Context(), `SELECT JSON_QUERY('{"a":1}', '$.a')`, testCatalog()); err == nil {
		t.Fatal("expected scalar JSON_QUERY error")
	}
	falseErr := execParams(t, `SELECT JSON_EXISTS('{"a":1}', 'strict $.b' FALSE ON ERROR) AS x`, nil)
	if falseErr.Rows[0]["x"] != false {
		t.Fatalf("false on error = %+v", falseErr.Rows[0])
	}
}
