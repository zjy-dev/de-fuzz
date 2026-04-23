# RISC-V64 Canary Full Run Report (2026-03-25)

## Summary

- Run ID: `riscv64_canary_20260325_i256_r8_full`
- Output: `fuzz_runs/riscv64_canary_20260325_i256_r8_full/riscv64/canary`
- Log: `logs/2026-03-25_12-54-22_CST.log`
- Iterations: `256`
- Duration: `5h52m23.329922192s`
- Final BB coverage: `76.23%`
- Oracle bug count: `0`

## Coverage Snapshot

Final per-function BB coverage from the run log:

- `stack_protect_decl_phase`: `20/21` (`95.2%`)
- `stack_protect_decl_phase_2`: `2/2` (`100.0%`)
- `stack_protect_return_slot_p`: `14/15` (`93.3%`)
- `defer_stack_allocation`: `13/56` (`23.2%`)
- `expand_used_vars`: `131/148` (`88.5%`)
- `stack_protect_prologue`: `10/20` (`50.0%`)
- `stack_protect_decl_phase_1`: `2/2` (`100.0%`)
- `add_stack_protection_conflicts`: `15/15` (`100.0%`)
- `create_stack_guard`: `1/1` (`100.0%`)
- `stack_protect_classify_type`: `23/23` (`100.0%`)

## Corpus Outcome

- Metadata entries with coverage improvements: `16`
- Last interesting seed ID: `1614`
- Last recorded total coverage: `7623` basis points
- Unique recorded `content_hash` values: `16`

## Bug Triage Result

This run produced no oracle bug reports.

Therefore:

- no true bug was confirmed
- no false positive triage set was needed
- no standalone bug report was generated for this run

## Generated Artifacts

- Coverage plot: `scripts/assets/riscv64_canary_20260325_i256_r8_full_coverage.png`
- Bug plot: `scripts/assets/riscv64_canary_20260325_i256_r8_full_bugs.png`
