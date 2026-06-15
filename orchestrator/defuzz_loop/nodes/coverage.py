"""Coverage node: the ONLY writer of Blackboard.coverage (FR-022).

Calls gRPC CoverageService.Measure with the round's artifacts and the cumulative
state, writes back the new cumulative + this round's delta. No agent path may write
coverage; that invariant is enforced at the node boundary in graph.py.
"""

from __future__ import annotations

from ..clients.grpc_client import CoreClient
from ..state import Blackboard, CoverageState
from .convert import artifact_to_pb


def make_coverage_node(client: CoreClient):
    def coverage_node(bb: Blackboard) -> dict:
        artifacts = [artifact_to_pb(a) for a in bb.build_artifacts]
        resp = client.measure(artifacts, bb.coverage.cumulative)
        return {
            "coverage": CoverageState(
                cumulative=resp.cumulative_state,
                last_delta=resp.delta_json,
            )
        }

    return coverage_node
