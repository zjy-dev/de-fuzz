# LoongArch64 Stack-Canary Leak Reproducer

Bypasses the LLM / fuzz-loop / corpus pipeline and drives
`@/home/yall/project/de-fuzz/internal/oracle/canary_oracle.go` directly on a
single hand-crafted seed, so we can confirm the oracle wiring is correct
**and** that the LoongArch64 GCC 16.1.0 cross-toolchain at
`@/home/yall/project/de-fuzz/target_compilers/gcc-v16.1.0-loongarch64-cross-compile/install-loongarch64-unknown-linux-gnu/`
exhibits the canary leak captured by `INV-SP-R03` (DREV-2026-001).

## Files

- `source.c` — freestanding standalone seed. Defines its own
  `__stack_chk_guard`, `__stack_chk_fail`, `_start`, syscall stubs, and
  the same `run_scrub_probe` asm block as
  `@/home/yall/project/de-fuzz/initial_seeds/loongarch64/canary/function_template.c`.
- `@/home/yall/project/de-fuzz/cmd/canary-repro/main.go` — Go driver:
  compiles `source.c` with the cross-compiler, runs three smoke probes
  (`scrub`, safe, smash), then calls `CanaryOracle.Analyze` directly.

## Run

```bash
go run ./cmd/canary-repro
```

The defaults bake in this workspace's tool paths; override with
`--source`, `--cc`, `--qemu`, `--sysroot` for a different setup.

## Why freestanding

The local glibc build under
`@/home/yall/project/de-fuzz/target_compilers/gcc-v16.1.0-loongarch64-cross-compile/`
never finished (binutils 2.44 BFD assertion in `elfnn-loongarch.c:4381`
when linking `static-pie` support binaries). To avoid a multi-stage
toolchain rebuild we provide our own `__stack_chk_guard` /
`__stack_chk_fail` / `memset` / `_start` and link with `-nostdlib -static`.
The oracle only consumes stdout markers (`GUARD_LEAKED`,
`CANARY_SCRUB_OK`, `SEED_RETURNED`) and exit codes, all of which we
produce via the LoongArch64 Linux syscall ABI (`write` = 64,
`exit_group` = 94).

## Observed result

```
=== smoke probes ===
--- scrub (INV-SP-R03) ---
seed: filled 0 bytes into 64-byte buffer
SEED_RETURNED
GUARD_LEAKED reg=9 name=t0          <-- canary value left in $r12 / $t0
(exit=1, err=exit status 1)
--- safe   (64, 32) ---
seed: filled 32 bytes into 64-byte buffer
SEED_RETURNED
(exit=0)
--- smash  (64, 256) ---
seed: filled 256 bytes into 64-byte buffer
SEED_RETURNED
*** stack smashing detected ***
(exit=134)

=== oracle: CanaryOracle.Analyze ===
oracle verdict: BUG DETECTED
[stack canary] 1 invariant violation(s) detected (polarity=positive).

Violations:
  - INV-SP-R03 (dynamic)
      Evidence: caller-saved register holds __stack_chk_guard after seed() returned: GUARD_LEAKED reg=9 name=t0
      Source: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=125045
Passed: INV-SP-A01, INV-SP-L01
Not applicable:
  - INV-SP-G01: no __stack_chk_fail import: ...
```

`INV-SP-G01` falls to N/A (rather than Pass/Fail) because we statically
link our own `__stack_chk_fail`, so the binary has no `__stack_chk_*`
import to inspect. That is the documented N/A path in
`@/home/yall/project/de-fuzz/internal/oracle/checker_static_canary.go`
and is the expected verdict for a freestanding harness.

## Root cause sketch

GCC 16.1.0 emits the loongarch64 stack-protector epilogue via the
`stack_protect_test` generic fallback, which materializes the guard in
`$r12` (= `$t0`, caller-saved) for the equality check and **does not
scrub it** before `jr $r1`:

```
la.global $r12, __stack_chk_guard
ld.d      $r13, $r22, -24
ldptr.d   $r12, $r12, 0
beq       $r13, $r12, .L_ok
    pcaddu18i $r1, %call36(__stack_chk_fail)
    jirl      $r1, $r1, 0
.L_ok:
ld.d      $r1, $r3, ...
jr        $r1                       ; <-- $r12 still equals __stack_chk_guard
```

The scrub probe pins a snapshot pointer in `$r23` (`$s0`, callee-saved),
calls `seed()`, and immediately spills every caller-saved GPR. The slot
for `$r12` matches `__stack_chk_guard`, which is the leak channel
INV-SP-R03 was written to detect.
