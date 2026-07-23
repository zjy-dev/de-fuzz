"""LangGraph wiring: the deterministic agentic loop (FR-005/006/009).

Fixed edge order (never reordered, so a run is reproducible):

    START → generator → routing → build → coverage → oracle → ⟨route⟩
                ▲                                                 │
                └────────────── bump (round+1, cursor) ←─ not_violated ┘
                                                violated → END

Only the Generator step is an LLM agent; routing/build/coverage/oracle are
deterministic gRPC nodes. Invariant scheduling is deterministic too: the
orchestrator enumerates the whole checker catalog into `target_queue` at run init,
and the bump node sweeps a cursor over it, spending a fixed per-invariant budget
(N rounds OR T seconds, whichever first) before advancing. The agent never picks
which invariant to attack — it only generates a seed for the assigned target.

The conditional router reads the oracle's aggregate verdict: violated → END;
not_violated → loop back unless the whole sweep is complete (cursor past the last
target with its budget spent).
"""

from __future__ import annotations

import time

from langgraph.graph import END, START, StateGraph

from .agents.generator import GeneratorAgent
from .agents.llm_oracle import LLMOracleAgent, promote_verdict
from .clients.grpc_client import CoreClient
from .nodes.build import make_build_node
from .nodes.coverage import make_coverage_node
from .nodes.oracle import bug_evidence, make_oracle_node
from .permissions import guard
from .routing import CheckerCatalog, make_routing_node
from .state import Aggregate, Blackboard


def make_generator_node(agent: GeneratorAgent):
    """Wrap the async Generator agent as a graph node.

    Sole writer of corpus / current_seed (write-permission matrix, FR-007).
    """

    async def generator_node(bb: Blackboard) -> dict:
        seed = await agent.generate(bb)
        return {"current_seed": seed, "corpus": [*bb.corpus, seed]}

    return generator_node


def make_feedback_node(agent):
    """Wrap the feedback agent: sole writer of guidance (FR-008)."""

    async def feedback_node(bb: Blackboard) -> dict:
        guidance = await agent.summarize(bb)
        return {"guidance": guidance}

    return feedback_node


def make_minimizer_node(agent):
    """Wrap the minimizer agent: sole writer of minimized_poc (FR-025/026)."""

    async def minimizer_node(bb: Blackboard) -> dict:
        poc = await agent.minimize(bb)
        return {"minimized_poc": poc}

    return minimizer_node


def make_llm_oracle_node(agent: LLMOracleAgent):
    """LLM-driven oracle: a fallback judge for the non-programmable invariants.

    Runs after the deterministic oracle, on the invariants it returned NA/Error
    for (FORTIFY W01/O01/O02/O03 on aarch64). It only ever *adds* information:
    the adjudications are appended to llm_verdicts (audit trail), and only a
    confident LLM FAIL promotes the aggregate to violated — it never overrides a
    deterministic PASS/FAIL (R8 zero false positives). On promotion it writes the
    bug record exactly like the deterministic oracle (subject to oracle_grounding).
    """

    async def llm_oracle_node(bb: Blackboard) -> dict:
        if not bb.ablation_flags.llm_oracle or bb.current_seed is None:
            return {}
        results = await agent.adjudicate(bb)
        if not results:
            return {}
        update: dict = {"llm_verdicts": [*bb.llm_verdicts, *results]}
        seed_id = bb.current_seed.id
        promoted = promote_verdict(bb.last_verdict, results, seed_id)
        if promoted is not None:
            update["last_verdict"] = promoted
            update["pending_bug"] = bug_evidence(bb, promoted)
        return update

    return llm_oracle_node


def target_budget_spent(bb: Blackboard, budget_rounds: int, budget_secs: float) -> bool:
    """Whether the current invariant's budget is exhausted after this round.

    Hybrid stop rule (owner decision): the just-finished round counts, so the
    current target is done when rounds_on_target+1 reaches budget_rounds, OR — when
    a time cap is enabled (budget_secs > 0) — when its wall-clock window elapses.
    budget_secs == 0 disables the time cap, giving pure, reproducible round-robin.
    """
    if bb.rounds_on_target + 1 >= budget_rounds:
        return True
    if budget_secs > 0 and bb.target_started_at > 0:
        return time.time() - bb.target_started_at >= budget_secs
    return False


def make_bump_node(budget_rounds: int, budget_secs: float):
    """Loop-back step: advance the round counter and the enumeration cursor.

    Stays on the current invariant until its budget is spent, then advances to the
    next one (resetting its per-target counters / clock). Sole writer of round and
    the cursor fields (write-permission matrix).
    """

    def bump_node(bb: Blackboard) -> dict:
        update: dict = {"round": bb.round + 1}
        if target_budget_spent(bb, budget_rounds, budget_secs):
            update["target_idx"] = bb.target_idx + 1
            update["rounds_on_target"] = 0
            update["target_started_at"] = time.time() if budget_secs > 0 else 0.0
        else:
            update["rounds_on_target"] = bb.rounds_on_target + 1
        return update

    return bump_node


def make_router(budget_rounds: int, budget_secs: float):
    """Conditional edge after oracle (FR-005).

    violated → bug branch. Otherwise loop back unless the sweep is complete: the
    cursor is past the last target, or the current (last) target's budget is spent.
    Error/NotApplicable aggregate to NOT_VIOLATED, so they take the non-bug branch
    — zero false positives (R8).
    """

    def route_after_oracle(bb: Blackboard) -> str:
        if bb.last_verdict is not None and bb.last_verdict.aggregate is Aggregate.VIOLATED:
            return "violated"
        if bb.target_idx >= len(bb.target_queue):
            return "stop"
        if target_budget_spent(bb, budget_rounds, budget_secs):
            if bb.target_idx + 1 >= len(bb.target_queue):
                return "stop"
        return "loop"

    return route_after_oracle


def build_graph(
    *,
    generator: GeneratorAgent,
    catalog: CheckerCatalog,
    client: CoreClient,
    budget_rounds: int,
    budget_secs: float = 0.0,
    feedback=None,
    minimizer=None,
    llm_oracle: LLMOracleAgent | None = None,
) -> StateGraph:
    """Assemble the fixed-order pipeline over the Blackboard state.

    Optional agents (feedback on the not_violated loop, minimizer on the violated
    terminus, llm_oracle as a fallback judge after the deterministic oracle) are
    wired only when supplied (the `--disable-agent` flags drop them). Every node is
    guarded by the write-permission matrix (FR-008/022).
    """
    g = StateGraph(Blackboard)

    g.add_node("generator", guard("generator", make_generator_node(generator)))
    g.add_node("routing", guard("routing", make_routing_node(catalog)))
    g.add_node("build", guard("build", make_build_node(client)))
    g.add_node("coverage", guard("coverage", make_coverage_node(client)))
    g.add_node("oracle", guard("oracle", make_oracle_node(client)))
    g.add_node("bump", guard("bump", make_bump_node(budget_rounds, budget_secs)))

    g.add_edge(START, "generator")
    g.add_edge("generator", "routing")
    g.add_edge("routing", "build")
    g.add_edge("build", "coverage")
    g.add_edge("coverage", "oracle")

    # not_violated loop: optional feedback (writes guidance) → bump → generator.
    loop_target = "bump"
    if feedback is not None:
        g.add_node("feedback", guard("feedback", make_feedback_node(feedback)))
        g.add_edge("feedback", "bump")
        loop_target = "feedback"

    # violated terminus: optional minimizer (writes MinimizedPoC) → END.
    violated_target = END
    if minimizer is not None:
        g.add_node("minimizer", guard("minimizer", make_minimizer_node(minimizer)))
        g.add_edge("minimizer", END)
        violated_target = "minimizer"

    # The router reads last_verdict.aggregate, which the LLM oracle may promote to
    # violated; so it must run after the LLM oracle. Insert llm_oracle between the
    # deterministic oracle and the router when supplied.
    router_source = "oracle"
    if llm_oracle is not None:
        g.add_node("llm_oracle", guard("llm_oracle", make_llm_oracle_node(llm_oracle)))
        g.add_edge("oracle", "llm_oracle")
        router_source = "llm_oracle"

    g.add_conditional_edges(
        router_source,
        make_router(budget_rounds, budget_secs),
        {"loop": loop_target, "stop": END, "violated": violated_target},
    )
    g.add_edge("bump", "generator")

    return g


def build_graph_skeleton() -> StateGraph:
    """Bare node-free StateGraph (kept for skeleton inspection / tests)."""
    return StateGraph(Blackboard)
