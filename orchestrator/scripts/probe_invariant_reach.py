"""Deterministic BM25 reach probe for invariant-as-seed (no LLM needed).

The graded pipeline gates four stages behind the Judge (distill / analogy /
specialize / entailment); with no LLM credentials none of them can run. But the
question the user asked first — "does seeding on the 426 surveyed invariants
surface enough cross-mechanism signal to be worth it?" — is answerable *without*
any judgment, because retrieval + the exit filter are fully deterministic.

This probe builds a faithful proxy SeedQuery from each invariant seed's own text
(the same ``violated_invariant`` + ``impact`` + ``notes`` fields the real
distiller reads), runs the real ``retrieve()`` (BM25 + exit filter), and reports:

- reach: how many seeds surface >=1 cross-mechanism source hit (the ceiling on
  candidates a judged run could accept);
- where the jumps land (origin_mechanism -> hit_mechanism pairs), i.e. where the
  analogies will cluster;
- per-seed top survivor so a high-value subset can be picked for the judged run.

It is a *lower bound*: the proxy query keeps the seed's mechanism-specific nouns
(the real distiller strips them), which biases toward the seed's own mechanism —
and those hits are dropped by the exit filter. So any cross-mechanism survivor
here is genuine shared-root-cause vocabulary, not mechanism-name collision.
"""

from __future__ import annotations

import json
from collections import Counter
from pathlib import Path

from defuzz_loop.specgen.corpus import load_corpus
from defuzz_loop.specgen.query import _harvest_exact_anchors
from defuzz_loop.specgen.retriever import BM25Retriever, retrieve
from defuzz_loop.specgen.schema import SeedQuery
from defuzz_loop.specgen.seeds import load_invariants

_DR_ROOT = Path("/Users/bytedance/projects/research/defend-reviewer/feat-merge-invariants")
_INV_ROOT = _DR_ROOT / "docs" / "invariants"
_CORPUS = Path("runs/specgen_full/cache/corpus.jsonl")
_OUT = Path("runs/specgen_inv_bm25")
_TOP_K = 8


def _proxy_query(seed) -> SeedQuery:
    """A deterministic stand-in for the distilled query (probe only).

    Uses the exact free-text fields the real distiller reads as the retrieval
    phrase; anchors are program-injected exactly as in ``distill_query`` so the
    exit filter behaves identically. This is NOT the graded query — it keeps
    mechanism nouns — so its reach is a conservative floor.
    """
    phrase = " ".join(
        p for p in (seed.violated_invariant, seed.impact, seed.notes) if p
    )
    return SeedQuery(
        seed_id=seed.seed_id,
        origin_mechanism=seed.origin_mechanism,
        violated_invariant=seed.violated_invariant,
        root_cause_phrase=phrase,
        agnostic_tokens=[],
        exact_anchors=_harvest_exact_anchors(seed),
    )


def main() -> None:
    _OUT.mkdir(parents=True, exist_ok=True)
    chunks = load_corpus(_CORPUS)
    retriever = BM25Retriever()
    retriever.index(chunks)

    seeds = load_invariants(_INV_ROOT)
    rows: list[dict] = []
    pair_counter: Counter[str] = Counter()
    hit_mech_counter: Counter[str] = Counter()
    seeds_with_reach = 0

    for seed in seeds:
        sq = _proxy_query(seed)
        survivors, dropped = retrieve(retriever, sq, top_k=_TOP_K)
        if survivors:
            seeds_with_reach += 1
        landed = sorted({h.metadata.mechanism for h in survivors})
        for m in landed:
            pair_counter[f"{seed.origin_mechanism} -> {m}"] += 1
            hit_mech_counter[m] += 1
        top = survivors[0] if survivors else None
        rows.append(
            {
                "seed_id": seed.seed_id,
                "origin_mechanism": seed.origin_mechanism,
                "n_survivors": len(survivors),
                "n_dropped": len(dropped),
                "landed_mechanisms": landed,
                "top_hit": (
                    {
                        "chunk_id": top.chunk_id,
                        "mechanism": top.metadata.mechanism,
                        "score": round(top.score, 3),
                        "path": top.metadata.path,
                        "symbol": top.metadata.symbol,
                    }
                    if top
                    else None
                ),
            }
        )

    rows.sort(key=lambda r: r["n_survivors"], reverse=True)
    (_OUT / "reach.json").write_text(
        json.dumps(rows, indent=2, ensure_ascii=False), encoding="utf-8"
    )

    n = len(seeds)
    print(f"corpus chunks       : {len(chunks)}")
    print(f"invariant seeds     : {n}")
    print(f"seeds with >=1 reach : {seeds_with_reach}  ({seeds_with_reach / n:.0%})")
    print(f"seeds with zero reach: {n - seeds_with_reach}")
    tot_surv = sum(r["n_survivors"] for r in rows)
    print(f"total cross-mech survivors (top-{_TOP_K} each): {tot_surv}")
    print("\n--- where jumps land (hit mechanism, # distinct seeds) ---")
    for m, c in hit_mech_counter.most_common():
        print(f"  {c:4d}  {m}")
    print("\n--- top origin -> hit pairs ---")
    for pair, c in pair_counter.most_common(25):
        print(f"  {c:4d}  {pair}")
    print(f"\nper-seed reach written: {_OUT / 'reach.json'}")


if __name__ == "__main__":
    main()
