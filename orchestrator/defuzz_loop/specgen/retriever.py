"""Stage 3 — retrieval (BM25 + dense embedding) + the cross-mechanism exit filter.

The ``Retriever`` protocol keeps the pipeline agnostic to the ranking backend
(plan §"Retriever 接口化"). ``BM25Retriever`` is the lexical v1; ``EmbeddingRetriever``
is the Phase-2 dense backend and slots in behind the same protocol, so the whole
pipeline (distill → analogy → specialize → entailment → dedup) runs unchanged and
the two produce comparable staging artifacts.

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
scorer; anchors and mechanism nouns never do — this holds for BOTH backends, so
the dense query embeds exactly the same text BM25 tokenizes.
"""

from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path
from typing import TYPE_CHECKING, Protocol

from rank_bm25 import BM25Okapi

from .schema import Chunk, Hit, SeedQuery

if TYPE_CHECKING:
    from .embedding import EmbeddingClient

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


class EmbeddingRetriever:
    """Dense retriever over ``doubao-embedding-vision`` cosine similarity.

    Corpus vectors are cached to ``cache_path`` keyed by a fingerprint of the
    embedding config + the concatenated chunk ids/text, so re-running a seed
    slice never re-embeds the 4.5k-chunk corpus. The query is embedded from the
    SAME ``query_terms()`` text BM25 tokenizes, so any accepted difference is a
    ranking-model difference, not an input difference.
    """

    def __init__(self, client: EmbeddingClient, *, cache_path: Path | None = None) -> None:
        self._client = client
        self._cache_path = cache_path
        # The embedding service is non-deterministic per call, so cache query
        # vectors keyed by exact query text to make retrieval reproducible.
        self._query_cache_path = (
            cache_path.with_name("query_vectors.json") if cache_path else None
        )
        self._query_cache: dict[str, list[float]] | None = None
        self._chunks: list[Chunk] = []
        self._matrix = None  # np.ndarray (n, dim), L2-normalized rows

    @staticmethod
    def _fingerprint(client: EmbeddingClient, chunks: list[Chunk]) -> str:
        h = hashlib.sha256()
        h.update(client._cfg.model.encode())
        h.update(str(client._cfg.dim).encode())
        for c in chunks:
            h.update(c.chunk_id.encode())
            h.update(str(len(c.text)).encode())
        return h.hexdigest()

    def index(self, chunks: list[Chunk]) -> None:
        import numpy as np

        self._chunks = list(chunks)
        if not self._chunks:
            self._matrix = None
            return

        fp = self._fingerprint(self._client, self._chunks)
        vectors = self._load_cache(fp)

        if vectors is None or len(vectors) < len(self._chunks):
            # Resume from a partial cache: only embed the not-yet-done tail.
            done = list(vectors) if vectors else []
            texts = [c.text for c in self._chunks]

            def _checkpoint(_start: int, vecs: list[list[float]]) -> None:
                done.extend(vecs)
                self._save_cache(fp, done)

            self._client.embed(texts[len(done) :], on_progress=_checkpoint)
            vectors = done
            self._save_cache(fp, vectors)

        mat = np.asarray(vectors, dtype=np.float32)
        norms = np.linalg.norm(mat, axis=1, keepdims=True)
        norms[norms == 0.0] = 1.0
        self._matrix = mat / norms

    def _load_cache(self, fingerprint: str) -> list[list[float]] | None:
        if not self._cache_path or not self._cache_path.exists():
            return None
        cached = json.loads(self._cache_path.read_text(encoding="utf-8"))
        if cached.get("fingerprint") != fingerprint:
            return None
        return cached.get("vectors")

    def _save_cache(self, fingerprint: str, vectors: list[list[float]]) -> None:
        if not self._cache_path:
            return
        self._cache_path.parent.mkdir(parents=True, exist_ok=True)
        self._cache_path.write_text(
            json.dumps({"fingerprint": fingerprint, "vectors": vectors}),
            encoding="utf-8",
        )

    def _embed_query(self, text: str) -> list[float]:
        """Embed a query, caching vectors by exact text for reproducible ranking."""
        if self._query_cache is None:
            self._query_cache = {}
            if self._query_cache_path and self._query_cache_path.exists():
                self._query_cache = json.loads(
                    self._query_cache_path.read_text(encoding="utf-8")
                )
        cached = self._query_cache.get(text)
        if cached is not None:
            return cached
        vec = self._client.embed([text])[0]
        self._query_cache[text] = vec
        if self._query_cache_path:
            self._query_cache_path.parent.mkdir(parents=True, exist_ok=True)
            self._query_cache_path.write_text(
                json.dumps(self._query_cache), encoding="utf-8"
            )
        return vec

    def search(self, query: SeedQuery, top_k: int) -> list[Hit]:
        import numpy as np

        if self._matrix is None:
            return []
        text = " ".join(query.query_terms()).strip()
        if not text:
            return []
        q = np.asarray(self._embed_query(text), dtype=np.float32)
        qn = np.linalg.norm(q)
        if qn == 0.0:
            return []
        q = q / qn
        scores = self._matrix @ q  # cosine similarity in [-1, 1]
        ranked = np.argsort(-scores)[:top_k]
        hits: list[Hit] = []
        for i in ranked:
            c = self._chunks[int(i)]
            hits.append(
                Hit(chunk_id=c.chunk_id, text=c.text, metadata=c.metadata, score=float(scores[i]))
            )
        return hits


class HybridRetriever:
    """Fuse ``BM25Retriever`` + ``EmbeddingRetriever`` by reciprocal rank fusion.

    Rationale (痛点 2): dense cosine is strong on paraphrase but blunt on the
    identifiers/opcodes that carry the signal in compiler code; BM25 is the
    reverse. RRF combines the two *rank* orders (not their incomparable raw
    scores) so a chunk that either backend ranks highly floats up, without
    tuning a score-scale mixing weight:

        fused(chunk) = Σ_backend 1 / (rrf_k + rank_backend(chunk))

    ``rrf_k`` (default 60, the Cormack et al. constant) damps the tail so a #1
    in one backend cannot be outvoted by a long run of mid-rank hits in the
    other. The returned ``Hit.score`` is the fused RRF score; ordering is what
    downstream (over-fetch → exit filter → top_k) consumes, so the absolute
    magnitude is immaterial. ``query_terms()`` is the sole input to BOTH legs,
    exactly as in the single-backend path, so the exit filter is unchanged.
    """

    def __init__(
        self, bm25: BM25Retriever, dense: EmbeddingRetriever, *, rrf_k: int = 60
    ) -> None:
        self._bm25 = bm25
        self._dense = dense
        self._rrf_k = rrf_k

    def index(self, chunks: list[Chunk]) -> None:
        self._bm25.index(chunks)
        self._dense.index(chunks)

    def search(self, query: SeedQuery, top_k: int) -> list[Hit]:
        # Over-fetch each leg so a chunk ranked highly by only one backend still
        # enters the fusion pool; RRF then re-ranks the union.
        pool = max(top_k, 1)
        bm25_hits = self._bm25.search(query, top_k=pool)
        dense_hits = self._dense.search(query, top_k=pool)
        fused = rrf_fuse([bm25_hits, dense_hits], rrf_k=self._rrf_k)
        return fused[:top_k]


def rrf_fuse(rankings: list[list[Hit]], *, rrf_k: int = 60) -> list[Hit]:
    """Reciprocal-rank-fuse several ranked hit lists into one, high→low.

    Each list is assumed already ordered best-first. A chunk's fused score is
    the sum of ``1 / (rrf_k + rank)`` over every list it appears in (rank is
    0-based). The first-seen ``Hit`` object carries the text/metadata; only its
    ``score`` is replaced by the fused value.
    """
    fused_score: dict[str, float] = {}
    seen: dict[str, Hit] = {}
    for ranking in rankings:
        for rank, hit in enumerate(ranking):
            fused_score[hit.chunk_id] = fused_score.get(hit.chunk_id, 0.0) + 1.0 / (
                rrf_k + rank
            )
            seen.setdefault(hit.chunk_id, hit)
    ordered = sorted(fused_score, key=lambda cid: fused_score[cid], reverse=True)
    return [seen[cid].model_copy(update={"score": fused_score[cid]}) for cid in ordered]


def is_cross_isa_sibling(query: SeedQuery, hit: Hit) -> bool:
    """True when ``hit`` is a same-mechanism sibling on a DIFFERENT concrete backend.

    The seed names the target(s) it lives on (``query.origin_isas``, e.g.
    mips/loongarch). A hit carrying a concrete backend ISA (not ``generic`` /
    target-less) that is NOT in that set is the sister-target implementation of
    the same mechanism — the highest-value cross-ISA lead and definitely not the
    seed's own site. This single predicate drives both the exit filter's exemption
    and the differential specialize/grounding path, so the two never diverge.
    """
    if hit.metadata.mechanism != query.origin_mechanism:
        return False
    origin_isas = {i.lower() for i in query.origin_isas if i}
    hit_isa = (hit.metadata.isa or "").lower()
    return bool(origin_isas and hit_isa and hit_isa != "generic" and hit_isa not in origin_isas)


def exit_filter(hits: list[Hit], query: SeedQuery) -> tuple[list[Hit], list[Hit]]:
    """Split hits into (survivors, dropped) per the cross-mechanism/ISA exit filter.

    A hit is "the seed rediscovering itself" and dropped when it is the seed's own
    mechanism, or its text contains any ``exact_anchor`` (a case-sensitive
    identifier match, so ``INV-FORT-O02`` / ``stack_protect_epilogue`` are caught
    but common words are not).

    The load-bearing change for cross-ISA discovery is an EXEMPTION carved ahead of
    both drops: a cross-ISA sibling (``is_cross_isa_sibling`` — same mechanism, a
    different concrete backend) cannot be the seed's own site, so it survives even
    though its mechanism matches and even though it shares mechanism-family symbols
    (``stack_protect_test`` lives on every backend, so anchoring on it would nuke
    the very references we want). A ``generic`` / target-less hit (the shared
    middle-end — which for an ISA-scoped seed IS its cited fallback defect site)
    still runs the normal self filters. A mechanism-neutral seed (no
    ``origin_isas``) never triggers the exemption, so the cross-mechanism path is
    unchanged.
    """
    survivors: list[Hit] = []
    dropped: list[Hit] = []
    anchors = [a for a in query.exact_anchors if a]
    for h in hits:
        if is_cross_isa_sibling(query, h):
            survivors.append(h)
            continue
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
