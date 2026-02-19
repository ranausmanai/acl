# ============================================================
# Example 04 – Generate 50 agents from a data list
#
# Demonstrates GROUP / FOR / MAKE to instantiate a template
# once per row in a variables list.
#
# Run with:
#   ecl run examples/04_mass_agents.ecl --vars examples/04_vars.json
#
# The vars file supplies `competitors` – a list of
# {name, url, category} dicts.  One agent is created per entry.
#
# Only the entry agent (CategorySummary) is executed;
# the generated agents are registered and callable via AGENT steps.
# ============================================================

INTENT "Run competitive intelligence checks across all competitor URLs"
ALLOW http.get, extract.table, llm.generate, email.draft
LIMIT time=300s calls=200 retries=1

# ---------------------------------------------------------------
# Template: fetch + extract from a single competitor URL
# ---------------------------------------------------------------
TEMPLATE CompetitorCheck(name, url, category)
IN name, url, category
OUT summary
TOOLS http.get, extract.table, llm.generate
MUST has(summary, "text")

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL retry

STEP rows = TOOL extract.table(text=page.text, columns=["plan","price"])
CHECK count(rows.rows) >= 1
ONFAIL fallback llm_extract

STEP llm_extract = TOOL llm.generate(prompt="extract pricing rows json", data=page.text, format="json")
CHECK has(llm_extract, "rows")
ONFAIL stop

STEP summary = TOOL llm.generate(prompt="pricing brief", data=rows.rows, format="text")
CHECK has(summary, "text")
ONFAIL stop

RESULT summary

# ---------------------------------------------------------------
# Generate one agent per competitor (from vars: competitors list)
# ---------------------------------------------------------------
GROUP AllCompetitors = FOR item IN competitors : MAKE CompetitorCheck(name=item.name, url=item.url, category=item.category)

# ---------------------------------------------------------------
# Entry agent: runs first competitor as demo, summarises overall
# ---------------------------------------------------------------
AGENT CategorySummary
OUT draft_id
TOOLS http.get, extract.table, llm.generate, email.draft
MUST has(overview, "text") and has(report, "draft_id")

STEP comp1 = AGENT AllCompetitors_Alpha(name="Alpha", url="https://alpha.io/pricing", category="saas")
CHECK has(comp1, "text")
ONFAIL stop

STEP comp2 = AGENT AllCompetitors_Beta(name="Beta", url="https://beta.io/pricing", category="saas")
CHECK has(comp2, "text")
ONFAIL stop

STEP overview = TOOL llm.generate(prompt="pricing brief competitive overview", data=[comp1.text, comp2.text], format="text")
CHECK has(overview, "text") and len(overview.text) >= 20
ONFAIL retry

STEP report = TOOL email.draft(to="strategy@company.com", subject="Competitive Pricing Overview", body=overview.text)
CHECK has(report, "draft_id")
ONFAIL stop

RESULT report.draft_id
