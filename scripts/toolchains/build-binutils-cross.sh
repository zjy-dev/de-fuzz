#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 <riscv64|loongarch64>" >&2
  exit 64
}

(( $# == 1 )) || usage
isa=$1
case "$isa" in
  riscv64) tuple=riscv64-linux-gnu ;;
  loongarch64) tuple=loongarch64-linux-gnu ;;
  *) usage ;;
esac

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
env_root=${DEFUZZ_GCC17_CROSS_ROOT:-"$repo_root/.work/gcc17-cross-env"}
image=${DEFUZZ_GCC17_CROSS_IMAGE:-local/gcc17-cross-base:debian12}
platform=${DEFUZZ_GCC17_CROSS_PLATFORM:-linux/amd64}
version=2.44
expected_sha256=ce2017e059d63e67ddb9240e9d4ec49c2893605035cd60e92ad53177f4377237
archive="$env_root/sources/binutils-$version.tar.xz"
source_root="$env_root/sources/binutils-$version"
build_root="$env_root/build-binutils-$isa"
install_root="$env_root/install-binutils-$isa"
mkdir -p "$env_root/sources" "$build_root" "$install_root"

if [[ ! -f "$archive" ]]; then
  curl -fL --retry 3 --retry-delay 2 \
    -o "$archive" "https://ftp.gnu.org/gnu/binutils/binutils-$version.tar.xz"
fi
actual_sha256=$(shasum -a 256 "$archive" | awk '{print $1}')
[[ "$actual_sha256" == "$expected_sha256" ]] || {
  echo "binutils archive SHA-256 mismatch: $actual_sha256" >&2
  exit 65
}
if [[ ! -x "$source_root/configure" ]]; then
  mkdir -p "$source_root"
  tar -xf "$archive" -C "$source_root" --strip-components=1
fi

docker run --rm -i --platform "$platform" \
  -v "$source_root:/src:ro" \
  -v "$build_root:/build" \
  -v "$install_root:/install" \
  -w /build "$image" bash -s -- "$tuple" <<'CONTAINER'
set -euo pipefail
tuple=$1
if [[ ! -f Makefile ]]; then
  /src/configure --target="$tuple" --prefix=/install \
    --disable-nls --disable-werror --disable-gdb --disable-sim
fi
make -j"$(nproc)" all-binutils all-gas all-ld
make install-binutils install-gas install-ld
/install/bin/"$tuple"-as --version | head -n 1
/install/bin/"$tuple"-ld --version | head -n 1
CONTAINER
