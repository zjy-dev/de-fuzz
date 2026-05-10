# Nano Banana 提示词：FORTIFY_SOURCE 优化路径示意（一、研究背景 motivation 用图）

> 用途：插入「一、研究背景」中关于 _FORTIFY_SOURCE 静默失效那段。
> 关键诉求：用一个**橘色框**框住 `strcat → __strcat_chk → __stpcpy_chk` 这条路径，把"为安全"和"为性能"两个意图与 size 参数的演化对应起来。
> 输出：白底、扁平矢量风格、横向左→右流动；标签全英文；最终建议宽度 1600px。

## 中文版（提示词主体）

```
你是为顶会论文绘制示意图的资深学术插画师，请绘制一张干净的扁平矢量示意图，
以白色背景呈现 GCC 在开启 _FORTIFY_SOURCE 时对 strcat 系列调用的优化路径。

整体布局：从左到右单向流动，三个矩形节点 + 两段箭头，下方加一条说明 size 参数的演化轨迹。

节点 1（最左）：
  - 标题 "User code: strcat(dst, src)"
  - 底部小字 "no bound info"

节点 2（中间）：
  - 标题 "__strcat_chk(dst, src, objsize)"
  - 底部小字 "for safety: bound = objsize"
  - 整个节点放在一个明显的橘色（#F2994A）粗描边圆角矩形里

节点 3（最右）：
  - 标题 "__stpcpy_chk(dst + strlen(dst), src, objsize)"
  - 底部小字 "for performance: dst advanced; bound NOT updated"
  - 节点 3 同样落在那同一个橘色框内（即橘色框横跨节点 2 与节点 3）

橘色框上方加一行小字："FORTIFY chain: safety wrapper → performance rewrite"

箭头：
  - 节点 1 → 节点 2：标签 "preprocessor"
  - 节点 2 → 节点 3：标签 "tree-ssa-strlen rewrite"

底部 size 演化条（在主流程下方）：
  - 三个用细线连起来的小标签
    "size = ?"  →  "size = objsize"  →  "size = objsize  (should be objsize − strlen(dst))"
  - 第三个标签用红色（#EB5757）下划线，并在右侧加一个小红色感叹号图标 + "silent gap"

色彩与风格要求：
  - 浅色系：节点用 #F4F8FB / #FBE5D6 / #FBE5D6
  - 字体清晰、英文 sans-serif，无衬线
  - 整体类似 DeepMind / OpenAI 论文常见 figure 的极简风
  - 禁止：照片质感、3D 阴影、卡通风格、复杂背景纹理、不可读文字
```

## 英文版（推荐直接喂给 nano banana）

```
You are an expert scientific illustrator for top-tier security conferences.
Draw a clean flat vector figure on a pure white background showing GCC's
_FORTIFY_SOURCE optimization path for strcat-family calls.

Layout: horizontal left-to-right flow with three rectangular nodes connected
by two arrows. Add a small "size argument timeline" below the main flow.

Node 1 (leftmost):
  Title  "User code: strcat(dst, src)"
  Small caption  "no bound info"

Node 2 (middle):
  Title  "__strcat_chk(dst, src, objsize)"
  Small caption  "for safety: bound = objsize"
  Wrap node 2 inside a thick rounded rectangle outlined in orange (#F2994A).

Node 3 (rightmost):
  Title  "__stpcpy_chk(dst + strlen(dst), src, objsize)"
  Small caption  "for performance: dst advanced; bound NOT updated"
  The SAME orange outlined rectangle must extend to also enclose node 3,
  i.e. one orange rectangle spans both node 2 and node 3.
  Above the orange rectangle, place a small label
    "FORTIFY chain: safety wrapper -> performance rewrite".

Arrows:
  Node 1 -> Node 2 with label "preprocessor".
  Node 2 -> Node 3 with label "tree-ssa-strlen rewrite".

Size timeline (below the main flow, three small linked tags):
  "size = ?"   ->   "size = objsize"   ->   "size = objsize  (should be objsize - strlen(dst))"
  Underline the third tag in red (#EB5757) and place a small red exclamation icon
  with the label "silent gap" to its right.

Style:
  Pastel palette, node fills around #F4F8FB or #FBE5D6, sans-serif text,
  flat vector illustration similar to DeepMind / OpenAI paper figures.
  No photorealism, no 3D shadows, no cartoon look, no clutter, no unreadable text.
  Recommended canvas: 1600 x 800 px.
```

## 备注

- 本图建议命名为 `assets/fortify-path.png`，docx 注入时插入到「一、研究背景」末尾。
- 也可以用任何符合上述描述的工具人工绘制；橘色框与"size 参数演化"两条线索是图义不可或缺的部分。
