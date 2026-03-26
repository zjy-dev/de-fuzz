# AArch64 Canary Stage4 Integration Notes

## 1. Final Scope

This change set completes the AArch64 canary Stage3/Stage4 integration for DeFuzz.

Implemented areas:

- Stage3 AArch64/general target functions were added into the canary config.
- Stage4 canary options were turned into a deterministic compiler flag strategy.
- The fuzz loop now tracks compiler flag profiles as first-class runtime state.
- Compile records now persist both the selected profile and the LLM flag decision.
- LLM CFLAGS are enabled again, but only as **post-profile supplemental flags**.
- Conflicting canary-related LLM flags are filtered before compilation.
- Canary oracle now uses the **actually applied** flags instead of raw requested flags.

Out of scope:

- No other ISA was generalized in this change.
- No fortify / PIE / CF-protection generic framework was added.

## 2. Main Code Changes

### 2.1 Config and Target Coverage

File:

- `configs/gcc-v15.2.0-aarch64-canary.yaml`

Changes:

- Added `compiler.fuzz.flag_strategy`.
- Switched canary behavior flags out of static `compiler.cflags` and into the selector.
- Added missing Stage3 functions for:
  - `function.cc`
  - `calls.cc`
  - `targhooks.cc`
  - `config/aarch64/aarch64.cc`
- Kept CFG-guided targeting limited to the current CFG dump source file to avoid analyzer mismatch.

### 2.2 Runtime Flag Profile Model

Files:

- `internal/seed/seed.go`
- `internal/seed/flag_profile.go`
- `internal/seed/storage.go`
- `internal/seed/compilation_record.go`

Changes:

- Added `FlagProfile` as a dedicated runtime data model.
- Persisted `flag_profile.json` alongside seeds.
- Extended `compile_command.json` with:
  - `profile_name`
  - `profile_flags`
  - `profile_axes`
  - `is_negative_control`
  - `llm_cflags`
  - `applied_llm_cflags`
  - `dropped_llm_cflags`
  - `llm_cflags_applied`

### 2.3 Compiler Merge Order and LLM Conflict Filtering

File:

- `internal/compiler/compiler.go`

Final compile order:

1. Prefix flags
2. Config `compiler.cflags`
3. Selected flag profile
4. Filtered LLM CFLAGS

LLM CFLAGS are now appended after the selected profile, but the compiler drops canary-axis conflicts.

Currently blocked LLM flag families:

- `-fstack-protector*`
- `-fno-stack-protector*`
- `--param=ssp-buffer-size=*`
- `-mstack-protector-guard*`
- `-fpic`
- `-fPIC`
- `-fpie`
- `-fPIE`
- `-fhardened`

This preserves the selector as the source of truth for canary policy/threshold/PIC/guard axes while still letting the LLM add supplemental flags such as `-O0`, warning toggles, or unrelated experimentation flags.

### 2.4 Prompt/Profile Coordination

Files:

- `internal/fuzz/engine.go`
- `internal/prompt/constraint.go`

Changes:

- The engine now selects a profile **before** each LLM prompt is built.
- The active profile is injected into `TargetContext`.
- Constraint/refinement/compile-error prompts now explicitly tell the model:
  - which profile is already selected
  - which flags are already active
  - that profile-reserved flag families will be filtered if emitted in `CFLAGS`

This means the LLM no longer chooses flags blind. It now reasons relative to the already selected compiler profile.

### 2.5 Deterministic Canary Scheduler

File:

- `internal/fuzz/flag_strategy.go`

Implemented behavior:

- Deterministic per-target profile rotation
- AArch64-specific profiles including:
  - policy variants
  - `ssp-buffer-size` variants
  - `-fPIC`
  - `guard=global`
  - `guard=sysreg` with `tpidr_el0` and offsets `0` / `16`
- Negative control support remains implemented through a dedicated profile path

### 2.6 Oracle Recheck

File:

- `internal/oracle/canary_oracle.go`

Final negative-case logic:

1. If `FlagProfile.IsNegativeControl` is set, treat as negative case.
2. Otherwise, only treat LLM-provided negative flags as active when:
   - `LLMCFlagsApplied == true`
   - and the flag exists in `AppliedLLMCFlags`

This is the correct behavior.

Why this is correct:

- The old behavior looked at raw requested `Seed.CFlags`, which could misclassify a seed as negative even when the compiler never used those flags.
- The new behavior matches the real compiler invocation.
- Therefore the oracle now reasons over the same flag set that actually reached GCC.

I rechecked the critical logic path and did **not** find an oracle regression.

## 3. Final Workflow

### 3.1 Initial Seeds

- Initial seeds are loaded.
- Each initial seed gets the default canary profile.
- They compile under the default profile, not under raw static canary flags.

### 3.2 Constraint Solving

For each target BB:

1. The scheduler selects the next profile for that target.
2. The selected profile is injected into the LLM prompt.
3. The LLM may still emit `CFLAGS`.
4. During compilation, DeFuzz appends those LLM flags **after** the profile.
5. Any flag that conflicts with selector-controlled canary axes is dropped.
6. The compile record persists both the raw requested flags and the filtered result.

### 3.3 Oracle

- Oracle evaluates the compiled binary.
- Negative case judgment uses:
  - negative profile state, or
  - applied negative LLM flags only
- False-positive filtering by sentinel remains unchanged.

### 3.4 Reproducibility

Each interesting seed directory can now answer:

- Which Stage4 profile compiled it
- Which flags the selector chose
- Which LLM flags were requested
- Which LLM flags were actually applied
- Which LLM flags were dropped due to profile conflicts

## 4. Validation Summary

Passing targeted tests:

```bash
go test ./internal/compiler ./internal/fuzz ./internal/oracle ./internal/prompt ./internal/seed ./cmd/defuzz/app
```

Additional validation:

- Smoke runs confirmed profile-tagged `compile_command.json`
- Smoke runs confirmed real AArch64 compilation with:
  - threshold variation
  - `-fPIC`
  - `guard=global`
  - `guard=sysreg`
- Smoke runs confirmed enabled LLM CFLAGS can still apply non-conflicting flags after the profile

## 5. Fresh Full Run

The fresh full run for the updated semantics should use a new output root so it does not mix with earlier runs created before the prompt-aware / filtered-LLM-CFLAGS behavior.

Recommended run:

```bash
go run ./cmd/defuzz fuzz \
  --output fuzz_runs/aarch64_canary_20260320_i256_r8_stage4_llmflags \
  --limit 256 \
  --timeout 30
```

Expected runtime characteristics:

- Initial seed processing is the dominant fixed startup cost.
- Per-target retries are expensive because each attempt recompiles instrumented GCC and runs QEMU.
- This is a long-running job; use the log file for progress monitoring.
