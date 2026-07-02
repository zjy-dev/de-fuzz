"""specgen stage 3 — BM25 ranking determinism + cross-mechanism exit filter.

Two independent guarantees:
- BM25 over the custom tokenizer gives a deterministic, sensible ranking on a
  small fixed corpus (the sub-token split lets "narrow" match "narrowing").
- the exit filter drops (a) same-mechanism hits (rediscovering your own
  mechanism) and (b) hits containing an exact anchor (rediscovering the seed).
"""

from __future__ import annotations

from defuzz_loop.specgen.retriever import (
    BM25Retriever,
    exit_filter,
    retrieve,
    tokenize,
)
from defuzz_loop.specgen.schema import Chunk, ChunkMeta, Hit, SeedQuery


def _chunk(cid: str, text: str, mechanism: str) -> Chunk:
    return Chunk(
        chunk_id=cid,
        text=text,
        metadata=ChunkMeta(source_kind="source", mechanism=mechanism, path=cid, line=1),
    )


def _hit(cid: str, text: str, mechanism: str, score: float = 1.0) -> Hit:
    return Hit(
        chunk_id=cid,
        text=text,
        metadata=ChunkMeta(source_kind="source", mechanism=mechanism, path=cid, line=1),
        score=score,
    )


_CORPUS = [
    _chunk("sc:1", "the residual stack size is narrowed to a 32-bit type before the probe loop",
           "stack-clash-protection"),
    _chunk("cet:1", "an indirect branch target marker endbr must precede the landing pad",
           "cet"),
    _chunk("fort:1", "the counted_by field is narrowed to int in access_with_size_object_size",
           "fortify-source"),
    _chunk("pac:1", "a signed pointer autiasp check on the return address", "pac-ret"),
]


def _query() -> SeedQuery:
    return SeedQuery(
        seed_id="DREV-2026-025",
        origin_mechanism="fortify-source",
        root_cause_phrase="a size-carrying value is narrowed before use as a bound",
        agnostic_tokens=["narrowing", "truncation", "stack", "size"],
        exact_anchors=["access_with_size_object_size", "INV-FORT-O02"],
    )


def test_tokenizer_keeps_whole_and_sub_tokens() -> None:
    toks = tokenize("access_with_size_object_size")
    assert "access_with_size_object_size" in toks  # whole identifier
    assert "size" in toks and "object" in toks  # sub-tokens


def test_bm25_ranking_is_deterministic_and_relevant() -> None:
    r = BM25Retriever()
    r.index(_CORPUS)
    q = _query()
    hits_a = r.search(q, top_k=4)
    hits_b = r.search(q, top_k=4)
    assert [h.chunk_id for h in hits_a] == [h.chunk_id for h in hits_b]
    # The stack-clash chunk shares "narrowed"/"stack"/"size" → it must rank.
    assert hits_a[0].chunk_id == "sc:1"
    # The pac chunk shares nothing with the query terms → excluded (score<=0).
    assert "pac:1" not in {h.chunk_id for h in hits_a}


def test_exit_filter_drops_same_mechanism() -> None:
    hits = [_hit("fort:1", "some fortify text", "fortify-source"),
            _hit("cet:1", "endbr marker", "cet")]
    survivors, dropped = exit_filter(hits, _query())
    assert {h.chunk_id for h in survivors} == {"cet:1"}
    assert {h.chunk_id for h in dropped} == {"fort:1"}  # origin mechanism dropped


def test_exit_filter_drops_exact_anchor_hit() -> None:
    # A sister-mechanism hit whose text contains an exact anchor = rediscovering seed.
    hits = [_hit("x:1", "code calling access_with_size_object_size here", "stack-clash-protection")]
    survivors, dropped = exit_filter(hits, _query())
    assert survivors == []
    assert dropped[0].chunk_id == "x:1"


def test_retrieve_over_fetches_then_trims_to_top_k() -> None:
    r = BM25Retriever()
    r.index(_CORPUS)
    survivors, _dropped = retrieve(r, _query(), top_k=1)
    # top_k survivors after the fortify (origin) hit is filtered out.
    assert len(survivors) == 1
    assert survivors[0].metadata.mechanism != "fortify-source"


def test_empty_corpus_returns_no_hits() -> None:
    r = BM25Retriever()
    r.index([])
    assert r.search(_query(), top_k=5) == []
