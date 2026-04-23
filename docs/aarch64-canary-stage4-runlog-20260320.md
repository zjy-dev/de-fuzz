# AArch64 Canary Stage4 Run Log

## Purpose

This file records long-running execution, debugging actions, reruns, and any unexpected issues
encountered while finishing the full AArch64 canary Stage4 workflow.

## Active Run

- Run ID: `aarch64_canary_20260320_i256_r8_stage4_llmflags`
- Output root: `fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags/aarch64/canary`
- Log file: `logs/2026-03-20_01-33-18_CST.log`
- Command:

```bash
go run ./cmd/defuzz fuzz \
  --output fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags \
  --limit 256 \
  --timeout 30
```

## Current Active Run

- Run ID: `aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1`
- Output root: `fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1/aarch64/canary`
- Log file: `logs/2026-03-20_02-37-49_CST.log`
- Command:

```bash
go run ./cmd/defuzz fuzz \
  --output fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1 \
  --limit 256 \
  --timeout 30
```

## Pre-Run Changes

- Integrated deterministic canary flag profiles into the fuzz loop.
- Enabled LLM CFLAGS after profile selection.
- Added filtering for canary-axis conflicting LLM flags.
- Updated prompt generation to expose the active compiler profile to the LLM.
- Rechecked the canary oracle so negative cases use only actually applied flags.

## Status Notes

- The run is currently active.
- No oracle bug has been reported so far.
- No crash/panic/systemic compile failure has been observed so far.
- If a bug appears later, final classification and a dedicated bug analysis report will be added after the run completes.

## Incident Log

- `2026-03-20 02:34 CST`
  - A false-positive bug report was identified for seed `148`.
  - Symptom:
    - log reported `BUG FOUND in seed 148`
    - profile in compile log was `negative-control__fno-stack-protector`
  - Root cause:
    - the negative-control marker was lost when the active prompt profile was copied into the generated seed
    - `clonePromptProfile()` copied name/axes/flags but did not preserve `IsNegativeControl`
    - this caused the canary oracle to treat a negative-control compile as a normal protected sample
  - Impact:
    - this is an implementation bug in DeFuzz integration, not a GCC bug
    - seed `148` must be treated as a false positive
  - Action:
    - patched prompt-context/profile cloning to preserve `IsNegativeControl`
    - added a regression test
    - current full run must be discarded and restarted from scratch

- `2026-03-20 02:38 CST`
  - Discarded the previous `...stage4_llmflags` run.
  - Started a fresh clean rerun `...stage4_llmflags_fixneg1` with the negative-control preservation fix included.

## Checkpoints

- `2026-03-20 01:55 CST`
  - Main process still alive.
  - No panic, fatal error, or oracle bug seen so far.
  - Corpus metadata count reached 11.
  - Observed live profiles include:
    - `policy-strong__threshold-8__pic-default__guard-default`
    - `policy-strong__threshold-8__pic-default__guard-sysreg-off0`
    - `policy-all__threshold-8__pic-default__guard-default`
    - `policy-ssp__threshold-1__pic-default__guard-default`
    - `policy-explicit__threshold-8__pic-default__guard-default`
    - `policy-strong__threshold-32__pic-fPIC__guard-global`
  - Verified explicit-profile seed source contained `__attribute__((stack_protect))`.
  - Verified LLM supplemental flags are being applied after the profile, while conflicting canary flags are dropped.
  - Current target selection has moved from repeated `expand_used_vars:BB66` attempts to `stack_protect_classify_type:BB2`.

- `2026-03-20 02:01 CST`
  - Main process still alive.
  - Metadata count reached 12.
  - No oracle bug seen so far.
  - Observed transient upstream LLM failures in the log:
    - one `502`
    - one `503`
  - The engine recovered and continued without crashing, so this is currently treated as an external transient issue rather than an implementation bug.
  - New profile families observed in live compiles:
    - `policy-strong__threshold-*_guard-global`
    - `policy-explicit__threshold-*`

- `2026-03-20 02:12 CST`
  - Main process still alive.
  - No bug report from oracle yet.
  - Targeting has advanced to `stack_protect_classify_type:BB4`.
  - Additional live LLM supplemental flags observed:
    - `-O0`
    - `-O2`
    - `-fsanitize=address`
  - Conflicting canary-related LLM flags continued to be dropped correctly.
  - No implementation bug has required a restart yet.

- `2026-03-20 02:23 CST`
  - Main process still alive.
  - Metadata count reached 13.
  - No oracle bug seen so far.
  - Iteration has advanced to at least 18.
  - New live combinations observed include:
    - `policy-ssp__threshold-*__guard-global`
    - `policy-all__threshold-*__guard-global`
    - `policy-strong__threshold-*__guard-global`
  - Additional LLM supplemental flags observed in applied form:
    - `-fsanitize=address`
  - Conflicting LLM canary flags are still being dropped as expected.

- `2026-03-20 03:26 CST`
  - The clean rerun `...fixneg1` is still alive and has not reproduced the earlier negative-control false positive.
  - No oracle bug has been reported in the rerun so far.
  - Metadata count reached 11 in the rerun.
  - New accepted supplemental LLM flags observed:
    - `-fno-asynchronous-unwind-tables`
    - `-fno-unwind-tables`
    - `-fno-exceptions`
    - `-fno-asan`
    - `-fno-sanitize=address`
    - `-fno-sanitize=all`
  - The run remains slow but stable; no further implementation bug has required intervention.

- `2026-03-20 14:35 CST`
  - The clean rerun completed.
  - Summary from log:
    - iterations: `256`
    - duration: about `11h56m`
    - final bug count: `202`
    - final BB coverage: `78.21%`
  - Plots generated:
    - `scripts/assets/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1_coverage.png`
    - `scripts/assets/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1_bugs.png`
  - Post-run analysis report written:
    - `docs/gcc-15.2.0-aarch64-canary-stage4-bug-report-20260320.md`
  - Current conclusion:
    - the observed bug reports are false positives
    - representative bug seeds explicitly annotate `seed()` with `__attribute__((no_stack_protector))`
    - this disables SSP at source level, so later overflow crashes are not evidence of a compiler-side canary bypass
