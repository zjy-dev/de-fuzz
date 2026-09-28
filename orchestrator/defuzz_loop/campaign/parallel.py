"""Parallel launcher for many single-invariant campaigns.

Explicit multi-agent orchestration (the user's requirement) is achieved
by two layers:

1. **Intra-campaign** — the LangGraph fixed edge order
   ``generator → routing → build → coverage → oracle → …`` (see
   :mod:`defuzz_loop.graph`). This is the *explicit* multi-agent flow.
2. **Inter-campaign** — this module launches N of those graphs in parallel
   with :func:`asyncio.gather`, one per invariant, with an
   :class:`asyncio.Semaphore` capping the fan-out.

Each campaign has an independent :class:`Blackboard`, an independent
LangGraph checkpointer thread, and an independent gRPC/MCP address (all
supplied by the caller). No shared state between campaigns.
"""

from __future__ import annotations

import asyncio
import logging
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any

from .single import CampaignConfig, run_single_invariant_campaign

log = logging.getLogger(__name__)


@dataclass
class CampaignResult:
    """Outcome of a single-invariant campaign."""

    phi_id: str
    ok: bool
    """``True`` if the campaign returned a final state; ``False`` if it
    raised. See :attr:`error` for the exception when ``ok`` is ``False``."""

    final_state: Any = None
    """The raw ``ainvoke`` return value (typically a dict). Callers wrap
    it in ``Blackboard.model_validate(final_state)``. ``None`` on error."""

    error: BaseException | None = None
    duration_secs: float = 0.0


CampaignRunner = Callable[[CampaignConfig], Awaitable[Any]]
"""Signature of the per-campaign entry point.

The default runner is a thin adapter over
:func:`~defuzz_loop.campaign.single.run_single_invariant_campaign`; tests
substitute a mock so we can validate the parallel launcher without spinning
up LangGraph."""


async def run_parallel_campaigns(
    configs: list[CampaignConfig],
    runner: CampaignRunner,
    *,
    max_parallel: int = 4,
) -> list[CampaignResult]:
    """Launch every campaign in ``configs`` concurrently, capped by
    ``max_parallel``.

    Guarantees:

    - The output list is ordered to match ``configs`` (index-preserving).
    - Failures don't cancel siblings — each :class:`CampaignResult` records
      its own ``ok`` / ``error``.
    - The semaphore ensures at most ``max_parallel`` campaigns are running
      at any instant; extras queue up.

    Parameters
    ----------
    configs:
        One :class:`CampaignConfig` per invariant to test.
    runner:
        Async callable that actually runs one campaign. Production callers
        pass a lambda that captures the shared LangGraph/gRPC dependencies
        (see ``defuzz_loop.campaign.cli``); tests pass a fake.
    max_parallel:
        Semaphore capacity. Choose based on Go-core gRPC pool size and LLM
        rate limits; default 4 is a conservative starting point.
    """

    if max_parallel < 1:
        raise ValueError(f"max_parallel must be >= 1, got {max_parallel}")

    sem = asyncio.Semaphore(max_parallel)

    async def _one(cfg: CampaignConfig) -> CampaignResult:
        started = time.monotonic()
        async with sem:
            log.info("campaign %s: start", cfg.phi_id)
            try:
                final = await runner(cfg)
            except BaseException as e:  # noqa: BLE001 — record and continue
                elapsed = time.monotonic() - started
                log.exception("campaign %s: FAILED after %.1fs", cfg.phi_id, elapsed)
                return CampaignResult(
                    phi_id=cfg.phi_id,
                    ok=False,
                    error=e,
                    duration_secs=elapsed,
                )
            elapsed = time.monotonic() - started
            log.info("campaign %s: done in %.1fs", cfg.phi_id, elapsed)
            return CampaignResult(
                phi_id=cfg.phi_id,
                ok=True,
                final_state=final,
                duration_secs=elapsed,
            )

    tasks = [asyncio.create_task(_one(cfg)) for cfg in configs]
    return await asyncio.gather(*tasks)


def build_default_runner(
    *,
    build_graph: Any,
    generator: Any,
    catalog: Any,
    client: Any,
    blackboard_factory: Any,
    feedback: Any | None = None,
    minimizer: Any | None = None,
    llm_oracle: Any | None = None,
    checkpointer: Any | None = None,
) -> CampaignRunner:
    """Curry the heavy dependencies into a :class:`CampaignRunner`.

    Production code passes real ``build_graph``, ``GeneratorAgent``,
    ``CheckerCatalog``, ``CoreClient`` etc.; the returned callable is what
    :func:`run_parallel_campaigns` calls for each ``CampaignConfig``.
    """

    async def _runner(cfg: CampaignConfig) -> Any:
        return await run_single_invariant_campaign(
            cfg,
            build_graph=build_graph,
            generator=generator,
            catalog=catalog,
            client=client,
            blackboard_factory=blackboard_factory,
            feedback=feedback,
            minimizer=minimizer,
            llm_oracle=llm_oracle,
            checkpointer=checkpointer,
        )

    return _runner
