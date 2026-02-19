package evidence

import (
	"fmt"
	"math"
	"regexp"
)

// Env is a variable environment: maps names to runtime values.
// Values must be one of: nil, bool, int64, float64, string, []any, map[string]any.
type Env map[string]any

// Evaluate evaluates expr against env and returns the raw Go value.
// Callers that need a bool (CHECK / MUST) should call Check().
func Evaluate(expr *Expr, env Env) (any, error) {
	ev := evaluator{env: env}
	return ev.eval(*expr)
}

// Check evaluates expr and coerces the result to bool.
// Any evaluation error is treated as false (fail closed).
func Check(expr *Expr, env Env) (bool, error) {
	val, err := Evaluate(expr, env)
	if err != nil {
		return false, err
	}
	return truthy(val), nil
}

// ── evaluator ─────────────────────────────────────────────────────────────────

type evaluator struct {
	env Env
}

func (e *evaluator) eval(expr Expr) (any, error) {
	switch x := expr.(type) {
	case *NullExpr:
		return nil, nil
	case *BoolExpr:
		return x.Val, nil
	case *IntExpr:
		return x.Val, nil
	case *FloatExpr:
		return x.Val, nil
	case *StringExpr:
		return x.Val, nil
	case *ListExpr:
		result := make([]any, len(x.Items))
		for i, item := range x.Items {
			v, err := e.eval(item)
			if err != nil {
				return nil, err
			}
			result[i] = v
		}
		return result, nil
	case *IdentExpr:
		v, ok := e.env[x.Name]
		if !ok {
			return nil, errorf("undefined variable %q", x.Name)
		}
		return v, nil
	case *AttrExpr:
		obj, err := e.eval(x.Obj)
		if err != nil {
			return nil, err
		}
		return getAttr(obj, x.Field)
	case *IndexExpr:
		obj, err := e.eval(x.Obj)
		if err != nil {
			return nil, err
		}
		idx, err := e.eval(x.Idx)
		if err != nil {
			return nil, err
		}
		return getIndex(obj, idx)
	case *BinOpExpr:
		return e.evalBinOp(x)
	case *UnaryExpr:
		return e.evalUnary(x)
	case *CallExpr:
		return e.evalCall(x)
	}
	return nil, errorf("unsupported expression type %T", expr)
}

func (e *evaluator) evalBinOp(x *BinOpExpr) (any, error) {
	// Short-circuit boolean ops
	switch x.Op {
	case "and":
		left, err := e.eval(x.Left)
		if err != nil {
			return nil, err
		}
		if !truthy(left) {
			return false, nil
		}
		right, err := e.eval(x.Right)
		if err != nil {
			return nil, err
		}
		return truthy(right), nil
	case "or":
		left, err := e.eval(x.Left)
		if err != nil {
			return nil, err
		}
		if truthy(left) {
			return true, nil
		}
		right, err := e.eval(x.Right)
		if err != nil {
			return nil, err
		}
		return truthy(right), nil
	}

	left, err := e.eval(x.Left)
	if err != nil {
		return nil, err
	}
	right, err := e.eval(x.Right)
	if err != nil {
		return nil, err
	}

	switch x.Op {
	case "==":
		return equal(left, right), nil
	case "!=":
		return !equal(left, right), nil
	case "<":
		return compare(left, right, "<")
	case "<=":
		return compare(left, right, "<=")
	case ">":
		return compare(left, right, ">")
	case ">=":
		return compare(left, right, ">=")
	case "in":
		return containedIn(left, right)
	case "not in":
		ok, err := containedIn(left, right)
		return !ok, err
	case "+":
		return add(left, right)
	case "-":
		return sub(left, right)
	case "*":
		return mul(left, right)
	case "/":
		return div(left, right)
	}
	return nil, errorf("unknown binary operator %q", x.Op)
}

func (e *evaluator) evalUnary(x *UnaryExpr) (any, error) {
	operand, err := e.eval(x.Operand)
	if err != nil {
		return nil, err
	}
	switch x.Op {
	case "not":
		return !truthy(operand), nil
	case "-":
		switch v := operand.(type) {
		case int64:
			return -v, nil
		case float64:
			return -v, nil
		}
		return nil, errorf("unary - on non-numeric value")
	}
	return nil, errorf("unknown unary operator %q", x.Op)
}

// ── Built-in functions ────────────────────────────────────────────────────────

var allowedFuncs = map[string]bool{
	"has": true, "len": true, "count": true,
	"matches": true, "all": true, "any": true,
	"str": true, "int": true, "float": true, "bool": true, "list": true,
}

func (e *evaluator) evalCall(x *CallExpr) (any, error) {
	if !allowedFuncs[x.Func] {
		return nil, errorf("unknown function %q (allowed: has, len, count, matches, all, any)", x.Func)
	}
	args := make([]any, len(x.Args))
	for i, a := range x.Args {
		v, err := e.eval(a)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	switch x.Func {
	case "has":
		if len(args) != 2 {
			return nil, errorf("has() requires 2 arguments")
		}
		return hasKey(args[0], args[1])
	case "len", "count":
		if len(args) != 1 {
			return nil, errorf("%s() requires 1 argument", x.Func)
		}
		return length(args[0])
	case "matches":
		if len(args) != 2 {
			return nil, errorf("matches() requires 2 arguments")
		}
		text, ok := args[0].(string)
		if !ok {
			text = fmt.Sprintf("%v", args[0])
		}
		pattern, ok := args[1].(string)
		if !ok {
			return nil, errorf("matches() pattern must be string")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, errorf("matches() invalid regex: %v", err)
		}
		return re.MatchString(text), nil
	case "all":
		// all(list, "field") → every item in list has the named field
		if len(args) == 2 {
			list, ok := args[0].([]any)
			if !ok {
				return nil, errorf("all() first argument must be a list")
			}
			field, ok := args[1].(string)
			if !ok {
				return nil, errorf("all() second argument must be a field name string")
			}
			for _, item := range list {
				ok, err := hasKey(item, field)
				if err != nil || !ok {
					return false, err
				}
			}
			return true, nil
		}
		if len(args) == 1 {
			list, ok := args[0].([]any)
			if !ok {
				return nil, errorf("all() argument must be a list")
			}
			for _, item := range list {
				if !truthy(item) {
					return false, nil
				}
			}
			return true, nil
		}
		return nil, errorf("all() requires 1 or 2 arguments")
	case "any":
		if len(args) == 2 {
			list, ok := args[0].([]any)
			if !ok {
				return nil, errorf("any() first argument must be a list")
			}
			field, ok := args[1].(string)
			if !ok {
				return nil, errorf("any() second argument must be a field name string")
			}
			for _, item := range list {
				ok, err := hasKey(item, field)
				if err == nil && ok {
					return true, nil
				}
			}
			return false, nil
		}
		if len(args) == 1 {
			list, ok := args[0].([]any)
			if !ok {
				return nil, errorf("any() argument must be a list")
			}
			for _, item := range list {
				if truthy(item) {
					return true, nil
				}
			}
			return false, nil
		}
		return nil, errorf("any() requires 1 or 2 arguments")
	case "str":
		return fmt.Sprintf("%v", args[0]), nil
	case "int":
		return toInt64(args[0])
	case "float":
		return toFloat64(args[0])
	case "bool":
		return truthy(args[0]), nil
	case "list":
		switch v := args[0].(type) {
		case []any:
			return v, nil
		}
		return []any{args[0]}, nil
	}
	return nil, errorf("unimplemented function %q", x.Func)
}

// ── Helper utilities ──────────────────────────────────────────────────────────

func truthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

func equal(a, b any) bool {
	af, aOk := toFloat64(a)
	bf, bOk := toFloat64(b)
	if aOk == nil && bOk == nil {
		return af == bf
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func compare(a, b any, op string) (bool, error) {
	af, errA := toFloat64(a)
	bf, errB := toFloat64(b)
	if errA == nil && errB == nil {
		switch op {
		case "<":
			return af < bf, nil
		case "<=":
			return af <= bf, nil
		case ">":
			return af > bf, nil
		case ">=":
			return af >= bf, nil
		}
	}
	as, aStr := a.(string)
	bs, bStr := b.(string)
	if aStr && bStr {
		switch op {
		case "<":
			return as < bs, nil
		case "<=":
			return as <= bs, nil
		case ">":
			return as > bs, nil
		case ">=":
			return as >= bs, nil
		}
	}
	return false, errorf("cannot compare %T and %T with %s", a, b, op)
}

func containedIn(elem, container any) (bool, error) {
	switch c := container.(type) {
	case []any:
		for _, v := range c {
			if equal(v, elem) {
				return true, nil
			}
		}
		return false, nil
	case string:
		s, ok := elem.(string)
		if !ok {
			return false, errorf("'in' string requires string element")
		}
		return len(regexp.MustCompile(regexp.QuoteMeta(s)).FindString(c)) > 0, nil
	case map[string]any:
		s, ok := elem.(string)
		if !ok {
			return false, nil
		}
		_, exists := c[s]
		return exists, nil
	}
	return false, errorf("'in' requires list or string on right side, got %T", container)
}

func getAttr(obj any, field string) (any, error) {
	switch v := obj.(type) {
	case map[string]any:
		val, ok := v[field]
		if !ok {
			return nil, errorf("key %q not found in object", field)
		}
		return val, nil
	}
	return nil, errorf("cannot access field %q on %T", field, obj)
}

func getIndex(obj any, idx any) (any, error) {
	switch v := obj.(type) {
	case []any:
		i, err := toInt64(idx)
		if err != nil {
			return nil, errorf("list index must be integer, got %T", idx)
		}
		n := int(i)
		if n < 0 {
			n = len(v) + n
		}
		if n < 0 || n >= len(v) {
			return nil, errorf("index %d out of range [0,%d)", i, len(v))
		}
		return v[n], nil
	case map[string]any:
		s, ok := idx.(string)
		if !ok {
			return nil, errorf("map key must be string, got %T", idx)
		}
		val, ok := v[s]
		if !ok {
			return nil, errorf("key %q not found", s)
		}
		return val, nil
	}
	return nil, errorf("cannot index %T", obj)
}

func hasKey(obj any, key any) (bool, error) {
	k := fmt.Sprintf("%v", key)
	switch v := obj.(type) {
	case map[string]any:
		_, ok := v[k]
		return ok, nil
	}
	return false, nil
}

func length(v any) (int64, error) {
	switch x := v.(type) {
	case string:
		return int64(len(x)), nil
	case []any:
		return int64(len(x)), nil
	case map[string]any:
		return int64(len(x)), nil
	}
	return 0, errorf("len/count: unsupported type %T", v)
}

func toFloat64(v any) (float64, error) {
	switch x := v.(type) {
	case int64:
		return float64(x), nil
	case float64:
		return x, nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	}
	return 0, errorf("cannot convert %T to float64", v)
}

func toInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case float64:
		return int64(x), nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	}
	return 0, errorf("cannot convert %T to int64", v)
}

func add(a, b any) (any, error) {
	af, errA := toFloat64(a)
	bf, errB := toFloat64(b)
	if errA == nil && errB == nil {
		if _, ok := a.(int64); ok {
			if _, ok := b.(int64); ok {
				return int64(af + bf), nil
			}
		}
		return af + bf, nil
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as + bs, nil
	}
	return nil, errorf("cannot add %T and %T", a, b)
}

func sub(a, b any) (any, error) {
	af, errA := toFloat64(a)
	bf, errB := toFloat64(b)
	if errA == nil && errB == nil {
		if _, ok := a.(int64); ok {
			if _, ok := b.(int64); ok {
				return int64(af - bf), nil
			}
		}
		return af - bf, nil
	}
	return nil, errorf("cannot subtract %T and %T", a, b)
}

func mul(a, b any) (any, error) {
	af, errA := toFloat64(a)
	bf, errB := toFloat64(b)
	if errA == nil && errB == nil {
		return af * bf, nil
	}
	return nil, errorf("cannot multiply %T and %T", a, b)
}

func div(a, b any) (any, error) {
	af, errA := toFloat64(a)
	bf, errB := toFloat64(b)
	if errA == nil && errB == nil {
		if bf == 0 {
			return nil, errorf("division by zero")
		}
		return af / bf, nil
	}
	return nil, errorf("cannot divide %T and %T", a, b)
}
