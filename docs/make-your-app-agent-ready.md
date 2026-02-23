# Make Your App Agent-Ready (Practical Guide)

ACL does not replace your app. It helps your app's capabilities become safer for AI agents to use.

Use this guide when you want to go from:

- "My app has APIs"
- to "An AI assistant can use them safely, with receipts"

## What "agent-ready" means

An agent-ready app has:

1. **Machine-usable capabilities** (API / MCP / Zapier / CLI)
2. **A safe execution contract** (ACL flow with checks and limits)
3. **A confirmation UX for risky writes**
4. **Receipts** so support/debugging can see what happened

In short:

- APIs give access
- ACL gives control

## The minimal recipe (today)

Start small. You do not need a platform migration.

1. Expose **2-3 actions as tools**
2. Mark which ones are **read** vs **write**
3. Add a **preview/confirm** pattern for writes
4. Define **1 ACL flow**
5. Run it and inspect the **receipt**

Good first examples:

- `calendar.find_event` (read)
- `calendar.create_event` preview/execute (write)
- `support.get_order` + `support.refund_order` (read + write)

## Integration paths (pick the easiest one first)

### 1) Native API wrappers (best long-term)

Wrap your app's APIs as tools directly.

Pros:
- strongest schemas
- best performance
- best control over auth + permissions

### 2) Zapier bridge (fastest breadth)

Use ACL's `zapier.invoke` to reach many apps quickly.

Pros:
- fast demos and broad compatibility
- great for proving the pattern

Tradeoff:
- less control than native integrations for high-volume or high-trust flows

See: `docs/zapier-bridge.md`

### 3) MCP adapters

If your app or tooling stack already exposes MCP tools, ACL can sit above them as the receipt/checks layer.

### 4) CLI wrappers

Great for internal tools and ops automation where CLIs already exist.

### 5) Browser automation (fallback)

Use only when nothing else exists. It works for prototypes but is fragile for production.

## Tool design checklist (important)

For each tool, define:

- stable tool name (`myapp.lookup_order`, `myapp.refund_order`)
- input fields (required vs optional)
- output shape (consistent keys)
- side effects (`read` vs `write`)
- idempotency behavior (can retry safely?)
- preview support (can we simulate before execute?)

This is what makes ACL flows easier to write and safer to run.

## Recommended flow pattern for writes

For money, orders, refunds, and external side effects:

1. **Read context first**
2. **Validate eligibility/constraints**
3. **Preview**
4. **Ask for confirmation in UI**
5. **Execute**
6. **Emit receipt**

ACL can express the workflow; your app UX should make the confirm step explicit.

## Example: Food ordering app (conceptual)

Capabilities as tools:

- `menu.search`
- `cart.add_item`
- `cart.update_item`
- `checkout.quote`
- `order.place`

ACL flow (pattern):

1. search menu
2. build cart
3. quote total + ETA
4. preview order placement
5. confirm in UI
6. place order
7. return summary + receipt

## Example: Support refund flow (practical)

Capabilities:

- `support.get_order`
- `support.refund_order`
- `support.send_email`

Flow:

1. fetch order
2. validate eligibility
3. preview/prepare
4. confirm
5. execute refund
6. send email
7. inspect receipt

## Recommended production architecture (important)

### Today (good for demos / fallback)

- Natural language -> prompt-grounded ACL generation -> execute -> receipt

### Production path (recommended)

- **LLM -> structured intent/slots (JSON)**
- **Compiler/templates -> ACL**
- **ACL runtime -> execution + receipts**

Keep freeform ACL generation as a fallback for novel requests.

Why this matters:

- lower token cost
- fewer syntax failures
- stronger guarantees
- easier approval UX

## JSON -> ACL example (conceptual)

Structured intent:

```json
{
  "intent": "calendar_create_event",
  "title": "Lunch with Ali",
  "starts_at": "2026-02-24T13:00:00",
  "location": "Home",
  "approval_required": true
}
```

Compiled ACL (preview variant):

```acl
AGENT CalendarCreatePreview
  OUT answer
  TOOLS zapier.invoke, llm.generate

  STEP z = TOOL zapier.invoke(
    action="calendar.create_event",
    mode="preview",
    approved=false,
    title="Lunch with Ali",
    starts_at="2026-02-24T13:00:00",
    location="Home")
  MUST z.status == "preview"

  STEP answer = TOOL llm.generate(prompt="Confirm what would be created using {z.preview} and ask for approval.")
  MUST has(answer, "text")
  RESULT answer
END
```

## Common mistakes to avoid

- Letting AI write directly when the request is ambiguous
- No confirmation step for high-risk actions
- No idempotency strategy for retries
- No receipts (hard to debug and hard to trust)
- Letting freeform ACL generation be the only production path

## Start here in this repo

- `/quickstart` — builder onboarding path
- `/agenticflow` — guided tutorial + demo lab
- `/playground` — prototype ACL directly
- `docs/zapier-bridge.md` — Zapier bridge MVP
- `docs/agent-ready-apps-roadmap.md` — broader architecture and roadmap
