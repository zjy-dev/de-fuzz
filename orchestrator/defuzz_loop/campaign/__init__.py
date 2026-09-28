"""Per-invariant campaign orchestration.

The existing ``_run`` in :mod:`defuzz_loop.cli` builds one full ``target_queue``
containing *every* checker for a mechanism and invokes the compiled LangGraph
**once** — internally the bump node sweeps sequentially through all targets.

This subpackage introduces a finer-grained unit of work:

- :func:`~defuzz_loop.campaign.single.run_single_invariant_campaign` — build
  a graph whose ``target_queue`` contains **exactly one** invariant ID and
  invoke it. Runs until the invariant's budget (``budget_rounds`` OR
  ``budget_secs``) is exhausted OR the oracle reports ``violated``, then
  returns the final :class:`Blackboard`.

- :func:`~defuzz_loop.campaign.parallel.run_parallel_campaigns` — launch
  many single-invariant campaigns concurrently with an
  :class:`asyncio.Semaphore` to cap fan-out.

- :mod:`~defuzz_loop.campaign.cli` — a ``defuzz-loop campaign`` sub-command
  that ties it all together with logging.

Rationale (see ``docs/tech-docs/architecture/opencode-migration-plan.md``):
each campaign is an *independent* Blackboard snapshot, so N campaigns share
no state and can be safely gathered with :func:`asyncio.gather`.
"""

from .parallel import CampaignResult, run_parallel_campaigns
from .single import CampaignConfig, run_single_invariant_campaign

__all__ = [
    "CampaignConfig",
    "CampaignResult",
    "run_single_invariant_campaign",
    "run_parallel_campaigns",
]
