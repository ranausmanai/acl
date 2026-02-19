"""
http.get  –  deterministic mock HTTP client.

Returns canned pricing/report content based on URL path so downstream
tools (extract.table, llm.generate) always have something to work with.
"""

from __future__ import annotations

import hashlib

# Canned page bodies keyed by URL fragment patterns
_PAGES: dict[str, dict] = {
    "pricing": {
        "status": 200,
        "text": (
            "Pricing Plans\n\n"
            "Plan    Price       Users   Support\n"
            "Basic   $9.99/mo   5       Email\n"
            "Pro     $49.99/mo  50      Priority\n"
            "Team    $99.99/mo  200     24/7 Phone\n"
            "Ent     $299.99/mo unlimited Dedicated\n"
        ),
    },
    "report": {
        "status": 200,
        "text": (
            "Monthly Sales Report – January 2024\n\n"
            "Region   Revenue    Units   Growth\n"
            "North    $128,400   342     +12%\n"
            "South    $98,200    281     +7%\n"
            "East     $143,500   398     +18%\n"
            "West     $112,800   305     +9%\n"
            "Total    $482,900   1326    +11.5%\n"
        ),
    },
    "incident": {
        "status": 200,
        "text": (
            "Incident Log 2024-01-15\n\n"
            "Time     Severity  Service   Message\n"
            "09:14    ERROR     auth-svc  JWT validation timeout (>5s)\n"
            "09:15    WARN      api-gw    Rate limit hit 950/1000 rps\n"
            "09:17    ERROR     db-pool   Connection pool exhausted (100/100)\n"
            "09:22    INFO      auth-svc  Auto-scaled from 3 to 8 replicas\n"
            "09:28    INFO      db-pool   Pool size increased to 200\n"
            "09:35    INFO      system    All services nominal\n"
        ),
    },
    "404": {
        "status": 404,
        "text": "Not Found",
    },
}

_DEFAULT_PAGE = {
    "status": 200,
    "text": (
        "Product Catalog\n\n"
        "Item     Price    Stock   Category\n"
        "Widget   $14.99   250     Hardware\n"
        "Gadget   $29.99   180     Electronics\n"
        "Doohick  $7.49    500     Accessories\n"
    ),
}


def http_get(url: str) -> dict:
    """
    Simulated HTTP GET.

    Returns: {status: int, text: str, url: str}
    """
    url_lower = url.lower()
    for key, page in _PAGES.items():
        if key in url_lower:
            return {"url": url, **page}
    # Deterministic fallback based on URL hash
    seed = int(hashlib.md5(url.encode()).hexdigest()[:4], 16) % len(_PAGES)
    page = list(_PAGES.values())[seed]
    return {"url": url, **page}
