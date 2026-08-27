"""Conservative, deterministic reuse of already-shipped oracle checkers.

This is intentionally a *pilot*, not semantic search. A generated invariant
is eligible only when its normalized wording states the complete P01 contract;
nearby IBT properties are left for Part II authoring. In particular, return
edges after `setjmp` and byte-pattern/immediate checks are different
checkers, even though they all mention ENDBR.
"""

from __future__ import annotations

import hashlib
import re
from collections.abc import Mapping, Sequence
from typing import Any

_SPACE = re.compile(r"\s+")
_PUNCTUATION = re.compile(r"[^a-z0-9]+")

PILOT_CHECKER_ID = "INV-IBT-P01"
_P01_CONCEPT = "ibt indirect callable function entry endbr"
_P01_ALLOWED_STATEMENTS = frozenset(
    {
        "every indirect callable function entry must begin with endbr",
        "indirect callable function entries must begin with endbr",
        (
            "elf assembly marks binaries with the ibt feature property and emits endbr "
            "at address taken function entries when intel cet indirect branch tracking "
            "is enabled"
        ),
    }
)


def canonical_concept_fingerprint(statement: str) -> str:
    """Return a stable fingerprint for the deliberately narrow P01 concept."""

    normalized = _SPACE.sub(
        " ", _PUNCTUATION.sub(" ", statement.casefold())
    ).strip()
    if normalized in _P01_ALLOWED_STATEMENTS:
        return hashlib.sha256(_P01_CONCEPT.encode("utf-8")).hexdigest()
    return ""


P01_FINGERPRINT = canonical_concept_fingerprint(
    "Indirect-callable function entries must begin with ENDBR."
)


def reusable_checker_ids(statement: str) -> list[str]:
    """Return trusted IDs only for exact pilot semantics; otherwise no match."""

    return [PILOT_CHECKER_ID] if canonical_concept_fingerprint(statement) == P01_FINGERPRINT else []


def reuse_report(records: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    """Create an auditable Part I artifact without making any finding claim."""

    decisions = []
    for record in records:
        statement = str(record.get("statement", ""))
        checker_ids = reusable_checker_ids(statement)
        decisions.append(
            {
                "invariant_id": str(record.get("invariant_id", "")),
                "concept_fingerprint": canonical_concept_fingerprint(statement) or None,
                "reused_checker_ids": checker_ids,
                "decision": "reused" if checker_ids else "author",
                "reason": (
                    "exact pilot semantic contract for indirect-callable function entry ENDBR"
                    if checker_ids
                    else "no trusted exact semantic fingerprint match"
                ),
            }
        )
    return {
        "schema_version": 1,
        "kind": "defuzz-checker-reuse-report",
        "scope": "part-i-to-part-ii-existing-checker-reuse",
        "finding_claims_exposed": False,
        "decisions": decisions,
    }
