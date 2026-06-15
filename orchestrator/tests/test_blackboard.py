"""T028 (US3): the blackboard moat — reproducibility, ablation, auditability.

The blackboard is the only linkage channel between the three agents (FR-006/008),
versioned by the LangGraph checkpointer. This module asserts the three guarantees
that make it a moat:

- write-permission matrix: a node returning a field it does not own raises
  immediately (FR-008/022, SC differentiator for "no cross-agent leakage"),
- replay: pinning a checkpoint reconstructs an identical input blackboard, and the
  recorded tool_call_log is what downstream replays (R7, SC-003),
- ablation: each AblationFlags bool toggles exactly one edge while the rest of the
  pipeline still advances (FR-010, SC-004),
- bug→evidence traceback: a pending_bug carries the deterministic failing checker /
  ISA / evidence all the way back through the corpus seed (FR-011, SC-005).
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from defuzz_loop.permissions import (
    NODE_WRITE_PERMISSIONS,
    WritePermissionError,
    guard,
)
from defuzz_loop.routing import CheckerCatalog, expand_matrix
from defuzz_loop.state import (
    AblationFlags,
    Aggregate,
    Blackboard,
    BugEvidence,
    OracleVerdict,
    Seed,
)

# --- write-permission matrix (FR-008/022) ----------------------------------


def test_guard_allows_owned_field() -> None:
    node = guard("coverage", lambda bb: {"coverage": bb.coverage})
    out = node(Blackboard())
    assert "coverage" in out


def test_guard_rejects_unowned_field() -> None:
    # coverage node trying to write guidance is a contract violation: only the
    # feedback agent may write guidance (FR-008), only coverage may write coverage.
    node = guard("coverage", lambda bb: {"guidance": None})
    with pytest.raises(WritePermissionError):
        node(Blackboard())


def test_guard_rejects_agent_writing_coverage() -> None:
    # The strongest invariant: no agent path may ever write coverage (FR-022).
    node = guard("feedback", lambda bb: {"coverage": bb.coverage})
    with pytest.raises(WritePermissionError):
        node(Blackboard())


def test_guard_unknown_node_raises() -> None:
    node = guard("not_a_node", lambda bb: {})
    with pytest.raises(WritePermissionError):
        node(Blackboard())


async def test_guard_wraps_async_nodes() -> None:
    async def writer(bb: Blackboard) -> dict:
        return {"guidance": None}

    node = guard("coverage", writer)
    with pytest.raises(WritePermissionError):
        await node(Blackboard())


def test_matrix_covers_every_writable_field() -> None:
    # Every field a node owns must be a real Blackboard field (no typos drifting
    # the matrix away from the schema it guards).
    fields = set(Blackboard.model_fields)
    for owned in NODE_WRITE_PERMISSIONS.values():
        assert owned <= fields


# --- replay: pinned checkpoint reconstructs identical input (SC-003) --------


def test_blackboard_roundtrips_through_checkpoint_dump() -> None:
    # A checkpoint stores channel_values; model_validate must reconstruct an
    # equal blackboard so a pinned checkpoint replays the same input (R7).
    bb = Blackboard(
        round=2,
        corpus=[Seed(id="s0", source="int main(void){return 0;}")],
        last_verdict=OracleVerdict(seed_id="s0", aggregate=Aggregate.NOT_VIOLATED),
    )
    dumped = bb.model_dump()
    restored = Blackboard.model_validate(dumped)
    assert restored == bb


# --- ablation: one edge toggles, rest of pipeline advances (SC-004) ---------


def _catalog() -> CheckerCatalog:
    metas = [
        SimpleNamespace(
            id="INV-SP-G01",
            applicable_isas=["x86_64", "aarch64"],
            mode="single",
            cost="cheap",
            category="static",
        ),
        SimpleNamespace(
            id="INV-SP-L01",
            applicable_isas=["x86_64", "aarch64", "riscv64"],
            mode="differential",
            cost="expensive",
            category="dynamic",
        ),
    ]
    return CheckerCatalog(SimpleNamespace(list_checker_metadata=lambda: metas))


def test_checker_routing_flag_toggles_only_that_edge() -> None:
    catalog = _catalog()
    seed = Seed(id="s", source="", selected_checkers=[])  # selects nothing

    on = expand_matrix(
        catalog,
        Blackboard(current_seed=seed, ablation_flags=AblationFlags(checker_routing=True)),
    )
    off = expand_matrix(
        catalog,
        Blackboard(current_seed=seed, ablation_flags=AblationFlags(checker_routing=False)),
    )

    # routing ON: only cheap checkers auto-run (expensive L01 not selected).
    assert {c.checker_id for c in on.cells} == {"INV-SP-G01"}
    # routing OFF (control arm): full checker × ISA product, no pruning.
    assert {c.checker_id for c in off.cells} == {"INV-SP-G01", "INV-SP-L01"}
    assert len(off.cells) > len(on.cells)


def test_ablation_flags_are_independent() -> None:
    # Toggling one edge leaves the others at their default (no coupling).
    flags = AblationFlags(coverage_feedback=False)
    assert flags.coverage_feedback is False
    assert flags.feedback_to_generator is True
    assert flags.checker_routing is True
    assert flags.oracle_grounding is True


def test_feedback_to_generator_edge_toggles() -> None:
    # The "feedback → Generator" edge: guidance reaches the Generator prompt only
    # when feedback_to_generator is on (SC-004).
    from defuzz_loop.agents.generator import guidance_block
    from defuzz_loop.state import Guidance

    g = Guidance(round=0, summary="stress alloca paths")
    on = Blackboard(guidance=g, ablation_flags=AblationFlags(feedback_to_generator=True))
    off = Blackboard(guidance=g, ablation_flags=AblationFlags(feedback_to_generator=False))
    assert "stress alloca paths" in guidance_block(on)
    assert guidance_block(off) == ""


def test_coverage_feedback_edge_toggles() -> None:
    # The "coverage feedback" edge: the coverage delta feeds the feedback agent
    # only when coverage_feedback is on (SC-004).
    from defuzz_loop.agents.feedback import coverage_signal

    diff = {"delta": "lines:+42"}
    on = Blackboard(ablation_flags=AblationFlags(coverage_feedback=True))
    off = Blackboard(ablation_flags=AblationFlags(coverage_feedback=False))
    assert coverage_signal(on, diff) == "lines:+42"
    assert "disabled" in coverage_signal(off, diff)


def test_oracle_grounding_edge_toggles() -> None:
    # The "oracle grounding" edge: the bug record carries deterministic evidence
    # only when oracle_grounding is on; off → degraded bare verdict (SC-004).
    from defuzz_loop.nodes.oracle import bug_evidence

    verdict = OracleVerdict(
        seed_id="s9",
        aggregate=Aggregate.VIOLATED,
        failing_checker="INV-SP-L01",
        failing_isa="aarch64",
        evidence="canary absent on aarch64",
    )
    on = bug_evidence(Blackboard(ablation_flags=AblationFlags(oracle_grounding=True)), verdict)
    off = bug_evidence(
        Blackboard(ablation_flags=AblationFlags(oracle_grounding=False)), verdict
    )
    assert on.failing_checker == "INV-SP-L01" and on.isa == "aarch64" and on.evidence
    assert off.seed_id == "s9"
    assert off.failing_checker == "" and off.isa == "" and off.evidence == ""


# --- bug→evidence traceback (SC-005) ----------------------------------------


def test_pending_bug_traces_to_deterministic_evidence() -> None:
    seed = Seed(id="s7", source="int main(void){char b[8];return 0;}")
    bb = Blackboard(
        corpus=[seed],
        pending_bug=BugEvidence(
            seed_id="s7",
            failing_checker="INV-SP-L01",
            isa="aarch64",
            evidence="canary absent on aarch64 but present on x86_64",
        ),
    )

    bug = bb.pending_bug
    assert bug is not None
    # The bug points at a real corpus seed (traceable back to its source).
    traced = next((s for s in bb.corpus if s.id == bug.seed_id), None)
    assert traced is seed
    # Deterministic evidence is fully populated (failing checker + ISA + reason).
    assert bug.failing_checker == "INV-SP-L01"
    assert bug.isa == "aarch64"
    assert bug.evidence
