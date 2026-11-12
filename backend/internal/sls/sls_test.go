package sls

import "testing"

func TestSLSTypeMapping(t *testing.T) {
	cases := map[string]string{
		"text": "string", "long": "integer", "double": "number", "bool": "boolean", "json": "json",
	}
	for in, want := range cases {
		if got := slsType(in); got != want {
			t.Fatalf("slsType(%q) = %q, want %q", in, got, want)
		}
	}
}
