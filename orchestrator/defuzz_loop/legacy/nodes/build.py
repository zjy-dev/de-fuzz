"""Build node: deterministic compile of the current seed across the BuildMatrix.

Calls gRPC BuildService and writes build_artifacts. A missing toolchain / failed
compile yields an artifact with success=False and a filled error — never a crash
(R8, FR-015).
"""

from __future__ import annotations

from ..clients.grpc_client import CoreClient
from ..state import Blackboard
from .convert import artifact_from_pb, cell_to_pb, seed_to_pb


def make_build_node(client: CoreClient):
    def build_node(bb: Blackboard) -> dict:
        if bb.current_seed is None or bb.build_matrix is None:
            return {"build_artifacts": []}

        cells = [cell_to_pb(c) for c in bb.build_matrix.cells]
        artifacts = client.build(seed_to_pb(bb.current_seed), cells)
        return {"build_artifacts": [artifact_from_pb(a) for a in artifacts]}

    return build_node
