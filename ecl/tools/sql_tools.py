"""
sql.query  –  in-memory SQLite with a demo dataset.

The database is initialised once per process with several tables so that
multiple example files can exercise different queries without external deps.

Demo tables
-----------
  sales(id, product, amount, date, region)
  incidents(id, time, severity, service, message)
  pricing(id, plan, price_cents, users_limit, support_level)
  agents_registry(id, name, category, url, active)
"""

from __future__ import annotations

import sqlite3
import threading

_lock = threading.Lock()
_conn: sqlite3.Connection | None = None

_SCHEMA_AND_SEED = """
CREATE TABLE IF NOT EXISTS sales (
    id      INTEGER PRIMARY KEY,
    product TEXT    NOT NULL,
    amount  REAL    NOT NULL,
    date    TEXT    NOT NULL,
    region  TEXT    NOT NULL
);
INSERT OR IGNORE INTO sales VALUES
  (1,  'Widget A', 1200.00, '2024-01-10', 'North'),
  (2,  'Widget B',  850.50, '2024-01-11', 'South'),
  (3,  'Gadget X', 2300.00, '2024-01-12', 'East'),
  (4,  'Gadget Y', 1750.25, '2024-01-13', 'West'),
  (5,  'Widget A', 1400.00, '2024-01-14', 'North'),
  (6,  'Gadget X', 2100.00, '2024-01-15', 'South'),
  (7,  'Widget B',  980.00, '2024-01-16', 'East'),
  (8,  'Gadget Y', 1600.00, '2024-01-17', 'West'),
  (9,  'Widget A', 1550.00, '2024-01-18', 'North'),
  (10, 'Gadget X', 2450.00, '2024-01-19', 'East');

CREATE TABLE IF NOT EXISTS incidents (
    id       INTEGER PRIMARY KEY,
    time     TEXT NOT NULL,
    severity TEXT NOT NULL,
    service  TEXT NOT NULL,
    message  TEXT NOT NULL
);
INSERT OR IGNORE INTO incidents VALUES
  (1, '09:14', 'ERROR', 'auth-svc', 'JWT validation timeout (>5s)'),
  (2, '09:15', 'WARN',  'api-gw',   'Rate limit hit 950/1000 rps'),
  (3, '09:17', 'ERROR', 'db-pool',  'Connection pool exhausted (100/100)'),
  (4, '09:22', 'INFO',  'auth-svc', 'Auto-scaled from 3 to 8 replicas'),
  (5, '09:28', 'INFO',  'db-pool',  'Pool size increased to 200'),
  (6, '09:35', 'INFO',  'system',   'All services nominal');

CREATE TABLE IF NOT EXISTS pricing (
    id            INTEGER PRIMARY KEY,
    plan          TEXT NOT NULL,
    price_cents   INTEGER NOT NULL,
    users_limit   TEXT NOT NULL,
    support_level TEXT NOT NULL
);
INSERT OR IGNORE INTO pricing VALUES
  (1, 'Basic',      999,   '5',         'Email'),
  (2, 'Pro',        4999,  '50',        'Priority'),
  (3, 'Team',       9999,  '200',       '24/7 Phone'),
  (4, 'Enterprise', 29999, 'unlimited', 'Dedicated');

CREATE TABLE IF NOT EXISTS agents_registry (
    id       INTEGER PRIMARY KEY,
    name     TEXT NOT NULL,
    category TEXT NOT NULL,
    url      TEXT NOT NULL,
    active   INTEGER NOT NULL DEFAULT 1
);
INSERT OR IGNORE INTO agents_registry VALUES
  (1,  'PriceWatcher',    'pricing',  'https://example.com/pricing',  1),
  (2,  'ReportBuilder',   'reports',  'https://example.com/report',   1),
  (3,  'IncidentTracker', 'ops',      'https://example.com/incident', 1);
"""


def _get_conn() -> sqlite3.Connection:
    global _conn
    with _lock:
        if _conn is None:
            _conn = sqlite3.connect(":memory:", check_same_thread=False)
            _conn.row_factory = sqlite3.Row
            _conn.executescript(_SCHEMA_AND_SEED)
        return _conn


def sql_query(query: str) -> dict:
    """
    Execute *query* against the in-memory demo database.

    Returns: {rows: [{col: val, ...}, ...], row_count: int}
    """
    conn = _get_conn()
    try:
        cur = conn.execute(query)
        rows = [dict(r) for r in cur.fetchall()]
        return {"rows": rows, "row_count": len(rows)}
    except sqlite3.Error as exc:
        return {"rows": [], "row_count": 0, "error": str(exc)}
