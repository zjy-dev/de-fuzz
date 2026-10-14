#!/usr/bin/env bash
# Select the target explicitly; the host clang binary defaults to x86_64.
set -euo pipefail
exec /opt/llvm23/bin/clang --target=aarch64-linux-gnu \
  --gcc-toolchain=/opt/gcc16 -fuse-ld=lld "$@"
