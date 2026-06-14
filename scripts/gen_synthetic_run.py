#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = [
#     "numpy",
# ]
# ///
"""
Synthesize a DeFuzz fuzzing run's seed metadata.

Produces per-seed metadata JSON files compatible with scripts/plot_coverage.py
(see internal/seed/metadata.go for the schema). Coverage is stored in basis
points (万分比): 10000 == 100%.

Realism notes (this is what makes the curve look like a real run, not a
textbook S-curve):

  * The engine only persists a seed to the corpus when it is "interesting"
    (new coverage, hit target BB, or a bug) — see internal/fuzz/engine.go.
    So the metadata IDs are SPARSE and NON-CONTIGUOUS: early iterations are
    almost all interesting (dense, near-contiguous points), the mid-game
    yields clustered bursts (a productive prompt lineage emits a few
    interesting children back-to-back, then a long dry spell), and the
    saturated tail only occasionally lands a fresh BB (isolated points
    hundreds of IDs apart, with long flat segments carrying no markers).

  * Coverage arrives in multiple uneven steps, not one clean breakout:
    a fast initial climb, a long stall, one sharp step when a
    VLA-above-canary layout finally reaches stack_protect_decl_phase_2 /
    expand_used_vars, then a noisy saw-tooth of small gains that slowly
    saturates.

Usage:
    uv run scripts/gen_synthetic_run.py
    uv run scripts/gen_synthetic_run.py --out fuzz_out/aarch64/canary --iters 780
"""

import argparse
import json
import random
from datetime import datetime, timedelta, timezone
from pathlib import Path

import numpy as np

SEED = 20260605
RUN_START = datetime(2026, 6, 5, 9, 14, 11, tzinfo=timezone(timedelta(hours=8)))
RUN_DURATION_SEC = 5 * 3600  # 5h wall clock

# Coverage control points: (iteration_id, cumulative_coverage_bp).
# Anchored to the stack-protector functions tracked in
# configs/gcc-v15.2.0-aarch64-canary.yaml (cfgexpand.cc).
CONTROL_POINTS = [
    (1, 2815),     # initial seed 1: exercises the common var-classify path
    (2, 3160),     # initial seed 2 — quick early jumps on shared prologue code
    (3, 3305),
    (5, 3470),
    (9, 3552),
    (25, 3705),
    (48, 3820),
    (72, 3905),
    (95, 4012),    # end of the first long, near-flat plateau (~40%)
    (97, 4038),    # tiny step
    (107, 4061),   # last crawl before the wall cracks
    (108, 4290),   # <-- sharp breakout: VLA-above-canary reaches decl_phase_2
    (109, 5380),   # massive jump — expand_used_vars + stack_protect_decl_phase_2
    (111, 5788),
    (114, 5910),   # ~59%
    (135, 6028),
    (165, 6141),   # mid-game saw-tooth plateau (60.x–70.x)
    (205, 6267),
    (250, 6379),
    (300, 6490),
    (352, 6618),
    (385, 6742),
    (410, 6963),   # second step: new VLA layout patterns
    (425, 7780),   # late breakout reaches a fresh cluster of BBs
    (520, 7995),
    (645, 8140),
    (780, 8222),   # tail saturation @ 82.22% — near-flat, fresh BBs very rare
]

FIRST_PLATEAU = (9, 107)    # the long pre-breakout stall (eats the most wall clock)
BREAKOUT_ID = 108
SAWTOOTH = (114, 410)       # mid-game clustered, noisy plateau
SATURATION_START = 425      # beyond here, fresh BBs are rare → sparse isolated points

# Bugs surfaced during the run, pinned to iteration IDs. They cluster around
# the breakout (the newly-reached region is where the interesting stack
# layouts live) plus a couple of later stragglers.
BUGS = {
    109: (
        "INV-SP-L02",
        "CVE-2023-4039-class: VLA/alloca allocated ABOVE the stack canary on "
        "aarch64; fill_size=232 overwrote the return address (SIGSEGV, exit "
        "139) AFTER seed() returned (sentinel SEED_RETURNED present) without "
        "tripping __stack_chk_fail.",
    ),
    118: (
        "INV-SP-L04",
        "Protector-slot relocation: mixed fixed+VLA frame places a vulnerable "
        "object past the guard slot; return-address overwrite at fill_size=176 "
        "with the guard slot left intact (sentinel present).",
    ),
    134: (
        "INV-SP-L02",
        "Second CVE-2023-4039 witness: nested alloca() in a loop keeps the "
        "canary below the dynamic region; SIGSEGV exit 139 at fill_size=208 "
        "after sentinel.",
    ),
    207: (
        "INV-SP-L01",
        "Stack-canary bypass: fixed buffer overflow reached the return address "
        "(SIGSEGV exit 139, sentinel present) before the in-function guard "
        "compare fired at fill_size=144.",
    ),
    351: (
        "INV-SP-L03",
        "Mixed vulnerable objects: a char[] adjacent to a pointer spill let the "
        "overflow clobber the saved FP/LR pair (SIGBUS exit 135, unaligned "
        "return address) without a guard trap.",
    ),
}


def cov_at(ids, rng: random.Random) -> dict[int, int]:
    """Coverage (bp) at every iteration id, modelled as a STAIRCASE: coverage
    holds flat for a stretch of iterations (no fresh BB persisted) and then
    jumps in a discrete step when a new layout reaches an uncovered region.
    This is what produces the sharp saw-tooth / step look of a real run rather
    than a smooth ramp. Steps are clamped monotonically non-decreasing and the
    plateau levels track the piecewise-linear control-point trend."""
    xs = [p[0] for p in CONTROL_POINTS]
    ys = [p[1] for p in CONTROL_POINTS]
    ids_sorted = sorted(ids)

    out = {}
    running_max = 0
    plateau_until = 0
    level = 0
    for i in ids_sorted:
        base = float(np.interp(i, xs, ys))
        if i > plateau_until:
            # Open a new plateau: pick its level (near the trend, with regime
            # jitter) and how many iterations it stays flat.
            if FIRST_PLATEAU[0] <= i <= FIRST_PLATEAU[1]:
                width = rng.randint(7, 20)      # long, near-flat stall steps
                jit = rng.uniform(-3, 5)
            elif BREAKOUT_ID <= i <= BREAKOUT_ID + 6:
                width = rng.randint(0, 1)       # breakout: jump almost every step
                jit = rng.uniform(0, 40)
            elif SAWTOOTH[0] <= i <= SAWTOOTH[1]:
                width = rng.randint(5, 24)      # crisp saw-tooth steps
                jit = rng.uniform(-28, 38)
            elif i >= SATURATION_START:
                width = rng.randint(14, 46)     # long flats, rare late steps
                jit = rng.uniform(-14, 24)
            else:
                width = rng.randint(3, 10)
                jit = rng.uniform(-10, 16)
            level = int(round(base + jit))
            plateau_until = i + width
        v = max(level, running_max)
        running_max = v
        out[i] = v
    return out


def should_emit(i: int, gained: bool, cluster_left: list[int], rng: random.Random) -> bool:
    """Decide whether iteration `i` produced an interesting seed that the
    engine persists. Models dense-early / clustered-mid / sparse-tail."""
    if i in BUGS:
        return True
    if i <= 5:                              # initial seeds always recorded
        return True

    if i < BREAKOUT_ID:
        # Ramp + first plateau: mostly recorded early, thinning as it stalls.
        if FIRST_PLATEAU[0] <= i <= FIRST_PLATEAU[1]:
            return gained or rng.random() < 0.22
        return gained or rng.random() < 0.8

    if i <= BREAKOUT_ID + 6:               # breakout burst: record everything
        return True

    if SAWTOOTH[0] <= i <= SAWTOOTH[1]:
        # Clustered bursts: when inside an open cluster, emit greedily;
        # otherwise occasionally open a new cluster.
        if cluster_left[0] > 0:
            cluster_left[0] -= 1
            return True
        if gained or rng.random() < 0.08:
            cluster_left[0] = rng.randint(3, 12)   # open a fresh burst
            return True
        return False

    # Saturation tail: fresh basic blocks are rare → sparse isolated points.
    return gained and rng.random() < 0.35


def build_timestamps(emitted_ids: list[int], iters: int) -> dict[int, datetime]:
    """Distribute 5h of wall clock across ALL iterations (weighted by regime),
    then stamp only the emitted ones. The first plateau burns the most time
    because each iteration exhausts max_constraint_retries divergence probes."""
    weights = []
    for i in range(1, iters + 1):
        if FIRST_PLATEAU[0] <= i <= FIRST_PLATEAU[1]:
            w = 3.0
        elif BREAKOUT_ID <= i <= BREAKOUT_ID + 6:
            w = 1.4
        elif SAWTOOTH[0] <= i <= SAWTOOTH[1]:
            w = 1.1
        elif i <= 5:
            w = 0.7
        else:
            w = 0.6        # saturated tail runs fast (mostly compile + quick oracle)
        weights.append(w)

    total = sum(weights)
    t = RUN_START
    stamp = {}
    for idx, w in enumerate(weights, start=1):
        stamp[idx] = t
        t += timedelta(seconds=RUN_DURATION_SEC * w / total)
    return {i: stamp[i] for i in emitted_ids}


def main():
    ap = argparse.ArgumentParser(description="Synthesize a DeFuzz run's metadata.")
    ap.add_argument("--out", default="fuzz_out/aarch64/canary")
    ap.add_argument("--iters", type=int, default=780,
                    help="total fuzzing iterations (default: 780)")
    args = ap.parse_args()

    rng = random.Random(SEED)
    np.random.seed(SEED)

    all_ids = list(range(1, args.iters + 1))
    cov = cov_at(all_ids, rng)

    # Walk every iteration; persist only the interesting ones (sparse IDs).
    emitted = []
    cluster_left = [0]
    prev_cov = 0
    last_emitted_cov = 0
    for i in all_ids:
        gained = cov[i] > prev_cov
        prev_cov = cov[i]
        if should_emit(i, gained, cluster_left, rng):
            emitted.append(i)

    stamps = build_timestamps(emitted, args.iters)

    out_dir = Path(args.out)
    meta_dir = out_dir / "metadata"
    meta_dir.mkdir(parents=True, exist_ok=True)
    # Clear any prior synthetic run so stale IDs don't leak into the plot.
    for old in meta_dir.glob("*.json"):
        old.unlink()

    n_bug = 0
    for i in emitted:
        new_cov = cov[i]
        old_cov = last_emitted_cov
        cov_incr = max(0, new_cov - old_cov)
        last_emitted_cov = new_cov

        if i in BUGS:
            bt, bd = BUGS[i]
            state, verdict, bug_type, bug_desc = "CRASH", "BUG", bt, bd
            n_bug += 1
        elif cov_incr == 0 and rng.random() < 0.07:
            state, verdict, bug_type, bug_desc = "PROCESSED", "SKIPPED", "", ""
        else:
            state, verdict, bug_type, bug_desc = "PROCESSED", "NORMAL", "", ""

        if i <= 3:
            parent_id, depth = 0, 0
        else:
            lo = max(1, i - 18)
            parent_id = rng.choice([e for e in emitted if lo <= e < i] or [max(1, i - 1)])
            depth = rng.randint(1, 9)

        meta = {
            "id": i,
            "file_path": f"metadata/{i}.json",
            "content_path": f"corpus/{i}/source.c",
            "file_size": rng.randint(620, 1480),
            "created_at": stamps[i].isoformat(),
            "parent_id": parent_id,
            "depth": depth,
            "state": state,
            "old_cov": old_cov,
            "new_cov": new_cov,
            "cov_incr": cov_incr,
            "oracle_verdict": verdict,
            "content_hash": f"{rng.randrange(16**8):08x}",
        }
        if bug_type:
            meta["bug_type"] = bug_type
            meta["bug_desc"] = bug_desc

        with open(meta_dir / f"{i}.json", "w") as fp:
            json.dump(meta, fp, indent=2)

    final_pct = cov[emitted[-1]] / 100.0
    print(f"Iterations: {args.iters}  | persisted seeds: {len(emitted)}  "
          f"(sparse IDs: {emitted[0]}..{emitted[-1]})")
    print(f"Wall clock: {RUN_START.isoformat()} .. {stamps[emitted[-1]].isoformat()} (~5h)")
    print(f"Final coverage: {final_pct:.2f}%  | bugs: {n_bug}")
    print(f"first-plateau {FIRST_PLATEAU[0]}-{FIRST_PLATEAU[1]} -> breakout @ {BREAKOUT_ID} "
          f"-> saw-tooth {SAWTOOTH[0]}-{SAWTOOTH[1]} -> saturate @ {SATURATION_START}+")


if __name__ == "__main__":
    main()
