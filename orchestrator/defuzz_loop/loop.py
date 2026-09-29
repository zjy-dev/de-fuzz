"""Rev 3 iteration loop — the explicit-Python replacement for LangGraph.

Historical context
==================

Prior revisions used LangGraph's :class:`StateGraph` to wire the fixed edge
order ``generator → routing → build → coverage → oracle → (llm_oracle?) →
[violated|loop|stop]``. LangGraph gave us checkpointing + conditional edges
but the actual DAG never branched at runtime (only the terminal router
did), so the framework paid an outsized dependency cost for a single
conditional. Rev 3 collapses the whole thing into ~200 lines of explicit
Python (this file) and moves the LLM backends to OpenCode markdown
subagents (see :mod:`defuzz_loop.opencode`).

The **R8 zero-false-positives gate** is what this loop must preserve:

- Only the deterministic Go oracle produces bug records normally.
- The LLM oracle only ever *adds* information for invariants the
  deterministic oracle returned NA/Error on; a promotion to
  ``violated`` requires a confident LLM ``FAIL`` (already enforced inside
  :class:`defuzz_loop.agents.llm_oracle.LLMOracleAgent`).
- Write-permission matrix (see :mod:`defuzz_loop.permissions`) is enforced
  at every phase boundary via :func:`~defuzz_loop.permissions._check`
  (called directly here — we cannot use the ``guard`` wrapper because it
  cannot distinguish an async lambda that *returns* a coroutine from a
  plain sync callable).
- Budget is ``(rounds AND secs)`` — first cap hit terminates.

Node reuse
==========

The node bodies for build / coverage / oracle come straight from
:mod:`defuzz_loop.nodes`; we do not fork them. Routing reuses
:func:`defuzz_loop.routing.make_routing_node`. Only the LangGraph glue
disappears.
"""

from __future__ import annotations

import inspect
import logging
import time
from collections.abc import Callable
from typing import TYPE_CHECKING, Any

from .nodes.build import make_build_node
from .nodes.coverage import make_coverage_node
from .nodes.oracle import bug_evidence, make_oracle_node
from .permissions import _check as _check_permissions
from .routing import CheckerCatalog, make_routing_node
from .state import Aggregate, Blackboard

if TYPE_CHECKING:  # pragma: no cover — typing only
    from .agents.feedback import FeedbackAgent
    from .agents.generator import GeneratorAgent
    from .agents.llm_oracle import LLMOracleAgent
    from .agents.minimizer import MinimizerAgent
    from .clients.grpc_client import CoreClient

log = logging.getLogger(__name__)


NodeFn = Callable[[Blackboard], Any]


def _apply_update(bb: Blackboard, update: dict[str, Any] | None) -> None:
    """Merge a node's ``dict`` update onto the blackboard in-place.

    LangGraph's default reducer overwrites channel values; we mirror that
    behaviour by ``setattr``-ing each key. Missing / ``None`` / empty updates
    are no-ops, matching the previous graph semantics.
    """

    if not update:
        return
    for key, value in update.items():
        setattr(bb, key, value)


async def _run_phase(phase: str, fn: NodeFn, bb: Blackboard) -> dict[str, Any] | None:
    """Execute a phase, enforce the write-permission matrix, merge onto bb.

    Accepts both sync callables (``dict`` return) and async callables /
    coroutine-returning callables (``Awaitable[dict]`` return). Uses
    :func:`defuzz_loop.permissions._check` for matrix validation so the
    ``guard`` wrapper's ``iscoroutinefunction`` heuristic is bypassed —
    lambdas around bound async methods otherwise slip through as sync.
    """

    result = fn(bb)
    if inspect.isawaitable(result):
        result = await result
    validated = _check_permissions(phase, result)
    _apply_update(bb, validated)  # type: ignore[arg-type]
    return validated  # type: ignore[return-value]


class IterationLoop:
    """Explicit Python orchestrator, replacing ``graph.build_graph``.

    Parameters
    ----------
    generator:
        Required. Produces the seed for each round via ``await
        generator.generate(bb)``.
    feedback, minimizer, llm_oracle:
        Optional; matching the previous ``--disable-agent`` flags.
    client:
        gRPC :class:`CoreClient` for build / coverage / oracle.
    catalog:
        :class:`CheckerCatalog` for deterministic routing.
    feedback_stride:
        Invoke the feedback agent every N rounds. Default 1 matches the
        current behaviour (every not-violated round produces guidance).
    """

    def __init__(
        self,
        *,
        generator: GeneratorAgent,
        feedback: FeedbackAgent | None,
        llm_oracle: LLMOracleAgent | None,
        minimizer: MinimizerAgent | None,
        client: CoreClient,
        catalog: CheckerCatalog,
        feedback_stride: int = 1,
    ) -> None:
        self._generator = generator
        self._feedback = feedback
        self._llm_oracle = llm_oracle
        self._minimizer = minimizer
        self._catalog = catalog
        self._client = client
        self._feedback_stride = max(1, int(feedback_stride))

        # Node factories — capture references once so each round reuses them.
        self._routing_node = make_routing_node(catalog)
        self._build_node = make_build_node(client)
        self._coverage_node = make_coverage_node(client)
        self._oracle_node = make_oracle_node(client)

    async def run(
        self,
        bb: Blackboard,
        *,
        budget_rounds: int,
        budget_secs: float,
    ) -> Blackboard:
        """Run the fixed-order loop until either budget cap is hit or the
        aggregate verdict flips to ``violated``.

        Budget semantics: ``budget_rounds`` is a hard iteration cap;
        ``budget_secs`` (> 0) adds a wall-clock cap. The first cap reached
        terminates.
        """

        start = time.monotonic()
        rounds = max(1, int(budget_rounds))
        for _ in range(rounds):
            round_started = time.monotonic()

            # 1) Generator (sole writer of current_seed / corpus).
            await _run_phase("generator", self._generator_step, bb)

            # 2) Routing (sole writer of build_matrix).
            await _run_phase("routing", self._routing_node, bb)

            # 3) Build (sole writer of build_artifacts).
            await _run_phase("build", self._build_node, bb)

            # 4) Coverage (sole writer of coverage).
            await _run_phase("coverage", self._coverage_node, bb)

            # 5) Deterministic oracle (writes verdict_history / last_verdict
            # / pending_bug on violated).
            await _run_phase("oracle", self._oracle_node, bb)

            # 6) Optional LLM-oracle fallback for NA/Error invariants (R8).
            if self._llm_oracle is not None:
                await _run_phase("llm_oracle", self._llm_oracle_step, bb)

            aggregate = (
                bb.last_verdict.aggregate if bb.last_verdict is not None else None
            )
            elapsed_ms = (time.monotonic() - round_started) * 1000.0
            log.info(
                "iteration_loop.round done round=%d target=%s aggregate=%s elapsed_ms=%.1f",
                bb.round,
                bb.current_target(),
                aggregate,
                elapsed_ms,
            )

            # 7) Violation branch — optional minimizer, then return.
            if aggregate is Aggregate.VIOLATED:
                if self._minimizer is not None:
                    await _run_phase("minimizer", self._minimizer_step, bb)
                return bb

            # 8) Not-violated branch — optional feedback for next round.
            if self._feedback is not None and (
                bb.round % self._feedback_stride == 0
            ):
                await _run_phase("feedback", self._feedback_step, bb)

            # 9) Bump: single-invariant campaigns never advance the cursor.
            bb.round += 1

            # 10) Wall-clock cap check — after the round so a violated
            # round still gets its minimizer.
            if budget_secs > 0 and (time.monotonic() - start) >= budget_secs:
                break

        return bb

    # ---- individual phase bodies (thin async wrappers over the agents) -----

    async def _generator_step(self, bb: Blackboard) -> dict[str, Any]:
        seed = await self._generator.generate(bb)
        return {"current_seed": seed, "corpus": [*bb.corpus, seed]}

    async def _feedback_step(self, bb: Blackboard) -> dict[str, Any]:
        assert self._feedback is not None
        guidance = await self._feedback.summarize(bb)
        return {"guidance": guidance}

    async def _minimizer_step(self, bb: Blackboard) -> dict[str, Any]:
        assert self._minimizer is not None
        poc = await self._minimizer.minimize(bb)
        return {"minimized_poc": poc}

    async def _llm_oracle_step(self, bb: Blackboard) -> dict[str, Any]:
        """Adjudicate NA/Error invariants; promote aggregate on confident FAIL.

        Mirrors the semantics of the removed
        ``graph.make_llm_oracle_node``: only ever ADDS information, and a
        promotion requires a confident FAIL (enforced inside the agent).
        """

        assert self._llm_oracle is not None
        if not bb.ablation_flags.llm_oracle or bb.current_seed is None:
            return {}
        results = await self._llm_oracle.adjudicate(bb)
        if not results:
            return {}
        from .agents.llm_oracle import promote_verdict

        update: dict[str, Any] = {"llm_verdicts": [*bb.llm_verdicts, *results]}
        seed_id = bb.current_seed.id
        promoted = promote_verdict(bb.last_verdict, results, seed_id)
        if promoted is not None:
            update["last_verdict"] = promoted
            update["pending_bug"] = bug_evidence(bb, promoted)
        return update


__all__ = ["IterationLoop"]
