"""Single-campaign wiring test (Rev 3 flavour).

The dependency-injection surface changed from ``build_graph`` +
``blackboard_factory`` to ``loop_builder`` + ``blackboard_factory``.
This test drives :func:`run_single_invariant_campaign` with a fake
IterationLoop stand-in to verify the target_queue and budget wiring.
"""

from __future__ import annotations

from typing import Any

import pytest

from defuzz_loop.campaign.single import (
    CampaignConfig,
    run_single_invariant_campaign,
)
from defuzz_loop.state import Blackboard


class _FakeLoop:
    def __init__(self) -> None:
        self.calls: list[tuple[Blackboard, dict[str, Any]]] = []

    async def run(
        self,
        bb: Blackboard,
        *,
        budget_rounds: int,
        budget_secs: float,
    ) -> Blackboard:
        self.calls.append(
            (bb, {"budget_rounds": budget_rounds, "budget_secs": budget_secs})
        )
        bb.round = 42
        return bb


@pytest.mark.asyncio
async def test_loop_receives_single_target_and_budget() -> None:
    loop = _FakeLoop()
    build_calls = {"n": 0}

    def _loop_builder() -> _FakeLoop:
        build_calls["n"] += 1
        return loop

    def _bb_factory(**kwargs: Any) -> Blackboard:
        return Blackboard(**kwargs)

    cfg = CampaignConfig(
        phi_id="SP-001",
        mechanism="stack_canary",
        budget_rounds=4,
        budget_secs=60.0,
    )

    result = await run_single_invariant_campaign(
        cfg,
        loop_builder=_loop_builder,  # type: ignore[arg-type]
        blackboard_factory=_bb_factory,
    )

    assert build_calls["n"] == 1
    assert result.ok is True
    assert result.phi_id == "SP-001"
    assert result.final_blackboard.round == 42

    initial_bb, kwargs = loop.calls[0]
    assert initial_bb.target_queue == ["SP-001"]
    assert kwargs == {"budget_rounds": 4, "budget_secs": 60.0}


@pytest.mark.asyncio
async def test_campaign_records_failure_without_crashing() -> None:
    class _Boom:
        async def run(self, bb: Blackboard, **_: Any) -> Blackboard:
            raise RuntimeError("oracle exploded")

    def _loop_builder() -> _Boom:
        return _Boom()

    def _bb_factory(**kwargs: Any) -> Blackboard:
        return Blackboard(**kwargs)

    result = await run_single_invariant_campaign(
        CampaignConfig(phi_id="X", mechanism="m", budget_rounds=1),
        loop_builder=_loop_builder,  # type: ignore[arg-type]
        blackboard_factory=_bb_factory,
    )
    assert result.ok is False
    assert isinstance(result.error, RuntimeError)


def test_campaign_config_defaults() -> None:
    cfg = CampaignConfig(phi_id="SP-001", mechanism="stack_canary", budget_rounds=8)
    assert cfg.resolved_thread_id() == "campaign::stack_canary::SP-001"
    assert cfg.resolved_recursion_limit() == 8 * 8 + 10
