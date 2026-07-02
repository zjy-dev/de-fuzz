"""specgen stage 6 — novelty / near-duplicate flagging against the baseline.

A candidate whose statement closely matches an existing invariant is demoted
(``is_novel=False``); a genuinely new cross-mechanism statement stays novel.
The threshold is BM25-relative, so the test builds a tiny baseline and picks a
threshold that separates a paraphrase of a baseline entry from an unrelated one.
"""

from __future__ import annotations

from pathlib import Path

from defuzz_loop.specgen.dedup import (
    NoveltyBaseline,
    assess_novelty,
    build_baseline,
    parse_drev_statements,
    parse_survey_invariants,
)
from defuzz_loop.specgen.schema import Candidate


def _cand(statement: str) -> Candidate:
    return Candidate(
        seed_id="DREV-2026-025",
        origin_mechanism="fortify-source",
        hit_mechanism="stack-clash-protection",
        statement=statement,
        observation="observable",
    )


_BASELINE = NoveltyBaseline(
    [
        ("INV-SP-L01", "canary slot must sit between vulnerable locals and saved registers"),
        ("INV-SP-S02", "epilogue must clobber registers holding the guard value before return"),
        ("INV-FORT-O02", "a size-carrying count field must not be narrowed to a fixed-width int"),
        ("INV-CET-01", "an endbr landing pad must precede every indirect branch target"),
        ("INV-PAC-01", "a signed return address must be authenticated before the return"),
        ("INV-SCP-01", "the probe loop must touch every guard page across the frame"),
        ("INV-IBT-02", "notrack prefixes must not disable endbr enforcement globally"),
        ("INV-STRUB-01", "a strub context must scrub its stack frame on exit"),
        ("INV-SP-H01", "vla and alloca functions must be instrumented under any enabled level"),
        ("INV-FORT-O05", "object size folding must preserve the declared element width"),
    ]
)


def _nearest_score(statement: str) -> float:
    _, score = _BASELINE.nearest(statement)
    return score


def test_near_duplicate_is_flagged_not_novel() -> None:
    stmt = "a size-carrying count field must not be narrowed to a fixed width int type"
    # A paraphrase of INV-FORT-O02 scores clearly higher than an unrelated statement.
    dup_score = _nearest_score(stmt)
    unrelated = _nearest_score("an endbr landing pad precedes every indirect branch target")
    threshold = (dup_score + unrelated) / 2  # a knob that separates the two
    dup = _cand(stmt)
    assess_novelty(_BASELINE, [dup], threshold=threshold)
    assert dup.novelty is not None
    assert dup.novelty.is_novel is False
    assert dup.novelty.nearest_id == "INV-FORT-O02"
    assert dup.novelty.nearest_score >= threshold


def test_unrelated_statement_is_novel() -> None:
    novel = _cand(
        "the residual probe loop bound in stack-clash protection must retain its full width"
    )
    # Threshold set above the near-duplicate score → an unrelated statement is novel.
    high = _nearest_score(
        "a size-carrying count field must not be narrowed to a fixed width int type"
    )
    assess_novelty(_BASELINE, [novel], threshold=high)
    assert novel.novelty is not None
    assert novel.novelty.is_novel is True


def test_threshold_is_the_only_knob() -> None:
    # The same near-duplicate flips novel/not by moving the threshold across its score.
    stmt = "a size-carrying count field must not be narrowed to a fixed width int"
    score = _nearest_score(stmt)
    c1 = _cand(stmt)
    assess_novelty(_BASELINE, [c1], threshold=score + 1.0)
    assert c1.novelty is not None and c1.novelty.is_novel is True  # threshold above score
    c2 = _cand(stmt)
    assess_novelty(_BASELINE, [c2], threshold=score - 0.1)
    assert c2.novelty is not None and c2.novelty.is_novel is False  # threshold below score


def test_empty_baseline_marks_everything_novel() -> None:
    empty = NoveltyBaseline([])
    c = _cand("anything at all")
    assess_novelty(empty, [c], threshold=6.0)
    assert c.novelty is not None and c.novelty.is_novel is True


def test_origin_seed_is_excluded_from_novelty() -> None:
    # A cross-mechanism candidate reuses its origin seed's vocabulary by
    # construction, so the origin seed must not count as its nearest neighbour.
    stmt = "a size-carrying count field must not be narrowed to a fixed width int"
    baseline = NoveltyBaseline(
        [
            ("DREV-2026-025", stmt),  # the candidate's own origin seed (near-identical)
            ("INV-CET-01", "an endbr landing pad must precede every indirect branch target"),
        ]
    )
    c = _cand(stmt)  # _cand sets seed_id="DREV-2026-025"
    assess_novelty(baseline, [c], threshold=1.0)
    assert c.novelty is not None
    # The origin seed is skipped, so the nearest match is the unrelated CET entry.
    assert c.novelty.nearest_id == "INV-CET-01"


def test_parse_survey_and_drev(tmp_path: Path) -> None:
    inv_root = tmp_path / "invariants"
    inv_root.mkdir()
    (inv_root / "stack-canary.md").write_text(
        "### INV-SP-L01 — Canary slot placement\n\n"
        "- **statement**: canary sits between locals and saved registers\n",
        encoding="utf-8",
    )
    # README/survey files are skipped by the parser.
    (inv_root / "README.md").write_text("### INV-X-01 — ignored\n", encoding="utf-8")

    entries = parse_survey_invariants(inv_root)
    expected = "Canary slot placement canary sits between locals and saved registers"
    assert ("INV-SP-L01", expected) in entries
    assert not any(e[0] == "INV-X-01" for e in entries)

    findings = tmp_path / "findings"
    (findings / "DREV-2026-001").mkdir(parents=True)
    (findings / "DREV-2026-001" / "README.md").write_text(
        "---\nid: DREV-2026-001\nmechanism: fortify-source\n"
        "invariant_violated: a count must not be narrowed\n---\n# body\n",
        encoding="utf-8",
    )
    drev = parse_drev_statements(findings)
    assert drev == [("DREV-2026-001", "a count must not be narrowed")]

    baseline = build_baseline(invariants_root=inv_root, findings_root=findings)
    assert len(baseline) == 2  # one survey inv + one DREV
