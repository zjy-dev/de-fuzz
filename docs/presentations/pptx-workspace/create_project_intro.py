"""Compatibility entry point for the D2 + PptxGenJS group-sharing deck.

The editable authoring source is diagram-deck/build.js.
Build output stays in diagram-deck/build until visual QA passes.
"""
import argparse
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--outline-only", action="store_true")
    args = parser.parse_args()
    command = ["node", str(ROOT / "diagram-deck" / "build.js")]
    if args.outline_only:
        command.append("--outline-only")
    else:
        subprocess.run(
            [sys.executable, str(ROOT / "diagram-deck" / "render_diagrams.py")],
            check=True,
        )
    subprocess.run(command, check=True)


if __name__ == "__main__":
    main()
