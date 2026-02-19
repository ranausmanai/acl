# ============================================================
# Example 02 – Generate a monthly sales report
#
# Pipeline:
#   1. Query sales data from DB (sql.query)
#   2. Summarise results       (llm.generate)
#   3. Render PDF report       (pdf.render)
#   4. Email the PDF           (email.draft)
# ============================================================

INTENT "Generate and distribute the monthly sales report"
ALLOW sql.query, llm.generate, pdf.render, email.draft
LIMIT time=90s calls=15 retries=2

# ---------------------------------------------------------------
# Template: summarise any SQL result set
# ---------------------------------------------------------------
TEMPLATE SummarizeTableAgent(query, report_title)
IN query, report_title
OUT summary
TOOLS sql.query, llm.generate
MUST has(summary, "text") and len(summary.text) >= 10

STEP data = TOOL sql.query(query=query)
CHECK count(data.rows) >= 1
ONFAIL stop

STEP summary = TOOL llm.generate(prompt="monthly report summary", data=data.rows, format="text")
CHECK has(summary, "text")
ONFAIL retry

RESULT summary

# ---------------------------------------------------------------
# Instantiate: North region summary
# ---------------------------------------------------------------
MAKE SummarizeTableAgent(query="SELECT * FROM sales WHERE region='North'", report_title="North Region") AS NorthSummary

# ---------------------------------------------------------------
# Main agent
# ---------------------------------------------------------------
AGENT MonthlyReport
OUT draft_id
TOOLS sql.query, llm.generate, pdf.render, email.draft
MUST has(pdf, "file_id") and has(email, "draft_id")

STEP totals = TOOL sql.query(query="SELECT region, SUM(amount) as revenue, COUNT(*) as units FROM sales GROUP BY region")
CHECK count(totals.rows) >= 1
ONFAIL stop

STEP north = AGENT NorthSummary(query="SELECT * FROM sales WHERE region='North'", report_title="North Region")
CHECK has(north, "text")
ONFAIL retry

STEP analysis = TOOL llm.generate(prompt="monthly report summary for all regions", data=totals.rows, format="text")
CHECK has(analysis, "text") and len(analysis.text) >= 30
ONFAIL retry

STEP pdf = TOOL pdf.render(template="monthly_report", data=totals.rows)
CHECK has(pdf, "file_id") and pdf.page_count >= 1
ONFAIL stop

STEP email = TOOL email.draft(to="board@company.com", subject="Monthly Sales Report", body=analysis.text, attachments=[])
CHECK has(email, "draft_id")
ONFAIL stop

RESULT email.draft_id
