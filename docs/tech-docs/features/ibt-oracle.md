---
title: IBT Oracle (Intel CET Indirect Branch Tracking)
description: Static oracle for detecting unintended ENDBR landing pads in compiled x86 binaries
priority: HIGH
last_updated: 2026-05-28
status: IMPLEMENTED
---

# IBT Oracle (Intel CET / IBT)

## Overview

The **IBT Oracle** (`oracle.IBTOracle`, registered as `"ibt"`) is a purely static mechanism oracle that detects violations of Intel CET Indirect Branch Tracking (IBT) invariants in compiled x86 / x86-64 ELF binaries.

Its first and primary checker is `UnintendedEndbrChecker` (**INV-IBT-B01**), which surfaces the class of GCC bugs exemplified by **DREV-2026-004**: a defective predicate (`ix86_endbr_immediate_operand`) that fails to detect ENDBR byte patterns embedded in 64-bit `movabs` immediates, producing an unintended CET landing pad inside a function body.

Unlike the canary oracle, the IBT oracle **does not require an Executor or QEMU**. It parses the ELF binary directly using `BinaryInspector` — making it fast, cross-platform, and runnable in CI without any emulation layer.

---

## Invariant: INV-IBT-B01

> Under `-fcf-protection=branch` / `full`, no byte sequence equivalent to ENDBR32 (`F3 0F 1E FB`) or ENDBR64 (`F3 0F 1E FA`) may appear inside a function body **except** as the deliberate prologue emitted at the function entry.

- **Source**: Intel SDM Vol. 1 §17.3.1 ("Tracking Indirect Jumps and Calls"); GCC `-fcf-protection` documentation.
- **Category**: `CategoryStatic` — no execution required.
- **Polarity-sensitive**: yes. If the seed was compiled with `-fcf-protection=none` or `-fno-cf-protection`, any finding is downgraded from `Fail` → `Pass` via `applyPolarity`.

See [`docs/tech-docs/invariants/endbr-ibt.md`](../invariants/endbr-ibt.md) for the full invariant survey.

---

## Bug: DREV-2026-004

| Field | Value |
|-------|-------|
| **ID** | DREV-2026-004 |
| **Affected compiler** | GCC (x86-64 back-end) |
| **Root cause** | `ix86_endbr_immediate_operand` shift-scan loop does not test every aligned 4-byte window of a 64-bit immediate, missing patterns at non-zero byte offsets |
| **Trigger** | `return 0xfa1e0ff300000000ULL;` compiled with `-O2 -fcf-protection=branch` |
| **Observable symptom** | `endbr64` (`F3 0F 1E FA`) appears at `gadget+0x7` inside `.text` |
| **Repro** | `go run ./cmd/ibt-repro` |
| **Repro source** | `repro/x64/ibt_endbr_imm/source.c` |

---

## Architecture

```
IBTOracle.Analyze(seed, ctx)
    │
    └─► MechanismOracle{
            Name:      "IBT (Intel CET indirect branch tracking)",
            Checkers:  [UnintendedEndbrChecker],   ← INV-IBT-B01
            Polarizer: IBTOracle.polarityFor,
        }
            │
            └─► CheckContext{
                    Inspector: elfInspector (from ctx.BinaryPath),
                    Polarity:  PolarityPositive | PolarityInverted,
                }
                    │
                    └─► UnintendedEndbrChecker.Check(ctx)
                            1. Machine gate: EM_X86_64 / EM_386 only
                            2. Build function map from STT_FUNC symbols
                            3. Scan SHF_EXECINSTR sections for ENDBR bytes
                            4. Classify each hit: entry / unintended / gap
                            5. Return VerdictFail if any unintended hit
```

### Polarity

| Seed flag | `polarityFor` result | VerdictFail becomes |
|-----------|---------------------|---------------------|
| _(none / positive)_ | `PolarityPositive` | `VerdictFail` → **Bug** |
| `-fcf-protection=none` | `PolarityInverted` | `VerdictPass` → **No Bug** |
| `-fno-cf-protection` | `PolarityInverted` | `VerdictPass` → **No Bug** |

---

## Files

| Path | Purpose |
|------|---------|
| `internal/oracle/ibt_oracle.go` | `IBTOracle` façade; registry init; `DefaultIBTNegativeCFlags` |
| `internal/oracle/checker_static_ibt.go` | `UnintendedEndbrChecker` (INV-IBT-B01) |
| `internal/oracle/inspector.go` | Extended `BinaryInspector`: `FunctionSymbols`, `ExecutableSections`, `Machine`, `Class` |
| `internal/oracle/ibt_oracle_test.go` | Unit tests: `NewIBTOracle`, `isNegativeCase`, `polarityFor`, error paths, registry |
| `internal/oracle/checker_static_ibt_test.go` | Unit tests: `selectEndbrPattern`, `findEnclosingFunction`, all `UnintendedEndbrChecker` paths |
| `internal/oracle/drev_2026_004_integration_test.go` | Integration test (`//go:build integration`): compile + detect + negative-control |
| `cmd/ibt-repro/main.go` | Standalone repro driver; no QEMU needed |
| `repro/x64/ibt_endbr_imm/source.c` | Minimal C trigger for DREV-2026-004 |
| `repro/x64/ibt_endbr_imm/README.md` | Repro instructions |

---

## Configuration

The oracle is registered as `"ibt"` and can be instantiated via `oracle.New("ibt", options, ...)`.

```yaml
oracle: ibt
options:
  negative_cflags:          # optional; defaults to the list below
    - "-fcf-protection=none"
    - "-fno-cf-protection"
```

If `negative_cflags` is omitted, `DefaultIBTNegativeCFlags` (`-fcf-protection=none`, `-fno-cf-protection`) is used.

---

## Running the Repro Driver

```bash
# Positive mode: should report BUG DETECTED on affected GCC builds.
go run ./cmd/ibt-repro

# Negative mode: must report NO BUG (polarity inversion).
go run ./cmd/ibt-repro --negative

# Custom GCC (e.g. a known-affected build):
go run ./cmd/ibt-repro --cc /opt/gcc-17-20260426/bin/gcc

# Keep the compiled object for manual inspection with objdump:
go run ./cmd/ibt-repro --keep --out /tmp/ibt-out
objdump -d /tmp/ibt-out/ibt_repro.o
```

---

## Running Tests

```bash
# Unit tests (no compiler needed):
go test ./internal/oracle/ -run 'TestSelectEndbr|TestFindEnclosing|TestMoreSuffix|TestUnintendedEndbr|TestNewIBTOracle|TestIBTOracle'

# Integration tests (requires host x86-64 GCC):
go test -tags integration ./internal/oracle/ -run 'TestDREV2026004' -v
```

---

## Checker Detail Map

`UnintendedEndbrChecker.Check` populates `InvariantResult.Detail` with:

| Key | Type | Meaning |
|-----|------|---------|
| `polarity_sensitive` | `bool` | Always `true`; triggers `applyPolarity` inversion |
| `machine` | `string` | ELF `e_machine` (e.g. `EM_X86_64`) |
| `class` | `string` | ELF `EI_CLASS` (e.g. `ELFCLASS64`) |
| `endbr_total` | `int` | Total ENDBR occurrences in executable sections |
| `endbr_at_function_entry` | `int` | Legitimate prologue hits (at `STT_FUNC` entry addresses) |
| `endbr_in_inter_function_gap` | `int` | Hits in padding between functions (ignored) |
| `endbr_unintended_in_function_body` | `int` | Violations (drives `VerdictFail`) |
| `violations` | `[]string` | Up to `MaxReportedViolations` (8) formatted as `func+offset@addr[section]` |

---

## Known Limitations

- **EH landing pads / computed-goto targets**: `setjmp` return sites and C++ EH landing pads are also legitimate ENDBR positions not at a `STT_FUNC` entry. The current checker does not consult `.eh_frame` / DWARF, so these can produce false positives in exception-heavy code. The DREV-2026-004 trigger set does not use EH, so this is accepted as a documented caveat.
- **Stripped binaries**: without `STT_FUNC` symbols the checker returns `VerdictNotApplicable`. This is intentional: without function ranges we cannot make a precise call.
- **Relocatable objects (`.o`)**: section addresses are 0; function symbol `st_value` is section-relative. The checker handles this correctly because both addresses use the same reference frame.

---

## Future Work

- **INV-IBT-P01**: every globally-visible function entry must begin with `endbr`. This is a complementary invariant to B03 and can be added as a second `InvariantChecker` in `IBTOracle.mechanism()`.
- **`.eh_frame` whitelist**: extend the legitimate-landing-pad set to include EH entries, eliminating the EH false-positive caveat.
- **Fuzz-loop integration**: add an `ibt` entry to `configs/config.yaml` once the negative-control flag-profile pipeline is wired to the IBT oracle.
