"""LangGraph wiring: the deterministic agentic loop (FR-005/006/009).

Fixed edge order (never reordered, so a run is reproducible):

    START → generator → routing → build → coverage → oracle → ⟨route⟩
                ▲                                                 │
                └────────────── bump (round+1) ←── not_violated ──┘
                                                violated → END

Only the Generator step is an LLM agent; routing/build/coverage/oracle are
deterministic gRPC nodes. The conditional router reads the oracle's aggregate
verdict: violated → END, not_violated → loop back after incrementing the round
(bounded by --max-rounds).
"""

from __future__ import annotations

from langgraph.graph import END, START, StateGraph

from .agents.generator import GeneratorAgent
from .clients.grpc_client import CoreClient
from .nodes.build import make_build_node
from .nodes.coverage import make_coverage_node
from .nodes.oracle import make_oracle_node
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


def bump_node(bb: Blackboard) -> dict:
    """Loop-back step: advance the round counter for the next iteration."""
    return {"round": bb.round + 1}


def make_router(max_rounds: int):
    """Conditional edge after oracle (FR-005).

    violated → bug branch; otherwise loop back unless the round budget is
    exhausted. Error/NotApplicable aggregate to NOT_VIOLATED, so they take the
    non-bug branch — zero false positives (R8).
    """

    def route_after_oracle(bb: Blackboard) -> str:
        if bb.last_verdict is not None and bb.last_verdict.aggregate is Aggregate.VIOLATED:
            return "violated"
        if bb.round + 1 >= max_rounds:
            return "stop"
        return "loop"

    return route_after_oracle


def build_graph(
    *,
    generator: GeneratorAgent,
    catalog: CheckerCatalog,
    client: CoreClient,
    max_rounds: int,
    feedback=None,
    minimizer=None,
) -> StateGraph:
    """Assemble the fixed-order pipeline over the Blackboard state.

    Optional agents (feedback on the not_violated loop, minimizer on the violated
    terminus) are wired only when supplied (the `--disable-agent` flags drop them).
    Every node is guarded by the write-permission matrix (FR-008/022).
    """
    g = StateGraph(Blackboard)

    g.add_node("generator", guard("generator", make_generator_node(generator)))
    g.add_node("routing", guard("routing", make_routing_node(catalog)))
    g.add_node("build", guard("build", make_build_node(client)))
    g.add_node("coverage", guard("coverage", make_coverage_node(client)))
    g.add_node("oracle", guard("oracle", make_oracle_node(client)))
    g.add_node("bump", guard("bump", bump_node))

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

    g.add_conditional_edges(
        "oracle",
        make_router(max_rounds),
        {"loop": loop_target, "stop": END, "violated": violated_target},
    )
    g.add_edge("bump", "generator")

    return g


def build_graph_skeleton() -> StateGraph:
    """Bare node-free StateGraph (kept for skeleton inspection / tests)."""
    return StateGraph(Blackboard)
