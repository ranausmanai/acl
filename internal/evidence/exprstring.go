package evidence

import (
	"fmt"
	"strings"
)

// ExprString renders an expression AST back to a human-readable string.
// Used for populating receipt CheckExpr / MustExpr fields.
func ExprString(e Expr) string {
	if e == nil {
		return ""
	}
	switch x := e.(type) {
	case *NullExpr:
		return "null"
	case *BoolExpr:
		if x.Val {
			return "true"
		}
		return "false"
	case *IntExpr:
		return fmt.Sprintf("%d", x.Val)
	case *FloatExpr:
		return fmt.Sprintf("%g", x.Val)
	case *StringExpr:
		return fmt.Sprintf("%q", x.Val)
	case *ListExpr:
		parts := make([]string, len(x.Items))
		for i, item := range x.Items {
			parts[i] = ExprString(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *IdentExpr:
		return x.Name
	case *AttrExpr:
		return ExprString(x.Obj) + "." + x.Field
	case *IndexExpr:
		return ExprString(x.Obj) + "[" + ExprString(x.Idx) + "]"
	case *BinOpExpr:
		return "(" + ExprString(x.Left) + " " + x.Op + " " + ExprString(x.Right) + ")"
	case *UnaryExpr:
		return x.Op + " " + ExprString(x.Operand)
	case *CallExpr:
		args := make([]string, len(x.Args))
		for i, a := range x.Args {
			args[i] = ExprString(a)
		}
		return x.Func + "(" + strings.Join(args, ", ") + ")"
	}
	return "<expr>"
}
