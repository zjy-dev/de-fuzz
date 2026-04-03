# Multi-File CFG and Fortify Update Notes

Date: 2026-04-02

This document records the current update set that completed the shift from single-file CFG assumptions to reproducible multi-file CFG guidance, while also promoting fortify to a first-class strategy.

## 1. Why This Update Was Needed

Three issues had to be resolved together.

First, many configs already listed target functions from multiple GCC source files, but the instrumented compiler only emitted `cfgexpand.cc.*.cfg` in practice. That meant the configured target surface was broader on paper than what the analyzer could actually target.

Second, online fuzzing was mixing two responsibilities:

- discovering interesting source-level seeds,
- traversing deterministic defense-owned option combinations.

Third, fortify had partial scaffolding but was not yet documented and validated as a reproducible multi-ISA target.

## 2. What Changed

### 2.1 Multi-file CFG is now part of the baseline design

The analyzer already supported multiple CFG dump files. The missing piece was the actual compiler build artifacts.

The checked-in GCC 15.2 instrumented builds now generate and use real multi-file CFG dumps.

Current validated whitelist:

- `cfgexpand.o`
- `function.o`
- `calls.o`
- `targhooks.o`
- `builtins.o`
- `gimple-fold.o`
- `c-family/c-opts.o`
- `linux.o`
- `aarch64.o`
- `riscv.o`

Project rule going forward:

- every configured target function is required,
- if its source file has no `.cfg`, generate that `.cfg`,
- do not solve missing CFG coverage by silently deleting or commenting out targets.

### 2.2 Canary online fuzzing no longer injects deterministic canary matrices

For the active GCC 15.2 canary configs:

- `flag_strategy.enabled: false`

That means online canary fuzzing now keeps:

- stable `compiler.cflags`,
- optional LLM-generated `Seed.CFlags`.

Deterministic canary option traversal is now expected to happen in `option-postpass`.

### 2.3 Fortify is now a real supported strategy

Added or completed:

- GCC 15.2 fortify configs for `aarch64`, `riscv64`, and `loongarch64`,
- fortify initial seeds for those three ISAs,
- fortify understanding docs and template files,
- fortify multi-file CFG paths,
- compiler-side filtering and prompt constraints for fortify-specific flags.

The current default fortify online profile is effectively:

- `-O2 -fhardened -fno-stack-protector`

### 2.4 Option-postpass became the reproducible deterministic traversal stage

`option-postpass` now has the role it should have had from the beginning:

- reuse persisted interesting seeds from `corpus/`,
- traverse deterministic option matrices from `configs/postpass.yaml`,
- reuse existing test cases and the configured oracle,
- write attempt-local artifacts under `postpass/<run-name>/`.

Default concurrency is now stored in `configs/postpass.yaml` via per-strategy `workers`.

The source of replay-time compile option changes is intentionally constrained to match the implementation notes:

- baseline comes from the original seed's `compile_command.json`,
- strategy-owned historical flags are removed by `strip_rules`,
- new deterministic replay flags come from materialized combos in `configs/postpass.yaml`,
- prefix flags are re-added by the compiler wrapper.

This means replay-time option variation is **not** sourced from current YAML defaults or new LLM-generated flags.

## 3. Current Validated State

### 3.1 Multi-file CFG artifacts now present

AArch64:

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `builtins.cc.015t.cfg`
- `gimple-fold.cc.015t.cfg`
- `linux.cc.015t.cfg`
- `aarch64.cc.015t.cfg`
- `c-family/c-opts.cc.015t.cfg`

RISC-V:

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `builtins.cc.015t.cfg`
- `gimple-fold.cc.015t.cfg`
- `linux.cc.015t.cfg`
- `riscv.cc.015t.cfg`
- `c-family/c-opts.cc.015t.cfg`

LoongArch64:

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `builtins.cc.015t.cfg`
- `gimple-fold.cc.015t.cfg`
- `linux.cc.015t.cfg`
- `c-family/c-opts.cc.015t.cfg`

### 3.2 Fortify probe result

After regenerating the relevant CFGs, relinking `cc1`, and strengthening fortify bootstrap seeds, the current focused probe result on all three GCC 15.2 cross ISAs is:

- 14 / 15 configured fortify target functions reached,
- 236 / 473 target basic blocks covered.

Covered functions include:

- `c_finish_options`
- `fold_builtin_object_size`
- `maybe_emit_chk_warning`
- `expand_builtin_memory_chk`
- `avoid_folding_inline_builtin`
- `fold_call_expr`
- `gimple_fold_builtin`
- `gimple_fold_builtin_memory_chk`
- `gimple_fold_builtin_stxcpy_chk`
- `gimple_fold_builtin_sprintf_chk`
- `gimple_fold_builtin_strncat_chk`
- `gimple_fold_builtin_stxncpy_chk`
- `gimple_fold_builtin_snprintf_chk`
- `linux_fortify_source_default_level`

Remaining cold function:

- `default_fortify_source_default_level`

This remaining cold function is expected on current GNU/Linux targets because the Linux-specific hook overrides the generic targhooks implementation.

## 4. Reproducibility Procedure

### 4.1 Full build parallelism

The checked-in GCC cross-build scripts already honor:

```bash
JOBS=<n> ./build-gcc-instrumented.sh
```

Examples:

```bash
cd target_compilers/gcc-v15.2.0-aarch64-cross-compile
JOBS=16 ./build-gcc-instrumented.sh
```

```bash
cd target_compilers/gcc-v15.2.0-riscv64-cross-compile
JOBS=16 ./build-gcc-instrumented.sh
```

```bash
cd target_compilers/gcc-v15.2.0-loongarch64-cross-compile
JOBS=16 ./build-gcc-instrumented.sh
```

### 4.2 Incremental CFG regeneration

If you change the whitelist or add new target files, rebuilding the entire compiler is not always necessary. You can rebuild the affected objects and then relink `cc1`.

Important:

- rebuilding only the object file is not enough,
- you must relink `cc1` after those object rebuilds.

#### AArch64

Run from the repository root:

```bash
REPO_ROOT=$(pwd)
AARCH64_BUILD="$REPO_ROOT/target_compilers/gcc-v15.2.0-aarch64-cross-compile/build-aarch64-none-linux-gnu/gcc-final-build/gcc"
AARCH64_SRC="$REPO_ROOT/target_compilers/gcc-v15.2.0-aarch64-cross-compile/gcc/gcc"

make -C "$AARCH64_BUILD" -j8 \
  -W "$AARCH64_SRC/cfgexpand.cc" \
  -W "$AARCH64_SRC/function.cc" \
  -W "$AARCH64_SRC/calls.cc" \
  -W "$AARCH64_SRC/targhooks.cc" \
  -W "$AARCH64_SRC/builtins.cc" \
  -W "$AARCH64_SRC/gimple-fold.cc" \
  -W "$AARCH64_SRC/c-family/c-opts.cc" \
  -W "$AARCH64_SRC/config/linux.cc" \
  -W "$AARCH64_SRC/config/aarch64/aarch64.cc" \
  cfgexpand.o function.o calls.o targhooks.o builtins.o gimple-fold.o c-family/c-opts.o linux.o aarch64.o

make -C "$AARCH64_BUILD" -j8 cc1
```

#### RISC-V

```bash
REPO_ROOT=$(pwd)
RISCV_BUILD="$REPO_ROOT/target_compilers/gcc-v15.2.0-riscv64-cross-compile/build-riscv64-unknown-linux-gnu/gcc-final-build/gcc"
RISCV_SRC="$REPO_ROOT/target_compilers/gcc-v15.2.0-riscv64-cross-compile/gcc/gcc"

make -C "$RISCV_BUILD" -j8 \
  -W "$RISCV_SRC/cfgexpand.cc" \
  -W "$RISCV_SRC/function.cc" \
  -W "$RISCV_SRC/calls.cc" \
  -W "$RISCV_SRC/targhooks.cc" \
  -W "$RISCV_SRC/builtins.cc" \
  -W "$RISCV_SRC/gimple-fold.cc" \
  -W "$RISCV_SRC/c-family/c-opts.cc" \
  -W "$RISCV_SRC/config/linux.cc" \
  -W "$RISCV_SRC/config/riscv/riscv.cc" \
  cfgexpand.o function.o calls.o targhooks.o builtins.o gimple-fold.o c-family/c-opts.o linux.o riscv.o

make -C "$RISCV_BUILD" -j8 cc1
```

#### LoongArch64

```bash
REPO_ROOT=$(pwd)
LOONG_BUILD="$REPO_ROOT/target_compilers/gcc-v15.2.0-loongarch64-cross-compile/build-loongarch64-unknown-linux-gnu/gcc-final-build/gcc"
LOONG_SRC="$REPO_ROOT/target_compilers/gcc-v15.2.0-loongarch64-cross-compile/gcc/gcc"

make -C "$LOONG_BUILD" -j8 \
  -W "$LOONG_SRC/cfgexpand.cc" \
  -W "$LOONG_SRC/function.cc" \
  -W "$LOONG_SRC/calls.cc" \
  -W "$LOONG_SRC/targhooks.cc" \
  -W "$LOONG_SRC/builtins.cc" \
  -W "$LOONG_SRC/gimple-fold.cc" \
  -W "$LOONG_SRC/c-family/c-opts.cc" \
  -W "$LOONG_SRC/config/linux.cc" \
  cfgexpand.o function.o calls.o targhooks.o builtins.o gimple-fold.o c-family/c-opts.o linux.o

make -C "$LOONG_BUILD" -j8 cc1
```

### 4.3 Online smoke command

Example fortify smoke run:

```bash
go run ./cmd/defuzz fuzz \
  --isa aarch64 \
  --strategy fortify \
  --limit 1 \
  --output /tmp/defuzz_fortify_smoke \
  --timeout 30
```

If LLM provider authentication is unavailable, the run can still fail after initialization. That is an external dependency issue, not a CFG/configuration issue.

### 4.4 Correct option-postpass invocation

Correct:

```bash
go run ./cmd/defuzz option-postpass \
  --run-dir ./fuzz_out/riscv64/canary
```

Incorrect:

```bash
go run ./cmd/defuzz option-postpass \
  --run-dir ./fuzz_out/riscv64/canary/corpus
```

`--run-dir` is defined as the run root. Passing the `corpus/` subdirectory makes the command look for `corpus/corpus`.

## 5. Why This Design Is Better

### Reproducibility

- multi-file CFGs make the real target surface explicit,
- `option-postpass` removes randomness from defense-option traversal,
- `compile_command.json` and `postpass/<run-name>/attempt.json` preserve the effective command and result,
- `workers` now lives in version-controlled config.

### Extensibility

- future strategies can reuse the same corpus-first then deterministic-replay workflow,
- new target functions can be added by extending the CFG whitelist instead of changing analyzer logic,
- `crash` and `llm` already have placeholders in `postpass.yaml`,
- fortify proves the framework can expand beyond canary without special-casing the pipeline.

## 6. Rules for Future Contributors

1. If a strategy adds target functions from a new compiler source file, add that source file to the instrumentation whitelist and generate its `.cfg`.
2. Do not hide missing instrumentation by shrinking the target list.
3. Keep stable toolchain flags in `compiler.cflags`.
4. Keep deterministic strategy-owned option traversal in `flag_strategy` or `postpass.yaml`.
5. Use `option-postpass` for seed-pool-based exhaustive traversal.
6. Prefer explicit `--isa` and `--strategy` CLI overrides for experiment scripts.
