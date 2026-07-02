"""Stage 6 — novelty / dedup against the existing invariant corpus.

Before a candidate is worth a human's promotion time, check it is not a
near-duplicate of something already written down. The baseline is the survey's
existing invariants (``docs/tech-docs/invariants/*.md`` ``### INV-...`` blocks)
plus the DREV findings' violated-invariant statements.

The similarity metric reuses the retrieval tokenizer + BM25: index the baseline
statements, score each candidate's statement, and if the top score exceeds a
threshold the candidate is flagged a near-duplicate (``is_novel=False``) and
demoted (kept in staging, not promoted). BM25 scores are corpus-relative, so the
threshold is a knob the end-to-end run calibrates, not an absolute.
"""

from __future__ import annotations

import re
from pathlib import Path

from rank_bm25 import BM25Okapi

from .retriever import tokenize
from .schema import Candidate, Novelty

_INV_HEADER = re.compile(r"^###\s+(INV-[A-Z0-9]+-[A-Z]+\d+)\s*[—-]\s*(.*)$")
_STATEMENT = re.compile(r"^\s*-\s*\*\*statement\*\*\s*[:：]\s*(.*)$")


class NoveltyBaseline:
    """A BM25 index over existing invariant statements for near-duplicate flagging."""

    def __init__(self, entries: list[tuple[str, str]]) -> None:
        # entries: (inv_id, statement_text)
        self._ids = [e[0] for e in entries]
        self._texts = [e[1] for e in entries]
        toks = [tokenize(t) for t in self._texts]
        self._bm25 = BM25Okapi(toks) if toks else None

    def nearest(self, statement: str, *, exclude_id: str = "") -> tuple[str, float]:
        if self._bm25 is None:
            return "", 0.0
        q = tokenize(statement)
        if not q:
            return "", 0.0
        scores = self._bm25.get_scores(q)
        # A cross-mechanism candidate reuses its origin seed's root-cause
        # vocabulary by construction, so scoring it against that same seed is a
        # guaranteed self-match, not a real duplicate. Exclude the origin entry.
        candidates = [i for i in range(len(scores)) if self._ids[i] != exclude_id]
        if not candidates:
            return "", 0.0
        best = max(candidates, key=lambda i: scores[i])
        return self._ids[best], float(scores[best])

    def __len__(self) -> int:
        return len(self._ids)


def parse_survey_invariants(invariants_root: Path) -> list[tuple[str, str]]:
    """Harvest ``(inv_id, statement)`` from every ``### INV-...`` block in the survey."""
    entries: list[tuple[str, str]] = []
    for md in sorted(invariants_root.glob("*.md")):
        if md.name in {"README.md", "gcc-llvm-defense-invariant-source-survey.md"}:
            continue
        lines = md.read_text(encoding="utf-8", errors="replace").split("\n")
        cur_id = ""
        cur_title = ""
        for line in lines:
            h = _INV_HEADER.match(line)
            if h:
                cur_id, cur_title = h.group(1), h.group(2).strip()
                # Seed the entry with the title so a block without an explicit
                # statement line still contributes text.
                entries.append((cur_id, cur_title))
                continue
            s = _STATEMENT.match(line)
            if s and cur_id:
                # Replace the title-only seed with the fuller statement text.
                entries[-1] = (cur_id, f"{cur_title} {s.group(1).strip()}")
    return entries


def parse_drev_statements(findings_root: Path) -> list[tuple[str, str]]:
    """Harvest ``(DREV-id, invariant_violated)`` from DREV findings."""
    import yaml

    entries: list[tuple[str, str]] = []
    for readme in sorted(findings_root.glob("DREV-*/README.md")):
        text = readme.read_text(encoding="utf-8", errors="replace")
        if not text.startswith("---"):
            continue
        end = text.find("\n---", 3)
        if end == -1:
            continue
        # Tolerate a malformed front matter in one seed (skip it).
        try:
            data = yaml.safe_load(text[3:end])
        except yaml.YAMLError:
            continue
        if not isinstance(data, dict):
            continue
        sid = data.get("id")
        stmt = data.get("invariant_violated")
        if isinstance(sid, str) and isinstance(stmt, str):
            entries.append((sid, stmt.strip()))
    return entries


def build_baseline(
    *, invariants_root: Path | None, findings_root: Path | None
) -> NoveltyBaseline:
    entries: list[tuple[str, str]] = []
    if invariants_root is not None:
        entries.extend(parse_survey_invariants(invariants_root))
    if findings_root is not None:
        entries.extend(parse_drev_statements(findings_root))
    return NoveltyBaseline(entries)


def assess_novelty(
    baseline: NoveltyBaseline, candidates: list[Candidate], *, threshold: float
) -> None:
    """Attach a ``Novelty`` verdict to each candidate in place."""
    for c in candidates:
        nid, score = baseline.nearest(c.statement, exclude_id=c.seed_id)
        c.novelty = Novelty(is_novel=score < threshold, nearest_id=nid, nearest_score=score)
