# DeFuzz 开题报告

## 文件说明

- `开题报告24例.doc`：东南大学专硕开题报告参考模板（原始来源，仅作存档；构建已不再使用）。
- `template.docx`：构建实际使用的模板，第一页（封面）已经填好学号、姓名、学院、导师等个人信息。**只读，构建过程不会改写**；如需修改封面，请直接手工编辑这个文件。
- `chapters/`：分章节的中文正文 txt；除章节正文外还嵌入 `[[FIG:stem]]` 占位行，指明配图在文内的位置（由 `inject.py` 展开为图+题注）。
- `assets/`：按章节子目录组织，每张图同时提供中英文两份 D2 源码与渲染产物：
  - `ch1-background/` — 图 1 `stack-layout`、图 2 `defense-matrix`
  - `ch3-objective/`  — 图 3 `pipeline`
  - `ch4-route/`      — 图 4 `architecture`、图 5 `oracle-flow`、图 6 `main-loop`
  - `ch5-experiment/` — 图 7 `fortify-path`
  - `_archive/`       — 老版 mermaid `*.mmd` / nano-banana `*.prompt.md` / 已弃用的 `silent-failure.*` 归档。
- `scripts/render_figures.sh`：一键渲染全部配图为 SVG + PNG（中英文各一版）。依赖 `d2`（`go install oss.terrastruct.com/d2@latest`）与 `rsvg-convert`（系统包 `librsvg`）。
- `DeFuzz-开题报告.docx`：最终交付的 Word 稿（由 `scripts/inject.py` 把 `chapters/*.txt` 注入 `template.docx` 生成）。
- `scripts/inject.py`：把 chapters 注入到模板 docx 的脚本；任何修改 chapters 或图后都需重新跑一次。脚本按 `[[FIG:stem]]` 占位行把图插到正文对应位置，并维护一张统一的 stem → (PNG 路径 / rId / 题注 / docPr id) 注册表，不会触碰封面。

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

全文共 7 张配图，统一由 D2 绘制，每图同时提供中文（`*.zh.d2`）与英文（`*.en.d2`）两份源码及对应 SVG/PNG 产物。报告 docx 只引用中文版，英文版留作英文论文与答辩 PPT 复用。

| 编号 | 位置 | 目录 | stem | 主题 |
| --- | --- | --- | --- | --- |
| 图 1 | §1 第一段末 | `ch1-background/` | `stack-layout`   | CVE-2023-4039 buggy 栈帧布局 |
| 图 2 | §1 二维矩阵段末 | `ch1-background/` | `defense-matrix` | 防御机制 × ISA 二维研究空间 |
| 图 3 | §3.2 首段末 | `ch3-objective/`  | `pipeline`       | 研究内容工作链条（不变量 → 预言机 → 主循环 → bug） |
| 图 4 | §4.1 总体架构末 | `ch4-route/`      | `architecture`   | DeFuzz 总体架构 |
| 图 5 | §4.2 预言机框架末 | `ch4-route/`      | `oracle-flow`    | 不变量预言机判定流程 |
| 图 6 | §4.3 主循环末 | `ch4-route/`      | `main-loop`      | 大模型约束求解主循环 |
| 图 7 | §5.1 案例一末 | `ch5-experiment/` | `fortify-path`   | \_FORTIFY\_SOURCE 优化路径与 size 静默放宽 |

每个 stem 在 `chapters/*.txt` 中以 `[[FIG:stem]]` 占位行表示插图位置，正文段落用"如图 N 所示"显式指引读者；`inject.py` 按 `FIGURES` 注册表把占位行展开为图+题注。

一键渲染全部图：

```bash
bash docs/开题报告/scripts/render_figures.sh
```

脚本按 `assets/ch*/*.{zh,en}.d2` 遍历，逐张调用 `d2 --layout=<dagre|elk> ...` 生成 SVG，再用 `rsvg-convert -w <宽>` 转 PNG，输出写回同一子目录，不会触及 docx。

老版图源（`*.mmd`、`*.prompt.md`）与已弃用的 `silent-failure.*` 保留在 `assets/_archive/` 下作为历史档。

## 封面信息

封面表格中的学号 / 研究生姓名 / 学院 / 学位类别 / 专业领域 / 校内导师 / 校外导师 / 论文题目 / 开题日期等字段已在 `template.docx` 内手工填好，构建时原样保留。如以后需要更新这些信息，请直接用 Word 打开 `template.docx` 修改并保存。
