#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 --image IMAGE [--platform PLATFORM] [--timeout SECONDS] [--mount HOST:CONTAINER[:MODE]]... --emulator PATH -- <qemu args> [--binary] <target binary> [program args]" >&2
  exit 64
}

image=
platform=
emulator=
timeout_seconds=${DEFUZZ_QEMU_TIMEOUT_SECONDS:-25}
mounts=()
while (( $# > 0 )); do
  case "$1" in
    --image) (( $# >= 2 )) || usage; image=$2; shift 2 ;;
    --platform) (( $# >= 2 )) || usage; platform=$2; shift 2 ;;
    --mount) (( $# >= 2 )) || usage; mounts+=("$2"); shift 2 ;;
    --timeout) (( $# >= 2 )) || usage; timeout_seconds=$2; shift 2 ;;
    --emulator) (( $# >= 2 )) || usage; emulator=$2; shift 2 ;;
    --) shift; break ;;
    *) usage ;;
  esac
done
[[ -n "$image" && -n "$emulator" && $# -gt 0 ]] || usage
[[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]] || { echo "QEMU timeout must be a positive integer" >&2; exit 64; }
command -v docker >/dev/null 2>&1 || { echo "docker is unavailable" >&2; exit 127; }

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$repo_root/.work"
stage_dir=$(mktemp -d "$repo_root/.work/docker-qemu.XXXXXX")
trap 'rm -rf -- "$stage_dir"' EXIT

qemu_argv=("$@")
binary_index=-1
for i in "${!qemu_argv[@]}"; do
  if [[ "${qemu_argv[$i]}" == --binary ]]; then
    binary_index=$i
    unset 'qemu_argv[i]'
    break
  fi
done
if (( binary_index < 0 )); then
  for i in "${!qemu_argv[@]}"; do
    if [[ "${qemu_argv[$i]}" == /* && -f "${qemu_argv[$i]}" ]]; then
      binary_index=$i
      break
    fi
  done
fi
qemu_argv=("${qemu_argv[@]}")
(( binary_index >= 0 )) || { echo "QEMU invocation contains no readable target binary" >&2; exit 66; }
[[ "${qemu_argv[$binary_index]}" == /* && -f "${qemu_argv[$binary_index]}" ]] || {
  echo "QEMU target binary is not a readable absolute file: ${qemu_argv[$binary_index]}" >&2
  exit 66
}
cp "${qemu_argv[$binary_index]}" "$stage_dir/program"
chmod +x "$stage_dir/program"
qemu_argv[$binary_index]=/work/program

# Keep stdin attached because dynamic checkers exercise both ExecuteWithInput
# and ExecuteWithArgs through this wrapper.
docker_argv=(run --rm -i -v "$stage_dir:/work:ro" -w /work)
[[ -z "$platform" ]] || docker_argv+=(--platform "$platform")
for ((i = 0; i < ${#mounts[@]}; i++)); do
  mount_value=${mounts[$i]}
  if [[ "$mount_value" == *:* ]]; then
    docker_argv+=(-v "$mount_value")
  else
    [[ -e "$mount_value" ]] || { echo "container mount does not exist: $mount_value" >&2; exit 66; }
    docker_argv+=(-v "$mount_value:$mount_value:ro")
  fi
done
docker_argv+=("$image" /usr/bin/timeout --signal=KILL "${timeout_seconds}s" "$emulator")
docker "${docker_argv[@]}" "${qemu_argv[@]}"
