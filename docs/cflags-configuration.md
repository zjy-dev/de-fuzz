# CFlags Configuration Guide

## 1. Purpose

This document describes the current flag-ownership model in DeFuzz. The important point is no longer just "where to put flags", but "which layer owns which flags" so experiments stay reproducible and strategies stay extensible.

## 2. Four Flag Sources

DeFuzz now has four distinct flag sources:

1. `compiler.cflags`
   - stable toolchain and environment flags
   - examples: `--sysroot`, `-B...`, `-L...`, neutral baseline optimization
2. `compiler.fuzz.flag_strategy`
   - deterministic strategy-owned flags for online fuzzing
   - examples: canary policy/threshold/layout matrices, fortify mode matrices
3. `Seed.CFlags`
   - dynamic per-seed flags emitted by the LLM
4. `configs/postpass.yaml`
   - deterministic replay-time option traversal for persisted corpus seeds

These sources should not be mixed casually.

## 3. Actual Compile Merge Order

The compiler layer merges flags in this order:

1. prefix flags inferred by the tool,
2. `compiler.cflags`,
3. selected `FlagProfile` flags,
4. filtered `Seed.CFlags`.

This order is what you see later in `compile_command.json.effective_flags`.

If `compiler.cflags` is omitted, the compiler falls back to the neutral baseline:

```text
-O0
```

## 4. What Belongs in `compiler.cflags`

`compiler.cflags` should contain only stable flags needed to make the compiler invocation valid and reproducible.

Typical examples:

```yaml
compiler:
  cflags:
    - "-O0"
    - "--sysroot=/path/to/sysroot"
    - "-B/path/to/gcc-final-build/gcc"
    - "-B/path/to/install/lib/gcc/<triplet>/<version>"
    - "-L/path/to/sysroot/lib64"
```

These flags are not supposed to represent the defense search space. They describe the execution environment.

## 5. What Belongs in `flag_strategy`

`compiler.fuzz.flag_strategy` owns deterministic strategy-specific option axes for online fuzzing.

Current examples:

- canary:
  - `policy`
  - `threshold`
  - `pic_mode`
  - ISA-specific `guard_source`
  - LoongArch64-specific `layout`
- fortify:
  - `optimization`
  - `fortify_mode`
  - `stack_protector_mode`

If a flag family is owned by `flag_strategy`, it should not also be hard-coded into `compiler.cflags`.

## 6. What Belongs in LLM-Generated `Seed.CFlags`

`Seed.CFlags` is for dynamic LLM exploration when the current strategy allows it.

Example:

```c
void seed(int buf_size, int fill_size) {
    char buffer[64];
    memset(buffer, 'A', fill_size);
}
// ||||| CFLAGS_START |||||
-fstack-protector-all
// ||||| CFLAGS_END |||||
```

The compiler records:

- requested LLM flags,
- applied LLM flags,
- dropped LLM flags,
- final effective flags.

For historical analysis, always prefer `compile_command.json` over `cflags.json`, because `compile_command.json` reflects the real final argv.

## 7. Current Strategy Policy

### GCC 15.2 Canary

The active GCC 15.2 canary configs now use:

```yaml
flag_strategy:
  enabled: false
```

This means:

- online canary fuzzing keeps only stable `compiler.cflags` plus LLM-generated `Seed.CFlags`,
- deterministic canary option traversal should happen in `option-postpass`,
- disabling `flag_strategy` is now the correct way to stop online injection of the discovered canary layout/policy option matrix.

### GCC 15.2 Fortify

The active GCC 15.2 fortify configs keep `flag_strategy.enabled: true`.

That is intentional because current fortify online fuzzing still uses deterministic fortify profile rotation. The current ranked default fortify profile is effectively:

```text
-O2 -fhardened -fno-stack-protector
```

## 8. Post-Pass Interaction

`option-postpass` reconstructs the historical baseline from each seed’s `compile_command.json`, strips strategy-owned flags defined in `configs/postpass.yaml`, then appends deterministic replay combinations.

That gives the repository a clean split:

- online fuzzing discovers interesting seeds,
- post-pass traverses the deterministic defense-option space on top of that seed pool.

For reproducibility, this is better than hiding more option traversal inside the online loop.

For `option-postpass`, the replay-time compile option changes come from exactly these sources:

1. historical baseline from the original seed's `compile_command.json`
   - first choice: `config_cflags + applied_llm_cflags`
   - fallback: `effective_flags - prefix_flags`
2. strategy-owned historical flags removed by `strip_rules`
3. replay combination flags materialized from `configs/postpass.yaml`
4. prefix flags re-added automatically by the compiler wrapper

Just as important, `option-postpass` replay does **not** take its changing option set from:

- the current compiler YAML's `compiler.cflags`,
- the current online `compiler.fuzz.flag_strategy`,
- new LLM-generated `Seed.CFlags`.

So when reading this document, keep the distinction strict:

- sections 2-7 describe the general and online ownership model,
- this section describes the replay-specific source of option changes in `option-postpass`.

## 9. `-B` and Prefix Paths

DeFuzz uses two related mechanisms:

1. prefix path inferred from the GCC binary directory
   - used to find `cc1`, assembler, linker, and related compiler components
2. explicit `-B...` entries inside `compiler.cflags`
   - used for runtime libraries such as `crtbegin.o` and `libgcc`

Multiple `-B` entries are valid and expected.

## 10. Reproducibility Rules

1. Put only stable environment flags in `compiler.cflags`.
2. Put deterministic strategy-owned flags in `flag_strategy` or `postpass.yaml`, not in `compiler.cflags`.
3. Use `compile_command.json` as the historical source of truth.
4. Prefer `--isa` and `--strategy` CLI overrides instead of relying on the current `configs/config.yaml`.
5. If an experiment is meant to compare source behavior only, keep the stable `compiler.cflags` fixed.

## 11. Config Examples

### Cross-compiler baseline

```yaml
compiler:
  path: "target_compilers/.../gcc/xgcc"
  cflags:
    - "-O0"
    - "--sysroot=target_compilers/.../libc"
    - "-Btarget_compilers/.../gcc-final-build/gcc"
    - "-Btarget_compilers/.../lib/gcc/<triplet>/<version>"
    - "-Ltarget_compilers/.../lib64"
```

### Fortify with deterministic online profiles

```yaml
compiler:
  cflags:
    - "-O0"
    - "--sysroot=..."
  fuzz:
    flag_strategy:
      enabled: true
      mode: "matrix"
      allow_llm_cflags: true
```

### Canary with online deterministic profiles disabled

```yaml
compiler:
  cflags:
    - "-O0"
    - "--sysroot=..."
  fuzz:
    flag_strategy:
      enabled: false
```
