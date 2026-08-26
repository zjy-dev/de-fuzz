"""Diagnose the signature-mode retrieval for one seed: print the distilled
signature query and the top-k survivors, so we can see WHICH sites it lands on
(sister-target failure sites vs generic shared functions). One LLM call.

Run (after sourcing ../.env):
  .venv/bin/python scripts/diag_signature_hits.py DREV-2026-001
"""

from __future__ import annotations

import asyncio
import sys
from pathlib import Path

from defuzz_loop.llm import LLMConfig
from defuzz_loop.specgen.judge import LLMJudge
from defuzz_loop.specgen.pipeline import _bodies_for_symbols
from defuzz_loop.specgen.query import _harvest_exact_anchors, distill_signature_query
from defuzz_loop.specgen.retriever import BM25Retriever, retrieve
from defuzz_loop.specgen.schema import Chunk
from defuzz_loop.specgen.seeds import load_findings

FULL = Path("runs/specgen_full")
DR = Path("/Users/bytedance/projects/research/defend-reviewer/feat-merge-invariants")


async def main() -> None:
    seed_id = sys.argv[1] if len(sys.argv) > 1 else "DREV-2026-001"
    corpus = [
        Chunk.model_validate_json(ln)
        for ln in (FULL / "cache" / "corpus.jsonl").read_text().splitlines()
        if ln.strip()
    ]
    by_sym: dict[str, list[Chunk]] = {}
    for c in corpus:
        if c.metadata.symbol:
            by_sym.setdefault(c.metadata.symbol, []).append(c)

    bm25 = BM25Retriever()
    bm25.index(corpus)

    seed = {s.seed_id: s for s in load_findings(DR / "findings")}[seed_id]
    anchors = _harvest_exact_anchors(seed)
    defect_bodies = _bodies_for_symbols(by_sym, anchors)

    judge = LLMJudge(LLMConfig.load())
    sq = await distill_signature_query(
        judge, seed, defect_bodies=defect_bodies, reference_bodies=[]
    )

    print(f"=== {seed_id} ({seed.origin_mechanism}) ===")
    print(f"anchors ({len(anchors)}): {anchors}")
    print(f"defect bodies resolved: {len(defect_bodies)}")
    print("\n--- SIGNATURE QUERY ---")
    print("phrase:", sq.root_cause_phrase)
    print("tokens:", sq.agnostic_tokens)

    survivors, dropped = retrieve(bm25, sq, top_k=8, over_fetch=4)
    print(f"\n--- TOP-{len(survivors)} SURVIVORS (after exit filter) ---")
    for h in survivors:
        print(f"  [{h.metadata.mechanism:<20}] {h.metadata.isa or '-':<12} "
              f"{h.chunk_id}  (score {h.score:.3f})")
    print(f"\ndropped by exit filter: {len(dropped)}")
    for h in dropped[:6]:
        print(f"  DROP [{h.metadata.mechanism:<20}] {h.chunk_id}")


if __name__ == "__main__":
    asyncio.run(main())
