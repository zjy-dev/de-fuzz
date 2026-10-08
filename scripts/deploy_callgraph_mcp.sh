#!/usr/bin/env bash
set -euo pipefail

HOST="${DEFUZZ_CALLGRAPH_HOST:-x64_8c16g_01}"
REMOTE_ROOT="${DEFUZZ_CALLGRAPH_REMOTE_ROOT:-~/defuzz-codeql}"
MODE="safe"
case "${1:-}" in
  "") ;;
  --code-only) MODE="code-only" ;;
  --force-ingest) MODE="force-ingest" ;;
  *) printf 'usage: %s [--code-only|--force-ingest]\n' "$0" >&2; exit 2 ;;
esac
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ARTIFACTS="$REMOTE_ROOT/artifacts/callgraph-mcp"
DB="$REMOTE_ROOT/out/callgraph.sqlite"
ssh "$HOST" "mkdir -p $ARTIFACTS"
rsync -az --delete --exclude .venv --exclude .pytest_cache --exclude __pycache__ "$REPO_ROOT/callgraph_mcp/" "$HOST:$ARTIFACTS/"
ssh "$HOST" "rm -rf $ARTIFACTS/.venv && python3 -m venv $ARTIFACTS/.venv && $ARTIFACTS/.venv/bin/pip install -q --upgrade $ARTIFACTS"
if [[ "$MODE" == "code-only" ]]; then exit 0; fi
if [[ "$MODE" == "safe" ]] && ssh "$HOST" "test -f $DB"; then
  printf 'database exists; leaving unchanged: %s:%s\n' "$HOST" "$DB" >&2
  exit 0
fi
ssh "$HOST" "$ARTIFACTS/.venv/bin/defuzz-callgraph-ingest --out $DB --add llvm $REMOTE_ROOT/out/llvm-callgraph.csv --add gcc-x86_64 $REMOTE_ROOT/out/gcc-x86_64-callgraph.csv --add gcc-aarch64 $REMOTE_ROOT/out/gcc-aarch64-callgraph.csv"
