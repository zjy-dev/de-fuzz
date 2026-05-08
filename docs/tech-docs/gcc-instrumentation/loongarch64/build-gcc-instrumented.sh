#!/bin/bash
# Symlink/copy of the actual build script
# The actual script is at: target_compilers/gcc-v15.2.0-loongarch64-cross-compile/build-gcc-instrumented.sh
# This file is kept here for documentation consistency with the aarch64 directory structure

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ACTUAL_SCRIPT="${SCRIPT_DIR}/../../../target_compilers/gcc-v15.2.0-loongarch64-cross-compile/build-gcc-instrumented.sh"

if [ -f "$ACTUAL_SCRIPT" ]; then
    exec "$ACTUAL_SCRIPT" "$@"
else
    echo "Error: Build script not found at $ACTUAL_SCRIPT"
    echo "Please run from target_compilers/gcc-v15.2.0-loongarch64-cross-compile/ directory"
    exit 1
fi
