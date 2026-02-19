package evidence_test

import (
	"testing"

	"acl/internal/evidence"
)

func eval(t *testing.T, expr string, env evidence.Env) any {
	t.Helper()
	e, err := evidence.Parse(expr)
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	v, err := evidence.Evaluate(e, env)
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return v
}

func check(t *testing.T, expr string, env evidence.Env) bool {
	t.Helper()
	e, err := evidence.Parse(expr)
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	v, err := evidence.Check(e, env)
	if err != nil {
		t.Fatalf("check %q: %v", expr, err)
	}
	return v
}

func TestLiterals(t *testing.T) {
	env := evidence.Env{}
	if eval(t, "42", env) != int64(42) {
		t.Error("int literal")
	}
	if eval(t, "3.14", env) != 3.14 {
		t.Error("float literal")
	}
	if eval(t, `"hello"`, env) != "hello" {
		t.Error("string literal")
	}
	if eval(t, "true", env) != true {
		t.Error("bool true")
	}
	if eval(t, "false", env) != false {
		t.Error("bool false")
	}
	if eval(t, "null", env) != nil {
		t.Error("null literal")
	}
}

func TestArithmetic(t *testing.T) {
	env := evidence.Env{}
	if eval(t, "2 + 3", env) != int64(5) {
		t.Error("add")
	}
	if eval(t, "10 - 4", env) != int64(6) {
		t.Error("sub")
	}
	if eval(t, "6 / 2", env) != 3.0 {
		t.Error("div")
	}
}

func TestComparisons(t *testing.T) {
	env := evidence.Env{"x": int64(5)}
	if !check(t, "x > 3", env) {
		t.Error("gt")
	}
	if !check(t, "x == 5", env) {
		t.Error("eq")
	}
	if !check(t, "x != 6", env) {
		t.Error("ne")
	}
	if check(t, "x < 3", env) {
		t.Error("lt should be false")
	}
}

func TestBooleanOps(t *testing.T) {
	env := evidence.Env{"a": true, "b": false}
	if !check(t, "a and true", env) {
		t.Error("and true")
	}
	if check(t, "a and b", env) {
		t.Error("and false")
	}
	if !check(t, "a or b", env) {
		t.Error("or")
	}
	if !check(t, "not b", env) {
		t.Error("not")
	}
}

func TestAttrAccess(t *testing.T) {
	env := evidence.Env{
		"obj": map[string]any{"status": int64(200), "text": "hello"},
	}
	if eval(t, "obj.status", env) != int64(200) {
		t.Error("attr status")
	}
	if eval(t, "obj.text", env) != "hello" {
		t.Error("attr text")
	}
}

func TestIndexAccess(t *testing.T) {
	env := evidence.Env{
		"items": []any{int64(10), int64(20), int64(30)},
	}
	if eval(t, "items[0]", env) != int64(10) {
		t.Error("index 0")
	}
	if eval(t, "items[2]", env) != int64(30) {
		t.Error("index 2")
	}
	if eval(t, "items[-1]", env) != int64(30) {
		t.Error("negative index")
	}
}

func TestInOperator(t *testing.T) {
	env := evidence.Env{
		"items": []any{"a", "b", "c"},
		"obj":   map[string]any{"key": int64(1)},
	}
	if !check(t, `"b" in items`, env) {
		t.Error("in list")
	}
	if check(t, `"z" in items`, env) {
		t.Error("not in list")
	}
	if !check(t, `"key" in obj`, env) {
		t.Error("in map")
	}
}

func TestBuiltinLen(t *testing.T) {
	env := evidence.Env{"s": "hello", "l": []any{1, 2, 3}}
	if eval(t, "len(s)", env) != int64(5) {
		t.Error("len string")
	}
	if eval(t, "len(l)", env) != int64(3) {
		t.Error("len list")
	}
}

func TestBuiltinHas(t *testing.T) {
	env := evidence.Env{
		"m": map[string]any{"x": 1, "y": 2},
	}
	if !check(t, `has(m, "x")`, env) {
		t.Error("has existing")
	}
	if check(t, `has(m, "z")`, env) {
		t.Error("has missing")
	}
}

func TestBuiltinMatches(t *testing.T) {
	env := evidence.Env{"s": "hello world"}
	if !check(t, `matches(s, "hello")`, env) {
		t.Error("matches positive")
	}
	if check(t, `matches(s, "^world")`, env) {
		t.Error("matches negative")
	}
}

func TestBuiltinAll(t *testing.T) {
	env := evidence.Env{
		"rows": []any{
			map[string]any{"plan": "starter", "price": "29"},
			map[string]any{"plan": "pro", "price": "99"},
		},
	}
	if !check(t, `all(rows, "plan")`, env) {
		t.Error("all with field")
	}
}

func TestBuiltinAny(t *testing.T) {
	env := evidence.Env{
		"items": []any{false, false, true},
	}
	if !check(t, "any(items)", env) {
		t.Error("any")
	}
}

func TestCount(t *testing.T) {
	env := evidence.Env{"r": map[string]any{"rows": []any{1, 2, 3}}}
	if eval(t, "count(r.rows)", env) != int64(3) {
		t.Error("count via attr")
	}
}

func TestUndefinedVar(t *testing.T) {
	e, _ := evidence.Parse("undefined_var")
	_, err := evidence.Evaluate(e, evidence.Env{})
	if err == nil {
		t.Error("expected error for undefined variable")
	}
}

func TestStringConcat(t *testing.T) {
	env := evidence.Env{}
	result := eval(t, `"hello" + " " + "world"`, env)
	if result != "hello world" {
		t.Errorf("string concat: %v", result)
	}
}

func TestExprString(t *testing.T) {
	cases := []string{
		"x > 3",
		"len(items)",
		"obj.field",
		"items[0]",
		"a and b",
		"not x",
	}
	for _, c := range cases {
		e, err := evidence.Parse(c)
		if err != nil {
			t.Errorf("parse %q: %v", c, err)
			continue
		}
		s := evidence.ExprString(*e)
		if s == "" {
			t.Errorf("ExprString(%q) returned empty", c)
		}
	}
}
