# FORTIFY oracle initial seeds (loongarch64)

> NOTE: The disasm-based checkers (INV-FORT-O01 / O02 / O03) currently
> NA-out on loongarch64 because `internal/oracle/fortify_disasm.go`
> only wires up an x86_64 backend today. The symbol-level
> (W01-presence, C02) and dynamic (R01, R02, C01) checkers do run on
> loongarch64.



This directory hosts the FORTIFY oracle's bootstrap seeds and the
shared `function_template.c` consumed by the LLM-driven prompt builder.

## Argv contract

The template's `main()` dispatches the following modes; each one drives
exactly one invariant from `docs/tech-docs/invariants/fortify-source.md` §6:

| argv | invariant(s) | observation channel |
| ---- | ------------ | ------------------- |
| `bos` (default for `seed()`) | INV-FORT-W01, O01, O02, O03 | static — `__memcpy_chk` call site dstlen |
| `procmaps` | INV-FORT-R01 | stdout marker `FORTIFY_R01_*` |
| `chkfail`  | INV-FORT-R02 | stdout marker `FORTIFY_R02_*` + exit code |
| `printf:<entry>` | INV-FORT-C01 | stdout marker `FORTIFY_C01_*` |

`<entry>` ∈ `{printf, sprintf, snprintf, vsnprintf, syslog}` and may
be narrowed via `oracle.options.fortify.printf_entries` in
`configs/*.yaml`.

## Required compiler flags

The oracle is positive-control only. The seed-flag filter
(`internal/seed/defense_flags.go`) rejects any seed that emits one of
`-D_FORTIFY_SOURCE=0`, `-U_FORTIFY_SOURCE`, or `-O0`.

A typical compile uses:

    -O2 -D_FORTIFY_SOURCE=2

Without `-O2` (or higher), glibc's `__fortify_function` wrappers are
elided and every checker NA-outs.

## Disassembly backend

INV-FORT-O01 / O02 / O03 / W01 require a disassembly backend; today
only x86_64 is wired (`internal/oracle/fortify_disasm.go` —
`SupportsFortifyDisasm`). Seeds compiled for other ISAs run the
symbol-level (W01-presence, C02) and dynamic (R01, R02, C01) checkers,
and the disasm-based ones return NA with a clear reason.

## Test hooks

The template honours two env-vars for repro / unit tests:

* `FORTIFY_FORCE_BYPASS=1` — make `procmaps` mode emit the BYPASS marker
* `FORTIFY_FORCE_TRAP=1`   — make `procmaps` mode emit the TRAPPED marker
* `FORTIFY_FORCE_C01=bypass|trap` — force `printf:*` outcome
