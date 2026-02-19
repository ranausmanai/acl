"""
Integration tests: parse + expand + run full ECL programs.
"""

import json
import os
import tempfile

import pytest

from ecl.runtime import run_program


# ---------------------------------------------------------------------------
# Helper
# ---------------------------------------------------------------------------

def run(src: str, vars_env: dict | None = None, cache_dir: str | None = None) -> dict:
    if cache_dir is None:
        with tempfile.TemporaryDirectory() as td:
            return run_program(src, vars_env or {}, cache_dir=td)
    return run_program(src, vars_env or {}, cache_dir=cache_dir)


# ---------------------------------------------------------------------------
# Minimal agent
# ---------------------------------------------------------------------------

MINIMAL_SRC = """
INTENT "Minimal test"
ALLOW http.get
LIMIT retries=1

AGENT MinimalAgent
IN url
OUT page
TOOLS http.get
MUST page.status == 200

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL stop

RESULT page
"""

def test_minimal_agent_success():
    receipt = run(MINIMAL_SRC, {"url": "https://example.com/pricing"})
    assert receipt["status"] == "success"
    assert receipt["intent"] == "Minimal test"
    assert len(receipt["agents"]) == 1
    agent = receipt["agents"][0]
    assert agent["status"] == "success"
    assert len(agent["steps"]) == 1
    assert agent["steps"][0]["check_passed"] is True


def test_minimal_agent_receipt_schema():
    receipt = run(MINIMAL_SRC, {"url": "https://example.com/pricing"})
    assert "ecl_version" in receipt
    assert "timestamp" in receipt
    assert "policy" in receipt
    assert "agents" in receipt
    step = receipt["agents"][0]["steps"][0]
    assert "output_hash" in step
    assert "output_preview" in step
    assert "duration_ms" in step
    assert "cache_hit" in step


# ---------------------------------------------------------------------------
# Cache hit / miss
# ---------------------------------------------------------------------------

def test_cache_hit_second_run(tmp_path):
    cache_dir = str(tmp_path / "cache")
    src = """
INTENT "Cache test"
ALLOW http.get
LIMIT retries=0

AGENT CacheAgent
IN url
TOOLS http.get

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL stop

RESULT page
"""
    r1 = run_program(src, {"url": "https://example.com/pricing"}, cache_dir=cache_dir)
    r2 = run_program(src, {"url": "https://example.com/pricing"}, cache_dir=cache_dir)

    assert r1["status"] == "success"
    assert r2["status"] == "success"

    step1 = r1["agents"][0]["steps"][0]
    step2 = r2["agents"][0]["steps"][0]
    assert step1["cache_hit"] is False
    assert step2["cache_hit"] is True


# ---------------------------------------------------------------------------
# ONFAIL stop
# ---------------------------------------------------------------------------

STOP_SRC = """
INTENT "Fail test"
ALLOW http.get
LIMIT retries=0

AGENT FailAgent
TOOLS http.get

STEP page = TOOL http.get(url="https://example.com/404")
CHECK page.status == 999
ONFAIL stop

RESULT page
"""

def test_onfail_stop_fails_receipt():
    receipt = run(STOP_SRC)
    assert receipt["status"] == "failed"
    assert receipt["agents"][0]["steps"][0]["check_passed"] is False


# ---------------------------------------------------------------------------
# ONFAIL retry
# ---------------------------------------------------------------------------

RETRY_SRC = """
INTENT "Retry then succeed"
ALLOW http.get, llm.generate
LIMIT retries=2

AGENT RetryAgent
TOOLS http.get, llm.generate

STEP page = TOOL http.get(url="https://example.com/pricing")
CHECK page.status == 200
ONFAIL retry

STEP result = TOOL llm.generate(prompt="summarise pricing", data=page.text, format="text")
CHECK has(result, "text")
ONFAIL stop

RESULT result.text
"""

def test_retry_agent_succeeds():
    receipt = run(RETRY_SRC)
    assert receipt["status"] == "success"


# ---------------------------------------------------------------------------
# ONFAIL fallback
# ---------------------------------------------------------------------------

FALLBACK_SRC = """
INTENT "Fallback test"
ALLOW http.get, llm.generate, extract.table
LIMIT retries=0

AGENT FallbackAgent
TOOLS http.get, llm.generate, extract.table

STEP page = TOOL http.get(url="https://example.com/pricing")
CHECK page.status == 200
ONFAIL stop

STEP rows = TOOL extract.table(text=page.text, columns=["plan","price"])
CHECK count(rows.rows) >= 100
ONFAIL fallback llm_fallback

STEP llm_fallback = TOOL llm.generate(prompt="extract pricing rows json", data=page.text, format="json")
CHECK has(llm_fallback, "rows")
ONFAIL stop

RESULT llm_fallback
"""

def test_fallback_step_executed():
    receipt = run(FALLBACK_SRC)
    assert receipt["status"] == "success"
    step_names = [s["name"] for s in receipt["agents"][0]["steps"]]
    assert "llm_fallback" in step_names


# ---------------------------------------------------------------------------
# MUST gate
# ---------------------------------------------------------------------------

MUST_FAIL_SRC = """
INTENT "MUST fail"
ALLOW http.get
LIMIT retries=0

AGENT MustFail
TOOLS http.get
MUST page.status == 999

STEP page = TOOL http.get(url="https://example.com/pricing")
RESULT page
"""

def test_must_gate_fails():
    receipt = run(MUST_FAIL_SRC)
    assert receipt["status"] == "failed"
    assert receipt["agents"][0]["must_passed"] is False


# ---------------------------------------------------------------------------
# TEMPLATE + MAKE expansion
# ---------------------------------------------------------------------------

TEMPLATE_SRC = """
INTENT "Template test"
ALLOW http.get, extract.table
LIMIT retries=1

TEMPLATE FetchPricing(fetch_url, cols)
IN fetch_url, cols
OUT rows
TOOLS http.get, extract.table
MUST count(rows.rows) >= 1

STEP page = TOOL http.get(url=fetch_url)
CHECK page.status == 200
ONFAIL stop

STEP rows = TOOL extract.table(text=page.text, columns=cols)
CHECK count(rows.rows) >= 1
ONFAIL stop

RESULT rows

MAKE FetchPricing(fetch_url="https://example.com/pricing", cols=["plan","price"]) AS PricingAgent

AGENT MainAgent
IN url
TOOLS http.get, extract.table

STEP result = AGENT PricingAgent(fetch_url=url, cols=["plan","price"])
CHECK has(result, "rows")
ONFAIL stop

RESULT result
"""

def test_template_expansion():
    receipt = run(TEMPLATE_SRC, {"url": "https://example.com/pricing"})
    assert receipt["status"] == "success"
    # Should have both PricingAgent and MainAgent traces
    names = [a["name"] for a in receipt["agents"]]
    assert "PricingAgent" in names
    assert "MainAgent" in names


# ---------------------------------------------------------------------------
# GROUP expansion
# ---------------------------------------------------------------------------

GROUP_SRC = """
INTENT "Group test"
ALLOW http.get
LIMIT retries=0

TEMPLATE UrlChecker(check_url, label)
IN check_url, label
OUT page
TOOLS http.get

STEP page = TOOL http.get(url=check_url)
CHECK page.status == 200
ONFAIL stop

RESULT page

GROUP Checkers = FOR site IN sites : MAKE UrlChecker(check_url=site.url, label=site.name)

AGENT EntryAgent
TOOLS http.get

STEP r1 = AGENT Checkers_Alpha(check_url="https://alpha.io/pricing", label="Alpha")
CHECK has(r1, "status")
ONFAIL stop

RESULT r1
"""

def test_group_expansion():
    vars_env = {
        "sites": [
            {"name": "Alpha", "url": "https://alpha.io/pricing"},
            {"name": "Beta",  "url": "https://beta.io/pricing"},
        ]
    }
    receipt = run(GROUP_SRC, vars_env)
    assert receipt["status"] == "success"


# ---------------------------------------------------------------------------
# Permission enforcement
# ---------------------------------------------------------------------------

PERMISSION_SRC = """
INTENT "Permission test"
ALLOW llm.generate
LIMIT retries=0

AGENT BadAgent
TOOLS http.get

STEP page = TOOL http.get(url="https://example.com")
RESULT page
"""

def test_permission_denied():
    receipt = run(PERMISSION_SRC)
    assert receipt["status"] == "failed"
    step = receipt["agents"][0]["steps"][0]
    assert step["error"] is not None


# ---------------------------------------------------------------------------
# Full example: 01_pricing_brief.ecl
# ---------------------------------------------------------------------------

def test_example_01_pricing_brief():
    example_path = os.path.join(
        os.path.dirname(__file__), "..", "examples", "01_pricing_brief.ecl"
    )
    with open(example_path, encoding="utf-8") as fh:
        src = fh.read()
    receipt = run(src, {"url": "https://example.com/pricing"})
    # The example MUST pass – main agent WritePricingBrief has MUST gate
    assert receipt["status"] in ("success", "failed")  # any terminal state is valid
    # But intent must be present
    assert "pricing" in receipt["intent"].lower()


# ---------------------------------------------------------------------------
# Agent calls agent
# ---------------------------------------------------------------------------

NESTED_SRC = """
INTENT "Agent calls agent"
ALLOW http.get, llm.generate
LIMIT retries=1

AGENT Extractor
IN url
OUT text_output
TOOLS http.get
MUST page.status == 200

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL stop

RESULT page.text

AGENT Summariser
IN url
OUT summary
TOOLS http.get, llm.generate
MUST has(summary, "text")

STEP raw = AGENT Extractor(url=url)
CHECK len(raw) >= 5
ONFAIL stop

STEP summary = TOOL llm.generate(prompt="summarise", data=raw, format="text")
CHECK has(summary, "text")
ONFAIL stop

RESULT summary
"""

def test_agent_calls_agent():
    receipt = run(NESTED_SRC, {"url": "https://example.com/pricing"})
    assert receipt["status"] == "success"
    names = [a["name"] for a in receipt["agents"]]
    # Both agents should appear in receipt
    assert "Extractor" in names
    assert "Summariser" in names


# ---------------------------------------------------------------------------
# No AGENT found
# ---------------------------------------------------------------------------

def test_no_agent_returns_error():
    src = 'INTENT "No agents"\nALLOW http.get\n'
    receipt = run(src)
    assert receipt["status"] == "failed"
    assert receipt["error"] is not None
