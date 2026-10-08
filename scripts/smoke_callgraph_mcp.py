#!/usr/bin/env python3
"""Exercise initialize/tools/list and all six callgraph MCP tools."""

from __future__ import annotations

import argparse
import asyncio
import json
from pathlib import Path
from typing import Any

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client


def decode(result: Any) -> Any:
    if getattr(result, "structuredContent", None):
        return result.structuredContent
    texts = [item.text for item in result.content if hasattr(item, "text")]
    if len(texts) == 1:
        try:
            return json.loads(texts[0])
        except json.JSONDecodeError:
            return texts[0]
    return texts


async def smoke(command: str) -> dict[str, Any]:
    params = StdioServerParameters(command=command, args=[])
    async with stdio_client(params) as streams, ClientSession(*streams) as session:
        init = await session.initialize()
        listed = await session.list_tools()
        calls = {}
        specs = [
            ("list_targets", {}),
            (
                "find_function",
                {"target": "gcc-x86_64", "pattern": "main", "limit": 5},
            ),
            (
                "get_callees",
                {
                    "target": "gcc-x86_64",
                    "function": "main",
                    "depth": 1,
                    "limit": 20,
                },
            ),
            (
                "get_callers",
                {
                    "target": "gcc-x86_64",
                    "function": "fputs_unlocked",
                    "depth": 1,
                    "limit": 20,
                },
            ),
            (
                "slice_from",
                {
                    "target": "gcc-x86_64",
                    "entry": "main",
                    "depth": 2,
                    "max_nodes": 50,
                },
            ),
            (
                "paths_between",
                {
                    "target": "gcc-x86_64",
                    "src": "main",
                    "dst": "fputs_unlocked",
                    "max_depth": 4,
                    "max_paths": 10,
                },
            ),
        ]
        for name, arguments in specs:
            result = await session.call_tool(name, arguments)
            calls[name] = {"is_error": result.isError, "result": decode(result)}
        return {
            "initialize": {
                "server_name": init.serverInfo.name,
                "server_version": init.serverInfo.version,
            },
            "tools_list": [tool.name for tool in listed.tools],
            "calls": calls,
        }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--command", default="./scripts/run_callgraph_mcp_ssh.sh")
    args = parser.parse_args()
    result = asyncio.run(smoke(args.command))
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print(args.out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
