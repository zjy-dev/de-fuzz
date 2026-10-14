# DeFuzz 开题报告

## 文件说明

- `开题报告-张景耀-v1.1.0.docx`：当前第二轮修订版，以 v1.0.5 为底稿。题目改为“大语言模型辅助的编译器插桩防御机制脆弱性分析方法研究”，在 §1.1 对照英文论文的 compiler-instrumented defenses 界定研究对象；在 §3.2 新增“研究挑战—研究内容—研究目标”对应关系图及说明，原图 3–7 顺延为图 4–8。保留上一轮的假阴性评测段落、23 条参考文献与已填写的审议记录。
- `开题报告-张景耀-v1.0.5.docx`：第一轮修订底稿，将 §5.4 新增的定量评估段落改为基于受控植入的假阴性评测方案，删除正文与审议意见中的跨语言调用表述。评测设计依据论文仓库 `defuzz-paper/DeFuzz_injection_delivery_20260916` 的配套材料，使用有效植入项作为统计分母，段落以假阴性率定义结尾，未填写假阴性率实验结果。
- `开题报告-张景耀-v1.0.3.docx`：审议信息填写版，在 v1.0.2 基础上按同门截图补入审议组长、五名成员及修改完成备注，保留本课题的审议意见；截图中的票数、质量等级、日期和签名均为空白，继续留空。
- `开题报告-张景耀-v1.0.2.docx`：审议意见修订版，以 v1.0.1 为底稿保留原有排版；在已有小幅修改基础上，以 SoK 和 RustBelt 两篇论文替换背景部分的两条博客引用，并同步更新审议小组意见。
- `审议小组意见-草稿.txt`：参照审议记录截图拟写的意见，可直接复制。
- `开题报告24例.doc`：东南大学专硕开题报告参考模板（原始来源，仅作存档；构建已不再使用）。
- `template.docx`：构建实际使用的模板，第一页（封面）已经填好学号、姓名、学院、导师等个人信息。**只读，构建过程不会改写**；如需修改封面，请直接手工编辑这个文件。
- `chapters/`：分章节的中文正文 txt；除章节正文外还嵌入 `[[FIG:stem]]` 占位行，指明配图在文内的位置（由 `inject.py` 展开为图+题注）。
- `assets/`：按章节子目录组织；原有配图保留中英文两份 D2 源码，新整体图提供中文 SVG 源码与 PNG：
  - `ch1-background/` — 图 1 `stack-layout`、图 2 `defense-matrix`
  - `ch3-objective/`  — 图 3 `research-map`、图 4 `pipeline`（图 4 仍位于第四章）
  - `ch4-route/`      — 图 5 `architecture`、图 6 `oracle-flow`、图 7 `main-loop`
  - `ch5-experiment/` — 图 8 `fortify-path`
  - `_archive/`       — 老版 mermaid `*.mmd` / nano-banana `*.prompt.md` / 已弃用的 `silent-failure.*` 归档。
- `scripts/render_figures.sh`：一键渲染原有 D2 配图，并从定版 SVG 渲染新的研究对应关系图。依赖 `d2`（`go install oss.terrastruct.com/d2@latest`）与 `rsvg-convert`（系统包 `librsvg`）。
- `DeFuzz-开题报告.docx`：早期模板构建稿（由 `scripts/inject.py` 把 `chapters/*.txt` 注入 `template.docx` 生成）。
- `scripts/inject.py`：历史模板构建脚本；按 `[[FIG:stem]]` 占位行把图插到正文对应位置，维护 stem → (PNG 路径 / rId / 题注 / docPr id) 注册表，并同步论文题目。当前修订稿在上一版 Word 上定点更新，以保留人工排版和审议记录。

## 历史模板构建方法

以下命令保留原构建环境路径，生成早期模板稿；当前修订应以最新版本 Word 为底稿，避免覆盖已填写的审议记录。

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

全文共 8 张配图。原有 7 张图由 D2 绘制，保留中英文源码及 SVG/PNG 产物；新增图 3 采用手工对齐的 SVG，渲染为 3200 × 2250 PNG，以固定模块与连线位置。报告 docx 引用中文版。

第二轮已核对原图、缩小预览及 Word 导出的页面预览：新图文字与连线无重叠，图片和图题同页，标题完整显示；正文与分章节源文件一致，Word 结构校验通过。本机导出预览使用兼容宋体替代字形，分页仅作辅助检查，交付 Word 保留原有字体、样式及段落格式。

| 编号 | 位置 | 目录 | stem | 主题 |
| --- | --- | --- | --- | --- |
| 图 1 | §1 第一段末 | `ch1-background/` | `stack-layout`   | CVE-2023-4039 buggy 栈帧布局 |
| 图 2 | §1 二维矩阵段末 | `ch1-background/` | `defense-matrix` | 防御机制 × ISA 二维研究空间 |
| 图 3 | §3.2 引言末 | `ch3-objective/` | `research-map` | 研究挑战、研究内容与研究目标的对应关系 |
| 图 4 | §4 引言末 | `ch3-objective/`  | `pipeline`       | 技术路线工作链条（不变量 → 预言机 → 主循环 → bug） |
| 图 5 | §4.1 总体架构末 | `ch4-route/`      | `architecture`   | 系统总体架构 |
| 图 6 | §4.2 预言机框架末 | `ch4-route/`      | `oracle-flow`    | 不变量预言机判定流程 |
| 图 7 | §4.3 主循环末 | `ch4-route/`      | `main-loop`      | 大模型约束求解主循环 |
| 图 8 | §5.1 案例一末 | `ch5-experiment/` | `fortify-path`   | \_FORTIFY\_SOURCE 优化路径与 size 静默放宽 |

每个 stem 在 `chapters/*.txt` 中以 `[[FIG:stem]]` 占位行表示插图位置，正文段落用"如图 N 所示"显式指引读者；`inject.py` 按 `FIGURES` 注册表把占位行展开为图+题注。

一键渲染全部图：

```bash
bash docs/开题报告/scripts/render_figures.sh
```

脚本对原有配图调用 `d2 --layout=<dagre|elk> ...` 生成 SVG，再用 `rsvg-convert -w <宽>` 转 PNG；`research-map.zh.svg` 直接转 PNG。输出写回同一子目录，不会触及 docx。

老版图源（`*.mmd`、`*.prompt.md`）与已弃用的 `silent-failure.*` 保留在 `assets/_archive/` 下作为历史档。

## 封面信息

封面表格中的学号 / 研究生姓名 / 学院 / 学位类别 / 专业领域 / 校内导师 / 校外导师 / 开题日期等字段已在 `template.docx` 内手工填好。构建脚本只同步论文题目，其余封面信息原样保留；当前题目同时记录在 `chapters/00-meta.txt` 中。
