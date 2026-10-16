#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DEEPSEEK_API_KEY:-}" ]]; then
  if [[ -f "$(dirname "${BASH_SOURCE[0]}")/../.work/gcc16-aarch64-run/.env" ]]; then
    # shellcheck disable=SC1091
    source "$(dirname "${BASH_SOURCE[0]}")/../.work/gcc16-aarch64-run/.env"
  fi
fi

if [[ -z "${DEEPSEEK_API_KEY:-}" ]]; then
  echo "Error: DEEPSEEK_API_KEY environment variable is not set" >&2
  exit 1
fi

export PYTHONUNBUFFERED=1

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

OUTPUT_ROOT="$PROJECT_ROOT/runs/gcc16-aarch64-deepseek"
mkdir -p "$OUTPUT_ROOT"

echo "=== [$(date '+%Y-%m-%d %H:%M:%S')] Starting GCC 16.2 AArch64 Experiment (DeepSeek-Flash) ==="
echo "Output directory: $OUTPUT_ROOT"
echo "Model: deepseek/deepseek-flash"
echo "ISA target: aarch64"
echo "Parity reference: /Users/bytedance/projects/research/defend-reviewer/main"

"$PROJECT_ROOT/orchestrator/.venv/bin/python" -m defuzz_loop.experiments_cli run-all \
  --output-root "$OUTPUT_ROOT" \
  --source-root "$PROJECT_ROOT/.work/toolchains-matched/gcc-source" \
  --target-tree "$PROJECT_ROOT/.work/toolchains-matched/gcc-source" \
  --toolchains-config "$PROJECT_ROOT/.work/gcc16-aarch64-run/toolchains.host.yaml" \
  --pi-config "$PROJECT_ROOT/.work/gcc16-aarch64-run/pi-runtime.deepseek.json" \
  --corpus-root "$PROJECT_ROOT/.work/gcc16-security-corpus" \
  --checker-source-root "$PROJECT_ROOT" \
  --backend pi \
  --isa aarch64 \
  --demo-parity \
  --max-concurrency 4 \
  --token-budget 300000000 \
  --time-budget-minutes 360 "$@" 2>&1 | tee "$OUTPUT_ROOT/experiment.log"

echo "=== [$(date '+%Y-%m-%d %H:%M:%S')] Experiment finished ==="
