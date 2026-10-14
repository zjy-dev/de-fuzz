#!/usr/bin/env bash
# Finish target runtimes and install GCC after the compiler build succeeds.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
matched_root=${DEFUZZ_MATCHED_TOOLCHAINS_ROOT:-"$root/.work/toolchains-matched"}
image=${DEFUZZ_TOOLCHAIN_BUILD_IMAGE:-localhost/defuzz-core:toolchain-bootstrap-20261012}
target=${1:-aarch64}
case "$target" in
  aarch64|x86_64) tuple="$target-linux-gnu" ;;
  *) echo "usage: $0 aarch64|x86_64" >&2; exit 64 ;;
esac
podman run --rm --platform linux/amd64 --entrypoint /bin/bash \
  --name "defuzz-gcc162-$target-install" \
  -v "$matched_root/gcc-source:/src:ro" \
  -v "$matched_root/gcc-$target-build:/build" \
  -v "$matched_root/gcc-$target-install:/install" \
  -w /build "$image" -c '
set -euo pipefail
make -j2 CC=x86_64-linux-gnu-gcc CXX=x86_64-linux-gnu-g++ all-target-libgcc
make CC=x86_64-linux-gnu-gcc CXX=x86_64-linux-gnu-g++ \
  DESTDIR=/install install-gcc install-target-libgcc
if [[ "$1" == x86_64-linux-gnu ]]; then
  ln -sf x86_64-linux-gnu-gcc /install/opt/gcc16/bin/gcc
  ln -sf x86_64-linux-gnu-g++ /install/opt/gcc16/bin/g++
else
  runtime=/install/opt/gcc16/lib/gcc/"$1"/16.2.0
  [[ -e "$runtime/libgcc_eh.a" ]] || ln -s libgcc.a "$runtime/libgcc_eh.a"
fi
' bash "$tuple"
