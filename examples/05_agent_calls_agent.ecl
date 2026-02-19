# ============================================================
# Example 05 – Agent-calls-agent orchestration
#
# A coordinator agent calls two specialist agents:
#
#   ExtractPricingAgent  – fetches + parses a pricing page
#   WriteBriefAgent      – turns structured data into a PDF brief
#
# The coordinator stitches results together and sends an email.
#
# Demonstrates:
#   - STEP x = AGENT <name>(args)
#   - Chained evidence gating across agent boundaries
#   - MUST at both sub-agent and coordinator level
# ============================================================

INTENT "Coordinate pricing extraction and brief generation via nested agents"
ALLOW http.get, extract.table, llm.generate, pdf.render, email.draft
LIMIT time=120s calls=30 retries=2

# ---------------------------------------------------------------
# Sub-agent 1: ExtractPricingAgent
# ---------------------------------------------------------------
AGENT ExtractPricingAgent
IN url
OUT rows
TOOLS http.get, extract.table, llm.generate
MUST count(rows.rows) >= 3 and has(rows.rows[0], "plan")

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL retry

STEP rows = TOOL extract.table(text=page.text, columns=["plan","price","users","support"])
CHECK count(rows.rows) >= 3
ONFAIL fallback llm_rows

STEP llm_rows = TOOL llm.generate(prompt="extract pricing rows json", data=page.text, format="json")
CHECK has(llm_rows, "rows") and count(llm_rows.rows) >= 1
ONFAIL stop

RESULT rows

# ---------------------------------------------------------------
# Sub-agent 2: WriteBriefAgent
# ---------------------------------------------------------------
AGENT WriteBriefAgent
IN pricing_rows
OUT file_id
TOOLS llm.generate, pdf.render
MUST has(pdf, "file_id") and pdf.page_count >= 1

STEP analysis = TOOL llm.generate(prompt="write executive pricing brief", data=pricing_rows, format="text")
CHECK has(analysis, "text") and len(analysis.text) >= 20
ONFAIL retry

STEP pdf = TOOL pdf.render(template="pricing_brief", data=analysis.text)
CHECK has(pdf, "file_id")
ONFAIL stop

RESULT pdf.file_id

# ---------------------------------------------------------------
# Coordinator agent
# ---------------------------------------------------------------
AGENT PricingBriefCoordinator
IN url, recipients
OUT draft_id
TOOLS http.get, extract.table, llm.generate, pdf.render, email.draft
MUST has(email, "draft_id")

# Call specialist agents
STEP extracted = AGENT ExtractPricingAgent(url=url)
CHECK has(extracted, "rows") and count(extracted.rows) >= 1
ONFAIL stop

STEP brief_file = AGENT WriteBriefAgent(pricing_rows=extracted.rows)
CHECK len(brief_file) >= 4
ONFAIL retry

# Final assembly
STEP email = TOOL email.draft(to=recipients, subject="Pricing Brief Ready", body="See attached PDF brief.", attachments=[brief_file])
CHECK has(email, "draft_id") and email.attachment_count >= 1
ONFAIL stop

RESULT email.draft_id
