"""Parallel launcher — Rev 3 flavour.

Runs many single-invariant campaigns concurrently with an
:class:`asyncio.Semaphore` cap. The intra-campaign explicit workflow
is now :class:`IterationLoop` (Rev 3) instead of the LangGraph fixed
edge order, but the inter-campaign contract is unchanged: each campaign
owns an independent :class:`Blackboard`, index-preserving ordering is
guaranteed, and one failure never cancels its siblings.
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Awaitable, Callable

from .single import CampaignConfig, CampaignResult

log = logging.getLogger(__name__)


CampaignRunner = Callable[[CampaignConfig], Awaitable[CampaignResult]]
"""Signature of the per-campaign entry point.

Production callers pass a lambda that captures the shared dependencies;
tests substitute a fake so the parallel launcher can be validated
without spinning up an :class:`IterationLoop`."""


async def run_parallel_campaigns(
    configs: list[CampaignConfig],
    runner: CampaignRunner,
    *,
    max_parallel: int = 4,
) -> list[CampaignResult]:
    """Launch every campaign concurrently, capped by ``max_parallel``.

    Guarantees:

    - Output list is index-preserved w.r.t. ``configs``.
    - One failure never cancels siblings — each :class:`CampaignResult`
      records its own ``ok`` / ``error``.
    - At most ``max_parallel`` campaigns are in flight at once.
    """

    if max_parallel < 1:
        raise ValueError(f"max_parallel must be >= 1, got {max_parallel}")

    sem = asyncio.Semaphore(max_parallel)

    async def _one(cfg: CampaignConfig) -> CampaignResult:
        async with sem:
            log.info("campaign %s: start", cfg.phi_id)
            try:
                result = await runner(cfg)
            except BaseException as e:  # noqa: BLE001 — record and continue
                log.exception("campaign %s: FAILED", cfg.phi_id)
                from ..state import Blackboard  # local import — cheap

                return CampaignResult(
                    phi_id=cfg.phi_id,
                    final_blackboard=Blackboard(),
                    ok=False,
                    error=e,
                    duration_secs=0.0,
                )
            log.info(
                "campaign %s: done in %.1fs ok=%s",
                cfg.phi_id,
                result.duration_secs,
                result.ok,
            )
            return result

    tasks = [asyncio.create_task(_one(cfg)) for cfg in configs]
    return await asyncio.gather(*tasks)


__all__ = ["CampaignRunner", "run_parallel_campaigns"]
