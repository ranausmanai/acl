"""
ECL Tool Registry and Agent Registry.

Tool Registry
-------------
Maps 'category.verb' names to Python callables.  Built-in tools are
registered at import time.  External tools can be added via register_tool().

Agent Registry
--------------
Stores all concrete AgentDef instances produced by the parser + template
expander.  The runtime looks up agents here when a STEP calls another agent.

Template / Group Expander
--------------------------
expand_templates() takes the raw list of AST nodes and returns a new list
where every MakeStmt and GroupStmt has been replaced by concrete AgentDef
instances.
"""

from __future__ import annotations

import re
from typing import Any, Callable

from .models import (
    AgentDef, ExprRef, GroupStmt, MakeStmt, StepDef, TemplateDef,
)
from . import evidence as ev


# ---------------------------------------------------------------------------
# Tool Registry
# ---------------------------------------------------------------------------

class ToolRegistry:
    def __init__(self) -> None:
        self._tools: dict[str, Callable] = {}
        self._versions: dict[str, str] = {}

    def register(
        self,
        name: str,
        fn: Callable,
        version: str = "1",
    ) -> None:
        self._tools[name] = fn
        self._versions[name] = version

    def get(self, name: str) -> Callable:
        if name not in self._tools:
            raise KeyError(f"Tool not registered: {name!r}")
        return self._tools[name]

    def version(self, name: str) -> str:
        return self._versions.get(name, "1")

    def has(self, name: str) -> bool:
        return name in self._tools

    def names(self) -> list[str]:
        return list(self._tools.keys())


# ---------------------------------------------------------------------------
# Agent Registry
# ---------------------------------------------------------------------------

class AgentRegistry:
    def __init__(self) -> None:
        self._agents: dict[str, AgentDef] = {}

    def register(self, agent: AgentDef) -> None:
        self._agents[agent.name] = agent

    def get(self, name: str) -> AgentDef:
        if name not in self._agents:
            raise KeyError(f"Agent not registered: {name!r}")
        return self._agents[name]

    def has(self, name: str) -> bool:
        return name in self._agents

    def all(self) -> list[AgentDef]:
        return list(self._agents.values())


# ---------------------------------------------------------------------------
# Template Expander
# ---------------------------------------------------------------------------

def expand_templates(
    nodes: list,
    vars_env: dict[str, Any] | None = None,
) -> list:
    """
    Walk the AST node list and replace MakeStmt / GroupStmt with
    concrete AgentDef instances.

    Returns a new list with only IntentNode, AllowNode, LimitNode, and
    AgentDef items (TemplateDef is consumed but not emitted).
    """
    vars_env = vars_env or {}
    templates: dict[str, TemplateDef] = {}
    result: list = []

    for node in nodes:
        if isinstance(node, TemplateDef):
            templates[node.name] = node
            # Templates are not emitted – only their expansions are.

        elif isinstance(node, MakeStmt):
            tdef = _require_template(templates, node.template)
            bindings = _resolve_make_args(node.args, vars_env, {})
            agent = _expand(tdef, bindings, node.agent_name)
            result.append(agent)

        elif isinstance(node, GroupStmt):
            tdef = _require_template(templates, node.make_template)
            # Evaluate source → must resolve to a list in vars_env
            source_list = _resolve_source(node.source, vars_env)
            for i, item in enumerate(source_list):
                loop_env = {node.var: item}
                # agent name: GroupName_i or derived from item.name
                if isinstance(item, dict) and "name" in item:
                    safe = re.sub(r'\W+', '_', str(item["name"]))
                    aname = f"{node.group_name}_{safe}"
                else:
                    aname = f"{node.group_name}_{i}"
                bindings = _resolve_make_args(node.make_args_template, vars_env, loop_env)
                agent = _expand(tdef, bindings, aname)
                result.append(agent)

        else:
            result.append(node)

    return result


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _require_template(templates: dict, name: str) -> TemplateDef:
    if name not in templates:
        raise KeyError(f"Template {name!r} not defined (MAKE before TEMPLATE?)")
    return templates[name]


def _resolve_make_args(
    args: dict[str, Any],
    vars_env: dict[str, Any],
    loop_env: dict[str, Any],
) -> dict[str, Any]:
    """Resolve ExprRef values in MAKE args against vars + loop env."""
    combined = {**vars_env, **loop_env}
    resolved: dict[str, Any] = {}
    for k, v in args.items():
        if isinstance(v, ExprRef):
            try:
                resolved[k] = ev.evaluate(v.expr, combined)
            except ev.EvidenceError:
                # Keep as string if evaluation fails (runtime will handle it)
                resolved[k] = v.expr
        else:
            resolved[k] = v
    return resolved


def _resolve_source(source: str, vars_env: dict[str, Any]) -> list:
    """Resolve the FOR ... IN <source> value."""
    # Direct lookup in vars
    if source in vars_env:
        val = vars_env[source]
        if isinstance(val, list):
            return val
        raise ValueError(f"GROUP source {source!r} must be a list, got {type(val).__name__}")
    # Try evaluating as an expression
    try:
        val = ev.evaluate(source, vars_env)
        if isinstance(val, list):
            return val
    except ev.EvidenceError:
        pass
    raise ValueError(
        f"GROUP source {source!r} not found in vars and cannot be evaluated"
    )


def _expand(tdef: TemplateDef, bindings: dict[str, Any], agent_name: str) -> AgentDef:
    """Instantiate a TemplateDef with concrete bindings → AgentDef."""
    proto = tdef.body

    new_steps: list[StepDef] = []
    for step in proto.steps:
        new_args: dict[str, Any] = {}
        for k, v in step.args.items():
            if isinstance(v, ExprRef):
                # Substitute if it's a simple template param reference
                if v.expr in bindings:
                    new_args[k] = bindings[v.expr]
                else:
                    # Keep as ExprRef – resolved at runtime from step outputs
                    new_args[k] = v
            else:
                new_args[k] = v

        new_check = _subst_expr(step.check, bindings)
        new_steps.append(StepDef(
            name=step.name,
            kind=step.kind,
            target=step.target,
            args=new_args,
            check=new_check,
            onfail=step.onfail,
        ))

    new_must = _subst_expr(proto.must_expr, bindings)
    new_result = _subst_expr(proto.result_expr, bindings)

    return AgentDef(
        name=agent_name,
        inputs=list(proto.inputs),
        outputs=list(proto.outputs),
        tools=list(proto.tools),
        must_expr=new_must,
        steps=new_steps,
        result_expr=new_result,
        from_template=tdef.name,
        template_args=bindings,
    )


def _subst_expr(expr: str | None, bindings: dict[str, Any]) -> str | None:
    """
    Substitute template param names as whole-word tokens in an expression
    string.  e.g. if bindings = {"url": "https://x.com"}, then
    "page.url == url" → 'page.url == "https://x.com"'.
    """
    if expr is None:
        return None
    for param, val in bindings.items():
        # Only substitute bare names (word boundaries), not substring matches
        expr = re.sub(
            r'\b' + re.escape(param) + r'\b',
            repr(val),
            expr,
        )
    return expr


# ---------------------------------------------------------------------------
# Default tool registry (built-in tools)
# ---------------------------------------------------------------------------

def build_default_tool_registry() -> ToolRegistry:
    from .tools.http_tools import http_get
    from .tools.extract_tools import extract_table
    from .tools.llm_tools import llm_generate
    from .tools.sql_tools import sql_query
    from .tools.pdf_tools import pdf_render
    from .tools.email_tools import email_draft

    reg = ToolRegistry()
    reg.register("http.get",       http_get,      version="1")
    reg.register("extract.table",  extract_table, version="1")
    reg.register("llm.generate",   llm_generate,  version="1")
    reg.register("sql.query",      sql_query,     version="1")
    reg.register("pdf.render",     pdf_render,    version="1")
    reg.register("email.draft",    email_draft,   version="1")
    return reg
