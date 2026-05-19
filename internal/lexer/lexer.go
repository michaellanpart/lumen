// Package lexer turns Lumen source text into a stream of tokens.
package lexer

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lumen-lang/lumen/internal/token"
)

type Lexer struct {
	src      string
	file     string
	pos      int // byte offset
	line     int
	col      int
	errs     []string
	prevKind token.Kind // last non-skipped kind we emitted (for ASI)
	doneEOF  bool
}

func New(file, src string) *Lexer {
	return &Lexer{src: src, file: file, line: 1, col: 1}
}

func (l *Lexer) Errors() []string { return l.errs }

func (l *Lexer) errf(p token.Pos, format string, args ...any) {
	l.errs = append(l.errs, fmt.Sprintf("%s: %s", p, fmt.Sprintf(format, args...)))
}

func (l *Lexer) peek() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
	return r
}

func (l *Lexer) peekAt(off int) rune {
	p := l.pos
	for i := 0; i < off; i++ {
		if p >= len(l.src) {
			return 0
		}
		_, w := utf8.DecodeRuneInString(l.src[p:])
		p += w
	}
	if p >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[p:])
	return r
}

func (l *Lexer) advance() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	r, w := utf8.DecodeRuneInString(l.src[l.pos:])
	l.pos += w
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *Lexer) match(r rune) bool {
	if l.peek() == r {
		l.advance()
		return true
	}
	return false
}

func (l *Lexer) here() token.Pos {
	return token.Pos{File: l.file, Line: l.line, Col: l.col}
}

func (l *Lexer) skipWhitespaceAndComments() (semiPos token.Pos, semi bool) {
	for {
		switch r := l.peek(); {
		case r == ' ' || r == '\t' || r == '\r':
			l.advance()
		case r == '\n':
			if !semi && isSemiEligible(l.prevKind) {
				semiPos = l.here()
				semi = true
			}
			l.advance()
		case r == '/' && l.peekAt(1) == '/':
			for l.peek() != '\n' && l.peek() != 0 {
				l.advance()
			}
		case r == '/' && l.peekAt(1) == '*':
			l.advance()
			l.advance()
			depth := 1
			for depth > 0 && l.peek() != 0 {
				if l.peek() == '/' && l.peekAt(1) == '*' {
					l.advance()
					l.advance()
					depth++
				} else if l.peek() == '*' && l.peekAt(1) == '/' {
					l.advance()
					l.advance()
					depth--
				} else {
					l.advance()
				}
			}
		default:
			return
		}
	}
}

// isSemiEligible returns true if a newline appearing after a token of
// this kind should cause automatic semicolon insertion (Go-style).
func isSemiEligible(k token.Kind) bool {
	switch k {
	case token.IDENT, token.INT, token.FLOAT, token.STRING, token.CHAR,
		token.TRUE, token.FALSE,
		token.RETURN, token.BREAK, token.CONTINUE,
		token.RPAREN, token.RBRACK, token.RBRACE,
		token.SELF_VAL, token.SELF_TYPE, token.UNDERSCORE:
		return true
	}
	return false
}

// Tokenize lexes the entire source into a slice of tokens (terminated by EOF).
func (l *Lexer) Tokenize() []token.Token {
	var out []token.Token
	for {
		t := l.Next()
		out = append(out, t)
		if t.Kind == token.EOF {
			return out
		}
	}
}

// Next returns the next token.
func (l *Lexer) Next() token.Token {
	semiPos, semi := l.skipWhitespaceAndComments()
	if semi {
		l.prevKind = token.SEMI
		return tok(token.SEMI, ";", semiPos)
	}
	start := l.here()
	r := l.peek()
	if r == 0 {
		// Emit one synthetic SEMI before EOF if the last real token was ASI-eligible.
		if !l.doneEOF && isSemiEligible(l.prevKind) {
			l.doneEOF = true
			l.prevKind = token.SEMI
			return tok(token.SEMI, ";", start)
		}
		return token.Token{Kind: token.EOF, Pos: start}
	}
	t := l.lexOne(start, r)
	l.prevKind = t.Kind
	return t
}

func (l *Lexer) lexOne(start token.Pos, r rune) token.Token {

	switch {
	case isIdentStart(r):
		return l.lexIdent(start)
	case unicode.IsDigit(r):
		return l.lexNumber(start)
	case r == '"':
		return l.lexString(start)
	case r == '\'':
		return l.lexChar(start)
	}

	l.advance()
	switch r {
	case '+':
		if l.match('=') {
			return tok(token.PLUSEQ, "+=", start)
		}
		return tok(token.PLUS, "+", start)
	case '-':
		if l.match('=') {
			return tok(token.MINUSEQ, "-=", start)
		}
		if l.match('>') {
			return tok(token.ARROW, "->", start)
		}
		return tok(token.MINUS, "-", start)
	case '*':
		if l.match('=') {
			return tok(token.STAREQ, "*=", start)
		}
		return tok(token.STAR, "*", start)
	case '/':
		if l.match('=') {
			return tok(token.SLASHEQ, "/=", start)
		}
		return tok(token.SLASH, "/", start)
	case '%':
		if l.match('=') {
			return tok(token.PERCENTEQ, "%=", start)
		}
		return tok(token.PERCENT, "%", start)
	case '=':
		if l.match('=') {
			return tok(token.EQEQ, "==", start)
		}
		if l.match('>') {
			return tok(token.FATARROW, "=>", start)
		}
		return tok(token.EQ, "=", start)
	case '!':
		if l.match('=') {
			return tok(token.NEQ, "!=", start)
		}
		return tok(token.NOT, "!", start)
	case '<':
		if l.match('=') {
			return tok(token.LTE, "<=", start)
		}
		if l.match('<') {
			if l.match('=') {
				return tok(token.SHLEQ, "<<=", start)
			}
			return tok(token.SHL, "<<", start)
		}
		return tok(token.LT, "<", start)
	case '>':
		if l.match('=') {
			return tok(token.GTE, ">=", start)
		}
		if l.match('>') {
			if l.match('=') {
				return tok(token.SHREQ, ">>=", start)
			}
			return tok(token.SHR, ">>", start)
		}
		return tok(token.GT, ">", start)
	case '&':
		if l.match('&') {
			return tok(token.AND, "&&", start)
		}
		if l.match('=') {
			return tok(token.AMPEQ, "&=", start)
		}
		return tok(token.AMP, "&", start)
	case '|':
		if l.match('|') {
			return tok(token.OR, "||", start)
		}
		if l.match('=') {
			return tok(token.PIPEEQ, "|=", start)
		}
		return tok(token.PIPE, "|", start)
	case '^':
		if l.match('=') {
			return tok(token.CARETEQ, "^=", start)
		}
		return tok(token.CARET, "^", start)
	case '~':
		return tok(token.TILDE, "~", start)
	case ':':
		if l.match('=') {
			return tok(token.COLONEQ, ":=", start)
		}
		if l.match(':') {
			return tok(token.COLONCOLON, "::", start)
		}
		return tok(token.COLON, ":", start)
	case '.':
		if l.match('.') {
			if l.match('=') {
				return tok(token.DOTDOTEQ, "..=", start)
			}
			return tok(token.DOTDOT, "..", start)
		}
		return tok(token.DOT, ".", start)
	case ',':
		return tok(token.COMMA, ",", start)
	case ';':
		return tok(token.SEMI, ";", start)
	case '(':
		return tok(token.LPAREN, "(", start)
	case ')':
		return tok(token.RPAREN, ")", start)
	case '{':
		return tok(token.LBRACE, "{", start)
	case '}':
		return tok(token.RBRACE, "}", start)
	case '[':
		return tok(token.LBRACK, "[", start)
	case ']':
		return tok(token.RBRACK, "]", start)
	case '?':
		return tok(token.QUESTION, "?", start)
	case '@':
		return tok(token.AT, "@", start)
	case '#':
		return tok(token.HASH, "#", start)
	}

	l.errf(start, "unexpected character %q", r)
	return tok(token.ILLEGAL, string(r), start)
}

func (l *Lexer) lexIdent(start token.Pos) token.Token {
	begin := l.pos
	for isIdentPart(l.peek()) {
		l.advance()
	}
	lit := l.src[begin:l.pos]
	if lit == "_" {
		return tok(token.UNDERSCORE, lit, start)
	}
	return tok(token.Lookup(lit), lit, start)
}

func (l *Lexer) lexNumber(start token.Pos) token.Token {
	begin := l.pos
	isFloat := false
	// 0x / 0b / 0o prefixes
	if l.peek() == '0' {
		l.advance()
		switch l.peek() {
		case 'x', 'X', 'b', 'B', 'o', 'O':
			l.advance()
			for isHexDigit(l.peek()) || l.peek() == '_' {
				l.advance()
			}
			return tok(token.INT, strings.ReplaceAll(l.src[begin:l.pos], "_", ""), start)
		}
	}
	for unicode.IsDigit(l.peek()) || l.peek() == '_' {
		l.advance()
	}
	if l.peek() == '.' && unicode.IsDigit(l.peekAt(1)) {
		isFloat = true
		l.advance()
		for unicode.IsDigit(l.peek()) || l.peek() == '_' {
			l.advance()
		}
	}
	if l.peek() == 'e' || l.peek() == 'E' {
		isFloat = true
		l.advance()
		if l.peek() == '+' || l.peek() == '-' {
			l.advance()
		}
		for unicode.IsDigit(l.peek()) {
			l.advance()
		}
	}
	// Optional type suffix: i32, u64, f32, etc.
	if l.peek() == 'i' || l.peek() == 'u' || l.peek() == 'f' {
		for isIdentPart(l.peek()) {
			l.advance()
		}
	}
	lit := strings.ReplaceAll(l.src[begin:l.pos], "_", "")
	if isFloat {
		return tok(token.FLOAT, lit, start)
	}
	return tok(token.INT, lit, start)
}

func (l *Lexer) lexString(start token.Pos) token.Token {
	l.advance() // consume opening "
	var b strings.Builder
	for {
		r := l.peek()
		if r == 0 || r == '\n' {
			l.errf(start, "unterminated string literal")
			return tok(token.ILLEGAL, b.String(), start)
		}
		if r == '"' {
			l.advance()
			return tok(token.STRING, b.String(), start)
		}
		if r == '\\' {
			l.advance()
			esc := l.advance()
			switch esc {
			case 'n':
				b.WriteRune('\n')
			case 't':
				b.WriteRune('\t')
			case 'r':
				b.WriteRune('\r')
			case '\\':
				b.WriteRune('\\')
			case '"':
				b.WriteRune('"')
			case '0':
				b.WriteRune(0)
			default:
				l.errf(start, "unknown escape \\%c", esc)
			}
			continue
		}
		b.WriteRune(l.advance())
	}
}

func (l *Lexer) lexChar(start token.Pos) token.Token {
	l.advance() // '
	var ch rune
	if l.peek() == '\\' {
		l.advance()
		esc := l.advance()
		switch esc {
		case 'n':
			ch = '\n'
		case 't':
			ch = '\t'
		case 'r':
			ch = '\r'
		case '\\':
			ch = '\\'
		case '\'':
			ch = '\''
		case '0':
			ch = 0
		default:
			l.errf(start, "unknown escape \\%c", esc)
		}
	} else {
		ch = l.advance()
	}
	if l.peek() != '\'' {
		l.errf(start, "unterminated char literal")
		return tok(token.ILLEGAL, "", start)
	}
	l.advance()
	return tok(token.CHAR, string(ch), start)
}

func tok(k token.Kind, lit string, p token.Pos) token.Token {
	return token.Token{Kind: k, Lit: lit, Pos: p}
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
func isHexDigit(r rune) bool {
	return unicode.IsDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
