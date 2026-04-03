# Option Post-Pass Implementation Notes

Date: 2026-04-02

This document explains the newly added `option-postpass` framework, how it is configured, how it runs, and how to use it in practice.

## 1. Why This Was Added

The online `defuzz fuzz` loop is good at producing source-level interesting seeds, but it mixed two different concerns:

- source generation / mutation with LLM,
- defense-related compiler option exploration.

The new `option-postpass` stage separates these concerns.

It reuses the existing interesting seed pool under `corpus/` and deterministically replays those seeds across strategy-managed compiler option combinations.

This makes the workflow more reproducible and aligns better with the intended model:

- first build a seed pool,
- then traverse defense-related option space on top of that pool.

For the active GCC 15.2 canary configs, this is now the intended place to do deterministic canary option traversal. Online canary fuzzing no longer injects those deterministic canary matrices when `flag_strategy.enabled=false`.

## 2. New CLI Command

The CLI now exposes:

- `defuzz generate`
- `defuzz fuzz`
- `defuzz option-postpass`

`option-postpass` does not call the LLM for new source code. It only replays existing corpus seeds.

## 3. High-Level Execution Flow

`defuzz option-postpass` performs the following steps:

1. load config and resolve the active `isa` + `strategy`,
2. load the post-pass matrix from `configs/postpass.yaml`,
3. scan `{run-root}/corpus/` for persisted seed directories,
4. require `compile_command.json` for each seed,
5. reconstruct a baseline compile flag set from the recorded compilation,
6. remove strategy-owned flags from that baseline,
7. materialize deterministic option combinations for the active strategy/ISA,
8. replay each `(seed, combination)` pair:
   - rewrite `flag_profile.json` for the attempt,
   - compile,
   - reuse the saved test cases,
   - execute,
   - invoke the configured oracle,
9. save all attempt artifacts under `postpass/<run-name>/`.

The main corpus is not mutated by this command.

Important path rule:

- `--run-dir` must point to the run root containing `corpus/`,
- not to the `corpus/` directory itself.

## 4. New Config File: `configs/postpass.yaml`

The post-pass matrix is now driven by a dedicated config file:

- [postpass.yaml](/home/bigsmater/de-fuzz/configs/postpass.yaml)

This file is strategy-oriented and defense-agnostic.

Each strategy entry contains:

- `workers`: default concurrency for this strategy,
- `group_order`: deterministic traversal order of option groups,
- `strip_rules`: which baseline flag families are owned by the strategy and must be removed before replay,
- `groups.common`: common option groups,
- `groups.by_isa`: ISA-specific option groups,
- `isa_options`: placeholder materialization inputs such as guard registers.

### Current Strategy Entries

Current entries are:

- `canary`
- `fortify`
- `crash`
- `llm`

`canary` currently has the richest option matrix.
`fortify` already has a generic matrix for optimization / fortify level / stack protector policy.
`crash` and `llm` currently materialize to a single default combination, which keeps the framework strategy-compatible without inventing unsupported policy semantics.

## 5. How `workers` Now Works

`workers` is now written into `postpass.yaml` as a per-strategy default.

Current defaults are:

- `canary.workers: 4`
- `fortify.workers: 4`
- `crash.workers: 4`
- `llm.workers: 2`

Runtime precedence is:

1. explicit CLI `--workers N`
2. strategy default from `postpass.yaml`
3. fallback `1`

So if you do not pass `--workers`, the command now uses the strategy default from config.

## 6. Target Selection: Run Dir, ISA, Strategy

You can now choose the target run in two ways.

### 6.1 Direct run directory

Use `--run-dir` when you already know the exact fuzz run root:

```bash
go run ./cmd/defuzz option-postpass \
  --run-dir fuzz_out/loongarch64/canary
```

This points directly to the directory that contains:

- `corpus/`
- `metadata/`
- `state/`
- `build/`

Do not pass the `corpus/` subdirectory itself. If you pass `.../corpus`, the command will look for `.../corpus/corpus` and fail by design because `--run-dir` is defined as the run root.

### 6.2 Composed path from output + ISA + strategy

Use `--output`, `--isa`, and `--strategy`:

```bash
go run ./cmd/defuzz option-postpass \
  --output fuzz_out \
  --isa loongarch64 \
  --strategy canary
```

This resolves to:

- `fuzz_out/loongarch64/canary`

`--isa` and `--strategy` now override `config.yaml` when selecting the compiler config and the default run directory.

## 7. Concurrency Usage

### Use strategy default from config

```bash
go run ./cmd/defuzz option-postpass \
  --run-dir fuzz_out/loongarch64/canary
```

This uses `workers` from `configs/postpass.yaml`.

### Override from CLI

```bash
go run ./cmd/defuzz option-postpass \
  --run-dir fuzz_out/loongarch64/canary \
  --workers 8
```

### QEMU replay with moderate concurrency

```bash
go run ./cmd/defuzz option-postpass \
  --output fuzz_out \
  --isa aarch64 \
  --strategy canary \
  --use-qemu \
  --workers 4 \
  --run-name aarch64_canary_pp_w4
```

### Practical advice

Recommended starting points:

- native execution: `--workers 8`
- QEMU execution: `--workers 2` or `--workers 4`

Reason:

- compile itself is expensive,
- many strategy oracles also execute binaries multiple times,
- QEMU increases per-attempt cost significantly.

Very large worker counts are usually counterproductive on QEMU-heavy runs.

If you need build-time parallelism rather than replay-time parallelism, set `JOBS=<n>` when rebuilding the instrumented compiler. `workers` only controls concurrent post-pass replay attempts.

## 8. Baseline Reconstruction Semantics

The command does not blindly recompile with current config defaults.

Instead it uses each seed’s:

- `compile_command.json`

to reconstruct the baseline compile semantics.

Current reconstruction logic:

- start from `config_cflags + applied_llm_cflags`,
- if that is empty, fall back to `effective_flags` minus `prefix_flags`,
- remove strategy-owned flags using `strip_rules`,
- append the selected option combination,
- let the compiler infrastructure re-add prefix flags normally.

This keeps stable environment/toolchain flags while allowing defense-option replay.

## 9. Strategy Compatibility

The framework is generic at the command/config level.

It does not hard-code:

- canary-specific option names,
- canary-specific oracle logic,
- canary-only output semantics.

Instead:

- option groups are strategy-defined in `postpass.yaml`,
- owned flag stripping is strategy-defined in `postpass.yaml`,
- replayed analysis is delegated to the configured oracle.

So:

- `canary` uses `CanaryOracle`,
- `fortify` uses `FortifyOracle`,
- `crash` uses `CrashOracle`,
- `llm` uses `LLMOracle`.

## 10. Test Case Reuse

This stage reuses the original persisted test cases.

For each replayed attempt:

- `source.c` is copied into the attempt directory,
- `testcases.json` is copied if present,
- no new test case is generated,
- no source mutation occurs.

This preserves the seed as a source-level artifact while varying compiler options only.

## 11. Output Layout

Results are written under:

- `{run-root}/postpass/{run-name}/`

For each seed and combination:

- `{run-root}/postpass/{run-name}/{seed-dir}/{combo-name}/source.c`
- `{run-root}/postpass/{run-name}/{seed-dir}/{combo-name}/testcases.json`
- `{run-root}/postpass/{run-name}/{seed-dir}/{combo-name}/flag_profile.json`
- `{run-root}/postpass/{run-name}/{seed-dir}/{combo-name}/compile_command.json`
- `{run-root}/postpass/{run-name}/{seed-dir}/{combo-name}/attempt.json`

And for the whole run:

- `{run-root}/postpass/{run-name}/summary.json`

## 12. Logging and Bug Visibility

The command now logs important replay outcomes in real time.

### Bug detected

When a bug is found, the logger emits:

- seed id,
- combination name,
- attempt directory,
- bug description.

### Compile failure

Compile failures are logged with:

- seed id,
- combination name,
- attempt directory,
- compile error.

### Oracle error

Oracle runtime failures are logged with:

- seed id,
- combination name,
- attempt directory,
- oracle error.

### Final summary

At the end of the run, the logger prints:

- seeds scanned,
- combinations,
- attempts,
- bug count,
- compile failure count,
- oracle error count,
- skipped seed count.

If `--log-dir` is provided, all of this is also written to the log file.

## 13. Current Limitations

Current limitations of this implementation:

- it does not promote replayed variants back into the main corpus,
- it does not yet provide `--seed-limit` or `--combo-filter`,
- it does not yet deduplicate semantically identical combinations across strategies,
- fortify is now fully wired for GCC 15.2 on `aarch64`, `riscv64`, and `loongarch64`, but live LLM-backed online fuzzing can still be blocked by external provider authentication state,
- `go test ./...` remains unsuitable at repo root because `target_compilers/` vendors upstream GCC Go testsuite content.

These do not block the new command itself.

## 14. Files Added or Changed

Core implementation:

- [option_postpass.go](/home/bigsmater/de-fuzz/cmd/defuzz/app/option_postpass.go)
- [config.go](/home/bigsmater/de-fuzz/internal/postpass/config.go)
- [runner.go](/home/bigsmater/de-fuzz/internal/postpass/runner.go)
- [config_test.go](/home/bigsmater/de-fuzz/internal/postpass/config_test.go)
- [baseline_test.go](/home/bigsmater/de-fuzz/internal/postpass/baseline_test.go)

Config:

- [postpass.yaml](/home/bigsmater/de-fuzz/configs/postpass.yaml)

Related support:

- [root.go](/home/bigsmater/de-fuzz/cmd/defuzz/app/root.go)
- [flag_strategy.go](/home/bigsmater/de-fuzz/internal/config/flag_strategy.go)
- [config.go](/home/bigsmater/de-fuzz/internal/config/config.go)

Docs:

- [project-understanding-20260402.md](/home/bigsmater/de-fuzz/docs/project-understanding-20260402.md)
- this document

## 15. Recommended Example Commands

Use config default workers:

```bash
go run ./cmd/defuzz option-postpass \
  --run-dir fuzz_out/loongarch64/canary \
  --run-name loongarch64_canary_pp_default
```

Use explicit worker override:

```bash
go run ./cmd/defuzz option-postpass \
  --run-dir fuzz_out/loongarch64/canary \
  --workers 8 \
  --run-name loongarch64_canary_pp_w8
```

Select target by ISA and strategy:

```bash
go run ./cmd/defuzz option-postpass \
  --output fuzz_out \
  --isa riscv64 \
  --strategy canary \
  --workers 4 \
  --run-name riscv64_canary_pp_w4
```

Write logs to files:

```bash
go run ./cmd/defuzz option-postpass \
  --run-dir fuzz_out/loongarch64/canary \
  --workers 8 \
  --log-dir logs \
  --run-name loongarch64_canary_pp_w8
```
