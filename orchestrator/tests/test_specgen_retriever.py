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
    is_cross_isa_sibling,
    retrieve,
    rrf_fuse,
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


def _isa_hit(cid: str, mechanism: str, isa: str, text: str = "reference template") -> Hit:
    return Hit(
        chunk_id=cid,
        text=text,
        metadata=ChunkMeta(source_kind="source", mechanism=mechanism, isa=isa, path=cid, line=1),
        score=1.0,
    )


def _isa_query() -> SeedQuery:
    # An ISA-scoped seed: it lives on mips/loongarch, mechanism stack-protector.
    return SeedQuery(
        seed_id="DREV-2026-001",
        origin_mechanism="stack-protector",
        root_cause_phrase="the guard value must be clobbered before the success return",
        agnostic_tokens=["clobber", "guard"],
        exact_anchors=["stack_protect_test"],  # a family symbol on every backend
        origin_isas=["mips", "loongarch64"],
    )


def test_is_cross_isa_sibling_only_for_other_concrete_backend() -> None:
    q = _isa_query()
    # arm != {mips, loongarch64}, same mechanism → sibling.
    assert is_cross_isa_sibling(q, _isa_hit("arm.md:1", "stack-protector", "arm"))
    # generic is the shared middle-end fallback — never a sibling target.
    assert not is_cross_isa_sibling(q, _isa_hit("cfgexpand.cc:1", "stack-protector", "generic"))
    # one of the seed's own ISAs = the seed's own site, not a sibling.
    assert not is_cross_isa_sibling(q, _isa_hit("mips.md:1", "stack-protector", "mips"))
    # different mechanism is out of scope for the sibling exemption.
    assert not is_cross_isa_sibling(q, _isa_hit("arm.md:2", "cet", "arm"))
    # a mechanism-neutral seed (no origin_isas) never yields a sibling.
    assert not is_cross_isa_sibling(_query(), _isa_hit("arm.md:1", "fortify-source", "arm"))


def test_exit_filter_exempts_cross_isa_sibling_from_both_drops() -> None:
    q = _isa_query()
    hits = [
        # sibling reference: SAME mechanism AND carries the family anchor — the v1
        # filter would have double-killed it; the exemption keeps it.
        _isa_hit("arm.md:9481", "stack-protector", "arm",
                 '(define_expand "stack_protect_test" ...)'),
        # the seed's own middle-end fallback site (generic) still self-filters out.
        _isa_hit("cfgexpand.cc:6940", "stack-protector", "generic",
                 "generic stack_protect_test fallback"),
    ]
    survivors, dropped = exit_filter(hits, q)
    assert {h.chunk_id for h in survivors} == {"arm.md:9481"}
    assert {h.chunk_id for h in dropped} == {"cfgexpand.cc:6940"}


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


def test_rrf_fuse_rewards_agreement_across_backends() -> None:
    # bm25 ranks A>B; dense ranks B>C. B is the only chunk both backends return,
    # so RRF must float B above A (bm25's #1) and C (dense's #1).
    bm25 = [_hit("A", "a", "m", 9.0), _hit("B", "b", "m", 8.0)]
    dense = [_hit("B", "b", "m", 0.9), _hit("C", "c", "m", 0.8)]
    fused = rrf_fuse([bm25, dense], rrf_k=60)
    assert fused[0].chunk_id == "B"  # agreement wins over either single #1
    # B: 1/61 + 1/60 ≈ 0.0331; A: 1/60 ≈ 0.0167; C: 1/61 ≈ 0.0164.
    assert fused[0].score > fused[1].score
    # A chunk seen by only one backend still appears (union, not intersection).
    assert {"A", "B", "C"} == {h.chunk_id for h in fused}


def test_rrf_fuse_preserves_hit_payload() -> None:
    bm25 = [_hit("A", "alpha-text", "stack-clash", 5.0)]
    fused = rrf_fuse([bm25], rrf_k=60)
    assert fused[0].text == "alpha-text"
    assert fused[0].metadata.mechanism == "stack-clash"
