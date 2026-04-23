# GCC 15.2 FORTIFY Understanding

## Objectives

This configuration targets GCC 15.2.0 FORTIFY-related lowering, folding, and runtime-check integration.

The key questions are:

- whether `_FORTIFY_SOURCE` is actually enabled,
- whether `-fhardened` auto-enables it or gets suppressed,
- whether object-size inference proves operations safe and removes `_chk` calls,
- whether `_chk` builtins survive to later passes,
- whether compile-time diagnostics such as `maybe_emit_chk_warning` trigger.

## Important Compiler Functions

The Stage 3 analysis identified these high-value compiler functions:

- `c_finish_options`
- `fold_builtin_object_size`
- `gimple_fold_builtin`
- `gimple_fold_builtin_memory_chk`
- `gimple_fold_builtin_stxcpy_chk`
- `gimple_fold_builtin_sprintf_chk`
- `maybe_emit_chk_warning`
- `expand_builtin_memory_chk`

Related gate/helper functions also matter:

- `default_fortify_source_default_level`
- `linux_fortify_source_default_level`
- `avoid_folding_inline_builtin`
- `fold_call_expr`

## Important Option Axes

The Stage 4 report says the highest-value option axes are:

1. Optimization:
   - `-O0`
   - `-O1`
   - `-O2`
   - `-O3`

2. Fortify activation:
   - `-D_FORTIFY_SOURCE=0`
   - `-D_FORTIFY_SOURCE=1`
   - `-D_FORTIFY_SOURCE=2`
   - `-D_FORTIFY_SOURCE=3`
   - `-fhardened`
   - `-fhardened -U_FORTIFY_SOURCE`
   - `-fhardened -D_FORTIFY_SOURCE=1/2/3`

3. Isolation:
   - `-fno-stack-protector`

## Current Default Profile

The current online default profile is intentionally:

- `-O2`
- `-fhardened`
- `-fno-stack-protector`

This combination is chosen because it:

- enables the hardened front-end path,
- triggers the Linux fortify default-level hook,
- and isolates fortify behavior from stack protector behavior in the oracle.

## Source Shapes That Matter

Useful source patterns include:

- fixed-size buffers with `memcpy` / `memmove`
- struct members with `strcpy` / `strncpy`
- `sprintf` / `snprintf` with simple and complex format strings
- pointer-based writes where object size becomes unknown
- `alloca` or runtime-dependent buffer shapes

The generated source should prefer libc API families over manual loops, because FORTIFY mainly operates through wrapper macros and checked builtins.

## Oracle Expectations

The fortify oracle treats:

- exit 0 as normal,
- exit 134 as fortify successfully blocking the write,
- exit 139 as potential fortify bypass, but only if the sentinel `SEED_RETURNED` appears in stdout.

This means every candidate seed must print:

- `SEED_RETURNED`

before returning from `seed()`.

## Current Probe Status

With the current initial seed set and the generated multi-file CFG dumps, the fortify bootstrap now reaches:

- 14 / 15 configured fortify target functions
- 236 / 473 total BBs across those targets

The remaining cold target is:

- `default_fortify_source_default_level`

That is expected on the current GNU/Linux targets because the active hook path goes through:

- `linux_fortify_source_default_level`

instead of the generic default targhooks implementation.
