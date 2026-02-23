// Package lexer tokenises ACL source files.
//
// ACL is line-based: a statement begins with a keyword and runs to end-of-line.
// NEWLINE tokens mark statement boundaries; blank lines and #-comments are
// silently consumed.
package lexer

import "fmt"

// TokenType classifies a lexical token.
type TokenType int

const (
	// ── Meta ────────────────────────────────────────────────────────────────
	ILLEGAL TokenType = iota
	EOF
	NEWLINE

	// ── Literals ────────────────────────────────────────────────────────────
	STRING // "hello world"
	INT    // 42
	FLOAT  // 3.14
	IDENT  // identifier: foo, my_var

	// ── ACL Keywords ────────────────────────────────────────────────────────
	kwBEGIN_
	KWINTENT   // INTENT
	KWALLOW    // ALLOW
	KWLIMIT    // LIMIT
	KWAGENT    // AGENT
	KWTEMPLATE // TEMPLATE
	KWMAKE     // MAKE
	KWGROUP    // GROUP
	KWIN       // IN
	KWOUT      // OUT
	KWTOOLS    // TOOLS
	KWMUST     // MUST
	KWSTEP     // STEP
	KWCHECK    // CHECK
	KWONFAIL   // ONFAIL
	KWRESULT   // RESULT
	KWFOR      // FOR
	KWAS       // AS
	KWTOOL     // TOOL  (inside STEP)
	KWPARALLEL // PARALLEL
	KWREMOTE   // REMOTE
	KWSCHEDULE // SCHEDULE
	KWIF       // IF
	KWELSE     // ELSE
	KWEND      // END
	KWFOREACH  // FOREACH
	kwEND_

	// ── Punctuation / operators ─────────────────────────────────────────────
	LPAREN   // (
	RPAREN   // )
	LBRACKET // [
	RBRACKET // ]
	COMMA    // ,
	DOT      // .
	COLON    // :
	ASSIGN   // =  (used in arg lists and LIMIT k=v)
	EQEQ     // ==
	NEQ      // !=
	LT       // <
	LTE      // <=
	GT       // >
	GTE      // >=
	PLUS     // +
	MINUS    // -
	STAR     // *
	SLASH    // /
)

var tokenNames = map[TokenType]string{
	ILLEGAL: "ILLEGAL", EOF: "EOF", NEWLINE: "NEWLINE",
	STRING: "STRING", INT: "INT", FLOAT: "FLOAT", IDENT: "IDENT",
	KWINTENT: "INTENT", KWALLOW: "ALLOW", KWLIMIT: "LIMIT",
	KWAGENT: "AGENT", KWTEMPLATE: "TEMPLATE", KWMAKE: "MAKE",
	KWGROUP: "GROUP", KWIN: "IN", KWOUT: "OUT", KWTOOLS: "TOOLS",
	KWMUST: "MUST", KWSTEP: "STEP", KWCHECK: "CHECK",
	KWONFAIL: "ONFAIL", KWRESULT: "RESULT", KWFOR: "FOR",
	KWAS: "AS", KWTOOL: "TOOL", KWPARALLEL: "PARALLEL",
	KWREMOTE: "REMOTE", KWSCHEDULE: "SCHEDULE",
	KWIF: "IF", KWELSE: "ELSE", KWEND: "END", KWFOREACH: "FOREACH",
	LPAREN: "(", RPAREN: ")", LBRACKET: "[", RBRACKET: "]",
	COMMA: ",", DOT: ".", COLON: ":", ASSIGN: "=",
	EQEQ: "==", NEQ: "!=", LT: "<", LTE: "<=", GT: ">", GTE: ">=",
	PLUS: "+", MINUS: "-", STAR: "*", SLASH: "/",
}

func (t TokenType) String() string {
	if s, ok := tokenNames[t]; ok {
		return s
	}
	return fmt.Sprintf("TokenType(%d)", int(t))
}

// keywords maps upper-case ACL keyword strings to their token types.
var keywords = map[string]TokenType{
	"INTENT":   KWINTENT,
	"ALLOW":    KWALLOW,
	"LIMIT":    KWLIMIT,
	"AGENT":    KWAGENT,
	"TEMPLATE": KWTEMPLATE,
	"MAKE":     KWMAKE,
	"GROUP":    KWGROUP,
	"IN":       KWIN,
	"OUT":      KWOUT,
	"TOOLS":    KWTOOLS,
	"MUST":     KWMUST,
	"STEP":     KWSTEP,
	"CHECK":    KWCHECK,
	"ONFAIL":   KWONFAIL,
	"RESULT":   KWRESULT,
	"FOR":     KWFOR,
	"AS":      KWAS,
	"TOOL":    KWTOOL,
	"PARALLEL": KWPARALLEL,
	"REMOTE":  KWREMOTE,
	"SCHEDULE": KWSCHEDULE,
	"IF":      KWIF,
	"ELSE":    KWELSE,
	"END":     KWEND,
	"FOREACH": KWFOREACH,
}

// Token is a single lexical unit.
type Token struct {
	Type    TokenType
	Literal string // exact source text
	Line    int
	Col     int
}

func (t Token) String() string {
	return fmt.Sprintf("Token{%s %q L%d:C%d}", t.Type, t.Literal, t.Line, t.Col)
}

func (t Token) IsKeyword() bool {
	return t.Type > kwBEGIN_ && t.Type < kwEND_
}
