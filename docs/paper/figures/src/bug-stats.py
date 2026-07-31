#!/usr/bin/env python3
# Fig: bug-finding statistics over the DREV zero-day findings corpus.
# Muted academic color palette (one hue per panel; colorblind-safe trio).
#
# Reads the findings corpus front matter directly so the figure is
# regenerable from ground truth rather than hand-transcribed numbers.
#
# render:
#   python3 bug-stats.py \
#     --findings /path/to/defend-reviewer/.../findings \
#     --out ../bug-stats
#   (writes ../bug-stats.pdf and ../bug-stats.svg)

import argparse
import collections
import os
import re
import sys

import matplotlib
matplotlib.use("pdf")
import matplotlib.pyplot as plt
from matplotlib import rcParams

# ---- academic color palette (soft, print-friendly; one hue per panel) -------
INK = "#333333"
MID = "#8A8A8A"
GRID = "#E8E8E8"

# Per-panel (fill, edge) pairs: soft, desaturated blue / green / amber, a
# colorblind-safe trio. Edges are a slightly deeper shade of the same hue and
# stay thin so the fill, not the outline, carries the figure.
PANEL_COLORS = {
    "toolchain": ("#6F9BC3", "#4E7CA6"),   # soft steel blue
    "mechanism": ("#8DBF9A", "#639C77"),   # soft sage green
    "isa":       ("#E7B26B", "#CE9040"),   # soft amber
}

rcParams.update({
    "font.family": "serif",
    "font.size": 8,
    "axes.edgecolor": "#CCCCCC",
    "axes.linewidth": 0.6,
    "axes.labelcolor": INK,
    "text.color": INK,
    "xtick.color": INK,
    "ytick.color": INK,
    "svg.fonttype": "none",
})

# A finding with this status is excluded from the "valid" defect count.
EXCLUDED_STATUS = {"retracted"}


def parse_front_matter(path):
    """Return the YAML-ish front-matter block (text between the first two ---)."""
    text = open(path, encoding="utf-8").read()
    parts = text.split("---")
    return parts[1] if len(parts) >= 3 else ""


def scalar(block, key):
    m = re.search(r"^%s:\s*(.+)$" % re.escape(key), block, re.M)
    return m.group(1).strip() if m else ""


def isa_list(block):
    raw = scalar(block, "isa")
    return [tok for tok in re.findall(r"[a-z0-9_]+", raw) if tok != "generic"]


def collect(findings_dir):
    rows = []
    for name in sorted(os.listdir(findings_dir)):
        if not re.match(r"DREV-\d{4}-\d{3}$", name):
            continue
        readme = os.path.join(findings_dir, name, "README.md")
        if not os.path.isfile(readme):
            continue
        fm = parse_front_matter(readme)
        rows.append({
            "id": name,
            "toolchain": scalar(fm, "toolchain") or "unknown",
            "mechanism": scalar(fm, "mechanism") or "unknown",
            "status": scalar(fm, "status") or "draft",
            "isa": isa_list(fm),
        })
    return rows


def normalize_mechanism(m):
    # Fold spelling variants so the mechanism panel stays readable.
    m = m.strip().lower()
    m = re.split(r"\s*\(", m)[0]           # drop parentheticals
    aliases = {
        "cet-ibt": "ibt",
        "cet": "ibt",
        "ret-hardening": "ret-hardening",
        "return-address-signing": "pac",
        "nx": "noexecstack",
    }
    return aliases.get(m, m)


def hbar(ax, labels, counts, title, colors, nrows_max):
    """Horizontal bars with rounded ends, thin edges, and a uniform row pitch.

    nrows_max fixes the y-range across panels so bar thickness is identical
    everywhere (panels with fewer rows simply get top/bottom padding).
    """
    fill, edge = colors
    n = len(labels)
    y = list(range(n))

    # subtle vertical reference grid under the bars
    ax.set_axisbelow(True)
    ax.xaxis.grid(True, color=GRID, linewidth=0.6)

    ax.barh(y, counts, color=fill, edgecolor=edge, linewidth=0.7,
            height=0.62, zorder=3,
            capstyle="round",
            path_effects=None)
    # round the bar ends
    for patch in ax.patches:
        patch.set_joinstyle("round")

    ax.set_yticks(y)
    ax.set_yticklabels(labels)
    # pin every panel to the same row pitch, then invert
    ax.set_ylim(nrows_max - 0.5, -0.5)
    ax.set_title(title, fontsize=9, pad=8, loc="center", color=INK)
    ax.tick_params(length=0)
    for spine in ("top", "right", "left", "bottom"):
        ax.spines[spine].set_visible(False)
    xmax = max(counts) if counts else 1
    ax.set_xlim(0, xmax * 1.16)
    ax.set_xticks([])
    for yi, c in zip(y, counts):
        ax.text(c + xmax * 0.025, yi, str(c), va="center", ha="left",
                fontsize=7.5, color=INK)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--findings", required=True)
    ap.add_argument("--out", default="../bug-stats")
    ap.add_argument("--top-mech", type=int, default=8)
    ap.add_argument("--top-isa", type=int, default=8)
    args = ap.parse_args()

    rows = collect(args.findings)
    if not rows:
        sys.exit("no findings parsed from %s" % args.findings)

    valid = [r for r in rows if r["status"] not in EXCLUDED_STATUS]

    tool = collections.Counter(r["toolchain"] for r in valid)
    mech = collections.Counter(normalize_mechanism(r["mechanism"]) for r in valid)
    isa = collections.Counter(t for r in valid for t in r["isa"])

    n_valid = len(valid)
    n_total = len(rows)
    n_generic = sum(1 for r in valid if not r["isa"])

    # ---- toolchain panel (fixed, meaningful order) ----
    tool_order = ["gcc", "llvm", "lld", "compiler-rt"]
    tool_labels = [t for t in tool_order if tool.get(t)] + \
                  [t for t in tool if t not in tool_order]
    tool_counts = [tool[t] for t in tool_labels]
    tool_disp = {"gcc": "GCC", "llvm": "LLVM", "lld": "lld",
                 "compiler-rt": "compiler-rt"}
    tool_labels_disp = [tool_disp.get(t, t) for t in tool_labels]

    mech_top = mech.most_common(args.top_mech)
    mech_labels = [m for m, _ in mech_top]
    mech_counts = [c for _, c in mech_top]

    isa_top = isa.most_common(args.top_isa)
    isa_disp = {"x86_64": "x86-64", "riscv64": "RISC-V 64", "riscv32": "RISC-V 32",
                "riscv": "RISC-V", "aarch64": "AArch64", "loongarch64": "LoongArch64",
                "i686": "i686", "i386": "i386", "arm": "ARM", "armv7": "ARMv7",
                "mips": "MIPS", "mips64": "MIPS64", "x86": "x86"}
    isa_labels = [isa_disp.get(k, k) for k, _ in isa_top]
    isa_counts = [c for _, c in isa_top]

    nrows_max = max(len(tool_labels_disp), len(mech_labels), len(isa_labels))

    fig, axes = plt.subplots(1, 3, figsize=(7.2, 2.7))
    hbar(axes[0], tool_labels_disp, tool_counts,
         "(a) by toolchain", PANEL_COLORS["toolchain"], nrows_max)
    hbar(axes[1], mech_labels, mech_counts,
         "(b) by defense mechanism", PANEL_COLORS["mechanism"], nrows_max)
    hbar(axes[2], isa_labels, isa_counts,
         "(c) by affected ISA", PANEL_COLORS["isa"], nrows_max)

    fig.text(0.5, -0.02,
             "%d confirmed silent-failure defects "
             "(%d archived, 1 retracted); "
             "%d ISA-specific, %d generic. "
             "ISA counts sum over multi-ISA findings."
             % (n_valid, n_total, n_valid - n_generic, n_generic),
             ha="center", va="top", fontsize=6.8, color=MID)

    fig.tight_layout(rect=(0, 0.04, 1, 1))
    fig.savefig(args.out + ".pdf", bbox_inches="tight")
    fig.savefig(args.out + ".svg", bbox_inches="tight")
    if os.environ.get("BUGSTATS_PNG"):
        fig.savefig(args.out + ".png", dpi=200, bbox_inches="tight")

    # echo the numbers so a caller can cross-check the paper text.
    print("valid=%d total=%d generic=%d" % (n_valid, n_total, n_generic))
    print("toolchain=%s" % dict(tool))
    print("mechanism(top%d)=%s" % (args.top_mech, mech_top))
    print("isa(top%d)=%s" % (args.top_isa, isa_top))


if __name__ == "__main__":
    main()
