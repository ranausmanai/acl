# LinkedIn Launch Post (Draft)

I have been thinking a lot about what it means for apps to become truly AI-native.

Most products today are still designed around human UI flows. Even when an API exists, teams still end up writing ad hoc prompt glue if they want AI agents to take actions safely.

That is what I built **ACL (Agent Contract Language)** for.

ACL is an open-source language + runtime for **safe AI actions**.
It helps connect:
- app capabilities (APIs / MCP / Zapier / CLI)
- AI interfaces (chat / voice / assistant)
- a safety model (checks, constraints, receipts)

In simple terms:
**APIs give access. ACL gives control.**

What ACL adds:
- explicit allowed actions
- step-by-step checks
- retries/fallbacks
- preview/confirm patterns for risky writes
- receipts (including full execution JSON) for transparency and debugging

I also built a live demo lab to show this pattern across multiple use cases:
- Calendar
- Splitwise-style expense split
- Support refund flow
- Monarch-style finance assistant
- Zapier bridge MVP (preview/confirm/execute with receipts)

Live:
- https://acl.fyi/agenticflow
- https://acl.fyi/agent (Monarch demo)
- https://github.com/ranausmanai/acl

If you are building an app/API and thinking about how to make it agent-ready, I would love to hear what workflow you would want an AI agent to handle first.
