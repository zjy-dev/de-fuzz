"""Parallel campaign launcher tests. No LangGraph, no gRPC — pure asyncio."""

from __future__ import annotations

import asyncio

import pytest

from defuzz_loop.campaign.parallel import run_parallel_campaigns
from defuzz_loop.campaign.single import CampaignConfig


@pytest.mark.asyncio
async def test_parallel_gathers_all_results_in_order() -> None:
    configs = [
        CampaignConfig(phi_id=f"SP-{i:03d}", mechanism="stack_canary", budget_rounds=1)
        for i in range(5)
    ]

    async def _fake(cfg: CampaignConfig) -> dict:
        await asyncio.sleep(0.01)
        return {"phi_id": cfg.phi_id, "rounds": 1}

    results = await run_parallel_campaigns(configs, _fake, max_parallel=3)
    assert [r.phi_id for r in results] == [c.phi_id for c in configs]
    assert all(r.ok for r in results)
    assert results[2].final_state == {"phi_id": "SP-002", "rounds": 1}


@pytest.mark.asyncio
async def test_parallel_respects_semaphore_cap() -> None:
    """Verify that no more than ``max_parallel`` fake runners are in-flight."""

    max_parallel = 2
    live = 0
    peak = 0
    lock = asyncio.Lock()

    async def _fake(cfg: CampaignConfig) -> dict:
        nonlocal live, peak
        async with lock:
            live += 1
            peak = max(peak, live)
        await asyncio.sleep(0.02)
        async with lock:
            live -= 1
        return {"phi_id": cfg.phi_id}

    configs = [
        CampaignConfig(phi_id=f"P{i}", mechanism="canary", budget_rounds=1)
        for i in range(8)
    ]
    results = await run_parallel_campaigns(configs, _fake, max_parallel=max_parallel)
    assert peak <= max_parallel
    assert all(r.ok for r in results)


@pytest.mark.asyncio
async def test_parallel_isolates_failures() -> None:
    """One failing campaign must NOT cancel siblings; result list still fully populated."""

    async def _fake(cfg: CampaignConfig) -> dict:
        if cfg.phi_id == "BAD":
            raise RuntimeError("boom")
        return {"phi_id": cfg.phi_id}

    configs = [
        CampaignConfig(phi_id="OK1", mechanism="canary", budget_rounds=1),
        CampaignConfig(phi_id="BAD", mechanism="canary", budget_rounds=1),
        CampaignConfig(phi_id="OK2", mechanism="canary", budget_rounds=1),
    ]
    results = await run_parallel_campaigns(configs, _fake, max_parallel=4)
    assert [r.phi_id for r in results] == ["OK1", "BAD", "OK2"]
    assert results[0].ok
    assert not results[1].ok
    assert isinstance(results[1].error, RuntimeError)
    assert results[2].ok


@pytest.mark.asyncio
async def test_parallel_records_duration() -> None:
    async def _fake(cfg: CampaignConfig) -> dict:
        await asyncio.sleep(0.05)
        return {}

    configs = [CampaignConfig(phi_id="X", mechanism="cfi", budget_rounds=1)]
    results = await run_parallel_campaigns(configs, _fake, max_parallel=1)
    assert results[0].duration_secs >= 0.04


@pytest.mark.asyncio
async def test_parallel_rejects_bad_max_parallel() -> None:
    with pytest.raises(ValueError):
        await run_parallel_campaigns(
            [CampaignConfig(phi_id="X", mechanism="m", budget_rounds=1)],
            lambda cfg: asyncio.sleep(0),  # type: ignore[arg-type]
            max_parallel=0,
        )


def test_campaign_config_defaults() -> None:
    """Deterministic thread_id / recursion_limit derivations."""

    cfg = CampaignConfig(phi_id="SP-001", mechanism="stack_canary", budget_rounds=8)
    assert cfg.resolved_thread_id() == "campaign::stack_canary::SP-001"
    assert cfg.resolved_recursion_limit() == 8 * 8 + 10

    cfg2 = CampaignConfig(
        phi_id="X",
        mechanism="m",
        budget_rounds=4,
        thread_id="custom-tid",
        recursion_limit=999,
    )
    assert cfg2.resolved_thread_id() == "custom-tid"
    assert cfg2.resolved_recursion_limit() == 999
