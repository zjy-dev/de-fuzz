"""Per-invariant campaign orchestration (Rev 3).

Historical revisions ran one full-catalog LangGraph invocation whose
internal bump node swept every checker. Rev 3 introduces a finer-grained
unit of work:

- :func:`~defuzz_loop.campaign.single.run_single_invariant_campaign` —
  build an :class:`IterationLoop` whose ``target_queue`` contains
  **exactly one** invariant ID and drive it. Runs until the invariant's
  budget (``budget_rounds`` OR ``budget_secs``) is exhausted OR the
  oracle reports ``violated``, then returns a :class:`CampaignResult`.

- :func:`~defuzz_loop.campaign.parallel.run_parallel_campaigns` — launch
  many single-invariant campaigns concurrently with an
  :class:`asyncio.Semaphore` cap.

- :mod:`~defuzz_loop.campaign.cli` — the ``defuzz-loop campaign``
  sub-command wiring production dependencies.
"""

from .parallel import CampaignRunner, run_parallel_campaigns
from .single import (
    CampaignConfig,
    CampaignResult,
    LoopFactory,
    run_single_invariant_campaign,
)

__all__ = [
    "CampaignConfig",
    "CampaignResult",
    "CampaignRunner",
    "LoopFactory",
    "run_single_invariant_campaign",
    "run_parallel_campaigns",
]
