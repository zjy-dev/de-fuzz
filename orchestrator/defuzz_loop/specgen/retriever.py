"""Stage 3 — retrieval (BM25 v1) + the cross-mechanism exit filter.

The ``Retriever`` protocol keeps the pipeline agnostic to the ranking backend
(plan §"Retriever 接口化"). v1 ships only ``BM25Retriever``; dense / hybrid
retrievers are Phase 2 and slot in behind the same protocol.

Tokenizer (the highest-signal choice for lexical retrieval over compiler code):
``[A-Za-z0-9_]+`` runs are kept whole (so ``__builtin_object_size`` is one
token), then each run is *also* split on ``_`` to yield sub-tokens, and every
token is lowercased. That lets ``root_cause_phrase`` words like "narrow" match
``narrowing`` sub-tokens without losing the whole-identifier signal.

The exit filter is the load-bearing part of innovation A: a hit is dropped when
(a) its mechanism equals the seed's ``origin_mechanism`` — you rediscovered your
own mechanism — or (b) its text contains any ``exact_anchor`` — you rediscovered
the seed itself. What survives is "a sister mechanism, same root-cause shape,
not the seed". Only ``query_terms()`` (phrase + agnostic tokens) enter the
scorer; anchors and mechanism nouns never do.
"""

from __future__ import annotations

import re
from typing import Protocol

from rank_bm25 import BM25Okapi

from .schema import Chunk, Hit, SeedQuery

_TOKEN = re.compile(r"[A-Za-z0-9_]+")


def tokenize(text: str) -> list[str]:
    """Whole-identifier tokens + ``_``-split sub-tokens, all lowercased."""
    out: list[str] = []
    for run in _TOKEN.findall(text):
        low = run.lower()
        out.append(low)
        if "_" in low:
            out.extend(p for p in low.split("_") if p)
    return out


class Retriever(Protocol):
    def index(self, chunks: list[Chunk]) -> None: ...
    def search(self, query: SeedQuery, top_k: int) -> list[Hit]: ...


class BM25Retriever:
    """rank-bm25 BM25Okapi over the custom tokenizer. v1's only retriever."""

    def __init__(self) -> None:
        self._chunks: list[Chunk] = []
        self._bm25: BM25Okapi | None = None

    def index(self, chunks: list[Chunk]) -> None:
        self._chunks = list(chunks)
        corpus_tokens = [tokenize(c.text) for c in self._chunks]
        # BM25Okapi requires a non-empty corpus.
        self._bm25 = BM25Okapi(corpus_tokens) if corpus_tokens else None

    def search(self, query: SeedQuery, top_k: int) -> list[Hit]:
        if self._bm25 is None:
            return []
        q_tokens = tokenize(" ".join(query.query_terms()))
        if not q_tokens:
            return []
        scores = self._bm25.get_scores(q_tokens)
        ranked = sorted(range(len(scores)), key=lambda i: scores[i], reverse=True)
        hits: list[Hit] = []
        for i in ranked[:top_k]:
            if scores[i] <= 0.0:
                break
            c = self._chunks[i]
            hits.append(
                Hit(chunk_id=c.chunk_id, text=c.text, metadata=c.metadata, score=float(scores[i]))
            )
        return hits


def exit_filter(hits: list[Hit], query: SeedQuery) -> tuple[list[Hit], list[Hit]]:
    """Split hits into (survivors, dropped) per the cross-mechanism exit filter.

    Dropped when the hit is the seed's own mechanism, or its text contains any
    exact anchor (a case-sensitive identifier match, so ``INV-FORT-O02`` and
    ``access_with_size`` are caught but common words are not).
    """
    survivors: list[Hit] = []
    dropped: list[Hit] = []
    anchors = [a for a in query.exact_anchors if a]
    for h in hits:
        if h.metadata.mechanism == query.origin_mechanism:
            dropped.append(h)
            continue
        if any(a in h.text for a in anchors):
            dropped.append(h)
            continue
        survivors.append(h)
    return survivors, dropped


def retrieve(
    retriever: Retriever, query: SeedQuery, *, top_k: int, over_fetch: int = 4
) -> tuple[list[Hit], list[Hit]]:
    """Fetch ``top_k * over_fetch`` then exit-filter down; return (survivors, dropped).

    Over-fetching compensates for hits the exit filter removes so a fixed
    ``top_k`` of *survivors* still gets a full slate of cross-mechanism leads.
    """
    raw = retriever.search(query, top_k=top_k * over_fetch)
    survivors, dropped = exit_filter(raw, query)
    return survivors[:top_k], dropped
