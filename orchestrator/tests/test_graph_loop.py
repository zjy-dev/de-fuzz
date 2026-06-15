"""T015 (US1): the deterministic loop's fixed order + conditional routing.

Verifies SC-001 / R8 without a live LLM or Go core: stub the Generator agent and
the gRPC client so the *real* graph wiring (generator→routing→build→coverage→
oracle→route) and the *real* router are exercised, while the verdict is forced.

Asserts:
- the fixed edge order is present in the compiled graph,
- not_violated → loop back, bounded by max_rounds,
- violated → terminate immediately (pending_bug written),
- an Error verdict (aggregates to NOT_VIOLATED) takes the non-bug branch.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest
from langgraph.checkpoint.memory import MemorySaver

from defuzz_loop.graph import build_graph, make_router
from defuzz_loop.routing import CheckerCatalog
from defuzz_loop.state import Aggregate, Blackboard, OracleVerdict, Seed, SeedOrigin


class _StubGenerator:
    """Async Generator stand-in; emits a deterministic seed each round."""

    def __init__(self) -> None:
        self.calls = 0

    async def generate(self, bb: Blackboard) -> Seed:
        self.calls += 1
        return Seed(
            id=f"seed{self.calls}",
            source="int main(void){return 0;}",
            selected_checkers=[],
            origin=SeedOrigin.GENERATOR,
        )


class _StubClient:
    """gRPC CoreClient stand-in returning empty/forced deterministic results."""

    def __init__(self, *, violated: bool) -> None:
        self._violated = violated
        self.analyze_calls = 0

    def list_checker_metadata(self) -> list:
        return []

    def build(self, seed, cells) -> list:
        return []

    def measure(self, artifacts, cumulative_state):
        return SimpleNamespace(cumulative_state=b"", delta_json="")

    def analyze(self, seed, artifacts):
        self.analyze_calls += 1
        return SimpleNamespace(
            results=[],
            violated=self._violated,
            failing_checker="INV-SP-G01" if self._violated else "",
            failing_isa="x86_64" if self._violated else "",
            evidence="forced" if self._violated else "",
        )


def _compile(*, violated: bool, max_rounds: int):
    client = _StubClient(violated=violated)
    catalog = CheckerCatalog(client)
    generator = _StubGenerator()
    graph = build_graph(
        generator=generator, catalog=catalog, client=client, max_rounds=max_rounds
    )
    app = graph.compile(checkpointer=MemorySaver())
    return app, generator, client


def _config(thread: str) -> dict:
    return {"configurable": {"thread_id": thread}, "recursion_limit": 100}


def test_fixed_edge_order() -> None:
    app, _, _ = _compile(violated=False, max_rounds=1)
    edges = {(e.source, e.target) for e in app.get_graph().edges}
    assert ("generator", "routing") in edges
    assert ("routing", "build") in edges
    assert ("build", "coverage") in edges
    assert ("coverage", "oracle") in edges


def test_router_violated_branch() -> None:
    route = make_router(max_rounds=5)
    bb = Blackboard(
        round=0,
        last_verdict=OracleVerdict(seed_id="s", aggregate=Aggregate.VIOLATED),
    )
    assert route(bb) == "violated"


def test_router_not_violated_loops_then_exhausts() -> None:
    route = make_router(max_rounds=3)
    nv = OracleVerdict(seed_id="s", aggregate=Aggregate.NOT_VIOLATED)
    assert route(Blackboard(round=0, last_verdict=nv)) == "loop"
    assert route(Blackboard(round=2, last_verdict=nv)) == "stop"


def test_router_error_takes_non_bug_branch() -> None:
    # An Error verdict maps to NOT_VIOLATED (convert.verdict_from_pb), so the
    # router must never treat it as a bug (zero false positives, R8).
    route = make_router(max_rounds=3)
    err_as_nv = OracleVerdict(seed_id="s", aggregate=Aggregate.NOT_VIOLATED)
    assert route(Blackboard(round=0, last_verdict=err_as_nv)) == "loop"


@pytest.mark.asyncio
async def test_not_violated_loops_for_max_rounds() -> None:
    app, generator, client = _compile(violated=False, max_rounds=2)
    final = await app.ainvoke(Blackboard(), config=_config("t-nv"))
    bb = Blackboard.model_validate(final)
    assert generator.calls == 2
    assert client.analyze_calls == 2
    assert len(bb.verdict_history) == 2
    assert all(v.aggregate is Aggregate.NOT_VIOLATED for v in bb.verdict_history)
    assert bb.pending_bug is None


@pytest.mark.asyncio
async def test_violated_terminates_immediately() -> None:
    app, generator, client = _compile(violated=True, max_rounds=5)
    final = await app.ainvoke(Blackboard(), config=_config("t-v"))
    bb = Blackboard.model_validate(final)
    assert generator.calls == 1
    assert client.analyze_calls == 1
    assert len(bb.verdict_history) == 1
    assert bb.verdict_history[0].aggregate is Aggregate.VIOLATED
    assert bb.pending_bug is not None
    assert bb.pending_bug.failing_checker == "INV-SP-G01"
