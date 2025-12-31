#!/usr/bin/env python3
"""
DeFuzz Coverage Visualization Script

This script generates publication-quality plots for fuzzing coverage data.
It parses the metadata directory containing per-seed coverage in basis points (万分比).

Coverage is stored as basis points where:
  - 10000 = 100%
  - 7474 = 74.74%

Usage:
    python scripts/plot_coverage.py --data-dir fuzz_out/x64/canary
    python scripts/plot_coverage.py --data-dir fuzz_out/aarch64/canary --output-prefix aarch64
"""

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Dict, List, Tuple, Any

import matplotlib.pyplot as plt
import matplotlib.ticker as ticker
import numpy as np

# Publication-quality plot settings
plt.rcParams.update({
    'font.family': 'serif',
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

# Color palette for scientific publications
COLORS = {
    'primary': '#2E86AB',      # Deep blue
    'secondary': '#A23B72',    # Magenta
    'accent': '#F18F01',       # Orange
    'success': '#28A745',      # Green
    'neutral': '#3B3B3B',      # Dark gray
    'light_bg': '#F5F5F5',     # Light background
}


def load_global_state(data_dir: Path) -> Dict[str, Any]:
    """Load global_state.json from the state directory."""
    global_state_path = data_dir / "state" / "global_state.json"
    if not global_state_path.exists():
        raise FileNotFoundError(f"global_state.json not found in {data_dir}/state")
    
    with open(global_state_path, 'r') as f:
        return json.load(f)


def load_seed_metadata(data_dir: Path) -> List[Dict[str, Any]]:
    """
    Load all seed metadata from metadata directory.
    Returns list of metadata sorted by seed ID.
    """
    metadata_dir = data_dir / "metadata"
    if not metadata_dir.exists():
        raise FileNotFoundError(f"metadata directory not found in {data_dir}")
    
    seeds = []
    for f in metadata_dir.glob("*.json"):
        try:
            with open(f, 'r') as fp:
                meta = json.load(fp)
                seeds.append(meta)
        except (json.JSONDecodeError, KeyError) as e:
            print(f"Warning: Failed to parse {f}: {e}", file=sys.stderr)
            continue
    
    # Sort by ID
    seeds.sort(key=lambda x: x.get('id', 0))
    return seeds


def basis_points_to_percent(bp: int) -> float:
    """Convert basis points to percentage."""
    return bp / 100.0


def plot_coverage_over_seeds(
    seeds: List[Dict[str, Any]],
    output_path: Path,
    title: str = "Coverage Growth Over Seeds"
):
    """Plot cumulative BB coverage as seeds are processed."""
    if not seeds:
        print("No seeds to plot", file=sys.stderr)
        return
    
    seed_ids = [s['id'] for s in seeds]
    # new_cov represents the cumulative coverage after processing this seed
    new_coverages = [basis_points_to_percent(s.get('new_cov', 0)) for s in seeds]
    
    fig, ax = plt.subplots(figsize=(10, 6))
    
    # Main line plot
    ax.plot(seed_ids, new_coverages, 
            color=COLORS['primary'], linewidth=2.5, 
            marker='o', markersize=4, markerfacecolor='white',
            markeredgewidth=1.5, markeredgecolor=COLORS['primary'],
            label='BB Coverage')
    
    # Fill under curve
    ax.fill_between(seed_ids, new_coverages, 
                    alpha=0.15, color=COLORS['primary'])
    
    # Styling
    ax.set_xlabel('Seed ID')
    ax.set_ylabel('Basic Block Coverage (%)')
    ax.set_title(title)
    
    # Add final coverage annotation
    if new_coverages:
        final_cov = new_coverages[-1]
        final_seed = seed_ids[-1]
        ax.annotate(f'{final_cov:.2f}%', 
                    xy=(final_seed, final_cov),
                    xytext=(final_seed - len(seed_ids)*0.15, final_cov + 3),
                    fontsize=11, fontweight='bold',
                    arrowprops=dict(arrowstyle='->', color=COLORS['neutral'], lw=1.5),
                    color=COLORS['primary'])
    
    ax.legend(loc='lower right')
    ax.set_xlim(left=min(seed_ids) - 0.5 if seed_ids else 0)
    ax.set_ylim(bottom=0, top=max(new_coverages) * 1.15 if new_coverages else 100)
    
    plt.tight_layout()
    plt.savefig(output_path)
    plt.close()
    print(f"Saved: {output_path}")


def plot_coverage_increase_per_seed(
    seeds: List[Dict[str, Any]],
    output_path: Path,
    title: str = "Coverage Increase Per Seed"
):
    """Bar chart showing coverage increase (basis points) from each seed."""
    if not seeds:
        print("No seeds to plot", file=sys.stderr)
        return
    
    seed_ids = [s['id'] for s in seeds]
    cov_increases = [basis_points_to_percent(s.get('cov_incr', 0)) for s in seeds]
    
    fig, ax = plt.subplots(figsize=(12, 6))
    
    # Color bars based on contribution (highlight high contributors)
    max_incr = max(cov_increases) if cov_increases else 1
    colors = [COLORS['accent'] if ci > max_incr * 0.3 else COLORS['primary'] 
              for ci in cov_increases]
    
    bars = ax.bar(seed_ids, cov_increases, color=colors, edgecolor='white', linewidth=0.5)
    
    ax.set_xlabel('Seed ID')
    ax.set_ylabel('Coverage Increase (%)')
    ax.set_title(title)
    
    # Add statistics annotation
    nonzero_seeds = sum(1 for ci in cov_increases if ci > 0)
    avg_incr = sum(cov_increases) / len(cov_increases) if cov_increases else 0
    total_incr = sum(cov_increases)
    stats_text = f'Seeds with coverage gain: {nonzero_seeds}/{len(seed_ids)}\nTotal increase: {total_incr:.2f}%\nAvg: {avg_incr:.2f}%/seed'
    ax.text(0.98, 0.95, stats_text, transform=ax.transAxes, 
            fontsize=10, verticalalignment='top', horizontalalignment='right',
            bbox=dict(boxstyle='round', facecolor=COLORS['light_bg'], alpha=0.8))
    
    ax.set_xlim(left=min(seed_ids) - 1 if seed_ids else 0, 
                right=max(seed_ids) + 1 if seed_ids else 1)
    ax.set_ylim(bottom=0)
    
    plt.tight_layout()
    plt.savefig(output_path)
    plt.close()
    print(f"Saved: {output_path}")


def plot_seed_lineage(
    seeds: List[Dict[str, Any]],
    output_path: Path,
    title: str = "Seed Lineage Distribution"
):
    """Plot distribution of seed depths (mutation generations)."""
    if not seeds:
        print("No seeds to plot", file=sys.stderr)
        return
    
    depths = [s.get('depth', 0) for s in seeds]
    unique_depths = sorted(set(depths))
    depth_counts = [depths.count(d) for d in unique_depths]
    
    fig, ax = plt.subplots(figsize=(10, 6))
    
    bars = ax.bar(unique_depths, depth_counts, color=COLORS['secondary'], 
                  edgecolor='white', linewidth=0.5)
    
    ax.set_xlabel('Mutation Depth')
    ax.set_ylabel('Number of Seeds')
    ax.set_title(title)
    
    # Add value labels on bars
    for bar, count in zip(bars, depth_counts):
        ax.text(bar.get_x() + bar.get_width()/2, bar.get_height() + 0.1,
                str(count), ha='center', va='bottom', fontsize=10)
    
    ax.set_xticks(unique_depths)
    ax.set_ylim(bottom=0, top=max(depth_counts) * 1.15 if depth_counts else 1)
    
    plt.tight_layout()
    plt.savefig(output_path)
    plt.close()
    print(f"Saved: {output_path}")


def plot_summary_dashboard(
    global_state: Dict[str, Any],
    seeds: List[Dict[str, Any]],
    output_path: Path
):
    """Create a summary dashboard with multiple subplots."""
    fig = plt.figure(figsize=(14, 10))
    
    # Create grid
    gs = fig.add_gridspec(2, 2, hspace=0.3, wspace=0.3)
    
    # Extract data
    seed_ids = [s['id'] for s in seeds]
    new_coverages = [basis_points_to_percent(s.get('new_cov', 0)) for s in seeds]
    cov_increases = [basis_points_to_percent(s.get('cov_incr', 0)) for s in seeds]
    depths = [s.get('depth', 0) for s in seeds]
    
    # 1. Coverage over seeds (top-left)
    ax1 = fig.add_subplot(gs[0, 0])
    if seed_ids and new_coverages:
        ax1.plot(seed_ids, new_coverages, 
                 color=COLORS['primary'], linewidth=2, marker='o', markersize=3)
        ax1.fill_between(seed_ids, new_coverages, alpha=0.15, color=COLORS['primary'])
    ax1.set_xlabel('Seed ID')
    ax1.set_ylabel('BB Coverage (%)')
    ax1.set_title('Cumulative Coverage')
    ax1.set_ylim(bottom=0)
    
    # 2. Coverage increase per seed (top-right)
    ax2 = fig.add_subplot(gs[0, 1])
    if seed_ids:
        ax2.bar(seed_ids, cov_increases, color=COLORS['secondary'], alpha=0.8)
    ax2.set_xlabel('Seed ID')
    ax2.set_ylabel('Increase (%)')
    ax2.set_title('Coverage Gain Per Seed')
    
    # 3. Summary statistics (bottom-left)
    ax3 = fig.add_subplot(gs[1, 0])
    ax3.axis('off')
    
    total_seeds = global_state.get('last_allocated_id', len(seeds))
    total_cov_bp = global_state.get('total_coverage', 0)
    total_cov_pct = basis_points_to_percent(total_cov_bp)
    nonzero_seeds = sum(1 for ci in cov_increases if ci > 0)
    max_depth = max(depths) if depths else 0
    
    stats_text = f"""
    Summary Statistics
    ─────────────────────────
    Total Seeds:           {total_seeds}
    Final BB Coverage:     {total_cov_pct:.2f}%
    Seeds with New Cov:    {nonzero_seeds}
    Max Mutation Depth:    {max_depth}
    Initial Seeds:         {sum(1 for d in depths if d == 0)}
    Mutated Seeds:         {sum(1 for d in depths if d > 0)}
    """
    
    ax3.text(0.1, 0.5, stats_text, transform=ax3.transAxes,
             fontsize=14, fontfamily='monospace', verticalalignment='center',
             bbox=dict(boxstyle='round', facecolor=COLORS['light_bg'], alpha=0.8, pad=1))
    
    # 4. Depth distribution (bottom-right)  
    ax4 = fig.add_subplot(gs[1, 1])
    if depths:
        unique_depths = sorted(set(depths))
        depth_counts = [depths.count(d) for d in unique_depths]
        ax4.bar(unique_depths, depth_counts, color=COLORS['accent'], alpha=0.8)
        ax4.set_xticks(unique_depths)
    ax4.set_xlabel('Mutation Depth')
    ax4.set_ylabel('Count')
    ax4.set_title('Seed Lineage Distribution')
    
    plt.suptitle('DeFuzz Coverage Report', fontsize=18, fontweight='bold', y=1.02)
    
    plt.savefig(output_path)
    plt.close()
    print(f"Saved: {output_path}")


def main():
    parser = argparse.ArgumentParser(
        description='Generate publication-quality coverage plots for DeFuzz',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
    python scripts/plot_coverage.py --data-dir fuzz_out/x64/canary
    python scripts/plot_coverage.py --data-dir fuzz_out/aarch64/canary --output-prefix aarch64
        """
    )
    parser.add_argument(
        '--data-dir', '-d',
        type=str,
        required=True,
        help='Path to the fuzzing output directory (e.g., fuzz_out/x64/canary)'
    )
    parser.add_argument(
        '--output-prefix', '-o',
        type=str,
        default='coverage',
        help='Prefix for output files (default: coverage)'
    )
    parser.add_argument(
        '--output-dir',
        type=str,
        default='./scripts/assets',
        help='Output directory for plots (default: ./scripts/assets)'
    )
    
    args = parser.parse_args()
    
    # Setup paths
    data_dir = Path(args.data_dir)
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    
    prefix = args.output_prefix
    
    # Validate input
    if not data_dir.exists():
        print(f"Error: Data directory not found: {data_dir}", file=sys.stderr)
        sys.exit(1)
    
    print(f"Loading data from: {data_dir}")
    print(f"Output directory: {output_dir}")
    print()
    
    # Load global state
    try:
        global_state = load_global_state(data_dir)
        total_cov_bp = global_state.get('total_coverage', 0)
        print(f"Global state: total_coverage = {total_cov_bp} bp ({basis_points_to_percent(total_cov_bp):.2f}%)")
    except FileNotFoundError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)
    
    # Load seed metadata
    try:
        seeds = load_seed_metadata(data_dir)
        print(f"Found {len(seeds)} seed metadata files")
    except FileNotFoundError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)
    
    if not seeds:
        print("Error: No seed metadata found", file=sys.stderr)
        sys.exit(1)
    
    # Summary
    final_cov = seeds[-1].get('new_cov', 0) if seeds else 0
    print(f"Final coverage: {basis_points_to_percent(final_cov):.2f}%")
    print()
    
    # Generate plots
    print("Generating plots...")
    
    # 1. Coverage growth
    plot_coverage_over_seeds(
        seeds,
        output_dir / f"{prefix}_growth.png",
        title="Cumulative BB Coverage Growth"
    )
    
    # 2. Coverage increase per seed
    plot_coverage_increase_per_seed(
        seeds,
        output_dir / f"{prefix}_per_seed.png",
        title="Coverage Increase Per Seed"
    )
    
    # 3. Seed lineage
    plot_seed_lineage(
        seeds,
        output_dir / f"{prefix}_lineage.png",
        title="Seed Mutation Depth Distribution"
    )
    
    # 4. Summary dashboard
    plot_summary_dashboard(
        global_state, seeds,
        output_dir / f"{prefix}_dashboard.png"
    )
    
    print()
    print("All plots generated successfully!")


if __name__ == '__main__':
    main()
