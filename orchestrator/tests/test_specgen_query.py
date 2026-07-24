"""specgen stage 1 — query distillation + mechanism-leak negative check.

Uses a stub Judge (no live LLM) that returns a fixed QueryDistillation, and
asserts the produced SeedQuery is mechanism-agnostic (``mechanism_leak`` finds
nothing in the phrase/tokens) while ``exact_anchors`` — the exit-filter fuel —
IS program-injected from the seed and DOES keep the mechanism-specific ids.
"""

from __future__ import annotations

from typing import TypeVar

from pydantic import BaseModel

from defuzz_loop.specgen.query import (
    QueryDistillation,
    distill_query,
    mechanism_leak,
)
from defuzz_loop.specgen.schema import Seed

T = TypeVar("T", bound=BaseModel)


class _StubJudge:
    """Return a canned output regardless of prompt (no LLM)."""

    def __init__(self, output: BaseModel) -> None:
        self._output = output
        self.calls: list[tuple[str, str]] = []

    async def complete(self, *, task, key, system, user, output_model: type[T]) -> T:
        self.calls.append((task, key))
        assert isinstance(self._output, output_model)
        return self._output


_SEED = Seed(
    seed_id="DREV-2026-025",
    origin_mechanism="fortify-source",
    violated_invariant="INV-FORT-O02 — counted_by count must not be narrowed to int.",
    impact="signed count truncated to int over-reports the bound",
    why_not_rescued="baked at compile time into the _chk bound",
    notes="",
    anchors=["access_with_size_object_size", "counted_by", "INV-FORT-O02"],
)


def test_mechanism_leak_flags_mechanism_nouns() -> None:
    # The negative-test helper the plan requires: it must catch mechanism nouns.
    assert "counted_by" in mechanism_leak("a counted_by field is narrowed")
    assert "canary" in mechanism_leak("the CANARY value spills")
    # A clean mechanism-agnostic sentence leaks nothing.
    assert mechanism_leak("a size-carrying value is narrowed to a fixed-width type") == []


async def test_distill_produces_agnostic_query() -> None:
    clean = QueryDistillation(
        root_cause_phrase=(
            "a size-carrying value is narrowed to a fixed-width type before use as a bound"
        ),
        agnostic_tokens=["narrowing", "truncation", "sign-extension", "fixed-width", "bound"],
    )
    judge = _StubJudge(clean)
    sq = await distill_query(judge, _SEED)

    # Nothing that enters retrieval may carry a mechanism-specific noun.
    assert mechanism_leak(sq.root_cause_phrase) == []
    assert mechanism_leak(" ".join(sq.agnostic_tokens)) == []
    assert mechanism_leak(" ".join(sq.query_terms())) == []

    # The judge was keyed by seed_id (stable transcript key).
    assert judge.calls == [("distill_query", "DREV-2026-025")]


async def test_exact_anchors_injected_from_seed_not_judge() -> None:
    # Even if the judge's output were dirty, anchors come from the seed program-side.
    clean = QueryDistillation(root_cause_phrase="a value is narrowed", agnostic_tokens=["narrow"])
    sq = await distill_query(_StubJudge(clean), _SEED)

    # exit-filter fuel keeps the mechanism-specific identifiers.
    assert "INV-FORT-O02" in sq.exact_anchors
    assert "access_with_size_object_size" in sq.exact_anchors
    # ...but those anchors never enter the retrieval bag of terms.
    assert "INV-FORT-O02" not in sq.query_terms()
    assert "access_with_size_object_size" not in sq.query_terms()
