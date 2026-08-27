"""specgen stage 1 — signature (PropertyGPT-style) query construction.

The signature query mode must (a) route to ``distill_signature_query`` and feed
it the corpus function bodies named by the seed's anchors, and (b) fall back to
the abstract query when the seed has no corpus-resident body. Both are checked
with a stub judge so the test stays deterministic (no API).
"""

from __future__ import annotations

from pathlib import Path

from defuzz_loop.specgen.pipeline import PipelineConfig, _bodies_for_symbols, _distill
from defuzz_loop.specgen.query import QueryDistillation, SignatureDistillation
from defuzz_loop.specgen.schema import Chunk, ChunkMeta, Seed


class _StubJudge:
    """Records which task/prompt it saw and returns a fixed structured output."""

    def __init__(self) -> None:
        self.seen: list[tuple[str, str]] = []

    async def complete(self, *, task, key, system, user, output_model):
        self.seen.append((task, user))
        if output_model is SignatureDistillation:
            return SignatureDistillation(
                signature_phrase="a transient secret register is left live at the return edge",
                structure_tokens=["emit_move_insn", "clobber", "return"],
            )
        return QueryDistillation(root_cause_phrase="abstract fallback phrase", agnostic_tokens=[])


def _chunk(sym: str, text: str) -> Chunk:
    return Chunk(
        chunk_id=f"{sym}.cc:1",
        text=text,
        metadata=ChunkMeta(
            source_kind="source", mechanism="m", path=f"{sym}.cc", line=1, symbol=sym
        ),
    )


def _cfg(query_mode: str) -> PipelineConfig:
    return PipelineConfig(
        seed_sources=[], gcc_root=Path("."), findings_root=None, bugs_root=None,
        invariants_root=None, out_dir=Path("."), cache_root=Path("."),
        query_mode=query_mode,
    )


def test_bodies_for_symbols_collects_all_matching_chunks():
    by_sym = {"foo": [_chunk("foo", "AAA"), _chunk("foo", "BBB")], "bar": [_chunk("bar", "CCC")]}
    assert _bodies_for_symbols(by_sym, ["foo", "bar", "absent"]) == ["AAA", "BBB", "CCC"]


async def test_signature_mode_uses_function_bodies_when_anchor_resolves():
    seed = Seed(seed_id="S1", origin_mechanism="stack-protector",
                violated_invariant="canary must not survive the return",
                anchors=["stack_protect_epilogue"])
    by_sym = {
        "stack_protect_epilogue": [
            _chunk("stack_protect_epilogue", "emit_move_insn (x, y);")
        ]
    }
    judge = _StubJudge()
    sq = await _distill(_cfg("signature"), judge, seed, by_sym)
    # routed to signature distiller: phrase carries the enforcing guarantee + code tokens
    assert "return edge" in sq.root_cause_phrase
    assert "emit_move_insn" in sq.query_terms()
    # the buggy body was actually placed in the prompt
    assert "emit_move_insn (x, y);" in judge.seen[0][1]


async def test_signature_mode_falls_back_when_no_body_resolves():
    seed = Seed(seed_id="S2", origin_mechanism="bti", anchors=["nonexistent_symbol"])
    sq = await _distill(_cfg("signature"), _StubJudge(), seed, by_sym={})
    assert sq.root_cause_phrase == "abstract fallback phrase"


async def test_abstract_mode_ignores_bodies():
    seed = Seed(seed_id="S3", origin_mechanism="bti", anchors=["foo"])
    by_sym = {"foo": [_chunk("foo", "some body")]}
    sq = await _distill(_cfg("abstract"), _StubJudge(), seed, by_sym)
    assert sq.root_cause_phrase == "abstract fallback phrase"
