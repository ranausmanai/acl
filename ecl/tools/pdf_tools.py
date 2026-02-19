"""
pdf.render  –  mock PDF renderer.

Returns a synthetic file ID and page count based on the template name and
data size.  No actual PDF is produced.
"""

from __future__ import annotations

import hashlib
import json
import math


def pdf_render(template: str, data: dict | list | str | None = None) -> dict:
    """
    Mock PDF render.

    Returns: {file_id: str, page_count: int, template: str, size_kb: int}
    """
    data_str = json.dumps(data, default=str) if data is not None else ""
    raw = f"{template}:{data_str}"
    file_id = "pdf_" + hashlib.md5(raw.encode()).hexdigest()[:12]

    # Estimate page count from data size
    num_items = len(data) if isinstance(data, (list, dict)) else 1
    page_count = max(1, math.ceil(num_items / 20))

    size_kb = max(10, num_items * 2 + len(template) // 10)

    return {
        "file_id": file_id,
        "page_count": page_count,
        "template": template,
        "size_kb": size_kb,
    }
