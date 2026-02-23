# Demo Recording Scripts (Launch)

## General Pattern (show this every time)
1. Type a natural-language request
2. Show the answer
3. Open generated ACL contract (briefly)
4. Show receipt summary
5. Open full receipt JSON (briefly)

## Clip 1 — Calendar (relatable)
Mode: `Calendar`

Prompt:
- `do i have any meetings?`

Then:
- `can you reschedule it to 28 feb at 4 pm ?`

What to emphasize:
- natural language -> structured action flow
- receipt makes the behavior inspectable

## Clip 2 — Splitwise-style ambiguity handling
Mode: `Splitwise`

Prompt:
- `Dinner with Sarah - 20 USD each`

What to emphasize:
- API access is not enough
- AI should clarify before a write when details are missing
- receipt shows checks and tool calls

## Clip 3 — Zapier bridge (preview-first)
Mode: `Zapier Calendar`

Prompt:
- `Schedule lunch with Ali tomorrow at 1pm at home`

What to emphasize:
- `zapier.invoke` runs in preview mode
- receipt JSON shows exact payload
- ACL adds control on top of Zapier breadth

## Clip 4 — Zapier execute path (optional)
Mode: `Zapier Calendar`

Prompt:
- `Schedule dentist appointment on Friday at 4pm (approved)`

What to emphasize:
- safe execute path
- if webhook not configured, receipt still shows transparent `not_configured` status (good for trust)
