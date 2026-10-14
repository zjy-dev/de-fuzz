"""Real container integration smoke; fixed seed, no model or paper-result claim."""

from __future__ import annotations

import asyncio
import errno
import hashlib
import json
import os
import subprocess
from pathlib import Path
from types import SimpleNamespace

import grpc
import yaml
from defuzz_loop.audit import build_manifest, make_run_dir, write_manifest
from defuzz_loop.clients.grpc_client import CoreClient
from defuzz_loop.clients.pb import oracle_pb2 as pb
from defuzz_loop.llm import LLMConfig
from defuzz_loop.loop import IterationLoop
from defuzz_loop.round_artifacts import RoundArtifacts
from defuzz_loop.routing import CheckerCatalog
from defuzz_loop.state import Blackboard, Guidance, Seed, Verdict
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

ISAS = list(yaml.safe_load(Path("/workspace/configs/toolchains.yaml").read_text())["toolchains"])
SOURCE = """#include <stdio.h>
__attribute__((noinline,noipa)) int probe(int n) {
    volatile char buf[32]; buf[n & 31] = (char)n; return buf[n & 31];
}
int main(void) { puts("defuzz-smoke"); return probe(7) != 7; }
"""


class FixedGenerator:
    async def generate(self, bb):
        return Seed(id=f"container-smoke-{bb.round}", source=SOURCE,
                    selected_checkers=["INV-SP-G01"])


class RuntimeFeedback:
    """Read and execute core-created files before the round cleanup callback."""

    def __init__(self):
        self.executed = set()

    async def summarize(self, bb):
        toolchains = yaml.safe_load(Path("/workspace/configs/toolchains.yaml").read_text())
        assert len(bb.build_artifacts) == len(ISAS)
        for artifact in bb.build_artifacts:
            assert artifact.success, artifact.error
            config = toolchains["toolchains"][artifact.cell.isa]
            command = [artifact.binary_path]
            if not config.get("native"):
                command = [config["qemu_path"], *config.get("qemu_args", []),
                           "-L", config["qemu_sysroot"], *command]
            result = subprocess.run(command, check=True, capture_output=True, text=True, timeout=30)
            assert result.stdout.strip() == "defuzz-smoke", result
            self.executed.add(artifact.cell.isa)
        return Guidance(round=bb.round, summary="Configured ISA binaries executed successfully.")


async def callgraph_check():
    params = StdioServerParameters(
        command="/workspace/callgraph_mcp/.venv/bin/defuzz-callgraph-mcp",
        args=["--db", os.environ["DEFUZZ_CALLGRAPH_DB"]],
    )
    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            tools = await session.list_tools()
            assert len(tools.tools) == 6
            result = await session.call_tool("list_targets", {})
            assert not result.isError, result
            return [item.model_dump(mode="json") for item in result.content]


async def main():
    work = Path(os.environ["DEFUZZ_WORK_ROOT"])
    mounts = [line.split() for line in Path("/proc/mounts").read_text().splitlines()]
    assert any(row[1] == str(work) and row[2] == "tmpfs" for row in mounts), mounts
    capacity = os.statvfs(work)
    assert capacity.f_blocks * capacity.f_frsize == 2 * 1024**3
    data_dir = Path(os.environ["DEFUZZ_CALLGRAPH_DB"]).parent
    # Verify the actual VFS mount flag, not just SQLite's optional mode=ro.
    assert os.statvfs(data_dir).f_flag & os.ST_RDONLY
    try:
        with (data_dir / ".defuzz-smoke-write-probe").open("x"):
            pass
    except OSError as exc:
        assert exc.errno == errno.EROFS, exc
    else:
        (data_dir / ".defuzz-smoke-write-probe").unlink()
        raise AssertionError("callgraph volume allowed writes")

    # A sentinel in the shared tmpfs must survive every round's scoped cleanup.
    sentinel = work / "smoke-unrelated.txt"
    sentinel.write_text("keep")
    run_dir = make_run_dir("/workspace/runs", "container-smoke", "canary")
    audit = RoundArtifacts(run_dir, work)
    bb = Blackboard(target_queue=["INV-SP-G01"])
    with grpc.insecure_channel("localhost:50051") as channel:
        grpc.channel_ready_future(channel).result(timeout=60)
    with CoreClient("localhost:50051") as client:
        metadata = client.list_checker_metadata()
        selected = next(item for item in metadata if item.id == "INV-SP-G01")
        selected.applicable_isas[:] = [isa for isa in ISAS if isa in selected.applicable_isas]
        assert len(selected.applicable_isas) == len(ISAS)
        catalog = CheckerCatalog(SimpleNamespace(list_checker_metadata=lambda: [selected]))
        feedback = RuntimeFeedback()
        write_manifest(run_dir, build_manifest(
            experiment="container-smoke", mechanism="canary", budget_rounds=2, budget_secs=0,
            grpc_addr="localhost:50051", mcp_addr="http://localhost:50052/mcp",
            llm_config=LLMConfig.load(), blackboard=bb,
            disabled_agents=["llm_oracle", "minimizer"], checker_ids=catalog.all_ids,
        ) | {"generator": "fixed fixture; no LLM calls"})
        loop = IterationLoop(
            generator=FixedGenerator(), feedback=feedback, llm_oracle=None, minimizer=None,
            client=client, catalog=catalog, on_round_complete=audit.complete,
        )
        await loop.run(bb, budget_rounds=2, budget_secs=0)
        audit.finish(bb)
        assert audit.completed_rounds == 2
        assert feedback.executed == set(ISAS)
        assert bb.last_verdict and len(bb.last_verdict.results) == len(ISAS)
        assert all(result.verdict == Verdict.PASS for result in bb.last_verdict.results)
        assert all(not Path(item.binary_path).exists() for item in bb.build_artifacts)
        assert sentinel.read_text() == "keep"
        # Unreachable failed-build paths must also be reclaimed by the core.
        before = set(work.glob("defuzz-build-*"))
        failed = client.build(pb.Seed(id="bad-source", source="not valid C"),
                              [pb.BuildCell(checker_id="INV-SP-G01", isa="x86_64")])
        assert len(failed) == 1 and not failed[0].success and failed[0].error
        assert set(work.glob("defuzz-build-*")) == before
    sentinel.unlink()
    for state_path in sorted((run_dir / "rounds").glob("*/state.json")):
        state = json.loads(state_path.read_text())
        assert len(state["archived_binaries"]) == len(ISAS)
        for artifact in state["archived_binaries"].values():
            binary = run_dir / artifact["path"]
            assert hashlib.sha256(binary.read_bytes()).hexdigest() == artifact["sha256"]
    results = {
        "kind": "engineering-smoke", "isas": ISAS, "completed_rounds": 2,
        "shared_tmpfs": True, "callgraph_read_only": True,
        "round_cleanup": True, "failed_build_cleanup": True,
        "callgraph_targets": await callgraph_check(),
    }
    (run_dir / "smoke.json").write_text(json.dumps(results, indent=2) + "\n")
    print(json.dumps({"passed": True, "run_dir": str(run_dir), **results}, indent=2))


if __name__ == "__main__":
    asyncio.run(main())
