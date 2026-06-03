# `repro/x64/ibt_endbr_imm/` — DREV-2026-004 IBT ENDBR-immediate bypass

This directory holds the standalone trigger for **DREV-2026-004**: GCC's
`ix86_endbr_immediate_operand` predicate misses the `0xfa1e0ff3` (ENDBR64)
byte pattern when the 64-bit immediate has the pattern at a non-lowest
byte offset *and* non-zero bytes above the pattern. Under
`-fcf-protection=branch`, the offending constant is materialised by
`movabsq $imm, %rax`, whose 8-byte little-endian encoding embeds a valid
`endbr64` opcode at offset +3 of the `movabs` instruction.

The full finding (background, evidence, runtime verification log) lives
in `findings/DREV-2026-004/` (gitignored). This `repro/` copy is the
in-tree, CI-runnable test surface used by the IBT oracle.

## Files

| Path | Purpose |
| --- | --- |
| `source.c` | Minimal C trigger: a single function `gadget` returning `0x1fa1e0ff3abULL`. |
| `README.md` | This document. |

## Build & inspect

```
gcc -O2 -fcf-protection=branch -c source.c -o source.o
objdump -d -Mintel source.o
```

Expected disassembly (verified on `gcc-15.2.0` and `gcc-17.0.0 20260426 (experimental)`):

```
0000000000000000 <gadget>:
   0:   f3 0f 1e fa             endbr64                       # deliberate prologue
   4:   48 b8 ab f3 0f 1e fa    movabs rax, 0x1fa1e0ff3ab     # start of movabs
   b:   01 00 00
   e:   c3                      ret
```

Disassembling at offset +7 reveals an unintended landing pad:

```
0000000000000007 <gadget+0x7>:
   7:   f3 0f 1e fa             endbr64                       # UNINTENDED endbr64
```

When indirect-branch lands at `<gadget>+0x7`, the CPU accepts it as a
legitimate IBT landing pad. IBT coverage is silently weakened.

## Run via the IBT oracle

```
go run ./cmd/ibt-repro
```

Expected output on an unfixed compiler:

```
verdict: BUG DETECTED
[IBT (Intel CET indirect branch tracking)] 1 invariant violation(s) detected (polarity=positive).

Violations:
  - INV-IBT-B01 (static)
      Evidence: found 1 unintended ENDBR opcode(s) inside function bodies: gadget+0x7@0x7[.text]
      ...
```

When the upstream patch (replace the shift-scan with a masked byte-window
scan inside `ix86_endbr_immediate_operand`) lands, the constant should be
routed through `.rodata` via RIP-relative load, and `ibt-repro` will
print `verdict: NO BUG`.

## See also

- `internal/oracle/checker_static_ibt.go` — `UnintendedEndbrChecker`
  implementation.
- `internal/oracle/ibt_oracle.go` — oracle façade.
- `cmd/ibt-repro/main.go` — direct-invocation harness.
- `docs/tech-docs/features/ibt-oracle.md` — feature doc with invariant
  table and follow-ups.
- `docs/tech-docs/invariants/endbr-ibt.md` — full invariant survey for
  Intel CET / IBT (the upstream knowledge source).
