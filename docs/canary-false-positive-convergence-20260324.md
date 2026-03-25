# Canary False Positive Convergence Rules

## Goal

Reduce canary false positives caused by source-level opt-out of stack protection, while keeping true compiler-side canary bypass candidates visible.

## Implemented Rules

### 1. Source-level protection evidence

The fuzzer now extracts source-level evidence from generated `seed()` code:

- `source_disables_stack_protector`
- `source_requests_stack_protect`
- `uses_alloca`
- `uses_vla`

These are derived from the generated C source and persisted into `compile_command.json`.

### 2. Effective negative classification

`compile_command.json` now also records:

- `effective_negative_reason`

Current values include:

- `negative_profile`
- `source_no_stack_protector_attr`
- `negative_llm_cflag`

This makes it possible to distinguish:

- intentionally negative runs
- source-disabled SSP samples
- normal protected samples

### 3. Oracle convergence

The canary oracle now treats the following as negative/non-bug cases:

- explicit negative-control profiles
- samples where `seed()` contains `__attribute__((no_stack_protector))`
- samples where an actually applied negative LLM flag disables SSP

This prevents source-disabled seeds from being reported as compiler canary bypasses.

### 4. Prompt guardrails

Prompts now explicitly state:

- normal profiles must not use `__attribute__((no_stack_protector))` on `seed()`
- explicit profiles should use `__attribute__((stack_protect))` when needed
- negative-control profiles may disable stack protection in source if needed

The canary function template comments were also updated with the same restriction.

### 5. Existing LLM-CFLAGS policy preserved

The earlier LLM-CFLAGS ordering and filtering remains in place:

- config flags
- selected profile flags
- filtered LLM CFLAGS

Conflicting canary-axis LLM flags are still dropped before compilation.

## Current Limitations

These convergence rules do **not** yet inspect binary-side instrumentation evidence such as:

- presence of `__stack_chk_fail`
- presence of canary load/store sequences

They only use:

- profile intent
- applied flags
- source text

That is sufficient to suppress the dominant false positive class found in the AArch64 Stage4 run, but not all theoretically possible false positive classes.

## Validation

Validated with targeted tests:

```bash
GOCACHE=/tmp/go-build-cache go test ./internal/seed ./internal/compiler ./internal/oracle ./internal/prompt
```

Covered behaviors:

- source analysis for `no_stack_protector`, `stack_protect`, VLA, alloca
- compilation record persistence of new evidence fields
- oracle suppression for source-disabled SSP samples
- prompt text for normal and negative-control profiles
