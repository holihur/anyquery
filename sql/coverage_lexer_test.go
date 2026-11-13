package sql

import (
	"errors"
	"strings"
	"testing"
)

func TestLexerTokenizeAndErrors(t *testing.T) {
	tokens, err := Tokenize("-- comment\nSELECT /* block */ `a`, [b], \"c\", $1, ?, 1.2e3, 'x''y', |> :: <= >= <> != == || && !~ ~* -> =>")
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) == 0 {
		t.Fatal("no tokens")
	}
	if !tokens[0].IsKeyword("SELECT") || tokens[1].Value != "a" || !tokens[1].Quoted {
		t.Fatalf("tokens = %+v (0=%q/%v/%v, 1=%q/%v/%v)", tokens, tokens[0].Value, tokens[0].Quoted, tokens[0].IsKeyword("SELECT"), tokens[1].Value, tokens[1].Quoted, tokens[1].IsKeyword("a"))
	}
	for _, src := range []string{
		"/* unterminated", "'unterminated", "`unterminated", "[unterminated", "@", "\\",
	} {
		if _, err := Tokenize(src); err == nil {
			t.Errorf("%q unexpectedly tokenized", src)
		}
	}
	if _, err := Tokenize("1e"); err == nil {
		t.Error("expected invalid number error")
	}
	if _, err := Tokenize("$x"); err != nil {
		t.Fatalf("$ identifier should tokenize: %v", err)
	}
}

func TestErrorString(t *testing.T) {
	err := &Error{Message: "bad", Line: 1, Col: 2}
	if !strings.Contains(err.Error(), "bad at line 1, column 2") {
		t.Fatalf("error = %s", err.Error())
	}
	if !errors.As(err, new(*Error)) {
		t.Fatal("Error does not match itself")
	}
}
