"""
extract.table  –  mock table extractor.

Parses whitespace-aligned tabular text into a list of row dicts.  If the
text contains a recognisable header row the columns are used automatically;
otherwise the *columns* argument is used as the column list.
"""

from __future__ import annotations

import re


def extract_table(text: str, columns: list[str] | None = None) -> dict:
    """
    Extract a table from *text*.

    Returns: {rows: [{col: value, ...}, ...]}

    Strategy:
      1. Split lines; skip blank lines and header separators.
      2. Detect header row (first non-blank line with multiple words).
      3. Parse subsequent lines as data rows.
      4. If *columns* is given, keep/rename only those columns.
    """
    lines = [l for l in text.splitlines() if l.strip()]
    # Remove separator lines (----, ====)
    lines = [l for l in lines if not re.match(r'^[-=\s]+$', l)]

    if not lines:
        return {"rows": []}

    # Detect header – first line
    raw_header = _split_row(lines[0])
    header = [h.strip().lower() for h in raw_header]

    # Data rows
    rows: list[dict] = []
    for line in lines[1:]:
        cells = _split_row(line)
        if len(cells) < 2:
            continue
        row: dict[str, str] = {}
        for i, h in enumerate(header):
            row[h] = cells[i].strip() if i < len(cells) else ""
        rows.append(row)

    # Filter / rename columns if requested
    if columns:
        cols_lower = [c.lower() for c in columns]
        filtered = []
        for row in rows:
            new_row: dict[str, str] = {}
            for orig_col, val in row.items():
                # Match by substring so "price" matches "$9.99/mo price"
                for requested in cols_lower:
                    if requested in orig_col or orig_col in requested:
                        new_row[requested] = val
                        break
            if new_row:
                filtered.append(new_row)
        rows = filtered

    # Ensure every row has at least the requested columns (fill empty if missing)
    if columns:
        for row in rows:
            for col in [c.lower() for c in columns]:
                row.setdefault(col, "")

    return {"rows": rows}


def _split_row(line: str) -> list[str]:
    """Split a line on 2+ spaces or tabs (whitespace-aligned tables)."""
    parts = re.split(r'\t|  +', line.strip())
    return [p.strip() for p in parts if p.strip()]
