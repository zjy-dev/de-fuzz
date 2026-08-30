#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 <aarch64|riscv64|loongarch64>" >&2
  exit 64
}

(( $# == 1 )) || usage
isa=$1
case "$isa" in
  aarch64) tuple=aarch64-linux-gnu; arch_args=() ;;
  riscv64) tuple=riscv64-linux-gnu; arch_args=(--with-arch=rv64gc --with-abi=lp64d) ;;
  loongarch64) tuple=loongarch64-linux-gnu; arch_args=() ;;
  *) usage ;;
esac

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
env_root=${DEFUZZ_GCC17_CROSS_ROOT:-"$repo_root/.work/gcc17-cross-env"}
canonical_source=${DEFUZZ_GCC17_SOURCE:-"$repo_root/.work/gcc17-env/src"}
source_root="$env_root/src-$isa"
image=${DEFUZZ_GCC17_CROSS_IMAGE:-local/gcc17-cross-base:debian12}
platform=${DEFUZZ_GCC17_CROSS_PLATFORM:-linux/amd64}
build_root="$env_root/build-$isa"
install_root="$env_root/install-$isa"
binutils_root=${DEFUZZ_CROSS_BINUTILS_ROOT:-"$env_root/install-binutils-$isa"}
loongarch_toolchain_root=${DEFUZZ_LOONGARCH_TOOLCHAIN_ROOT:-"$env_root/loongarch-community-root"}
if [[ ! -d "$source_root/.git" && ! -f "$source_root/.git" ]]; then
  git -C "$canonical_source" worktree add --detach "$source_root" HEAD
fi
expected_commit=$(git -C "$canonical_source" rev-parse HEAD)
actual_commit=$(git -C "$source_root" rev-parse HEAD)
[[ "$actual_commit" == "$expected_commit" ]] || {
  echo "source worktree revision mismatch: expected $expected_commit, got $actual_commit" >&2
  exit 65
}
mkdir -p "$build_root" "$install_root"

docker_args=(run --rm -i --platform "$platform" \
  -v "$source_root:/src" \
  -v "$build_root:/build" \
  -v "$install_root:/install" \
  -w /build)
has_external_binutils=false
if [[ -x "$binutils_root/bin/$tuple-as" && -x "$binutils_root/bin/$tuple-ld" ]]; then
  docker_args+=(-v "$binutils_root:/cross-binutils:ro")
  has_external_binutils=true
fi
has_loongarch_sysroot=false
if [[ "$isa" == loongarch64 ]]; then
  [[ -f "$loongarch_toolchain_root/usr/loongarch64-linux-gnu/include/stdio.h" ]] || {
    echo "LoongArch sysroot is unavailable: $loongarch_toolchain_root" >&2
    exit 66
  }
  [[ "$has_external_binutils" == true ]] || {
    echo "LoongArch GNU binutils are unavailable: $binutils_root" >&2
    exit 66
  }
  docker_args+=(-v "$loongarch_toolchain_root:/toolchain:ro")
  has_loongarch_sysroot=true
fi

docker "${docker_args[@]}" "$image" \
  bash -s -- "$tuple" "$has_external_binutils" "$has_loongarch_sysroot" "${arch_args[@]}" <<'CONTAINER'
set -euo pipefail
tuple=$1
has_external_binutils=$2
has_loongarch_sysroot=$3
shift 3
if [[ "$has_external_binutils" == true ]]; then
  export PATH=/cross-binutils/bin:$PATH
fi
sysroot_args=(--without-headers --with-newlib)
if [[ "$has_loongarch_sysroot" == true ]]; then
  sysroot_args=(
    --with-sysroot=/toolchain/usr/loongarch64-linux-gnu
    --with-native-system-header-dir=/include
  )
fi
if [[ ! -f Makefile ]]; then
  /src/configure \
    --build=x86_64-linux-gnu \
    --host=x86_64-linux-gnu \
    --target="$tuple" \
    --prefix=/install \
    --enable-languages=c \
    --disable-bootstrap \
    --disable-multilib \
    --disable-nls \
    --disable-werror \
    --disable-shared \
    --disable-threads \
    --disable-libatomic \
    --disable-libgomp \
    --disable-libitm \
    --disable-libquadmath \
    --disable-libsanitizer \
    --disable-libssp \
    --disable-libstdcxx \
    "${sysroot_args[@]}" \
    "$@"
fi
make -j"$(nproc)" all-gcc all-target-libgcc
make install-gcc install-target-libgcc
version=$(/install/bin/"$tuple"-gcc -dumpfullversion)
runtime=/install/lib/gcc/"$tuple"/"$version"
if [[ -f "$runtime/libgcc.a" && ! -e "$runtime/libgcc_eh.a" ]]; then
  ln -s libgcc.a "$runtime/libgcc_eh.a"
fi
/install/bin/"$tuple"-gcc --version | head -n 1
CONTAINER
