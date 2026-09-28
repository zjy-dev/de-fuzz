"""Single-campaign wiring test: does :func:`run_single_invariant_campaign`
correctly wire ``target_queue = [phi_id]`` and forward the budget?

No real LangGraph — we substitute fakes for every dependency.
"""

from __future__ import annotations

from typing import Any

import pytest

from defuzz_loop.campaign.single import CampaignConfig, run_single_invariant_campaign


class _FakeApp:
    def __init__(self, out: dict) -> None:
        self._out = out
        self.invocations: list[tuple[Any, dict]] = []

    async def ainvoke(self, initial: Any, config: dict) -> dict:
        self.invocations.append((initial, config))
        return {**self._out, "initial": initial, "config": config}


class _FakeGraph:
    def __init__(self, out: dict) -> None:
        self._out = out
        self.compile_kwargs: dict = {}

    def compile(self, **kwargs: Any) -> _FakeApp:
        self.compile_kwargs = kwargs
        self.app = _FakeApp(self._out)
        return self.app


@pytest.mark.asyncio
async def test_single_campaign_builds_graph_with_expected_target_queue() -> None:
    build_calls: list[dict] = []

    fake_graph = _FakeGraph(out={"final": True})

    def _build_graph(**kwargs: Any) -> _FakeGraph:
        build_calls.append(kwargs)
        return fake_graph

    def _bb_factory(**kwargs: Any) -> dict:
        return {"blackboard_kwargs": kwargs}

    cfg = CampaignConfig(
        phi_id="SP-001",
        mechanism="stack_canary",
        budget_rounds=4,
        budget_secs=60.0,
    )

    result = await run_single_invariant_campaign(
        cfg,
        build_graph=_build_graph,
        generator="fake-gen",
        catalog="fake-cat",
        client="fake-client",
        blackboard_factory=_bb_factory,
        feedback=None,
        minimizer=None,
        llm_oracle=None,
        checkpointer=None,
    )

    # build_graph got exactly the campaign's budget.
    assert len(build_calls) == 1
    assert build_calls[0]["budget_rounds"] == 4
    assert build_calls[0]["budget_secs"] == 60.0
    assert build_calls[0]["generator"] == "fake-gen"

    # Blackboard was seeded with the single phi_id in target_queue.
    initial, invoke_cfg = fake_graph.app.invocations[0]
    assert initial == {"blackboard_kwargs": {"target_queue": ["SP-001"]}}

    # Thread id + recursion limit are derived from the campaign config.
    assert invoke_cfg["configurable"]["thread_id"] == "campaign::stack_canary::SP-001"
    assert invoke_cfg["recursion_limit"] == cfg.resolved_recursion_limit()

    # Result is the fake graph's response, decorated with what we saw.
    assert result["final"] is True


@pytest.mark.asyncio
async def test_single_campaign_forwards_checkpointer_when_supplied() -> None:
    fake_graph = _FakeGraph(out={})

    def _build_graph(**kwargs: Any) -> _FakeGraph:
        return fake_graph

    def _bb_factory(**kwargs: Any) -> dict:
        return {}

    sentinel = object()

    await run_single_invariant_campaign(
        CampaignConfig(phi_id="X", mechanism="m", budget_rounds=1),
        build_graph=_build_graph,
        generator=None,
        catalog=None,
        client=None,
        blackboard_factory=_bb_factory,
        checkpointer=sentinel,
    )
    assert fake_graph.compile_kwargs.get("checkpointer") is sentinel


@pytest.mark.asyncio
async def test_single_campaign_omits_checkpointer_when_none() -> None:
    fake_graph = _FakeGraph(out={})

    def _build_graph(**kwargs: Any) -> _FakeGraph:
        return fake_graph

    def _bb_factory(**kwargs: Any) -> dict:
        return {}

    await run_single_invariant_campaign(
        CampaignConfig(phi_id="X", mechanism="m", budget_rounds=1),
        build_graph=_build_graph,
        generator=None,
        catalog=None,
        client=None,
        blackboard_factory=_bb_factory,
        checkpointer=None,
    )
    assert "checkpointer" not in fake_graph.compile_kwargs
