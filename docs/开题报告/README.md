# DeFuzz 开题报告

## 文件说明

- `开题报告24例.doc`：东南大学专硕开题报告参考模板（原始来源，仅作存档；构建已不再使用）。
- `template.docx`：构建实际使用的模板，第一页（封面）已经填好学号、姓名、学院、导师等个人信息。**只读，构建过程不会改写**；如需修改封面，请直接手工编辑这个文件。
- `chapters/`：分章节的中文正文 txt（中转中 + 去 AI 味后定稿），是正文的真实内容源；当前包含 01–05 五个章节正文，外加 06 参考文献与 07 进度计划。
- `assets/`：全部 6 张配图的 D2 源码（`*.d2`）+ 渲染产物（`*.svg` / `*.png`）。老版的 mermaid `*.mmd` 与 nano-banana `*.prompt.md` 作为历史档保留。
- `scripts/render_figures.sh`：一键渲染 6 张配图为 SVG + PNG。依赖 `d2`（`go install oss.terrastruct.com/d2@latest`）与 `rsvg-convert`（系统包 `librsvg`）。
- `DeFuzz-开题报告.docx`：最终交付的 Word 稿（由 `scripts/inject.py` 把 `chapters/*.txt` 注入 `template.docx` 生成）。
- `scripts/inject.py`：把 chapters 注入到模板 docx 的脚本；任何修改 chapters 后都需重新跑一次。脚本只重写正文表格、（一）理论分析单元格和进度表数据行，不会触碰封面。

## 重新生成 docx

```bash
# 1. 拷贝模板（封面已固定）→ 输出文件
cp /home/yall/project/de-fuzz/docs/开题报告/template.docx \
   /home/yall/project/de-fuzz/docs/开题报告/DeFuzz-开题报告.docx

# 2. unpack → 注入 → pack
rm -rf /tmp/defuzz-kaiti/work
python /home/yall/project/de-fuzz/.windsurf/skills/docx/scripts/office/unpack.py \
    /home/yall/project/de-fuzz/docs/开题报告/DeFuzz-开题报告.docx \
    /tmp/defuzz-kaiti/work/
python3 /home/yall/project/de-fuzz/docs/开题报告/scripts/inject.py
python /home/yall/project/de-fuzz/.windsurf/skills/docx/scripts/office/pack.py \
    /tmp/defuzz-kaiti/work/ \
    /home/yall/project/de-fuzz/docs/开题报告/DeFuzz-开题报告.docx \
    --original /home/yall/project/de-fuzz/docs/开题报告/template.docx
```

## 配图

全文共 6 张配图（统一由 D2 重画，从老的 mermaid / nano-banana 迁移过来，以获得一致的扁平极简风格）：

| 编号 | 位置 | D2 源 | 产物 | 主题 |
| --- | --- | --- | --- | --- |
| 图 1 | 一·研究背景 末 | `assets/defense-matrix.d2` | `defense-matrix.{svg,png}` | 防御机制 × ISA 二维研究矩阵 |
| 图 2 | 四·技术路线 §1 末 | `assets/architecture.d2` | `architecture.{svg,png}` | DeFuzz 总体架构 |
| 图 3 | 四·技术路线 §2 末 | `assets/oracle-flow.d2` | `oracle-flow.{svg,png}` | 预言机三阶段调度 |
| 图 4 | 四·技术路线 §3 末 | `assets/main-loop.d2` | `main-loop.{svg,png}` | LLM 约束求解主循环 |
| 图 5 | 五·初步实验 §1 末 | `assets/fortify-path.d2` | `fortify-path.{svg,png}` | \_FORTIFY\_SOURCE 优化路径 + size silent gap |
| 图 6 | 一·研究背景 开篇 | `assets/silent-failure.d2` | `silent-failure.{svg,png}` | 隐式信任链中的 silent failure 概念示意 |

一键渲染。先装好 `d2`（`go install oss.terrastruct.com/d2@latest`）与 `librsvg`（提供 `rsvg-convert`），然后：

```bash
bash docs/开题报告/scripts/render_figures.sh
```

脚本逐张调用 `d2 --layout=<dagre|elk> ...` 生成 SVG，再用 `rsvg-convert -w <宽>` 转 PNG，输出只写入 `assets/`，不会触及 docx。渲染拿到 PNG 后，可在 Word 中手动定位到对应的“图 N 待补：…”占位行替换成图片，或在 docx XML 层用 docx skill 的 image 注入流程替换。

老版图源（`*.mmd`、`*.prompt.md`）作为历史档保留在 `assets/` 内，不再需要重新调用。

## 封面信息

封面表格中的学号 / 研究生姓名 / 学院 / 学位类别 / 专业领域 / 校内导师 / 校外导师 / 论文题目 / 开题日期等字段已在 `template.docx` 内手工填好，构建时原样保留。如以后需要更新这些信息，请直接用 Word 打开 `template.docx` 修改并保存。
