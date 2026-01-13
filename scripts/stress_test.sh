#!/bin/bash
# Stress Test Script for DeFuzz
# This script tests the performance of the traditional fuzzer components
# (compilation, coverage measurement, oracle) without LLM involvement.
#
# Usage: ./scripts/stress_test.sh [num_copies]
#   num_copies: Number of seed copies to create (default: 64)

set -e

# Configuration
NUM_COPIES=${1:-64}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
STRESS_TEST_DIR="${PROJECT_ROOT}/stress_test_out"
INITIAL_SEEDS_DIR="${PROJECT_ROOT}/initial_seeds/aarch64/canary"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_step() { echo -e "${BLUE}[STEP]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }

echo ""
log_info "============================================================"
log_info "DeFuzz Stress Test - Traditional Components Performance"
log_info "============================================================"
log_info "Copies:     ${NUM_COPIES}"
log_info "Output:     ${STRESS_TEST_DIR}"
log_info "============================================================"
echo ""

# Clean up previous stress test
log_step "Cleaning up previous stress test..."
rm -rf "${STRESS_TEST_DIR}"
mkdir -p "${STRESS_TEST_DIR}/corpus"

# Find a seed file to copy
SEED_FILE=""
for dir in "${INITIAL_SEEDS_DIR}"/id-*; do
    if [ -d "$dir" ] && [ -f "$dir/source.c" ]; then
        SEED_FILE="$dir/source.c"
        break
    fi
done

if [ -z "$SEED_FILE" ]; then
    log_warn "No seed file found in ${INITIAL_SEEDS_DIR}"
    exit 1
fi

log_info "Using seed: ${SEED_FILE}"

# Create copies of the seed
log_step "Creating ${NUM_COPIES} copies of the seed..."
for i in $(seq 1 $NUM_COPIES); do
    seed_id=$(printf "%06d" $i)
    seed_dir="${STRESS_TEST_DIR}/corpus/id-${seed_id}-src-000000-cov-00000-00000000"
    mkdir -p "$seed_dir"
    cp "$SEED_FILE" "$seed_dir/source.c"
    
    # Create a minimal .seed file
    echo "// Stress test seed $i" > "${seed_dir}/id-${seed_id}-src-000000-cov-00000-00000000.seed"
    cat "$SEED_FILE" >> "${seed_dir}/id-${seed_id}-src-000000-cov-00000-00000000.seed"
done

log_info "Created ${NUM_COPIES} seed copies"

# Build defuzz if needed
log_step "Building defuzz..."
cd "$PROJECT_ROOT"
make build > /dev/null 2>&1

# Run stress test with --limit 0 (only process initial seeds, no constraint solving)
log_step "Running stress test (--limit 0)..."
echo ""

START_TIME=$(date +%s.%N)

# Run with debug log level to see timing information
./defuzz fuzz --output "${STRESS_TEST_DIR}" --limit 0 --log-dir "" 2>&1 | tee "${STRESS_TEST_DIR}/stress_test.log"

END_TIME=$(date +%s.%N)
ELAPSED=$(echo "$END_TIME - $START_TIME" | bc)

echo ""
log_info "============================================================"
log_info "Stress Test Results"
log_info "============================================================"
log_info "Total seeds:      ${NUM_COPIES}"
log_info "Total time:       ${ELAPSED}s"

if command -v bc &> /dev/null; then
    AVG_TIME=$(echo "scale=3; $ELAPSED / $NUM_COPIES" | bc)
    SEEDS_PER_SEC=$(echo "scale=2; $NUM_COPIES / $ELAPSED" | bc)
    log_info "Avg per seed:     ${AVG_TIME}s"
    log_info "Seeds/second:     ${SEEDS_PER_SEC}"
fi

log_info "============================================================"
echo ""
log_info "For detailed timing breakdown, run with debug log level:"
echo "  ./defuzz fuzz --output ${STRESS_TEST_DIR} --limit 0 2>&1 | grep TIMING"
echo ""
log_info "Done!"
