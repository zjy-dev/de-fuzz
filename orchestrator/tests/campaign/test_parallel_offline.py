"""Parallel campaign launcher tests (Rev 3).

The per-campaign runner now returns :class:`CampaignResult` directly
(no more raw ``ainvoke`` output). Failures inside the runner are
recorded via ``ok=False`` + ``error=...`` rather than being raised out
of the semaphore. Order and semaphore-cap guarantees are unchanged.
"""

from __future__ import annotations

import asyncio

import pytest

from defuzz_loop.campaign.parallel import run_parallel_campaigns
from defuzz_loop.campaign.single import CampaignConfig, CampaignResult
from defuzz_loop.state import Blackboard


def _ok_result(phi_id: str) -> CampaignResult:
    return CampaignResult(
        phi_id=phi_id,
        final_blackboard=Blackboard(),
        ok=True,
        duration_secs=0.01,
    )


@pytest.mark.asyncio
async def test_parallel_gathers_all_results_in_order() -> None:
    configs = [
        CampaignConfig(phi_id=f"SP-{i:03d}", mechanism="stack_canary", budget_rounds=1)
        for i in range(5)
    ]

    async def _fake(cfg: CampaignConfig) -> CampaignResult:
        await asyncio.sleep(0.005)
        return _ok_result(cfg.phi_id)

    results = await run_parallel_campaigns(configs, _fake, max_parallel=3)
    assert [r.phi_id for r in results] == [c.phi_id for c in configs]
    assert all(r.ok for r in results)


@pytest.mark.asyncio
async def test_parallel_respects_semaphore_cap() -> None:
    max_parallel = 2
    live = 0
    peak = 0
    lock = asyncio.Lock()

    async def _fake(cfg: CampaignConfig) -> CampaignResult:
        nonlocal live, peak
        async with lock:
            live += 1
            peak = max(peak, live)
        await asyncio.sleep(0.02)
        async with lock:
            live -= 1
        return _ok_result(cfg.phi_id)

    configs = [
        CampaignConfig(phi_id=f"P{i}", mechanism="canary", budget_rounds=1)
        for i in range(8)
    ]
    results = await run_parallel_campaigns(configs, _fake, max_parallel=max_parallel)
    assert peak <= max_parallel
    assert all(r.ok for r in results)


@pytest.mark.asyncio
async def test_parallel_isolates_failures_when_runner_raises() -> None:
    async def _fake(cfg: CampaignConfig) -> CampaignResult:
        if cfg.phi_id == "BAD":
            raise RuntimeError("boom")
        return _ok_result(cfg.phi_id)

    configs = [
        CampaignConfig(phi_id="OK1", mechanism="canary", budget_rounds=1),
        CampaignConfig(phi_id="BAD", mechanism="canary", budget_rounds=1),
        CampaignConfig(phi_id="OK2", mechanism="canary", budget_rounds=1),
    ]
    results = await run_parallel_campaigns(configs, _fake, max_parallel=4)
    assert [r.phi_id for r in results] == ["OK1", "BAD", "OK2"]
    assert results[0].ok is True
    assert results[1].ok is False
    assert isinstance(results[1].error, RuntimeError)
    assert results[2].ok is True


@pytest.mark.asyncio
async def test_parallel_rejects_bad_max_parallel() -> None:
    async def _noop(_: CampaignConfig) -> CampaignResult:
        return _ok_result("x")

    with pytest.raises(ValueError):
        await run_parallel_campaigns(
            [CampaignConfig(phi_id="X", mechanism="m", budget_rounds=1)],
            _noop,
            max_parallel=0,
        )
