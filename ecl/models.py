"""
ECL AST node definitions.

Every parsed construct maps to one of these dataclasses.  ExprRef marks a
value that must be evaluated against the runtime environment rather than used
literally.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional


# ---------------------------------------------------------------------------
# Primitive wrapper for runtime-evaluated expressions
# ---------------------------------------------------------------------------

@dataclass
class ExprRef:
    """
    An expression that is stored as a string and evaluated at runtime.

    Created when a tool-call argument is not a Python literal (e.g. the
    argument references a prior step output or a template parameter).
    """
    expr: str

    def __repr__(self) -> str:
        return f"<ExprRef:{self.expr}>"


# ---------------------------------------------------------------------------
# Top-level directives
# ---------------------------------------------------------------------------

@dataclass
class IntentNode:
    text: str


@dataclass
class AllowNode:
    tools: list[str]


@dataclass
class LimitNode:
    time_s: Optional[float] = None    # execution wall-clock budget (seconds)
    calls: Optional[int] = None       # max total tool invocations
    retries: int = 2                  # default retry count for ONFAIL retry


# ---------------------------------------------------------------------------
# Step-level nodes
# ---------------------------------------------------------------------------

@dataclass
class OnFail:
    """ONFAIL policy for a single step."""
    policy: str                       # retry | fallback | askhuman | stop
    fallback_step: Optional[str] = None


@dataclass
class StepDef:
    """A single STEP inside an AGENT definition."""
    name: str
    kind: str                         # "tool" | "agent"
    target: str                       # tool name or agent name
    args: dict[str, Any] = field(default_factory=dict)
    check: Optional[str] = None       # CHECK expression string
    onfail: Optional[OnFail] = None


# ---------------------------------------------------------------------------
# Agent definition
# ---------------------------------------------------------------------------

@dataclass
class AgentDef:
    """A concrete, fully-resolved agent definition."""
    name: str
    inputs: list[str] = field(default_factory=list)
    outputs: list[str] = field(default_factory=list)
    tools: list[str] = field(default_factory=list)
    must_expr: Optional[str] = None   # MUST expression (evaluated after all steps)
    steps: list[StepDef] = field(default_factory=list)
    result_expr: Optional[str] = None # RESULT expression
    # Provenance (set when expanded from a template)
    from_template: Optional[str] = None
    template_args: dict[str, Any] = field(default_factory=dict)


# ---------------------------------------------------------------------------
# Template, MAKE, GROUP
# ---------------------------------------------------------------------------

@dataclass
class TemplateDef:
    """
    A parameterised agent blueprint.

    TEMPLATE Name(p1, p2, ...)
      IN  ...
      OUT ...
      TOOLS ...
      MUST ...
      STEP ...
      RESULT ...
    """
    name: str
    params: list[str]
    body: AgentDef   # prototype – may contain ExprRef(param_name) in step args


@dataclass
class MakeStmt:
    """MAKE Template(args...) AS AgentName"""
    template: str
    args: dict[str, Any]   # param_name -> literal or ExprRef
    agent_name: str


@dataclass
class GroupStmt:
    """GROUP Name = FOR var IN source : MAKE Template(args...)"""
    group_name: str
    var: str               # loop variable name
    source: str            # expression evaluated against vars to get the list
    make_template: str
    make_args_template: dict[str, Any]   # may contain ExprRef("<var>.<attr>")
