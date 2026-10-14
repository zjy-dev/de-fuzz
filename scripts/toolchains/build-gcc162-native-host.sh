#!/usr/bin/env bash
# Build x86-hosted GCC with a native AArch64 clang compiler.
# Use a consistent host compiler for every compiler object; do not mix a
# GCC-configured partial build with Clang objects (host headers have ABI switches).
# ARM64 build image contains clang/lld and Debian x86_64 cross libc/libstdc++.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
matched_root=${DEFUZZ_MATCHED_TOOLCHAINS_ROOT:-"$root/.work/toolchains-matched"}
image=${DEFUZZ_NATIVE_BUILD_IMAGE:-localhost/defuzz-gcc-build-arm64:20261012}
jobs=${DEFUZZ_GCC_BUILD_JOBS:-3}
target=${1:-aarch64}
case "$target" in
  aarch64) tuple=aarch64-linux-gnu ;;
  x86_64) tuple=x86_64-linux-gnu ;;
  *) echo "usage: $0 aarch64|x86_64" >&2; exit 64 ;;
esac
mkdir -p "$matched_root/gcc-$target-build" "$matched_root/gcc-$target-install"
podman run --rm --platform linux/arm64 --entrypoint /bin/bash \
  --name "defuzz-gcc162-$target-native-build" \
  -v "$matched_root/gcc-source:/src:ro" \
  -v "$matched_root:/matched:ro" \
  -v "$matched_root/gcc-$target-build:/build" \
  -v "$matched_root/gcc-$target-install:/install" \
  -w /build "$image" -c '
set -euo pipefail
tuple=$1
tar -xf /matched/gmp-headers.tar -C /usr/include
tar -xf /matched/isl-headers.tar -C /usr/include
cp /matched/libisl.so.23.2.0 /usr/x86_64-linux-gnu/lib/
ln -sf libisl.so.23.2.0 /usr/x86_64-linux-gnu/lib/libisl.so.23
ln -sf libisl.so.23.2.0 /usr/x86_64-linux-gnu/lib/libisl.so
cp /matched/libgmp.so.10.4.1 /matched/libmpfr.so.6.2.0 /matched/libmpc.so.3.3.1 \
  /usr/x86_64-linux-gnu/lib/
for lib in libgmp libmpfr libmpc; do
  case "$lib" in libgmp) soname=10.4.1; major=10;; libmpfr) soname=6.2.0; major=6;; libmpc) soname=3.3.1; major=3;; esac
  ln -sf "$lib.so.$soname" "/usr/x86_64-linux-gnu/lib/$lib.so.$major"
  ln -sf "$lib.so.$soname" "/usr/x86_64-linux-gnu/lib/$lib.so"
done
export QEMU_LD_PREFIX=/usr/x86_64-linux-gnu
cc="/usr/bin/clang --target=x86_64-linux-gnu --gcc-toolchain=/usr -fuse-ld=lld -Wno-unused-command-line-argument -isystem /usr/include/x86_64-linux-gnu"
cxx="/usr/bin/clang++ --target=x86_64-linux-gnu --gcc-toolchain=/usr -fuse-ld=lld -Wno-unused-command-line-argument -isystem /usr/include/x86_64-linux-gnu"
if [[ ! -f Makefile ]]; then
  CC="$cc" CXX="$cxx" LDFLAGS="-L/usr/x86_64-linux-gnu/lib" /src/configure --build=x86_64-linux-gnu \
    --host=x86_64-linux-gnu --target="$tuple" --prefix=/opt/gcc16 \
    --enable-languages=c,c++ --disable-bootstrap --disable-multilib --disable-nls \
    --disable-werror --disable-shared --disable-threads --disable-libatomic \
    --disable-libgomp --disable-libitm --disable-libquadmath --disable-libsanitizer \
    --disable-libssp --disable-libstdcxx --without-headers --with-newlib
fi
make -j"$2" CC="$cc" CXX="$cxx" CXXFLAGS="-g -O1 -fno-strict-aliasing" \
  LDFLAGS="-static-libstdc++ -static-libgcc -L/usr/x86_64-linux-gnu/lib" all-gcc
# libgcc is produced by the new GCC; its target assembler lives in the amd64 runtime.
' bash "$tuple" "$jobs"
