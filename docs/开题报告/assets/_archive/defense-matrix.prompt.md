# Nano Banana 提示词：防御机制 × ISA 二维研究矩阵（一、研究背景 motivation 用图）

> 用途：作为「一、研究背景」中"二维研究矩阵"段落的配图，用于说服评委——少量已被发现的 silent failure 仅占据矩阵中很小一部分格点，其他大部分空间仍属未探索区域。
> 关键诉求：把"软件防御机制"列为行、"目标 ISA"列为列，用色块标注三类格点（已修复 CVE / 我们已上报 / 未探索）。
> 输出：白底、扁平矢量风格、横向网格表；最终建议宽度 1600px。

## 英文版（推荐直接喂给 nano banana）

```
You are an expert scientific illustrator for systems-security papers.
Draw a clean flat vector matrix figure on a pure white background that
visualizes the two-dimensional research space of compiler-emitted defenses
(rows) versus target instruction set architectures (columns).

Layout: a tabular grid, rows labelled on the left, columns on the top.

Rows (top to bottom):
  Stack Canary
  _FORTIFY_SOURCE
  IBT / SHSTK
  BTI / PAC
  Shadow Stack
  SafeStack
  CFI / KCFI
  HCFR

Columns (left to right):
  x86-64
  AArch64
  RISC-V
  LoongArch
  MIPS
  PowerPC

Cell color semantics (legend at the bottom):
  light grey   = unexplored grid point
  pale orange  = known historical CVE / patched (e.g. CVE-2023-4039 at
                 row "Stack Canary" x column "AArch64")
  light green  = "discovered & confirmed by this work" (mark a few cells:
                 _FORTIFY_SOURCE x x86-64; IBT / SHSTK x x86-64;
                 Stack Canary x MIPS; Stack Canary x LoongArch)
  most cells should remain light grey to communicate "vast unexplored space"

Text: small sans-serif labels for rows and columns; no long sentences.
Annotation arrow on the upper-right pointing into the grey region with the
caption "Likely undiscovered silent failures".

Style: pastel palette, sans-serif labels, similar to DeepMind / OpenAI
academic figures. No 3D shadows, no photoreal style, no clutter.
Recommended canvas: 1600 x 900 px.
```

## 备注

- 输出建议命名为 `assets/defense-matrix.png`，docx 注入时插入到「一、研究背景」末尾。
- 该图与正文中"二维研究矩阵"段落直接呼应，是说服评委"潜在 bug 空间巨大"的关键图。
