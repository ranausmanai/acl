"""
ECL Evidence Engine.

Safely evaluates boolean CHECK/MUST expressions and general-value RESULT /
argument expressions against a runtime environment dict.

Supported constructs
--------------------
* Literals          – strings, numbers, booleans, None
* Names             – variable lookup in `env`
* Attribute access  – obj.field  (works on dicts too: obj["field"])
* Subscript         – obj[index]
* Comparisons       – == != < <= > >=
* Boolean ops       – and  or  not
* Functions         – has(obj, key)
                      len(x)  /  count(x)
                      matches(text, pattern)
                      all(iterable, field_name)   → every item has that key
                      any(iterable, field_name)   → some item has that key
* List literals     – [a, b, c]

Security: uses Python's `ast` module with an explicit whitelist. `eval()`
is never called.
"""

from __future__ import annotations

import ast
import re
from typing import Any


class EvidenceError(Exception):
    """Raised when an expression cannot be evaluated."""


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------

def evaluate(expr: str, env: dict[str, Any]) -> Any:
    """
    Evaluate *expr* against *env* and return the raw Python value.

    For CHECK/MUST callers, cast the result to bool.
    """
    try:
        tree = ast.parse(expr.strip(), mode="eval")
    except SyntaxError as exc:
        raise EvidenceError(f"Syntax error in expression {expr!r}: {exc}") from exc
    evaluator = _Evaluator(env)
    return evaluator.eval_node(tree.body)


def check(expr: str, env: dict[str, Any]) -> bool:
    """Evaluate *expr* and coerce to bool (for CHECK / MUST gates)."""
    return bool(evaluate(expr, env))


# ---------------------------------------------------------------------------
# Internal evaluator
# ---------------------------------------------------------------------------

class _Evaluator:
    def __init__(self, env: dict[str, Any]) -> None:
        self.env = env

    def eval_node(self, node: ast.expr) -> Any:  # type: ignore[name-defined]
        match node:
            # --- literals ------------------------------------------------
            case ast.Constant():
                return node.value

            # --- name (variable lookup) ----------------------------------
            case ast.Name(id=name):
                if name in self.env:
                    return self.env[name]
                if name in ("True", "False", "None"):
                    return {"True": True, "False": False, "None": None}[name]
                raise EvidenceError(f"Undefined variable: {name!r}")

            # --- attribute access: obj.field -----------------------------
            case ast.Attribute(value=obj_node, attr=attr):
                obj = self.eval_node(obj_node)
                return _getattr_safe(obj, attr)

            # --- subscript: obj[idx] -------------------------------------
            case ast.Subscript(value=obj_node, slice=slice_node):
                obj = self.eval_node(obj_node)
                idx = self.eval_node(slice_node)
                try:
                    return obj[idx]
                except (KeyError, IndexError, TypeError) as exc:
                    raise EvidenceError(f"Subscript error: {exc}") from exc

            # --- list literal --------------------------------------------
            case ast.List(elts=elts):
                return [self.eval_node(e) for e in elts]

            # --- tuple ---------------------------------------------------
            case ast.Tuple(elts=elts):
                return tuple(self.eval_node(e) for e in elts)

            # --- unary ops -----------------------------------------------
            case ast.UnaryOp(op=ast.Not(), operand=operand):
                return not self.eval_node(operand)
            case ast.UnaryOp(op=ast.USub(), operand=operand):
                return -self.eval_node(operand)
            case ast.UnaryOp(op=ast.UAdd(), operand=operand):
                return +self.eval_node(operand)

            # --- boolean ops ---------------------------------------------
            case ast.BoolOp(op=ast.And(), values=values):
                result = True
                for v in values:
                    result = self.eval_node(v)
                    if not result:
                        return result
                return result
            case ast.BoolOp(op=ast.Or(), values=values):
                for v in values:
                    result = self.eval_node(v)
                    if result:
                        return result
                return result

            # --- comparisons ---------------------------------------------
            case ast.Compare(left=left_node, ops=ops, comparators=comps):
                left = self.eval_node(left_node)
                for op, right_node in zip(ops, comps):
                    right = self.eval_node(right_node)
                    if not _compare(left, op, right):
                        return False
                    left = right
                return True

            # --- function calls ------------------------------------------
            case ast.Call(func=func_node, args=args_nodes, keywords=kw_nodes):
                fname = _func_name(func_node)
                args = [self.eval_node(a) for a in args_nodes]
                kwargs = {kw.arg: self.eval_node(kw.value) for kw in kw_nodes}
                return self._call_builtin(fname, args, kwargs)

            # --- if expression -------------------------------------------
            case ast.IfExp(test=test, body=body, orelse=orelse):
                return self.eval_node(body) if self.eval_node(test) else self.eval_node(orelse)

            case _:
                raise EvidenceError(
                    f"Unsupported expression node: {ast.dump(node)}"
                )

    # ------------------------------------------------------------------
    # Built-in function dispatch
    # ------------------------------------------------------------------

    def _call_builtin(self, name: str, args: list, kwargs: dict) -> Any:
        match name:
            case "has":
                if len(args) != 2:
                    raise EvidenceError("has(obj, key) requires 2 arguments")
                obj, key = args
                if isinstance(obj, dict):
                    return key in obj
                return hasattr(obj, key)

            case "len" | "count":
                if len(args) != 1:
                    raise EvidenceError(f"{name}(x) requires 1 argument")
                try:
                    return len(args[0])
                except TypeError as exc:
                    raise EvidenceError(f"{name}() on non-sized object: {exc}") from exc

            case "matches":
                if len(args) != 2:
                    raise EvidenceError("matches(text, pattern) requires 2 arguments")
                text, pattern = args
                return bool(re.search(str(pattern), str(text)))

            case "all":
                # all(iterable, field_name_str)  → every item has that key
                if len(args) == 2:
                    iterable, field = args
                    if isinstance(field, str):
                        return all(
                            (field in item if isinstance(item, dict) else hasattr(item, field))
                            for item in iterable
                        )
                # all(iterable)  → standard truthiness
                return all(args[0]) if len(args) == 1 else False

            case "any":
                if len(args) == 2:
                    iterable, field = args
                    if isinstance(field, str):
                        return any(
                            (field in item if isinstance(item, dict) else hasattr(item, field))
                            for item in iterable
                        )
                return any(args[0]) if len(args) == 1 else False

            case "str":
                return str(args[0]) if args else ""
            case "int":
                return int(args[0]) if args else 0
            case "float":
                return float(args[0]) if args else 0.0
            case "bool":
                return bool(args[0]) if args else False
            case "list":
                return list(args[0]) if args else []

            case _:
                raise EvidenceError(f"Unknown function: {name!r}")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _getattr_safe(obj: Any, attr: str) -> Any:
    """Access obj.attr; supports dict, list, and regular objects."""
    if isinstance(obj, dict):
        if attr in obj:
            return obj[attr]
        raise EvidenceError(f"Key {attr!r} not found in dict")
    try:
        return getattr(obj, attr)
    except AttributeError as exc:
        raise EvidenceError(f"Object {type(obj).__name__!r} has no attribute {attr!r}") from exc


def _compare(left: Any, op: ast.cmpop, right: Any) -> bool:
    try:
        match op:
            case ast.Eq():  return left == right
            case ast.NotEq(): return left != right
            case ast.Lt():  return left < right
            case ast.LtE(): return left <= right
            case ast.Gt():  return left > right
            case ast.GtE(): return left >= right
            case ast.In():  return left in right
            case ast.NotIn(): return left not in right
            case _:
                raise EvidenceError(f"Unsupported comparison operator: {type(op).__name__}")
    except TypeError as exc:
        raise EvidenceError(f"Type error in comparison: {exc}") from exc


def _func_name(node: ast.expr) -> str:  # type: ignore[name-defined]
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute):
        return f"{_func_name(node.value)}.{node.attr}"
    raise EvidenceError(f"Cannot resolve function name from: {ast.dump(node)}")
