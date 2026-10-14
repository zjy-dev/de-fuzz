# Run status — GCC16.2/AArch64 full experiment (2026-10-13)

Single `full`-variant, one repetition, GCC 16.2.0 (release
78d4ac73dd391005b895b16148cd9831e28e1208b), AArch64, Pi backend via the
local Coconut gateway. Engineering run (not formal four-variant campaign).

## Stage results

| Stage | Status | Calls | Tokens | usage_missing | Note |
|---|---|---|---|---|---|
| 1 invariant-generation | completed | 539 | 1,576,298 | 0 | 173/173 segments; 50 accepted (43 AArch64) |
| 2 checker-authoring | completed | 685 | 18,065,918 | 0 | **43/43 checkers pass**, bundle ready |
| 3 source-mapping | completed | 324 | 5,228,078 | 0 | **43/43 mapped**, CallGraph used |
| 4 log-instrumentation | completed | 0 (deterministic) | 0 | 0 | 551 anchors, 86 active DEFUZZ_HIT probes, 58 files |
| 5 agent-audit | relaunch in progress | — | — | — | first run 4/5 families valid; family C needed more budget |

## Agent-audit detail and fixes

First audit launch (per-attempt 1.0M): all 5 families failed after 2 calls
(`insufficient remaining token budget`) because audit prompts carry
~340–400k input tokens per call.

Second launch (per-attempt 2.5M): families A,B,D,E valid; family C reached
6 calls / 2.40M and its 7th call tripped the 2.5M ceiling, so
`result_valid=false` with `invalid_workers=1` (worker C parse issue).
Families A,B,D,E each wrote 8.3–9.8 KB audit reports; 0 candidates admitted
(this is normal — admission requires a deterministic PoC-validated defect).

Third launch (current, pid recorded in run-audit.log): per-attempt 4.0M in
`.work/gcc16-aarch64-run/pi-runtime.audit.json`. Reuses unchanged
Parts I–IV and their content-addressed artifacts. `run-audit.sh` derives
ROOT from its own location and removes the prior audit dir.

Stage startup re-materializes the 1.4 GB / 159k-file GCC tree (and
`bind_mapping_handoff` re-validates), so ~35–40 min elapse before the first
audit model call. Dedicated monitor: `watch-audit.sh` -> `watch-audit.log`.

## Recorded engineering issues (resolved)

1. Part II output/deny-root collision → out-of-repo output + checker roots.
2. Per-attempt token ceiling too small for checker authoring (200k→1M).
3. One provider timeout cascaded under strict_usage → local config
   strict_usage=false (formal evidence still needs a strict clean rerun).
4. Audit per-attempt ceiling (1M→2.5M→4M) for large family prompts.

All missing-usage counts are 0 on every completed stage, but the engineering
config disables strict blocking, so formal paper statistics require a later
strict rerun.
