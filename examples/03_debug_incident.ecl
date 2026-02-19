# ============================================================
# Example 03 – Automated incident post-mortem
#
# Pipeline:
#   1. Fetch raw incident log    (http.get)
#   2. Parse the log table       (extract.table)
#   3. Query DB for error events (sql.query)
#   4. LLM root-cause analysis   (llm.generate → json)
#   5. Draft post-mortem email   (email.draft)
#
# Evidence gating:
#   - Log must have >= 3 rows
#   - Root-cause analysis must identify severity and root_cause
#   - MUST: email draft_id exists and rca has severity
# ============================================================

INTENT "Diagnose incident and produce a post-mortem report"
ALLOW http.get, extract.table, sql.query, llm.generate, email.draft
LIMIT time=120s calls=25 retries=2

AGENT DebugIncident
IN incident_url
OUT draft_id
TOOLS http.get, extract.table, sql.query, llm.generate, email.draft
MUST has(rca, "root_cause") and has(rca, "severity") and has(postmortem, "draft_id")

# Step 1: Fetch the incident log page
STEP page = TOOL http.get(url=incident_url)
CHECK page.status == 200
ONFAIL retry

# Step 2: Extract structured rows from log
STEP log_rows = TOOL extract.table(text=page.text, columns=["time","severity","service","message"])
CHECK count(log_rows.rows) >= 3
ONFAIL fallback db_fallback

# Fallback: query DB directly if page extraction failed
STEP db_fallback = TOOL sql.query(query="SELECT time, severity, service, message FROM incidents")
CHECK count(db_fallback.rows) >= 1
ONFAIL stop

# Step 3: Count ERROR-level events from DB
STEP errors = TOOL sql.query(query="SELECT COUNT(*) as error_count FROM incidents WHERE severity='ERROR'")
CHECK errors.rows[0].error_count >= 1
ONFAIL stop

# Step 4: LLM root-cause analysis
STEP rca = TOOL llm.generate(prompt="debug incident root cause analysis", data=log_rows.rows, format="json")
CHECK has(rca, "root_cause") and has(rca, "severity") and has(rca, "affected_services")
ONFAIL retry

# Step 5: Draft post-mortem report
STEP postmortem = TOOL email.draft(to="oncall@company.com", subject="Incident Post-Mortem", body=rca.root_cause)
CHECK has(postmortem, "draft_id")
ONFAIL stop

RESULT postmortem.draft_id
