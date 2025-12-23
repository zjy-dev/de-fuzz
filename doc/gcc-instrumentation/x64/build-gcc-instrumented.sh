#!/bin/bash
set -e

# ============================================================================
# GCC x64 Native Build Script with Coverage and CFG Dump Instrumentation
# ============================================================================
# This script builds a native x86_64 GCC compiler with:
# 1. Selective coverage instrumentation for cfgexpand.cc (gcov)
# 2. CFG dump for cfgexpand.cc (-fdump-tree-cfg-lineno)
#
# Prerequisites:
#   - GCC source code (gcc-releases-gcc-12.2.0 or similar)
#   - Build dependencies: flex bison libgmp-dev libmpfr-dev libmpc-dev texinfo
#   - Modified Makefile.in (see Makefile.in.patch)
#
# Usage:
#   ./build-gcc-instrumented.sh [source_dir] [build_dir]
#
# Default paths:
#   source_dir: ./gcc-releases-gcc-12.2.0
#   build_dir:  ./gcc-build
# ============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR="${1:-${SCRIPT_DIR}/gcc-releases-gcc-12.2.0}"
BUILD_DIR="${2:-${SCRIPT_DIR}/gcc-build}"
INSTALL_DIR="${BUILD_DIR}/install"
LOG_DIR="${SCRIPT_DIR}/build-logs"

# Create directories
mkdir -p "${BUILD_DIR}"
mkdir -p "${LOG_DIR}"

echo "============================================"
echo "GCC x64 Native Build Configuration"
echo "============================================"
echo "Source:   ${SRC_DIR}"
echo "Build:    ${BUILD_DIR}"
echo "Install:  ${INSTALL_DIR}"
echo "Logs:     ${LOG_DIR}"
echo ""

# Verify source directory
if [ ! -f "${SRC_DIR}/gcc/Makefile.in" ]; then
    echo "ERROR: GCC source not found at ${SRC_DIR}"
    echo "Please provide the correct path or download GCC source."
    exit 1
fi

# Verify Makefile.in has been patched
if ! grep -q "FUZZ-COVERAGE-INSTRUMENTATION" "${SRC_DIR}/gcc/Makefile.in"; then
    echo "ERROR: Makefile.in has not been patched for instrumentation"
    echo "Please apply the patch from doc/gcc-instrumentation/Makefile.in.patch"
    exit 1
fi

echo "[INFO] Makefile.in patch verified ✓"
echo ""

# Step 1: Configure
echo "[1/4] Configuring GCC..."
cd "${BUILD_DIR}"

if [ ! -f "Makefile" ]; then
    "${SRC_DIR}/configure" \
        --prefix="${INSTALL_DIR}" \
        --enable-languages=c,c++ \
        --disable-bootstrap \
        --disable-multilib \
        --enable-coverage=noopt \
        2>&1 | tee "${LOG_DIR}/configure.log"
    
    echo "✓ Configure completed"
else
    echo "✓ Makefile exists, skipping configure"
fi

# Step 2: Build GCC
echo ""
echo "[2/4] Building GCC (this may take 30-60 minutes)..."
echo "Build log: ${LOG_DIR}/build-verbose.log"

# Build GCC compiler
echo "Building GCC compiler (all-gcc)..."
make -j$(nproc) VERBOSE=1 all-gcc 2>&1 | tee "${LOG_DIR}/build-verbose.log"

# Build target libraries (libgcc required for linking)
echo ""
echo "Building target libraries (all-target-libgcc)..."
make -j$(nproc) all-target-libgcc 2>&1 | tee -a "${LOG_DIR}/build-verbose.log"

echo "✓ Build completed"

# Step 3: Verify CFG dump
echo ""
echo "[3/4] Verifying CFG dump generation..."

CFG_FILES=$(find "${BUILD_DIR}/gcc" -name "cfgexpand.*.cfg" 2>/dev/null || true)
if [ -n "$CFG_FILES" ]; then
    echo "✓ CFG dump files found:"
    echo "$CFG_FILES" | while read -r file; do
        size=$(du -h "$file" | cut -f1)
        echo "  - $(basename $file) (${size})"
    done
else
    echo "✗ No CFG dump files found!"
fi

# Step 4: Verify selective instrumentation
echo ""
echo "[4/4] Verifying selective instrumentation..."

# Check cfgexpand.cc compilation flags
if grep "cfgexpand.cc" "${LOG_DIR}/build-verbose.log" | grep -q "\-fdump-tree-cfg-lineno"; then
    echo "✓ cfgexpand.cc has -fdump-tree-cfg-lineno flag"
else
    echo "✗ cfgexpand.cc missing -fdump-tree-cfg-lineno flag"
fi

if grep "cfgexpand.cc" "${LOG_DIR}/build-verbose.log" | grep -q "\-fprofile-arcs"; then
    echo "✓ cfgexpand.cc has coverage flags"
else
    echo "✗ cfgexpand.cc missing coverage flags"
fi

# Summary
echo ""
echo "============================================"
echo "Build Summary"
echo "============================================"
GCNO_COUNT=$(find "${BUILD_DIR}/gcc" -name "*.gcno" 2>/dev/null | wc -l)
CFG_COUNT=$(find "${BUILD_DIR}/gcc" -name "*.cfg" 2>/dev/null | wc -l)
echo "Coverage files (.gcno): ${GCNO_COUNT}"
echo "CFG dump files (.cfg):  ${CFG_COUNT}"
echo ""
echo "GCC binary: ${BUILD_DIR}/gcc/xgcc"
echo "CFG files:  ${BUILD_DIR}/gcc/*.cfg"
echo ""
echo "Done!"
