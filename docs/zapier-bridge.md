# ACL x Zapier Bridge (MVP)

## What it is
`zapier.invoke` is a built-in ACL tool that lets ACL call Zapier webhooks with a preview/execute pattern.

Why this matters:
- Zapier gives access to many apps
- ACL adds safety (preview-first), checks, and receipts

If you're new to ACL, start with:
- `https://acl.fyi/quickstart` (builder onboarding)
- `docs/make-your-app-agent-ready.md` (practical integration guide)

## Tool
- `zapier.invoke`

## Arguments
Required:
- `action` (string), for example `calendar.create_event`

Optional:
- `mode` (`preview` or `execute`) — defaults to `preview`
- `approved` (bool/string) — required for safe execute flows
- `webhook_url` — explicit webhook override
- any additional fields become the webhook payload

## Return shape (summary)
`zapier.invoke` returns fields such as:
- `status` (`preview`, `accepted`, `approval_required`, `not_configured`, `http_error`)
- `mode`
- `action`
- `approved`
- `configured`
- `request_id`
- `payload`
- `preview`
- `response_text`
- `http_status` (execute path)

It also aliases common payload fields (for example `title`, `starts_at`) to top-level keys to make ACL interpolation easier.

## Environment Variables
Per-action webhook (recommended):
- `ACL_ZAPIER_WEBHOOK_<ACTION>`

Example:
- action: `calendar.create_event`
- env var: `ACL_ZAPIER_WEBHOOK_CALENDAR_CREATE_EVENT`

Fallback generic webhook (less strict):
- `ACL_ZAPIER_WEBHOOK_URL`

## Example: Preview-first calendar event
```acl
AGENT SchedulePreview
  OUT answer
  TOOLS zapier.invoke, llm.generate

  STEP z = TOOL zapier.invoke(
    action="calendar.create_event",
    mode="preview",
    approved=false,
    title="Lunch with Ali",
    starts_at="2026-02-23T13:00:00",
    location="Home")
  MUST z.status == "preview"

  STEP answer = TOOL llm.generate(
    prompt="Using this preview payload: {z.preview}, confirm what would be created and ask for confirmation.")
  MUST has(answer, "text")
  RESULT answer
END
```

## Example: Execute after explicit approval
```acl
AGENT ScheduleExecute
  OUT answer
  TOOLS zapier.invoke, llm.generate

  STEP z = TOOL zapier.invoke(
    action="calendar.create_event",
    mode="execute",
    approved=true,
    title="Lunch with Ali",
    starts_at="2026-02-23T13:00:00",
    location="Home")
  MUST (z.status == "accepted") or (z.status == "not_configured")

  STEP answer = TOOL llm.generate(
    prompt="Summarize the result of calendar.create_event. Status: {z.status}. Message: {z.response_text}.")
  MUST has(answer, "text")
  RESULT answer
END
```

## Zapier Setup (Google Calendar example)
1. Create a Zap in Zapier.
2. Trigger: `Webhooks by Zapier` -> `Catch Hook`.
3. Action: `Google Calendar` -> `Create Detailed Event` (or `Create Event`).
4. Copy the webhook URL.
5. Set `ACL_ZAPIER_WEBHOOK_CALENDAR_CREATE_EVENT=<your_hook>` in the environment running `acl serve`.
6. Restart the ACL server.

## Testing with /agenticflow
Use the `Zapier Calendar` mode in `https://acl.fyi/agenticflow`.

- Preview example:
  - `Schedule lunch with Ali tomorrow at 1pm at home`
- Execute example:
  - `Schedule dentist appointment on Friday at 4pm (approved)`

Even when no webhook is configured, the receipt still proves the ACL flow and shows the payload that would be sent.
