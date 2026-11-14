package sql

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenKind classifies lexical tokens.
type TokenKind int

const (
	TokEOF TokenKind = iota
	TokIdent
	TokNumber
	TokString
	TokPunct
)

// String returns a stable name for the token kind.
func (k TokenKind) String() string {
	switch k {
	case TokEOF:
		return "EOF"
	case TokIdent:
		return "identifier"
	case TokNumber:
		return "number"
	case TokString:
		return "string"
	case TokPunct:
		return "punctuation"
	default:
		return "unknown"
	}
}

// Token is one lexical token. Quoted identifiers keep their original spelling
// (quotes stripped) and are marked Quoted so keywords can be used as names.
type Token struct {
	Kind    TokenKind
	Value   string
	Quoted  bool
	Bracket bool // quoted with [..] (SQL Server style)
	Line    int
	Col     int
}

func (t Token) IsKeyword(kw string) bool {
	return t.Kind == TokIdent && !t.Quoted && strings.EqualFold(t.Value, kw)
}

func (t Token) IsPunct(p string) bool {
	return t.Kind == TokPunct && t.Value == p
}

func (t Token) String() string {
	if t.Kind == TokEOF {
		return "end of query"
	}
	return strconv.Quote(t.Value)
}

// Lexer tokenizes a SQL string.
type Lexer struct {
	src  string
	pos  int
	line int
	col  int
}

func NewLexer(src string) *Lexer { return &Lexer{src: src, line: 1, col: 1} }

// Next returns the next token.
func (l *Lexer) Next() (Token, error) {
	for {
		if l.pos >= len(l.src) {
			return Token{Kind: TokEOF, Line: l.line, Col: l.col}, nil
		}
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if unicode.IsSpace(r) {
			l.advance(r, size)
			continue
		}
		if r == '-' && l.peekByte() == '-' {
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.advanceRune()
			}
			continue
		}
		if r == '/' && l.peekByte() == '*' {
			startLine, startCol := l.line, l.col
			l.advance(r, size)
			l.advanceRune() // *
			closed := false
			for l.pos < len(l.src) {
				if l.src[l.pos] == '*' && l.peekByte() == '/' {
					l.advanceRune()
					l.advanceRune()
					closed = true
					break
				}
				l.advanceRune()
			}
			if !closed {
				return Token{}, errorAt(startLine, startCol, "unterminated comment")
			}
			continue
		}
		break
	}

	startLine, startCol := l.line, l.col
	r, size := utf8.DecodeRuneInString(l.src[l.pos:])

	if r == '$' && l.pos+1 < len(l.src) && isDigitByte(l.src[l.pos+1]) {
		l.advance(r, size)
		return Token{Kind: TokPunct, Value: "$", Line: startLine, Col: startCol}, nil
	}

	if isIdentStart(r) {
		start := l.pos
		for l.pos < len(l.src) {
			r, size := utf8.DecodeRuneInString(l.src[l.pos:])
			if !isIdentPart(r) {
				break
			}
			l.advance(r, size)
		}
		return Token{Kind: TokIdent, Value: l.src[start:l.pos], Line: startLine, Col: startCol}, nil
	}

	if unicode.IsDigit(r) || (r == '.' && l.pos+1 < len(l.src) && isDigitByte(l.src[l.pos+1])) {
		return l.scanNumber(startLine, startCol)
	}

	switch r {
	case '\'':
		return l.scanString(startLine, startCol)
	case '"':
		return l.scanQuotedIdent('"', startLine, startCol)
	case '`':
		return l.scanQuotedIdent('`', startLine, startCol)
	case '[':
		return l.scanBracketIdent(startLine, startCol)
	}

	// Operators and punctuation, longest match first.
	two := ""
	if l.pos+1 < len(l.src) {
		two = l.src[l.pos : l.pos+2]
	}
	switch two {
	case "|>", "::", "<=", ">=", "<>", "!=", "==", "||", "&&", "!~", "~*", "!~*", "->", "=>":
		l.advance(r, size)
		l.advanceRune()
		return Token{Kind: TokPunct, Value: two, Line: startLine, Col: startCol}, nil
	}

	if strings.ContainsRune("()[]{}.,;:+*/%<>=|&~!-^?$", r) {
		l.advance(r, size)
		return Token{Kind: TokPunct, Value: string(r), Line: startLine, Col: startCol}, nil
	}

	return Token{}, errorAt(startLine, startCol, "unexpected character %q", r)
}

func (l *Lexer) scanNumber(line, col int) (Token, error) {
	start := l.pos
	hasDot := false
	hasExp := false
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c >= '0' && c <= '9':
			l.advanceRune()
		case c == '.' && !hasDot && !hasExp:
			hasDot = true
			l.advanceRune()
		case (c == 'e' || c == 'E') && !hasExp:
			hasExp = true
			l.advanceRune()
			if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
				l.advanceRune()
			}
		default:
			goto done
		}
	}
done:
	value := l.src[start:l.pos]
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		return Token{}, errorAt(line, col, "invalid number %q", value)
	}
	return Token{Kind: TokNumber, Value: value, Line: line, Col: col}, nil
}

func (l *Lexer) scanString(line, col int) (Token, error) {
	l.advanceRune() // opening quote
	var sb strings.Builder
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if r == '\'' {
			if l.peekByte() == '\'' {
				sb.WriteByte('\'')
				l.advanceRune()
				l.advanceRune()
				continue
			}
			l.advanceRune()
			return Token{Kind: TokString, Value: sb.String(), Line: line, Col: col}, nil
		}
		if r == '\\' && l.pos+1 < len(l.src) {
			l.advanceRune()
			esc, size := utf8.DecodeRuneInString(l.src[l.pos:])
			l.advance(esc, size)
			switch esc {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '\\':
				sb.WriteByte('\\')
			case '\'':
				sb.WriteByte('\'')
			default:
				sb.WriteRune(esc)
			}
			continue
		}
		sb.WriteRune(r)
		l.advance(r, size)
	}
	return Token{}, errorAt(line, col, "unterminated string")
}

func (l *Lexer) scanQuotedIdent(quote rune, line, col int) (Token, error) {
	l.advanceRune() // opening
	var sb strings.Builder
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if r == quote {
			if quote == '`' || quote == '"' {
				if l.peekByte() == byte(quote) {
					sb.WriteRune(quote)
					l.advanceRune()
					l.advanceRune()
					continue
				}
			}
			l.advanceRune()
			return Token{Kind: TokIdent, Value: sb.String(), Quoted: true, Line: line, Col: col}, nil
		}
		sb.WriteRune(r)
		l.advance(r, size)
	}
	return Token{}, errorAt(line, col, "unterminated quoted identifier")
}

func (l *Lexer) scanBracketIdent(line, col int) (Token, error) {
	l.advanceRune() // [
	var sb strings.Builder
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if r == ']' {
			if l.peekByte() == ']' {
				sb.WriteByte(']')
				l.advanceRune()
				l.advanceRune()
				continue
			}
			l.advanceRune()
			return Token{Kind: TokIdent, Value: sb.String(), Quoted: true, Bracket: true, Line: line, Col: col}, nil
		}
		sb.WriteRune(r)
		l.advance(r, size)
	}
	return Token{}, errorAt(line, col, "unterminated bracketed identifier")
}

func (l *Lexer) advanceRune() {
	r, size := utf8.DecodeRuneInString(l.src[l.pos:])
	l.advance(r, size)
}

func (l *Lexer) advance(r rune, size int) {
	l.pos += size
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
}

func (l *Lexer) peekByte() byte {
	if l.pos+1 >= len(l.src) {
		return 0
	}
	return l.src[l.pos+1]
}

func isIdentStart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }

// Tokenize is a helper for tests and editors.
func Tokenize(src string) ([]Token, error) {
	l := NewLexer(src)
	var out []Token
	for {
		t, err := l.Next()
		if err != nil {
			return nil, err
		}
		if t.Kind == TokEOF {
			return out, nil
		}
		out = append(out, t)
	}
}
