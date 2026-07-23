---
title: FORTIFY_SOURCE Static Audit — GCC 16.1.0 + glibc 2.39
description: Consolidated campaign report for the static silent-bypass audit of the _FORTIFY_SOURCE / object-size-checking mechanism
priority: HIGH
last_updated: 2026-06-26
status: IMPLEMENTED
---

# FORTIFY_SOURCE Static Audit — GCC 16.1.0 + glibc 2.39

> Campaign deliverable. Methodology: the [`defend-reviewer-invariants`](../../../../defend-reviewer-invariants/README.md)
> static source-audit framework (precision over recall; 5-item Finding
> Admission Gate; "trust but verify" via independent compile / IR dump /
> end-to-end PoC). Threat model is **silent bypass** as defined in
> [`docs/invariants/fortify-source.md`](../invariants/fortify-source.md):
> `_FORTIFY_SOURCE` is enabled, optimization is sufficient, libc provides
> the `__*_chk` entities, the `__fortify_function` wrappers are in scope —
> yet an attacker-constructed out-of-bounds write still completes and
> `__chk_fail` / `__fortify_fail` is never called.

## 1. Scope

| Axis | Value |
| --- | --- |
| Compiler | GCC 16.1.0 (self-built; `--disable-bootstrap --disable-multilib`, C frontend) |
| Compiler cross-check | GCC 17 trunk snapshot `20260531` (for fix-status only) |
| C library | glibc 2.39-0ubuntu8.7 (headers + `libc.so.6`) |
| ISA / host | aarch64-unknown-linux-gnu (Colima VM, Ubuntu noble) |
| FORTIFY levels exercised | `-D_FORTIFY_SOURCE=2` (BOS) and `=3` (BDOS), `-O2` |
| Mechanism | `_FORTIFY_SOURCE` / object-size checking (`__builtin_object_size`, `__builtin_dynamic_object_size`) |
| Invariant catalogue | `INV-FORT-{O01,O02,O03,W01,R01,R02,C01,C02}` |

**Audit surfaces examined:**

- GCC middle-end: `gcc/tree-object-size.cc` (BOS / BDOS lowering, the
  `.ACCESS_WITH_SIZE` internal-fn path for `counted_by`).
- glibc fortify headers: `bits/string_fortified.h`, `bits/stdio2.h`,
  `bits/stdlib.h`, `bits/unistd-decl.h`, `bits/unistd.h`, `bits/err.h`
  *(absent — see C02)*, `error.h`, `bits/error.h`, `sys/cdefs.h`
  comparison macros.
- glibc runtime symbols: `__chk_fail`, `__fortify_fail`,
  `__stack_chk_fail`, the `__readonly_area` `%n` machinery
  (`libc.so.6` symbol table + strings).

## 2. Invariant verdict matrix

| Invariant | Statement (short) | Verdict | Evidence |
| --- | --- | --- | --- |
| **INV-FORT-O01** | BOS must not return `(size_t)-1` for a struct's last-member array | **HOLDS** | `o02_nested.c`: flex/last-member `__bos`/`__bdos` return the static layout size, not `-1` |
| **INV-FORT-O02** | BDOS on `counted_by` must return `count * sizeof(elem)` | **VIOLATED** → **[DREV-2026-025](#4-the-finding--drev-2026-025)** | signed count wider than `int` with bit 31 set is truncated to `int32` then sign-extended |
| **INV-FORT-O03** | BDOS must re-read the latest SSA value of a reassigned size var | **HOLDS** (not reproduced on 16.1) | no stale-value regression observed; PR 113514 family appears resolved on this tree |
| **INV-FORT-W01** | `__fortify_function` inlining must preserve BOS context | **HOLDS** | header wrappers are `__always_inline` + `__glibc_objsize`; GCC keeps BOS through inlining |
| **INV-FORT-R01** | `__readonly_area` must fail-closed when `/proc/self/maps` is unavailable | **HOLDS at machinery level** (design choice unchanged) | `libc.so.6` contains the `%n`-in-writable-segment path + `/proc/self/maps`; fail-open design is upstream-known (open question, not a new defect) |
| **INV-FORT-R02** | `__chk_fail` / `__fortify_fail` must be `noreturn` | **HOLDS** | `__chk_fail@@GLIBC_2.17`, `__fortify_fail@@GLIBC_PRIVATE`, `__stack_chk_fail@@GLIBC_2.17` all present |
| **INV-FORT-C01** | `vfprintf` entry must set the unified fortify flag | **HOLDS** | `printf`/`fprintf` with writable `%n` → `*** %n in writable segment detected ***`, abort (exit 134) |
| **INV-FORT-C02** | `<err.h>` / `<error.h>` must have `_chk` wrappers | **GAP (carry-forward)** | `err`/`warn`/`error` families have **no** fortify wrapper; `%n` write completes (exit 0). Public/known: sourceware #24987 |

**One new admitted finding: DREV-2026-025 (INV-FORT-O02).** Everything else
either holds on this toolchain or is a previously-disclosed upstream gap.

## 3. Cross-mechanism / cross-source summary

```
                      GCC 16.1.0 middle-end        glibc 2.39 header/runtime
                      ----------------------       --------------------------
  Object size (BOS)   O01 HOLDS                     W01 HOLDS (objsize macros)
  Dynamic size (BDOS) O02 VIOLATED (DREV-2026-025)  —
                      O03 HOLDS
  printf %n path       —                            C01 HOLDS, R01 machinery present
  err/error path       —                            C02 GAP (sourceware #24987)
  fail-closed contract —                            R02 HOLDS
```

The single defect is entirely **compiler-side** and ISA-independent (a
middle-end fold). glibc 2.39's header wrappers and `_chk` entities are
standard-correct across `string_fortified.h`, `stdio2.h`, `stdlib.h`,
`unistd-decl.h`, and the `sys/cdefs.h` comparison macros
(`__glibc_safe_len_cond`, `__glibc_safe_or_unknown_len`,
`__glibc_unsafe_len`, `__glibc_fortify`, `__glibc_fortify_n`).

## 4. The finding — DREV-2026-025

**`counted_by` BDOS truncates a signed count to 32 bits → FORTIFY=3 silent bypass.**

- **Archived at:** `defend-reviewer-invariants/findings/DREV-2026-025/`
  (README + evidence + poc/{trigger,confirm,build,expected,observed} +
  timeline). `poc_verified: true`, `severity: high`, `status: draft`,
  `cwe: CWE-787`.
- **Site:** `gcc/tree-object-size.cc`, `access_with_size_object_size`,
  **line 891**. This is the *sole* producer of the dynamic object size
  for a `counted_by` flexible array or pointer.

### Root cause

The negative-clamp `COND_EXPR` is built with `integer_type_node` (plain
32-bit `int`) as its result type:

```c
size = fold_build3 (COND_EXPR, integer_type_node, cond_expr,
                    build_zero_cst (type), size);
```

`size` enters with the count field's declared type (`type`, e.g. 64-bit
`long`). Forcing the result to `integer_type_node` narrows the count to
`int`; because this block only runs for signed counts
(`!TYPE_UNSIGNED (type)`), the narrowed value is then sign-extended back
to 64 bits at the subsequent `fold_convert (sizetype, size)`. The
`MULT_EXPR` by `element_size` (done in `sizetype`) is itself correct — it
is fed an already-corrupted value.

It is the **only** size-bearing `COND_EXPR` in the file built with
`integer_type_node`; the analogous select in `cond_expr_object_size`
(lines ~1702–1714) correctly uses `sizetype`.

### Model and observed values

`bdos = (int32_truncate(count) sign-extended to 64) * elem_size`

| `p->n` (signed `long`) | observed BDOS | correct | verdict |
| --- | --- | --- | --- |
| `0x7fffffff` | 2147483647 | 2147483647 | OK |
| `0x80000000` | 18446744071562067968 | 2147483648 | **WRONG (over-report → OOB write passes)** |
| `0x100000000` | 0 | 4294967296 | WRONG (under-report → DoS / false abort) |
| `0x100000001` | 1 | 4294967297 | WRONG |

IR footprint (`-fdump-tree-objsz1`): `_23 = (int) _22;` — the 32-bit
truncation of the clamped count.

End-to-end (PoC `confirm.c`): with `struct S { long n; char arr[]
__attribute__((counted_by(n))); }`, `n = 0x80000000`, a `memcpy` of 5000
bytes into a 4096-byte tail **SURVIVED** (`exit=0`) — `__memcpy_chk`
received the bogus `0xFFFFFFFF80000000`-scale bound and never called
`__chk_fail`. Unsigned counts are unaffected (they skip the clamp block).
Pointer `counted_by` (`d->p`) is affected identically.

### Why later layers don't rescue

The wrong size is constant-folded into the `_chk` bound at compile time.
glibc's `__memcpy_chk` / `__strcpy_chk` trust their length argument
verbatim and cannot re-derive the array's real capacity from a runtime
pointer. `.ACCESS_WITH_SIZE` is the only producer of the counted_by size,
so the truncated value is authoritative.

### Suggested patch direction

Build the clamp with the count's own type (or `sizetype`) instead of
`integer_type_node` — e.g. `MAX_EXPR (size, 0)` in `TREE_TYPE (size)`
before converting to `sizetype`.

### Fix status

Present **identically in gcc-17 trunk snapshot `20260531`**. Unfixed
upstream at audit time. Next step: human review → upstream filing
(GCC Bugzilla).

## 5. Honest negative results

Per the methodology's precision discipline, candidates that did **not**
clear the Admission Gate are recorded here, not promoted to findings:

- **GCC has no equivalent of the Clang `counted_by` bugs**
  (LLVM PR #110497 nested-pointer → 0; PR #112636 whole-struct off-by-4).
  Probe `o02_nested.c` (FORTIFY=3): `bdos(o.in->arr,1)=100` ✓,
  `bdos(p->arr,0)=100` ✓, whole-struct `bdos(p,0)=104` = `offsetof+n`
  (no off-by-4). GCC's defect is a different root cause (signed-count
  truncation), not a port of either Clang bug.
- **INV-FORT-O01 holds**: last-member / flexible-array BOS returns the
  static layout size, not `(size_t)-1`, on this tree.
- **INV-FORT-O03 holds**: no stale-SSA over-report reproduced.
- **glibc header wrappers are standard-correct**: `string_fortified.h`,
  `stdio2.h`, `stdlib.h` (incl. `wctomb` worst-case `MB_LEN_MAX=16`),
  `unistd-decl.h`, and the `sys/cdefs.h` comparison macros all match the
  intended design. No glibc-side DREV.

## 6. Coverage gaps (Admission-Gate rejected / out of this campaign)

- **INV-FORT-C02 (`err`/`warn`/`error` %n gap)** — empirically confirmed
  on glibc 2.39 (`c02_n.c`: `printf`/`fprintf` abort with the `%n`
  detector; `warn`/`warnx`/`err`/`errx`/`error` let the `%n` write
  complete, `sink=1`, exit 0). This is the **publicly-known**
  sourceware #24987 / Red Hat BZ 836931 long-standing hole, so it is a
  **carry-forward**, not a new DREV. `bits/err.h` has no `__fortify_function`
  wrapper; `error.h` only conditionally includes `bits/error.h` for the
  `noreturn` specialization, with no `_chk` path.
- **INV-FORT-R01 (`__readonly_area` fail-open)** — the machinery exists
  in `libc.so.6` (the `%n`-in-writable-segment path and the
  `/proc/self/maps` reader). The fail-open-when-`/proc`-unavailable
  behavior is the upstream-known design (hxp CTF 2017 PoC); confirming
  the bypass requires a seccomp/sandbox runtime that was out of scope for
  this static campaign.
- **Not audited this round:** musl / bionic `__*_chk` matrices; LTO/ThinLTO
  BDOS stability; `wchar2.h` / `socket2.h` / `poll2.h` wrappers beyond a
  declaration scan.

## 7. Next steps

1. **Human review of DREV-2026-025** → file upstream (GCC Bugzilla),
   transition `draft` → `internal-review` → `reported-upstream` in
   `findings/DREV-2026-025/timeline.md`.
2. Land the suggested `gcc.dg/builtin-dynamic-object-size` regression
   test (assert `bdos(p->arr,1) == 0x80000000UL` for `n=0x80000000`; an
   execution test that an overflowing `__memcpy_chk` aborts; an IR-level
   assert that no `(int)` cast narrows the count).
3. Extend the counted_by surface: enum / bitfield count types, `__int128`
   counts, and the `=2` vs `=3` boundary, to confirm the truncation is
   isolated to the line 891 clamp.
4. Treat C02 / R01 as detection samples for the dynamic loop (already
   public), not as new static findings.

## 8. Reproduction pointers

- Finding corpus: `defend-reviewer-invariants/findings/DREV-2026-025/`
  (`poc/build.sh` drives `trigger` + objsz1 dump + `confirm`;
  `poc/observed/gcc-16.1.0-aarch64.txt` is the captured run).
- Probes (research repo): `.audit-probes/` —
  `o02_min.c`, `o02_confirm.c`, `o02_ext.c`, `o02_nested.c` (O02 surface),
  `c02_n.c` (C02 empirical confirmation).
- Source copies: `.audit-src-gcc16/tree-object-size.cc` (defect site),
  `.audit-src-glibc/` (header wrappers examined).
- Invariant catalogue: [`docs/tech-docs/invariants/fortify-source.md`](../invariants/fortify-source.md).
