# Presentations

本目录存放一次性汇报、答辩和 PPT 生成资产，不参与 DeFuzz 的核心方法论文档体系。

约定：

- `开题答辩.md`：答辩内容提纲与页面要求。
- `项目介绍.md`：项目介绍大纲与讲稿，15 页，约 12:54。开头给“四个章节、一条主线”的结构表，再给逐页大纲与讲者备注。由构建脚本自动生成，不要手改。
- `PPTX-SKILL-GUIDE.md`：PPT 生成工作流说明。
- `assets/`：答辩专用图片资源。
- `pptx-workspace/`：PPT 生成脚本、D2 图示源文件和导出产物。

清理原则：

- 生成依赖目录如 `node_modules/`、`.venv/` 不提交到仓库。
- 实验方法、oracle、架构设计仍放在 `docs/`；仅汇报材料放在此目录。

小组项目介绍的产物为 `pptx-workspace/项目介绍-DeFuzz.pptx`，附同名 PDF。
文案源为 `project_intro_content.json`，PPT 源码为 `diagram-deck/build.js`，
D2 源文件在 `diagram-deck/diagrams/`。配色与版式沿用开题。
`create_project_intro.py` 保留为兼容构建入口；完整命令见 `项目介绍.md`。
全篇只讲四个章节：**背景、挑战、技术路线、结果**；相关工作不单独设页，其核心判断（崩溃与差分判不出静默失效）压进 P3 的动机备注。
技术路线按 `defuzz-paper/main.tex @ ba9e258` 组织，挑战与方案设计逐条对应论文章节，
不以本仓库代码为第一优先级。三项挑战固定叫 **C1 不变量生成 / C2 基于不变量做测试 / C3 ISA 差异**，必须在 P3 的动机案例里由“三件难事”引出、P4 逐条展开、P6 与“测什么 / 怎么判 / 怎么找”显式对齐（测什么解 C1、怎么判解 C3、怎么找解 C2），改动这几页时不要打断这条链路。
分工原则：**结果类页面直接沿用开题答辩** —— P12 已有成果沿用开题 P15 / P17（三例上报缺陷 + severe 邮件），P15 工作进度沿用开题 P19 的 S1–S6 阶段划分、仅更新当前位置；只有挑战与技术路线（P4、P6–P11）按论文重做。
上片文字一律只留核心结论，推理与数据放讲者备注：卡片正文 ≤ 50 字、表格单格 ≤ 25 字、结论带 ≤ 45 字。方案只有一套，用“测什么 / 怎么判 / 怎么找”三个问题串着讲，标题保持大白话，
不得写成“方案一 / 二 / 三”“环节一 / 二 / 三”或“知识层 / 判定层 / 搜索层”。
**用词必须是大白话（含 D2 图示）**：不写公式化记号（`C(s, i)`、`O : P × I × S`），不写“证据接地 / 元数据表驱动 / 蒸馏 / 类比 / 特化 / 蕴含 / 消融 / 上下文隔离”这类术语，英文字段名（statement / observation / mechanism…）一律换成中文说法（规则本身 / 怎么看出来 / 防御机制…），`Embedding + BM25` 写成“语义检索 + 关键词检索”，`Oracle / checker` 写成“检查器”，`ISA` 在正文里写“架构”。举例只用 **CVE-2023-4039** 一个案例，不再引其他 PR 作为讲解示例（P12 的三例上报缺陷属于成果，不受此限）。
假阴性评估引用
`defuzz-paper/DeFuzz_injection_delivery_20260916`。
当前进度：植入已完成，尚未开展植入相关实验。

构建与导出：

```sh
cd docs/presentations/pptx-workspace/diagram-deck
uv run --with fonttools --with Pillow python render_diagrams.py   # 仅在改动 D2 后需要
npm run build
cd build && osascript ../../export_project_intro.applescript \
  "$PWD/项目介绍-DeFuzz.pptx" "$PWD/项目介绍-DeFuzz.pdf"
```

macOS 也可直接在 `pptx-workspace` 用本机 PowerPoint 导出 PDF：

```sh
cd docs/presentations/pptx-workspace
osascript export_project_intro.applescript "$PWD/项目介绍-DeFuzz.pptx" "$PWD/项目介绍-DeFuzz.pdf"
```
