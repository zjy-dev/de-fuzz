#!/usr/bin/env bash
# Build the actual GCC release used by the bundled AArch64 CodeQL graph.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
source_root=${DEFUZZ_GCC162_SOURCE:-"$root/.work/toolchains-matched/gcc-source"}
output_root=${DEFUZZ_MATCHED_TOOLCHAINS_ROOT:-"$root/.work/toolchains-matched"}
image=${DEFUZZ_TOOLCHAIN_BUILD_IMAGE:-localhost/defuzz-core:toolchain-bootstrap-20261012}
[[ $(cat "$source_root/gcc/BASE-VER") == 16.2.0 ]] || {
  echo "GCC source must be the verified 16.2.0 release" >&2; exit 65;
}
mkdir -p "$output_root/gcc-aarch64-build" "$output_root/gcc-aarch64-install"
podman run --rm --platform linux/amd64 --entrypoint /bin/bash \
  --name defuzz-gcc162-aarch64-build \
  -v "$source_root:/src:ro" \
  -v "$output_root/gcc-aarch64-build:/build" \
  -v "$output_root/gcc-aarch64-install:/install" \
  -w /build "$image" -c '
set -euo pipefail
if [[ ! -f Makefile ]]; then
  /src/configure --build=x86_64-linux-gnu --host=x86_64-linux-gnu \
    --target=aarch64-linux-gnu --prefix=/opt/gcc16 \
    --enable-languages=c,c++ --disable-bootstrap --disable-multilib --disable-nls \
    --disable-werror --disable-shared --disable-threads --disable-libatomic \
    --disable-libgomp --disable-libitm --disable-libquadmath --disable-libsanitizer \
    --disable-libssp --disable-libstdcxx --without-headers --with-newlib
fi
make -j3 CC=x86_64-linux-gnu-gcc CXX=x86_64-linux-gnu-g++ all-gcc all-target-libgcc
make CC=x86_64-linux-gnu-gcc CXX=x86_64-linux-gnu-g++ DESTDIR=/install install-gcc install-target-libgcc
runtime=/install/opt/gcc16/lib/gcc/aarch64-linux-gnu/16.2.0
if [[ -f "$runtime/libgcc.a" && ! -e "$runtime/libgcc_eh.a" ]]; then
  ln -s libgcc.a "$runtime/libgcc_eh.a"
fi
'
