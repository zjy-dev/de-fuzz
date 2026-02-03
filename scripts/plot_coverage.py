#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = [
#     "matplotlib",
#     "numpy",
# ]
# ///
"""
DeFuzz Coverage & Bug Visualization Script

Generates two plots:
1. BB Coverage growth over seed IDs
2. Cumulative Bug count over seed IDs

Coverage is stored as basis points (万分比):
  - 10000 = 100%
  - 5358 = 53.58%

Usage:
    uv run scripts/plot_coverage.py --data-dir fuzz_out/loongarch64/canary
    uv run scripts/plot_coverage.py -d fuzz_out/aarch64/canary -o aarch64
"""

import argparse
import json
import sys
from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np

# Plot style settings
plt.rcParams.update({
    'font.size': 12,
    'axes.labelsize': 14,
    'axes.titlesize': 16,
    'xtick.labelsize': 11,
    'ytick.labelsize': 11,
    'legend.fontsize': 11,
    'figure.figsize': (10, 6),
    'figure.dpi': 150,
    'savefig.dpi': 300,
    'savefig.bbox': 'tight',
    'axes.grid': True,
    'grid.alpha': 0.3,
    'axes.spines.top': False,
    'axes.spines.right': False,
})

COLORS = {
    'coverage': '#2E86AB',   # Blue
    'bug': '#E74C3C',        # Red
}


def load_seed_metadata(data_dir: Path) -> list[dict]:
    """Load all seed metadata from metadata directory, sorted by ID."""
    metadata_dir = data_dir / "metadata"
    if not metadata_dir.exists():
        raise FileNotFoundError(f"metadata directory not found: {metadata_dir}")
    
    seeds = []
    for f in metadata_dir.glob("*.json"):
        try:
            with open(f, 'r') as fp:
                seeds.append(json.load(fp))
        except (json.JSONDecodeError, KeyError) as e:
            print(f"Warning: Failed to parse {f}: {e}", file=sys.stderr)
    
    seeds.sort(key=lambda x: x.get('id', 0))
    return seeds


def bp_to_percent(bp: int) -> float:
    """Convert basis points (万分比) to percentage."""
    return bp / 100.0


def plot_coverage(seeds: list[dict], output_path: Path):
    """Plot BB coverage growth over seed IDs."""
    if not seeds:
        print("No seeds to plot", file=sys.stderr)
        return
    
    seed_ids = [s['id'] for s in seeds]
    coverages = [bp_to_percent(s.get('new_cov', 0)) for s in seeds]
    
    fig, ax = plt.subplots()
    
    ax.plot(seed_ids, coverages, 
            color=COLORS['coverage'], linewidth=2,
            marker='o', markersize=3, markerfacecolor='white',
            markeredgecolor=COLORS['coverage'], markeredgewidth=1)
    ax.fill_between(seed_ids, coverages, alpha=0.15, color=COLORS['coverage'])
    
    ax.set_xlabel('Seed ID')
    ax.set_ylabel('Basic Block Coverage (%)')
    ax.set_title('Coverage Growth Over Seeds')
    
    # Annotate final coverage
    final_cov = coverages[-1]
    final_id = seed_ids[-1]
    ax.annotate(f'{final_cov:.2f}%', 
                xy=(final_id, final_cov),
                xytext=(final_id * 0.85, final_cov * 1.05),
                fontsize=12, fontweight='bold',
                arrowprops=dict(arrowstyle='->', color='gray'),
                color=COLORS['coverage'])
    
    ax.set_xlim(left=0)
    ax.set_ylim(bottom=0, top=max(coverages) * 1.1)
    
    plt.tight_layout()
    plt.savefig(output_path)
    plt.close()
    print(f"Saved: {output_path}")


def plot_bugs(seeds: list[dict], output_path: Path):
    """Plot cumulative bug count over seed IDs."""
    if not seeds:
        print("No seeds to plot", file=sys.stderr)
        return
    
    seed_ids = [s['id'] for s in seeds]
    
    # Calculate cumulative bug count
    cumulative_bugs = []
    bug_count = 0
    for s in seeds:
        if s.get('oracle_verdict') == 'BUG':
            bug_count += 1
        cumulative_bugs.append(bug_count)
    
    fig, ax = plt.subplots()
    
    ax.step(seed_ids, cumulative_bugs, where='post',
            color=COLORS['bug'], linewidth=2)
    ax.fill_between(seed_ids, cumulative_bugs, alpha=0.15, 
                    color=COLORS['bug'], step='post')
    
    ax.set_xlabel('Seed ID')
    ax.set_ylabel('Cumulative Bug Count')
    ax.set_title('Bug Discovery Over Seeds')
    
    # Annotate final bug count
    final_bugs = cumulative_bugs[-1]
    final_id = seed_ids[-1]
    ax.annotate(f'{final_bugs} bugs', 
                xy=(final_id, final_bugs),
                xytext=(final_id * 0.85, final_bugs * 1.1 if final_bugs > 0 else 1),
                fontsize=12, fontweight='bold',
                arrowprops=dict(arrowstyle='->', color='gray'),
                color=COLORS['bug'])
    
    ax.set_xlim(left=0)
    ax.set_ylim(bottom=0, top=max(cumulative_bugs) * 1.2 if final_bugs > 0 else 10)
    
    # Use integer ticks for bug count
    ax.yaxis.set_major_locator(plt.MaxNLocator(integer=True))
    
    plt.tight_layout()
    plt.savefig(output_path)
    plt.close()
    print(f"Saved: {output_path}")


def main():
    parser = argparse.ArgumentParser(
        description='Generate coverage and bug plots for DeFuzz',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
    uv run scripts/plot_coverage.py -d fuzz_out/loongarch64/canary
    uv run scripts/plot_coverage.py -d fuzz_out/aarch64/canary -o aarch64
        """
    )
    parser.add_argument(
        '--data-dir', '-d',
        type=str,
        required=True,
        help='Path to fuzzing output directory (e.g., fuzz_out/loongarch64/canary)'
    )
    parser.add_argument(
        '--output-prefix', '-o',
        type=str,
        default='result',
        help='Prefix for output files (default: result)'
    )
    parser.add_argument(
        '--output-dir',
        type=str,
        default='./scripts/assets',
        help='Output directory for plots (default: ./scripts/assets)'
    )
    
    args = parser.parse_args()
    
    data_dir = Path(args.data_dir)
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    prefix = args.output_prefix
    
    if not data_dir.exists():
        print(f"Error: Data directory not found: {data_dir}", file=sys.stderr)
        sys.exit(1)
    
    print(f"Loading data from: {data_dir}")
    
    # Load seed metadata
    try:
        seeds = load_seed_metadata(data_dir)
        print(f"Loaded {len(seeds)} seeds")
    except FileNotFoundError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)
    
    if not seeds:
        print("Error: No seed metadata found", file=sys.stderr)
        sys.exit(1)
    
    # Summary
    final_cov = bp_to_percent(seeds[-1].get('new_cov', 0))
    total_bugs = sum(1 for s in seeds if s.get('oracle_verdict') == 'BUG')
    print(f"Final coverage: {final_cov:.2f}%")
    print(f"Total bugs found: {total_bugs}")
    print()
    
    # Generate plots
    print("Generating plots...")
    plot_coverage(seeds, output_dir / f"{prefix}_coverage.png")
    plot_bugs(seeds, output_dir / f"{prefix}_bugs.png")
    
    print("\nDone!")


if __name__ == '__main__':
    main()
