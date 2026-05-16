# ACL — Agent Contract Language: Specification v0.1

## 1. Overview

ACL is a minimal, line-based DSL for defining AI-agent workflows with deterministic
execution, evidence gating, and structured receipts.

**Core primitive:**

```
INTENT → ACTION → EVIDENCE → RECEIPT
```

Every action must produce evidence (a `CHECK` expression that passes) before the
workflow proceeds. If evidence is absent or fails, the runtime **fails closed**:
execution stops, no result is emitted as authoritative, and the receipt records
the exact expression that failed.

The runtime is implemented in Go (`internal/`) with built-in tools under
`tools/builtin/`. A reference Python implementation lives in `ecl/` for
experimentation; the Go runtime is the source of truth for this spec.

---

## 2. Keywords

ACL has two namespaces of keywords: **top-level** (program structure) and
**agent body** (inside an `AGENT`/`TEMPLATE` block).

### Top-level

| Keyword    | Purpose                                                      |
|------------|--------------------------------------------------------------|
| `INTENT`   | Human-readable goal. Recorded verbatim in every receipt.     |
| `ALLOW`    | Allowlist of tool names the program may call.                |
| `LIMIT`    | Execution budget: `time=60s`, `calls=100`, `retries=3`.      |
| `AGENT`    | Define a concrete, runnable agent.                           |
| `TEMPLATE` | Define a parameterised agent blueprint.                      |
| `MAKE`     | Instantiate a `TEMPLATE` into a named agent.                 |
| `GROUP`    | Generate many concrete agents from a list variable.          |
| `REMOTE`   | Declare an agent served by another `acl serve` process.      |
| `SCHEDULE` | Cron trigger for an agent in `acl serve` mode.               |

### Agent body

| Keyword    | Purpose                                                       |
|------------|---------------------------------------------------------------|
| `IN`       | Declare input parameters for this agent.                      |
| `OUT`      | Declare output parameter names (informational; for receipts). |
| `TOOLS`    | Per-agent tool restriction (subset of `ALLOW`).               |
| `STEP`     | Execute a tool or sub-agent and bind the result.              |
| `PARALLEL` | Open a block whose contained `STEP`s run concurrently.        |
| `END`      | Close a `PARALLEL` block.                                     |
| `CHECK`    | Step-level evidence expression. Must hold to continue.        |
| `ONFAIL`   | Policy when `CHECK` fails: retry / fallback / askhuman / stop.|
| `MUST`     | Agent-level evidence gate, evaluated after all steps succeed. |
| `RESULT`   | Output expression; becomes this agent's return value.         |

---

## 3. Grammar (EBNF)

```ebnf
program        ::= stmt*

stmt           ::= intent_stmt
                 | allow_stmt
                 | limit_stmt
                 | agent_def
                 | template_def
                 | make_stmt
                 | group_stmt
                 | remote_stmt
                 | schedule_stmt

intent_stmt    ::= "INTENT" '"' text '"'
allow_stmt     ::= "ALLOW" tool ("," tool)*
limit_stmt     ::= "LIMIT" (limit_kv)+
limit_kv       ::= ("time" | "calls" | "retries") "=" value
remote_stmt    ::= "REMOTE" agent_name '"' url '"'
schedule_stmt  ::= "SCHEDULE" agent_name '"' cron_expr '"'

agent_def      ::= "AGENT" name NL agent_body
template_def   ::= "TEMPLATE" name "(" params ")" NL agent_body

agent_body     ::= (in_clause | out_clause | tools_clause
                  | must_clause | step_def | parallel_block
                  | result_clause)*

in_clause      ::= "IN"    name ("," name)*
out_clause     ::= "OUT"   name ("," name)*
tools_clause   ::= "TOOLS" tool  ("," tool)*
must_clause    ::= "MUST"  expr
result_clause  ::= "RESULT" expr

step_def       ::= "STEP" name "=" ("TOOL" | "AGENT") call_expr NL
                   check_clause? onfail_clause?
parallel_block ::= "PARALLEL" NL step_def+ "END"

check_clause   ::= "CHECK" expr
onfail_clause  ::= "ONFAIL" onfail_policy

onfail_policy  ::= "retry"
                 | "fallback" step_name
                 | "askhuman"
                 | "stop"

make_stmt      ::= "MAKE" name "(" kwargs ")" "AS" name
group_stmt     ::= "GROUP" name "=" "FOR" name "IN" source
                   ":" "MAKE" name "(" kwargs ")"

call_expr      ::= name "(" kwargs ")"
kwargs         ::= (name "=" expr ("," name "=" expr)*)?
params         ::= (name ("," name)*)?

name           ::= identifier
tool           ::= identifier ("." identifier)*
source         ::= identifier          (* variable from --vars *)
cron_expr      ::= 5-field cron string (min hour dom mon dow)
expr           ::= comparison / boolean / call / attr / index / literal

(* Lines beginning with # are comments; blank lines are ignored. *)
```

---

## 4. Agent definition

```
AGENT ExtractPricing
  IN  url
  OUT rows
  TOOLS http.get, extract.table, llm.generate

  STEP page = TOOL http.get(url=url)
    CHECK page.status == 200
    ONFAIL retry

  STEP rows = TOOL extract.table(text=page.text, columns=["plan","price"])
    CHECK count(rows.rows) >= 3
    ONFAIL fallback parse_with_llm

  STEP parse_with_llm = TOOL llm.generate(
    prompt="extract pricing rows as json",
    data=page.text,
    format="json")
    CHECK has(parse_with_llm, "rows")
    ONFAIL stop

  MUST count(rows.rows) >= 3 and has(rows.rows[0], "price")
  RESULT rows
```

### 4.1 Execution order

Steps execute in **declaration order** by default. A `PARALLEL ... END` block runs
its contained steps concurrently (see §5). Fallback jumps substitute the current
step position; execution resumes forward from there.

### 4.2 MUST gate

After all steps succeed, the agent-level `MUST` expression is evaluated against
the full step-output environment. If it fails, the agent status becomes `failed`
and `RESULT` is not emitted. This is the agent-level evidence gate.

### 4.3 RESULT

The `RESULT` expression is evaluated last and its value becomes the agent's
return value, stored under the `STEP` name that invoked this agent in the
caller's environment.

---

## 5. PARALLEL

```
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

### 5.1 Semantics

- Every `STEP` inside the block is launched concurrently.
- The block completes when **all** contained steps have finished.
- Each step's `CHECK` (if present) is evaluated as soon as that step finishes.
- Determinism: cache keys for each step are computed independently, so a
  cache hit on one step still avoids the underlying call regardless of order.
- Steps in a `PARALLEL` block **cannot reference each other** by `STEP` name.
  References to outputs of preceding (non-parallel) steps are allowed.
- The receipt records `"parallel": true` on every step trace inside the block,
  preserving the as-completed order.

### 5.2 Failure handling

If any `CHECK` inside the block fails, its `ONFAIL` policy applies to that step
in isolation. The block as a whole completes once every contained step has a
terminal status (passed / retried-to-success / failed-after-policy). The next
top-level statement only executes if the agent's overall state still allows it.

---

## 6. TEMPLATE, MAKE, and GROUP

### 6.1 TEMPLATE

```
TEMPLATE FetchAgent(url, columns)
  IN  url, columns
  OUT rows
  TOOLS http.get, extract.table
  STEP page = TOOL http.get(url=url)
    CHECK page.status == 200
    ONFAIL stop
  STEP rows = TOOL extract.table(text=page.text, columns=columns)
  MUST count(rows.rows) >= 1
  RESULT rows
```

Template parameters (`url`, `columns`) are substituted at expansion time.

### 6.2 MAKE

```
MAKE FetchAgent(url="https://example.com/pricing",
                columns=["plan","price"]) AS PricingFetcher
```

Produces a concrete `AgentDef` named `PricingFetcher`.

### 6.3 GROUP

```
GROUP AllCheckers = FOR site IN sites
  : MAKE FetchAgent(url=site.url, columns=["plan","price"])
```

- `sites` is resolved from the `--vars` JSON file as a list.
- For each item, a concrete agent is created named `AllCheckers_<item.name>`
  (or `AllCheckers_<i>` if no `name` field exists).
- All generated agents are registered and callable from `STEP`s.

---

## 7. REMOTE and SCHEDULE

### 7.1 REMOTE

```
REMOTE AnalyticsAgent "http://analytics-service:8080"

AGENT Pipeline
  TOOLS http.get
  STEP analysis = AGENT AnalyticsAgent()   # dispatched over HTTP
  RESULT analysis
```

When a `STEP` calls `AGENT AnalyticsAgent(...)`, the runtime issues
`POST <url>/run/AnalyticsAgent` with `{"vars": {...inputs...}}` and treats the
response receipt's `agents[0].result` as the step output. The remote receipt's
agent trace is nested into the caller's receipt so the audit log is end-to-end.

### 7.2 SCHEDULE

```
SCHEDULE HealthMonitor "*/5 * * * *"

AGENT HealthMonitor
  TOOLS http.get
  STEP page = TOOL http.get(url="https://api.example.com/health")
    CHECK page.status == 200
  RESULT page
```

`SCHEDULE <agent> "<cron>"` is honored only by `acl serve`. The server parses
the 5-field cron expression on startup and fires the agent in the background
on each tick. Every scheduled run is persisted to `~/.acl/history.db` just
like a manual run.

---

## 8. Evidence language

### 8.1 Supported constructs

| Construct           | Example                                       |
|---------------------|-----------------------------------------------|
| Literals            | `200`, `"text"`, `true`, `false`, `null`      |
| Variable lookup     | `page`, `rows`                                |
| Attribute access    | `page.status`, `result.rows[0].plan`          |
| Subscript           | `items[0]`, `rows["plan"]`                    |
| Comparison          | `==  !=  <  <=  >  >=  in  not in`            |
| Boolean ops         | `and  or  not`                                |
| `has(obj, key)`     | `has(rows[0], "price")`                       |
| `len(x)`            | `len(brief.text) > 100`                       |
| `count(x)`          | `count(rows.rows) >= 3`                       |
| `matches(text, re)` | `matches(page.text, "\\d+\\.\\d{2}")`         |
| `all(list, field)`  | `all(rows.rows, "price")`                     |
| `any(list, field)`  | `any(rows.rows, "price")`                     |
| List literals       | `["plan", "price"]`                           |

### 8.2 Security

Expressions are parsed and evaluated by `internal/evidence`, a safe AST walker.
`eval()` is never called. Unknown function names are an evaluation error.

### 8.3 Fail closed

If any `CHECK` expression raises an error (undefined variable, type mismatch,
unknown function), the check result is `false` and the configured `ONFAIL`
policy is applied. No expression failure silently passes.

---

## 9. ONFAIL policies

| Policy            | Behaviour                                                |
|-------------------|----------------------------------------------------------|
| `retry`           | Re-execute the same step, bounded by `LIMIT retries=N`.  |
| `fallback <name>` | Jump to a named step in the same agent.                  |
| `askhuman`        | Halt with `status=needs_human`.                          |
| `stop`            | Halt with `status=failed`.                               |

If no `ONFAIL` is given and `CHECK` fails, the runtime **fails closed**
(equivalent to `stop`).

---

## 10. Determinism rules

1. **Step ordering**: top-level steps execute in declaration order. `PARALLEL`
   blocks (§5) explicitly opt into concurrent execution within their scope.
2. **Retry bound**: maximum `LIMIT.retries` attempts (default 0) per step.
3. **Fallback resolution**: target is resolved by name at parse/expand time;
   missing name → `failed`.
4. **Caching**: before every tool call, the runtime computes
   `cache_key = SHA-256(tool_name + normalised_args + tool_version)`.
   If a cache file exists, the cached result is returned and `cache_hit=true`
   is recorded. Identical inputs always produce identical outputs across runs.
5. **Arg normalisation**: dict keys are sorted; numbers, booleans, nulls are
   normalised before hashing.
6. **No global mutable state**: each agent invocation receives a fresh local
   environment (a copy of program vars + declared inputs).
7. **Agent call depth**: capped (default 10) to prevent runaway recursion.

---

## 11. Caching rules

- Default cache directory: `~/.acl/cache/` (overridable via `--cache-dir`).
- Cache files: `<sha256_hex>.json`.
- Key computation: `SHA-256(json({"tool": name, "args": normalised, "version": v}))`.
- Cache is populated on first tool call; subsequent identical calls are served.
- `--no-cache` runs a cache-free pass (results are not written to disk).
- The receipt records `cache_hit` per step and per tool call.

---

## 12. Permission allowlist

- `ALLOW` declares the set of tools permitted for the entire program.
- If a `STEP` attempts to call a tool not in `ALLOW`, execution halts immediately
  with `status=failed` and a permission error in the receipt.
- A per-agent `TOOLS` clause provides an informational declaration (used in
  receipts and route schemas); it does not override the program-level `ALLOW`.

---

## 13. Receipt JSON schema

```json
{
  "acl_version": "0.1.0",
  "timestamp": "<ISO-8601>",
  "status": "success | failed | needs_human",
  "intent": "<string>",
  "policy": {
    "allow": ["tool1", "tool2"],
    "limit": { "time_s": 60.0, "calls": 100, "retries": 3 }
  },
  "agents": [
    {
      "name": "<string>",
      "from_template": "<string> | null",
      "template_args": {},
      "inputs": ["url"],
      "outputs": ["rows"],
      "tools": ["http.get", "extract.table"],
      "must_expr": "<string> | null",
      "must_passed": true,
      "result": "<any>",
      "status": "success | failed | needs_human | skipped",
      "steps": [
        {
          "name": "<string>",
          "kind": "tool | agent",
          "target": "<string>",
          "args": {},
          "output_hash": "<sha256-prefix-16>",
          "output_preview": "<first 200 chars>",
          "cache_hit": false,
          "check_expr": "<string> | null",
          "check_passed": true,
          "onfail_policy": "retry | fallback | askhuman | stop | null",
          "retries_used": 0,
          "parallel": false,
          "started_at": "<ISO-8601>",
          "ended_at": "<ISO-8601>",
          "duration_ms": 12,
          "tool_calls": [
            { "tool": "http.get", "cache_hit": false, "duration_ms": 8 }
          ],
          "error": null
        }
      ]
    }
  ],
  "error": null
}
```

### 13.1 Output hashing

Tool outputs are hashed with SHA-256 and stored as a 16-character hex prefix in
`output_hash`. The full output is never stored in the receipt; only a 200-character
`output_preview` is included.

### 13.2 History

Receipts produced by `acl run` and `acl serve` are persisted to a local SQLite
database at `~/.acl/history.db` unless `ACL_NO_HISTORY=1` is set.

---

## 14. Built-in tools

The Go runtime ships these production tools under `tools/builtin/`. Mock variants
exist alongside (`*_mock.go`) and are used by the test suite — never wired into
production runs.

| Tool             | Signature                                            | Returns                                       |
|------------------|------------------------------------------------------|-----------------------------------------------|
| `http.get`       | `url: str`                                           | `{status, text, url}`                         |
| `extract.table`  | `text: str, columns: list[str]?`                     | `{rows: [{col: val}]}`                        |
| `llm.generate`   | `prompt: str, data?, format="text|json", provider?, model?` | `{text, model, tokens, provider}`     |
| `sql.query`      | `query: str, db?`                                    | `{rows: [{col: val}], count}`                 |
| `pdf.render`     | `template: str, data?`                               | `{path, size_bytes, page_count}`              |
| `email.draft`    | `to, subject, body, attachments=[]`                  | `{message_id, to, subject, sent_at}`          |

### 14.1 `llm.generate` provider resolution

The provider is auto-detected from environment variables and may be overridden
per-step or globally via `ACL_LLM_PROVIDER`:

| Environment variable | Provider   | Default model            |
|----------------------|------------|--------------------------|
| `ANTHROPIC_API_KEY`  | anthropic  | `claude-sonnet-4-6`      |
| `OPENAI_API_KEY`     | openai     | `gpt-4o`                 |
| `GROQ_API_KEY`       | groq       | `llama-3.3-70b-versatile`|
| `MISTRAL_API_KEY`    | mistral    | `mistral-large-latest`   |
| `OLLAMA_HOST`        | ollama     | `llama3.2`               |

### 14.2 `sql.query` databases

`db=` accepts a `sqlite:` or `postgres://` URL. If omitted, the runtime uses
`ACL_DB_URL` from the environment.

### 14.3 `email.draft` SMTP

Set `ACL_SMTP_HOST`, `ACL_SMTP_PORT`, `ACL_SMTP_USER`, `ACL_SMTP_PASS`, and
`ACL_SMTP_FROM` to enable real send. With none set, the tool produces a
deterministic mock receipt for local development.

---

## 15. Error handling summary

| Situation                              | Outcome                              |
|----------------------------------------|--------------------------------------|
| Tool not in `ALLOW`                    | Permission error; step fails         |
| `CHECK` expression raises              | Check = false; `ONFAIL` applied      |
| `CHECK` fails, no `ONFAIL`             | Fail closed (`stop` semantics)       |
| Retry limit exceeded                   | `status=failed`                      |
| Fallback target not defined            | `status=failed`                      |
| `MUST` expression fails                | `status=failed`, no `RESULT` emitted |
| Parse error in source file             | Receipt emitted with `status=failed` |
| Time / call budget exceeded            | `status=failed`                      |
| Agent call depth > 10                  | `status=failed`                      |
| Remote agent unreachable               | Step fails; `ONFAIL` applied         |

---

## 16. Execution model

```
parse(source)
     │
     ▼
expand_templates()          ← replaces MAKE/GROUP with concrete AgentDefs
     │
     ▼
register all AgentDefs
     │
     ▼
run agents in declaration order (or named via --agent / POST /run/<Name>)
  │
  ├─ for each STEP (or PARALLEL block) in order:
  │    ├─ resolve args against local env
  │    ├─ assert ALLOW permission
  │    ├─ compute cache key → cache hit? return cached
  │    ├─ invoke tool / sub-agent / remote agent
  │    ├─ store output in local_env[step.name]
  │    ├─ evaluate CHECK → bool
  │    └─ if fail: apply ONFAIL (retry / fallback / askhuman / stop)
  │
  ├─ evaluate MUST expression
  └─ evaluate RESULT expression
       │
       ▼
  emit receipt → ~/.acl/history.db
```
