#!/bin/bash
set -e

# ============================================================================
# AArch64 Cross-Compiler Build Script with Coverage and CFG Dump Support
# ============================================================================
# Based on ARM GNU Toolchain 12.2.Rel1
# Target: aarch64-none-linux-gnu (cross-compiler)
#
# This script builds an aarch64 cross-compiler with:
# 1. Selective coverage instrumentation for cfgexpand.cc (gcov) on HOST
# 2. CFG dump for cfgexpand.cc (-fdump-tree-cfg-lineno) on HOST
#
# Prerequisites:
#   - ARM GNU Toolchain source (arm-gnu-toolchain-src-snapshot-12.2.rel1)
#   - Build dependencies: build-essential flex bison texinfo
#   - Modified Makefile.in (see ../Makefile.in.patch)
#
# NOTE: This builds a cross-compiler that runs on x86_64 and generates
#       aarch64 binaries. The coverage is for the compiler itself.
#
# Usage:
#   ./build-gcc-instrumented.sh [source_dir] [build_base] [install_dir]
# ============================================================================

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step() { echo -e "${BLUE}[STEP]${NC} $1"; }

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR="${1:-${SCRIPT_DIR}/arm-gnu-toolchain-src-snapshot-12.2.rel1}"
BUILD_BASE="${2:-${SCRIPT_DIR}/build-aarch64-none-linux-gnu}"
INSTALL_DIR="${3:-${SCRIPT_DIR}/install-aarch64-none-linux-gnu}"
HOST_TOOLS="${BUILD_BASE}/host-tools"
TARGET="aarch64-none-linux-gnu"
SYSROOT="${INSTALL_DIR}/${TARGET}/libc"
LOG_DIR="${SCRIPT_DIR}/build-logs"
JOBS=$(nproc)

# Create directories
mkdir -p "${BUILD_BASE}" "${INSTALL_DIR}" "${HOST_TOOLS}" "${SYSROOT}" "${LOG_DIR}"
export PATH="${INSTALL_DIR}/bin:${PATH}"

echo ""
log_info "============================================================"
log_info "AArch64 Cross-Compiler Build with Coverage & CFG Dump"
log_info "============================================================"
log_info "Source:  ${SRC_DIR}"
log_info "Build:   ${BUILD_BASE}"
log_info "Install: ${INSTALL_DIR}"
log_info "Target:  ${TARGET}"
log_info "Jobs:    ${JOBS}"
log_info "============================================================"
echo ""

# Verify source and patch
if [ ! -d "${SRC_DIR}/arm-gnu-toolchain-src-snapshot-12.2.rel1/gcc" ]; then
    log_error "Source not found at ${SRC_DIR}"
    exit 1
fi

MAKEFILE_IN="${SRC_DIR}/arm-gnu-toolchain-src-snapshot-12.2.rel1/gcc/Makefile.in"
if ! grep -q "FUZZ-COVERAGE-INSTRUMENTATION" "${MAKEFILE_IN}"; then
    log_error "Makefile.in not patched. Apply doc/gcc-instrumentation/Makefile.in.patch first."
    exit 1
fi
log_info "Makefile.in patch verified ✓"

# ============================================================================
# Stage 1: Build host tools (GMP, MPFR, MPC, ISL)
# ============================================================================
log_step "=== Stage 1/8: Host tools (GMP, MPFR, MPC, ISL) ==="

for lib in gmp mpfr mpc isl; do
    case $lib in
        gmp)  lib_file="libgmp.a";  config_opts="--disable-maintainer-mode --disable-shared --host=x86_64-pc-linux-gnu" ;;
        mpfr) lib_file="libmpfr.a"; config_opts="--disable-maintainer-mode --disable-shared --with-gmp=${HOST_TOOLS}" ;;
        mpc)  lib_file="libmpc.a";  config_opts="--disable-maintainer-mode --disable-shared --with-gmp=${HOST_TOOLS} --with-mpfr=${HOST_TOOLS}" ;;
        isl)  lib_file="libisl.a";  config_opts="--disable-maintainer-mode --disable-shared --with-gmp-prefix=${HOST_TOOLS}" ;;
    esac
    
    if [ ! -f "${HOST_TOOLS}/lib/${lib_file}" ]; then
        log_info "Building ${lib}..."
        cd "${BUILD_BASE}" && mkdir -p ${lib}-build && cd ${lib}-build
        "${SRC_DIR}/${lib}/configure" --prefix="${HOST_TOOLS}" ${config_opts}
        make -j${JOBS} && make install
        log_info "✓ ${lib} completed"
    else
        log_info "✓ ${lib} already built"
    fi
done

# ============================================================================
# Stage 2: Binutils
# ============================================================================
log_step "=== Stage 2/8: Binutils ==="
if [ ! -f "${INSTALL_DIR}/bin/${TARGET}-as" ]; then
    cd "${BUILD_BASE}" && mkdir -p binutils-build && cd binutils-build
    "${SRC_DIR}/binutils-gdb/configure" \
        --enable-64-bit-bfd --target="${TARGET}" --prefix="${INSTALL_DIR}" \
        --with-build-sysroot="${SYSROOT}" --with-sysroot="/${TARGET}/libc" \
        --enable-gold --enable-plugins --disable-doc --disable-gdb --disable-nls --disable-werror
    make -j${JOBS} && make install
    log_info "✓ Binutils completed"
else
    log_info "✓ Binutils already built"
fi

# ============================================================================
# Stage 3: Linux headers
# ============================================================================
log_step "=== Stage 3/8: Linux headers ==="
if [ ! -d "${SYSROOT}/usr/include/linux" ]; then
    cd "${SRC_DIR}/linux"
    make ARCH=arm64 INSTALL_HDR_PATH="${SYSROOT}/usr" headers_install
    log_info "✓ Linux headers installed"
else
    log_info "✓ Linux headers already installed"
fi

# ============================================================================
# Stage 4: GCC stage 1 (bootstrap)
# ============================================================================
log_step "=== Stage 4/8: GCC stage 1 (bootstrap) ==="
if [ ! -f "${BUILD_BASE}/gcc1-build/gcc/xgcc" ]; then
    cd "${BUILD_BASE}" && rm -rf gcc1-build && mkdir -p gcc1-build && cd gcc1-build
    "${SRC_DIR}/arm-gnu-toolchain-src-snapshot-12.2.rel1/configure" \
        --target="${TARGET}" --prefix="${INSTALL_DIR}" \
        --with-sysroot="/${TARGET}/libc" --with-build-sysroot="${SYSROOT}" \
        --without-headers --with-newlib --disable-shared --disable-threads \
        --disable-libatomic --disable-libssp --disable-libgomp --disable-libquadmath \
        --enable-languages=c --enable-checking=yes --enable-coverage \
        --with-gmp="${HOST_TOOLS}" --with-mpfr="${HOST_TOOLS}" --with-mpc="${HOST_TOOLS}"
    make -j${JOBS} VERBOSE=1 all-gcc 2>&1 | tee "${LOG_DIR}/gcc1-build.log"
    make install-gcc
    log_info "✓ GCC stage 1 completed"
else
    log_info "✓ GCC stage 1 already built"
fi

# ============================================================================
# Stage 5: glibc headers + CSUs
# ============================================================================
log_step "=== Stage 5/8: glibc headers ==="
if [ ! -f "${SYSROOT}/usr/lib/crt1.o" ]; then
    cd "${BUILD_BASE}" && rm -rf glibc-build && mkdir -p glibc-build && cd glibc-build
    CC="${TARGET}-gcc" CXX="${TARGET}-g++" AR="${TARGET}-ar" RANLIB="${TARGET}-ranlib" \
    "${SRC_DIR}/glibc/configure" \
        --enable-shared --disable-profile --disable-sanity-checks --prefix=/usr \
        --with-headers="${SYSROOT}/usr/include" --build=x86_64-pc-linux-gnu \
        --host="${TARGET}" --disable-werror
    make install-headers install_root="${SYSROOT}"
    touch "${SYSROOT}/usr/include/gnu/stubs.h" "${SYSROOT}/usr/include/bits/stdio_lim.h"
    make -j${JOBS} csu/subdir_lib
    mkdir -p "${SYSROOT}/usr/lib"
    find . -name 'crt*.o' -exec cp {} "${SYSROOT}/usr/lib/" \;
    echo "/* GNU ld script */
GROUP ( libc.so.6 libc_nonshared.a AS_NEEDED ( ld-linux-aarch64.so.1 ) )" > "${SYSROOT}/usr/lib/libc.so"
    touch "${SYSROOT}/usr/lib/libc.so.6" "${SYSROOT}/usr/lib/libc_nonshared.a" "${SYSROOT}/usr/lib/ld-linux-aarch64.so.1"
    log_info "✓ glibc headers completed"
else
    log_info "✓ glibc headers already installed"
fi

# ============================================================================
# Stage 6: GCC stage 2 (libgcc)
# ============================================================================
log_step "=== Stage 6/8: GCC stage 2 (libgcc) ==="
if [ ! -f "${INSTALL_DIR}/lib/gcc/${TARGET}/12.2.1/libgcc.a" ]; then
    cd "${BUILD_BASE}" && rm -rf gcc2-build && mkdir -p gcc2-build && cd gcc2-build
    "${SRC_DIR}/arm-gnu-toolchain-src-snapshot-12.2.rel1/configure" \
        --target="${TARGET}" --prefix="${INSTALL_DIR}" \
        --with-sysroot="/${TARGET}/libc" --with-build-sysroot="${SYSROOT}" \
        --enable-shared --disable-libatomic --disable-libssp --disable-libgomp --disable-libquadmath \
        --enable-languages=c --enable-checking=yes --enable-coverage \
        --with-gmp="${HOST_TOOLS}" --with-mpfr="${HOST_TOOLS}" --with-mpc="${HOST_TOOLS}"
    make -j${JOBS} all-target-libgcc && make install-target-libgcc
    log_info "✓ GCC stage 2 completed"
else
    log_info "✓ GCC stage 2 already built"
fi

# ============================================================================
# Stage 7: Full glibc
# ============================================================================
log_step "=== Stage 7/8: Full glibc ==="
if [ ! -f "${SYSROOT}/lib/libc.so.6" ]; then
    cd "${BUILD_BASE}/glibc-build"
    make -j${JOBS} && make install install_root="${SYSROOT}"
    log_info "✓ Full glibc completed"
else
    log_info "✓ Full glibc already built"
fi

# ============================================================================
# Stage 8: Final GCC
# ============================================================================
log_step "=== Stage 8/8: Final GCC with coverage ==="
FINAL_GCC_BUILD="${BUILD_BASE}/gcc-final-build"

if [ ! -f "${FINAL_GCC_BUILD}/gcc/xgcc" ] || [ ! -f "${INSTALL_DIR}/bin/${TARGET}-gcc" ]; then
    cd "${BUILD_BASE}" && rm -rf gcc-final-build && mkdir -p gcc-final-build && cd gcc-final-build
    "${SRC_DIR}/arm-gnu-toolchain-src-snapshot-12.2.rel1/configure" \
        --target="${TARGET}" --prefix="${INSTALL_DIR}" \
        --with-sysroot="/${TARGET}/libc" --with-build-sysroot="${SYSROOT}" \
        --enable-gnu-indirect-function --enable-shared \
        --disable-libquadmath --disable-libgomp --disable-libssp --disable-libatomic \
        --disable-libitm --disable-libsanitizer --disable-libstdcxx --disable-libbacktrace \
        --enable-checking=release --enable-languages=c --enable-coverage \
        --with-gmp="${HOST_TOOLS}" --with-mpfr="${HOST_TOOLS}" --with-mpc="${HOST_TOOLS}" --with-isl="${HOST_TOOLS}"
    
    make -j${JOBS} VERBOSE=1 all-gcc 2>&1 | tee "${LOG_DIR}/gcc-final-build.log"
    make -j${JOBS} all-target-libgcc 2>&1 | tee -a "${LOG_DIR}/gcc-final-build.log"
    
    # Build libbacktrace (required for install-gcc)
    log_info "Building libbacktrace..."
    mkdir -p libbacktrace && cd libbacktrace
    "${SRC_DIR}/arm-gnu-toolchain-src-snapshot-12.2.rel1/libbacktrace/configure" --host=x86_64-pc-linux-gnu
    make -j${JOBS} && cd ..
    
    make install-gcc
    log_info "✓ Final GCC completed"
else
    log_info "✓ Final GCC already built"
fi

# ============================================================================
# Verification
# ============================================================================
echo ""
log_step "Verification"
echo ""

CFG_FILE="${FINAL_GCC_BUILD}/gcc/cfgexpand.cc.015t.cfg"
if [ -f "$CFG_FILE" ]; then
    echo -e "${GREEN}✓${NC} CFG dump: $(du -h "$CFG_FILE" | cut -f1)"
else
    echo -e "${YELLOW}!${NC} CFG dump not found"
fi

GCNO_FILE="${FINAL_GCC_BUILD}/gcc/cfgexpand.gcno"
if [ -f "$GCNO_FILE" ]; then
    echo -e "${GREEN}✓${NC} Coverage: $(du -h "$GCNO_FILE" | cut -f1)"
else
    echo -e "${YELLOW}!${NC} Coverage file not found"
fi

echo ""
log_info "============================================================"
log_info "Build Summary"
log_info "============================================================"
log_info "Compiler: ${INSTALL_DIR}/bin/${TARGET}-gcc"
log_info "CFG file: ${FINAL_GCC_BUILD}/gcc/cfgexpand.*.cfg"
log_info ""
log_info "Usage:"
echo "  export PATH=\"${INSTALL_DIR}/bin:\$PATH\""
echo "  ${TARGET}-gcc --sysroot=${SYSROOT} -o test test.c"
echo ""
log_info "Done!"
