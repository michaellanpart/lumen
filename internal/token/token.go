// Package token defines the lexical tokens of the Lumen language.
package token

import "fmt"

type Kind int

const (
	ILLEGAL Kind = iota
	EOF

	// Literals
	IDENT
	INT
	FLOAT
	STRING
	CHAR
	TRUE
	FALSE

	// Keywords
	FN
	LET
	MUT
	CONST
	COMPTIME
	RETURN
	IF
	ELSE
	MATCH
	WHILE
	FOR
	IN
	LOOP
	BREAK
	CONTINUE
	STRUCT
	ENUM
	TRAIT
	IMPL
	TYPE
	USE
	PUB
	MOD
	SPAWN
	CHAN
	SELECT
	DEFER
	AS
	WHERE
	SELF_TYPE // Self
	SELF_VAL  // self
	EXTERN
	MOVE
	// v0.6 Go-shape keywords
	FUNC
	VAR
	SWITCH
	CASE
	DEFAULT
	PACKAGE
	IMPORT

	// Operators
	PLUS       // +
	MINUS      // -
	STAR       // *
	SLASH      // /
	PERCENT    // %
	EQ         // =
	EQEQ       // ==
	NEQ        // !=
	LT         // <
	LTE        // <=
	GT         // >
	GTE        // >=
	AND        // &&
	OR         // ||
	NOT        // !
	AMP        // &
	PIPE       // |
	CARET      // ^
	TILDE      // ~
	SHL        // <<
	SHR        // >>
	PLUSEQ     // +=
	MINUSEQ    // -=
	STAREQ     // *=
	SLASHEQ    // /=
	PERCENTEQ  // %=
	AMPEQ      // &=
	PIPEEQ     // |=
	CARETEQ    // ^=
	SHLEQ      // <<=
	SHREQ      // >>=
	ARROW      // ->
	FATARROW   // =>
	COLONCOLON // ::
	DOT        // .
	DOTDOT     // ..
	DOTDOTEQ   // ..=
	COMMA      // ,
	SEMI       // ;
	COLON      // :
	LPAREN     // (
	RPAREN     // )
	LBRACE     // {
	RBRACE     // }
	LBRACK     // [
	RBRACK     // ]
	QUESTION   // ?
	AT         // @
	HASH       // #
	UNDERSCORE // _
	COLONEQ    // :=
)

var keywords = map[string]Kind{
	"fn":       FN,
	"let":      LET,
	"mut":      MUT,
	"const":    CONST,
	"comptime": COMPTIME,
	"return":   RETURN,
	"if":       IF,
	"else":     ELSE,
	"match":    MATCH,
	"while":    WHILE,
	"for":      FOR,
	"in":       IN,
	"loop":     LOOP,
	"break":    BREAK,
	"continue": CONTINUE,
	"struct":   STRUCT,
	"enum":     ENUM,
	"trait":    TRAIT,
	"impl":     IMPL,
	"type":     TYPE,
	"use":      USE,
	"pub":      PUB,
	"mod":      MOD,
	"spawn":    SPAWN,
	"chan":     CHAN,
	"select":   SELECT,
	"defer":    DEFER,
	"as":       AS,
	"where":    WHERE,
	"Self":     SELF_TYPE,
	"self":     SELF_VAL,
	"extern":   EXTERN,
	"move":     MOVE,
	"true":     TRUE,
	"false":    FALSE,
	// v0.6 Go-shape keywords
	"func":    FUNC,
	"var":     VAR,
	"switch":  SWITCH,
	"case":    CASE,
	"default": DEFAULT,
	"package": PACKAGE,
	"import":  IMPORT,
}

// Lookup returns the token kind for ident, or IDENT if it isn't a keyword.
func Lookup(ident string) Kind {
	if k, ok := keywords[ident]; ok {
		return k
	}
	return IDENT
}

// Pos is a 1-based line/column source position.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string { return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col) }

// Token is a lexed token with its literal text and source position.
type Token struct {
	Kind Kind
	Lit  string
	Pos  Pos
}

func (t Token) String() string {
	return fmt.Sprintf("%s(%q) at %s", t.Kind, t.Lit, t.Pos)
}

var kindNames = map[Kind]string{
	ILLEGAL: "ILLEGAL", EOF: "EOF", IDENT: "IDENT", INT: "INT", FLOAT: "FLOAT",
	STRING: "STRING", CHAR: "CHAR", TRUE: "true", FALSE: "false",
	FN: "fn", LET: "let", MUT: "mut", CONST: "const", COMPTIME: "comptime",
	RETURN: "return", IF: "if", ELSE: "else", MATCH: "match", WHILE: "while",
	FOR: "for", IN: "in", LOOP: "loop", BREAK: "break", CONTINUE: "continue",
	STRUCT: "struct", ENUM: "enum", TRAIT: "trait", IMPL: "impl", TYPE: "type",
	USE: "use", PUB: "pub", MOD: "mod", SPAWN: "spawn", CHAN: "chan",
	SELECT: "select", DEFER: "defer", AS: "as", WHERE: "where",
	SELF_TYPE: "Self", SELF_VAL: "self", EXTERN: "extern", MOVE: "move",
	PLUS: "+", MINUS: "-", STAR: "*", SLASH: "/", PERCENT: "%",
	EQ: "=", EQEQ: "==", NEQ: "!=", LT: "<", LTE: "<=", GT: ">", GTE: ">=",
	AND: "&&", OR: "||", NOT: "!", AMP: "&", PIPE: "|", CARET: "^", TILDE: "~",
	SHL: "<<", SHR: ">>",
	PLUSEQ: "+=", MINUSEQ: "-=", STAREQ: "*=", SLASHEQ: "/=", PERCENTEQ: "%=",
	AMPEQ: "&=", PIPEEQ: "|=", CARETEQ: "^=", SHLEQ: "<<=", SHREQ: ">>=",
	ARROW: "->", FATARROW: "=>", COLONCOLON: "::",
	DOT: ".", DOTDOT: "..", DOTDOTEQ: "..=",
	COMMA: ",", SEMI: ";", COLON: ":",
	LPAREN: "(", RPAREN: ")", LBRACE: "{", RBRACE: "}", LBRACK: "[", RBRACK: "]",
	QUESTION: "?", AT: "@", HASH: "#", UNDERSCORE: "_", COLONEQ: ":=",
	FUNC: "func", VAR: "var", SWITCH: "switch", CASE: "case", DEFAULT: "default",
	PACKAGE: "package", IMPORT: "import",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}
