"""绘制近 10 年 (2016–2025) CVE 年度发布数折线图.

数据来源 (按年份):
- 2016–2023: NVD / cve.org 公开统计 (cvedetails / cve.icu 汇总值, 与 NVD 一致).
- 2024: JerryGamblin 2024 CVE Data Review (40,009).
- 2025: JerryGamblin 2025 CVE Data Review (48,185).

风格与全 deck 主题对齐: 白底, 主线 SEU 深绿, 2024–2025 跃升段用黄色高亮,
中文标签使用微软雅黑.

Usage:
    uv run python assets/cve_growth.py
Output:
    assets/cve_growth.zh.png  (~300 dpi)
"""
from __future__ import annotations

from pathlib import Path

import matplotlib.pyplot as plt
from matplotlib import font_manager

ASSETS_DIR = Path(__file__).resolve().parent
OUTPUT_PATH = ASSETS_DIR / "cve_growth.zh.png"

# 字体: 优先微软雅黑, 兜底 Noto / WenQuanYi
plt.rcParams["font.sans-serif"] = [
    "Microsoft YaHei",
    "Noto Sans CJK SC",
    "WenQuanYi Micro Hei",
    "DejaVu Sans",
]
plt.rcParams["font.family"] = "sans-serif"
plt.rcParams["axes.unicode_minus"] = False

YEARS = [2016, 2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025]
COUNTS = [6447, 14645, 16511, 17305, 18353, 20162, 25082, 28818, 40009, 48185]

# SEU 主题色
GREEN_DARK = "#1B5E20"
YELLOW = "#FFC107"
GRAY_TEXT = "#333333"
GRAY_GRID = "#E0E0E0"
GRAY_AXIS = "#9E9E9E"


def main() -> None:
    fig, ax = plt.subplots(figsize=(9.0, 4.0), dpi=200)
    fig.patch.set_facecolor("white")
    ax.set_facecolor("white")

    # 主线: 2016–2024 深绿
    ax.plot(
        YEARS[:-1], COUNTS[:-1],
        color=GREEN_DARK, linewidth=2.4, marker="o", markersize=7,
        markerfacecolor=GREEN_DARK, markeredgecolor="white", markeredgewidth=1.2,
        zorder=3,
    )
    # 2024–2025 跃升段用黄色, 端点黄色实心
    ax.plot(
        YEARS[-2:], COUNTS[-2:],
        color=YELLOW, linewidth=3.2, marker="o", markersize=9,
        markerfacecolor=YELLOW, markeredgecolor=GREEN_DARK, markeredgewidth=1.5,
        zorder=4,
    )

    # 数据标签
    for i, (y, c) in enumerate(zip(YEARS, COUNTS)):
        offset = 1400 if i < len(YEARS) - 2 else 2100
        color = GREEN_DARK if i < len(YEARS) - 2 else "#B7791F"
        weight = "normal" if i < len(YEARS) - 2 else "bold"
        ax.annotate(
            f"{c:,}",
            xy=(y, c), xytext=(y, c + offset),
            ha="center", va="bottom",
            fontsize=9.5, color=color, weight=weight,
        )

    ax.set_xticks(YEARS)
    ax.set_xticklabels([str(y) for y in YEARS], fontsize=10, color=GRAY_TEXT)
    ax.set_yticks([0, 10000, 20000, 30000, 40000, 50000])
    ax.set_yticklabels(["0", "1 万", "2 万", "3 万", "4 万", "5 万"], fontsize=10, color=GRAY_TEXT)
    ax.set_ylim(0, 55000)
    ax.set_xlim(2015.5, 2025.5)

    ax.set_xlabel("年份", fontsize=11, color=GRAY_TEXT, labelpad=8)
    ax.set_ylabel("年度发布 CVE 数", fontsize=11, color=GRAY_TEXT, labelpad=10)

    ax.grid(axis="y", linestyle="--", linewidth=0.6, color=GRAY_GRID, zorder=1)
    ax.set_axisbelow(True)

    for spine in ("top", "right"):
        ax.spines[spine].set_visible(False)
    for spine in ("left", "bottom"):
        ax.spines[spine].set_color(GRAY_AXIS)
        ax.spines[spine].set_linewidth(0.8)

    ax.tick_params(axis="both", colors=GRAY_AXIS, length=4, width=0.6)

    fig.tight_layout(pad=1.2)
    fig.savefig(OUTPUT_PATH, dpi=300, bbox_inches="tight", facecolor="white")
    print(f"[OK] wrote {OUTPUT_PATH}")


if __name__ == "__main__":
    main()
