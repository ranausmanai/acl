"""Tests for the ECL file-based cache."""

import os
import json
import tempfile

import pytest

from ecl.cache import Cache


@pytest.fixture
def cache(tmp_path):
    return Cache(str(tmp_path / "cache"))


# ---------------------------------------------------------------------------
# Key computation
# ---------------------------------------------------------------------------

def test_key_deterministic(cache):
    k1 = cache.make_key("http.get", {"url": "https://x.com"})
    k2 = cache.make_key("http.get", {"url": "https://x.com"})
    assert k1 == k2


def test_key_different_tool(cache):
    k1 = cache.make_key("http.get", {"url": "https://x.com"})
    k2 = cache.make_key("http.post", {"url": "https://x.com"})
    assert k1 != k2


def test_key_different_args(cache):
    k1 = cache.make_key("http.get", {"url": "https://a.com"})
    k2 = cache.make_key("http.get", {"url": "https://b.com"})
    assert k1 != k2


def test_key_arg_order_independent(cache):
    k1 = cache.make_key("t", {"a": 1, "b": 2})
    k2 = cache.make_key("t", {"b": 2, "a": 1})
    assert k1 == k2


def test_key_version_matters(cache):
    k1 = cache.make_key("http.get", {"url": "x"}, version="1")
    k2 = cache.make_key("http.get", {"url": "x"}, version="2")
    assert k1 != k2


# ---------------------------------------------------------------------------
# Miss
# ---------------------------------------------------------------------------

def test_miss_returns_false_none(cache):
    hit, val = cache.get("nonexistent_key_abc123")
    assert hit is False
    assert val is None


# ---------------------------------------------------------------------------
# Set and get
# ---------------------------------------------------------------------------

def test_set_then_get(cache):
    key = cache.make_key("http.get", {"url": "https://x.com"})
    value = {"status": 200, "text": "hello"}
    cache.set(key, value)
    hit, val = cache.get(key)
    assert hit is True
    assert val == value


def test_set_overwrites(cache):
    key = cache.make_key("t", {"x": 1})
    cache.set(key, "first")
    cache.set(key, "second")
    _, val = cache.get(key)
    assert val == "second"


def test_set_complex_value(cache):
    key = cache.make_key("sql.query", {"query": "SELECT 1"})
    value = {"rows": [{"a": 1, "b": "hello"}, {"a": 2, "b": "world"}], "row_count": 2}
    cache.set(key, value)
    hit, val = cache.get(key)
    assert hit is True
    assert val["row_count"] == 2


# ---------------------------------------------------------------------------
# Invalidate
# ---------------------------------------------------------------------------

def test_invalidate_existing(cache):
    key = cache.make_key("t", {"x": 1})
    cache.set(key, 42)
    removed = cache.invalidate(key)
    assert removed is True
    hit, _ = cache.get(key)
    assert hit is False


def test_invalidate_missing(cache):
    removed = cache.invalidate("not_there")
    assert removed is False


# ---------------------------------------------------------------------------
# Clear
# ---------------------------------------------------------------------------

def test_clear(cache):
    for i in range(5):
        key = cache.make_key("t", {"i": i})
        cache.set(key, i)
    removed = cache.clear()
    assert removed == 5


# ---------------------------------------------------------------------------
# File creation
# ---------------------------------------------------------------------------

def test_cache_dir_created(tmp_path):
    cache_path = str(tmp_path / "new_dir" / "cache")
    c = Cache(cache_path)
    assert os.path.isdir(cache_path)


def test_cache_file_is_valid_json(cache):
    key = cache.make_key("t", {"x": 99})
    cache.set(key, {"result": [1, 2, 3]})
    path = cache._path(key)
    with open(path) as fh:
        data = json.load(fh)
    assert data == {"result": [1, 2, 3]}
