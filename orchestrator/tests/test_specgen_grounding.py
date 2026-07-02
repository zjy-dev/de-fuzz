"""specgen stages 4-5 — three-gate generate + two grounding gates.

Uses a stub Judge that returns per-task canned outputs (no live LLM), and
asserts:
- generate keeps a hit only when analogy holds AND the draft is falsifiable,
  program-injecting the evidence (path:line, snippet) instead of trusting the
  model;
- grounding accepts only when both gates pass, and rejects the deterministic
  falsifiability gate before spending an entailment call.
"""

from __future__ import annotations

from typing import TypeVar

from pydantic import BaseModel

from defuzz_loop.specgen.generate import generate_candidates
from defuzz_loop.specgen.grounding import ground_candidate, ground_candidates
from defuzz_loop.specgen.schema import (
    AnalogyJudgment,
    Candidate,
    CandidateDraft,
    ChunkMeta,
    Falsifiability,
    Hit,
    SeedQuery,
)

T = TypeVar("T", bound=BaseModel)


class _ScriptedJudge:
    """Return a canned output per task name (stage-specific)."""

    def __init__(self, by_task: dict[str, BaseModel]) -> None:
        self._by_task = by_task

    async def complete(self, *, task, key, system, user, output_model: type[T]) -> T:
        out = self._by_task[task]
        assert isinstance(out, output_model)
        return out


def _sq() -> SeedQuery:
    return SeedQuery(
        seed_id="DREV-2026-025",
        origin_mechanism="fortify-source",
        root_cause_phrase="a size-carrying value is narrowed before use as a bound",
        agnostic_tokens=["narrowing", "truncation"],
    )


def _hit(mechanism: str = "stack-clash-protection") -> Hit:
    return Hit(
        chunk_id="explow.cc:100:anti_adjust_stack",
        text="residual = (int) size;  /* narrowed before probe loop */",
        metadata=ChunkMeta(
            source_kind="source",
            mechanism=mechanism,
            compiler="GCC",
            version="gcc-16.1.0",
            isa="generic",
            path="explow.cc",
            line=100,
            symbol="anti_adjust_stack",
        ),
        score=3.2,
    )


_ANALOGY_YES = AnalogyJudgment(
    does_analogy_hold=True,
    aligned_operation="residual = (int) size narrows a wider size to 32-bit",
    protected_asset="the guard page probe bound",
    why_analogous="both narrow a wider size-carrying value to a fixed width",
)
_ANALOGY_NO = AnalogyJudgment(does_analogy_hold=False, why_analogous="lexical only")

_DRAFT_OK = CandidateDraft(
    statement="the residual stack size passed to the probe loop must retain its full width",
    observation="disassembly shows a 32-bit truncation of the residual before the probe loop",
    version_sensitivity="likely-to-drift",
    falsifiability=Falsifiability(
        observability="a wrong immediate / missing probe visible in disassembly",
        determinism="decisive",
        cost="static disasm",
        static_or_dynamic="static",
    ),
)
_DRAFT_UNFALSIFIABLE = CandidateDraft(
    statement="the mechanism should be correct",
    observation="",
    falsifiability=Falsifiability(observability=""),
)


async def test_generate_accepts_when_analogy_and_falsifiable() -> None:
    judge = _ScriptedJudge({"analogy": _ANALOGY_YES, "specialize": _DRAFT_OK})
    cands, rejected = await generate_candidates(judge, _sq(), [_hit()])
    assert rejected == []
    assert len(cands) == 1
    c = cands[0]
    # Cross-mechanism: hit mechanism differs from the seed's origin.
    assert c.origin_mechanism == "fortify-source"
    assert c.hit_mechanism == "stack-clash-protection"
    # Evidence is program-injected, not from the model.
    assert c.source_url_or_path == "explow.cc:100"
    assert c.evidence_snippet.startswith("residual = (int) size")
    assert c.analogy is not None and c.analogy.does_analogy_hold


async def test_generate_drops_when_analogy_fails() -> None:
    judge = _ScriptedJudge({"analogy": _ANALOGY_NO})
    cands, rejected = await generate_candidates(judge, _sq(), [_hit()])
    assert cands == []
    assert rejected[0].stage == "analogy"


async def test_generate_drops_unfalsifiable_draft() -> None:
    judge = _ScriptedJudge({"analogy": _ANALOGY_YES, "specialize": _DRAFT_UNFALSIFIABLE})
    cands, rejected = await generate_candidates(judge, _sq(), [_hit()])
    assert cands == []
    assert rejected[0].stage == "specialize"
    assert rejected[0].reason == "not-falsifiable"


def _candidate(*, observation: str, observability: str) -> Candidate:
    return Candidate(
        seed_id="DREV-2026-025",
        origin_mechanism="fortify-source",
        hit_mechanism="stack-clash-protection",
        statement="the residual stack size must retain its full width",
        observation=observation,
        source_url_or_path="explow.cc:100",
        evidence_snippet="residual = (int) size;  /* narrowed before probe loop */",
        falsifiability=Falsifiability(observability=observability),
        chunk_id="explow.cc:100:anti_adjust_stack",
    )


async def test_grounding_accepts_when_both_gates_pass() -> None:
    from defuzz_loop.specgen.grounding import EntailmentJudgment

    judge = _ScriptedJudge({"entailment": EntailmentJudgment(entailed=True, support="line 100")})
    c = _candidate(observation="32-bit truncation visible", observability="wrong immediate")
    result = await ground_candidate(judge, c)
    assert result.accepted
    assert result.evidence_entailed and result.falsifiable


async def test_grounding_rejects_non_falsifiable_before_judgment() -> None:
    # Empty observability → deterministic gate 2 fails; no judge call needed.
    class _BoomJudge:
        async def complete(self, **_kw):
            raise AssertionError("entailment must not run for a non-falsifiable candidate")

    c = _candidate(observation="", observability="")
    result = await ground_candidate(_BoomJudge(), c)
    assert not result.accepted
    assert result.reason.startswith("not-falsifiable")


async def test_grounding_rejects_when_not_entailed() -> None:
    from defuzz_loop.specgen.grounding import EntailmentJudgment

    judge = _ScriptedJudge(
        {"entailment": EntailmentJudgment(entailed=False, reason="written from memory")}
    )
    c = _candidate(observation="something visible", observability="a wrong immediate")
    accepted, rejected = await ground_candidates(judge, [c])
    assert accepted == []
    assert len(rejected) == 1
    assert rejected[0].grounding is not None
    assert not rejected[0].grounding.accepted
    assert "not-entailed" in rejected[0].grounding.reason
