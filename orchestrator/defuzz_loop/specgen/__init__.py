"""Offline cross-mechanism invariant generation (innovation A).

A staged pipeline (seeds → query → corpus → retrieve → generate → ground → dedup)
that starts from a known bug in one defense mechanism, distills a
mechanism-agnostic root cause, retrieves sister-mechanism corpus by BM25, and
proposes grounded, falsifiable candidate invariants for the mechanism it jumped
to. Not wired into the LangGraph runtime; run via `defuzz-loop specgen`.
"""

from __future__ import annotations
