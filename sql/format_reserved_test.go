package sql

import "testing"

func TestFormatReservedIdentifierRoundTrip(t *testing.T) {
	for _, input := range []string{
		"SELECT SELECT%0",
		"SELECT select FROM t",
		"SELECT \"select\" FROM t",
	} {
		stmt, err := Parse(input)
		if err != nil {
			t.Fatalf("parse %q: %v", input, err)
		}
		out := FormatStatement(stmt)
		if _, err := Parse(out); err != nil {
			t.Fatalf("reparse %q -> %q: %v", input, out, err)
		}
	}
}
