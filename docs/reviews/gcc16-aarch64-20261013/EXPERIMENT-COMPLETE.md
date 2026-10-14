# GCC16.2/AArch64 full experiment — COMPLETE (2026-10-13)

`run_id = gcc16-aarch64-full-20261013`. One repetition, `full` variant,
GCC 16.2.0 (release 78d4ac73dd391005b895b16148cd9831e28e1208b), AArch64,
Pi backend via the local Coconut gateway. Engineering run, not a formal
four-variant/three-repetition paper campaign.

## All five stages completed, 0 missing usage

| # | Stage | Calls | Tokens | Key result |
|---|---|---:|---:|---|
| 1 | invariant-generation | 539 | 1,576,298 | 173/173 segments; 50 accepted (43 AArch64) |
| 2 | checker-authoring | 685 | 18,065,918 | **43/43 checkers pass**, bundle ready |
| 3 | source-mapping | 324 | 5,228,078 | **43/43 mapped**, CallGraph used |
| 4 | log-instrumentation | 0 | 0 | 551 anchors, 86 active DEFUZZ_HIT probes, 58 files |
| 5 | agent-audit | 27 | 10,125,151 | **5/5 workers valid, result_valid=true** |
| — | **total** | **1,575** | **34,995,445** | **usage_missing = 0** |

Every repetition is token-comparable (`usage_missing_count == 0`).

## Audit conclusion (Part III)

Families A–E all produced clean structured reports
(`tainted=false`, `parse_issues=[]`), each auditing 6–7 real GCC 16.2
components (cfgexpand/function/calls, tree-ssa-dse/strlen, aarch64.cc +
elf-metadata, opts/g.cc PIE/RELRO, ASan/auto-var-init). They reported
**0 candidate defects** that clear the Finding Admission Gate, plus
coverage gaps: source + CallGraph evidence cannot prove final emitted code
without compile / assembly / binutils / glibc-runtime checks. This is a
valid source-only null result, with concrete next-step checks
(AArch64 assembly, FORTIFY=3 at -O0/-Os, non-AEABI GNU property path,
RELRO boundaries, ASan stack-layout/LTO cases).

## Engineering fixes made this session

1. Moved run output and the checker source to out-of-repo roots so the
   repo-root read-deny boundary no longer collides with agent output or
   with the in-repo Pi worker runtime (isolation preserved).
2. Added the deterministic `log-instrumentation` stage
   (`log_instrumentation_stage.py`, wired into CLI/run-all, tests).
3. Per-attempt token budgets: checker authoring 200k→1.0M; agent-audit
   families 1.0M→2.5M→4.0M (large ~400k/call prompts); stage ceiling 40M.
4. Local engineering Pi configs set `strict_usage=false` so one transient
   provider timeout cannot cascade-abort later invariants. Every call still
   records usage; a strict clean rerun is required for formal evidence.
5. Podman-backed host toolchain wrappers for the GCC 16.2 image
   (`podman-compiler-driver.sh`, `podman-qemu-user.sh`).

Known performance issue: each stage materializes/hashes the full 1.4 GB /
159k-file GCC source tree (audit does it twice), adding ~30–40 min setup
per model stage. A future optimization is to copy/hash only the mapped
file set.

## Artifacts

- Final stats: `final-statistics.json` (this directory)
- Per-stage evidence: `~/projects/research/defuzz-experiment-runs/gcc16-aarch64-20261013/`
- Checker bundle/dispatcher: Part II `rep-001/artifacts/`
- Instrumentation diff/catalog: Part IV `rep-001/artifacts/`
- Five audit reports: Part V `rep-001/artifacts/worker-{a..e}-initial/audit-report.json`
- Notes: RUN-STATUS.md, PART-II-FIX.md, PART-II-COMPLETE.md,
  SOURCE-MAP-AND-INSTRUMENTATION.md, PART-II-FIX.md.
