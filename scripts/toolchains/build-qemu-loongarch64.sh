#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
env_root=${DEFUZZ_GCC17_CROSS_ROOT:-"$repo_root/.work/gcc17-cross-env"}
image=${DEFUZZ_GCC17_CROSS_IMAGE:-local/gcc17-cross-base:debian12}
platform=${DEFUZZ_GCC17_CROSS_PLATFORM:-linux/amd64}
version=10.2.0
expected_sha256=9e30ad1b8b9f7b4463001582d1ab297f39cfccea5d08540c0ca6d6672785883a
archive="$env_root/sources/qemu-$version.tar.xz"
canonical_source="$env_root/sources/qemu-$version"
source_root="$env_root/src-qemu-$version"
build_root="$env_root/build-qemu-$version"
install_root="$env_root/install-qemu-$version"
mkdir -p "$env_root/sources" "$build_root" "$install_root"

if [[ ! -f "$archive" ]]; then
  curl -fL --retry 3 --retry-delay 2 \
    -o "$archive" "https://download.qemu.org/qemu-$version.tar.xz"
fi
actual_sha256=$(shasum -a 256 "$archive" | awk '{print $1}')
[[ "$actual_sha256" == "$expected_sha256" ]] || {
  echo "QEMU archive SHA-256 mismatch: $actual_sha256" >&2
  exit 65
}
if [[ ! -f "$canonical_source/meson.build" ]]; then
  mkdir -p "$canonical_source"
  tar -xf "$archive" -C "$canonical_source" --strip-components=1
fi
# Meson writes wrap locks below the source tree. Preserve the verified extract
# and build from a separate writable copy.
if [[ ! -f "$source_root/meson.build" ]]; then
  cp -R "$canonical_source" "$source_root"
fi

docker run --rm -i --platform "$platform" \
  -v "$source_root:/src" \
  -v "$build_root:/build" \
  -v "$install_root:/install" \
  -w /build "$image" bash <<'CONTAINER'
set -euo pipefail
apt-get update -qq
apt-get install -y --no-install-recommends \
  python3-venv ninja-build pkg-config libglib2.0-dev libpixman-1-dev
if [[ ! -f build.ninja ]]; then
  /src/configure --target-list=loongarch64-linux-user --prefix=/install \
    --disable-system --disable-tools --disable-docs --disable-guest-agent \
    --disable-plugins --disable-slirp --disable-gnutls --disable-nettle \
    --disable-capstone --disable-fdt
fi
make -j"$(nproc)"
make install
/install/bin/qemu-loongarch64 --version | head -n 1
CONTAINER
