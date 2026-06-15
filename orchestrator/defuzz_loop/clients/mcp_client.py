"""MCP client wrapper for agent tool calls.

Agents reach the Go-core read-only tools (search_source / query_invariants /
coverage_diff / creduce_run / compile_exec) over MCP. Every call and its result
digest is appended to the blackboard's tool_call_log so replay/audit never has a
gap (R5). The Go MCP server is exposed via Streamable HTTP at /mcp.
"""

from __future__ import annotations

import hashlib
import json
from typing import Any

from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

from ..state import ToolCall


def _digest(payload: Any) -> str:
    raw = json.dumps(payload, sort_keys=True, default=str)
    return hashlib.sha256(raw.encode()).hexdigest()[:16]


class MCPClient:
    """Async client over the Streamable HTTP MCP endpoint.

    Each ``call_tool`` appends a ToolCall record to ``tool_log`` (the caller passes
    the blackboard's tool_call_log list) keeping the run reproducible.
    """

    def __init__(self, url: str = "http://127.0.0.1:50052/mcp") -> None:
        self._url = url

    async def list_tools(self) -> list[str]:
        async with streamablehttp_client(self._url) as (read, write, _):
            async with ClientSession(read, write) as session:
                await session.initialize()
                resp = await session.list_tools()
                return [t.name for t in resp.tools]

    async def call_tool(
        self,
        name: str,
        args: dict[str, Any],
        *,
        agent: str,
        round: int,
        tool_log: list[ToolCall] | None = None,
    ) -> dict[str, Any]:
        async with streamablehttp_client(self._url) as (read, write, _):
            async with ClientSession(read, write) as session:
                await session.initialize()
                result = await session.call_tool(name, args)

        payload = result.structuredContent if result.structuredContent is not None else {}
        if tool_log is not None:
            tool_log.append(
                ToolCall(
                    round=round,
                    agent=agent,
                    tool=name,
                    args={k: str(v) for k, v in args.items()},
                    result_digest=_digest(payload),
                )
            )
        return payload
