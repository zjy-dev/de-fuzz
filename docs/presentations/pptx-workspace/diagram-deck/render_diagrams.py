"""Compile editable D2 sources into SVG and high-resolution PNG slide assets."""
from pathlib import Path
import os
import re
import subprocess
import sys

from fontTools.ttLib import TTFont
from PIL import Image

ROOT = Path(__file__).resolve().parent
ASSETS = ROOT / "build" / "diagrams"
FONTS = ROOT / "build" / "fonts"
OFFICE_FONTS = Path("/Applications/Microsoft PowerPoint.app/Contents/Resources/DFonts")


def main():
    ASSETS.mkdir(parents=True, exist_ok=True)
    FONTS.mkdir(parents=True, exist_ok=True)
    # Reuse the opening deck's evidence; crop only surrounding email UI.
    original = ROOT.parent / "assets" / "gcc-dev-severe.png"
    with Image.open(original) as image:
        image.crop((25, 320, 1015, 590)).save(ASSETS / "gcc-comment-crop.png")
    for source, target in (("msyh.ttc", "regular.ttf"), ("msyhbd.ttc", "bold.ttf")):
        output = FONTS / target
        if not output.exists():
            font = TTFont(OFFICE_FONTS / source, fontNumber=0)
            font.save(output)
    config = FONTS / "fonts.conf"
    config.write_text(
        '<?xml version="1.0"?><!DOCTYPE fontconfig SYSTEM "fonts.dtd">'
        f"<fontconfig><dir>{FONTS}</dir><dir>/System/Library/Fonts</dir>"
        f"<cachedir>{FONTS / 'cache'}</cachedir></fontconfig>"
    )
    env = {**os.environ, "FONTCONFIG_FILE": str(config)}
    requested = set(sys.argv[1:])
    for source in sorted((ROOT / "diagrams").glob("*.d2")):
        if source.stem == "theme" or (requested and source.stem not in requested):
            continue
        svg = ASSETS / source.with_suffix(".svg").name
        subprocess.run(
            ["d2", "--font-regular", str(FONTS / "regular.ttf"),
             "--font-bold", str(FONTS / "bold.ttf"), "--font-semibold",
             str(FONTS / "bold.ttf"), "--pad", "12",
             str(source), str(svg)], check=True,
        )
        # Explicit installed font names keep librsvg and Office rendering aligned.
        # D2 still measures layout with the same Microsoft YaHei font files.
        text = svg.read_text()
        text = re.sub(r"@font-face\s*\{[^}]*\}", "", text)
        text = re.sub(r"font-family:\s*[^;}]+", 'font-family: "Microsoft YaHei"', text)
        svg.write_text(text)
        subprocess.run(
            ["rsvg-convert", "--width", "2800", str(svg),
             "--output", str(svg.with_suffix(".png"))],
            env=env, check=True,
        )
        print(f"[diagram] {source.stem}", flush=True)


if __name__ == "__main__":
    main()
