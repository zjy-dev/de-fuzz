# Part II completion — GCC16.2/AArch64 full run (2026-10-13)

## Checker authoring (Part II) — COMPLETED

After the three fixes (out-of-repo output+checker roots, per-attempt budget
200k→1M / stage 20M→40M, `strict_usage:false` in the engineering-local config),
Part II completed successfully:

- input accepted invariants: 50; selected for AArch64: 43 (7 excluded, out-of-ISA)
- **final passed: 43/43**; bundle_ready=true; coverage_complete=true
- agent attempts: 52 (first-draft `go test` failures self-repaired; several
  converged on attempt 2)
- model calls: 685; tokens: 18,065,918; **usage_missing: 0**; provider errors: 0
- wall time: 14:46:30 → 16:27 UTC (~1h41m)
- artifacts: `checker-bundle-manifest.json` (status ready),
  `checker-catalog.json`, per-invariant first/final patches.

Comparison: prior GCC17 OpenCode run yielded 1/42 passing checkers; this
Pi run yields 43/43. The repair loop (gofmt + go test + go vet gate) is the
difference — first drafts that fail the agent's own tests are fixed.

Note on token comparability: this is an engineering `run-all`-equivalent with
`strict_usage:false`. Per-call usage was in fact complete (0 missing), but
formal paper evidence still needs a later strict rerun.

## Chain progression

- [3/5] source-mapping started 16:27 UTC after Part II exit=0.
- [4/5] log-instrumentation (deterministic) follows mapping.
- [5/5] agent-audit follows instrumentation, consuming the ready bundle.

Monitor: `bash .work/gcc16-aarch64-run/status.sh`; watcher ticks in
`.work/gcc16-aarch64-run/watch.log`.
