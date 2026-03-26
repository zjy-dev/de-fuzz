# GCC 15.2.0 AArch64 Canary Stage4 Bug Analysis Report

## Summary

This report analyzes the full run:

- Run ID: `aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1`
- Output: `fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1/aarch64/canary`
- Log: `logs/2026-03-20_02-37-49_CST.log`

Final run summary from the log:

- Iterations: `256`
- Duration: about `11h56m`
- Final BB coverage: `78.21%`
- Oracle bug count: `202`

## Final Verdict

The reported canary bugs in this run are classified as **false positives**, not confirmed GCC stack-canary vulnerabilities.

The dominant root cause is:

- the generated `seed()` function itself is annotated with `__attribute__((no_stack_protector))`

That attribute disables stack protection for the vulnerable function body even when the selected compile profile uses:

- `-fstack-protector`
- `-fstack-protector-strong`
- `-fstack-protector-all`
- `-fstack-protector-explicit`

In other words, the fuzzer is frequently testing a function that has explicitly opted out of SSP at source level. A later crash after overflow is therefore not evidence of a compiler-side canary bypass.

## Evidence

### 1. All analyzed bug seeds disable SSP in source

A batch scan of bug-triggering corpus entries showed:

- bug entries analyzed: `202`
- bug entries without `no_stack_protector` on `seed()`: `0`

Representative source:

- `fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1/aarch64/canary/corpus/id-000637-src-000048-cov-00000-52760934/source.c`

The `seed()` function begins with:

```c
__attribute__((no_stack_protector))
void seed(int buf_size, int fill_size) {
```

The same source hash reappears in later bug seeds, including:

- `id-001276-src-000637-cov-00000-52760934`

### 2. The binary does not contain the normal canary fail path

Representative binary:

- `fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1/aarch64/canary/build/seed_637`

The associated compile profile is:

- `policy-all__threshold-32__pic-default__guard-default`

But symbol inspection shows only:

- `seed`
- `main`

and no `__stack_chk_fail` / `__stack_chk_guard` symbol use was observed in the sample binary inspection.

### 3. Representative disassembly is consistent with an unprotected function

Representative disassembly of `seed` for `seed_637`:

- prologue saves frame and registers
- large local allocation exists
- no canary load/store sequence appears
- function epilogue returns directly

This is consistent with source-level SSP suppression, not compiler failure to honor a protected profile.

### 4. Reproduction behavior matches “unprotected overflow” rather than “protected function bypass”

Representative repro:

```bash
qemu-aarch64 -L target_compilers/gcc-v15.2.0-aarch64-cross-compile/install-aarch64-none-linux-gnu/aarch64-none-linux-gnu/libc \
  fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1/aarch64/canary/build/seed_637 \
  64 41
```

Observed:

- prints sentinel
- exits `0`

With larger overflow:

```bash
qemu-aarch64 -L target_compilers/gcc-v15.2.0-aarch64-cross-compile/install-aarch64-none-linux-gnu/aarch64-none-linux-gnu/libc \
  fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1/aarch64/canary/build/seed_637 \
  64 73
```

Observed:

- prints sentinel
- exits `132`
- QEMU reports `Illegal instruction`

Given the function is explicitly compiled without stack protection, this is expected for a raw return-address smash and does not show a canary bypass in a protected function.

## Bug Pattern Analysis

The reported bugs are highly repetitive rather than independent.

Observed characteristics:

- many repeated `content_hash` clusters
- dominant crash form:
  - overflow size `73`
  - exit code `132`
- minor variants:
  - overflow sizes `41`, `57`, `265`, `377`

The large repetition across many profiles suggests the same source-level opt-out pattern is being rediscovered in multiple profile contexts.

## Representative Clusters

Common repeating source hashes include:

- `4d211553`
- `048c31b1`
- `70e64265`
- `c8382ba6`
- `293fe40d`

These clusters span multiple compile profiles, including sysreg/global guard profiles, but the common factor is still the same: source-level `no_stack_protector`.

## Conclusion

The current bug reports from this Stage4 run are **false positives**.

They do **not** currently demonstrate a GCC AArch64 stack-canary code-generation bug.

They instead demonstrate that the current fuzz pipeline allows generated seeds to opt out of stack protection at source level while the oracle still interprets the resulting crash as a canary bypass candidate.

## Artifacts

- Coverage plot:
  - `scripts/assets/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1_coverage.png`
- Bug plot:
  - `scripts/assets/aarch64_canary_20260320_i256_r8_stage4_llmflags_fixneg1_bugs.png`
- Implementation notes:
  - `docs/aarch64-canary-stage4-implementation-20260320.md`
- Run log:
  - `docs/aarch64-canary-stage4-runlog-20260320.md`
