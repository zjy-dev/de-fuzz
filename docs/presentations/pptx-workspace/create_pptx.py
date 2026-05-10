"""Build DeFuzz 开题答辩 PowerPoint from scratch using python-pptx.

Usage:
    uv run python create_pptx.py

Output:
    开题答辩-DeFuzz.pptx  (overwrites existing file in the same directory)
"""
from __future__ import annotations

from pptx import Presentation

from builders import ALL_SLIDES
from theme import OUTPUT_PATH, SLIDE_H, SLIDE_W


def build() -> None:
    pres = Presentation()
    pres.slide_width = SLIDE_W
    pres.slide_height = SLIDE_H
    pres.core_properties.title = "DeFuzz: LLM 驱动的编译器软件防御机制模糊测试"
    pres.core_properties.author = "DeFuzz"
    pres.core_properties.subject = "硕士学位论文开题答辩"

    for builder in ALL_SLIDES:
        builder(pres)

    pres.save(str(OUTPUT_PATH))
    print(f"[OK] wrote {OUTPUT_PATH} ({len(ALL_SLIDES)} slides)")


if __name__ == "__main__":
    build()
