# ============================================================
# Example 01 – Extract pricing and write an executive brief
#
# Pipeline:
#   1. Fetch the pricing page  (http.get)
#   2. Parse the table         (extract.table)
#   3. Fallback: ask LLM to parse if table extraction fails
#   4. Summarise into a brief  (llm.generate)
#   5. Draft the email         (email.draft)
# ============================================================

INTENT "Extract pricing tiers and write an executive brief"
ALLOW http.get, extract.table, llm.generate, email.draft
LIMIT time=60s calls=20 retries=2

# ---------------------------------------------------------------
# Template: reusable pricing extractor
# ---------------------------------------------------------------
TEMPLATE ExtractPricingAgent(url, columns)
IN url, columns
OUT rows
TOOLS http.get, extract.table, llm.generate
MUST count(rows.rows) >= 3 and has(rows.rows[0], "plan")

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL retry

STEP rows = TOOL extract.table(text=page.text, columns=columns)
CHECK count(rows.rows) >= 3
ONFAIL fallback parse_with_llm

STEP parse_with_llm = TOOL llm.generate(prompt="extract pricing rows json", data=page.text, format="json")
CHECK has(parse_with_llm, "rows") and count(parse_with_llm.rows) >= 1
ONFAIL stop

RESULT rows

# ---------------------------------------------------------------
# Instantiate template
# ---------------------------------------------------------------
MAKE ExtractPricingAgent(url="https://example.com/pricing", columns=["plan","price","users"]) AS FetchPricing

# ---------------------------------------------------------------
# Main agent: orchestrates extraction + brief writing
# ---------------------------------------------------------------
AGENT WritePricingBrief
IN url
OUT draft_id
TOOLS http.get, extract.table, llm.generate, email.draft
MUST has(brief, "text") and has(draft, "draft_id")

STEP pricing = AGENT FetchPricing(url=url)
CHECK count(pricing.rows) >= 2
ONFAIL stop

STEP brief = TOOL llm.generate(prompt="write executive pricing brief", data=pricing.rows, format="text")
CHECK has(brief, "text") and len(brief.text) >= 20
ONFAIL retry

STEP draft = TOOL email.draft(to="cto@company.com", subject="Pricing Analysis Brief", body=brief.text)
CHECK has(draft, "draft_id")
ONFAIL stop

RESULT draft.draft_id
