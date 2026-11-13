package sql

import "fmt"

// Error is a lexer or parser error with a source position. Line and column are
// one-based; Col counts Unicode code points within the line.
type Error struct {
	Message string
	Line    int
	Col     int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s at line %d, column %d", e.Message, e.Line, e.Col)
}

func errorAt(line, col int, format string, args ...any) error {
	return &Error{Message: fmt.Sprintf(format, args...), Line: line, Col: col}
}
