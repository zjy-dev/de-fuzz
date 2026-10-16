"""Conversions between blackboard pydantic models and gRPC protobuf messages.

Keeps node code free of pb boilerplate and centralizes the four-state verdict
mapping (internal/oracle/invariant.go <-> Blackboard.Verdict).
"""

from __future__ import annotations

from ..clients.pb import oracle_pb2 as pb
from ..state import (
    Aggregate,
    BuildArtifact,
    BuildCell,
    InvariantResult,
    OracleVerdict,
    Seed,
    Verdict,
)

_PB_TO_VERDICT = {
    pb.VERDICT_PASS: Verdict.PASS,
    pb.VERDICT_FAIL: Verdict.FAIL,
    pb.VERDICT_NOT_APPLICABLE: Verdict.NOT_APPLICABLE,
    pb.VERDICT_ERROR: Verdict.ERROR,
}


def seed_to_pb(seed: Seed) -> pb.Seed:
    return pb.Seed(
        id=seed.id,
        source=seed.source,
        parent_id=seed.parent_id or "",
        selected_checkers=list(seed.selected_checkers),
    )


def cell_to_pb(cell: BuildCell) -> pb.BuildCell:
    return pb.BuildCell(checker_id=cell.checker_id, isa=cell.isa)


def artifact_from_pb(a: pb.BuildArtifact) -> BuildArtifact:
    return BuildArtifact(
        cell=BuildCell(checker_id=a.cell.checker_id, isa=a.cell.isa),
        binary_path=a.binary_path,
        success=a.success,
        error=a.error,
    )


def artifact_to_pb(a: BuildArtifact) -> pb.BuildArtifact:
    return pb.BuildArtifact(
        cell=cell_to_pb(a.cell),
        binary_path=a.binary_path,
        success=a.success,
        error=a.error,
    )


def result_from_pb(r: pb.InvariantResult) -> InvariantResult:
    return InvariantResult(
        id=r.id,
        category=r.category,
        verdict=_PB_TO_VERDICT.get(r.verdict, Verdict.ERROR),
        evidence=r.evidence,
        detail=dict(r.detail),
        reason=r.reason,
        isa=r.isa,
    )


def verdict_from_pb(seed_id: str, resp: pb.OracleResponse) -> OracleVerdict:
    return OracleVerdict(
        seed_id=seed_id,
        results=[result_from_pb(r) for r in resp.results],
        aggregate=Aggregate.VIOLATED if resp.violated else Aggregate.NOT_VIOLATED,
        failing_checker=resp.failing_checker,
        failing_isa=resp.failing_isa,
        evidence=resp.evidence,
    )
