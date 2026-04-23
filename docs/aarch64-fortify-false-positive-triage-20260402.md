# AArch64 Fortify False-Positive Triage (2026-04-02)

## Summary

- Run under analysis: `fuzz_runs/aarch64_fortify_20260402_i256_r8_full/aarch64/fortify`
- Log: `logs/2026-04-02_21-46-26_CST.log`
- Status: partial run stopped after triage
- Conclusion: the reported fortify bugs observed in this run are false positives
- Follow-up rerun under analysis: `fuzz_runs/aarch64_fortify_20260402_i256_r8_full_fixoracle2/aarch64/fortify`
- Follow-up log: `logs/2026-04-02_23-04-44_CST.log`
- Follow-up conclusion: initial seed 2 in the rerun was also a false positive

The false positives were caused by the fortify oracle reporting crashes from profiles where `_FORTIFY_SOURCE` was not actually effective.

## Affected Seeds

### Seed 8

- Corpus dir: `fuzz_runs/aarch64_fortify_20260402_i256_r8_full/aarch64/fortify/corpus/id-000008-src-000003-cov-00085-ea2cfc4e`
- Reported crash: exit code `132`
- Profile: `optimization-O0__fortify_mode-hardened__stack_protector_mode-no-stack-protector`
- Effective fortify state: disabled

Evidence:

- compile record stderr contains:
  - `'_FORTIFY_SOURCE' is not enabled by '-fhardened' because optimizations are turned off`
- profile flags were:
  - `-O0`
  - `-fhardened`
  - `-fno-stack-protector`

Control experiment:

- the same source compiled with `-O2 -fhardened -fno-stack-protector`
- reproducer exit code became `134` (`SIGABRT`)

Conclusion:

- the original bug report came from an unprotected `-O0` configuration
- once fortify was actually enabled, the program aborted as expected

### Seed 13

- Corpus dir: `fuzz_runs/aarch64_fortify_20260402_i256_r8_full/aarch64/fortify/corpus/id-000013-src-000003-cov-00000-721dd010`
- Reported crash: exit code `132`
- Profile: `optimization-O0__fortify_mode-fortify0__stack_protector_mode-no-stack-protector`
- Effective fortify state: explicitly disabled

Evidence:

- profile flags were:
  - `-O0`
  - `-D_FORTIFY_SOURCE=0`
  - `-fno-stack-protector`

Control experiment:

- the same source compiled with `-O2 -D_FORTIFY_SOURCE=2 -fno-stack-protector`
- reproducer exit code became `134` (`SIGABRT`)

Conclusion:

- this seed was executed in an explicit no-fortify profile
- reporting it as a fortify bypass is incorrect

### Seed 15

- Corpus dir: `fuzz_runs/aarch64_fortify_20260402_i256_r8_full/aarch64/fortify/corpus/id-000015-src-000003-cov-00000-3cfbbfd1`
- Reported crash: exit code `132`
- Profile: `optimization-O0__fortify_mode-fortify2__stack_protector_mode-no-stack-protector`
- Effective fortify state: disabled by `-O0`

Evidence:

- compile record stderr contains:
  - `_FORTIFY_SOURCE requires compiling with optimization (-O)`
- profile flags were:
  - `-O0`
  - `-D_FORTIFY_SOURCE=2`
  - `-fno-stack-protector`

Control experiment:

- the same source compiled with `-O2 -D_FORTIFY_SOURCE=2 -fno-stack-protector`
- reproducer exit code became `134` (`SIGABRT`)

Conclusion:

- the profile requested fortify level 2 but also disabled optimization
- fortify was not effective, so the observed crash is not evidence of a bypass

### Seed 2 In `fixoracle2` Rerun

- Corpus dir: `fuzz_runs/aarch64_fortify_20260402_i256_r8_full_fixoracle2/aarch64/fortify/corpus/id-000002-src-000000-cov-00063-0491ba51`
- Reported crash: exit code `133`
- Metadata verdict: `BUG`
- Effective compile flags:
  - `-O0`
  - no `_FORTIFY_SOURCE` define
  - no `-fhardened`

Evidence:

- `compile_command.json` for seed 2 shows the binary was compiled only with the stable config baseline and `-O0`
- the seed has no `FlagProfile`, so the previous oracle-side negative-profile filter did not apply
- direct reproduction with the exact rerun binary still yields the same non-fortify crash:
  - `qemu-aarch64 .../build/seed_2 64 41`
  - stdout contains `SEED_RETURNED`
  - exit code is `133`

Control experiment:

- the same source compiled with a non-instrumented cross compiler and real fortify flags:
  - `aarch64-linux-gnu-gcc -O2 -D_FORTIFY_SOURCE=2 -fno-stack-protector source.c -o seed2_o2_f2`
  - `qemu-aarch64 -L /usr/aarch64-linux-gnu ./seed2_o2_f2 64 41`
- reproducer exit code became `134` (`SIGABRT`)

Conclusion:

- this rerun bug was triggered from a compile where fortify was not enabled at all
- once fortify was actually enabled for the same source, the program aborted as expected
- the seed 2 report is therefore a false positive

## Root Cause

The current fortify oracle treated any non-`SIGABRT` crash after the sentinel as a potential bug, but it did not first verify whether the active compiler profile could reasonably enable fortify.

This allowed the following invalid cases to be reported as fortify bugs:

- `optimization=O0`
- `fortify_mode=fortify0`
- `fortify_mode=hardened-no-fortify`
- no `FlagProfile` attached, while the actual compile argv still had fortify disabled

## Fix

The fortify oracle was updated to treat the above profiles as negative cases and to fall back to the actual effective compiler flags when no `FlagProfile` is attached.

Code changes:

- `internal/oracle/fortify_oracle.go`
- `internal/oracle/fortify_oracle_test.go`
- `internal/oracle/oracle.go`
- `internal/fuzz/engine.go`
- `internal/fuzz/phase_random.go`

Validation:

- `GOCACHE=/tmp/go-build-cache go test ./internal/oracle`
- `GOCACHE=/tmp/go-build-cache go test ./internal/fuzz`
- `GOCACHE=/tmp/go-build-cache go test ./cmd/defuzz/app`

## Operational Note

The partial run `aarch64_fortify_20260402_i256_r8_full` and the follow-up rerun `aarch64_fortify_20260402_i256_r8_full_fixoracle2` should not be used as final fortify result sets. A fresh rerun is required after the effective-flags oracle fix.
