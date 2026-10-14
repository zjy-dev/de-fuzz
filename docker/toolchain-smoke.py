"""Compiler/source/CallGraph integration check; no model calls or defect claims."""

from __future__ import annotations

import asyncio
import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path

import yaml
from defuzz_loop.experiment_engine.callgraph_identity import bind_callgraph, verify_driver_versions
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

SOURCE = """#include <stdio.h>
__attribute__((noinline)) int probe(int n) {
  volatile char buf[32]; buf[n & 31] = (char)n; return buf[n & 31];
}
int main(void) { puts("defuzz-toolchain-smoke"); return probe(7) != 7; }
"""


def run(argv: list[str]) -> str:
    return subprocess.check_output(argv, text=True, stderr=subprocess.STDOUT, timeout=60).strip()


def sha256(path: Path) -> str:
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


async def mcp_probe(database: Path, binding: dict) -> dict:
    params = StdioServerParameters(
        command="/workspace/callgraph_mcp/.venv/bin/defuzz-callgraph-mcp",
        args=["--db", str(database), *[
            arg for target in binding["targets"] for arg in ("--allow-target", target)
        ]],
    )
    async with stdio_client(params) as streams, ClientSession(*streams) as session:
        await session.initialize()
        listed = await session.list_tools()
        assert len(listed.tools) == 6
        result = await session.call_tool("list_targets", {})
        assert not result.isError
        targets = result.structuredContent["targets"]
        assert {item["target"] for item in targets} == set(binding["targets"])
        query = await session.call_tool("find_function", {
            "target": binding["targets"][0],
            "pattern": "stack_protect" if binding["compiler"] == "gcc" else "StackProtector",
            "limit": 5,
        })
        assert not query.isError and query.structuredContent["functions"]
        outside = "llvm-23.1.2" if binding["compiler"] == "gcc" else "gcc-16.2.0-aarch64"
        rejected = await session.call_tool("find_function", {"target": outside, "pattern": "x"})
        assert rejected.isError
        return {"tools": 6, "targets": targets, "outside_target_rejected": True,
                "symbols": query.structuredContent["functions"]}


def main() -> None:
    database = Path(os.environ["DEFUZZ_CALLGRAPH_DB"])
    toolchains = Path("/workspace/configs/toolchains.yaml")
    entries = yaml.safe_load(toolchains.read_text())["toolchains"]
    result = {"kind": "engineering-toolchain-smoke", "bindings": {}, "matrix": []}
    for compiler, source_root in (
        ("gcc", Path("/opt/defuzz-sources/gcc-16.2.0")),
        ("llvm", Path("/opt/defuzz-sources/llvm-project-23.1.2")),
    ):
        binding = bind_callgraph(database, source_root, compiler=compiler, isas=list(entries))
        binding["driver_versions"] = verify_driver_versions(binding, toolchains)
        binding["mcp"] = asyncio.run(mcp_probe(database, binding))
        result["bindings"][compiler] = binding
        with tempfile.TemporaryDirectory(prefix="defuzz-toolchain-smoke-") as directory:
            root = Path(directory)
            seed = root / "seed.c"
            seed.write_text(SOURCE)
            for isa, entry in entries.items():
                driver = Path(entry["gcc_path" if compiler == "gcc" else "clang_path"])
                output = root / isa
                argv = [str(driver), *entry.get("driver_args", [])]
                if entry.get("prefix"):
                    argv.append("-B" + entry["prefix"])
                argv.extend(entry.get("cflags", []))
                run([*argv, str(seed), "-o", str(output)])
                command = [str(output)]
                if not entry["native"]:
                    command = [entry["qemu_path"], *entry.get("qemu_args", []),
                               "-L", entry["qemu_sysroot"], *command]
                assert run(command) == "defuzz-toolchain-smoke"
                header = run(["/opt/llvm23/bin/llvm-readelf", "-h", str(output)])
                expected = "AArch64" if isa == "aarch64" else "X86-64"
                assert expected in header
                result["matrix"].append({
                    "compiler": compiler, "isa": isa, "command": [*argv, str(seed), "-o", str(output)],
                    "driver_sha256": sha256(driver.resolve()),
                    "input_sha256": sha256(seed), "binary_sha256": sha256(output),
                    "machine": expected, "exit_code": 0,
                })
    result["llvm_config"] = run(["/opt/llvm23/bin/llvm-config", "--version"])
    result["lld"] = run(["/opt/llvm23/bin/ld.lld", "--version"])
    result["passed"] = True
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
