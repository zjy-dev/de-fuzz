"""Single-invariant fixed workflow.

One invariant → one campaign → one final :class:`Blackboard`. The compiled
LangGraph is built with ``target_queue = [phi_id]`` so the bump node has
exactly one thing to sweep, and the campaign terminates when:

- the oracle promotes the aggregate to ``violated`` (bug found), OR
- the invariant's ``budget_rounds`` OR ``budget_secs`` is exhausted.

The heavy dependencies (:class:`GeneratorAgent`, :class:`CoreClient`, etc.)
are **injected**, not constructed here — this keeps the module unit-testable
with fakes and lets both the production CLI and the parallel launcher share
the same code path.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Protocol


class _GraphBuilder(Protocol):
    """Structural type for the graph-building callable.

    Matches :func:`defuzz_loop.graph.build_graph`, but we accept anything
    with the same shape so tests can pass a stub returning a fake graph.
    """

    def __call__(
        self,
        *,
        generator: Any,
        catalog: Any,
        client: Any,
        budget_rounds: int,
        budget_secs: float,
        feedback: Any | None = ...,
        minimizer: Any | None = ...,
        llm_oracle: Any | None = ...,
    ) -> Any: ...


class _BlackboardFactory(Protocol):
    """Callable producing an initial :class:`Blackboard` for a given
    ``target_queue`` and other run parameters. Matches the ``Blackboard``
    pydantic constructor."""

    def __call__(self, **kwargs: Any) -> Any: ...


@dataclass(frozen=True)
class CampaignConfig:
    """Everything :func:`run_single_invariant_campaign` needs.

    Attributes
    ----------
    phi_id:
        The single checker/invariant ID to attack this campaign.
    mechanism:
        Defense mechanism family (e.g. ``"stack_canary"``). Forwarded to the
        agents; determines which checker family the campaign operates within.
    budget_rounds:
        Hard cap on the number of ``generator → routing → build → coverage →
        oracle`` rounds spent on this invariant.
    budget_secs:
        Wall-clock cap in seconds. ``0`` disables. Whichever of the two caps
        hits first stops the campaign.
    thread_id:
        LangGraph checkpointer thread ID. Should be unique per campaign so
        parallel campaigns don't overwrite each other's checkpoints.
    recursion_limit:
        LangGraph recursion cap. Defaults to ``8 * budget_rounds + 10``
        (matches the calculation in the existing CLI).
    """

    phi_id: str
    mechanism: str
    budget_rounds: int
    budget_secs: float = 0.0
    thread_id: str | None = None
    recursion_limit: int | None = None

    def resolved_thread_id(self) -> str:
        return self.thread_id or f"campaign::{self.mechanism}::{self.phi_id}"

    def resolved_recursion_limit(self) -> int:
        if self.recursion_limit is not None:
            return self.recursion_limit
        return 8 * max(1, self.budget_rounds) + 10


async def run_single_invariant_campaign(
    config: CampaignConfig,
    *,
    build_graph: _GraphBuilder,
    generator: Any,
    catalog: Any,
    client: Any,
    blackboard_factory: _BlackboardFactory,
    feedback: Any | None = None,
    minimizer: Any | None = None,
    llm_oracle: Any | None = None,
    checkpointer: Any | None = None,
) -> Any:
    """Build and invoke a single-invariant LangGraph campaign.

    Returns the final blackboard state (whatever ``ainvoke`` returns —
    typically a dict for LangGraph's default reducer). Callers usually
    wrap it in ``Blackboard.model_validate(final)``.

    All heavy dependencies are injected so the function has no import-time
    coupling to LangGraph or gRPC — that keeps the parallel launcher and
    unit tests lean.
    """

    graph = build_graph(
        generator=generator,
        catalog=catalog,
        client=client,
        budget_rounds=config.budget_rounds,
        budget_secs=config.budget_secs,
        feedback=feedback,
        minimizer=minimizer,
        llm_oracle=llm_oracle,
    )

    initial = blackboard_factory(target_queue=[config.phi_id])

    compile_kwargs: dict[str, Any] = {}
    if checkpointer is not None:
        compile_kwargs["checkpointer"] = checkpointer
    app = graph.compile(**compile_kwargs)

    invoke_config: dict[str, Any] = {
        "configurable": {"thread_id": config.resolved_thread_id()},
        "recursion_limit": config.resolved_recursion_limit(),
    }

    return await app.ainvoke(initial, config=invoke_config)
