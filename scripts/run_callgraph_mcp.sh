#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DB="${DEFUZZ_CALLGRAPH_DB:-$REPO_ROOT/callgraph_mcp/data/callgraph.sqlite}"
exec uv run --project "$REPO_ROOT/callgraph_mcp" defuzz-callgraph-mcp --db "$DB"
