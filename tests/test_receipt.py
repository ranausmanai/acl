"""Tests for the ECL receipt builder."""

import json

import pytest

from ecl.receipt import AgentTrace, ReceiptBuilder, StepTrace


# ---------------------------------------------------------------------------
# StepTrace
# ---------------------------------------------------------------------------

def test_step_trace_basic():
    st = StepTrace("my_step", "tool", "http.get")
    st.output = {"status": 200, "text": "hello"}
    st.check_expr = "my_step.status == 200"
    st.check_passed = True
    st.finish()

    d = st.to_dict()
    assert d["name"] == "my_step"
    assert d["kind"] == "tool"
    assert d["target"] == "http.get"
    assert d["check_passed"] is True
    assert d["output_hash"]  # non-empty string
    assert "200" in d["output_preview"] or "hello" in d["output_preview"]
    assert d["duration_ms"] >= 0


def test_step_trace_redaction():
    st = StepTrace("s", "tool", "email.draft")
    st.args = {"to": "secret@co.com", "body": "plain text"}
    d = st.to_dict(sensitive_keys={"to"})
    assert d["args"]["to"] == "***REDACTED***"
    assert d["args"]["body"] == "plain text"


def test_step_trace_cache_hit():
    st = StepTrace("s", "tool", "http.get")
    st.cache_hit = True
    st.retries_used = 1
    st.finish()
    d = st.to_dict()
    assert d["cache_hit"] is True
    assert d["retries_used"] == 1


# ---------------------------------------------------------------------------
# AgentTrace
# ---------------------------------------------------------------------------

def test_agent_trace_dict():
    at = AgentTrace("MyAgent")
    at.inputs = ["url"]
    at.outputs = ["result"]
    at.status = "success"
    at.result = "draft_001"

    step = StepTrace("s", "tool", "http.get")
    step.finish()
    at.steps.append(step)

    d = at.to_dict()
    assert d["name"] == "MyAgent"
    assert d["status"] == "success"
    assert d["result"] == "draft_001"
    assert len(d["steps"]) == 1


# ---------------------------------------------------------------------------
# ReceiptBuilder
# ---------------------------------------------------------------------------

def test_receipt_builder_basic():
    rb = ReceiptBuilder()
    rb.intent = "Test run"
    rb.allow = ["http.get"]
    rb.limit = {"time_s": 30.0, "calls": 10, "retries": 2}
    rb.status = "success"

    at = AgentTrace("MyAgent")
    at.status = "success"
    rb.agent_traces.append(at)

    receipt = rb.build()
    assert receipt["ecl_version"] == "0.1.0"
    assert receipt["status"] == "success"
    assert receipt["intent"] == "Test run"
    assert receipt["policy"]["allow"] == ["http.get"]
    assert receipt["policy"]["limit"]["retries"] == 2
    assert len(receipt["agents"]) == 1


def test_receipt_json_serialisable():
    rb = ReceiptBuilder()
    rb.intent = "test"
    rb.status = "failed"
    rb.error = "Something went wrong"
    receipt = rb.build()
    # Must be JSON-serialisable
    serialised = json.dumps(receipt)
    parsed = json.loads(serialised)
    assert parsed["status"] == "failed"
    assert parsed["error"] == "Something went wrong"


def test_receipt_write(tmp_path):
    rb = ReceiptBuilder()
    rb.intent = "write test"
    rb.status = "success"
    path = str(tmp_path / "receipt.json")
    rb.write(path)

    with open(path) as fh:
        data = json.load(fh)
    assert data["ecl_version"] == "0.1.0"


def test_receipt_failed_status():
    rb = ReceiptBuilder()
    rb.status = "failed"
    rb.error = "MUST check failed"
    d = rb.build()
    assert d["status"] == "failed"
    assert d["error"] == "MUST check failed"


def test_receipt_needs_human():
    rb = ReceiptBuilder()
    rb.status = "needs_human"
    d = rb.build()
    assert d["status"] == "needs_human"
