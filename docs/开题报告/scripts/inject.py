#!/usr/bin/env python3
"""Inject DeFuzz chapter content into the unpacked docx template."""

from __future__ import annotations

import re
import shutil
import struct
from pathlib import Path

WORK = Path("/tmp/defuzz-kaiti/work")
DOC = WORK / "word" / "document.xml"
STYLES = WORK / "word" / "styles.xml"
CHAPTERS = Path("/home/yall/project/de-fuzz/docs/开题报告/chapters")
ASSETS = Path("/home/yall/project/de-fuzz/docs/开题报告/assets")

# Page layout constants (from template.docx pgSz / pgMar)
# Page width = 11906 twips, left+right margins = 1134+1134 = 2268 twips
# Content width = 9638 twips × 635 EMU/twip
CONTENT_W_EMU = 9638 * 635          # 6_120_130 EMU  ≈ 170 mm
# Page height = 16838, top+bottom+footer margins = 1134+1134+992 = 3260 twips
CONTENT_H_EMU = (16838 - 3260) * 635  # 8_621_730 EMU  ≈ 239 mm

# 6 figures: (png stem, rId, caption)
FIGURES = [
    ("silent-failure",  "rId21", "图 6  隐式信任链中的 silent failure 概念示意"),
    ("defense-matrix",  "rId22", "图 1  防御机制 × ISA 二维研究空间矩阵"),
    ("fortify-path",    "rId23", "图 5  _FORTIFY_SOURCE 优化路径与 size silent gap"),
    ("architecture",   "rId24", "图 2  DeFuzz 总体架构"),
    ("oracle-flow",    "rId25", "图 3  防御机制不变量预言机判定流程"),
    ("main-loop",      "rId26", "图 4  大模型约束求解主循环"),
]

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------


def read_png_dims(path: Path) -> tuple[int, int]:
    """Read (width, height) from a PNG file header without external deps."""
    with open(path, "rb") as fh:
        fh.read(8)   # PNG signature
        fh.read(4)   # IHDR chunk length
        fh.read(4)   # IHDR type
        w = struct.unpack(">I", fh.read(4))[0]
        h = struct.unpack(">I", fh.read(4))[0]
    return w, h


def figure_emu(png_path: Path) -> tuple[int, int]:
    """Return (cx, cy) in EMU that fit the image within the content area."""
    pw, ph = read_png_dims(png_path)
    aspect = pw / ph
    if aspect >= 1.0:           # landscape / square: fill 88 % of content width
        cx = int(CONTENT_W_EMU * 0.88)
        cy = int(cx / aspect)
    else:                       # portrait: limit height to 78 % of page height
        max_cy = int(CONTENT_H_EMU * 0.78)
        cy_by_width = int(CONTENT_W_EMU * 0.70 / aspect)
        cy = min(max_cy, cy_by_width)
        cx = int(cy * aspect)
    return cx, cy


def xml_escape(s: str) -> str:
    s = s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
    # smart quotes
    s = s.replace("\u201c", "&#x201C;").replace("\u201d", "&#x201D;")
    s = s.replace("\u2018", "&#x2018;").replace("\u2019", "&#x2019;")
    return s


# Paragraph builders -------------------------------------------------------

def p_main_heading(text: str) -> str:
    """matches the original '一、研究背景:' style: bold, sz=24, no indent"""
    return (
        '<w:p>'
        '<w:pPr><w:pStyle w:val="Normal"/><w:widowControl/><w:jc w:val="start"/>'
        '<w:rPr><w:b/><w:bCs/><w:kern w:val="0"/><w:sz w:val="24"/></w:rPr></w:pPr>'
        '<w:r><w:rPr><w:b/><w:bCs/><w:kern w:val="0"/><w:sz w:val="24"/></w:rPr>'
        f'<w:t>{xml_escape(text)}</w:t></w:r></w:p>'
    )


def p_sub_heading(text: str) -> str:
    """subsection like '2.1 编译器模糊测试' or '1. 总体架构': bold sz 24"""
    return (
        '<w:p>'
        '<w:pPr><w:pStyle w:val="Normal"/><w:spacing w:lineRule="auto" w:line="360"/>'
        '<w:rPr><w:b/><w:bCs/><w:kern w:val="0"/><w:sz w:val="24"/></w:rPr></w:pPr>'
        '<w:r><w:rPr><w:b/><w:bCs/><w:kern w:val="0"/><w:sz w:val="24"/></w:rPr>'
        f'<w:t>{xml_escape(text)}</w:t></w:r></w:p>'
    )


def p_body(text: str) -> str:
    """body paragraph with first-line indent of 2 chars (firstLineChars=200)"""
    return (
        '<w:p>'
        '<w:pPr><w:pStyle w:val="Normal"/>'
        '<w:spacing w:lineRule="auto" w:line="360"/>'
        '<w:ind w:firstLineChars="200" w:startChars="0" w:start="0" w:endChars="0" w:end="0"/>'
        '<w:rPr><w:kern w:val="0"/><w:sz w:val="24"/></w:rPr></w:pPr>'
        '<w:r><w:rPr><w:kern w:val="0"/><w:sz w:val="24"/></w:rPr>'
        f'<w:t xml:space="preserve">{xml_escape(text)}</w:t></w:r></w:p>'
    )


def p_ref(text: str) -> str:
    """reference list item: no first-line indent, hanging style"""
    return (
        '<w:p>'
        '<w:pPr><w:pStyle w:val="Normal"/>'
        '<w:spacing w:lineRule="auto" w:line="360"/>'
        '<w:rPr><w:kern w:val="0"/><w:sz w:val="24"/></w:rPr></w:pPr>'
        '<w:r><w:rPr><w:kern w:val="0"/><w:sz w:val="24"/></w:rPr>'
        f'<w:t xml:space="preserve">{xml_escape(text)}</w:t></w:r></w:p>'
    )


def p_blank() -> str:
    return (
        '<w:p>'
        '<w:pPr><w:pStyle w:val="Normal"/>'
        '<w:spacing w:lineRule="auto" w:line="360"/>'
        '<w:rPr><w:sz w:val="24"/></w:rPr></w:pPr></w:p>'
    )


def p_image(rid: str, img_name: str, fig_id: int, cx: int, cy: int) -> str:
    """Centered inline image paragraph using a pre-registered relationship ID."""
    ns_a   = "xmlns:a='http://schemas.openxmlformats.org/drawingml/2006/main'"
    ns_pic = "xmlns:pic='http://schemas.openxmlformats.org/drawingml/2006/picture'"
    ns_r   = "xmlns:r='http://schemas.openxmlformats.org/officeDocument/2006/relationships'"
    return (
        '<w:p>'
        '<w:pPr><w:pStyle w:val="Normal"/>'
        '<w:spacing w:lineRule="auto" w:line="360"/>'
        '<w:jc w:val="center"/></w:pPr>'
        '<w:r><w:rPr/>'
        '<w:drawing>'
        f'<wp:inline distT="0" distB="0" distL="0" distR="0" '
        f'xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">'
        f'<wp:extent cx="{cx}" cy="{cy}"/>'
        f'<wp:effectExtent l="0" t="0" r="0" b="0"/>'
        f'<wp:docPr id="{fig_id}" name="Figure{fig_id}"/>'
        f'<wp:cNvGraphicFramePr>'
        f'<a:graphicFrameLocks {ns_a} noChangeAspect="1"/>'
        f'</wp:cNvGraphicFramePr>'
        f'<a:graphic {ns_a}>'
        f'<a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">'
        f'<pic:pic {ns_pic}>'
        f'<pic:nvPicPr>'
        f'<pic:cNvPr id="{fig_id}" name="{img_name}"/>'
        f'<pic:cNvPicPr/>'
        f'</pic:nvPicPr>'
        f'<pic:blipFill>'
        f'<a:blip {ns_r} r:embed="{rid}"/>'
        f'<a:stretch><a:fillRect/></a:stretch>'
        f'</pic:blipFill>'
        f'<pic:spPr>'
        f'<a:xfrm><a:off x="0" y="0"/><a:ext cx="{cx}" cy="{cy}"/></a:xfrm>'
        f'<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>'
        f'</pic:spPr>'
        f'</pic:pic>'
        f'</a:graphicData>'
        f'</a:graphic>'
        f'</wp:inline>'
        '</w:drawing>'
        '</w:r></w:p>'
    )


def p_caption(text: str) -> str:
    """Centered, small-italic caption line below a figure."""
    return (
        '<w:p>'
        '<w:pPr><w:pStyle w:val="Normal"/>'
        '<w:spacing w:lineRule="auto" w:line="300"/>'
        '<w:jc w:val="center"/>'
        '<w:rPr><w:rFonts w:ascii="宋体" w:hAnsi="宋体" w:eastAsia="宋体"/>'
        '<w:i/><w:sz w:val="20"/></w:rPr></w:pPr>'
        '<w:r><w:rPr><w:rFonts w:ascii="宋体" w:hAnsi="宋体" w:eastAsia="宋体"/>'
        '<w:i/><w:sz w:val="20"/></w:rPr>'
        f'<w:t>{xml_escape(text)}</w:t></w:r></w:p>'
    )


def p_image_placeholder(label: str) -> str:
    """centered grey caption used as image placeholder (fallback only)"""
    return (
        '<w:p>'
        '<w:pPr><w:pStyle w:val="Normal"/><w:spacing w:lineRule="auto" w:line="360"/>'
        '<w:jc w:val="center"/>'
        '<w:rPr><w:rFonts w:ascii="宋体" w:hAnsi="宋体" w:eastAsia="宋体"/>'
        '<w:i/><w:color w:val="808080"/><w:sz w:val="22"/></w:rPr></w:pPr>'
        '<w:r><w:rPr><w:rFonts w:ascii="宋体" w:hAnsi="宋体" w:eastAsia="宋体"/>'
        '<w:i/><w:color w:val="808080"/><w:sz w:val="22"/></w:rPr>'
        f'<w:t>{xml_escape(label)}</w:t></w:r></w:p>'
    )


# ---------------------------------------------------------------------------
# Body paragraph generation from chapters
# ---------------------------------------------------------------------------

MAIN_HEADING_RE = re.compile(r"^[一二三四五六]、")
SUB_HEADING_RE = re.compile(r"^\d+\.\d+\s")
NUMBERED_HEADING_RE = re.compile(r"^\d+\.\s")
REF_LINE_RE = re.compile(r"^\[\d+\]")


def render_chapter(text: str, *, kind: str = "body") -> list[str]:
    blocks: list[str] = []
    paragraphs = [p.strip() for p in text.strip().split("\n\n") if p.strip()]
    for para in paragraphs:
        first = para.split("\n", 1)[0].strip()
        if kind == "ref":
            # references: each para is one citation
            blocks.append(p_ref(para))
            continue
        if first == "参考文献":
            blocks.append(p_main_heading(first))
            continue
        if MAIN_HEADING_RE.match(first):
            blocks.append(p_main_heading(first))
        elif SUB_HEADING_RE.match(first) or NUMBERED_HEADING_RE.match(first):
            blocks.append(p_sub_heading(first))
        else:
            blocks.append(p_body(para))
    return blocks


# ---------------------------------------------------------------------------
# Build full body block
# ---------------------------------------------------------------------------

def _fig(stem: str) -> list[str]:
    """Return [p_image, p_caption, p_blank] for a named figure."""
    match = next((f for f in FIGURES if f[0] == stem), None)
    if match is None:
        raise ValueError(f"unknown figure stem: {stem!r}")
    _, rid, caption = match
    fig_id = 21 + [f[0] for f in FIGURES].index(stem)  # unique docPr id
    png_path = ASSETS / f"{stem}.png"
    cx, cy = figure_emu(png_path)
    return [
        p_image(rid, f"{stem}.png", fig_id, cx, cy),
        p_caption(caption),
        p_blank(),
    ]


def build_body_xml() -> str:
    parts: list[str] = []

    # Chapter 1
    parts += render_chapter((CHAPTERS / "01-background.txt").read_text(encoding="utf-8"))
    parts += _fig("silent-failure")   # 图 6 放开篇，甧为研究动机示意图
    parts += _fig("defense-matrix")   # 图 1 第一章末

    # Chapter 2
    parts += render_chapter((CHAPTERS / "02-related-work.txt").read_text(encoding="utf-8"))
    parts.append(p_blank())

    # Chapter 3
    parts += render_chapter((CHAPTERS / "03-objective-content.txt").read_text(encoding="utf-8"))
    parts.append(p_blank())

    # Chapter 4
    parts += render_chapter((CHAPTERS / "04-technical-route.txt").read_text(encoding="utf-8"))
    parts += _fig("architecture")     # 图 2 第四章 §1
    parts += _fig("oracle-flow")      # 图 3 第四章 §2
    parts += _fig("main-loop")        # 图 4 第四章 §3

    # Chapter 5
    parts += render_chapter((CHAPTERS / "05-preliminary-experiment.txt").read_text(encoding="utf-8"))
    parts += _fig("fortify-path")     # 图 5 第五章 5.1

    # References (the txt file already starts with the "参考文献" heading)
    ref_text = (CHAPTERS / "06-references.txt").read_text(encoding="utf-8")
    parts.append(p_main_heading("参考文献"))
    # strip the leading "参考文献" line from the txt before rendering refs
    ref_body = re.sub(r"^\s*参考文献\s*\n+", "", ref_text)
    parts += render_chapter(ref_body, kind="ref")
    return "\n".join(parts)


# ---------------------------------------------------------------------------
# Schedule rows
# ---------------------------------------------------------------------------

SCHEDULE = [
    ("2026.02-2026.03", "完成防御机制不变量调研，建立统一记录格式"),
    ("2026.03-2026.04", "扩展预言机框架到 _FORTIFY_SOURCE 与 IBT，完善大模型提示词流水线"),
    ("2026.04-2026.05", "跨架构 flag profile 实验框架定型，在 GCC 上完成全机制端到端实验"),
    ("2026.04-2026.05", "把编译器子系统与覆盖率适配层扩展到 LLVM 工具链"),
    ("2026.05-2026.06", "在 LLVM 上复用预言机框架开展对照实验，形成缺陷上报闭环"),
    ("2026.05-2026.06", "撰写学位论文，整理实验数据与图表，准备投稿"),
    ("2026.06", "学位论文答辩"),
]


# ---------------------------------------------------------------------------
# Main injection
# ---------------------------------------------------------------------------

def main() -> None:
    text = DOC.read_text(encoding="utf-8")

    # 1. Body content replacement.
    # The first page (cover) is preserved verbatim from template.docx, which
    # already contains the filled-in personal info. We only rewrite the body
    # cell that holds the chapter content. The body lives inside the big
    # single-cell table titled "开题报告内容（具体要求见《东南大学…》）"; we
    # snap to: end of the title paragraph (stable across rebuilds) → the
    # closing </w:tc> of that cell.
    title_marker = (
        '<w:t>（具体要求见《东南大学研究生论文选题和开题报告的原则和要求》）</w:t>'
    )
    title_idx = text.find(title_marker)
    if title_idx < 0:
        raise RuntimeError("could not locate body title marker")
    body_start = text.find('</w:p>', title_idx)
    if body_start < 0:
        raise RuntimeError("could not snap to title <w:p> close")
    body_start += len('</w:p>')
    body_end = text.find('</w:tc>', body_start)
    if body_end < 0:
        raise RuntimeError("could not locate body cell </w:tc>")

    # 0. Prepare images in work dir (idempotent)
    _prepare_images()

    body_xml = build_body_xml()
    text = text[:body_start] + body_xml + text[body_end:]

    # 2. Theory-and-hardware cell (the （一）论文的理论分析... block):
    # replace the entire content of the single-cell table after that heading.
    theory_marker = '<w:t>（一）论文的理论分析与硬件要求及其预期达到的水平与结果</w:t>'
    theory_idx = text.find(theory_marker)
    if theory_idx < 0:
        raise RuntimeError("missing theory section heading")
    cell_open = text.find('<w:tc>', theory_idx)
    if cell_open < 0:
        raise RuntimeError("could not locate theory <w:tc>")
    cell_pr_close = text.find('</w:tcPr>', cell_open) + len('</w:tcPr>')
    cell_close = text.find('</w:tc>', cell_pr_close)
    if cell_close < 0:
        raise RuntimeError("could not locate theory </w:tc>")

    plan_text = (CHAPTERS / "07-plan.txt").read_text(encoding="utf-8")
    # Pull only the prose between the (一) and (二) headings.
    m = re.search(
        r"（一）[^\n]*\n+(?P<prose>.*?)\n+（二）", plan_text, re.DOTALL,
    )
    if m is None:
        raise RuntimeError("could not parse theory paragraph from 07-plan.txt")
    theory_paras = [p.strip() for p in m.group("prose").strip().split("\n\n") if p.strip()]
    theory_xml = "\n".join(p_body(p) for p in theory_paras)
    # Replace the entire body of the theory cell with the theory paragraphs.
    text = text[:cell_pr_close] + theory_xml + text[cell_close:]

    # 3. Schedule table replacement.
    # Strategy: find the schedule table by its header row marker
    # ('起讫日期'/'工  作  内  容  和  要  求'/'备  注') and replace
    # all subsequent <w:tr>...</w:tr> data rows up to but not
    # including the row containing '学校指导教师'.
    schedule_header_idx = text.find('<w:t>起讫日期</w:t>')
    schedule_after_idx = text.find('<w:t>学校指导教师对开题报告的综合意见</w:t>')
    if schedule_header_idx < 0 or schedule_after_idx < 0:
        raise RuntimeError("could not locate schedule table")

    # Find end of header row: first </w:tr> after the header marker
    header_row_end = text.find('</w:tr>', schedule_header_idx) + len('</w:tr>')
    # Find start of the row that contains 学校指导教师: walk back from
    # schedule_after_idx to the nearest '<w:tr' before it. We match the
    # opening tag without the closing '>' so both <w:tr> and
    # <w:tr w14:paraId="..."> (post pack/unpack) work.
    row_start = text.rfind('<w:tr ', header_row_end, schedule_after_idx)
    row_start_plain = text.rfind('<w:tr>', header_row_end, schedule_after_idx)
    row_start = max(row_start, row_start_plain)
    if row_start < 0:
        raise RuntimeError("could not locate next-row boundary for schedule table")

    # Build new schedule data rows
    rows_xml: list[str] = []
    for date_str, work_str in SCHEDULE:
        rows_xml.append(_schedule_row(date_str, work_str))
    new_schedule_block = "\n".join(rows_xml)

    text = text[:header_row_end] + "\n" + new_schedule_block + "\n" + text[row_start:]

    DOC.write_text(text, encoding="utf-8")
    print(f"injected body ({len(body_xml)} chars) + {len(SCHEDULE)} schedule rows + {len(FIGURES)} figures")

    # Normalise font names across all word XML files (replace compat-font lists)
    _normalise_fonts()


def _prepare_images() -> None:
    """Copy PNGs into word/media/ and register relationships (idempotent)."""
    media_dir = WORK / "word" / "media"
    media_dir.mkdir(exist_ok=True)

    rels_path = WORK / "word" / "_rels" / "document.xml.rels"
    rels_text = rels_path.read_text(encoding="utf-8")

    added: list[str] = []
    for stem, rid, _ in FIGURES:
        src = ASSETS / f"{stem}.png"
        dst = media_dir / f"{stem}.png"
        shutil.copy2(src, dst)

        # Add relationship if not already present
        if rid not in rels_text:
            rel = (
                f'  <Relationship Id="{rid}" '
                f'Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" '
                f'Target="media/{stem}.png"/>'
            )
            rels_text = rels_text.replace("</Relationships>", rel + "\n</Relationships>")
            added.append(rid)

    rels_path.write_text(rels_text, encoding="utf-8")

    # Ensure [Content_Types].xml declares the png extension
    ct_path = WORK / "[Content_Types].xml"
    ct_text = ct_path.read_text(encoding="utf-8")
    png_ct = '<Default Extension="png" ContentType="image/png"/>'
    if 'Extension="png"' not in ct_text:
        ct_text = ct_text.replace("</Types>", f"  {png_ct}\n</Types>")
        ct_path.write_text(ct_text, encoding="utf-8")

    print(f"  images: copied {len(FIGURES)} PNGs, registered {len(added)} new rels")


def _normalise_fonts() -> None:
    """Replace 'SimSun;...' / 'SimHei;...' compat-font strings with plain names."""
    replacements = [
        ("SimSun;汉仪书宋二KW", "宋体"),
        ("SimHei;汉仪中黑KW", "黑体"),
    ]
    for xml_file in WORK.rglob("*.xml"):
        raw = xml_file.read_text(encoding="utf-8")
        modified = raw
        for old, new in replacements:
            modified = modified.replace(old, new)
        if modified != raw:
            xml_file.write_text(modified, encoding="utf-8")
            print(f"  font-normalised {xml_file.relative_to(WORK)}")


def _schedule_row(date_str: str, work_str: str) -> str:
    cell_pPr = (
        '<w:pPr><w:pStyle w:val="Normal"/>'
        '<w:spacing w:lineRule="exact" w:line="320"/>'
        '<w:jc w:val="center"/>'
        '<w:rPr><w:rFonts w:ascii="宋体" w:hAnsi="宋体" w:eastAsia="宋体"/>'
        '<w:sz w:val="24"/></w:rPr></w:pPr>'
    )
    rPr = (
        '<w:rPr><w:rFonts w:ascii="宋体" w:hAnsi="宋体" w:eastAsia="宋体"/>'
        '<w:sz w:val="24"/></w:rPr>'
    )

    def cell(width: int, text_value: str) -> str:
        return (
            '<w:tc>'
            f'<w:tcPr><w:tcW w:w="{width}" w:type="dxa"/>'
            '<w:tcBorders>'
            '<w:top w:val="single" w:sz="4" w:space="0" w:color="000000"/>'
            '<w:start w:val="single" w:sz="4" w:space="0" w:color="000000"/>'
            '<w:bottom w:val="single" w:sz="4" w:space="0" w:color="000000"/>'
            '<w:end w:val="single" w:sz="4" w:space="0" w:color="000000"/>'
            '</w:tcBorders><w:vAlign w:val="center"/></w:tcPr>'
            f'<w:p>{cell_pPr}<w:r>{rPr}<w:t xml:space="preserve">{xml_escape(text_value)}</w:t></w:r></w:p>'
            '</w:tc>'
        )

    return (
        '<w:tr><w:trPr><w:trHeight w:val="480" w:hRule="atLeast"/></w:trPr>'
        + cell(2077, date_str)
        + cell(5167, work_str)
        + cell(2297, "")
        + '</w:tr>'
    )


if __name__ == "__main__":
    main()
