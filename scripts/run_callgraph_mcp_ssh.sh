#!/usr/bin/env bash
set -euo pipefail
HOST="${DEFUZZ_CALLGRAPH_HOST:-x64_8c16g_01}"
REMOTE_ROOT="${DEFUZZ_CALLGRAPH_REMOTE_ROOT:-~/defuzz-codeql}"
exec ssh -T "$HOST" "$REMOTE_ROOT/artifacts/callgraph-mcp/.venv/bin/defuzz-callgraph-mcp --db $REMOTE_ROOT/out/callgraph.sqlite"
