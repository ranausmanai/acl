"""
email.draft  –  mock email drafter.

Produces a deterministic draft ID from the envelope fields.  No email is
actually sent.
"""

from __future__ import annotations

import hashlib


def email_draft(
    to: str | list[str],
    subject: str,
    body: str,
    attachments: list[str] | None = None,
) -> dict:
    """
    Create a mock email draft.

    Returns: {draft_id: str, to: list[str], subject: str, attachment_count: int}
    """
    if isinstance(to, str):
        to = [to]
    attachments = attachments or []

    raw = f"{','.join(to)}|{subject}|{body[:64]}"
    draft_id = "draft_" + hashlib.md5(raw.encode()).hexdigest()[:10]

    return {
        "draft_id": draft_id,
        "to": to,
        "subject": subject,
        "attachment_count": len(attachments),
        "status": "queued",
    }
