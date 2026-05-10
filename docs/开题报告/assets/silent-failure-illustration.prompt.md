# Nano Banana 提示词：编译器防御机制 silent failure 概念图（可选，研究背景开篇用）

> 用途：可作为「一、研究背景」开篇的概念图，与文字"编译器是防御机制的承载体"呼应。时间紧时可省略。
> 关键诉求：分两层呈现"用户代码 → 编译器 → 防御机制 → 二进制"的可信链；其中防御机制层有一处灰底"看似存在却失效"的空槽。

## 英文版（推荐）

```
You are an expert scientific illustrator for systems-security papers.
Draw a flat vector concept figure on a white background that depicts the
implicit trust chain modern software places on the compiler-emitted defenses.

Layout: vertical four-layer stack, top-to-bottom.

Layer 1 (top): "User Source Code"
  light blue (#E8F1FB) rectangle, left-aligned text

Layer 2: "Compiler" with three sub-blocks side-by-side: "Frontend",
  "Middle-end Passes", "Backend Templates"
  fill #F4F8FB

Layer 3: "Compiled Defenses" with four small icon-cards aligned horizontally:
  "stack canary", "_FORTIFY_SOURCE", "IBT / endbr", "BTI / shadow stack".
  Three cards have a small green check mark; ONE card (use _FORTIFY_SOURCE)
  is greyed out with a faint dashed outline and a small red caption
  "silently weakened".

Layer 4: "Running Binary" - a thin grey bar at the bottom.

Connect layers with thin downward arrows. On the right side, add a vertical
text strip "Implicit trust chain" running from top to bottom in muted grey.

Style: pastel palette, sans-serif labels, similar to DeepMind / OpenAI
academic figures. No 3D shadows, no photoreal style, no clutter.
Recommended canvas: 1400 x 1000 px.
```

## 备注

- 输出建议命名为 `assets/silent-failure.png`，作为研究背景章节的辅图。
- 该图为可选项，主图仍以 `fortify-path.png` 为优先。
