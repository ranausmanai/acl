"""
ECL file-based deterministic cache.

Cache key = SHA-256( json(tool_name, normalized_args, tool_version) )
Cache files live in  .ecl_cache/<key>.json

A cache miss returns (False, None).
A cache hit  returns (True, value)  and marks the receipt entry.
"""

from __future__ import annotations

import hashlib
import json
import os
from typing import Any


_CACHE_DIR = ".ecl_cache"


class Cache:
    def __init__(self, cache_dir: str = _CACHE_DIR) -> None:
        self.cache_dir = cache_dir
        os.makedirs(cache_dir, exist_ok=True)

    # ------------------------------------------------------------------
    # Key computation
    # ------------------------------------------------------------------

    def make_key(
        self,
        tool_name: str,
        args: dict[str, Any],
        tool_version: str = "1",
        *,
        version: str | None = None,      # alias for tool_version
    ) -> str:
        if version is not None:
            tool_version = version
        """Return the SHA-256 hex digest for this (tool, args, version) triple."""
        payload = {
            "tool": tool_name,
            "args": _normalize(args),
            "version": tool_version,
        }
        raw = json.dumps(payload, sort_keys=True, ensure_ascii=True)
        return hashlib.sha256(raw.encode()).hexdigest()

    # ------------------------------------------------------------------
    # Read / write
    # ------------------------------------------------------------------

    def get(self, key: str) -> tuple[bool, Any]:
        """Return (hit, value).  Value is None on miss."""
        path = self._path(key)
        if os.path.isfile(path):
            try:
                with open(path, encoding="utf-8") as fh:
                    return True, json.load(fh)
            except (json.JSONDecodeError, OSError):
                return False, None
        return False, None

    def set(self, key: str, value: Any) -> None:
        """Persist *value* under *key*.  Silently skips non-serialisable values."""
        try:
            serialised = json.dumps(value, ensure_ascii=False, default=str)
        except (TypeError, ValueError):
            return
        with open(self._path(key), "w", encoding="utf-8") as fh:
            fh.write(serialised)

    def invalidate(self, key: str) -> bool:
        path = self._path(key)
        if os.path.isfile(path):
            os.remove(path)
            return True
        return False

    def clear(self) -> int:
        removed = 0
        for fname in os.listdir(self.cache_dir):
            if fname.endswith(".json"):
                os.remove(os.path.join(self.cache_dir, fname))
                removed += 1
        return removed

    # ------------------------------------------------------------------
    # Private
    # ------------------------------------------------------------------

    def _path(self, key: str) -> str:
        return os.path.join(self.cache_dir, f"{key}.json")


# ---------------------------------------------------------------------------
# Normalisation helpers
# ---------------------------------------------------------------------------

def _normalize(obj: Any) -> Any:
    """
    Recursively produce a JSON-serialisable, deterministically-ordered
    representation of *obj* suitable for hashing.
    """
    if obj is None or isinstance(obj, (bool, int, float, str)):
        return obj
    if isinstance(obj, (list, tuple)):
        return [_normalize(v) for v in obj]
    if isinstance(obj, dict):
        return {str(k): _normalize(v) for k, v in sorted(obj.items())}
    # Fallback: stringify
    return str(obj)
