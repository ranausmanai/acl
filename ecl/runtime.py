"""
ECL Runtime / Evaluator.

Execution model
---------------
1.  Parse the source file into AST nodes.
2.  Expand templates/groups → concrete AgentDef list.
3.  Register all AgentDef instances in the AgentRegistry.
4.  Run the *entry agent* (first AgentDef) with inputs from vars.
5.  Each STEP is executed in declaration order:
      a. Resolve args (replace ExprRef with values from local env).
      b. Check permission allowlist.
      c. Compute cache key; return cached result if hit.
      d. Invoke tool (or recursively invoke agent).
      e. Store output in local env.
      f. Evaluate CHECK expression → bool.
      g. If check fails, apply ONFAIL policy:
           retry    → re-run step up to LIMIT.retries times
           fallback → jump to named fallback step (must exist in agent)
           askhuman → halt with status=needs_human
           stop     → halt with status=failed
6.  After all steps, evaluate MUST expression (agent-level evidence gate).
7.  Evaluate RESULT expression → final output value.
8.  Emit receipt JSON.

Determinism guarantees
----------------------
* Steps execute in declaration order.
* Retries are bounded by LIMIT.retries (default 2).
* Fallback targets are resolved by name from the same agent's step list.
* Cache keyed by SHA-256(tool + normalised_args + version) – identical
  inputs always hit cache after the first successful run.
* No global mutable state between agent invocations.
"""

from __future__ import annotations

import time
from typing import Any

from .models import AgentDef, ExprRef, LimitNode, OnFail, StepDef
from .registry import AgentRegistry, ToolRegistry
from .cache import Cache
from .receipt import AgentTrace, ReceiptBuilder, StepTrace
from . import evidence as ev


class ExecutionError(Exception):
    pass


class PermissionError(ExecutionError):  # noqa: A001
    pass


# ---------------------------------------------------------------------------
# Runtime context (shared across all agents in one run)
# ---------------------------------------------------------------------------

class RuntimeContext:
    def __init__(
        self,
        tool_registry: ToolRegistry,
        agent_registry: AgentRegistry,
        allow: list[str],
        limit: LimitNode,
        cache: Cache,
        receipt: ReceiptBuilder,
        vars_env: dict[str, Any],
    ) -> None:
        self.tool_registry = tool_registry
        self.agent_registry = agent_registry
        self.allow = allow
        self.limit = limit
        self.cache = cache
        self.receipt = receipt
        self.vars_env = vars_env
        # Counters
        self.total_calls: int = 0
        self._start_time: float = time.monotonic()

    def check_time_budget(self) -> None:
        if self.limit.time_s is not None:
            elapsed = time.monotonic() - self._start_time
            if elapsed > self.limit.time_s:
                raise ExecutionError(
                    f"Time budget exceeded: {elapsed:.1f}s > {self.limit.time_s}s"
                )

    def check_call_budget(self) -> None:
        if self.limit.calls is not None and self.total_calls >= self.limit.calls:
            raise ExecutionError(
                f"Call budget exceeded: {self.total_calls} >= {self.limit.calls}"
            )

    def assert_allowed(self, tool_name: str) -> None:
        if self.allow and tool_name not in self.allow:
            raise PermissionError(
                f"Tool {tool_name!r} not in allowlist: {self.allow}"
            )


# ---------------------------------------------------------------------------
# Agent runner
# ---------------------------------------------------------------------------

def run_agent(
    agent: AgentDef,
    inputs: dict[str, Any],
    ctx: RuntimeContext,
    depth: int = 0,
) -> tuple[str, Any]:
    """
    Execute *agent* with *inputs*.

    Returns (status, result_value) where status ∈ {success, failed, needs_human}.
    """
    if depth > 10:
        raise ExecutionError("Agent call depth limit exceeded (>10)")

    # Build the agent trace for the receipt
    atrace = AgentTrace(agent.name)
    atrace.from_template = agent.from_template
    atrace.template_args = agent.template_args
    atrace.inputs = agent.inputs
    atrace.outputs = agent.outputs
    atrace.tools = agent.tools
    atrace.must_expr = agent.must_expr
    atrace.status = "in_progress"
    ctx.receipt.agent_traces.append(atrace)

    # Local execution environment: vars + inputs
    local_env: dict[str, Any] = {**ctx.vars_env, **inputs}

    # Index steps by name for fallback resolution
    step_by_name: dict[str, StepDef] = {s.name: s for s in agent.steps}

    # Steps that are ONLY reachable via ONFAIL fallback are excluded from the
    # main execution sequence.  They run only when explicitly jumped to.
    fallback_targets: set[str] = {
        s.onfail.fallback_step
        for s in agent.steps
        if s.onfail and s.onfail.policy == "fallback" and s.onfail.fallback_step
    }
    steps_list = [s for s in agent.steps if s.name not in fallback_targets]

    i = 0
    while i < len(steps_list):
        step = steps_list[i]
        ctx.check_time_budget()

        status, local_env = _run_step_with_policy(
            step=step,
            local_env=local_env,
            ctx=ctx,
            atrace=atrace,
            step_by_name=step_by_name,
            depth=depth,
        )

        if status == "needs_human":
            atrace.status = "needs_human"
            return "needs_human", None

        if status == "failed":
            atrace.status = "failed"
            return "failed", None

        if status == "fallback":
            # The fallback step name is stored as a sentinel in local_env
            fallback_name = local_env.pop("__fallback__")
            if fallback_name not in step_by_name:
                atrace.status = "failed"
                return "failed", None
            # Insert fallback step right after the current position.
            # Remove any existing occurrence first to prevent double execution.
            fallback_step_def = step_by_name[fallback_name]
            steps_list = [s for s in steps_list if s.name != fallback_name]
            steps_list.insert(i + 1, fallback_step_def)
            # Continue: the loop will advance i and execute the fallback next.

        i += 1

    # ---- MUST check (agent-level evidence gate) ----
    if agent.must_expr:
        try:
            must_ok = ev.check(agent.must_expr, local_env)
        except ev.EvidenceError as exc:
            must_ok = False
            atrace.error = f"MUST eval error: {exc}"
        atrace.must_passed = must_ok
        if not must_ok:
            atrace.status = "failed"
            atrace.error = atrace.error or f"MUST failed: {agent.must_expr}"
            return "failed", None
    else:
        atrace.must_passed = None

    # ---- RESULT ----
    result: Any = None
    if agent.result_expr:
        try:
            result = ev.evaluate(agent.result_expr, local_env)
        except ev.EvidenceError as exc:
            atrace.status = "failed"
            atrace.error = f"RESULT eval error: {exc}"
            return "failed", None

    atrace.result = result
    atrace.status = "success"
    return "success", result


# ---------------------------------------------------------------------------
# Step execution with ONFAIL policy
# ---------------------------------------------------------------------------

def _run_step_with_policy(
    step: StepDef,
    local_env: dict[str, Any],
    ctx: RuntimeContext,
    atrace: AgentTrace,
    step_by_name: dict[str, StepDef],
    depth: int,
) -> tuple[str, dict[str, Any]]:
    """
    Execute *step* applying retries.  Returns (status, env).

    Status values:
      ok        – step succeeded (check passed or no check)
      failed    – unrecoverable failure
      needs_human – askhuman policy triggered
      fallback  – env["__fallback__"] holds the fallback step name
    """
    max_retries = ctx.limit.retries
    attempt = 0
    strace: StepTrace | None = None

    while attempt <= max_retries:
        strace = StepTrace(name=step.name, kind=step.kind, target=step.target)
        if attempt > 0:
            strace.retries_used = attempt

        # Execute
        try:
            output, cache_hit = _execute_step(step, local_env, ctx, strace, depth)
        except (ExecutionError, PermissionError, KeyError) as exc:
            strace.error = str(exc)
            strace.finish()
            atrace.steps.append(strace)

            policy = step.onfail
            if policy and policy.policy == "retry" and attempt < max_retries:
                attempt += 1
                continue
            if policy and policy.policy == "askhuman":
                return "needs_human", local_env
            return "failed", local_env

        # Store output in environment
        new_env = {**local_env, step.name: output}

        # CHECK
        check_ok: bool | None = None
        if step.check:
            strace.check_expr = step.check
            try:
                check_ok = ev.check(step.check, new_env)
            except ev.EvidenceError as exc:
                check_ok = False
                strace.error = f"CHECK eval error: {exc}"
            strace.check_passed = check_ok
        strace.finish()
        atrace.steps.append(strace)

        if check_ok is False:
            policy = step.onfail
            if policy:
                strace.onfail_policy = policy.policy
                if policy.policy == "retry" and attempt < max_retries:
                    attempt += 1
                    continue
                if policy.policy == "fallback" and policy.fallback_step:
                    new_env["__fallback__"] = policy.fallback_step
                    return "fallback", new_env
                if policy.policy == "askhuman":
                    return "needs_human", new_env
                if policy.policy == "stop":
                    return "failed", new_env
            # No policy → fail closed
            return "failed", new_env

        return "ok", new_env

    # Exhausted retries
    if strace:
        strace.finish()
        if strace not in atrace.steps:
            atrace.steps.append(strace)
    return "failed", local_env


# ---------------------------------------------------------------------------
# Single step execution
# ---------------------------------------------------------------------------

def _execute_step(
    step: StepDef,
    env: dict[str, Any],
    ctx: RuntimeContext,
    strace: StepTrace,
    depth: int,
) -> tuple[Any, bool]:
    """
    Call the tool or agent for this step.  Returns (output, cache_hit).
    """
    resolved_args = _resolve_args(step.args, env)
    strace.args = {k: v for k, v in resolved_args.items() if not k.startswith("_pos_")}

    if step.kind == "tool":
        return _call_tool(step.target, resolved_args, ctx, strace)
    elif step.kind == "agent":
        return _call_agent(step.target, resolved_args, ctx, strace, depth)
    else:
        raise ExecutionError(f"Unknown step kind: {step.kind!r}")


def _resolve_args(args: dict[str, Any], env: dict[str, Any]) -> dict[str, Any]:
    """Resolve ExprRef values against the current environment."""
    resolved: dict[str, Any] = {}
    for k, v in args.items():
        if isinstance(v, ExprRef):
            try:
                resolved[k] = ev.evaluate(v.expr, env)
            except ev.EvidenceError as exc:
                raise ExecutionError(
                    f"Cannot resolve argument {k!r}={v.expr!r}: {exc}"
                ) from exc
        else:
            resolved[k] = v
    return resolved


def _call_tool(
    name: str,
    args: dict[str, Any],
    ctx: RuntimeContext,
    strace: StepTrace,
) -> tuple[Any, bool]:
    ctx.assert_allowed(name)
    ctx.check_call_budget()

    # Cache lookup
    cache_key = ctx.cache.make_key(name, args, ctx.tool_registry.version(name))
    hit, cached_val = ctx.cache.get(cache_key)
    if hit:
        strace.cache_hit = True
        strace.output = cached_val
        strace.tool_calls.append({"tool": name, "cache_hit": True, "duration_ms": 0})
        return cached_val, True

    # Invoke
    t0 = time.monotonic()
    fn = ctx.tool_registry.get(name)
    # Strip positional arg markers before calling
    clean_args = {k: v for k, v in args.items() if not k.startswith("_pos_")}
    output = fn(**clean_args)
    duration_ms = int((time.monotonic() - t0) * 1000)

    ctx.total_calls += 1
    strace.output = output
    strace.tool_calls.append({"tool": name, "cache_hit": False, "duration_ms": duration_ms})

    # Cache store
    ctx.cache.set(cache_key, output)

    return output, False


def _call_agent(
    name: str,
    args: dict[str, Any],
    ctx: RuntimeContext,
    strace: StepTrace,
    depth: int,
) -> tuple[Any, bool]:
    if not ctx.agent_registry.has(name):
        raise ExecutionError(f"Agent not found: {name!r}")
    sub_agent = ctx.agent_registry.get(name)
    clean_args = {k: v for k, v in args.items() if not k.startswith("_pos_")}
    status, result = run_agent(sub_agent, clean_args, ctx, depth=depth + 1)
    if status != "success":
        raise ExecutionError(f"Sub-agent {name!r} failed with status={status}")
    strace.output = result
    strace.tool_calls.append({"tool": f"agent:{name}", "cache_hit": False, "duration_ms": 0})
    return result, False


# ---------------------------------------------------------------------------
# Top-level runner
# ---------------------------------------------------------------------------

def run_program(
    source: str,
    vars_env: dict[str, Any] | None = None,
    cache_dir: str = ".ecl_cache",
    tool_registry: ToolRegistry | None = None,
) -> dict:
    """
    Parse and execute an ECL program.

    Returns the receipt dict.  Always emits a receipt even on failure.
    """
    from .parser import parse
    from .registry import AgentRegistry, build_default_tool_registry, expand_templates
    from .models import IntentNode, AllowNode, LimitNode

    vars_env = vars_env or {}
    tool_registry = tool_registry or build_default_tool_registry()

    receipt = ReceiptBuilder()
    receipt.status = "failed"  # default; overwritten on success

    try:
        nodes = parse(source)
        nodes = expand_templates(nodes, vars_env)
    except Exception as exc:
        receipt.error = f"Parse/expand error: {exc}"
        return receipt.build()

    # Collect directives and agents
    intent = ""
    allow: list[str] = []
    limit = LimitNode()
    agents: list[AgentDef] = []

    from .models import AgentDef as AD
    for node in nodes:
        if isinstance(node, IntentNode):
            intent = node.text
        elif isinstance(node, AllowNode):
            allow.extend(node.tools)
        elif isinstance(node, LimitNode):
            limit = node
        elif isinstance(node, AD):
            agents.append(node)

    receipt.intent = intent
    receipt.allow = allow
    receipt.limit = {
        "time_s": limit.time_s,
        "calls": limit.calls,
        "retries": limit.retries,
    }

    if not agents:
        receipt.error = "No AGENT definitions found in program."
        return receipt.build()

    # Build agent registry
    agent_registry = AgentRegistry()
    for agent in agents:
        agent_registry.register(agent)

    # Build runtime context
    cache = Cache(cache_dir)
    ctx = RuntimeContext(
        tool_registry=tool_registry,
        agent_registry=agent_registry,
        allow=allow,
        limit=limit,
        cache=cache,
        receipt=receipt,
        vars_env=vars_env,
    )

    # Entry agent = LAST agent (orchestrator pattern: sub-agents are defined
    # before the coordinator that calls them).
    entry = agents[-1]
    entry_inputs = {k: vars_env.get(k) for k in entry.inputs if k in vars_env}

    try:
        status, result = run_agent(entry, entry_inputs, ctx, depth=0)
    except ExecutionError as exc:
        receipt.status = "failed"
        receipt.error = str(exc)
        return receipt.build()

    receipt.status = status
    if status != "success":
        receipt.error = receipt.error or f"Agent returned status={status}"

    return receipt.build()
