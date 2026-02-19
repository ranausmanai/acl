"""
llm.generate  –  deterministic mock LLM.

The output is seeded from SHA-256(prompt + str(data) + format) so that
identical inputs always produce identical outputs (cache-friendly, testable).

format="text"  → {text: str}
format="json"  → {rows: [...]} or {summary: str, ...} depending on prompt
"""

from __future__ import annotations

import hashlib
import json
import random


# ---------------------------------------------------------------------------
# Canned response templates for common prompt patterns
# ---------------------------------------------------------------------------

_JSON_TEMPLATES = {
    "pricing": {
        "rows": [
            {"plan": "Basic", "price": "$9.99/mo", "users": "5"},
            {"plan": "Pro", "price": "$49.99/mo", "users": "50"},
            {"plan": "Team", "price": "$99.99/mo", "users": "200"},
            {"plan": "Enterprise", "price": "$299.99/mo", "users": "unlimited"},
        ]
    },
    "report": {
        "summary": "Monthly revenue increased 11.5% YoY. North region led growth.",
        "regions": ["North", "South", "East", "West"],
        "total_revenue": 482900,
        "top_region": "East",
    },
    "incident": {
        "root_cause": "Database connection pool exhaustion triggered auth service timeouts.",
        "severity": "P1",
        "duration_minutes": 21,
        "affected_services": ["auth-svc", "api-gw", "db-pool"],
        "resolution": "Auto-scaled replicas and increased pool size.",
        "rows": [
            {"time": "09:14", "severity": "ERROR", "service": "auth-svc", "message": "JWT timeout"},
            {"time": "09:17", "severity": "ERROR", "service": "db-pool",  "message": "Pool exhausted"},
            {"time": "09:35", "severity": "INFO",  "service": "system",   "message": "All nominal"},
        ],
    },
    "debug": {
        "root_cause": "Connection pool exhaustion.",
        "severity": "P1",
        "affected_services": ["auth-svc", "db-pool"],
        "recommended_fix": "Increase pool size; add circuit breaker.",
        "rows": [
            {"time": "09:14", "severity": "ERROR", "service": "auth-svc"},
            {"time": "09:17", "severity": "ERROR", "service": "db-pool"},
        ],
    },
    "extract": {
        "rows": [
            {"plan": "Basic", "price": "$9.99/mo"},
            {"plan": "Pro", "price": "$49.99/mo"},
            {"plan": "Enterprise", "price": "$299.99/mo"},
        ]
    },
    "summary": {
        "text": "Analysis complete. Three pricing tiers identified with competitive rates.",
        "key_points": [
            "Basic tier suitable for small teams",
            "Enterprise tier includes dedicated support",
        ],
    },
}

_TEXT_TEMPLATES = {
    "pricing": (
        "Pricing analysis: The vendor offers four tiers ranging from $9.99/mo (Basic, 5 users) "
        "to $299.99/mo (Enterprise, unlimited users). The Pro tier at $49.99/mo offers the best "
        "value for mid-sized teams. Enterprise includes 24/7 dedicated support."
    ),
    "report": (
        "Monthly report summary: Total revenue of $482,900 across four regions with 11.5% overall "
        "growth. East region led with 18% growth. All regions showed positive momentum."
    ),
    "incident": (
        "Incident post-mortem: A 21-minute P1 outage on 2024-01-15 caused by database connection "
        "pool exhaustion. Auth service timeouts cascaded from pool saturation. Auto-scaling and "
        "pool size increase restored service at 09:35."
    ),
    "brief": (
        "Executive brief: Competitive pricing analysis complete. Recommend Pro tier for current "
        "team size. Negotiate Enterprise SLA terms before contract renewal."
    ),
}


def llm_generate(
    prompt: str,
    data: str | dict | None = None,
    format: str = "text",  # noqa: A002
) -> dict:
    """
    Deterministic mock LLM.

    Returns: {text: str}  or  {rows: [...], ...}  based on *format*.
    """
    seed_raw = f"{prompt}|{json.dumps(data, default=str) if data else ''}|{format}"
    seed = int(hashlib.sha256(seed_raw.encode()).hexdigest()[:8], 16)
    rng = random.Random(seed)

    prompt_lower = prompt.lower()

    if format == "json":
        # Pick the best matching template
        for key, template in _JSON_TEMPLATES.items():
            if key in prompt_lower:
                return dict(template)  # shallow copy
        # Generic JSON fallback: deterministic rows from seed
        num_rows = rng.randint(3, 5)
        plans = ["Starter", "Growth", "Scale", "Business", "Ultimate"]
        prices = [9, 19, 49, 99, 199, 299, 499]
        rows = []
        for i in range(num_rows):
            rows.append({
                "plan": plans[i % len(plans)],
                "price": f"${rng.choice(prices)}/mo",
                "users": str(rng.choice([5, 10, 50, 100, 500])),
            })
        return {"rows": rows}

    else:  # text
        for key, template in _TEXT_TEMPLATES.items():
            if key in prompt_lower:
                return {"text": template}
        # Generic fallback
        words = [
            "analysis", "report", "summary", "data", "insights", "metrics",
            "growth", "revenue", "performance", "evaluation",
        ]
        sentence = " ".join(rng.choices(words, k=20)).capitalize() + "."
        return {"text": sentence}
