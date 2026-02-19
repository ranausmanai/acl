# Agent Contract Language (ACL)

A minimal, line-based programming language for defining AI agent workflows with **deterministic execution**, **evidence gating**, and **structured receipts**.

```acl
INTENT "Extract pricing and send a brief"
ALLOW  http.get, extract.table, llm.generate, email.draft

AGENT PricingBrief
  TOOLS http.get, extract.table, llm.generate, email.draft
  STEP  page    = TOOL http.get(url="https://example.com/pricing")
    CHECK page.status == 200
  STEP  rows    = TOOL extract.table(text=page.text, columns=["plan","price"])
    CHECK count(rows.rows) >= 1
  STEP  brief   = TOOL llm.generate(prompt="Write an executive brief", data=rows, format="text")
  STEP  draft   = TOOL email.draft(to="cto@co.com", subject="Pricing Brief", body=brief.text)
    CHECK has(draft, "message_id")
  RESULT draft
```

Every run emits a **receipt** — a structured JSON trace of what ran, what passed, what failed, and why.

---

## Core principle

```
INTENT → STEP → CHECK → RESULT → RECEIPT
```

Every step must produce evidence (a `CHECK` expression that passes) before the pipeline advances. No evidence = **fail closed**. No hallucinations, no silent failures.

---

## Install

Requires Go 1.22+.

```bash
git clone https://github.com/ranausmanai/agent-contract-language
cd agent-contract-language
go install ./cmd/acl
```

Or build directly:

```bash
go build -o acl ./cmd/acl
./acl --help
```

---

## Quickstart

```bash
# Initialise a new project
acl init my-agents

# Run a program
acl run examples/01_pricing_brief.acl

# Pass input variables
acl run examples/02_monthly_report.acl --var region=us-west --var quarter=Q1

# View run history
acl history list
acl history show 3

# Serve all agents as an HTTP API
acl serve examples/01_pricing_brief.acl --port 8080
```

---

## Language reference

### Top-level declarations

| Keyword      | Purpose                                               |
|--------------|-------------------------------------------------------|
| `INTENT`     | Human-readable goal statement                         |
| `ALLOW`      | Tool permission allowlist for the whole program       |
| `LIMIT`      | Budget constraints: `time=60s calls=100 retries=3`   |
| `AGENT`      | Define a concrete agent                               |
| `TEMPLATE`   | Define a parameterised agent blueprint                |
| `MAKE`       | Instantiate a template into a named agent             |
| `GROUP`      | Generate many agents from a list via a template       |
| `REMOTE`     | Declare an agent hosted on another `acl serve` server |
| `SCHEDULE`   | Cron-trigger an agent in serve mode                   |

### Agent body

| Keyword      | Purpose                                               |
|--------------|-------------------------------------------------------|
| `IN`         | Declare input parameters                              |
| `OUT`        | Declare output parameters                             |
| `TOOLS`      | Restrict tool access within this agent                |
| `MUST`       | Agent-level evidence gate (evaluated after all steps) |
| `STEP`       | Define an execution step (`TOOL` or `AGENT` call)     |
| `PARALLEL`   | Run the next group of steps concurrently              |
| `CHECK`      | Step-level evidence expression                        |
| `ONFAIL`     | Policy when CHECK fails: `retry` `fallback` `stop` `askhuman` |
| `RESULT`     | Final output expression                               |

### Full example

```acl
INTENT "Weekly incident report"
ALLOW  sql.query, llm.generate, pdf.render, email.draft
LIMIT  time=120s retries=2

AGENT IncidentReport
  IN  week_start
  TOOLS sql.query, llm.generate, pdf.render, email.draft

  STEP rows = TOOL sql.query(
    query="SELECT * FROM incidents WHERE created_at >= $1",
    db="postgres://prod/ops")
    CHECK count(rows.rows) >= 0

  STEP summary = TOOL llm.generate(
    prompt="Summarise these incidents for the engineering team",
    data=rows, format="text")
    CHECK len(summary.text) > 50

  STEP report = TOOL pdf.render(template="incident_report", data=summary.text)
    CHECK has(report, "path")

  STEP draft = TOOL email.draft(
    to="engineering@co.com",
    subject="Weekly Incident Report",
    body=summary.text)
    CHECK has(draft, "message_id")

  MUST has(report, "path") and has(draft, "message_id")
  RESULT draft
```

### PARALLEL blocks

```acl
PARALLEL
  STEP a = TOOL http.get(url="https://api-a.example.com/health")
  STEP b = TOOL http.get(url="https://api-b.example.com/health")
END
  CHECK a.status == 200
  CHECK b.status == 200
```

### TEMPLATE, MAKE, GROUP

```acl
TEMPLATE Checker(target_url)
  TOOLS http.get
  STEP page = TOOL http.get(url=target_url)
    CHECK page.status == 200
  RESULT page

MAKE Checker(target_url="https://api.example.com") AS APIChecker

# Generate one agent per item in a list
GROUP AllCheckers = FOR site IN sites : MAKE Checker(target_url=site.url)
```

### REMOTE and SCHEDULE (serve mode)

```acl
# Declare an agent running on another server
REMOTE AnalysisAgent "http://analytics:8080"

# Cron-trigger an agent when running `acl serve`
SCHEDULE HealthChecker "*/5 * * * *"
```

---

## Serve mode

`acl serve` exposes every `AGENT` as an HTTP endpoint:

```bash
acl serve my_agents.acl --port 8080
```

| Route               | Auth | Description                       |
|---------------------|------|-----------------------------------|
| `GET /health`       | No   | Liveness check + agent list       |
| `GET /agents`       | Yes  | Agent descriptors (in/out/tools)  |
| `POST /run/{name}`  | Yes  | Execute agent, returns receipt    |

```bash
# Run an agent
curl -X POST http://localhost:8080/run/IncidentReport \
  -H "Content-Type: application/json" \
  -d '{"vars": {"week_start": "2025-01-01"}}'

# Secure with an API key
ACL_SERVE_API_KEY=secret acl serve my_agents.acl
curl -H "Authorization: Bearer secret" http://localhost:8080/agents
```

---

## Built-in tools

| Tool             | What it does                                              |
|------------------|-----------------------------------------------------------|
| `http.get`       | HTTP GET; returns `{status, text, url}`                   |
| `extract.table`  | Parse text tables into rows                               |
| `llm.generate`   | LLM call — Anthropic, OpenAI, Groq, Mistral, or Ollama    |
| `sql.query`      | SQL via SQLite or PostgreSQL; returns `{rows, count}`     |
| `pdf.render`     | Generate a PDF file; returns `{path, size_bytes}`         |
| `email.draft`    | Send email via SMTP; returns `{message_id, sent_at}`      |

### LLM provider configuration

`llm.generate` auto-detects the provider from environment variables:

| Env var            | Provider    | Default model                    |
|--------------------|-------------|----------------------------------|
| `ANTHROPIC_API_KEY`| anthropic   | `claude-sonnet-4-6`              |
| `OPENAI_API_KEY`   | openai      | `gpt-4o`                         |
| `GROQ_API_KEY`     | groq        | `llama-3.3-70b-versatile`        |
| `MISTRAL_API_KEY`  | mistral     | `mistral-large-latest`           |
| `OLLAMA_HOST`      | ollama      | `llama3.2`                       |

Override with `ACL_LLM_PROVIDER=groq` or pass `provider=` as a step argument.

### SQL database configuration

```bash
# SQLite
acl run report.acl --var db=sqlite:./data.db

# PostgreSQL
ACL_DB_URL=postgres://user:pass@localhost/mydb acl run report.acl
```

### Email (SMTP) configuration

```bash
ACL_SMTP_HOST=smtp.gmail.com
ACL_SMTP_PORT=587
ACL_SMTP_USER=you@gmail.com
ACL_SMTP_PASS=app-password
ACL_SMTP_FROM=you@gmail.com
```

---

## Receipt

Every `acl run` emits a structured JSON receipt:

```json
{
  "acl_version": "0.1.0",
  "timestamp": "2025-01-15T09:00:00Z",
  "status": "success",
  "intent": "Weekly incident report",
  "policy": {
    "allow": ["sql.query", "llm.generate", "pdf.render", "email.draft"],
    "limit": {"time_s": 120, "calls": null, "retries": 2}
  },
  "agents": [{
    "name": "IncidentReport",
    "status": "success",
    "must_passed": true,
    "result": {"message_id": "msg_abc123", "sent_at": "2025-01-15T09:00:01Z"},
    "steps": [{
      "name": "rows",
      "kind": "tool",
      "target": "sql.query",
      "check_passed": true,
      "cache_hit": false,
      "duration_ms": 42
    }]
  }]
}
```

Run history is stored locally at `~/.acl/history.db`:

```bash
acl history list          # last 20 runs
acl history show 7        # full receipt for run #7
acl history purge 30      # delete runs older than 30 days
```

---

## Evidence language

`CHECK` and `MUST` expressions use a safe evaluator (no `eval`):

```acl
CHECK page.status == 200
CHECK count(rows.rows) >= 3 and has(rows.rows[0], "plan")
CHECK matches(page.text, "\\d+\\.\\d{2}")
MUST  all(rows.rows, "price") and len(summary.text) > 100
```

| Function          | Description                               |
|-------------------|-------------------------------------------|
| `count(x)`        | Length of list or map                     |
| `len(x)`          | Length of string or list                  |
| `has(obj, key)`   | True if object has the given key          |
| `matches(s, re)`  | True if string matches regex              |
| `all(list, field)`| True if every item has the field          |
| `any(list, field)`| True if any item has the field            |

---

## ONFAIL policies

| Policy            | Behaviour                                              |
|-------------------|--------------------------------------------------------|
| `retry`           | Re-run the step, up to `LIMIT retries=N`              |
| `fallback name`   | Jump to a named step in the same agent                 |
| `askhuman`        | Halt with `status=needs_human`                         |
| `stop`            | Halt with `status=failed`                              |

No `ONFAIL` + failed `CHECK` → **fail closed** (same as `stop`).

---

## Custom tools (Go SDK)

```go
package main

import (
    "context"
    "acl/internal/protocol"
    "acl/internal/runtime"
    "acl/tools/builtin"
)

func main() {
    reg := builtin.NewRegistry()
    reg.RegisterBuiltin("weather.current", func(ctx context.Context, args map[string]any) (any, error) {
        city, _ := args["city"].(string)
        return map[string]any{"temp_c": 22, "city": city}, nil
    }, "1")

    r, _ := runtime.RunSource(context.Background(), src, runtime.Config{}, reg)
    // r is a *receipt.Receipt
}
```

---

## Project layout

```
cmd/acl/           ← CLI entrypoint (acl run, serve, init, history)
internal/
  ast/             ← AST node types
  lexer/           ← tokeniser
  parser/          ← line-based parser
  checker/         ← semantic validation
  runtime/         ← execution engine (steps, PARALLEL, REMOTE)
  evidence/        ← safe expression evaluator
  receipt/         ← receipt builder
  cache/           ← SHA-256 tool output cache
  server/          ← HTTP server + cron scheduler
  store/           ← SQLite run history
tools/
  builtin/         ← built-in tools (http, sql, llm, pdf, email, extract)
  sdk/go/          ← Go SDK for custom tools
  sdk/python/      ← Python SDK for custom tools
examples/          ← example .acl programs
```

---

## Run tests

```bash
go test ./...
```

---

## License

MIT
