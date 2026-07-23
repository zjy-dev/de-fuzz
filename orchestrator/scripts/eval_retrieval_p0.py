"""Offline, API-free evaluation of the P0 retrieval change (hybrid RRF + top_k).

We cannot re-run the live pipeline here (no ARK_API_KEY / OPENAI_API_KEY), and a
TranscriptJudge replay would miss every new chunk_id. But we do not need the LLM
to measure P0's effect on the *retrieval bottleneck*, because of one property:

    the analogy judgment for a (seed, chunk) pair depends ONLY on the distilled
    root-cause phrase and the chunk text — never on which retriever surfaced it.

So a chunk the transcript already labelled ``does_analogy_hold=true`` stays a
true analogy no matter which backend ranks it into the funnel. The union of
TRUE-labelled chunks from the two shipped runs (specgen_full = BM25,
specgen_embed = dense) is therefore a backend-neutral gold set of "chunks that
would pass the analogy gate and become candidates".

This harness reconstructs, fully deterministically from cached artifacts:
  * BM25 ranking (rank_bm25 is deterministic, no API),
  * dense ranking (cosine of cached query vectors vs cached corpus vectors),
  * hybrid ranking (RRF of the two — exactly HybridRetriever),
then runs the real exit filter and reports, per backend x top_k:
  * survivors      = analogy-gate LLM calls the run would spend,
  * gold-TRUE hits = candidates that would pass the gate (yield),
  * precision      = gold-TRUE / labelled survivors,
  * recall         = gold-TRUE / |union gold set|,
  * unlabelled     = survivors neither shipped run ever judged (upside the LLM
                     would newly score — counted, not scored).

Run:  .venv/bin/python scripts/eval_retrieval_p0.py
"""

from __future__ import annotations

import json
from pathlib import Path

import numpy as np

from defuzz_loop.specgen.query import _harvest_exact_anchors
from defuzz_loop.specgen.retriever import BM25Retriever, exit_filter, rrf_fuse
from defuzz_loop.specgen.schema import Chunk, Hit, SeedQuery
from defuzz_loop.specgen.seeds import load_seeds

ROOT = Path(__file__).resolve().parents[1]
RUNS = ROOT / "runs"
FULL = RUNS / "specgen_full"       # BM25 run
EMBED = RUNS / "specgen_embed"     # dense run (has the vector caches)
DR_ROOT = Path("/Users/bytedance/projects/defend-reviewer-invariants")

TOP_KS = [8, 16, 25]
OVER_FETCH = 4


def load_corpus(path: Path) -> list[Chunk]:
    lines = path.read_text().splitlines()
    return [Chunk.model_validate_json(ln) for ln in lines if ln.strip()]


def build_seedqueries() -> dict[str, SeedQuery]:
    """Reconstruct the exact SeedQuery for each seed from Stage 0 + transcript distill."""
    seeds = load_seeds(
        ["findings"],
        findings_root=DR_ROOT / "findings",
        bugs_root=DR_ROOT / "docs" / "bugs",
        invariants_root=DR_ROOT / "docs" / "invariants",
    )
    distill = json.loads((FULL / "transcript.json").read_text())["distill_query"]
    out: dict[str, SeedQuery] = {}
    for s in seeds:
        d = distill.get(s.seed_id)
        if d is None:
            continue
        out[s.seed_id] = SeedQuery(
            seed_id=s.seed_id,
            origin_mechanism=s.origin_mechanism,
            violated_invariant=s.violated_invariant,
            root_cause_phrase=d["root_cause_phrase"].strip(),
            agnostic_tokens=[t.strip() for t in d.get("agnostic_tokens", []) if t.strip()],
            exact_anchors=_harvest_exact_anchors(s),
        )
    return out


def gold_true_by_seed() -> dict[str, set[str]]:
    """Union of chunks judged does_analogy_hold=true across both shipped runs."""
    gold: dict[str, set[str]] = {}
    labelled: dict[str, set[str]] = {}
    for run in (FULL, EMBED):
        analogy = json.loads((run / "transcript.json").read_text())["analogy"]
        for key, val in analogy.items():
            seed, chunk = key.split("::", 1)
            labelled.setdefault(seed, set()).add(chunk)
            if val.get("does_analogy_hold"):
                gold.setdefault(seed, set()).add(chunk)
    return gold, labelled


def dense_matrix() -> tuple[np.ndarray, list[str]]:
    """Load cached corpus vectors, L2-normalized, aligned to corpus order."""
    chunks = load_corpus(EMBED / "cache" / "corpus.jsonl")
    cache = json.loads((EMBED / "cache" / "embeddings.json").read_text())
    vecs = np.asarray(cache["vectors"], dtype=np.float32)
    norms = np.linalg.norm(vecs, axis=1, keepdims=True)
    norms[norms == 0.0] = 1.0
    return vecs / norms, [c.chunk_id for c in chunks]


def main() -> None:
    corpus = load_corpus(EMBED / "cache" / "corpus.jsonl")
    by_id = {c.chunk_id: c for c in corpus}
    queries = build_seedqueries()
    gold, labelled = gold_true_by_seed()

    # dense side
    qvecs = json.loads((EMBED / "cache" / "query_vectors.json").read_text())
    matrix, order = dense_matrix()

    # bm25 side
    bm25 = BM25Retriever()
    bm25.index(corpus)

    # Only seeds that have a cached query vector can be scored on the dense leg.
    scored_seeds = [
        sid for sid, q in queries.items()
        if " ".join(q.query_terms()).strip() in qvecs
    ]

    def dense_search(q: SeedQuery, k: int) -> list[Hit]:
        text = " ".join(q.query_terms()).strip()
        qv = np.asarray(qvecs[text], dtype=np.float32)
        qn = np.linalg.norm(qv) or 1.0
        scores = matrix @ (qv / qn)
        idx = np.argsort(-scores)[:k]
        return [Hit(chunk_id=order[i], text=by_id[order[i]].text,
                    metadata=by_id[order[i]].metadata, score=float(scores[i])) for i in idx]

    union_gold = sum(len(gold.get(s, set())) for s in scored_seeds)
    print(f"seeds scored: {len(scored_seeds)}   union gold-TRUE chunks: {union_gold}\n")
    cols = ["backend", "top_k", "survivors", "goldTRUE", "precision", "recall", "unlabelled"]
    header = (
        f"{cols[0]:<8} {cols[1]:>5} {cols[2]:>9} {cols[3]:>8} "
        f"{cols[4]:>9} {cols[5]:>7} {cols[6]:>10}"
    )
    print(header)
    print("-" * len(header))

    for top_k in TOP_KS:
        pool = top_k * OVER_FETCH
        for backend in ("bm25", "dense", "hybrid"):
            tot_surv = tot_gold = tot_unlab = tot_lab = 0
            for sid in scored_seeds:
                q = queries[sid]
                braw = bm25.search(q, top_k=pool)
                draw = dense_search(q, pool)
                if backend == "bm25":
                    raw = braw
                elif backend == "dense":
                    raw = draw
                else:
                    raw = rrf_fuse([braw, draw])
                survivors, _ = exit_filter(raw, q)
                survivors = survivors[:top_k]
                g = gold.get(sid, set())
                lab = labelled.get(sid, set())
                for h in survivors:
                    tot_surv += 1
                    if h.chunk_id in g:
                        tot_gold += 1
                    if h.chunk_id in lab:
                        tot_lab += 1
                    else:
                        tot_unlab += 1
            prec = tot_gold / tot_lab if tot_lab else 0.0
            rec = tot_gold / union_gold if union_gold else 0.0
            print(
                f"{backend:<8} {top_k:>5} {tot_surv:>9} {tot_gold:>8} "
                f"{prec:>9.3f} {rec:>7.3f} {tot_unlab:>10}"
            )
        print()


if __name__ == "__main__":
    main()
