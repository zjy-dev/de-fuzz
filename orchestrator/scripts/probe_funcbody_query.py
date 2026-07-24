"""Probe: function-body-as-query (PropertyGPT-style) vs abstract-phrase query.

No API needed — retrieval is deterministic BM25 over the *same* indexed corpus.
Only the QUERY text differs:

  * abstract  — the shipped SeedQuery.query_terms() (distilled root-cause phrase
                + agnostic tokens), replayed from specgen_full/transcript.json.
  * funcbody  — the actual buggy GCC function *bodies* the seed's evidence names,
                looked up from the corpus by symbol (identifiers, control flow
                and data flow preserved — the opposite of stripping them).

For each seed we retrieve top_k with each query and report, for the funcbody
query, how the surfaced sister sites differ. The point is qualitative: does a
structural query land on different (and more failure-relevant) sites than the
mechanism-neutral shape query? We print both slates side by side so a human /
the model can judge the shift the audit asked about.

Run:  .venv/bin/python scripts/probe_funcbody_query.py
"""

from __future__ import annotations

import json
from pathlib import Path

from defuzz_loop.specgen.query import _harvest_exact_anchors
from defuzz_loop.specgen.retriever import BM25Retriever, exit_filter
from defuzz_loop.specgen.schema import Chunk, SeedQuery
from defuzz_loop.specgen.seeds import load_findings

ROOT = Path(__file__).resolve().parents[1]
FULL = ROOT / "runs" / "specgen_full"
DR_ROOT = Path("/Users/bytedance/projects/defend-reviewer-invariants")
TOP_K = 8
OVER_FETCH = 4


def load_corpus() -> list[Chunk]:
    lines = (FULL / "cache" / "corpus.jsonl").read_text().splitlines()
    return [Chunk.model_validate_json(ln) for ln in lines if ln.strip()]


def main() -> None:
    corpus = load_corpus()
    by_sym: dict[str, list[Chunk]] = {}
    for c in corpus:
        if c.metadata.symbol:
            by_sym.setdefault(c.metadata.symbol, []).append(c)

    bm25 = BM25Retriever()
    bm25.index(corpus)

    seeds = {s.seed_id: s for s in load_findings(DR_ROOT / "findings")}
    distill = json.loads((FULL / "transcript.json").read_text())["distill_query"]

    covered = both = only_fb = only_ab = 0
    for sid, seed in seeds.items():
        d = distill.get(sid)
        if d is None:
            continue
        anchors = _harvest_exact_anchors(seed)

        # --- abstract query (shipped) ---
        abstract = SeedQuery(
            seed_id=sid, origin_mechanism=seed.origin_mechanism,
            root_cause_phrase=d["root_cause_phrase"].strip(),
            agnostic_tokens=[t.strip() for t in d.get("agnostic_tokens", []) if t.strip()],
            exact_anchors=anchors,
        )
        # --- function-body query: bodies of the buggy funcs the seed names ---
        bodies = [c.text for sym in anchors for c in by_sym.get(sym, [])]
        if not bodies:
            continue
        covered += 1
        # Reuse SeedQuery: put the raw function text into root_cause_phrase so
        # query_terms() tokenizes the real identifiers/flow (no stripping).
        funcbody = SeedQuery(
            seed_id=sid, origin_mechanism=seed.origin_mechanism,
            root_cause_phrase="\n".join(bodies)[:6000],
            agnostic_tokens=[], exact_anchors=anchors,
        )

        def survivors(q: SeedQuery) -> list[tuple[str, str]]:
            raw = bm25.search(q, top_k=TOP_K * OVER_FETCH)
            surv, _ = exit_filter(raw, q)
            return [(h.metadata.mechanism, h.chunk_id) for h in surv[:TOP_K]]

        ab = survivors(abstract)
        fb = survivors(funcbody)
        ab_ids = {c for _, c in ab}
        fb_ids = {c for _, c in fb}
        both += len(ab_ids & fb_ids)
        only_fb += len(fb_ids - ab_ids)
        only_ab += len(ab_ids - fb_ids)

        print(f"\n=== {sid}  ({seed.origin_mechanism}) ===")
        print(f"  abstract query : {d['root_cause_phrase'][:90]}...")
        print(f"  funcbody query : {len(bodies)} function body/-ies, "
              f"{sum(len(b) for b in bodies)} chars")
        print(f"  overlap top-{TOP_K}: {len(ab_ids & fb_ids)}   "
              f"only-abstract: {len(ab_ids - fb_ids)}   only-funcbody: {len(fb_ids - ab_ids)}")
        print("  funcbody-only sister sites:")
        for mech, cid in fb:
            if cid not in ab_ids:
                print(f"      [{mech:<24}] {cid}")

    print("\n" + "=" * 60)
    print(f"seeds with a corpus-resident buggy function: {covered}")
    print(f"top-{TOP_K} slate overlap totals — both={both}  "
          f"only-abstract={only_ab}  only-funcbody={only_fb}")


if __name__ == "__main__":
    main()
