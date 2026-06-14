"""gRPC client wrapping the four deterministic Go-core services.

The orchestrator drives the deterministic pipeline (build / coverage / oracle /
checker-metadata) over gRPC; agents use MCP separately. Every call here should be
recorded into the blackboard by the calling node to keep the run reproducible.
"""

from __future__ import annotations

import grpc

from .pb import oracle_pb2 as pb
from .pb import oracle_pb2_grpc as pb_grpc


class CoreClient:
    """Thin synchronous wrapper over the four deterministic gRPC services."""

    def __init__(self, address: str = "localhost:50051") -> None:
        self._address = address
        self._channel = grpc.insecure_channel(address)
        self._build = pb_grpc.BuildServiceStub(self._channel)
        self._coverage = pb_grpc.CoverageServiceStub(self._channel)
        self._oracle = pb_grpc.OracleServiceStub(self._channel)
        self._metadata = pb_grpc.CheckerMetadataServiceStub(self._channel)

    def close(self) -> None:
        self._channel.close()

    def __enter__(self) -> CoreClient:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    def list_checker_metadata(self) -> list[pb.CheckerMetadata]:
        resp = self._metadata.ListCheckerMetadata(pb.ListCheckerMetadataRequest())
        return list(resp.checkers)

    def build(self, seed: pb.Seed, cells: list[pb.BuildCell]) -> list[pb.BuildArtifact]:
        resp = self._build.Build(pb.BuildRequest(seed=seed, cells=cells))
        return list(resp.artifacts)

    def measure(
        self, artifacts: list[pb.BuildArtifact], cumulative_state: bytes
    ) -> pb.CoverageResponse:
        return self._coverage.Measure(
            pb.CoverageRequest(artifacts=artifacts, cumulative_state=cumulative_state)
        )

    def analyze(self, seed: pb.Seed, artifacts: list[pb.BuildArtifact]) -> pb.OracleResponse:
        return self._oracle.Analyze(pb.OracleRequest(seed=seed, artifacts=artifacts))
