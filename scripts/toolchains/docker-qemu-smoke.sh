#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 TOOLCHAINS_YAML [gcc|llvm] [isa ...]" >&2
  exit 64
}

(( $# >= 1 )) || usage
config=$1
compiler_family=${2:-gcc}
if (( $# >= 2 )); then shift 2; else shift; fi
requested_isas=("$@")
[[ -f "$config" ]] || { echo "toolchains config not found: $config" >&2; exit 66; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is unavailable" >&2; exit 127; }

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
tmp_dir=$(mktemp -d "$repo_root/.work/qemu-matrix-smoke.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT

mapfile_cmd=(python3 - "$config" "$compiler_family")
for isa in "${requested_isas[@]}"; do mapfile_cmd+=("$isa"); done
while IFS=$'\t' read -r isa compiler_path driver_args_b64 qemu_path qemu_args_b64 qemu_sysroot_b64 native <&3; do
  [[ -n "$isa" ]] || continue
  source_path="$tmp_dir/$isa.c"
  binary_path="$tmp_dir/$isa.elf"
  case "$isa" in
    x86_64|i386) body='register long a __asm__("rax") = 60; register long d __asm__("rdi") = 0; __asm__ volatile ("syscall" : : "r"(a), "r"(d) : "memory");' ;;
    aarch64) body='register long a __asm__("x8") = 93; register long d __asm__("x0") = 0; __asm__ volatile ("svc 0" : : "r"(a), "r"(d) : "memory");' ;;
    riscv64) body='register long a __asm__("a7") = 93; register long d __asm__("a0") = 0; __asm__ volatile ("ecall" : : "r"(a), "r"(d) : "memory");' ;;
    loongarch64) body='register long a __asm__("$r11") = 93; register long d __asm__("$r4") = 0; __asm__ volatile ("syscall 0" : : "r"(a), "r"(d) : "memory");' ;;
    *) echo "unsupported smoke ISA: $isa" >&2; exit 64 ;;
  esac
  printf 'void _start(void) { %s __builtin_unreachable(); }\n' "$body" > "$source_path"
  driver_args=()
  while IFS= read -r arg; do driver_args+=("$arg"); done < <(
    printf '%s' "$driver_args_b64" | base64 --decode |
      python3 -c 'import json,sys; print("\n".join(json.load(sys.stdin)))'
  )
  "$compiler_path" "${driver_args[@]}" -nostdlib -static "$source_path" -o "$binary_path"
  if [[ "$native" == true ]]; then
    "$binary_path"
  else
    qemu_args=()
    while IFS= read -r arg; do qemu_args+=("$arg"); done < <(
      printf '%s' "$qemu_args_b64" | base64 --decode |
        python3 -c 'import json,sys; print("\n".join(json.load(sys.stdin)))'
    )
    qemu_cmd=("$qemu_path" "${qemu_args[@]}")
    qemu_sysroot=$(
      printf '%s' "$qemu_sysroot_b64" | base64 --decode |
        python3 -c 'import json,sys; print(json.load(sys.stdin))'
    )
    [[ -z "$qemu_sysroot" ]] || qemu_cmd+=(-L "$qemu_sysroot")
    qemu_cmd+=("$binary_path")
    "${qemu_cmd[@]}"
  fi
  echo "PASS $isa"
done 3< <("${mapfile_cmd[@]}" <<'PY'
import base64
import json
import pathlib
import sys

import yaml

path = pathlib.Path(sys.argv[1]).resolve()
family = sys.argv[2]
wanted = sys.argv[3:]
payload = yaml.safe_load(path.read_text())
toolchains = payload.get("toolchains", {})
isas = wanted or list(toolchains)
field = "gcc_path" if family == "gcc" else "clang_path"
for isa in isas:
    entry = toolchains.get(isa)
    if not isinstance(entry, dict):
        raise SystemExit(f"ISA {isa!r} is not configured")
    def encoded(name):
        return base64.b64encode(json.dumps(entry.get(name, [])).encode()).decode()
    values = [
        isa, str(entry.get(field, "")), encoded("driver_args"),
        str(entry.get("qemu_path", "")), encoded("qemu_args"),
        base64.b64encode(json.dumps(str(entry.get("qemu_sysroot", ""))).encode()).decode(),
        "true" if entry.get("native") else "false",
    ]
    if any("\t" in value or "\n" in value for value in values):
        raise SystemExit(f"ISA {isa!r} contains unsafe tab/newline characters")
    print("\t".join(values))
PY
)
