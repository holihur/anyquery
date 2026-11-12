package bigquery

import "testing"

func TestBigQueryTypesAndValues(t *testing.T) {
	if bqType("INT64") != "integer" || bqType("FLOAT64") != "number" || bqType("TIMESTAMP") != "timestamp" {
		t.Fatalf("unexpected type mapping")
	}
	if v := bqValue(bqField{Type: "INTEGER"}, "42"); v != int64(42) {
		t.Fatalf("integer = %#v", v)
	}
	if v := bqValue(bqField{Type: "BOOLEAN"}, "true"); v != true {
		t.Fatalf("boolean = %#v", v)
	}
}
