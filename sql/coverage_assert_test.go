package sql

import (
	"errors"
	"testing"
)

func checkParserError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var se *Error
	if !errors.As(err, &se) || se.Line < 1 || se.Col < 1 || se.Message == "" {
		t.Fatalf("parser returned non-positional error: %T %v", err, err)
	}
}
