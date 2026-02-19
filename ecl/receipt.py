"""
ECL Receipt generator.

A receipt is the authoritative trace of one program execution.  It is always
emitted, even when execution fails.

Receipt JSON schema
-------------------
{
  "ecl_version": "0.1.0",
  "timestamp": "<ISO-8601>",
  "status": "success" | "failed" | "needs_human",
  "intent": "<string>",
  "policy": {
    "allow": ["tool1", ...],
    "limit": { "time_s": float|null, "calls": int|null, "retries": int }
  },
  "agents": [
    {
      "name": "<string>",
      "from_template": "<string>|null",
      "template_args": {},
      "inputs": [...],
      "outputs": [...],
      "tools": [...],
      "must_expr": "<string>|null",
      "must_passed": true|false|null,
      "result": <any>,
      "status": "success"|"failed"|"needs_human"|"skipped",
      "steps": [
        {
          "name": "<string>",
          "kind": "tool"|"agent",
          "target": "<string>",
          "args": {},           # sensitive values redacted
          "output_hash": "<sha256>",
          "output_preview": "<first 200 chars of repr>",
          "cache_hit": true|false,
          "check_expr": "<string>|null",
          "check_passed": true|false|null,
          "onfail_policy": "<string>|null",
          "retries_used": 0,
          "started_at": "<ISO-8601>",
          "ended_at": "<ISO-8601>",
          "duration_ms": int,
          "tool_calls": [{"tool": str, "cache_hit": bool, "duration_ms": int}],
          "error": "<string>|null"
        }
      ]
    }
  ],
  "error": "<string>|null"
}
"""

from __future__ import annotations

import hashlib
import json
from datetime import datetime, timezone
from typing import Any


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _hash(obj: Any) -> str:
    try:
        raw = json.dumps(obj, default=str, sort_keys=True)
    except (TypeError, ValueError):
        raw = repr(obj)
    return hashlib.sha256(raw.encode()).hexdigest()[:16]


def _preview(obj: Any, max_len: int = 200) -> str:
    try:
        s = json.dumps(obj, default=str)
    except (TypeError, ValueError):
        s = repr(obj)
    return s[:max_len] + ("…" if len(s) > max_len else "")


# ---------------------------------------------------------------------------
# Step trace builder (mutable, used by runtime)
# ---------------------------------------------------------------------------

class StepTrace:
    def __init__(self, name: str, kind: str, target: str) -> None:
        self.name = name
        self.kind = kind
        self.target = target
        self.args: dict = {}
        self.output: Any = None
        self.cache_hit: bool = False
        self.check_expr: str | None = None
        self.check_passed: bool | None = None
        self.onfail_policy: str | None = None
        self.retries_used: int = 0
        self.started_at: str = _now()
        self.ended_at: str = ""
        self.duration_ms: int = 0
        self.tool_calls: list[dict] = []
        self.error: str | None = None

    def finish(self) -> None:
        self.ended_at = _now()
        started = datetime.fromisoformat(self.started_at)
        ended = datetime.fromisoformat(self.ended_at)
        self.duration_ms = int((ended - started).total_seconds() * 1000)

    def to_dict(self, sensitive_keys: set[str] | None = None) -> dict:
        sensitive_keys = sensitive_keys or set()
        safe_args = {
            k: ("***REDACTED***" if k in sensitive_keys else v)
            for k, v in self.args.items()
        }
        return {
            "name": self.name,
            "kind": self.kind,
            "target": self.target,
            "args": safe_args,
            "output_hash": _hash(self.output),
            "output_preview": _preview(self.output),
            "cache_hit": self.cache_hit,
            "check_expr": self.check_expr,
            "check_passed": self.check_passed,
            "onfail_policy": self.onfail_policy,
            "retries_used": self.retries_used,
            "started_at": self.started_at,
            "ended_at": self.ended_at,
            "duration_ms": self.duration_ms,
            "tool_calls": self.tool_calls,
            "error": self.error,
        }


class AgentTrace:
    def __init__(self, agent_name: str) -> None:
        self.agent_name = agent_name
        self.from_template: str | None = None
        self.template_args: dict = {}
        self.inputs: list[str] = []
        self.outputs: list[str] = []
        self.tools: list[str] = []
        self.must_expr: str | None = None
        self.must_passed: bool | None = None
        self.result: Any = None
        self.status: str = "skipped"
        self.steps: list[StepTrace] = []
        self.error: str | None = None

    def to_dict(self) -> dict:
        return {
            "name": self.agent_name,
            "from_template": self.from_template,
            "template_args": self.template_args,
            "inputs": self.inputs,
            "outputs": self.outputs,
            "tools": self.tools,
            "must_expr": self.must_expr,
            "must_passed": self.must_passed,
            "result": self.result,
            "status": self.status,
            "error": self.error,
            "steps": [s.to_dict() for s in self.steps],
        }


# ---------------------------------------------------------------------------
# Receipt builder
# ---------------------------------------------------------------------------

class ReceiptBuilder:
    def __init__(self) -> None:
        self.intent: str = ""
        self.allow: list[str] = []
        self.limit: dict = {}
        self.agent_traces: list[AgentTrace] = []
        self.status: str = "success"
        self.error: str | None = None
        self._started_at: str = _now()

    def build(self) -> dict:
        return {
            "ecl_version": "0.1.0",
            "timestamp": self._started_at,
            "status": self.status,
            "intent": self.intent,
            "policy": {
                "allow": self.allow,
                "limit": self.limit,
            },
            "agents": [a.to_dict() for a in self.agent_traces],
            "error": self.error,
        }

    def write(self, path: str) -> None:
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(self.build(), fh, indent=2, default=str)
