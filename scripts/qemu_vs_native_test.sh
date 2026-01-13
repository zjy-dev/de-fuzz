#!/bin/bash
# QEMU vs Native Performance Comparison
# Compares aarch64 QEMU emulation vs native x86_64 execution
#
# Usage: ./scripts/qemu_vs_native_test.sh [iterations]

set -e

ITERATIONS=${1:-100}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
TEST_DIR="/tmp/qemu_perf_test"

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_step() { echo -e "${BLUE}[STEP]${NC} $1"; }

echo ""
log_info "============================================================"
log_info "QEMU vs Native Performance Test"
log_info "============================================================"
log_info "Iterations: ${ITERATIONS}"
log_info "============================================================"
echo ""

# Setup test directory
mkdir -p "$TEST_DIR"
cp "$PROJECT_ROOT/initial_seeds/aarch64/canary/id-000001-src-000000-cov-00000-11111111/source.c" "$TEST_DIR/test.c"

# Compile for both architectures
log_step "Compiling for aarch64 (cross-compile)..."
COMPILE_AARCH64_START=$(date +%s.%N)
aarch64-linux-gnu-gcc -fstack-protector-strong -static -o "$TEST_DIR/test_aarch64" "$TEST_DIR/test.c"
COMPILE_AARCH64_END=$(date +%s.%N)
COMPILE_AARCH64=$(echo "$COMPILE_AARCH64_END - $COMPILE_AARCH64_START" | bc)

log_step "Compiling for x86_64 (native)..."
COMPILE_X86_START=$(date +%s.%N)
gcc -fstack-protector-strong -o "$TEST_DIR/test_x86" "$TEST_DIR/test.c"
COMPILE_X86_END=$(date +%s.%N)
COMPILE_X86=$(echo "$COMPILE_X86_END - $COMPILE_X86_START" | bc)

echo ""
log_info "Compile times:"
log_info "  aarch64 cross-compile: ${COMPILE_AARCH64}s"
log_info "  x86_64 native:         ${COMPILE_X86}s"
echo ""

# Run execution tests
log_step "Running QEMU aarch64 execution test (${ITERATIONS} iterations)..."
QEMU_START=$(date +%s.%N)
for i in $(seq 1 $ITERATIONS); do
    qemu-aarch64 "$TEST_DIR/test_aarch64" 64 32 >/dev/null 2>&1
done
QEMU_END=$(date +%s.%N)
QEMU_TOTAL=$(echo "$QEMU_END - $QEMU_START" | bc)
QEMU_AVG=$(echo "scale=6; $QEMU_TOTAL / $ITERATIONS" | bc)

log_step "Running native x86_64 execution test (${ITERATIONS} iterations)..."
NATIVE_START=$(date +%s.%N)
for i in $(seq 1 $ITERATIONS); do
    "$TEST_DIR/test_x86" 64 32 >/dev/null 2>&1
done
NATIVE_END=$(date +%s.%N)
NATIVE_TOTAL=$(echo "$NATIVE_END - $NATIVE_START" | bc)
NATIVE_AVG=$(echo "scale=6; $NATIVE_TOTAL / $ITERATIONS" | bc)

# Calculate speedup
SPEEDUP=$(echo "scale=2; $QEMU_TOTAL / $NATIVE_TOTAL" | bc)

echo ""
log_info "============================================================"
log_info "Results (${ITERATIONS} iterations)"
log_info "============================================================"
log_info ""
log_info "Compilation:"
log_info "  aarch64 cross-compile: ${COMPILE_AARCH64}s"
log_info "  x86_64 native:         ${COMPILE_X86}s"
log_info ""
log_info "Execution:"
log_info "  QEMU aarch64 total:    ${QEMU_TOTAL}s"
log_info "  QEMU aarch64 avg:      ${QEMU_AVG}s/exec"
log_info "  Native x86_64 total:   ${NATIVE_TOTAL}s"
log_info "  Native x86_64 avg:     ${NATIVE_AVG}s/exec"
log_info ""
log_info "  Native is ${SPEEDUP}x faster than QEMU"
log_info "============================================================"

# Save results to JSON
RESULTS_FILE="${PROJECT_ROOT}/docs/qemu_vs_native_results.json"
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

cat > "$RESULTS_FILE" << EOF
{
  "timestamp": "$TIMESTAMP",
  "iterations": $ITERATIONS,
  "compilation": {
    "aarch64_cross_seconds": $COMPILE_AARCH64,
    "x86_64_native_seconds": $COMPILE_X86
  },
  "execution": {
    "qemu_aarch64": {
      "total_seconds": $QEMU_TOTAL,
      "avg_seconds": $QEMU_AVG
    },
    "native_x86_64": {
      "total_seconds": $NATIVE_TOTAL,
      "avg_seconds": $NATIVE_AVG
    },
    "speedup_factor": $SPEEDUP
  }
}
EOF

log_info "Results saved to: ${RESULTS_FILE}"

# Cleanup
rm -rf "$TEST_DIR"

echo ""
log_info "Done!"
