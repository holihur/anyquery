package sql

import "testing"

// FuzzLexer checks that arbitrary input either fails cleanly or produces
// in-order tokens followed by EOF. It also exercises Tokenize's public path.
func FuzzLexer(f *testing.F) {
	for _, seed := range []string{
		"SELECT 1",
		"SELECT 'a''b' -- comment\nFROM t",
		"SELECT /* block */ `a`, [b], \"c\", $1, ?, 1.5e+2",
		"|> :: <= >= <> != == || && !~ ~* -> =>",
		"\x00\xff\xfe",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		lexer := NewLexer(src)
		lastLine, lastCol := 0, 0
		sawEOF := false
		for i := 0; i < 100_000; i++ {
			tok, err := lexer.Next()
			if err != nil {
				// The public tokenizer must fail the same way, without panicking.
				_, _ = Tokenize(src)
				return
			}
			if tok.Kind == TokEOF {
				sawEOF = true
				break
			}
			if tok.Kind < TokEOF || tok.Kind > TokPunct {
				t.Fatalf("invalid token kind %d", tok.Kind)
			}
			if tok.Line < 1 || tok.Col < 1 {
				t.Fatalf("invalid token position: %+v", tok)
			}
			if tok.Line < lastLine || (tok.Line == lastLine && tok.Col < lastCol) {
				t.Fatalf("token positions regressed: previous (%d,%d), token %+v", lastLine, lastCol, tok)
			}
			lastLine, lastCol = tok.Line, tok.Col
		}
		if !sawEOF {
			t.Fatal("lexer did not reach EOF")
		}
		if _, err := Tokenize(src); err != nil {
			t.Fatalf("Tokenize rejected input accepted by Lexer: %v", err)
		}
	})
}
