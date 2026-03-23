# RISC-V64 Canary Bug Triage Report (2026-03-13)

## Scope

This report triages the 419 canary bug reports produced by the single-model DeepSeek run against GCC 15.2.0 RISC-V64.

Data sources:

- Fuzz log: `logs/riscv64-canary-full-256.log`
- Bug metadata: `fuzz_out/riscv64/canary/metadata/*.json`
- Bug corpus: `fuzz_out/riscv64/canary/corpus/*/source.c`

## Executive Summary

After deduplicating and reproducing representative samples, the 419 reported "bugs" are false positives with respect to compiler stack-canary bypasses.

Final classification:

- Reported bug records: 419
- Unique bug source hashes: 94
- Confirmed true compiler canary bugs: 0
- Confirmed false positives: 419

The root cause is not in GCC's canary implementation. The generated seeds explicitly disable stack protection on `seed()` via `__attribute__((no_stack_protector))`, but the canary oracle only treats `-fno-stack-protector` in CFlags as a negative case. As a result, unprotected crashing programs were misclassified as compiler canary bypasses.

## Main Findings

### 1. Every reported bug seed disables stack protection in source

Across all 419 bug reports:

- 419/419 bug records contain `no_stack_protector`
- 94/94 unique bug hashes contain `no_stack_protector`
- 0/94 unique bug hashes explicitly request `stack_protect`

This makes the reported crashes invalid as evidence of a compiler-side canary bypass.

Representative examples:

- `id-000693` uses `__attribute__((no_stack_protector)) void seed(...)` and disables protection in `main` as well.
- `id-001938`, `id-001431`, `id-001965` do the same, with only structural variations added to drive compiler coverage.

### 2. The reports are heavily duplicated

The 419 reports collapse to only 94 unique source hashes.

Top repeated hashes:

| Content hash | Reports |
| --- | ---: |
| `1f3f10f1` | 205 |
| `80e306e4` | 20 |
| `eb479deb` | 17 |
| `3d26758f` | 14 |
| `0440305f` | 9 |

The top hash alone contributes 48.9% of all reported bugs.

### 3. Crash types are consistent with unprotected return-address corruption

From the fuzz log:

| Exit code | Count | Meaning |
| --- | ---: | --- |
| `139` | 315 | `SIGSEGV` |
| `132` | 77 | usually `SIGILL` / corrupted control flow |
| `137` | 21 | killed after bad control flow / runtime abnormality |
| `1` | 6 | abnormal termination |

These are exactly the kinds of failures expected once the function frame is left unprotected.

## Reproduction Evidence

### Case A: Representative VLA report (`id-000693`)

Source behavior:

- Original bug seed: `seed()` is annotated with `__attribute__((no_stack_protector))`
- Compile flags still include `-fstack-protector-strong`

Observed reproduction:

1. Compile the original seed as-is.
2. The resulting binary contains no `__stack_chk_fail` symbol.
3. Running with `buf_size=64`, `fill_size=121` produces a crash with signal-style exit (`SIGSEGV`) after printing `SEED_RETURNED`.

Patch-only control experiment:

1. Remove `__attribute__((no_stack_protector))` from `seed()` and `main`.
2. Recompile with the same `-fstack-protector-strong` flags.
3. The resulting binary now contains `__stack_chk_fail`.
4. Running with the same input changes the behavior to `SIGABRT` with `*** stack smashing detected ***`.

Interpretation:

The bug disappears when stack protection is allowed to exist. This is a seed-level false positive, not a compiler bypass.

### Case B: Stack-protected control sample (`id-000520`, non-bug)

This control seed explicitly uses `__attribute__((stack_protect))` on `seed()`.

Observed reproduction:

1. Compile under the same RISC-V64 toolchain.
2. The binary contains `__stack_chk_fail`.
3. Running with `buf_size=64`, `fill_size=121` yields `SIGABRT` and `*** stack smashing detected ***`.

Interpretation:

When the seed actually requests protection, the RISC-V64 compiler behaves as expected.

## Root Cause Analysis

### Root cause 1: Oracle negative-case detection is incomplete

The canary oracle only treats explicit negative CFlags as negative cases, specifically `-fno-stack-protector`.

It does not recognize source-level protection disabling such as:

- `__attribute__((no_stack_protector))` on `seed()`
- equivalent pragma- or attribute-based disabling forms

That gap causes unprotected seeds to be analyzed as if canary protection were enabled.

### Root cause 2: The prompt/template leaves room for invalid seeds

The canary function template:

- explicitly allows adding function attributes
- disables stack protection for `main`
- does not forbid adding `no_stack_protector` to `seed()`

LLM-generated seeds then exploit that degree of freedom and produce high-coverage but invalid test programs.

### Root cause 3: Bug count is inflated by repeated source reuse

The corpus stores many bug seeds with identical source hashes under different IDs, so the raw bug total overstates the number of distinct behaviors.

## Classification

### False positives

All 419 reported bugs fall into this category.

Definition used here:

- The program crashes after return-address corruption.
- But the crashing function has explicitly disabled stack protection.
- Therefore the crash does not demonstrate a failure of the compiler's stack-canary mechanism.

### True bugs

None confirmed in this run.

I found no remaining bug seed that both:

- keeps stack protection enabled on `seed()`
- and still reproduces a canary bypass under the configured RISC-V64 compiler

## Practical Impact On This Run

The run is still useful for coverage and corpus growth, but the reported bug count cannot be used as a security-vulnerability count.

For tomorrow's differential experiment, the correct baseline interpretation is:

- Coverage baseline: valid
- Corpus baseline: valid
- Raw bug count baseline: invalid without triage/filtering
- Security bug baseline after triage: 0 confirmed

## Recommended Fixes

### Immediate

1. Reject any canary seed that contains `__attribute__((no_stack_protector))` on `seed()`.
2. Extend the canary oracle negative-case detection to inspect source text, not only CFlags.
3. Exclude source-level no-protector seeds from bug statistics and reports.

### Prompt-level

1. Update the canary function template to explicitly forbid disabling stack protection on `seed()`.
2. Keep `main` unprotected if desired, but state that `seed()` must remain protectable.
3. Prefer positive guidance such as `__attribute__((stack_protect))` for canary experiments.

### Reporting-level

1. Deduplicate bug counts by `content_hash` in summary tables.
2. Report raw bug count and unique bug count separately.
3. Add a `triaged_bug_count` field to the final summary.

## Bottom Line

The 419 RISC-V64 canary bug reports are not evidence of 419 compiler vulnerabilities.

They are 419 false-positive records caused by a mismatch between:

- seed generation, which frequently disabled protection in source code
- oracle logic, which only recognized CFlag-based disabling

After full triage, this run contains zero confirmed true canary bypass bugs for GCC 15.2.0 RISC-V64.