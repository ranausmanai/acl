"""Tests for the ECL DSL parser."""

import pytest

from ecl.parser import parse, ParseError
from ecl.models import (
    AgentDef, AllowNode, ExprRef, GroupStmt, IntentNode,
    LimitNode, MakeStmt, OnFail, StepDef, TemplateDef,
)


# ---------------------------------------------------------------------------
# INTENT
# ---------------------------------------------------------------------------

def test_intent_basic():
    nodes = parse('INTENT "Hello world"')
    assert len(nodes) == 1
    assert isinstance(nodes[0], IntentNode)
    assert nodes[0].text == "Hello world"


def test_intent_missing_quotes_raises():
    with pytest.raises(ParseError):
        parse("INTENT no quotes here")


# ---------------------------------------------------------------------------
# ALLOW
# ---------------------------------------------------------------------------

def test_allow_single():
    nodes = parse("ALLOW http.get")
    assert isinstance(nodes[0], AllowNode)
    assert nodes[0].tools == ["http.get"]


def test_allow_multiple():
    nodes = parse("ALLOW http.get, extract.table, llm.generate")
    assert nodes[0].tools == ["http.get", "extract.table", "llm.generate"]


# ---------------------------------------------------------------------------
# LIMIT
# ---------------------------------------------------------------------------

def test_limit_all_fields():
    nodes = parse("LIMIT time=45s calls=20 retries=3")
    lim = nodes[0]
    assert isinstance(lim, LimitNode)
    assert lim.time_s == 45.0
    assert lim.calls == 20
    assert lim.retries == 3


def test_limit_minutes():
    nodes = parse("LIMIT time=2m")
    assert nodes[0].time_s == 120.0


def test_limit_partial():
    nodes = parse("LIMIT retries=5")
    lim = nodes[0]
    assert lim.retries == 5
    assert lim.calls is None
    assert lim.time_s is None


# ---------------------------------------------------------------------------
# AGENT + STEP
# ---------------------------------------------------------------------------

def test_agent_basic():
    src = """
AGENT MyAgent
IN url
OUT result
TOOLS http.get
MUST has(result, "text")

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL retry

RESULT page
"""
    nodes = parse(src)
    agent = next(n for n in nodes if isinstance(n, AgentDef))
    assert agent.name == "MyAgent"
    assert agent.inputs == ["url"]
    assert agent.outputs == ["result"]
    assert agent.tools == ["http.get"]
    assert agent.must_expr == 'has(result, "text")'
    assert len(agent.steps) == 1
    assert agent.result_expr == "page"


def test_step_tool_literal_args():
    src = """
AGENT A
STEP s = TOOL http.get(url="https://example.com")
CHECK s.status == 200
ONFAIL stop
"""
    nodes = parse(src)
    agent = nodes[0]
    step = agent.steps[0]
    assert step.name == "s"
    assert step.kind == "tool"
    assert step.target == "http.get"
    assert step.args["url"] == "https://example.com"
    assert step.check == "s.status == 200"
    assert step.onfail.policy == "stop"


def test_step_tool_exprref_args():
    src = """
AGENT A
IN url
STEP s = TOOL http.get(url=url)
"""
    nodes = parse(src)
    step = nodes[0].steps[0]
    assert isinstance(step.args["url"], ExprRef)
    assert step.args["url"].expr == "url"


def test_step_agent():
    src = """
AGENT Coordinator
STEP sub = AGENT SubAgent(x=myvar)
CHECK has(sub, "result")
ONFAIL askhuman
"""
    nodes = parse(src)
    step = nodes[0].steps[0]
    assert step.kind == "agent"
    assert step.target == "SubAgent"
    assert step.onfail.policy == "askhuman"


def test_onfail_fallback():
    src = """
AGENT A
STEP s = TOOL http.get(url="https://x.com")
CHECK s.status == 200
ONFAIL fallback my_fallback
"""
    nodes = parse(src)
    step = nodes[0].steps[0]
    assert step.onfail.policy == "fallback"
    assert step.onfail.fallback_step == "my_fallback"


def test_multiple_steps():
    src = """
AGENT A
STEP s1 = TOOL http.get(url="https://a.com")
STEP s2 = TOOL llm.generate(prompt="hello", format="text")
STEP s3 = TOOL extract.table(text=s1.text, columns=["plan","price"])
"""
    nodes = parse(src)
    assert len(nodes[0].steps) == 3
    assert nodes[0].steps[0].name == "s1"
    assert nodes[0].steps[2].name == "s3"
    # s3 text arg should be ExprRef
    assert isinstance(nodes[0].steps[2].args["text"], ExprRef)


# ---------------------------------------------------------------------------
# TEMPLATE
# ---------------------------------------------------------------------------

def test_template_basic():
    src = """
TEMPLATE MyT(p1, p2)
IN p1
OUT result
TOOLS http.get

STEP s = TOOL http.get(url=p1)
RESULT s
"""
    nodes = parse(src)
    tdef = next(n for n in nodes if isinstance(n, TemplateDef))
    assert tdef.name == "MyT"
    assert tdef.params == ["p1", "p2"]
    assert isinstance(tdef.body, AgentDef)
    assert len(tdef.body.steps) == 1
    assert isinstance(tdef.body.steps[0].args["url"], ExprRef)


# ---------------------------------------------------------------------------
# MAKE
# ---------------------------------------------------------------------------

def test_make_basic():
    src = 'MAKE MyTemplate(url="https://x.com", cols=["a","b"]) AS ConcreteAgent'
    nodes = parse(src)
    make = nodes[0]
    assert isinstance(make, MakeStmt)
    assert make.template == "MyTemplate"
    assert make.agent_name == "ConcreteAgent"
    assert make.args["url"] == "https://x.com"
    assert make.args["cols"] == ["a", "b"]


# ---------------------------------------------------------------------------
# GROUP
# ---------------------------------------------------------------------------

def test_group_basic():
    src = "GROUP AllAgents = FOR item IN items : MAKE MyTemplate(url=item.url)"
    nodes = parse(src)
    grp = nodes[0]
    assert isinstance(grp, GroupStmt)
    assert grp.group_name == "AllAgents"
    assert grp.var == "item"
    assert grp.source == "items"
    assert grp.make_template == "MyTemplate"
    assert isinstance(grp.make_args_template["url"], ExprRef)


# ---------------------------------------------------------------------------
# Comments and blank lines
# ---------------------------------------------------------------------------

def test_comments_skipped():
    src = """
# This is a comment
INTENT "hi"
# another comment
ALLOW http.get
"""
    nodes = parse(src)
    assert len(nodes) == 2
    assert isinstance(nodes[0], IntentNode)
    assert isinstance(nodes[1], AllowNode)


def test_blank_lines_skipped():
    src = "\n\nINTENT \"hello\"\n\n\nALLOW http.get\n\n"
    nodes = parse(src)
    assert len(nodes) == 2


# ---------------------------------------------------------------------------
# Multiple agents
# ---------------------------------------------------------------------------

def test_multiple_agents():
    src = """
AGENT AgentA
IN x
STEP s = TOOL http.get(url=x)

AGENT AgentB
IN y
STEP t = TOOL llm.generate(prompt=y, format="text")
"""
    nodes = parse(src)
    agents = [n for n in nodes if isinstance(n, AgentDef)]
    assert len(agents) == 2
    assert agents[0].name == "AgentA"
    assert agents[1].name == "AgentB"
