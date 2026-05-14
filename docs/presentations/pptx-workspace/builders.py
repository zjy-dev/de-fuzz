"""Per-slide builders for the DeFuzz 开题答辩 deck.

章节结构:
    01 研究背景与意义   slides 1-3
    02 研究现状         slide 4
    03 研究目标与内容   slides 5-7
    04 研究方案         slides 8-10
    05 现有成果         slides 11-13

页码由 theme.PageCounter 自动维护; 扉页 builder 不调 pc.next(),
内容页调 pc.next() 取页号传给 add_page_frame.
"""
from __future__ import annotations

from pathlib import Path

from pptx.util import Inches, Pt

from theme import (
    BLUE_DARK,
    BLUE_LIGHT,
    CONTENT_H,
    CONTENT_PAD_X,
    CONTENT_PAD_Y,
    CONTENT_TOP,
    CONTENT_W,
    FOOTER_H,
    GRAY_100,
    GRAY_300,
    GRAY_500,
    GRAY_600,
    GRAY_700,
    GRAY_800,
    GREEN_DARK,
    GREEN_LIGHT,
    GREEN_PALE,
    HEADER_H,
    MSO_ANCHOR,
    MSO_SHAPE,
    PP_ALIGN,
    RED_DARK,
    RED_LIGHT,
    SLIDE_H,
    SLIDE_W,
    WHITE,
    YELLOW_ACCENT,
    YELLOW_LIGHT,
    add_arrow,
    add_page_frame,
    add_rect,
    add_section_title,
    add_text,
)

BLANK_LAYOUT = 6  # python-pptx default blank layout index

ASSETS_DIR = Path(__file__).resolve().parent / "assets"
IMG_CVE = ASSETS_DIR / "cve_growth.zh.png"
IMG_STACK = ASSETS_DIR / "stack-layout.zh.png"
IMG_MATRIX = ASSETS_DIR / "defense-matrix.zh.png"
IMG_PIPELINE = ASSETS_DIR / "pipeline.zh.png"
IMG_ARCH = ASSETS_DIR / "architecture.zh.png"
IMG_ORACLE = ASSETS_DIR / "oracle-flow.zh.png"
IMG_GCC_DEV = ASSETS_DIR / "gcc-dev-severe.png"


# ─────────────────────────────────────────────────────────────
# 通用: 章节扉页渲染
# ─────────────────────────────────────────────────────────────
def _render_section_divider(slide, number: str, title_zh: str, title_en: str, footnote: str) -> None:
    """白底 + 绿/黄装饰条 + 大号章节号 + 主副标题 + 底部脚注 (5 章共用)."""
    add_rect(slide, 0, 0, SLIDE_W, Inches(0.12), fill=GREEN_DARK)
    add_rect(slide, 0, SLIDE_H - Inches(0.08), SLIDE_W, Inches(0.08), fill=YELLOW_ACCENT)

    # 大号章节号
    add_text(
        slide,
        Inches(0.7), Inches(1.55), Inches(2.2), Inches(1.6),
        [number],
        font_size=110, bold=True, color=GREEN_PALE,
        align=PP_ALIGN.LEFT, anchor=MSO_ANCHOR.MIDDLE,
    )
    # 黄色短线分隔
    add_rect(
        slide,
        Inches(3.0), Inches(2.32), Inches(0.08), Inches(0.95),
        fill=YELLOW_ACCENT,
    )
    # 主标题
    add_text(
        slide,
        Inches(3.3), Inches(2.25), Inches(6.2), Inches(0.7),
        [title_zh],
        font_size=40, bold=True, color=GREEN_DARK,
        align=PP_ALIGN.LEFT, anchor=MSO_ANCHOR.MIDDLE,
    )
    # 英文副标题
    add_text(
        slide,
        Inches(3.3), Inches(2.95), Inches(6.2), Inches(0.4),
        [title_en],
        font_size=18, italic=True, color=GRAY_600,
        align=PP_ALIGN.LEFT, anchor=MSO_ANCHOR.MIDDLE,
    )
    # 底部脚注
    add_text(
        slide,
        Inches(0.7), SLIDE_H - Inches(0.7),
        Inches(8.6), Inches(0.32),
        [footnote],
        font_size=12, italic=True, color=GRAY_700,
        align=PP_ALIGN.LEFT, anchor=MSO_ANCHOR.MIDDLE,
    )


def _new_slide(pres):
    return pres.slides.add_slide(pres.slide_layouts[BLANK_LAYOUT])


# ═════════════════════════════════════════════════════════════
# 章节 01 — 研究背景与意义 (slides 1-3)
# ═════════════════════════════════════════════════════════════


def slide_section_01_divider(pres, pc):
    slide = _new_slide(pres)
    _render_section_divider(
        slide,
        number="01",
        title_zh="研究背景与意义",
        title_en="Background & Motivation",
        footnote="漏洞过载环境下，编译器防御机制如何成为系统安全的坚实后盾？",
    )
    return slide


def slide_01_cve_growth(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "研究背景与意义 — 漏洞过载与防御底座", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "研究背景：漏洞过载环境下，防御机制已成为系统安全的最后防线",
    )

    body_top = y + Inches(0.42)
    body_h = CONTENT_H - Inches(0.5) - Inches(0.3)

    chart_w = Inches(5.85)
    chart_h = Inches(5.85 / 2.295)
    chart_x = CONTENT_PAD_X
    chart_y = body_top + (body_h - chart_h) / 2
    slide.shapes.add_picture(str(IMG_CVE), chart_x, chart_y, width=chart_w, height=chart_h)
    add_text(
        slide,
        chart_x, chart_y - Inches(0.24),
        chart_w, Inches(0.22),
        ["图 1: 近 10 年 CVE 年度发布数"],
        font_size=10, italic=True, color=GRAY_600,
        align=PP_ALIGN.CENTER,
    )

    right_x = CONTENT_PAD_X + chart_w + Inches(0.22)
    right_w = CONTENT_W - chart_w - Inches(0.22)
    card_h = (body_h - Inches(0.18)) / 2

    ay = body_top
    add_rect(slide, right_x, ay, right_w, card_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
    add_rect(slide, right_x, ay, right_w, Inches(0.05), fill=GREEN_DARK)
    add_text(
        slide,
        right_x + Inches(0.15), ay + Inches(0.12),
        right_w - Inches(0.3), Inches(0.3),
        ["防御底座：系统安全的坚实基石"],
        font_size=12, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        right_x + Inches(0.15), ay + Inches(0.5),
        right_w - Inches(0.3), card_h - Inches(0.55),
        [
            {"text": "• 栈金丝雀 · _FORTIFY_SOURCE", "space_after": 3},
            {"text": "• CFI · IBT · PAC · BTI", "space_after": 3},
            {"text": "• Shadow Stack · SafeStack", "space_after": 3},
            {"text": "由编译器与运行时协同部署，旨在漏洞触发时果断切断攻击链。"},
        ],
        font_size=10.5, color=GRAY_800, line_spacing=1.3,
    )

    by = ay + card_h + Inches(0.18)
    add_rect(slide, right_x, by, right_w, card_h, fill=YELLOW_LIGHT, line=YELLOW_ACCENT, line_width=0.75)
    add_rect(slide, right_x, by, right_w, Inches(0.05), fill=YELLOW_ACCENT)
    add_text(
        slide,
        right_x + Inches(0.15), by + Inches(0.12),
        right_w - Inches(0.3), Inches(0.3),
        ["核心命题"],
        font_size=12, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        right_x + Inches(0.15), by + Inches(0.5),
        right_w - Inches(0.3), card_h - Inches(0.55),
        [
            "既然上层漏洞难以根除，防御机制在关键时刻能否“御敌于外”，直接决定了系统的最后防线是否稳固。"
        ],
        font_size=10.5, color=GRAY_800, line_spacing=1.35,
    )

    src_y = SLIDE_H - FOOTER_H - Inches(0.28)
    add_text(
        slide,
        CONTENT_PAD_X, src_y, CONTENT_W, Inches(0.22),
        ["数据来源: NVD · cve.org · JerryGamblin 2024–2025 CVE Data Review"],
        font_size=8.5, italic=True, color=GRAY_500,
        align=PP_ALIGN.LEFT,
    )
    return slide


def slide_02_canary_aarch64(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "研究背景与意义 — 防御机制本身也会出错", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "现实困境：防线本身的脆弱性——Canary 在 AArch64 上的失效",
    )

    body_top = y + Inches(0.42)
    body_h = CONTENT_H - Inches(0.5) - Inches(0.28)

    img_h = body_h
    img_w = int(body_h * 0.804)
    img_x = CONTENT_PAD_X + Inches(0.1)
    img_y = body_top
    slide.shapes.add_picture(str(IMG_STACK), img_x, img_y, width=img_w, height=img_h)
    add_text(
        slide,
        img_x, body_top + body_h + Inches(0.04),
        img_w, Inches(0.22),
        ["图 2: AArch64 栈帧布局 (CVE-2023-4039)"],
        font_size=9.5, italic=True, color=GRAY_600,
        align=PP_ALIGN.CENTER,
    )

    right_x = img_x + img_w + Inches(0.25)
    right_w = CONTENT_W + CONTENT_PAD_X - right_x
    block_gap = Inches(0.18)
    block_h = (body_h - block_gap) / 2

    ay = body_top
    add_rect(slide, right_x, ay, right_w, block_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
    add_rect(slide, right_x, ay, Inches(0.08), block_h, fill=GREEN_DARK)
    add_text(
        slide,
        right_x + Inches(0.2), ay + Inches(0.1),
        right_w - Inches(0.3), Inches(0.3),
        ["Canary 原理"],
        font_size=12, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        right_x + Inches(0.2), ay + Inches(0.46),
        right_w - Inches(0.3), block_h - Inches(0.5),
        [
            {"text": "编译器在缓冲区与返回地址之间放置随机值哨兵。", "space_after": 4},
            {"text": "函数返回前比对哨兵是否被改写, 一旦改写就调用 __stack_chk_fail 终止程序。"},
        ],
        font_size=10.5, color=GRAY_800, line_spacing=1.35,
    )

    by = ay + block_h + block_gap
    add_rect(slide, right_x, by, right_w, block_h, fill=RED_LIGHT, line=RED_DARK, line_width=0.75)
    add_rect(slide, right_x, by, Inches(0.08), block_h, fill=RED_DARK)
    add_text(
        slide,
        right_x + Inches(0.2), by + Inches(0.1),
        right_w - Inches(0.3), Inches(0.3),
        ["AArch64 上失效的根因 (CVE-2023-4039)"],
        font_size=12, bold=True, color=RED_DARK,
    )
    add_text(
        slide,
        right_x + Inches(0.2), by + Inches(0.46),
        right_w - Inches(0.3), block_h - Inches(0.5),
        [
            {
                "runs": [
                    {"text": "GCC 把 canary 固定放在", "size": 10.5},
                    {"text": "局部变量区的最上方", "size": 10.5, "bold": True, "color": RED_DARK},
                    {"text": "。", "size": 10.5},
                ],
                "space_after": 4,
            },
            {
                "runs": [
                    {"text": "但 AArch64 栈帧把 VLA / alloca 放在", "size": 10.5},
                    {"text": "保存的返回地址之下", "size": 10.5, "bold": True, "color": RED_DARK},
                    {"text": ", 越界写入先于 canary 检查改写返回地址, 哨兵被绕开。", "size": 10.5},
                ],
            },
        ],
        color=GRAY_800, line_spacing=1.35,
    )

    src_y = SLIDE_H - FOOTER_H - Inches(0.28)
    add_text(
        slide,
        CONTENT_PAD_X, src_y, CONTENT_W, Inches(0.22),
        [
            "该缺陷潜伏多年：编译无告警、运行无异常，直到 2023 年才由外部研究者偶然发现。"
        ],
        font_size=8.5, italic=True, color=GRAY_500,
        align=PP_ALIGN.LEFT,
    )
    return slide


def slide_03_core_challenge(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "研究背景与意义 — 核心挑战", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "核心挑战：为何现有的检测工具行不通？",
    )

    body_top = y + Inches(0.42)
    ribbon_h = Inches(0.55)
    body_h = CONTENT_H - Inches(0.5) - ribbon_h - Inches(0.18)

    img_w = Inches(5.0)
    img_h = Inches(5.0 / 1.988)
    img_x = CONTENT_PAD_X
    img_y = body_top + (body_h - img_h - Inches(0.26)) / 2
    slide.shapes.add_picture(str(IMG_MATRIX), img_x, img_y, width=img_w, height=img_h)
    add_text(
        slide,
        img_x, img_y + img_h + Inches(0.04),
        img_w, Inches(0.22),
        ["图 3: 防御机制 × ISA 矩阵示意"],
        font_size=9.5, italic=True, color=GRAY_600,
        align=PP_ALIGN.CENTER,
    )

    right_x = CONTENT_PAD_X + img_w + Inches(0.25)
    right_w = CONTENT_W + CONTENT_PAD_X - right_x
    card_gap = Inches(0.18)
    card_h = (body_h - card_gap) / 2

    ay = body_top
    add_rect(slide, right_x, ay, right_w, card_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
    add_rect(slide, right_x, ay, Inches(0.08), card_h, fill=GREEN_DARK)
    add_text(
        slide,
        right_x + Inches(0.2), ay + Inches(0.1),
        right_w - Inches(0.3), Inches(0.3),
        ["C1 · 空间爆炸：机制与架构的无限组合"],
        font_size=12, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        right_x + Inches(0.2), ay + Inches(0.46),
        right_w - Inches(0.3), card_h - Inches(0.5),
        [
            "防御机制多、架构后端杂。面对“机制 × 架构 × 选项”的笛卡尔积，"
            "单纯的人工审计与随机测试已力不从心。"
        ],
        font_size=10.5, color=GRAY_800, line_spacing=1.35,
    )

    by = ay + card_h + card_gap
    add_rect(slide, right_x, by, right_w, card_h, fill=RED_LIGHT, line=RED_DARK, line_width=0.75)
    add_rect(slide, right_x, by, Inches(0.08), card_h, fill=RED_DARK)
    add_text(
        slide,
        right_x + Inches(0.2), by + Inches(0.1),
        right_w - Inches(0.3), Inches(0.3),
        ["C2 · 判定空白：如何察觉“静默”的失效？"],
        font_size=12, bold=True, color=RED_DARK,
    )
    add_text(
        slide,
        right_x + Inches(0.2), by + Inches(0.46),
        right_w - Inches(0.3), card_h - Inches(0.5),
        [
            "失效不报错、运行无异常，但核心安全契约已然失守。"
            "传统的崩溃检测对这类“温水煮青蛙”式的漏洞毫无察觉。"
        ],
        font_size=10.5, color=GRAY_800, line_spacing=1.35,
    )

    rb_y = SLIDE_H - FOOTER_H - ribbon_h - Inches(0.04)
    add_rect(slide, CONTENT_PAD_X, rb_y, CONTENT_W, ribbon_h, fill=YELLOW_LIGHT, line=YELLOW_ACCENT, line_width=0.75)
    add_rect(slide, CONTENT_PAD_X, rb_y, Inches(0.08), ribbon_h, fill=YELLOW_ACCENT)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.05),
        CONTENT_W - Inches(0.3), Inches(0.26),
        ["应对思路"],
        font_size=11, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.3),
        CONTENT_W - Inches(0.3), ribbon_h - Inches(0.32),
        ["以不变量（Invariant）驱动预言机填补检测空白，结合大模型生成高多样性种子，逐格排查“机制 × 架构”矩阵。"],
        font_size=10, color=GRAY_800, line_spacing=1.3,
    )
    return slide


# ═════════════════════════════════════════════════════════════
# 章节 02 — 研究现状 (slide 4)
# ═════════════════════════════════════════════════════════════


def slide_section_02_divider(pres, pc):
    slide = _new_slide(pres)
    _render_section_divider(
        slide,
        number="02",
        title_zh="研究现状",
        title_en="Related Work",
        footnote="静态分析与现有 Fuzz 框架为何在防御机制的静默失效面前集体失灵？",
    )
    return slide


def slide_04_status_methods(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "研究现状 — 现有检测手段的局限", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "研究现状：现有自动化方法为何集体失灵？",
    )

    body_top = y + Inches(0.42)
    ribbon_h = Inches(0.65)
    body_h = CONTENT_H - Inches(0.5) - ribbon_h - Inches(0.18)

    # 左栏: 静态分析 (红框)
    left_w = Inches(3.4)
    left_x = CONTENT_PAD_X
    add_rect(slide, left_x, body_top, left_w, body_h, fill=RED_LIGHT, line=RED_DARK, line_width=0.75)
    add_rect(slide, left_x, body_top, left_w, Inches(0.05), fill=RED_DARK)
    add_text(
        slide,
        left_x + Inches(0.18), body_top + Inches(0.15),
        left_w - Inches(0.36), Inches(0.32),
        ["静态分析：难以应对的多样性"],
        font_size=13, bold=True, color=RED_DARK,
    )
    add_text(
        slide,
        left_x + Inches(0.18), body_top + Inches(0.55),
        left_w - Inches(0.36), body_h - Inches(0.65),
        [
            {"text": "静态分析依赖固定的代码模式。", "space_after": 6},
            {"text": "然而，防御机制的安全契约散落在中端 Pass 与复杂的后端模板中，失效形态千奇百怪，难以实现全自动的统一建模。", "space_after": 6},
        ],
        font_size=10.5, color=GRAY_800, line_spacing=1.4,
    )

    # 右栏: LLM 驱动的 fuzz (上下两个绿框)
    right_x = left_x + left_w + Inches(0.22)
    right_w = CONTENT_W + CONTENT_PAD_X - right_x
    card_gap = Inches(0.15)
    card_h = (body_h - card_gap) / 2

    # 卡 A: WhiteFox
    add_rect(slide, right_x, body_top, right_w, card_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
    add_rect(slide, right_x, body_top, Inches(0.08), card_h, fill=GREEN_DARK)
    add_text(
        slide,
        right_x + Inches(0.2), body_top + Inches(0.1),
        right_w - Inches(0.3), Inches(0.3),
        [
            {
                "runs": [
                    {"text": "WhiteFox", "bold": True, "color": GREEN_DARK, "size": 13},
                    {"text": "  · OSDI'24 · LLM 阅读优化 pass", "italic": True, "color": GRAY_600, "size": 10},
                ]
            }
        ],
    )
    add_text(
        slide,
        right_x + Inches(0.2), body_top + Inches(0.46),
        right_w - Inches(0.3), card_h - Inches(0.5),
        [
            "白盒思路：通过 LLM 归纳优化 Pass 的输入模式。其目标依然是常规的崩溃或误编译，并不关心安全契约是否达成。"
        ],
        font_size=10, color=GRAY_800, line_spacing=1.3,
    )

    # 卡 B: HLPFuzz
    by = body_top + card_h + card_gap
    add_rect(slide, right_x, by, right_w, card_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
    add_rect(slide, right_x, by, Inches(0.08), card_h, fill=GREEN_DARK)
    add_text(
        slide,
        right_x + Inches(0.2), by + Inches(0.1),
        right_w - Inches(0.3), Inches(0.3),
        [
            {
                "runs": [
                    {"text": "HLPFuzz", "bold": True, "color": GREEN_DARK, "size": 13},
                    {"text": "  · USENIX Sec'25 · LLM 约束求解 + 覆盖率", "italic": True, "color": GRAY_600, "size": 10},
                ]
            }
        ],
    )
    add_text(
        slide,
        right_x + Inches(0.2), by + Inches(0.46),
        right_w - Inches(0.3), card_h - Inches(0.5),
        [
            "渐进式构造：将未覆盖路径翻译为 LLM 的约束求解任务。核心目标是路径可达性，判定标准依然局限于崩溃检测。"
        ],
        font_size=10, color=GRAY_800, line_spacing=1.3,
    )

    # 底部 ribbon: oracle 空白
    rb_y = SLIDE_H - FOOTER_H - ribbon_h - Inches(0.04)
    add_rect(slide, CONTENT_PAD_X, rb_y, CONTENT_W, ribbon_h, fill=YELLOW_LIGHT, line=YELLOW_ACCENT, line_width=0.75)
    add_rect(slide, CONTENT_PAD_X, rb_y, Inches(0.08), ribbon_h, fill=YELLOW_ACCENT)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.05),
        CONTENT_W - Inches(0.3), Inches(0.28),
        ["Oracle 空白"],
        font_size=11, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.32),
        CONTENT_W - Inches(0.3), ribbon_h - Inches(0.34),
        [
            "程序无崩溃、跨编译器输出一致，安全契约却已失守。现有的崩溃、差分和变形预言机在这类缺陷面前全部沉默。"
        ],
        font_size=10, color=GRAY_800, line_spacing=1.3,
    )
    return slide


# ═════════════════════════════════════════════════════════════
# 章节 03 — 研究目标与内容 (slides 5-6)
# ═════════════════════════════════════════════════════════════


def slide_section_03_divider(pres, pc):
    slide = _new_slide(pres)
    _render_section_divider(
        slide,
        number="03",
        title_zh="研究目标与内容",
        title_en="Goals & Content",
        footnote="核心目标：精准捕获“静默失效”；研究内容：构建从不变量到预言机的自动化检测链路。",
    )
    return slide


def slide_05_goals(pres, pc):
    """研究目标 (a/b/c) + 顶部 CVE-2023-4039 锚点 narrative (融合故事线)."""
    slide = _new_slide(pres)
    add_page_frame(slide, "研究目标与内容 — 研究目标", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "研究目标：填补防御机制的检测空白，捕获真实静默失效",
    )

    # ── 顶部锚点 / framing 卡 (融合故事线: 双重无声 + CVE-2023-4039 现实参考)
    intro_y = y + Inches(0.45)
    intro_h = Inches(0.85)
    add_rect(slide, CONTENT_PAD_X, intro_y, CONTENT_W, intro_h, fill=YELLOW_LIGHT, line=YELLOW_ACCENT, line_width=0.75)
    add_rect(slide, CONTENT_PAD_X, intro_y, Inches(0.08), intro_h, fill=YELLOW_ACCENT)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), intro_y + Inches(0.08),
        CONTENT_W - Inches(0.3), Inches(0.3),
        [
            {
                "runs": [
                    {"text": "目标参照: ", "bold": True, "color": GREEN_DARK, "size": 11.5},
                    {"text": "CVE-2023-4039", "bold": True, "color": RED_DARK, "size": 11.5},
                    {"text": "  ·  AArch64 上 ", "color": GRAY_700, "size": 11.5, "italic": True},
                    {"text": "-fstack-protector", "color": GRAY_700, "size": 11.5, "italic": True},
                    {"text": " 形同虚设, 编译时无错、运行时正常, 但 canary 已被绕开", "color": GRAY_700, "size": 11.5, "italic": True},
                ]
            }
        ],
    )
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), intro_y + Inches(0.42),
        CONTENT_W - Inches(0.3), intro_h - Inches(0.46),
        [
            "我们以“安全契约”为新锚点，替代传统的崩溃判据。通过在“机制 × 架构”矩阵上的系统性排查，将此类“双重无声”的失效彻底暴露。"
        ],
        font_size=10.5, color=GRAY_800, line_spacing=1.3,
    )

    # ── 三大目标卡 (a/b/c)
    body_top = intro_y + intro_h + Inches(0.18)
    body_h = SLIDE_H - FOOTER_H - body_top - Inches(0.1)
    gap = Inches(0.18)
    card_w = (CONTENT_W - gap * 2) / 3

    goals = [
        {
            "tag": "G (a)",
            "title": "安全不变量集合",
            "bullets": [
                "覆盖主流防御机制 (canary · FORTIFY · IBT 等)",
                "每条不变量给出可机器判定的证据形式",
                "GCC 与 LLVM 共用同一描述结构",
            ],
        },
        {
            "tag": "G (b)",
            "title": "Fuzzer 原型",
            "bullets": [
                "同时面向 GCC 与 LLVM",
                "跨 ISA: x86-64 · AArch64 · RISC-V · LoongArch",
                "防御机制相关代码覆盖率为推进信号",
                "由 LLM 完成精细变异",
            ],
        },
        {
            "tag": "G (c)",
            "title": "真实缺陷验证",
            "bullets": [
                "发现 CVE-2023-4039 类的静默失效",
                "推动上游确认与回归测试合入",
                "验证方法的跨编译器迁移性",
            ],
        },
    ]
    cx = CONTENT_PAD_X
    for g in goals:
        add_rect(slide, cx, body_top, card_w, body_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
        add_rect(slide, cx, body_top, card_w, Inches(0.05), fill=GREEN_DARK)
        add_text(
            slide,
            cx + Inches(0.18), body_top + Inches(0.13),
            card_w - Inches(0.36), Inches(0.26),
            [g["tag"]],
            font_size=11, bold=True, italic=True, color=YELLOW_ACCENT,
        )
        add_text(
            slide,
            cx + Inches(0.18), body_top + Inches(0.4),
            card_w - Inches(0.36), Inches(0.38),
            [g["title"]],
            font_size=15, bold=True, color=GREEN_DARK,
        )
        bullets = [{"text": "• " + b, "space_after": 5} for b in g["bullets"]]
        add_text(
            slide,
            cx + Inches(0.2), body_top + Inches(0.85),
            card_w - Inches(0.4), body_h - Inches(0.95),
            bullets,
            font_size=10.5, color=GRAY_800, line_spacing=1.3,
        )
        cx += card_w + gap
    return slide


def slide_06_content(pres, pc):
    """研究内容: 复用 pipeline 图 (上半) + 三项内容详细卡 (下半)."""
    slide = _new_slide(pres)
    add_page_frame(slide, "研究目标与内容 — 研究内容", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "研究内容：从安全契约形式化到自动化测试引擎",
    )

    body_top = y + Inches(0.45)

    # ── 上半: pipeline 图 (复用报告图)
    img_w = Inches(8.4)
    img_h = Inches(8.4 / 4.357)
    img_x = CONTENT_PAD_X + (CONTENT_W - img_w) / 2
    img_y = body_top
    slide.shapes.add_picture(str(IMG_PIPELINE), img_x, img_y, width=img_w, height=img_h)
    add_text(
        slide,
        img_x, img_y + img_h + Inches(0.02),
        img_w, Inches(0.2),
        ["图 4: 研究内容总览 — 不变量为基础, 预言机给判据, LLM × 覆盖率把测试推进到机制代码路径"],
        font_size=9, italic=True, color=GRAY_600,
        align=PP_ALIGN.CENTER,
    )

    # ── 下半: 三项内容详细卡
    cards_top = img_y + img_h + Inches(0.32)
    cards_h = SLIDE_H - FOOTER_H - cards_top - Inches(0.1)
    gap = Inches(0.18)
    card_w = (CONTENT_W - gap * 2) / 3

    contents = [
        {
            "n": "①",
            "title": "安全不变量调研与形式化",
            "color_fill": GREEN_LIGHT,
            "color_border": GREEN_DARK,
            "color_title": GREEN_DARK,
            "bullets": [
                "三类来源: 源码注释 · 官方文档 · 历史 bug",
                "统一字段: 陈述 · 证据 · 版本敏感性",
            ],
        },
        {
            "n": "②",
            "title": "不变量到预言机的工程化",
            "color_fill": BLUE_LIGHT,
            "color_border": BLUE_DARK,
            "color_title": BLUE_DARK,
            "bullets": [
                "静态: ELF 符号 · 指令字节 · .note",
                "动态: 受控执行 · 二分搜索",
                "任一 Fail 即记机制级违例 + 开/关对照",
            ],
        },
        {
            "n": "③",
            "title": "LLM × 覆盖率主循环",
            "color_fill": YELLOW_LIGHT,
            "color_border": YELLOW_ACCENT,
            "color_title": GREEN_DARK,
            "bullets": [
                "按「未覆盖 × 机制相关性」挑目标基本块",
                "机制契约 + 目标上下文 → LLM 生成种子",
                "新覆盖但未命中: 定位发散点回喂模型",
            ],
        },
    ]
    cx = CONTENT_PAD_X
    for c in contents:
        add_rect(slide, cx, cards_top, card_w, cards_h, fill=c["color_fill"], line=c["color_border"], line_width=0.75)
        add_rect(slide, cx, cards_top, card_w, Inches(0.05), fill=c["color_border"])
        add_text(
            slide,
            cx + Inches(0.16), cards_top + Inches(0.12),
            Inches(0.4), Inches(0.4),
            [c["n"]],
            font_size=20, bold=True, color=c["color_title"], anchor=MSO_ANCHOR.MIDDLE,
        )
        add_text(
            slide,
            cx + Inches(0.58), cards_top + Inches(0.16),
            card_w - Inches(0.7), Inches(0.36),
            [c["title"]],
            font_size=12, bold=True, color=c["color_title"], line_spacing=1.1,
        )
        bullets = [{"text": "• " + b, "space_after": 4} for b in c["bullets"]]
        add_text(
            slide,
            cx + Inches(0.18), cards_top + Inches(0.62),
            card_w - Inches(0.36), cards_h - Inches(0.7),
            bullets,
            font_size=9.5, color=GRAY_800, line_spacing=1.3,
        )
        cx += card_w + gap
    return slide


# ═════════════════════════════════════════════════════════════
# 章节 04 — 研究方案 (slides 8-10)
# ═════════════════════════════════════════════════════════════


def slide_section_04_divider(pres, pc):
    slide = _new_slide(pres)
    _render_section_divider(
        slide,
        number="04",
        title_zh="研究方案",
        title_en="Technical Route",
        footnote="从总体架构到核心算法：构建基于安全契约的自动化检测系统。",
    )
    return slide


def slide_08_method_arch(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "研究方案 — 总体架构", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "总体架构：DeFuzz 的模块化设计与协同逻辑",
    )

    body_top = y + Inches(0.48)
    # 图比例 2.064: 宽景; 缩小以留出底部说明 ribbon 空间
    img_w = Inches(5.6)
    img_h = Inches(5.6 / 2.064)
    img_x = CONTENT_PAD_X + (CONTENT_W - img_w) / 2
    img_y = body_top
    slide.shapes.add_picture(str(IMG_ARCH), img_x, img_y, width=img_w, height=img_h)
    add_text(
        slide,
        img_x, img_y + img_h + Inches(0.04),
        img_w, Inches(0.22),
        ["图 5: 总体架构 — 子系统分工"],
        font_size=9.5, italic=True, color=GRAY_600,
        align=PP_ALIGN.CENTER,
    )

    # 底部说明 ribbon
    desc_y = img_y + img_h + Inches(0.34)
    desc_h = SLIDE_H - FOOTER_H - desc_y - Inches(0.08)
    add_rect(slide, CONTENT_PAD_X, desc_y, CONTENT_W, desc_h, fill=GRAY_100, line=GRAY_300, line_width=0.5)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.18), desc_y + Inches(0.06),
        CONTENT_W - Inches(0.36), desc_h - Inches(0.12),
        [
            {"text": "• Fuzz 引擎: 主循环调度 + 提示词组装", "space_after": 2},
            {"text": "• 编译器子系统: 封装插桩 GCC / LLVM, 统一「行级覆盖 → 种子 ID」映射", "space_after": 2},
            {"text": "• 预言机子系统: 按机制聚合检查器, 输出安全判定; 配置模块按「机制 · ISA · flag」三元组下发任务"},
        ],
        font_size=10, color=GRAY_800, line_spacing=1.25,
    )
    return slide


def slide_09_method_oracle(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "研究方案 — 预言机框架", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "预言机框架：多维度不变量判定逻辑",
    )

    body_top = y + Inches(0.42)
    ribbon_h = Inches(0.5)
    body_h = CONTENT_H - Inches(0.5) - ribbon_h - Inches(0.16)

    # 左侧 oracle-flow 图 (比例 0.763 portrait)
    img_h = body_h
    img_w = int(body_h * 0.763)
    img_x = CONTENT_PAD_X + Inches(0.2)
    img_y = body_top
    slide.shapes.add_picture(str(IMG_ORACLE), img_x, img_y, width=img_w, height=img_h)
    add_text(
        slide,
        img_x, body_top + body_h + Inches(0.02),
        img_w, Inches(0.22),
        ["图 6: 预言机判定流程"],
        font_size=9.5, italic=True, color=GRAY_600,
        align=PP_ALIGN.CENTER,
    )

    # 右侧: 两类 checker 卡片 (上蓝 / 下绿)
    right_x = img_x + img_w + Inches(0.3)
    right_w = CONTENT_W + CONTENT_PAD_X - right_x
    card_gap = Inches(0.18)
    card_h = (body_h - card_gap) / 2

    # 静态检查器
    add_rect(slide, right_x, body_top, right_w, card_h, fill=BLUE_LIGHT, line=BLUE_DARK, line_width=0.75)
    add_rect(slide, right_x, body_top, Inches(0.08), card_h, fill=BLUE_DARK)
    add_text(
        slide,
        right_x + Inches(0.2), body_top + Inches(0.1),
        right_w - Inches(0.3), Inches(0.3),
        ["静态检查器"],
        font_size=13, bold=True, color=BLUE_DARK,
    )
    add_text(
        slide,
        right_x + Inches(0.2), body_top + Inches(0.5),
        right_w - Inches(0.3), card_h - Inches(0.55),
        [
            {"text": "• ELF 符号表断言 (如 __stack_chk_fail 是否被引用)", "space_after": 4},
            {"text": "• 指令字节模式扫描 (如 endbr 出现位置)", "space_after": 4},
            {"text": "• .note 段属性核对 (CET / BTI 标志位)"},
        ],
        font_size=10.5, color=GRAY_800, line_spacing=1.3,
    )

    # 动态检查器
    dy = body_top + card_h + card_gap
    add_rect(slide, right_x, dy, right_w, card_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
    add_rect(slide, right_x, dy, Inches(0.08), card_h, fill=GREEN_DARK)
    add_text(
        slide,
        right_x + Inches(0.2), dy + Inches(0.1),
        right_w - Inches(0.3), Inches(0.3),
        ["动态检查器"],
        font_size=13, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        right_x + Inches(0.2), dy + Inches(0.5),
        right_w - Inches(0.3), card_h - Inches(0.55),
        [
            {"text": "• 受控执行 + 二分搜索, 观察越界写入是否被拦截", "space_after": 4},
            {"text": "• 内联汇编探针, 检测寄存器残留 (canary 泄漏判据)", "space_after": 4},
            {"text": "• QEMU-user 跨架构跑同一颗种子做对照"},
        ],
        font_size=10.5, color=GRAY_800, line_spacing=1.3,
    )

    # 底部 ribbon: 判定语义 + 对照
    rb_y = SLIDE_H - FOOTER_H - ribbon_h - Inches(0.04)
    add_rect(slide, CONTENT_PAD_X, rb_y, CONTENT_W, ribbon_h, fill=YELLOW_LIGHT, line=YELLOW_ACCENT, line_width=0.75)
    add_rect(slide, CONTENT_PAD_X, rb_y, Inches(0.08), ribbon_h, fill=YELLOW_ACCENT)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.05),
        CONTENT_W - Inches(0.3), Inches(0.26),
        ["聚合判定与对照实验"],
        font_size=11, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.28),
        CONTENT_W - Inches(0.3), ribbon_h - Inches(0.3),
        ["检查器返回 Pass / Fail / Error。任何一处 Fail 均被视为违反安全契约并保留现场证据；通过双 flag profile 对照剔除缺省行为干扰。"],
        font_size=10, color=GRAY_800, line_spacing=1.3,
    )
    return slide


def slide_10_method_loop(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "研究方案 — LLM × 覆盖率主循环 + 跨架构调度", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "主循环：LLM 与覆盖率反馈的深度联合",
    )

    body_top = y + Inches(0.5)
    # 上半 3 个流程方框 (自绘, 不用 main-loop 图)
    flow_h = Inches(1.6)
    box_h = Inches(1.2)
    arrow_w = Inches(0.38)
    arrow_h = Inches(0.34)
    box_w = (CONTENT_W - arrow_w * 2) / 3
    bx = CONTENT_PAD_X
    by = body_top + (flow_h - box_h) / 2

    steps = [
        {
            "n": "1",
            "title": "选目标基本块",
            "sub": "CFG + 防御机制相关性",
            "detail": "未覆盖 × 与防御逻辑关联高的基本块",
        },
        {
            "n": "2",
            "title": "LLM 变异",
            "sub": "机制契约 + 目标上下文",
            "detail": "目标函数源码 · 覆盖标注 · 示例种子",
        },
        {
            "n": "3",
            "title": "预言机 + 覆盖率反馈",
            "sub": "新覆盖 / 命中目标 / 触发 bug",
            "detail": "任一为真即保存; 否则定位发散点回喂",
        },
    ]
    for i, s in enumerate(steps):
        add_rect(slide, bx, by, box_w, box_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
        add_rect(slide, bx, by, box_w, Inches(0.05), fill=GREEN_DARK)
        # 圆形编号
        circle_d = Inches(0.46)
        add_rect(
            slide, bx + Inches(0.15), by + Inches(0.14),
            circle_d, circle_d,
            fill=GREEN_DARK, shape=MSO_SHAPE.OVAL,
        )
        add_text(
            slide,
            bx + Inches(0.15), by + Inches(0.14),
            circle_d, circle_d,
            [s["n"]],
            font_size=16, bold=True, color=WHITE,
            align=PP_ALIGN.CENTER, anchor=MSO_ANCHOR.MIDDLE,
        )
        add_text(
            slide,
            bx + Inches(0.72), by + Inches(0.15),
            box_w - Inches(0.85), Inches(0.3),
            [s["title"]],
            font_size=13, bold=True, color=GREEN_DARK,
        )
        add_text(
            slide,
            bx + Inches(0.72), by + Inches(0.45),
            box_w - Inches(0.85), Inches(0.24),
            [s["sub"]],
            font_size=9.5, italic=True, color=GRAY_600,
        )
        add_text(
            slide,
            bx + Inches(0.2), by + Inches(0.76),
            box_w - Inches(0.4), box_h - Inches(0.82),
            [s["detail"]],
            font_size=10, color=GRAY_800, line_spacing=1.3,
        )
        if i < 2:
            add_arrow(
                slide,
                bx + box_w,
                by + (box_h - arrow_h) / 2,
                arrow_w, arrow_h,
                fill=GREEN_DARK,
            )
        bx += box_w + arrow_w

    # 下半: 跨架构调度 ribbon
    rb_y = body_top + flow_h + Inches(0.25)
    rb_h = SLIDE_H - FOOTER_H - rb_y - Inches(0.12)
    add_rect(slide, CONTENT_PAD_X, rb_y, CONTENT_W, rb_h, fill=YELLOW_LIGHT, line=YELLOW_ACCENT, line_width=0.75)
    add_rect(slide, CONTENT_PAD_X, rb_y, Inches(0.08), rb_h, fill=YELLOW_ACCENT)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.08),
        CONTENT_W - Inches(0.3), Inches(0.3),
        ["跨架构与编译选项调度"],
        font_size=12, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.4),
        CONTENT_W - Inches(0.3), rb_h - Inches(0.45),
        [
            {"text": "• 配置模块按「机制 · ISA · flag」三元组下发任务; 同一颗种子分发至 x86-64 / AArch64 / RISC-V / LoongArch 后端经 QEMU 执行。", "space_after": 3},
            {"text": "• 开/关机制双 flag profile 对照, 把检查器输出与编译器缺省行为区分开。"},
        ],
        font_size=10, color=GRAY_800, line_spacing=1.3,
    )
    return slide


# ═════════════════════════════════════════════════════════════
# 章节 05 — 现有成果 (slides 11-13)
# ═════════════════════════════════════════════════════════════


def slide_section_05_divider(pres, pc):
    slide = _new_slide(pres)
    _render_section_divider(
        slide,
        number="05",
        title_zh="现有成果",
        title_en="Preliminary Results",
        footnote="初步成果：已在 GCC 多个架构与版本中捕获 3 例静默失效缺陷。",
    )
    return slide


def slide_11_results_overview(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "现有成果 — GCC 上的三例静默失效", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "实验成果：捕获多例真实静默失效缺陷",
    )

    body_top = y + Inches(0.5)
    foot_h = Inches(0.42)
    body_h = CONTENT_H - Inches(0.55) - foot_h - Inches(0.1)

    gap = Inches(0.18)
    card_w = (CONTENT_W - gap * 2) / 3

    bugs = [
        {
            "tag": "Bug 1",
            "title": "_FORTIFY_SOURCE size 静默放宽",
            "where": "tree-ssa-strlen pass",
            "pr": "PR-125079",
            "status": "✓ 已合入 master",
            "status_color": GREEN_DARK,
            "desc": "strlen pass 把 __strcat_chk 改写为 __stpcpy_chk 时, 第三个 size 参数没有按 strlen(dst) 减小, 运行时上限被悄悄放宽。",
        },
        {
            "tag": "Bug 2",
            "title": "IBT endbr 字节模式漏检",
            "where": "x86 后端 endbr 谓词",
            "pr": "PR-125084",
            "status": "⏳ 等待维护者回复",
            "status_color": "orange",
            "desc": "扫描循环在「立即数高位非零」时提前退出, endbr64 字节排布在高位即可绕过检测, .text 中混入伪 landing pad。",
        },
        {
            "tag": "Bug 3",
            "title": "多 ISA canary 寄存器残留",
            "where": "通用 SSP fallback 后端",
            "pr": "PR-125045 (meta) · PR-125049",
            "status": "✓ LoongArch 子例已合入",
            "status_color": GREEN_DARK,
            "desc": "MIPS / LoongArch 等 ISA 的 fallback 后端在 epilogue 未清零 canary 寄存器, __stack_chk_guard 在返回时残留。",
        },
    ]
    cx = CONTENT_PAD_X
    for b in bugs:
        add_rect(slide, cx, body_top, card_w, body_h, fill=RED_LIGHT, line=RED_DARK, line_width=0.75)
        add_rect(slide, cx, body_top, card_w, Inches(0.05), fill=RED_DARK)
        # 标签 (Bug N)
        add_text(
            slide,
            cx + Inches(0.18), body_top + Inches(0.14),
            card_w - Inches(0.36), Inches(0.26),
            [b["tag"]],
            font_size=11, bold=True, italic=True, color=YELLOW_ACCENT,
        )
        # 标题
        add_text(
            slide,
            cx + Inches(0.18), body_top + Inches(0.42),
            card_w - Inches(0.36), Inches(0.6),
            [b["title"]],
            font_size=13, bold=True, color=GRAY_800, line_spacing=1.1,
        )
        # where
        add_text(
            slide,
            cx + Inches(0.18), body_top + Inches(1.0),
            card_w - Inches(0.36), Inches(0.24),
            [
                {
                    "runs": [
                        {"text": "位置: ", "italic": True, "color": GRAY_600, "size": 9.5},
                        {"text": b["where"], "color": GRAY_800, "size": 9.5},
                    ]
                }
            ],
        )
        # PR
        add_text(
            slide,
            cx + Inches(0.18), body_top + Inches(1.24),
            card_w - Inches(0.36), Inches(0.24),
            [
                {
                    "runs": [
                        {"text": "上游: ", "italic": True, "color": GRAY_600, "size": 9.5},
                        {"text": b["pr"], "color": GRAY_800, "size": 9.5, "bold": True},
                    ]
                }
            ],
        )
        # 描述
        add_text(
            slide,
            cx + Inches(0.18), body_top + Inches(1.55),
            card_w - Inches(0.36), body_h - Inches(2.05),
            [b["desc"]],
            font_size=10, color=GRAY_800, line_spacing=1.3,
        )
        # 状态条 (底部一行)
        status_y = body_top + body_h - Inches(0.4)
        from theme import ORANGE_DARK  # local import to keep top-level imports tidy
        status_color = b["status_color"] if b["status_color"] != "orange" else ORANGE_DARK
        add_rect(slide, cx, status_y, card_w, Inches(0.4), fill=WHITE, line=status_color, line_width=0.5)
        add_text(
            slide,
            cx + Inches(0.12), status_y, card_w - Inches(0.24), Inches(0.4),
            [b["status"]],
            font_size=10.5, bold=True, color=status_color,
            anchor=MSO_ANCHOR.MIDDLE,
        )
        cx += card_w + gap

    # 底部小字: 实验范围
    foot_y = SLIDE_H - FOOTER_H - foot_h - Inches(0.02)
    add_text(
        slide,
        CONTENT_PAD_X, foot_y, CONTENT_W, foot_h,
        [
            "实验范围: GCC × {aarch64-v12.2 · aarch64-v15.2 · x86_64-v15.2 · loongarch64-v15.2}; "
            "LLVM 接入与其他防御机制 (SafeStack / PAC / BTI / Shadow Stack) 在下一阶段展开。"
        ],
        font_size=9, italic=True, color=GRAY_600, line_spacing=1.25,
        anchor=MSO_ANCHOR.MIDDLE,
    )
    return slide


def slide_12_canary_principle(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "现有成果 — Canary 寄存器残留: 原理与历史教训", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "案例分析：为何 Canary 寄存器残留必须被视为漏洞？",
    )

    body_top = y + Inches(0.5)
    foot_h = Inches(0.5)
    body_h = CONTENT_H - Inches(0.55) - foot_h - Inches(0.1)

    gap = Inches(0.22)
    col_w = (CONTENT_W - gap) / 2

    # 左: Canary 原理
    add_rect(slide, CONTENT_PAD_X, body_top, col_w, body_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
    add_rect(slide, CONTENT_PAD_X, body_top, col_w, Inches(0.05), fill=GREEN_DARK)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.18), body_top + Inches(0.14),
        col_w - Inches(0.36), Inches(0.34),
        ["Canary 寄存器为何必须清零"],
        font_size=13, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), body_top + Inches(0.55),
        col_w - Inches(0.4), body_h - Inches(0.65),
        [
            {
                "runs": [
                    {"text": "① Canary 值来自 ", "size": 10.5, "color": GRAY_800},
                    {"text": "__stack_chk_guard", "size": 10.5, "color": GREEN_DARK, "bold": True},
                    {"text": ", 同一进程或线程内多次复用。一旦攻击者拿到这个值, 后续金丝雀检查全部形同失效。", "size": 10.5, "color": GRAY_800},
                ],
                "space_after": 8,
            },
            {
                "runs": [
                    {"text": "② Canary 必须频繁进入寄存器: 函数序言中加载、对照前比较、写存到栈帧。这些操作都会让 canary 值短暂出现在通用寄存器里。", "size": 10.5, "color": GRAY_800},
                ],
                "space_after": 8,
            },
            {
                "runs": [
                    {"text": "③ 因此安全规范要求: ", "size": 10.5, "color": GRAY_800},
                    {"text": "存储过 canary 的寄存器, 函数返回前必须显式清零", "size": 10.5, "color": GREEN_DARK, "bold": True},
                    {"text": "; 否则 canary 值会随返回值泄漏给调用者乃至攻击者。", "size": 10.5, "color": GRAY_800},
                ],
            },
        ],
        line_spacing=1.35,
    )

    # 右: 历史教训
    rx = CONTENT_PAD_X + col_w + gap
    add_rect(slide, rx, body_top, col_w, body_h, fill=YELLOW_LIGHT, line=YELLOW_ACCENT, line_width=0.75)
    add_rect(slide, rx, body_top, col_w, Inches(0.05), fill=YELLOW_ACCENT)
    add_text(
        slide,
        rx + Inches(0.18), body_top + Inches(0.14),
        col_w - Inches(0.36), Inches(0.34),
        ["历史教训: ARM 已经踩过这个坑"],
        font_size=13, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        rx + Inches(0.2), body_top + Inches(0.55),
        col_w - Inches(0.4), body_h - Inches(0.65),
        [
            {
                "runs": [
                    {"text": "2020 年 ", "size": 10.5, "color": GRAY_800},
                    {"text": "PR-96191", "size": 10.5, "color": GREEN_DARK, "bold": True},
                    {"text": " 修复了 ARM / AArch64 的 canary 寄存器残留, 在 epilogue 中显式清零承载 canary 的寄存器。", "size": 10.5, "color": GRAY_800},
                ],
                "space_after": 10,
            },
            {
                "runs": [
                    {"text": "但 GCC 的通用 SSP fallback 后端并未被同步修复, MIPS / LoongArch / xtensa / csky 等 ISA 走的是 fallback 路径。", "size": 10.5, "color": GRAY_800},
                ],
                "space_after": 10,
            },
            {
                "runs": [
                    {"text": "这条同源遗漏在 5 年内无人察觉 — 因为程序不崩溃、二进制看起来仍带 canary, 但 ", "size": 10.5, "color": GRAY_800},
                    {"text": "guard 值随时可能从寄存器漏出来", "size": 10.5, "color": RED_DARK, "bold": True},
                    {"text": "。", "size": 10.5, "color": GRAY_800},
                ],
            },
        ],
        line_spacing=1.35,
    )

    # 底部 ribbon
    rb_y = SLIDE_H - FOOTER_H - foot_h - Inches(0.04)
    add_rect(slide, CONTENT_PAD_X, rb_y, CONTENT_W, foot_h, fill=GRAY_100, line=GRAY_300, line_width=0.5)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.18), rb_y + Inches(0.06),
        CONTENT_W - Inches(0.36), foot_h - Inches(0.12),
        [
            {
                "runs": [
                    {"text": "我们的做法: ", "bold": True, "color": GREEN_DARK, "size": 11},
                    {"text": "把「epilogue 必须 scrub canary 寄存器」这条规则形式化为一条动态不变量, 作为预言机检查器的判据 — 详见下页。", "color": GRAY_800, "size": 11},
                ]
            }
        ],
    )
    return slide


def slide_13_canary_checker(pres, pc):
    slide = _new_slide(pres)
    add_page_frame(slide, "现有成果 — 用内联汇编 checker 触发多 ISA 泄漏", page_no=pc.next())

    y = CONTENT_TOP
    add_section_title(
        slide, CONTENT_PAD_X, y, CONTENT_W,
        "实证发现：LoongArch 等架构上存在的严重安全隐患",
    )

    body_top = y + Inches(0.48)
    foot_h = Inches(0.72)
    body_h = CONTENT_H - Inches(0.53) - foot_h - Inches(0.1)

    gap = Inches(0.22)
    left_w = Inches(4.5)
    right_w = CONTENT_W - left_w - gap

    # 左: checker 设计 + 短例
    add_rect(slide, CONTENT_PAD_X, body_top, left_w, body_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
    add_rect(slide, CONTENT_PAD_X, body_top, left_w, Inches(0.05), fill=GREEN_DARK)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.18), body_top + Inches(0.14),
        left_w - Inches(0.36), Inches(0.34),
        ["动态 checker 设计"],
        font_size=13, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), body_top + Inches(0.55),
        left_w - Inches(0.4), Inches(1.1),
        [
            {
                "runs": [
                    {"text": "在受测函数返回前插入内联汇编探针, 把候选寄存器的当前值与 ", "size": 10.5, "color": GRAY_800},
                    {"text": "__stack_chk_guard", "size": 10.5, "color": GREEN_DARK, "bold": True},
                    {"text": " 比较; 命中即判定为 ", "size": 10.5, "color": GRAY_800},
                    {"text": "Fail (canary 泄漏)", "size": 10.5, "color": RED_DARK, "bold": True},
                    {"text": "。", "size": 10.5, "color": GRAY_800},
                ],
            }
        ],
        line_spacing=1.3,
    )
    # 代码示例 (LoongArch)
    code_y = body_top + Inches(1.78)
    code_h = body_h - Inches(1.85)
    add_rect(slide, CONTENT_PAD_X + Inches(0.18), code_y, left_w - Inches(0.36), code_h, fill=GRAY_100, line=GRAY_300, line_width=0.5)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.28), code_y + Inches(0.06),
        left_w - Inches(0.56), Inches(0.24),
        ["LoongArch 探针示例 (PR-125049)"],
        font_size=9, italic=True, color=GRAY_600,
    )
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.28), code_y + Inches(0.3),
        left_w - Inches(0.56), code_h - Inches(0.36),
        [
            {"text": "asm volatile (", "space_after": 0},
            {"text": "    \"la.local $t1, __stack_chk_guard\\n\"", "space_after": 0},
            {"text": "    \"ld.d   $t1, $t1, 0\\n\"", "space_after": 0},
            {"text": "    \"beq    $t1, $r12, leaked\\n\"", "space_after": 0},
            {"text": ");  // 返回前若 $r12 还存着 guard 值 → 泄漏", "space_after": 0},
        ],
        font_size=8.5, color=GRAY_800,
        font_name="Courier New", ea_name="Microsoft YaHei",
        line_spacing=1.25,
    )

    # 右: 截图
    rx = CONTENT_PAD_X + left_w + gap
    # 图比例 1.715, 限定宽度 right_w
    img_w = right_w
    img_h = int(right_w * (1.0 / 1.715))
    if img_h > body_h - Inches(0.35):
        img_h = body_h - Inches(0.35)
        img_w = int(img_h * 1.715)
    img_x = rx + (right_w - img_w) / 2
    img_y = body_top + (body_h - img_h - Inches(0.28)) / 2
    slide.shapes.add_picture(str(IMG_GCC_DEV), img_x, img_y, width=img_w, height=img_h)
    add_text(
        slide,
        rx, img_y + img_h + Inches(0.04),
        right_w, Inches(0.24),
        ["图 7: LoongArch 维护者邮件 (PR-125049 patch review)"],
        font_size=9.5, italic=True, color=GRAY_600,
        align=PP_ALIGN.CENTER,
    )

    # 底部 ribbon: 后续
    rb_y = SLIDE_H - FOOTER_H - foot_h - Inches(0.04)
    add_rect(slide, CONTENT_PAD_X, rb_y, CONTENT_W, foot_h, fill=GREEN_LIGHT, line=GREEN_DARK, line_width=0.75)
    add_rect(slide, CONTENT_PAD_X, rb_y, Inches(0.08), foot_h, fill=GREEN_DARK)
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.06),
        CONTENT_W - Inches(0.3), Inches(0.28),
        ["上游反馈"],
        font_size=11, bold=True, color=GREEN_DARK,
    )
    add_text(
        slide,
        CONTENT_PAD_X + Inches(0.2), rb_y + Inches(0.32),
        CONTENT_W - Inches(0.3), foot_h - Inches(0.35),
        [
            {
                "runs": [
                    {"text": "LoongArch 维护者 Xi Ruoyao 将其确认为 ", "color": GRAY_800, "size": 10},
                    {"text": "\"severe security vulnerability\"", "bold": True, "color": RED_DARK, "size": 10},
                    {"text": ", 决定把修复 backport 至旧版本 GCC; meta-bug PR-125045 跟踪的其他 ISA 子例正在依次上报。", "color": GRAY_800, "size": 10},
                ]
            }
        ],
        line_spacing=1.3,
    )
    return slide


# ═════════════════════════════════════════════════════════════
# Deck slide 顺序 — 自动维护页码
# ═════════════════════════════════════════════════════════════
ALL_SLIDES = [
    # 章节 01
    slide_section_01_divider,
    slide_01_cve_growth,
    slide_02_canary_aarch64,
    slide_03_core_challenge,
    # 章节 02
    slide_section_02_divider,
    slide_04_status_methods,
    # 章节 03
    slide_section_03_divider,
    slide_05_goals,
    slide_06_content,
    # 章节 04
    slide_section_04_divider,
    slide_08_method_arch,
    slide_09_method_oracle,
    slide_10_method_loop,
    # 章节 05
    slide_section_05_divider,
    slide_11_results_overview,
    slide_12_canary_principle,
    slide_13_canary_checker,
]
