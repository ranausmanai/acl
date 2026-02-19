"""
ECL line-based DSL parser.

Grammar (EBNF sketch):

  program        := stmt*
  stmt           := intent | allow | limit | agent_def | template_def
                  | make_stmt | group_stmt

  intent         := INTENT '"' text '"'
  allow          := ALLOW tool (',' tool)*
  limit          := LIMIT (limit_kv)*
  limit_kv       := ('time' | 'calls' | 'retries') '=' value

  agent_def      := AGENT name NL agent_body
  template_def   := TEMPLATE name '(' params ')' NL agent_body
  agent_body     := (in_clause | out_clause | tools_clause | must_clause
                    | step_def | result_clause)*

  in_clause      := IN name (',' name)*
  out_clause     := OUT name (',' name)*
  tools_clause   := TOOLS tool (',' tool)*
  must_clause    := MUST expr
  result_clause  := RESULT expr

  step_def       := STEP name '=' ('TOOL' | 'AGENT') call_expr NL
                    (check_clause)? (onfail_clause)?
  check_clause   := CHECK expr
  onfail_clause  := ONFAIL onfail_policy
  onfail_policy  := 'retry' | 'fallback' name | 'askhuman' | 'stop'

  make_stmt      := MAKE name '(' kwargs ')' AS name
  group_stmt     := GROUP name '=' FOR name IN name ':' MAKE name '(' kwargs ')'

  call_expr      := name '(' kwargs ')'
  kwargs         := (name '=' expr (',' name '=' expr)*)?

Expressions are kept as raw strings and evaluated by the evidence engine.
Non-literal call arguments become ExprRef instances.
"""

from __future__ import annotations

import ast as pyast
import re
from typing import Any

from .models import (
    AgentDef, AllowNode, ExprRef, GroupStmt, IntentNode,
    LimitNode, MakeStmt, OnFail, StepDef, TemplateDef,
)

# Keywords that start a new top-level section
_TOP_LEVEL_KW = frozenset(
    {"AGENT", "TEMPLATE", "MAKE", "GROUP", "INTENT", "ALLOW", "LIMIT"}
)
# Keywords that are valid only inside an AGENT/TEMPLATE body
_AGENT_BODY_KW = frozenset({"IN", "OUT", "TOOLS", "MUST", "STEP", "RESULT"})
# Keywords that follow a STEP line
_STEP_TRAIL_KW = frozenset({"CHECK", "ONFAIL"})


class ParseError(Exception):
    pass


class Parser:
    """Stateful line-by-line parser."""

    def __init__(self, source: str) -> None:
        # Strip inline comments and blank lines lazily via _line property
        self._raw_lines: list[str] = source.splitlines()
        self.pos: int = 0

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    @property
    def _line(self) -> str | None:
        """Return the next non-blank, non-comment stripped line (or None)."""
        while self.pos < len(self._raw_lines):
            s = self._raw_lines[self.pos].strip()
            # strip inline comment
            s = re.sub(r'\s*#.*$', '', s).strip()
            if s:
                return s
            self.pos += 1
        return None

    def _advance(self) -> None:
        self.pos += 1

    def _consume(self) -> str:
        """Return current line and advance."""
        line = self._line
        self._advance()
        return line  # type: ignore[return-value]

    def _peek_kw(self) -> str | None:
        line = self._line
        if line is None:
            return None
        return line.split()[0]

    # ------------------------------------------------------------------
    # Top-level
    # ------------------------------------------------------------------

    def parse(self) -> list:
        nodes: list = []
        while self._line is not None:
            node = self._parse_stmt()
            if node is not None:
                nodes.append(node)
        return nodes

    def _parse_stmt(self):
        kw = self._peek_kw()
        if kw == "INTENT":
            return self._parse_intent()
        if kw == "ALLOW":
            return self._parse_allow()
        if kw == "LIMIT":
            return self._parse_limit()
        if kw == "AGENT":
            return self._parse_agent()
        if kw == "TEMPLATE":
            return self._parse_template()
        if kw == "MAKE":
            return self._parse_make()
        if kw == "GROUP":
            return self._parse_group()
        # Unknown top-level line – skip
        self._advance()
        return None

    # ------------------------------------------------------------------
    # Simple directives
    # ------------------------------------------------------------------

    def _parse_intent(self) -> IntentNode:
        line = self._consume()
        m = re.match(r'INTENT\s+"([^"]*)"', line)
        if not m:
            raise ParseError(f"Malformed INTENT: {line!r}")
        return IntentNode(text=m.group(1))

    def _parse_allow(self) -> AllowNode:
        line = self._consume()
        rest = line[len("ALLOW"):].strip()
        tools = _parse_csv(rest)
        return AllowNode(tools=tools)

    def _parse_limit(self) -> LimitNode:
        line = self._consume()
        rest = line[len("LIMIT"):].strip()
        node = LimitNode()
        for key, val in re.findall(r'(\w+)=([^\s]+)', rest):
            if key == "time":
                node.time_s = _parse_duration(val)
            elif key == "calls":
                node.calls = int(val)
            elif key == "retries":
                node.retries = int(val)
        return node

    # ------------------------------------------------------------------
    # AGENT definition
    # ------------------------------------------------------------------

    def _parse_agent(self) -> AgentDef:
        line = self._consume()
        m = re.match(r'AGENT\s+(\w+)', line)
        if not m:
            raise ParseError(f"Malformed AGENT: {line!r}")
        return self._parse_agent_body(m.group(1))

    def _parse_agent_body(self, name: str) -> AgentDef:
        """Parse IN/OUT/TOOLS/MUST/STEP/RESULT lines until a top-level KW."""
        agent = AgentDef(name=name)
        while self._line is not None:
            kw = self._peek_kw()
            if kw in _TOP_LEVEL_KW and kw not in _AGENT_BODY_KW:
                break
            line = self._line
            if kw == "IN":
                self._advance()
                agent.inputs = _parse_csv(line[2:].strip())
            elif kw == "OUT":
                self._advance()
                agent.outputs = _parse_csv(line[3:].strip())
            elif kw == "TOOLS":
                self._advance()
                agent.tools = _parse_csv(line[5:].strip())
            elif kw == "MUST":
                self._advance()
                agent.must_expr = line[4:].strip()
            elif kw == "STEP":
                agent.steps.append(self._parse_step())
            elif kw == "RESULT":
                self._advance()
                agent.result_expr = line[6:].strip()
            else:
                self._advance()  # skip unrecognised body lines
        return agent

    # ------------------------------------------------------------------
    # STEP definition
    # ------------------------------------------------------------------

    def _parse_step(self) -> StepDef:
        line = self._consume()
        # STEP <name> = TOOL <call> | STEP <name> = AGENT <call>
        m = re.match(r'STEP\s+(\w+)\s*=\s*(TOOL|AGENT)\s+(.+)', line)
        if not m:
            raise ParseError(f"Malformed STEP: {line!r}")
        step_name = m.group(1)
        step_kind = m.group(2).lower()
        call_str = m.group(3).strip()
        target, args = _parse_call(call_str)
        step = StepDef(name=step_name, kind=step_kind, target=target, args=args)

        # Collect optional CHECK and ONFAIL that immediately follow
        while self._line is not None:
            kw = self._peek_kw()
            if kw not in _STEP_TRAIL_KW:
                break
            trail = self._consume()
            if kw == "CHECK":
                step.check = trail[5:].strip()
            elif kw == "ONFAIL":
                step.onfail = _parse_onfail(trail[6:].strip())
        return step

    # ------------------------------------------------------------------
    # TEMPLATE definition
    # ------------------------------------------------------------------

    def _parse_template(self) -> TemplateDef:
        line = self._consume()
        m = re.match(r'TEMPLATE\s+(\w+)\(([^)]*)\)', line)
        if not m:
            raise ParseError(f"Malformed TEMPLATE: {line!r}")
        name = m.group(1)
        params = _parse_csv(m.group(2))
        # Body is the same grammar as agent body but may have ExprRef args
        body = self._parse_agent_body(name)
        return TemplateDef(name=name, params=params, body=body)

    # ------------------------------------------------------------------
    # MAKE statement
    # ------------------------------------------------------------------

    def _parse_make(self) -> MakeStmt:
        line = self._consume()
        # MAKE Template(k=v, ...) AS AgentName
        m = re.match(r'MAKE\s+(\w+)\(([^)]*)\)\s+AS\s+(\w+)', line)
        if not m:
            raise ParseError(f"Malformed MAKE: {line!r}")
        template = m.group(1)
        args = _parse_kwargs(m.group(2))
        agent_name = m.group(3)
        return MakeStmt(template=template, args=args, agent_name=agent_name)

    # ------------------------------------------------------------------
    # GROUP statement
    # ------------------------------------------------------------------

    def _parse_group(self) -> GroupStmt:
        line = self._consume()
        # GROUP Name = FOR var IN source : MAKE Template(k=v, ...)
        m = re.match(
            r'GROUP\s+(\w+)\s*=\s*FOR\s+(\w+)\s+IN\s+(\S+)\s*:\s*MAKE\s+(\w+)\(([^)]*)\)',
            line,
        )
        if not m:
            raise ParseError(f"Malformed GROUP: {line!r}")
        return GroupStmt(
            group_name=m.group(1),
            var=m.group(2),
            source=m.group(3),
            make_template=m.group(4),
            make_args_template=_parse_kwargs(m.group(5)),
        )


# ---------------------------------------------------------------------------
# Utility parsers (module-level, reusable)
# ---------------------------------------------------------------------------

def _parse_csv(s: str) -> list[str]:
    return [t.strip() for t in s.split(",") if t.strip()]


def _parse_duration(s: str) -> float:
    m = re.match(r'(\d+(?:\.\d+)?)(s|m|h)?$', s)
    if not m:
        raise ParseError(f"Invalid duration: {s!r}")
    val = float(m.group(1))
    unit = m.group(2) or "s"
    return val * {"s": 1.0, "m": 60.0, "h": 3600.0}[unit]


def _parse_call(s: str) -> tuple[str, dict[str, Any]]:
    """
    Parse 'name.space(k=v, k=v)' into (tool_name, args_dict).

    Literal values are stored directly; non-literal expressions become
    ExprRef instances to be evaluated at runtime.
    """
    try:
        tree = pyast.parse(s, mode="eval")
    except SyntaxError as exc:
        raise ParseError(f"Syntax error in call {s!r}: {exc}") from exc

    call = tree.body
    if not isinstance(call, pyast.Call):
        raise ParseError(f"Expected a call expression, got: {s!r}")

    tool_name = _unparse_name(call.func)
    args: dict[str, Any] = {}

    for i, arg in enumerate(call.args):
        try:
            args[f"_pos_{i}"] = pyast.literal_eval(arg)
        except (ValueError, TypeError):
            args[f"_pos_{i}"] = ExprRef(pyast.unparse(arg))

    for kw in call.keywords:
        try:
            args[kw.arg] = pyast.literal_eval(kw.value)
        except (ValueError, TypeError):
            args[kw.arg] = ExprRef(pyast.unparse(kw.value))

    return tool_name, args


def _parse_kwargs(s: str) -> dict[str, Any]:
    """Parse 'k=v, k=v' (no outer parens) → dict."""
    s = s.strip()
    if not s:
        return {}
    _, args = _parse_call(f"_({s})")
    return args


def _parse_onfail(s: str) -> OnFail:
    s = s.strip()
    if s == "retry":
        return OnFail(policy="retry")
    if s.startswith("fallback"):
        parts = s.split(None, 1)
        step_name = parts[1].strip() if len(parts) > 1 else None
        return OnFail(policy="fallback", fallback_step=step_name)
    if s == "askhuman":
        return OnFail(policy="askhuman")
    if s == "stop":
        return OnFail(policy="stop")
    raise ParseError(f"Unknown ONFAIL policy: {s!r}")


def _unparse_name(node: pyast.expr) -> str:
    if isinstance(node, pyast.Name):
        return node.id
    if isinstance(node, pyast.Attribute):
        return f"{_unparse_name(node.value)}.{node.attr}"
    raise ParseError(f"Cannot extract name from AST node: {pyast.dump(node)}")


# ---------------------------------------------------------------------------
# Public entry point
# ---------------------------------------------------------------------------

def parse(source: str) -> list:
    """Parse an ECL source string and return a list of AST nodes."""
    return Parser(source).parse()
