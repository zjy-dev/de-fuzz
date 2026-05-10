#!/usr/bin/env bash
# 渲染开题报告全部 6 张配图：assets/*.d2 → assets/*.svg + assets/*.png
# 用法：bash scripts/render_figures.sh [d2-binary]
#       默认使用 PATH 中的 d2，找不到则尝试 ~/go/bin/d2
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS_DIR="$REPO_ROOT/assets"

# 选用的 d2 二进制
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

# 每张图的渲染参数：name layout pad-px width-px
FIGURES=(
  "silent-failure  dagre  24  1400"
  "defense-matrix  dagre  24  1600"
  "fortify-path    dagre  24  1800"
  "architecture    elk    24  1800"
  "oracle-flow     dagre  24  1400"
  "main-loop       dagre  24  1600"
)

cd "$ASSETS_DIR"

for spec in "${FIGURES[@]}"; do
  read -r name layout pad width <<<"$spec"
  src="$ASSETS_DIR/${name}.d2"
  svg="$ASSETS_DIR/${name}.svg"
  png="$ASSETS_DIR/${name}.png"

  if [[ ! -f "$src" ]]; then
    echo "skip: $src not found"
    continue
  fi

  echo "==> rendering $name ($layout, pad=$pad, w=$width)"
  "$D2_BIN" --layout="$layout" --pad="$pad" "$src" "$svg"
  rsvg-convert -w "$width" -o "$png" "$svg"
  echo "    -> $svg"
  echo "    -> $png"
done

echo
echo "all figures rendered to $ASSETS_DIR/*.svg and *.png"
