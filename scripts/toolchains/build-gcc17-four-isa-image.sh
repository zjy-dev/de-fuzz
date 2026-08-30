#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
env_root=${DEFUZZ_GCC17_CROSS_ROOT:-"$repo_root/.work/gcc17-cross-env"}
base_image=${DEFUZZ_GCC17_CROSS_IMAGE:-local/gcc17-cross-base:debian12}
image=${DEFUZZ_GCC17_RUNTIME_IMAGE:-local/defuzz-gcc17-four-isa:20260531-qemu10.2}
platform=${DEFUZZ_GCC17_CROSS_PLATFORM:-linux/amd64}
expected_revision=f20bc4c2fe00928013c533e241b89ae3a6724ca1
dockerfile="$repo_root/scripts/toolchains/Dockerfile.gcc17-four-isa-runtime"
ignore_template="$repo_root/scripts/toolchains/gcc17-four-isa.dockerignore"
qemu_binary="$env_root/install-qemu-10.2.0/bin/qemu-loongarch64"

for path in \
  "$env_root/install-x86_64/opt/gcc17/bin/gcc" \
  "$env_root/install-aarch64/bin/aarch64-linux-gnu-gcc" \
  "$env_root/install-riscv64/bin/riscv64-linux-gnu-gcc" \
  "$env_root/install-loongarch64/bin/loongarch64-linux-gnu-gcc" \
  "$env_root/install-binutils-riscv64/bin/riscv64-linux-gnu-as" \
  "$env_root/install-binutils-loongarch64/bin/loongarch64-linux-gnu-as" \
  "$qemu_binary"; do
  [[ -x "$path" ]] || { echo "required runtime artifact is missing: $path" >&2; exit 66; }
done
actual_revision=$(git -C "$repo_root/.work/gcc17-env/src" rev-parse HEAD)
[[ "$actual_revision" == "$expected_revision" ]] || {
  echo "GCC source revision mismatch: $actual_revision" >&2
  exit 65
}
qemu_sha256=$(shasum -a 256 "$qemu_binary" | awk '{print $1}')
cp "$ignore_template" "$env_root/.dockerignore"
docker build --platform "$platform" \
  --build-arg "BASE_IMAGE=$base_image" \
  --build-arg "GCC_REVISION=$expected_revision" \
  --build-arg "QEMU_LOONGARCH64_SHA256=$qemu_sha256" \
  -f "$dockerfile" -t "$image" "$env_root"

"$repo_root/scripts/toolchains/docker-image-ref.sh" "$image"
