"""FastMCP stdio server exposing exactly six bounded callgraph tools."""

from __future__ import annotations

import argparse
from collections.abc import Sequence
from pathlib import Path
from typing import Annotated, Any

from mcp.server.fastmcp import FastMCP
from pydantic import Field

from . import db as graph

FindLimit = Annotated[int, Field(ge=1, le=500)]
TraversalDepth = Annotated[int, Field(ge=1, le=5)]
TraversalLimit = Annotated[int, Field(ge=1, le=5000)]
MaxNodes = Annotated[int, Field(ge=1, le=2000)]
PathDepth = Annotated[int, Field(ge=1, le=8)]
MaxPaths = Annotated[int, Field(ge=1, le=50)]


def create_server(
    db_path: str | Path, *, allowed_targets: Sequence[str] = (),
) -> FastMCP:
    path = Path(db_path).expanduser().resolve()
    mcp = FastMCP("defuzz-callgraph")

    def query(fn: Any, *args: Any, **kwargs: Any) -> dict[str, Any]:
        if fn is not graph.list_targets and allowed_targets and (
            not args or args[0] not in allowed_targets
        ):
            raise ValueError("CallGraph target is outside the source-bound allowlist")
        con = graph.connect(path, read_only=True)
        try:
            result = fn(con, *args, **kwargs)
            if fn is graph.list_targets and allowed_targets:
                result["targets"] = [
                    row for row in result["targets"] if row["target"] in allowed_targets
                ]
                result["counters"] = {
                    "targets": len(result["targets"]),
                    "edges": sum(row["edge_count"] for row in result["targets"]),
                }
            return result
        finally:
            con.close()

    @mcp.tool()
    def list_targets() -> dict[str, Any]:
        """List indexed compiler targets and edge counts."""
        return query(graph.list_targets)

    @mcp.tool()
    def find_function(target: str, pattern: str, limit: FindLimit = 100) -> dict[str, Any]:
        """Find symbols by literal substring; limit must be <=500."""
        return query(graph.find_function, target, pattern, limit)

    @mcp.tool()
    def get_callees(
        target: str, function: str, depth: TraversalDepth = 1, limit: TraversalLimit = 5000
    ) -> dict[str, Any]:
        """Bounded breadth-first outgoing traversal with edge evidence."""
        return query(
            graph.traverse, target, function, direction="callees", depth=depth, limit=limit
        )

    @mcp.tool()
    def get_callers(
        target: str, function: str, depth: TraversalDepth = 1, limit: TraversalLimit = 5000
    ) -> dict[str, Any]:
        """Bounded breadth-first incoming traversal with edge evidence."""
        return query(
            graph.traverse, target, function, direction="callers", depth=depth, limit=limit
        )

    @mcp.tool()
    def slice_from(
        target: str, entry: str, depth: TraversalDepth = 3, max_nodes: MaxNodes = 2000
    ) -> dict[str, Any]:
        """Produce a bounded forward slice from entry without revisiting nodes."""
        return query(graph.slice_from, target, entry, depth, max_nodes)

    @mcp.tool()
    def paths_between(
        target: str, src: str, dst: str, max_depth: PathDepth = 6, max_paths: MaxPaths = 50
    ) -> dict[str, Any]:
        """Enumerate bounded simple paths using reverse-distance-pruned BFS."""
        return query(graph.paths_between, target, src, dst, max_depth, max_paths)

    return mcp


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run the DeFuzz callgraph MCP over stdio")
    parser.add_argument("--db", type=Path, required=True)
    parser.add_argument("--allow-target", action="append", default=[])
    args = parser.parse_args(argv)
    create_server(args.db, allowed_targets=args.allow_target).run(transport="stdio")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
