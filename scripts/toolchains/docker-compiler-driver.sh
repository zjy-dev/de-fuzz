#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 --image IMAGE [--platform PLATFORM] [--mount HOST:CONTAINER[:MODE]]... --compiler PATH -- <compiler args>" >&2
  exit 64
}

image=
platform=
compiler=
mounts=()
while (( $# > 0 )); do
  case "$1" in
    --image) (( $# >= 2 )) || usage; image=$2; shift 2 ;;
    --platform) (( $# >= 2 )) || usage; platform=$2; shift 2 ;;
    --mount) (( $# >= 2 )) || usage; mounts+=("$2"); shift 2 ;;
    --compiler) (( $# >= 2 )) || usage; compiler=$2; shift 2 ;;
    --) shift; break ;;
    *) usage ;;
  esac
done
[[ -n "$image" && -n "$compiler" && $# -gt 0 ]] || usage
command -v docker >/dev/null 2>&1 || { echo "docker is unavailable" >&2; exit 127; }

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$repo_root/.work"
stage_dir=$(mktemp -d "$repo_root/.work/docker-compiler.XXXXXX")
trap 'rm -rf -- "$stage_dir"' EXIT

compiler_argv=()
output_host=
expect_output=false
for arg in "$@"; do
  if $expect_output; then
    output_host=$arg
    compiler_argv+=(/work/output)
    expect_output=false
    continue
  fi
  if [[ "$arg" == -o ]]; then
    compiler_argv+=(-o)
    expect_output=true
    continue
  fi
  if [[ "$arg" == /* && -f "$arg" ]]; then
    staged_name=$(printf '%s' "$arg" | shasum -a 256 | cut -c1-16)-$(basename "$arg")
    cp "$arg" "$stage_dir/$staged_name"
    compiler_argv+=("/work/$staged_name")
  else
    compiler_argv+=("$arg")
  fi
done
[[ "$expect_output" == false && -n "$output_host" ]] || {
  echo "compiler invocation must contain '-o <absolute-output-path>'" >&2
  exit 64
}

docker_argv=(run --rm -v "$stage_dir:/work" -w /work)
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
docker_argv+=("$image" "$compiler")
docker "${docker_argv[@]}" "${compiler_argv[@]}"
[[ -f "$stage_dir/output" ]] || { echo "container compiler produced no output" >&2; exit 1; }
cp "$stage_dir/output" "$output_host"
