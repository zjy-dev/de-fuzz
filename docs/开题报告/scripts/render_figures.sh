#!/usr/bin/env bash
# 渲染开题报告全部配图：assets/ch*/*.{zh,en}.d2 → 同目录下 *.svg + *.png
# 用法：bash scripts/render_figures.sh [d2-binary]
#       默认使用 PATH 中的 d2，找不到则尝试 ~/go/bin/d2
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS_DIR="$REPO_ROOT/assets"

D2_BIN="${1:-}"
if [[ -z "$D2_BIN" ]]; then
  if command -v d2 >/dev/null 2>&1; then
    D2_BIN="$(command -v d2)"
  elif [[ -x "$HOME/go/bin/d2" ]]; then
    D2_BIN="$HOME/go/bin/d2"
  else
    echo "error: cannot find d2 binary; install with 'go install oss.terrastruct.com/d2@latest'" >&2
    exit 1
  fi
fi

if ! command -v rsvg-convert >/dev/null 2>&1; then
  echo "error: rsvg-convert not found (needed for SVG → PNG); install librsvg" >&2
  exit 1
fi

# 每张图的渲染参数：stem layout pad-px width-px
# stem 不含 .zh / .en 后缀；脚本对每个 stem 同时渲染中英文两版。
FIGURES=(
  "ch1-background/stack-layout    dagre  24  1200"
  "ch1-background/defense-matrix  dagre  24  1600"
  "ch3-objective/pipeline         dagre  24  2000"
  "ch4-route/architecture         elk    24  1800"
  "ch4-route/oracle-flow          dagre  24  1400"
  "ch4-route/main-loop            dagre  24  1600"
  "ch5-experiment/fortify-path    dagre  24  1400"
)

cd "$ASSETS_DIR"

for spec in "${FIGURES[@]}"; do
  read -r path layout pad width <<<"$spec"
  for lang in zh en; do
    src="$ASSETS_DIR/${path}.${lang}.d2"
    svg="$ASSETS_DIR/${path}.${lang}.svg"
    png="$ASSETS_DIR/${path}.${lang}.png"

    if [[ ! -f "$src" ]]; then
      echo "skip: $src not found"
      continue
    fi

    echo "==> rendering $path.$lang ($layout, pad=$pad, w=$width)"
    "$D2_BIN" --layout="$layout" --pad="$pad" "$src" "$svg"
    rsvg-convert -w "$width" -o "$png" "$svg"
    echo "    -> $svg"
    echo "    -> $png"
  done
done

echo
echo "all figures rendered to $ASSETS_DIR/ch*/*.{zh,en}.{svg,png}"
