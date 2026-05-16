<div align="center">

```
 █████╗  ██████╗██╗
██╔══██╗██╔════╝██║
███████║██║     ██║
██╔══██║██║     ██║
██║  ██║╚██████╗███████╗
╚═╝  ╚═╝ ╚═════╝╚══════╝
```

**Agent Contract Language**

*Safe AI actions for existing apps — with receipts*

[![CI](https://github.com/ranausmanai/acl/actions/workflows/ci.yml/badge.svg)](https://github.com/ranausmanai/acl/actions)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go&logoColor=white)](https://go.dev)
[![Go Report Card](https://goreportcard.com/badge/github.com/ranausmanai/acl)](https://goreportcard.com/report/github.com/ranausmanai/acl)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)](https://github.com/ranausmanai/acl)

</div>

---

ACL is an open-source **language + runtime for safe AI actions**.

It helps you make existing app capabilities (APIs, MCP tools, Zapier actions, CLIs) usable by AI agents with:

- explicit allowed actions
- step-by-step checks
- retries/fallbacks
- preview/confirm patterns for risky writes
- receipts for every run

In short: **APIs give access. ACL gives control.**

You can treat ACL as the contract layer between:

- app capabilities (`refund_order`, `create_transaction`, `calendar.create_event`, ...)
- an AI interface (chat/voice/assistant)
- a production safety model (checks + receipts)

Try the live demos and guided tutorial:

- `https://acl.fyi/quickstart` — builder quickstart (5-minute onboarding path)
- `https://acl.fyi/agenticflow` — multi-mode demo lab (Splitwise, Calendar, Support, Zapier, Monarch)
- `https://acl.fyi/playground` — author and run ACL directly

ACL is still a **line-based language** at its core, so contracts remain readable and auditable. Every step must prove it worked — no hidden side effects, no silent failures, no hallucinations sneaking through.

```acl
INTENT "Extract pricing and send a brief to the CTO"
ALLOW  http.get, extract.table, llm.generate, email.draft

AGENT PricingBrief
  TOOLS http.get, extract.table, llm.generate, email.draft

  STEP page    = TOOL http.get(url="https://example.com/pricing")
    CHECK page.status == 200

  STEP rows    = TOOL extract.table(text=page.text, columns=["plan","price"])
    CHECK count(rows.rows) >= 1
    ONFAIL stop

  STEP brief   = TOOL llm.generate(prompt="Write an executive brief", data=rows, format="text")
    CHECK len(brief.text) > 50

  STEP draft   = TOOL email.draft(to="cto@co.com", subject="Pricing Brief", body=brief.text)
    CHECK has(draft, "message_id")

  MUST has(draft, "message_id")
  RESULT draft
```

Every run produces a structured **receipt** — a cryptographically hashed audit log of everything that happened.

---

## How it works

```
┌─────────────────────────────────────────────────────────┐
│                                                         │
│   INTENT ──▶ ALLOW ──▶ AGENT ──┐                       │
│                                 │                       │
│              ┌──────────────────▼──────────────────┐   │
│              │  for each STEP:                      │   │
│              │                                      │   │
│              │   resolve args ──▶ check cache       │   │
│              │         │               │            │   │
│              │         └───── call tool/agent ────▶ │   │
│              │                         │            │   │
│              │              evaluate CHECK expr     │   │
│              │                         │            │   │
│              │               pass ─────┴──── fail   │   │
│              │                │               │     │   │
│              │             continue       ONFAIL     │   │
│              │                          retry │      │   │
│              │                       fallback │      │   │
│              │                       askhuman │      │   │
│              │                          stop  │      │   │
│              └──────────────────────────────────────┘   │
│                                                         │
│   evaluate MUST ──▶ evaluate RESULT ──▶ emit RECEIPT   │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

---

## Install

**Requires Go 1.22+**

```bash
go install github.com/ranausmanai/acl/cmd/acl@latest
```

Or build from source:

```bash
git clone https://github.com/ranausmanai/acl
cd acl
go build -o acl ./cmd/acl
```

---

## Quickstart

Start here (recommended):

- `https://acl.fyi/quickstart` — builder onboarding path
- `https://acl.fyi/agenticflow` — guided tutorial + demos
- `https://acl.fyi/playground` — template-based prototyping

CLI quickstart:

```bash
# Start a new project
acl init my-workflow
cd my-workflow

# Edit the generated main.acl, then run it
acl run main.acl

# Pass variables inline or from a file
acl run report.acl --var region=us-west --var quarter=Q1
acl run report.acl --vars vars.json

# View run history
acl history list
acl history show 3

# Expose all agents as an HTTP API
acl serve main.acl --port 8080
```

### Make an app agent-ready (simple version)

1. Expose app capabilities as tools (API wrappers, MCP, Zapier, CLI).
2. Define an ACL flow for how an agent may use them.
3. Preview/confirm risky writes.
4. Keep the receipt.

See:

- `docs/make-your-app-agent-ready.md` — practical builder guide (start here for integrations)
- `docs/agent-ready-apps-roadmap.md` — architecture and roadmap
- `docs/zapier-bridge.md` — ACL x Zapier bridge MVP

---

## Language reference

### Top-level keywords

| Keyword    | Purpose                                                        |
|------------|----------------------------------------------------------------|
| `INTENT`   | Human-readable goal — goes into every receipt                  |
| `ALLOW`    | Allowlist of tools the program may call                        |
| `LIMIT`    | Execution budget: `time=60s calls=100 retries=3`              |
| `AGENT`    | Define a concrete runnable agent                               |
| `TEMPLATE` | Define a parameterised agent blueprint                         |
| `MAKE`     | Instantiate a template into a named agent                      |
| `GROUP`    | Generate many agents from a list variable                      |
| `REMOTE`   | Declare an agent served by another `acl serve` process         |
| `SCHEDULE` | Cron-trigger an agent in serve mode                            |

### Agent body keywords

| Keyword    | Purpose                                                        |
|------------|----------------------------------------------------------------|
| `IN`       | Declare input parameters                                       |
| `OUT`      | Declare output parameters                                      |
| `TOOLS`    | Per-agent tool restriction                                     |
| `MUST`     | Agent-level evidence gate — evaluated after all steps          |
| `STEP`     | Execute a tool or call a sub-agent                             |
| `PARALLEL` | Run the following group of steps concurrently                  |
| `CHECK`    | Step-level evidence expression — must pass to continue         |
| `ONFAIL`   | What to do when CHECK fails                                    |
| `RESULT`   | Output expression — becomes the agent's return value           |

---

## Examples

### The refund that gets stopped — what ACL does in 60 seconds

The same contract, two inputs, two opposite outcomes. Both produce receipts.

```bash
# Green: order is in-window and eligible — refund proceeds, email is sent.
acl run examples/support_refund.acl --var order_id=10482

# Blocked: order is outside the refund window. The contract halts here:
#   CHECK (eligible.refund_eligible == true)
# No code change between the two runs. The contract did the work.
acl run examples/support_refund.acl --var order_id=10483
```

See [`examples/support_refund.acl`](examples/support_refund.acl). The failure
receipt records the exact CHECK expression that stopped the agent — that's the
audit trail support, engineers, or a regulator can read without guessing.

### Incident report pipeline

```acl
INTENT "Weekly incident report for engineering"
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
    data=rows,
    format="text")
    CHECK len(summary.text) > 50
    ONFAIL retry

  STEP report = TOOL pdf.render(template="incident_report", data=summary.text)
    CHECK has(report, "path")

  STEP draft = TOOL email.draft(
    to="engineering@co.com",
    subject="Weekly Incident Report",
    body=summary.text)
    CHECK has(draft, "message_id")
    ONFAIL stop

  MUST has(report, "path") and has(draft, "message_id")
  RESULT draft
```

### PARALLEL — fetch multiple endpoints at once

```acl
AGENT HealthCheck
  TOOLS http.get

  PARALLEL
    STEP api  = TOOL http.get(url="https://api.example.com/health")
    STEP db   = TOOL http.get(url="https://db.example.com/health")
    STEP auth = TOOL http.get(url="https://auth.example.com/health")
  END

  CHECK api.status  == 200
  CHECK db.status   == 200
  CHECK auth.status == 200

  RESULT api
```

### TEMPLATE + GROUP — scale to N agents

```acl
TEMPLATE SiteChecker(target_url)
  TOOLS http.get
  STEP page = TOOL http.get(url=target_url)
    CHECK page.status == 200
  RESULT page

# Instantiate once
MAKE SiteChecker(target_url="https://api.example.com") AS APICheck

# Or generate from a list variable (sites = [{url: "..."}, ...])
GROUP AllChecks = FOR site IN sites : MAKE SiteChecker(target_url=site.url)

AGENT Orchestrator
  IN  sites
  TOOLS http.get
  STEP r0 = AGENT AllChecks_0()
  STEP r1 = AGENT AllChecks_1()
  RESULT r0
```

### REMOTE + SCHEDULE — distributed serve mode

```acl
INTENT "Distributed health monitoring"
ALLOW  http.get, sql.query

# Call an agent running on another server
REMOTE AnalyticsAgent "http://analytics-service:8080"

# Fire this agent every 5 minutes when running `acl serve`
SCHEDULE HealthMonitor "*/5 * * * *"

AGENT HealthMonitor
  TOOLS http.get
  STEP page = TOOL http.get(url="https://api.example.com/health")
    CHECK page.status == 200
  RESULT page

AGENT Pipeline
  TOOLS http.get, sql.query
  STEP health   = AGENT HealthMonitor()
  STEP analysis = AGENT AnalyticsAgent()   # dispatched over HTTP
  RESULT analysis
```

---

## Built-in tools

| Tool             | Description                                     | Returns                                  |
|------------------|-------------------------------------------------|------------------------------------------|
| `http.get`       | HTTP GET request                                | `{status, text, url}`                    |
| `extract.table`  | Parse aligned text tables into structured rows  | `{rows: [{col: val}]}`                   |
| `llm.generate`   | LLM call (multi-provider, see below)            | `{text, model, tokens, provider}`        |
| `sql.query`      | SQL via SQLite or PostgreSQL                    | `{rows: [{col: val}], count}`            |
| `pdf.render`     | Generate a PDF file                             | `{path, size_bytes, page_count}`         |
| `email.draft`    | Send email via SMTP                             | `{message_id, to, subject, sent_at}`     |

### LLM providers

`llm.generate` auto-detects the provider from environment variables:

| Environment variable | Provider   | Default model                   |
|----------------------|------------|---------------------------------|
| `ANTHROPIC_API_KEY`  | anthropic  | `claude-sonnet-4-6`             |
| `OPENAI_API_KEY`     | openai     | `gpt-4o`                        |
| `GROQ_API_KEY`       | groq       | `llama-3.3-70b-versatile`       |
| `MISTRAL_API_KEY`    | mistral    | `mistral-large-latest`          |
| `OLLAMA_HOST`        | ollama     | `llama3.2`                      |

Override per-step: `TOOL llm.generate(prompt="...", provider="groq", model="mixtral-8x7b")`
Or globally: `ACL_LLM_PROVIDER=openai`

### Database (sql.query)

```bash
# SQLite
STEP r = TOOL sql.query(query="SELECT * FROM logs", db="sqlite:./app.db")
STEP r = TOOL sql.query(query="SELECT * FROM logs", db="sqlite::memory:")

# PostgreSQL
STEP r = TOOL sql.query(query="SELECT * FROM logs", db="postgres://user:pass@host/db")

# Or set globally
ACL_DB_URL=postgres://user:pass@host/db acl run report.acl
```

### Email (SMTP)

```bash
ACL_SMTP_HOST=smtp.gmail.com
ACL_SMTP_PORT=587
ACL_SMTP_USER=you@gmail.com
ACL_SMTP_PASS=app-password
ACL_SMTP_FROM=you@gmail.com
```

---

## Serve mode

`acl serve` exposes every `AGENT` as a live HTTP endpoint:

```bash
acl serve my_agents.acl --port 8080
```

| Route               | Auth | Description                             |
|---------------------|------|-----------------------------------------|
| `GET  /health`      | No   | Liveness + list of agent names          |
| `GET  /agents`      | Yes  | Full agent descriptors (in/out/tools)   |
| `POST /run/{name}`  | Yes  | Execute an agent, returns full receipt  |

```bash
# Call an agent
curl -s -X POST http://localhost:8080/run/IncidentReport \
  -H "Content-Type: application/json" \
  -d '{"vars": {"week_start": "2025-01-01"}}' | jq .status

# Lock it down with an API key
ACL_SERVE_API_KEY=mysecret acl serve my_agents.acl

curl -H "Authorization: Bearer mysecret" http://localhost:8080/agents
```

SCHEDULE-triggered agents run automatically in the background and are saved to history.

---

## Evidence language

`CHECK` and `MUST` use a **safe expression evaluator** — no `eval()`, no arbitrary code.

```acl
CHECK page.status == 200
CHECK count(rows.rows) >= 3 and has(rows.rows[0], "plan")
CHECK matches(page.text, "\\d+\\.\\d{2}")
MUST  all(rows, "price") and len(summary.text) > 100
```

| Function           | Description                                   |
|--------------------|-----------------------------------------------|
| `count(x)`         | Number of items in a list or map              |
| `len(x)`           | Length of a string or list                    |
| `has(obj, key)`    | True if the object contains the key           |
| `matches(s, re)`   | True if string matches the regular expression |
| `all(list, field)` | True if every item in list has the field      |
| `any(list, field)` | True if any item in list has the field        |

If a `CHECK` expression raises an error (undefined variable, type mismatch), it **fails closed** — never silently passes.

---

## ONFAIL policies

| Policy            | Behaviour                                           |
|-------------------|-----------------------------------------------------|
| `retry`           | Re-run the step, up to `LIMIT retries=N`            |
| `fallback name`   | Jump to a named step in the same agent              |
| `askhuman`        | Halt with `status=needs_human`                      |
| `stop`            | Halt with `status=failed`                           |

No `ONFAIL` + failed `CHECK` → **fail closed** (equivalent to `stop`).

---

## Receipt

Every run emits a structured JSON receipt — the authoritative record of what happened:

```json
{
  "acl_version": "0.1.0",
  "timestamp": "2025-06-01T09:00:00Z",
  "status": "success",
  "intent": "Extract pricing and send a brief to the CTO",
  "policy": {
    "allow": ["http.get", "extract.table", "llm.generate", "email.draft"],
    "limit": { "time_s": null, "calls": null, "retries": 0 }
  },
  "agents": [{
    "name": "PricingBrief",
    "status": "success",
    "must_passed": true,
    "result": { "message_id": "msg_k3j9x", "sent_at": "2025-06-01T09:00:02Z" },
    "steps": [
      {
        "name": "page",
        "kind": "tool",
        "target": "http.get",
        "output_hash": "a3f1c9d2b7e84011",
        "check_passed": true,
        "cache_hit": false,
        "duration_ms": 312
      },
      {
        "name": "rows",
        "kind": "tool",
        "target": "extract.table",
        "check_passed": true,
        "cache_hit": true,
        "duration_ms": 1
      }
    ]
  }]
}
```

Run history is persisted locally at `~/.acl/history.db`:

```bash
acl history list           # last 20 runs
acl history show 7         # full receipt for run #7
acl history purge 30       # delete runs older than 30 days
```

---

## Determinism and caching

Before every tool call, ACL computes:

```
cache_key = SHA-256(tool_name + normalised_args + tool_version)
```

Identical inputs always return from cache on subsequent runs. The receipt records `"cache_hit": true` for every cached step. Use `--no-cache` to bypass.

---

## Custom tools (Go)

```go
package main

import (
    "context"
    "github.com/ranausmanai/acl/internal/runtime"
    "github.com/ranausmanai/acl/tools/builtin"
)

func main() {
    reg := builtin.NewRegistry()

    reg.RegisterBuiltin("weather.now", func(ctx context.Context, args map[string]any) (any, error) {
        city, _ := args["city"].(string)
        return map[string]any{"city": city, "temp_c": 22, "conditions": "sunny"}, nil
    }, "1")

    src := `
INTENT "Get weather"
ALLOW  weather.now
AGENT WeatherAgent
  IN  city
  TOOLS weather.now
  STEP w = TOOL weather.now(city=city)
    CHECK has(w, "temp_c")
  RESULT w
`
    r, _ := runtime.RunSource(context.Background(), src, runtime.Config{
        Vars: map[string]any{"city": "London"},
    }, reg)

    // r.Status == "success", r.Agents[0].Result == {city: London, temp_c: 22, ...}
}
```

---

## Project layout

```
acl/
├── cmd/acl/                  ← CLI: run, serve, init, history
├── internal/
│   ├── lexer/                ← tokeniser
│   ├── parser/               ← line-based parser → AST
│   ├── ast/                  ← node types (AgentDef, StepDef, ...)
│   ├── checker/              ← semantic validation
│   ├── runtime/              ← execution engine (steps, PARALLEL, REMOTE)
│   ├── evidence/             ← safe expression evaluator
│   ├── receipt/              ← receipt builder + JSON schema
│   ├── cache/                ← SHA-256 file cache
│   ├── server/               ← HTTP server + cron scheduler
│   └── store/                ← SQLite run history (~/.acl/history.db)
├── tools/
│   ├── builtin/              ← http, sql, llm, pdf, email, extract
│   └── sdk/
│       ├── go/               ← Go tool SDK
│       └── python/           ← Python tool SDK
└── examples/                 ← sample .acl programs
```

---

## Run tests

```bash
go test ./...
```

---

## Production deploy (acl.fyi)

The live site at [acl.fyi](https://acl.fyi) is served by a single systemd-managed
`acl serve` process on a Linux/amd64 host. A one-command deploy script ships
in this repo:

```bash
ssh root@<host>
cd /opt/acl-src      # this repo, cloned on the VPS
git pull
./deploy.sh
```

`deploy.sh` cross-compiles the binary, `acl check`s the canonical demo file,
backs up the current `/opt/acl/acl` to `acl.bak-<timestamp>-deploy`, atomically
swaps the new binary in, restarts `acl.service`, and verifies that `/health`
and `/agenticflow` return 200. On any failure it puts the old binary back and
exits non-zero.

Overrides are environment variables — see the comments at the top of
[`deploy.sh`](deploy.sh) for the full list.

---

## Contributing

Pull requests are welcome. For major changes please open an issue first.

1. Fork the repo
2. Create a branch: `git checkout -b my-feature`
3. Make your changes and add tests
4. Run `go test ./...` — all tests must pass
5. Open a pull request

---

## License

[MIT](LICENSE)
