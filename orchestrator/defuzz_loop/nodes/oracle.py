"""Oracle node: deterministic adjudication via gRPC OracleService.Analyze.

Appends the four-state verdict to verdict_history and, on violation, writes
pending_bug with the deterministic evidence for the minimizer branch (FR-025).
The oracle path contains no LLM adjudication (C6); bugs come only from here
(FR-021).
"""

from __future__ import annotations

from ..clients.grpc_client import CoreClient
from ..state import Aggregate, Blackboard, BugEvidence, OracleVerdict
from .convert import artifact_to_pb, seed_to_pb, verdict_from_pb


def bug_evidence(bb: Blackboard, verdict: OracleVerdict) -> BugEvidence:
    """Build the deterministic evidence behind a violation — the "oracle grounding" edge.

    Gated by ablation_flags.oracle_grounding (FR-010 / SC-004): when off, the bug
    record degrades to a bare verdict (no failing checker/ISA/evidence grounding),
    so the contribution of grounding the adjudication can be measured.
    """
    if not bb.ablation_flags.oracle_grounding:
        return BugEvidence(
            seed_id=verdict.seed_id, failing_checker="", isa="", evidence=""
        )
    return BugEvidence(
        seed_id=verdict.seed_id,
        failing_checker=verdict.failing_checker,
        isa=verdict.failing_isa,
        evidence=verdict.evidence,
    )


def make_oracle_node(client: CoreClient):
    def oracle_node(bb: Blackboard) -> dict:
        if bb.current_seed is None:
            return {}

        artifacts = [artifact_to_pb(a) for a in bb.build_artifacts]
        resp = client.analyze(seed_to_pb(bb.current_seed), artifacts)
        verdict = verdict_from_pb(bb.current_seed.id, resp)

        update: dict = {
            "verdict_history": [*bb.verdict_history, verdict],
            "last_verdict": verdict,
        }
        if verdict.aggregate is Aggregate.VIOLATED:
            update["pending_bug"] = bug_evidence(bb, verdict)
        return update

    return oracle_node
