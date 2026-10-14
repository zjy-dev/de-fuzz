/* DeFuzz group sharing. D2 figures + editable native PowerPoint text.
 * npm run outline -> render_diagrams.py -> npm run build
 * Final artifacts remain in build/ until the native PowerPoint QA pass.
 */
"use strict";
const fs = require("fs");
const path = require("path");
const PptxGenJS = require("pptxgenjs");
const { autoFontSize, calcTextBox } = require("./pptxgenjs_helpers/text");
const { imageSizingContain } = require("./pptxgenjs_helpers/image");
const { warnIfSlideHasOverlaps, warnIfSlideElementsOutOfBounds } =
  require("./pptxgenjs_helpers/layout");
const { FontLibrary } = require("skia-canvas");

const ROOT = __dirname;
const WORKSPACE = path.dirname(ROOT);
const OUT = path.join(ROOT, "build");
const content = JSON.parse(fs.readFileSync(path.join(WORKSPACE, "project_intro_content.json"), "utf8"));
const T = {
  width: 10, height: 5.625, font: "Microsoft YaHei",
  green: "1B5E20", light: "E8F5E9", yellow: "FFC107", pale: "FFF8E1",
  dark: "333333", muted: "666666", gray: "E0E0E0", red: "C62828",
};
fs.mkdirSync(OUT, { recursive: true });
const clock = (sec) => `${Math.floor(sec / 60)}:${String(sec % 60).padStart(2, "0")}`;
const total = content.slides.reduce((sum, s) => sum + s.seconds, 0);
if (total >= 900) throw new Error("Speaking schedule exceeds 15 minutes.");

function writeOutline() {
  let elapsed = 0;
  const lines = [
    "# DeFuzz 项目介绍：论文结构与方法", "",
    `${content.slides.length} 页，建议讲述 ${clock(total)}，预留停顿时间，整体控制在 15 分钟以内。`,
    "", content.style, "",
    "技术路线以 `defuzz-paper/main.tex @ " + content.paper_revision + "` 为准。"
      + "复用开题的配色、校徽、栈布局案例及 GCC 上游反馈截图。",
    "", "植入来源：`" + content.injection_source + "`。",
    "当前进度：" + content.progress + " 新交付包含十份批次补丁及编号清单；以本次进度说明为准，不沿用旧计划包的状态。",
  ];

  const groups = [];
  content.slides.forEach((s, i) => {
    const last = groups[groups.length - 1];
    if (last && last.name === s.section) { last.end = i + 1; last.seconds += s.seconds; }
    else groups.push({ name: s.section, start: i + 1, end: i + 1, seconds: s.seconds });
  });
  const purpose = new Map(content.sections);
  lines.push("", "## 结构：四个章节，一条主线", "",
    "| 章节 | 页码 | 用时 | 这一章要说清什么 |", "| --- | --- | --- | --- |");
  groups.forEach((g) => {
    const span = g.start === g.end ? `P${g.start}` : `P${g.start}–P${g.end}`;
    lines.push(`| ${g.name} | ${span} | ${clock(g.seconds)} | ${purpose.get(g.name) || "—"} |`);
  });
  lines.push("",
    "主线是一句话：**编译器防御会静默失效 → 现有预言机判不出来 → 用证据接地的安全不变量当预言机 → "
    + "逐 ISA 落成可执行 checker → 用显式编排的 Agent 闭环定向搜索 → 用受控植入量化漏检**。",
    "",
    "技术路线只有**一套方案**，按顺序回答三件事，缺一件都跑不起来："
    + "**测什么**——把散落的安全要求写成带证据的不变量；"
    + "**怎么判**——每条不变量按适用 ISA 各写一个 checker；"
    + "**怎么找**——编排器盯住目标，Agent 按反馈反复改写种子。",
    "", "## 逐页大纲", "", "| 页 | 章节 | 讲述重点 | 用时 | 累计 |",
    "| --- | --- | --- | --- | --- |");
  content.slides.forEach((s, i) => {
    elapsed += s.seconds;
    lines.push(`| ${i + 1} | ${s.section} | ${s.title.replace(/\n/g, "")} `
      + `| ${clock(s.seconds)} | ${clock(elapsed)} |`);
  });
  lines.push("", "## 逐页文案与讲者备注");
  let cursor = 0;
  content.slides.forEach((s, i) => {
    lines.push("", `### P${i + 1} · ${s.section} · ${s.title.replace(/\n/g, "")}`, "",
      `讲述时间：${clock(s.seconds)}（累计 ${clock(cursor += s.seconds)}）。`, "");
    if (s.diagram) {
      lines.push(`主图：[${s.diagram}.d2](pptx-workspace/diagram-deck/diagrams/${s.diagram}.d2)`, "",
        "图中文字与关系：", "```d2",
        fs.readFileSync(path.join(ROOT, "diagrams", `${s.diagram}.d2`), "utf8").replace("...@theme.d2\n", "").trim(),
        "```", "");
    }
    if (s.side_title || s.schema_title) lines.push(s.side_title || s.schema_title, "");
    if (s.formula) lines.push("判定契约：`" + s.formula.replace(/　/g, " ") + "`", "");
    if (s.highlight) lines.push(s.highlight.replace(/\n/g, " "), "");
    if (s.challenges) {
      s.challenges.forEach((c) => lines.push(`**${c.num} ${c.name}**`, "",
        c.difficulty, "", "DeFuzz 的对应设计：" + c.ours, ""));
    }
    if (s.left) lines.push(s.left_title + "：", "", ...s.left.map((v) => "- " + v), "");
    if (s.right) lines.push(s.right_title + "：", "", ...s.right.map((v) => "- " + v), "");
    if (s.items) lines.push(...s.items.map((r) => `- ${r.join("：")}`), "");
    if (s.rows) lines.push(...s.rows.map((r) => `- ${r.join(" | ")}`), "");
    if (s.note) lines.push("设计说明：" + s.note, "");
    if (s.bottom) lines.push(s.bottom, "");
    if (s.code) lines.push(s.code_title || s.example_title || "代码示例", "", "```c", s.code, "```", "");
    if (s.stats) lines.push(s.stats.map(([n, label]) => `${label}：${n}`).join("；"), "");
    if (s.delivery) lines.push("植入交付：" + s.delivery, "");
    if (s.bugs) {
      lines.push(...s.bugs.map((b) => `- **${b.name}**（${b.meta}）${b.detail} ${b.status}`), "");
    }
    if (s.stages) {
      lines.push("| 阶段 | 时间 | 工作 | 内容 |", "| --- | --- | --- | --- |",
        ...s.stages.map((t) => `| ${t.id}${t.id === s.current ? " ←当前" : ""} `
          + `| ${t.span} | ${t.name} | ${t.desc} |`), "", s.position, "");
    }
    if (s.done) lines.push("已完成：", "", ...s.done.map((v) => "- " + v), "");
    if (s.todo) lines.push("接下来：", "", ...s.todo.map((v) => "- " + v), "");
    if (s.conclusion) lines.push(s.conclusion, "");
    if (s.lines) lines.push(s.lines.join(" → "), "");
    if (s.takeaway) lines.push("页面结论：" + s.takeaway, "");
    lines.push("讲者备注：", "", s.notes, "", "来源：" + s.source);
  });
  lines.push("", "## 生成与维护", "",
    "源文案：`pptx-workspace/project_intro_content.json`。PPT 源码：`diagram-deck/build.js`。",
    "图示保留 D2 与 SVG；为确保中文在投影电脑上稳定显示，PPT 使用高分辨率图像，标题、表格、代码与解释文字仍可直接编辑。",
    "", "```sh",
    "cd docs/presentations/pptx-workspace/diagram-deck",
    "npm ci",
    "npm run outline",
    "cd ..",
    "uv run python diagram-deck/render_diagrams.py",
    "node diagram-deck/build.js",
    "osascript export_project_intro.applescript \"$PWD/diagram-deck/build/项目介绍-DeFuzz.pptx\" \"$PWD/diagram-deck/build/项目介绍-DeFuzz.pdf\"",
    "```", "",
    "构建产物先保存在 `diagram-deck/build/`，完成渲染检查后复制到 `pptx-workspace/`。",
  );
  fs.writeFileSync(path.join(WORKSPACE, "..", "项目介绍.md"), lines.join("\n") + "\n");
  console.log(`[outline] ${content.slides.length} pages, ${clock(total)}`);
}
writeOutline();
if (process.argv.includes("--outline-only")) process.exit(0);

const fonts = path.join(OUT, "fonts");
if (fs.existsSync(path.join(fonts, "regular.ttf"))) {
  FontLibrary.use(T.font, [path.join(fonts, "regular.ttf"), path.join(fonts, "bold.ttf")]);
}
const pptx = new PptxGenJS();
pptx.defineLayout({ name: "OPENING_STYLE", width: T.width, height: T.height });
pptx.layout = "OPENING_STYLE";
pptx.author = "张景耀";
pptx.subject = "论文结构、具体方法、假阴性评估与植入进度";
pptx.title = content.title;
pptx.lang = "zh-CN";
pptx.theme = { headFontFace: T.font, bodyFontFace: T.font, lang: "zh-CN" };

function shape(slide, kind, x, y, w, h, fill, line = fill) {
  slide.addShape(kind, { x, y, w, h, fill: { color: fill },
    line: { color: line, width: .65 } });
}
function text(slide, value, x, y, w, h, size, options = {}) {
  const measured = autoFontSize(value, T.font, {
    x, y, w, h, fontSize: size, minFontSize: size, maxFontSize: size,
    mode: "auto", margin: 0, padding: 0, ...options,
  });
  slide.addText(value, {
    ...measured, fontFace: T.font, margin: 0, breakLine: false,
    color: T.dark, valign: "mid", ...options,
  });
}
function token(slide, value, x, y, w, h, size, options = {}) {
  // Single-line tokens deliberately disable wrapping, with ample box width.
  text(slide, value, x, y, w, h, size, { wrap: false, vert: "horz", ...options });
}
function plainEdges(slide) {
  shape(slide, pptx.ShapeType.rect, 0, 0, 10, .15, T.green);
  shape(slide, pptx.ShapeType.rect, 0, 5.545, 10, .08, T.yellow);
}
function footer(slide, s) {
  token(slide, "来源：" + s.source, .45, 5.30, 8.7, .18, 7.4, { color: T.muted });
  slide.slideNumber = { x: 9.27, y: 5.29, w: .30, h: .20,
    fontFace: T.font, fontSize: 8.5, color: T.muted, margin: 0 };
}
function takeaway(slide, value) {
  // Intentional containment: conclusion text sits on a pale yellow strip.
  shape(slide, pptx.ShapeType.rect, .45, 4.79, 9.1, .39, T.pale);
  text(slide, value, .58, 4.825, 8.84, .30, 11.8,
    { color: T.green, bold: true, align: "center" });
}
function frame(slide, s) {
  slide.background = { color: "FFFFFF" };
  // Intentional containment: white heading lies inside the inherited green header.
  shape(slide, pptx.ShapeType.rect, 0, 0, 10, .55, T.green);
  text(slide, s.title, .36, .075, 9.28, .40, 19.5,
    { color: "FFFFFF", bold: true, wrap: false });
  shape(slide, pptx.ShapeType.rect, 0, 5.545, 10, .08, T.yellow);
  if (s.takeaway) takeaway(slide, s.takeaway);
  footer(slide, s);
  token(slide, s.section, .45, .70, 9.1, .22, 10,
    { color: T.muted, charSpacing: 1.2 });
}
function image(slide, filename, x, y, w, h, altText) {
  slide.addImage({ path: filename, ...imageSizingContain(filename, x, y, w, h), altText });
}
function cover(slide, s) {
  plainEdges(slide);
  image(slide, path.join(WORKSPACE, "seu-logo.png"), 4.59, .49, .82, .82, "东南大学校徽");
  token(slide, "东南大学 · 网络空间安全学院", 1, 1.40, 8, .29, 12, { align: "center", color: T.green });
  const geometry = calcTextBox(28, { text: s.title, w: 9, fontFace: T.font, padding: 0 });
  text(slide, s.title, .5, 2.01, 9, Math.max(1.13, geometry.h), 28,
    { bold: true, color: T.green, align: "center" });
  text(slide, s.subtitle, .7, 3.28, 8.6, .64, 12.5, { color: T.muted, align: "center" });
  token(slide, "张景耀  ·  指导教师：徐坚皓", 1, 4.21, 8, .35, 13,
    { color: T.green, align: "center" });
  token(slide, "小组分享 · 2026.09", 1, 5.00, 8, .28, 10.5,
    { color: T.muted, align: "center" });
}
function diagram(slide, s) {
  diagramAt(slide, s, .45, 1.04, 9.10, 3.52);
}
function diagramAt(slide, s, x, y, w, h) {
  const filename = path.join(OUT, "diagrams", `${s.diagram}.png`);
  const svg = fs.readFileSync(path.join(OUT, "diagrams", `${s.diagram}.svg`), "utf8");
  const labels = [...svg.matchAll(/<text\b[^>]*>([\s\S]*?)<\/text>/g)]
    .map((m) => m[1].replace(/<[^>]+>/g, "").replace(/&amp;/g, "&")).join("；");
  image(slide, filename, x, y, w, h, labels);
  slide.addNotes("图中标签：" + labels);
}
function explainItems(slide, items, x, y, w, pitch = .95, size = 12) {
  items.forEach(([label, desc], i) => {
    const yy = y + i * pitch;
    token(slide, label, x, yy, w, .28, 13, { color: T.green, bold: true });
    text(slide, desc, x, yy + .34, w, pitch - .40, size, { valign: "top" });
  });
}
function figureSide(slide, s) {
  diagramAt(slide, s, .45, 1.14, 5.6, 3.35);
  token(slide, s.side_title, 6.34, 1.08, 3.20, .34, 14.5,
    { color: T.green, bold: true });
  explainItems(slide, s.items, 6.34, 1.66, 3.20, s.item_pitch || .92, 11.7);
}
function figureBottom(slide, s) {
  diagramAt(slide, s, .45, 1.03, 9.1, 2.70);
  // Bottom band splits evenly across however many explanation columns exist.
  const count = s.items.length;
  const span = 9.0 / count;
  s.items.forEach(([label, desc], i) => {
    const xx = .60 + i * span;
    token(slide, label, xx, 3.94, span - .28, .28, 13, { color: T.green, bold: true });
    text(slide, desc, xx, 4.27, span - .30, .48, count > 2 ? 10.4 : 11.2,
      { valign: "top" });
  });
}
function challenges(slide, s) {
  const gap = .20;
  const width = (9.1 - gap * 2) / 3;
  s.challenges.forEach((c, i) => {
    const x = .45 + i * (width + gap);
    // Intentional containment: challenge id and name share one green band.
    shape(slide, pptx.ShapeType.rect, x, 1.20, width, .50, T.green);
    token(slide, c.num, x + .14, 1.29, .48, .32, 16, { color: T.yellow, bold: true });
    token(slide, c.name, x + .68, 1.30, width - .82, .30, 14,
      { color: "FFFFFF", bold: true });
    shape(slide, pptx.ShapeType.rect, x, 1.70, width, 1.46, "FAFAFA", T.gray);
    text(slide, c.difficulty, x + .16, 1.80, width - .32, 1.26, 12.6,
      { valign: "top" });
    shape(slide, pptx.ShapeType.rect, x, 3.16, width, 1.06, T.pale);
    token(slide, "DeFuzz 的对应设计", x + .16, 3.24, width - .32, .24, 10.4,
      { color: "8C6D00", bold: true });
    text(slide, c.ours, x + .16, 3.52, width - .32, .62, 12.6,
      { color: T.green, valign: "top" });
  });
}
function goals(slide, s) {
  const columns = [
    { x: .45, w: 4.20, title: s.left_title, lines: s.left, fill: T.light },
    { x: 4.90, w: 4.65, title: s.right_title, lines: s.right, fill: T.pale },
  ];
  columns.forEach((col) => {
    // Intentional containment: bullet text sits inside the tinted column panel.
    shape(slide, pptx.ShapeType.rect, col.x, 1.02, col.w, 3.62, col.fill);
    shape(slide, pptx.ShapeType.rect, col.x, 1.02, col.w, .40, T.green);
    text(slide, col.title, col.x + .14, 1.065, col.w - .28, .31, 12.8,
      { color: "FFFFFF", bold: true });
    col.lines.forEach((line, i) => {
      const yy = 1.58 + i * .76;
      shape(slide, pptx.ShapeType.ellipse, col.x + .16, yy + .06, .13, .13, T.green);
      text(slide, line, col.x + .40, yy, col.w - .56, .68, 10.8, { valign: "top" });
    });
  });
}
function nativeTable(slide, rows, x, y, widths, rowHeight, header = false, size = 11.4) {
  rows.forEach((row, r) => {
    let xx = x;
    row.forEach((value, c) => {
      const fill = header && r === 0 ? T.green : r % 2 ? "F5F5F5" : T.light;
      // Intentional containment: each cell contains a text layer.
      shape(slide, pptx.ShapeType.rect, xx, y + r * rowHeight, widths[c], rowHeight, fill, "FFFFFF");
      text(slide, value, xx + .12, y + r * rowHeight + .05, widths[c] - .24, rowHeight - .10,
        size, { color: header && r === 0 ? "FFFFFF" : c === 0 ? T.green : T.dark,
          bold: c === 0 || (header && r === 0), valign: "mid" });
      xx += widths[c];
    });
  });
}
function figureSchema(slide, s) {
  // Optional formula strip on top, optional full-width design note underneath.
  let top = 1.11;
  if (s.formula) {
    shape(slide, pptx.ShapeType.rect, .45, 1.00, 9.1, .37, T.pale);
    token(slide, s.formula, .55, 1.055, 8.9, .27, 11.6,
      { color: T.green, bold: true, align: "center" });
    top = 1.50;
  }
  const bottom = s.note ? 4.20 : 4.62;
  diagramAt(slide, s, .48, top, 4.20, bottom - top);
  token(slide, s.schema_title, 5.08, top + .02, 4.38, .31, 14.3,
    { color: T.green, bold: true });
  const tableTop = top + .50;
  const rowHeight = Math.min(.66, (bottom - tableTop) / s.rows.length);
  nativeTable(slide, s.rows, 5.08, tableTop, [1.25, 3.19], rowHeight, false, 10.9);
  if (s.note) {
    // Intentional containment: design note text sits on a tinted strip.
    shape(slide, pptx.ShapeType.rect, .45, 4.26, 9.1, .48, T.light);
    text(slide, s.note, .60, 4.30, 8.8, .40, 9.7, { color: T.dark, valign: "top" });
  }
}
function researchTable(slide, s) {
  const rows = s.rows.length + 1;
  const rowHeight = Math.min(.71, 3.10 / rows);
  const tableBottom = 1.18 + rows * rowHeight;
  nativeTable(slide, [s.columns, ...s.rows], .45, 1.18, s.widths, rowHeight, true,
    rows > 4 ? 10.6 : 11.7);
  text(slide, s.bottom, .62, tableBottom + .06, 8.8, .32, 11.6,
    { color: T.green, bold: true, valign: "mid" });
}
function codeBox(slide, code, x, y, w, h, size = 12.2) {
  // Intentional containment: editable code printed on a neutral background.
  shape(slide, pptx.ShapeType.rect, x, y, w, h, "F5F5F5", T.gray);
  text(slide, code, x + .18, y + .15, w - .36, h - .30, size,
    { fontFace: "Consolas", valign: "top", breakLine: false, wrap: false });
}
function walkthrough(slide, s) {
  token(slide, s.code_title, .60, 1.11, 4.10, .32, 13,
    { color: T.green, bold: true });
  codeBox(slide, s.code, .60, 1.65, 4.10, 2.23, 11.5);
  shape(slide, pptx.ShapeType.rect, .60, 4.10, 4.10, .41, "FFEBEE");
  token(slide, "buf < 返回地址 < guard → Fail", .71, 4.16, 3.88, .26, 11.8,
    { color: T.red, bold: true, align: "center" });
  explainItems(slide, s.items, 5.03, 1.13, 4.40, s.item_pitch || .83, 11.4);
}
function fnr(slide, s) {
  token(slide, s.delivery, .55, 1.03, 9.0, .30, 13.4, { color: T.green, bold: true });
  s.stats.forEach(([value, label], i) => {
    const xx = .55 + i * 3.06;
    token(slide, value, xx, 1.44, 1.45, .43, 25, { color: T.green, bold: true });
    token(slide, label, xx + 1.50, 1.55, 1.40, .27, 11.5, { color: T.muted });
  });
  token(slide, s.example_title, .55, 2.12, 4.75, .28, 12.4, { color: T.green, bold: true });
  codeBox(slide, s.code, .55, 2.60, 4.68, 1.93, 10.4);
  explainItems(slide, s.items, 5.60, 2.13, 3.82, s.item_pitch || .83, 11.4);
}
function progress(slide, s) {
  const columns = [
    { x: .55, title: s.done_title, lines: s.done, fill: T.light },
    { x: 5.17, title: s.todo_title, lines: s.todo, fill: T.pale },
  ];
  const rows = Math.max(s.done.length, s.todo.length);
  const pitch = Math.min(.50, 2.28 / rows);
  columns.forEach((col) => {
    shape(slide, pptx.ShapeType.rect, col.x, 1.18, 4.25, 2.90, col.fill);
    token(slide, col.title, col.x + .18, 1.34, 3.89, .40, 19, { color: T.green, bold: true });
    col.lines.forEach((line, i) => text(slide, line, col.x + .18, 1.90 + i * pitch,
      3.89, pitch - .06, rows > 3 ? 11.2 : 12));
  });
  text(slide, s.conclusion, .64, 4.24, 8.72, .37, 12.4, { color: T.green, bold: true });
}
function findings(slide, s) {
  text(slide, s.highlight.replace(/\n/g, "，"), .60, .98, 8.8, .34, 15.5,
    { bold: true, color: T.green, align: "center" });
  image(slide, path.join(OUT, "diagrams", "gcc-comment-crop.png"), .60, 1.34, 8.8, 1.30,
    "开题材料中的 GCC LoongArch 邮件：severe security vulnerability；讨论移植修复。");
  // Copied from the proposal defense deck: one card per confirmed upstream defect.
  s.bugs.forEach((b, i) => {
    const x = .60 + i * 3.00;
    shape(slide, pptx.ShapeType.rect, x, 2.78, 2.80, .34, T.green);
    token(slide, b.name, x + .12, 2.845, 2.56, .24, 11, { color: "FFFFFF", bold: true });
    shape(slide, pptx.ShapeType.rect, x, 3.12, 2.80, 1.54, "FAFAFA", T.gray);
    token(slide, b.meta, x + .12, 3.20, 2.56, .22, 8.8, { color: T.muted });
    text(slide, b.detail, x + .12, 3.44, 2.56, .80, 9.5, { valign: "top" });
    token(slide, b.status, x + .12, 4.32, 2.56, .24, 9.4, { color: T.green, bold: true });
  });
}
function schedule(slide, s) {
  // Copied from the proposal defense deck: six-stage plan, current stage highlighted.
  const gap = .20;
  const width = (9.1 - gap * 2) / 3;
  s.stages.forEach((st, i) => {
    const x = .45 + (i % 3) * (width + gap);
    const y = 1.02 + Math.floor(i / 3) * 1.64;
    const active = st.id === s.current;
    shape(slide, pptx.ShapeType.rect, x, y, width, .40, active ? T.green : T.light);
    token(slide, st.id, x + .13, y + .07, .40, .26, 12.6,
      { color: active ? T.yellow : T.green, bold: true });
    token(slide, st.span, x + .60, y + .08, width - .74, .24, 10.2,
      { color: active ? "FFFFFF" : T.muted });
    shape(slide, pptx.ShapeType.rect, x, y + .40, width, 1.02, active ? T.pale : "FAFAFA", T.gray);
    token(slide, st.name, x + .13, y + .48, width - .26, .26, 11.4, { color: T.green, bold: true });
    text(slide, st.desc, x + .13, y + .76, width - .26, .62, 9.5, { valign: "top" });
  });
  token(slide, s.position, .5, 4.36, 9, .28, 11.6, { align: "center", color: T.green, bold: true });
}
function candidateMap(slide, s) {
  s.groups.forEach((g, index) => {
    const x = index ? 5.65 : .85;
    token(slide, g.name, x, 1.10, 2.0, .42, 23, { color: T.green, bold: true });
    token(slide, `${g.total} 项`, x + 2.08, 1.10, 1.6, .44, 25,
      { color: T.green, bold: true, align: "right" });
    for (let i = 0; i < g.total; i++) {
      const col = i % 10, row = Math.floor(i / 10);
      shape(slide, pptx.ShapeType.rect, x + col * .37, 1.89 + row * .37, .27, .27,
        i < g.core ? T.green : T.yellow);
    }
  });
  shape(slide, pptx.ShapeType.rect, 2.51, 4.03, .18, .18, T.green);
  token(slide, s.legend[0], 2.82, 3.96, 2.10, .30, 13, { color: T.green });
  shape(slide, pptx.ShapeType.rect, 5.14, 4.03, .18, .18, T.yellow);
  token(slide, s.legend[1], 5.46, 3.96, 2.10, .30, 13, { color: T.green });
  token(slide, s.scope, .5, 4.39, 9, .27, 12, { align: "center", color: T.muted });
}
function closing(slide, s) {
  plainEdges(slide);
  text(slide, s.title, .5, 1.10, 9, 1.22, 29, { color: T.green, bold: true, align: "center" });
  s.lines.forEach((label, i) => {
    const x = .65 + i * 3.0;
    shape(slide, pptx.ShapeType.ellipse, x + 1.03, 2.79, .47, .47, T.green);
    // Intentional containment: index in a circular marker.
    token(slide, String(i + 1), x + 1.03, 2.84, .47, .32, 16,
      { color: "FFFFFF", bold: true, align: "center" });
    token(slide, label, x, 3.51, 2.6, .40, 18, { color: T.green, align: "center" });
  });
  token(slide, s.takeaway, .5, 4.55, 9, .34, 12.5, { align: "center", color: T.muted });
  token(slide, "欢迎讨论", .5, 5.04, 9, .27, 13, { align: "center", color: T.green });
}

let elapsed = 0;
content.slides.forEach((s, index) => {
  const slide = pptx.addSlide();
  if (!["cover", "closing"].includes(s.layout)) frame(slide, s);
  const renderer = { cover, diagram, findings, candidate_map: candidateMap, closing,
    figure_side: figureSide, figure_bottom: figureBottom, figure_schema: figureSchema,
    research_table: researchTable, walkthrough, fnr, progress,
    challenges, goals, schedule }[s.layout];
  if (!renderer) throw new Error(`Unknown layout ${s.layout}`);
  renderer(slide, s);
  elapsed += s.seconds;
  slide.addNotes(`第 ${index + 1} 页｜建议 ${clock(s.seconds)}｜累计 ${clock(elapsed)}\n\n`
    + s.notes + `\n\n来源：${s.source}\n论文版本：${content.paper_revision}`);
  // Header text, takeaway strips and circular indices intentionally contain text.
  // All other elements occupy disjoint layout bands.
  warnIfSlideHasOverlaps(slide, pptx);
  warnIfSlideElementsOutOfBounds(slide, pptx);
});

pptx.writeFile({ fileName: path.join(OUT, "项目介绍-DeFuzz.pptx") }).then(() => {
  fs.writeFileSync(path.join(OUT, "build-summary.json"), JSON.stringify({
    slide_count: content.slides.length, planned_seconds: total,
    d2_diagrams: content.slides.filter((s) => s.diagram).map((s) => s.diagram),
    copied_opening_image: "assets/gcc-dev-severe.png",
    status: "Awaiting native PowerPoint render and independent visual QA",
  }, null, 2) + "\n");
  console.log(`[pptx] ${content.slides.length} pages | ${clock(total)} | build/项目介绍-DeFuzz.pptx`);
});
