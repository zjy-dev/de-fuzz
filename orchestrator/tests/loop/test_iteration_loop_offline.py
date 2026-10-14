"""Rev 3 IterationLoop offline test.

Exercises the *real* :class:`IterationLoop` wiring (routing, permissions,
node factories) against stub agents and a stub :class:`CoreClient`. No
LangGraph, no model subprocess.

Assertions:

- Blackboard state advances phase-by-phase (current_seed, build_matrix,
  build_artifacts, coverage, verdict_history/last_verdict).
- R8 gate: a stub ``VIOLATED`` verdict causes the loop to invoke the
  minimizer (when supplied) and terminate immediately.
"""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any

import pytest

from defuzz_loop.loop import IterationLoop
from defuzz_loop.routing import CheckerCatalog
from defuzz_loop.state import (
    Aggregate,
    Blackboard,
    Guidance,
    InvariantResult,
    MinimizedPoC,
    OracleVerdict,
    Seed,
    SeedOrigin,
    Verdict,
)


class _StubGenerator:
    def __init__(self) -> None:
        self.calls = 0
        self.seen_targets: list[str | None] = []

    async def generate(self, bb: Blackboard) -> Seed:
        self.calls += 1
        self.seen_targets.append(bb.current_target())
        return Seed(
            id=f"seed{self.calls}",
            source="int main(void){return 0;}",
            selected_checkers=[],
            origin=SeedOrigin.GENERATOR,
        )


class _StubFeedback:
    def __init__(self) -> None:
        self.calls = 0

    async def summarize(self, bb: Blackboard) -> Guidance:
        self.calls += 1
        return Guidance(round=bb.round, summary=f"guidance-{self.calls}")


class _StubMinimizer:
    def __init__(self) -> None:
        self.calls = 0

    async def minimize(self, bb: Blackboard) -> MinimizedPoC:
        self.calls += 1
        assert bb.current_seed is not None
        return MinimizedPoC(
            original_seed_id=bb.current_seed.id,
            reduced_source="int main(void){return 0;}",
            still_triggers=True,
        )


class _StubClient:
    """Stub CoreClient returning empty/forced deterministic results."""

    def __init__(self, *, violated: bool) -> None:
        self._violated = violated
        self.analyze_calls = 0

    def list_checker_metadata(self) -> list:
        return []

    def build(self, seed: Any, cells: Any) -> list:
        return []

    def measure(self, artifacts: Any, cumulative_state: Any):
        return SimpleNamespace(cumulative_state=b"", delta_json="lines:+1")

    def analyze(self, seed: Any, artifacts: Any):
        self.analyze_calls += 1
        if self._violated:
            return SimpleNamespace(
                results=[],
                violated=True,
                failing_checker="INV-SP-G01",
                failing_isa="x86_64",
                evidence="forced",
            )
        return SimpleNamespace(
            results=[],
            violated=False,
            failing_checker="",
            failing_isa="",
            evidence="",
        )


def _build_loop(
    *,
    violated: bool,
    feedback: _StubFeedback | None = None,
    minimizer: _StubMinimizer | None = None,
) -> tuple[IterationLoop, _StubGenerator, _StubClient]:
    generator = _StubGenerator()
    client = _StubClient(violated=violated)
    catalog = CheckerCatalog(client)
    loop = IterationLoop(
        generator=generator,
        feedback=feedback,
        llm_oracle=None,
        minimizer=minimizer,
        client=client,
        catalog=catalog,
    )
    return loop, generator, client


@pytest.mark.asyncio
async def test_single_round_advances_blackboard_state() -> None:
    loop, generator, client = _build_loop(violated=False)
    bb = Blackboard(target_queue=["INV-A"])
    final = await loop.run(bb, budget_rounds=1, budget_secs=0.0)

    assert generator.calls == 1
    assert client.analyze_calls == 1
    assert final.current_seed is not None
    assert final.build_matrix is not None
    # No metadata → empty cells is OK; the invariant we care about is that
    # routing ran and wrote a matrix rather than leaving it None.
    assert final.coverage.last_delta == "lines:+1"
    assert len(final.verdict_history) == 1
    assert final.last_verdict is not None
    assert final.last_verdict.aggregate is Aggregate.NOT_VIOLATED


@pytest.mark.asyncio
async def test_multi_round_stays_on_same_target() -> None:
    """Single-invariant campaigns keep hitting target_queue[0]."""

    loop, generator, _ = _build_loop(violated=False)
    bb = Blackboard(target_queue=["INV-A"])
    await loop.run(bb, budget_rounds=3, budget_secs=0.0)
    assert generator.seen_targets == ["INV-A", "INV-A", "INV-A"]


@pytest.mark.asyncio
async def test_r8_gate_triggers_minimizer_and_terminates() -> None:
    minimizer = _StubMinimizer()
    loop, generator, client = _build_loop(violated=True, minimizer=minimizer)
    bb = Blackboard(target_queue=["INV-A"])
    final = await loop.run(bb, budget_rounds=5, budget_secs=0.0)

    # Terminated after the first round despite budget_rounds=5.
    assert generator.calls == 1
    assert client.analyze_calls == 1
    assert final.last_verdict is not None
    assert final.last_verdict.aggregate is Aggregate.VIOLATED
    # Minimizer was invoked exactly once and its PoC is on the blackboard.
    assert minimizer.calls == 1
    assert final.minimized_poc is not None
    assert final.minimized_poc.still_triggers is True


@pytest.mark.asyncio
async def test_feedback_invoked_on_not_violated_branch() -> None:
    feedback = _StubFeedback()
    loop, _, _ = _build_loop(violated=False, feedback=feedback)
    bb = Blackboard(target_queue=["INV-A"])
    await loop.run(bb, budget_rounds=2, budget_secs=0.0)
    # Two not-violated rounds → feedback each time.
    assert feedback.calls == 2


@pytest.mark.asyncio
async def test_feedback_skipped_on_violated_branch() -> None:
    feedback = _StubFeedback()
    minimizer = _StubMinimizer()
    loop, _, _ = _build_loop(
        violated=True, feedback=feedback, minimizer=minimizer
    )
    bb = Blackboard(target_queue=["INV-A"])
    await loop.run(bb, budget_rounds=5, budget_secs=0.0)
    # Violated → return immediately → feedback never called.
    assert feedback.calls == 0
    assert minimizer.calls == 1


def _v(id: str, verdict: Verdict, isa: str = "aarch64") -> InvariantResult:
    return InvariantResult(id=id, verdict=verdict, isa=isa)


@pytest.mark.asyncio
async def test_llm_oracle_only_promotes_on_confident_fail() -> None:
    """R8 spot check: the loop wires promote_verdict through llm_oracle_step."""

    from defuzz_loop.agents.llm_oracle import promote_verdict

    base = OracleVerdict(seed_id="s0", aggregate=Aggregate.NOT_VIOLATED)
    # PASS / NOT_APPLICABLE never promote.
    assert promote_verdict(base, [_v("X", Verdict.PASS)], "s0") is None
    assert (
        promote_verdict(base, [_v("X", Verdict.NOT_APPLICABLE)], "s0") is None
    )
    # FAIL promotes to VIOLATED with the failing checker recorded.
    promoted = promote_verdict(base, [_v("X", Verdict.FAIL)], "s0")
    assert promoted is not None
    assert promoted.aggregate is Aggregate.VIOLATED
    assert promoted.failing_checker == "X"
