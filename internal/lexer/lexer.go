package lexer

import (
	"fmt"
	"strings"
	"unicode"
)

// Lexer converts an ACL source string into a flat token slice.
//
// Design notes:
//   - Blank lines and #-comments are silently discarded.
//   - A NEWLINE token is emitted at the end of every meaningful line.
//   - Horizontal whitespace (spaces, tabs) between tokens on the same line
//     is consumed but not emitted.
//   - Consecutive NEWLINEs are collapsed to one.
type Lexer struct {
	src    []rune
	pos    int
	line   int
	col    int
	tokens []Token
}

// New creates a Lexer for the given source.
func New(src string) *Lexer {
	return &Lexer{src: []rune(src), line: 1, col: 0}
}

// Tokenize runs the lexer and returns the complete token list.
func (l *Lexer) Tokenize() ([]Token, error) {
	for l.pos < len(l.src) {
		ch := l.src[l.pos]

		switch {
		case ch == '\n':
			l.emitNewline()
			l.pos++
			l.line++
			l.col = 0

		case ch == '\r':
			l.pos++ // ignore CR in CRLF

		case ch == ' ' || ch == '\t':
			l.pos++
			l.col++

		case ch == '#':
			l.skipToEOL()

		case ch == '"':
			tok, err := l.readString()
			if err != nil {
				return nil, err
			}
			l.tokens = append(l.tokens, tok)

		case unicode.IsDigit(ch):
			l.tokens = append(l.tokens, l.readNumber())

		case unicode.IsLetter(ch) || ch == '_':
			l.tokens = append(l.tokens, l.readWord())

		default:
			tok, err := l.readPunct()
			if err != nil {
				return nil, err
			}
			l.tokens = append(l.tokens, tok)
		}
	}

	// Ensure file ends with NEWLINE then EOF
	l.emitNewline()
	l.tokens = append(l.tokens, Token{Type: EOF, Line: l.line, Col: l.col})
	return l.tokens, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (l *Lexer) peek(offset int) rune {
	i := l.pos + offset
	if i < len(l.src) {
		return l.src[i]
	}
	return 0
}

func (l *Lexer) emitNewline() {
	// Collapse consecutive NEWLINEs
	if len(l.tokens) > 0 && l.tokens[len(l.tokens)-1].Type == NEWLINE {
		return
	}
	// Don't emit a leading NEWLINE at the very start
	if len(l.tokens) == 0 {
		return
	}
	l.tokens = append(l.tokens, Token{Type: NEWLINE, Literal: "\n", Line: l.line, Col: l.col})
}

func (l *Lexer) skipToEOL() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.pos++
	}
}

func (l *Lexer) readString() (Token, error) {
	start := l.pos
	startCol := l.col
	l.pos++ // consume opening "
	l.col++
	var sb strings.Builder
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == '"' {
			l.pos++
			l.col++
			return Token{Type: STRING, Literal: sb.String(), Line: l.line, Col: startCol}, nil
		}
		if ch == '\n' {
			return Token{}, fmt.Errorf("L%d: unterminated string", l.line)
		}
		if ch == '\\' {
			l.pos++
			l.col++
			if l.pos >= len(l.src) {
				break
			}
			switch l.src[l.pos] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default:
				sb.WriteRune('\\')
				sb.WriteRune(l.src[l.pos])
			}
		} else {
			sb.WriteRune(ch)
		}
		l.pos++
		l.col++
	}
	_ = start
	return Token{}, fmt.Errorf("L%d: unterminated string", l.line)
}

func (l *Lexer) readNumber() Token {
	start := l.pos
	startCol := l.col
	isFloat := false
	for l.pos < len(l.src) && (unicode.IsDigit(l.src[l.pos]) || l.src[l.pos] == '.') {
		if l.src[l.pos] == '.' {
			isFloat = true
		}
		l.pos++
		l.col++
	}
	lit := string(l.src[start:l.pos])
	typ := INT
	if isFloat {
		typ = FLOAT
	}
	return Token{Type: typ, Literal: lit, Line: l.line, Col: startCol}
}

// readWord reads an identifier or keyword.
// In ACL, identifiers may contain dots (e.g. http.get used as tool names
// inside call expressions).  We read the bare word here; the parser joins
// dotted sequences where tool names are expected.
func (l *Lexer) readWord() Token {
	start := l.pos
	startCol := l.col
	for l.pos < len(l.src) && (unicode.IsLetter(l.src[l.pos]) || unicode.IsDigit(l.src[l.pos]) || l.src[l.pos] == '_') {
		l.pos++
		l.col++
	}
	lit := string(l.src[start:l.pos])

	// Check keyword table (keywords are UPPER_CASE)
	if tt, ok := keywords[lit]; ok {
		return Token{Type: tt, Literal: lit, Line: l.line, Col: startCol}
	}
	return Token{Type: IDENT, Literal: lit, Line: l.line, Col: startCol}
}

func (l *Lexer) readPunct() (Token, error) {
	ch := l.src[l.pos]
	startCol := l.col
	l.pos++
	l.col++

	switch ch {
	case '(':
		return Token{Type: LPAREN, Literal: "(", Line: l.line, Col: startCol}, nil
	case ')':
		return Token{Type: RPAREN, Literal: ")", Line: l.line, Col: startCol}, nil
	case '[':
		return Token{Type: LBRACKET, Literal: "[", Line: l.line, Col: startCol}, nil
	case ']':
		return Token{Type: RBRACKET, Literal: "]", Line: l.line, Col: startCol}, nil
	case ',':
		return Token{Type: COMMA, Literal: ",", Line: l.line, Col: startCol}, nil
	case '.':
		return Token{Type: DOT, Literal: ".", Line: l.line, Col: startCol}, nil
	case ':':
		return Token{Type: COLON, Literal: ":", Line: l.line, Col: startCol}, nil
	case '+':
		return Token{Type: PLUS, Literal: "+", Line: l.line, Col: startCol}, nil
	case '-':
		return Token{Type: MINUS, Literal: "-", Line: l.line, Col: startCol}, nil
	case '*':
		return Token{Type: STAR, Literal: "*", Line: l.line, Col: startCol}, nil
	case '/':
		return Token{Type: SLASH, Literal: "/", Line: l.line, Col: startCol}, nil
	case '=':
		if l.pos < len(l.src) && l.src[l.pos] == '=' {
			l.pos++
			l.col++
			return Token{Type: EQEQ, Literal: "==", Line: l.line, Col: startCol}, nil
		}
		return Token{Type: ASSIGN, Literal: "=", Line: l.line, Col: startCol}, nil
	case '!':
		if l.pos < len(l.src) && l.src[l.pos] == '=' {
			l.pos++
			l.col++
			return Token{Type: NEQ, Literal: "!=", Line: l.line, Col: startCol}, nil
		}
		return Token{}, fmt.Errorf("L%d: unexpected character '!'", l.line)
	case '<':
		if l.pos < len(l.src) && l.src[l.pos] == '=' {
			l.pos++
			l.col++
			return Token{Type: LTE, Literal: "<=", Line: l.line, Col: startCol}, nil
		}
		return Token{Type: LT, Literal: "<", Line: l.line, Col: startCol}, nil
	case '>':
		if l.pos < len(l.src) && l.src[l.pos] == '=' {
			l.pos++
			l.col++
			return Token{Type: GTE, Literal: ">=", Line: l.line, Col: startCol}, nil
		}
		return Token{Type: GT, Literal: ">", Line: l.line, Col: startCol}, nil
	}
	return Token{}, fmt.Errorf("L%d: unexpected character %q", l.line, ch)
}
