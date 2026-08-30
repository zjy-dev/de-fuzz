#!/usr/bin/env bash
set -euo pipefail

(( $# == 1 )) || { echo "usage: $0 IMAGE" >&2; exit 64; }
image=$1
id=$(docker image inspect "$image" --format '{{.Id}}')
case "$id" in
  sha256:*) ;;
  *) echo "unexpected Docker image ID: $id" >&2; exit 1 ;;
esac
repository=${image%%:*}
repository=${repository%%@*}
printf '%s@%s\n' "$repository" "$id"
