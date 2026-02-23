# Agent-Ready Apps Roadmap (ACL)

## Summary
ACL's role is not to replace apps. ACL is the safety and execution contract layer that lets apps safely accept actions from AI agents.

A useful mental model:

- APIs / MCP / Zapier / CLI = capability access
- LLM = intent understanding
- ACL = control (rules, checks, receipts)
- Chat/voice/UI = interface

In short: APIs give access. ACL gives control.

## Problem
Most products are not agent-ready today.

What exists:
- human UI flows
- APIs designed for developers, not autonomous agents
- ad hoc prompt glue for multi-step automation

What is missing:
- safe, inspectable action workflows for AI
- standard capability metadata (read/write, idempotency, approval requirements)
- consistent receipts and replay for support/debugging

## What ACL Proves Today
ACL already demonstrates:
- declarative agent workflows (`AGENT`, `STEP`, `CHECK`, `MUST`, `ONFAIL`)
- tool allowlists and policy constraints (`ALLOW`, `LIMIT`)
- receipts (what ran, what passed/failed, timings, outputs)
- serve mode (`acl serve`) and scheduled/remote execution
- natural language -> ACL -> action via `/agent` and `/agenticflow`
- preview-first patterns (e.g. `zapier.invoke` preview/execute)

## Target Architecture (Agent-Ready App Stack)

### 1. Capability layer (owned by app or integrator)
Expose app actions as machine-usable tools.

Examples:
- `splitwise.create_expense`
- `monarch.create_transaction`
- `shopify.refund_order`
- `calendar.create_event`
- `order.place`

How capabilities may be exposed:
- native app API wrappers (best)
- MCP adapters
- Zapier bridge (fast breadth)
- CLI wrappers
- browser automation (fallback only)

### 2. Safety contract layer (ACL)
Define how an agent may use those tools:
- allowed tools
- step order
- evidence checks (`CHECK`)
- hard gates (`MUST`)
- retries/fallbacks (`ONFAIL`)
- receipts

### 3. Interface layer
Any interface can sit on top:
- app-native chat
- voice assistant
- support agent console
- external personal agent
- scheduled automation

## Near-Term Product Primitives (What ACL Should Add)

### A. Tool manifest spec (high priority)
A standard way for apps/integrators to publish capabilities.

Manifest fields should include:
- tool name
- input schema
- output schema
- side effect level (`read`, `write`, `external`)
- idempotency information
- retry safety
- approval requirement (default policy)
- auth scopes needed
- human description for LLM/tool discovery

Why it matters:
- makes apps easier to integrate
- reduces prompt bloat
- enables static validation and better UX

### B. Adapter SDKs (high priority)
Fast path to wrap existing app APIs as ACL tools.

Targets:
- Go SDK (native)
- Python SDK (popular integrator path)
- MCP adapter helpers
- OpenAPI -> tool wrapper generator (stretch)

### C. Approval/confirmation primitive (high priority)
Preview-first exists as a pattern today. It should become a first-class ACL concept.

Examples:
- `APPROVAL required`
- policy-level approval rules for write actions
- explicit receipt entries for approval requests/decisions

### D. Intent compiler path (high priority)
Production-grade path for common flows should avoid free-writing ACL each request.

Preferred split:
- LLM -> structured intent/slots (JSON)
- compiler/template -> ACL
- ACL runtime -> execution + receipts

Keep freeform NL -> ACL generation as a fallback for exploratory workflows.

### E. Replay/simulation (medium priority)
Support trustworthy adoption:
- dry-run mode
- mocked tool responses
- replay from receipt
- golden test receipts

## Reliability Principles
ACL should optimize for:
- safe failure over silent failure
- transparent execution over hidden automation
- predictable behavior over prompt cleverness

Practical rules:
- preview before risky writes
- require confirmation for money/orders/refunds/payments
- keep receipts for every run
- use templates/compilers for common actions
- use freeform generation as fallback, not default

## Ecosystem Strategy

### Zapier (breadth)
Best for quickly demonstrating ACL across many apps.

- Zapier gives reach across thousands of apps
- ACL adds preview/confirm/receipt safety
- good wedge for builders and founders

### Native integrations (depth)
For serious production use cases (finance, orders, logistics), native tools remain best for:
- stronger schemas
- lower latency
- better idempotency
- better auth and permission control

### MCP (interoperability)
ACL should remain compatible with MCP-style tool ecosystems while preserving ACL's safety/receipt model.

## Example Integration Patterns

### Consumer finance (Monarch/Splitwise)
- parse intent from chat
- clarify missing facts
- preview/confirm writes
- create transaction/expense
- show receipt

### Support/ops (refunds)
- fetch order context
- validate policy eligibility
- draft comms
- require approval
- execute refund
- receipt for support/debugging

### Food ordering / delivery apps
- app exposes cart/menu/order capabilities
- ACL handles search/customization/quote/confirm/order flow
- receipt gives trust and debugging visibility

## What This Release Is (and is not)
This release proves:
- ACL as a safe action layer concept
- multi-domain demos in `/agenticflow`
- receipt transparency (including full JSON)
- Zapier bridge MVP (`zapier.invoke`)

This release does not yet solve:
- formal manifest standard
- built-in approval primitive
- compiler-first production path for all common intents
- end-to-end native integrations for every app

## Suggested Next Milestones
1. Tool manifest spec + validator
2. Approval primitive + UI support
3. Intent JSON -> ACL compiler for common actions
4. OpenAPI/MCP adapter tooling
5. Replay/simulation toolkit
