// Package evidence implements the ACL evidence language:
// a safe, sandboxed expression evaluator used by CHECK, MUST, and RESULT.
//
// Expression AST nodes are defined here.
// The parser (exprparser.go) produces them from source strings.
// The evaluator (eval.go) reduces them to Go values.
package evidence

import "fmt"

// ── Expression AST ────────────────────────────────────────────────────────────

// Expr is the common interface for all expression nodes.
type Expr interface{ exprTag() }

// Literals
type (
	NullExpr   struct{}
	BoolExpr   struct{ Val bool }
	IntExpr    struct{ Val int64 }
	FloatExpr  struct{ Val float64 }
	StringExpr struct{ Val string }
	ListExpr   struct{ Items []Expr }
)

// Variable / field access
type (
	IdentExpr  struct{ Name string }              // foo
	AttrExpr   struct{ Obj Expr; Field string }   // obj.field
	IndexExpr  struct{ Obj Expr; Idx Expr }       // obj[idx]
)

// Operators
type (
	BinOpExpr struct {
		Op    string // ==  !=  <  <=  >  >=  and  or  +  -  *  /  in
		Left  Expr
		Right Expr
	}
	UnaryExpr struct {
		Op      string // not  -
		Operand Expr
	}
)

// Function call  –  only whitelisted names are permitted at eval time.
type CallExpr struct {
	Func string
	Args []Expr
}

// ── exprTag markers ───────────────────────────────────────────────────────────

func (*NullExpr) exprTag()   {}
func (*BoolExpr) exprTag()   {}
func (*IntExpr) exprTag()    {}
func (*FloatExpr) exprTag()  {}
func (*StringExpr) exprTag() {}
func (*ListExpr) exprTag()   {}
func (*IdentExpr) exprTag()  {}
func (*AttrExpr) exprTag()   {}
func (*IndexExpr) exprTag()  {}
func (*BinOpExpr) exprTag()  {}
func (*UnaryExpr) exprTag()  {}
func (*CallExpr) exprTag()   {}

// ── Error ─────────────────────────────────────────────────────────────────────

// EvidenceError is returned when expression parsing or evaluation fails.
type EvidenceError struct{ Msg string }

func (e *EvidenceError) Error() string { return e.Msg }

func errorf(format string, args ...any) error {
	return &EvidenceError{Msg: fmt.Sprintf(format, args...)}
}
