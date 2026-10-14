"""Re-run the deterministic oracle on an archived online round in the same Pod."""

import argparse
import hashlib
import json
from pathlib import Path

from defuzz_loop.clients.grpc_client import CoreClient
from defuzz_loop.clients.pb import oracle_pb2 as pb
from google.protobuf.json_format import MessageToDict

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("state", type=Path, help="/workspace/runs/.../rounds/NNNNNN/state.json")
parser.add_argument("--grpc", default="localhost:50051")
args = parser.parse_args()
state = json.loads(args.state.read_text())
root = args.state.resolve().parents[2]
artifacts = []
for original in state["build_artifacts"]:
    if not original["success"]:
        continue
    saved = state["archived_binaries"][original["binary_path"]]
    binary = (root / saved["path"]).resolve(strict=True)
    if not binary.is_relative_to(root):
        raise SystemExit("Archived binary is outside this run")
    if hashlib.sha256(binary.read_bytes()).hexdigest() != saved["sha256"]:
        raise SystemExit(f"Archived binary checksum mismatch: {binary}")
    artifacts.append(pb.BuildArtifact(
        cell=pb.BuildCell(**original["cell"]), binary_path=str(binary), success=True,
    ))
seed = state["current_seed"]
with CoreClient(args.grpc) as client:
    result = client.analyze(
        pb.Seed(id=seed["id"], source=seed["source"], selected_checkers=seed["selected_checkers"]),
        artifacts,
    )
print(json.dumps(MessageToDict(result, preserving_proto_field_name=True), indent=2))
