"""Tests for the ECL evidence engine."""

import pytest

from ecl.evidence import evaluate, check, EvidenceError


# ---------------------------------------------------------------------------
# Literals
# ---------------------------------------------------------------------------

def test_literal_int():
    assert evaluate("42", {}) == 42

def test_literal_str():
    assert evaluate('"hello"', {}) == "hello"

def test_literal_bool_true():
    assert evaluate("True", {}) is True

def test_literal_bool_false():
    assert evaluate("False", {}) is False

def test_literal_none():
    assert evaluate("None", {}) is None


# ---------------------------------------------------------------------------
# Variable lookup
# ---------------------------------------------------------------------------

def test_name_lookup():
    assert evaluate("x", {"x": 99}) == 99

def test_name_missing_raises():
    with pytest.raises(EvidenceError):
        evaluate("missing_var", {})


# ---------------------------------------------------------------------------
# Attribute access
# ---------------------------------------------------------------------------

def test_dict_attr_access():
    env = {"page": {"status": 200, "text": "hello"}}
    assert evaluate("page.status", env) == 200

def test_dict_nested_attr():
    env = {"result": {"rows": [{"plan": "Basic", "price": "$9"}]}}
    assert evaluate("result.rows[0].plan", env) == "Basic"

def test_attr_missing_raises():
    env = {"obj": {"a": 1}}
    with pytest.raises(EvidenceError):
        evaluate("obj.missing_key", env)


# ---------------------------------------------------------------------------
# Subscript
# ---------------------------------------------------------------------------

def test_list_index():
    assert evaluate("items[0]", {"items": [10, 20, 30]}) == 10

def test_list_index_negative():
    assert evaluate("items[-1]", {"items": [1, 2, 3]}) == 3

def test_dict_subscript():
    env = {"d": {"key": "val"}}
    assert evaluate('d["key"]', env) == "val"


# ---------------------------------------------------------------------------
# Comparisons
# ---------------------------------------------------------------------------

def test_eq():
    assert check("x == 200", {"x": 200}) is True

def test_neq():
    assert check("x != 200", {"x": 404}) is True

def test_lt():
    assert check("x < 10", {"x": 5}) is True

def test_gt():
    assert check("x > 5", {"x": 10}) is True

def test_lte():
    assert check("x <= 3", {"x": 3}) is True

def test_gte():
    assert check("x >= 3", {"x": 4}) is True

def test_chained_comparison():
    assert check("1 < x < 10", {"x": 5}) is True


# ---------------------------------------------------------------------------
# Boolean ops
# ---------------------------------------------------------------------------

def test_and_true():
    assert check("a == 1 and b == 2", {"a": 1, "b": 2}) is True

def test_and_false():
    assert check("a == 1 and b == 3", {"a": 1, "b": 2}) is False

def test_or_true():
    assert check("a == 1 or b == 3", {"a": 1, "b": 2}) is True

def test_not():
    assert check("not a", {"a": False}) is True

def test_compound():
    env = {"status": 200, "text": "hello"}
    assert check("status == 200 and len(text) > 0", env) is True


# ---------------------------------------------------------------------------
# Built-in functions
# ---------------------------------------------------------------------------

def test_has_dict_true():
    assert check('has(obj, "key")', {"obj": {"key": "val"}}) is True

def test_has_dict_false():
    assert check('has(obj, "missing")', {"obj": {"key": "val"}}) is False

def test_len_list():
    assert evaluate("len(items)", {"items": [1, 2, 3]}) == 3

def test_count_list():
    assert evaluate("count(items)", {"items": [1, 2, 3]}) == 3

def test_len_string():
    assert evaluate("len(s)", {"s": "hello"}) == 5

def test_matches_true():
    assert check('matches(text, "\\\\d+")', {"text": "abc123"}) is True

def test_matches_false():
    assert check('matches(text, "^\\\\d+$")', {"text": "abc"}) is False

def test_all_field_true():
    rows = [{"price": "$9"}, {"price": "$19"}, {"price": "$49"}]
    assert check('all(rows, "price")', {"rows": rows}) is True

def test_all_field_false():
    rows = [{"price": "$9"}, {"name": "Basic"}]
    assert check('all(rows, "price")', {"rows": rows}) is False

def test_any_field_true():
    rows = [{"name": "Basic"}, {"price": "$9"}]
    assert check('any(rows, "price")', {"rows": rows}) is True

def test_any_field_false():
    rows = [{"name": "Basic"}, {"name": "Pro"}]
    assert check('any(rows, "price")', {"rows": rows}) is False


# ---------------------------------------------------------------------------
# Complex expressions from ECL example
# ---------------------------------------------------------------------------

def test_pricing_must():
    env = {
        "rows": {"rows": [{"plan": "Basic", "price": "$9"}, {"plan": "Pro", "price": "$49"}, {"plan": "Enterprise", "price": "$199"}]},
    }
    assert check('count(rows.rows) >= 3 and has(rows.rows[0], "plan")', env) is True

def test_pricing_must_fails():
    env = {"rows": {"rows": [{"plan": "Basic"}]}}
    assert check('count(rows.rows) >= 3', env) is False


# ---------------------------------------------------------------------------
# List literals
# ---------------------------------------------------------------------------

def test_list_literal():
    result = evaluate('["a", "b", "c"]', {})
    assert result == ["a", "b", "c"]


# ---------------------------------------------------------------------------
# Error handling
# ---------------------------------------------------------------------------

def test_unsupported_expr_raises():
    with pytest.raises(EvidenceError):
        evaluate("lambda x: x", {})

def test_unknown_function_raises():
    with pytest.raises(EvidenceError):
        evaluate("evil_func(x)", {"x": 1})

def test_type_error_in_comparison():
    with pytest.raises(EvidenceError):
        check('"hello" < 42', {})
