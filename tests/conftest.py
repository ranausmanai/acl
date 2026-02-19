"""Shared pytest fixtures."""

import pytest


SIMPLE_AGENT_SRC = """
INTENT "Test intent"
ALLOW http.get, llm.generate
LIMIT time=30s calls=10 retries=1

AGENT SimpleAgent
IN url
OUT result
TOOLS http.get, llm.generate
MUST has(result, "text")

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL stop

STEP result = TOOL llm.generate(prompt="summarise", data=page.text, format="text")
CHECK has(result, "text")
ONFAIL stop

RESULT result.text
"""


TEMPLATE_SRC = """
INTENT "Template test"
ALLOW http.get, extract.table
LIMIT retries=1

TEMPLATE FetchAgent(fetch_url, cols)
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

MAKE FetchAgent(fetch_url="https://example.com/pricing", cols=["plan","price"]) AS PricingFetcher

AGENT EntryAgent
IN url
OUT rows
TOOLS http.get, extract.table

STEP rows = AGENT PricingFetcher(fetch_url=url, cols=["plan","price"])
CHECK has(rows, "rows")
ONFAIL stop

RESULT rows
"""
