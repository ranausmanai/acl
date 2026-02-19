# ECL — Universal AI Execution Protocol: Specification v0.1

## 1. Overview

ECL (Execution Control Language) is a minimal, line-based DSL for defining multi-agent AI pipelines with deterministic execution, evidence gating, and structured receipts.

**Core primitive:**

```
INTENT → ACTION → EVIDENCE → RECEIPT
```

Every action must produce evidence (a CHECK expression that passes) before the pipeline proceeds. If evidence is absent or fails, the runtime **fails closed** — execution stops and no output is emitted as authoritative.

---

## 2. Keywords (14 total)

| Keyword    | Scope          | Purpose                                         |
|------------|----------------|-------------------------------------------------|
| `INTENT`   | top-level      | Human-readable goal declaration                 |
| `ALLOW`    | top-level      | Tool permission allowlist                       |
| `LIMIT`    | top-level      | Budget constraints (time, calls, retries)       |
| `AGENT`    | top-level      | Define a concrete agent                         |
| `TEMPLATE` | top-level      | Define a parameterised agent blueprint          |
| `MAKE`     | top-level      | Instantiate a template into a named agent       |
| `GROUP`    | top-level      | Generate many agents from a list via a template |
| `IN`       | agent body     | Declare input parameters                        |
| `OUT`      | agent body     | Declare output parameters                       |
| `TOOLS`    | agent body     | Restrict tool access for this agent             |
| `MUST`     | agent body     | Agent-level evidence gate (post-all-steps)      |
| `STEP`     | agent body     | Define an execution step                        |
| `CHECK`    | after STEP     | Step-level evidence gate                        |
| `ONFAIL`   | after CHECK    | Policy if CHECK fails                           |
| `RESULT`   | agent body     | Final output expression                         |

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

intent_stmt    ::= "INTENT" '"' text '"'
allow_stmt     ::= "ALLOW" tool ("," tool)*
limit_stmt     ::= "LIMIT" (limit_kv)+
limit_kv       ::= ("time" | "calls" | "retries") "=" value

agent_def      ::= "AGENT" name NL agent_body
template_def   ::= "TEMPLATE" name "(" params ")" NL agent_body

agent_body     ::= (in_clause | out_clause | tools_clause
                  | must_clause | step_def | result_clause)*

in_clause      ::= "IN"    name ("," name)*
out_clause     ::= "OUT"   name ("," name)*
tools_clause   ::= "TOOLS" tool  ("," tool)*
must_clause    ::= "MUST"  expr
result_clause  ::= "RESULT" expr

step_def       ::= "STEP" name "=" ("TOOL" | "AGENT") call_expr NL
                   check_clause? onfail_clause?
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
expr           ::= (* Python-subset expression: comparisons, bool ops,
                      function calls, attribute access, subscripts, literals *)

(* Lines beginning with # are comments; blank lines are ignored *)
```

---

## 4. Agent Definition

```
AGENT ExtractPricing
IN  url
OUT rows
TOOLS http.get, extract.table, llm.generate
MUST count(rows.rows) >= 3 and has(rows.rows[0], "price")

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL retry

STEP rows = TOOL extract.table(text=page.text, columns=["plan","price"])
CHECK count(rows.rows) >= 3
ONFAIL fallback parse_with_llm

STEP parse_with_llm = TOOL llm.generate(prompt="extract pricing rows json", data=page.text, format="json")
CHECK has(parse_with_llm, "rows")
ONFAIL stop

RESULT rows
```

### 4.1 Execution Order

Steps execute in **declaration order** (deterministic, top-to-bottom).  Fallback jumps substitute the current step position; execution resumes forward from there.

### 4.2 MUST Gate

After all steps succeed, the `MUST` expression is evaluated against the full step-output environment.  If it fails, the agent status is `failed` and no RESULT is emitted.  This is the **agent-level evidence gate**.

### 4.3 RESULT

The `RESULT` expression is evaluated last and its value becomes the agent's output (stored in the calling agent's environment under the STEP name that invoked this agent).

---

## 5. Template, MAKE, and GROUP

### 5.1 TEMPLATE

```
TEMPLATE FetchAgent(url, columns)
IN url, columns
OUT rows
TOOLS http.get, extract.table
MUST count(rows.rows) >= 1

STEP page = TOOL http.get(url=url)
CHECK page.status == 200
ONFAIL stop

STEP rows = TOOL extract.table(text=page.text, columns=columns)
RESULT rows
```

Parameters (`url`, `columns`) are substituted when the template is instantiated.  Non-literal argument references become `ExprRef` instances resolved at expansion time (for template params) or runtime (for step output refs).

### 5.2 MAKE

```
MAKE FetchAgent(url="https://example.com/pricing", columns=["plan","price"]) AS PricingFetcher
```

Instantiates `FetchAgent` with the given bindings, producing a concrete `AgentDef` named `PricingFetcher`.

### 5.3 GROUP

```
GROUP AllCheckers = FOR site IN sites : MAKE FetchAgent(url=site.url, columns=["plan","price"])
```

* `sites` is resolved from the `--vars` JSON file as a list.
* For each item in the list, a concrete agent is created named `AllCheckers_<item.name>` (or `AllCheckers_<i>` if no `name` field).
* All generated agents are registered and callable from STEP definitions.

---

## 6. Evidence Language

### 6.1 Supported Constructs

| Construct          | Example                                      |
|--------------------|----------------------------------------------|
| Literals           | `200`, `"text"`, `True`, `None`              |
| Variable lookup    | `page`, `rows`                               |
| Attribute access   | `page.status`, `result.rows[0].plan`         |
| Subscript          | `items[0]`, `rows["plan"]`                   |
| Comparison         | `==  !=  <  <=  >  >=  in  not in`          |
| Boolean ops        | `and  or  not`                               |
| `has(obj, key)`    | `has(rows[0], "price")`                      |
| `len(x)` / `count(x)` | `count(rows.rows) >= 3`                  |
| `matches(text, re)`| `matches(page.text, "\\d{3}")`               |
| `all(list, field)` | `all(rows, "price")` – every item has field  |
| `any(list, field)` | `any(rows, "price")` – some item has field   |
| List literals      | `["plan", "price"]`                          |

### 6.2 Security

Expressions are parsed with Python's `ast` module.  Only the listed node types are allowed.  `eval()` is never called.  Unknown function names raise `EvidenceError`.

### 6.3 Fail Closed

If any CHECK expression raises an error (undefined variable, type mismatch, unknown function), the check result is `False` and the configured ONFAIL policy is applied.  **No expression failure silently passes.**

---

## 7. ONFAIL Policies

| Policy             | Behaviour                                                        |
|--------------------|------------------------------------------------------------------|
| `retry`            | Re-execute the same step; bounded by `LIMIT retries=N`          |
| `fallback <name>`  | Jump to the named step (must be defined in the same agent)       |
| `askhuman`         | Halt with `status=needs_human`; human intervention required      |
| `stop`             | Halt with `status=failed`                                        |

If no ONFAIL is defined and CHECK fails, the runtime **fails closed** (`stop` semantics).

---

## 8. Determinism Rules

1. **Step ordering**: steps execute in declaration order; no parallelism.
2. **Retry bound**: maximum `LIMIT.retries` attempts (default 2) per step.
3. **Fallback resolution**: fallback target is resolved by name at parse time; missing name → `failed`.
4. **Caching**: before every tool call, the runtime computes
   `cache_key = SHA-256(tool_name + normalized_args + tool_version)`.
   If a cache file exists, the cached result is returned and `cache_hit=true` is recorded.  Same inputs always produce the same output across runs.
5. **Arg normalisation**: dict keys are sorted; numbers, booleans, nulls are normalised before hashing.
6. **No global mutable state**: each agent invocation receives a fresh local environment (copy of vars + inputs).

---

## 9. Caching Rules

* Cache directory: `.ecl_cache/` (configurable via `--cache-dir`).
* Cache files: `<sha256_hex>.json`.
* Key computation: `SHA-256(json({"tool": name, "args": normalised_args, "version": version}))`.
* Cache is populated on first tool call; subsequent identical calls are served from cache.
* `--no-cache` flag uses a temporary directory (discarded after run).
* `ecl cache clear` deletes all cache entries.

---

## 10. Permission Allowlist

* `ALLOW` declares the set of tools permitted for the entire program.
* If a STEP attempts to call a tool not in the allowlist, execution halts immediately with `status=failed` and `PermissionError` logged.
* Per-agent `TOOLS` clause provides an informational declaration (used in receipts); it does **not** override the program-level `ALLOW`.

---

## 11. Receipt JSON Schema

```json
{
  "ecl_version": "0.1.0",
  "timestamp": "<ISO-8601>",
  "status": "success | failed | needs_human",
  "intent": "<string>",
  "policy": {
    "allow": ["tool1", "tool2"],
    "limit": {
      "time_s": 60.0,
      "calls": 20,
      "retries": 2
    }
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
          "started_at": "<ISO-8601>",
          "ended_at": "<ISO-8601>",
          "duration_ms": 12,
          "tool_calls": [
            {"tool": "http.get", "cache_hit": false, "duration_ms": 8}
          ],
          "error": null
        }
      ]
    }
  ],
  "error": null
}
```

### 11.1 Output hashing

Tool outputs are hashed with SHA-256 and stored as a 16-character hex prefix in `output_hash`. The full output is never stored in the receipt; only a 200-character preview is included.

---

## 12. Built-in Tools

All tools are pure Python callables in `ecl/tools/`.

| Tool             | Signature                                        | Returns                                              |
|------------------|--------------------------------------------------|------------------------------------------------------|
| `http.get`       | `url: str`                                       | `{status: int, text: str, url: str}`                |
| `extract.table`  | `text: str, columns: list[str] \| None`          | `{rows: [{col: val}]}`                              |
| `llm.generate`   | `prompt: str, data=None, format="text\|json"`    | `{text: str}` or `{rows: [...], ...}`               |
| `sql.query`      | `query: str`                                     | `{rows: [{col: val}], row_count: int}`              |
| `pdf.render`     | `template: str, data=None`                       | `{file_id: str, page_count: int, ...}`              |
| `email.draft`    | `to, subject, body, attachments=[]`              | `{draft_id: str, status: str, ...}`                 |

### 12.1 `llm.generate` determinism

The mock LLM uses `SHA-256(prompt + data + format)` as a seed for a `random.Random` instance.  Identical inputs always produce identical outputs.  When caching is active, the LLM is not called at all on cache hits.

---

## 13. Error Handling Summary

| Situation                              | Outcome                                |
|----------------------------------------|----------------------------------------|
| Tool not in ALLOW                      | `PermissionError`, step fails          |
| CHECK expression raises error          | Check = False, ONFAIL applied          |
| CHECK fails, no ONFAIL                 | Fail closed (stop)                     |
| Retry limit exceeded                   | `status=failed`                        |
| Fallback step not defined              | `status=failed`                        |
| MUST expression fails                  | `status=failed`, RESULT not emitted    |
| Parse error in source file             | Receipt emitted with `status=failed`   |
| Time/call budget exceeded              | `ExecutionError`, `status=failed`      |
| Agent call depth > 10                  | `ExecutionError`, `status=failed`      |

---

## 14. Execution Model Diagram

```
parse(source)
     │
     ▼
expand_templates()          ← replaces MAKE/GROUP with AgentDef
     │
     ▼
register all AgentDefs
     │
     ▼
run entry agent (first AGENT)
  │
  ├─ for each STEP in order:
  │    ├─ resolve ExprRef args against local_env
  │    ├─ assert ALLOW permission
  │    ├─ compute cache key → cache hit? return cached
  │    ├─ invoke tool / sub-agent
  │    ├─ store output in local_env[step.name]
  │    ├─ evaluate CHECK → bool
  │    └─ if fail: apply ONFAIL policy (retry/fallback/askhuman/stop)
  │
  ├─ evaluate MUST expression
  └─ evaluate RESULT expression
       │
       ▼
  emit receipt.json
```
