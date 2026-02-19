"""
ACL Tool SDK for Python.

Write an ACL-compatible external tool in a few lines:

    from acl_tool import ToolServer

    server = ToolServer()

    @server.handle("my.greet")
    def greet(args: dict) -> dict:
        name = args.get("name", "world")
        return {"greeting": f"Hello, {name}!"}

    if __name__ == "__main__":
        server.run()

The process reads newline-delimited JSON from stdin and writes responses to
stdout.  Stderr is available for debug logging (ACL ignores subprocess stderr).
"""

from __future__ import annotations

import json
import sys
from typing import Callable, Any


Handler = Callable[[dict[str, Any]], Any]


class ToolServer:
    """Dispatches ACL tool protocol requests to registered Python handlers."""

    def __init__(self) -> None:
        self._handlers: dict[str, Handler] = {}

    def handle(self, name: str) -> Callable[[Handler], Handler]:
        """Decorator that registers *fn* as the handler for tool *name*."""
        def decorator(fn: Handler) -> Handler:
            self._handlers[name] = fn
            return fn
        return decorator

    def register(self, name: str, fn: Handler) -> None:
        """Register *fn* as the handler for tool *name* (non-decorator form)."""
        self._handlers[name] = fn

    def run(self, *, stdin=None, stdout=None) -> None:
        """Start the request/response loop (blocks until stdin closes)."""
        _in = stdin or sys.stdin
        _out = stdout or sys.stdout

        for line in _in:
            line = line.strip()
            if not line:
                continue
            try:
                req = json.loads(line)
            except json.JSONDecodeError as exc:
                resp = {"ok": False, "error": f"invalid JSON: {exc}"}
                _out.write(json.dumps(resp) + "\n")
                _out.flush()
                continue

            resp = self._dispatch(req)
            _out.write(json.dumps(resp) + "\n")
            _out.flush()

    def _dispatch(self, req: dict) -> dict:
        req_id = req.get("id", "")
        tool_name = req.get("tool", "")
        args = req.get("args") or {}

        handler = self._handlers.get(tool_name)
        if handler is None:
            return {"id": req_id, "ok": False, "error": f"unknown tool {tool_name!r}"}

        try:
            result = handler(args)
            return {"id": req_id, "ok": True, "result": result}
        except Exception as exc:  # noqa: BLE001
            return {"id": req_id, "ok": False, "error": str(exc)}


# ── Convenience entry-point helpers ───────────────────────────────────────────

def run_tools(handlers: dict[str, Handler]) -> None:
    """Register *handlers* and run the server.  Convenience one-liner."""
    server = ToolServer()
    for name, fn in handlers.items():
        server.register(name, fn)
    server.run()


# ── Example / self-test ───────────────────────────────────────────────────────

if __name__ == "__main__":
    """
    Demo: run with
        echo '{"id":"1","tool":"demo.echo","args":{"msg":"hello"},"version":"1"}' | python acl_tool.py
    """
    server = ToolServer()

    @server.handle("demo.echo")
    def echo(args: dict) -> dict:
        return {"echoed": args.get("msg", ""), "args": args}

    @server.handle("demo.add")
    def add(args: dict) -> dict:
        a = float(args.get("a", 0))
        b = float(args.get("b", 0))
        return {"result": a + b}

    server.run()
