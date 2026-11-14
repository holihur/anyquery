package sql

import (
	"errors"
	"testing"
)

func checkParserPositional(t *testing.T, err error) {
	t.Helper()
	var se *Error
	if err == nil || !errors.As(err, &se) || se.Line < 1 || se.Col < 1 || se.Message == "" {
		t.Fatalf("parser returned non-positional error: %T %v", err, err)
	}
}

func checkParserError(t *testing.T, err error) {
	t.Helper()
	checkParserPositional(t, err)
}

// checkParserOutcome is for branch corpora that intentionally mix syntactically
// valid and invalid fragments; it still requires positional errors for failures.
func checkParserOutcome(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		checkParserPositional(t, err)
	}
}
