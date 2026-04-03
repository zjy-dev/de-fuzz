# DeFuzz Current Project Understanding

Date: 2026-04-02

This document is the current repository-level understanding of `de-fuzz` after the multi-file CFG, fortify, and option-postpass updates. It is written as an operational reference for future contributors, not as a historical narrative.

## 1. What the Project Is

DeFuzz is a Go-based compiler-defense fuzzer for instrumented compiler builds.

The stable mental model is:

- online `defuzz fuzz` finds source-level interesting seeds,
- coverage is measured on compiler source code, not on the generated program,
- strategy-specific oracles decide whether a compiled test case exposes a defense bug,
- interesting seeds are persisted into a reusable corpus,
- offline `defuzz option-postpass` deterministically replays that corpus across strategy-owned option combinations.

This repository is no longer canary-only. The current codebase must be treated as a multi-strategy framework with concrete implementations for:

- `canary`
- `fortify`
- `crash`
- `llm`

## 2. Repository Shape

The major directories are:

- `cmd/defuzz`: CLI entrypoints.
- `internal/config`: config schema and loader.
- `internal/compiler`: compiler invocation, flag filtering, compile record persistence.
- `internal/coverage`: gcovr integration and CFG analyzer.
- `internal/fuzz`: main online fuzzing engine and deterministic flag scheduler.
- `internal/postpass`: offline deterministic option traversal on persisted corpus seeds.
- `internal/oracle`: strategy-specific bug oracles.
- `internal/prompt`: prompt construction for generation, refinement, and constraints.
- `internal/seed`, `internal/corpus`: seed persistence, metadata, recovery.
- `configs`: global config, compiler configs, post-pass matrix config.
- `initial_seeds`: per-ISA and per-strategy bootstrap seed pools.
- `docs`: architecture, instrumentation, experiment, and operational docs.
- `target_compilers`: instrumented compiler sources, build trees, install trees, and build scripts.

## 3. Main Commands

The active CLI surface is:

- `defuzz generate`
- `defuzz fuzz`
- `defuzz option-postpass`

`defuzz fuzz` and `defuzz option-postpass` both support `--isa` and `--strategy` overrides. For reproducible runs, prefer explicit overrides instead of depending on whatever `configs/config.yaml` happens to select at that moment.

## 4. Config Model

Config resolution is two-stage:

1. `configs/config.yaml` chooses the active compiler family, version, ISA, and strategy.
2. That selection resolves to a compiler config file named `{compiler}-v{version}-{isa}-{strategy}.yaml`.

Examples currently present:

- `configs/gcc-v15.2.0-aarch64-canary.yaml`
- `configs/gcc-v15.2.0-riscv64-canary.yaml`
- `configs/gcc-v15.2.0-loongarch64-canary.yaml`
- `configs/gcc-v15.2.0-aarch64-fortify.yaml`
- `configs/gcc-v15.2.0-riscv64-fortify.yaml`
- `configs/gcc-v15.2.0-loongarch64-fortify.yaml`
- `configs/postpass.yaml`

`LoadConfigWithOverrides(isa, strategy)` is now the practical entrypoint for commands that must stay stable even if `configs/config.yaml` changes.

## 5. Flag Ownership Model

The repository now has a clearer separation of compiler flags:

- `compiler.cflags`: stable toolchain and environment flags.
- `compiler.fuzz.flag_strategy`: deterministic strategy-owned flag matrices for online fuzzing.
- `Seed.CFlags`: LLM-suggested per-seed flags.
- `configs/postpass.yaml`: deterministic replay-time option traversal for persisted corpus seeds.

The actual compile merge order is:

1. compiler prefix flags,
2. `compiler.cflags`,
3. active `FlagProfile` flags,
4. filtered `Seed.CFlags`.

If `compiler.cflags` is empty, the compiler layer falls back to the neutral baseline `-O0`.

Two runtime rules are critical:

- `flag_strategy.enabled=true` means online fuzzing may inject deterministic strategy-managed profiles before LLM flags.
- `flag_strategy.enabled=false` means online fuzzing keeps only stable `compiler.cflags` plus LLM-generated flags.

Current validated policy:

- active GCC 15.2 canary configs set `flag_strategy.enabled: false`, so online canary fuzzing no longer secretly injects canary policy/threshold/layout combinations;
- active GCC 15.2 fortify configs still use `flag_strategy.enabled: true`, because fortify online fuzzing currently depends on deterministic fortify profile rotation.

## 6. Seed Model and Persistent Artifacts

The central model is `seed.Seed`. A seed may include:

- source code,
- test cases,
- LLM-generated flags,
- an applied deterministic flag profile,
- compile-time filtering metadata,
- coverage and oracle metadata.

Interesting seeds are persisted under:

- `{run-root}/corpus/id-000123-src-000042-cov-00132-<hash>/`

Important files inside a seed directory:

- `source.c`
- `testcases.json`
- `cflags.json`
- `flag_profile.json`
- `compile_command.json`

`compile_command.json` is the ground truth for the actual compile semantics of that seed.

## 7. What Counts as an Interesting Seed

In the current engine, a mutated seed is considered interesting if any of the following is true:

- it covers new compiler lines,
- it covers the current target basic block,
- it triggers a bug in the active oracle.

If it is interesting and not a negative-control profile, it is added to the persistent corpus and becomes eligible for later post-pass replay.

This is the key workflow split:

- online fuzzing is responsible for discovering source-level interesting seeds,
- option-postpass is responsible for deterministic traversal of strategy-owned option combinations on top of that seed pool.

## 8. Current Corpus and Resume Semantics

The corpus manager still mixes persistence with pending-queue semantics.

A practical consequence remains:

- `Corpus.Add()` stores new seeds as `PENDING`,
- later interesting seeds can remain `PENDING` even after already being compiled and oracle-checked,
- `Recover()` may requeue them on resume.

Therefore, when building a replay seed pool, the authoritative source is the actual seed directories under `corpus/`, not only metadata counters such as `processed_count`.

## 9. CFG and Coverage Model

Coverage is collected from the instrumented compiler via gcovr JSON. CFG-guided targeting is performed by the analyzer over parsed `.cfg` dump files.

The current repository now supports true multi-file CFG guidance through `compiler.fuzz.cfg_file_paths`.

The invariant is:

- one compiler source file you want the analyzer to target
- requires one corresponding object built with `-fdump-tree-cfg-lineno`
- which must produce one matching `*.cfg` file
- and that path must be present in `cfg_file_path` or `cfg_file_paths`.

Listing functions in config without generating the corresponding `.cfg` file does not make them targetable.

Project rule going forward:

- all configured target functions are considered required,
- missing CFGs must be generated,
- target functions should not be silently removed or commented out only to satisfy current build artifacts.

## 10. Currently Validated Multi-File CFG Surface

The current validated GCC 15.2 cross-toolchain CFG coverage is:

For AArch64 canary:

- `cfgexpand.cc`
- `function.cc`
- `calls.cc`
- `targhooks.cc`
- `aarch64.cc`

For AArch64 fortify:

- `c-family/c-opts.cc`
- `builtins.cc`
- `gimple-fold.cc`
- `targhooks.cc`
- `linux.cc`

For RISC-V canary:

- `cfgexpand.cc`
- `function.cc`
- `calls.cc`
- `targhooks.cc`
- `riscv.cc`

For RISC-V fortify:

- `c-family/c-opts.cc`
- `builtins.cc`
- `gimple-fold.cc`
- `targhooks.cc`
- `linux.cc`

For LoongArch64 canary:

- `cfgexpand.cc`
- `function.cc`
- `calls.cc`
- `targhooks.cc`

For LoongArch64 fortify:

- `c-family/c-opts.cc`
- `builtins.cc`
- `gimple-fold.cc`
- `targhooks.cc`
- `linux.cc`

The instrumented Makefile whitelist in the checked-in GCC 15.2 builds now includes:

- `cfgexpand.o`
- `function.o`
- `calls.o`
- `targhooks.o`
- `builtins.o`
- `gimple-fold.o`
- `c-family/c-opts.o`
- `linux.o`
- `aarch64.o`
- `riscv.o`

That whitelist is enough for the currently configured canary and fortify surfaces. If future strategies need backend-specific LoongArch logic, `loongarch.o` must be added explicitly and rebuilt.

## 11. Current Strategy Status

### Canary

Canary remains the most mature strategy in terms of oracle logic and target coverage. The current GCC 15.2 canary configs intentionally keep online fuzzing in an LLM-only dynamic-flag mode by disabling `flag_strategy`.

That means:

- online canary fuzzing still does CFG-guided source mutation,
- LLM-generated `Seed.CFlags` may still be used,
- deterministic canary policy/threshold/layout traversal is expected to happen in `option-postpass`, not inside the online loop.

### Fortify

Fortify is now a real first-class target, not just a bootstrap placeholder.

The repository now contains:

- compiler configs for all three GCC 15.2 cross ISAs,
- fortify initial seeds and understanding docs for all three ISAs,
- fortify oracle wiring,
- multi-file CFG support for the relevant compiler source files.

The current ranked default fortify online profile is effectively:

- `-O2 -fhardened -fno-stack-protector`

Validated probe result on all three GCC 15.2 cross ISAs:

- 14 of 15 configured fortify target functions reached,
- 236 of 473 total target basic blocks covered,
- remaining cold function: `default_fortify_source_default_level`.

That remaining cold function is expected on current GNU/Linux builds because `linux_fortify_source_default_level` overrides the generic targhooks default path.

### Crash and LLM

`crash` and `llm` are already represented in the oracle registry and in `configs/postpass.yaml`. Their post-pass matrices currently materialize to a single default combination. This keeps the framework extensible without inventing unsupported semantics.

## 12. Online Fuzz Lifecycle

`defuzz fuzz` currently does:

1. load the selected config,
2. initialize corpus, compiler, coverage, prompt service, LLM, and oracle,
3. build the flag scheduler if enabled,
4. load or recover corpus state,
5. process initial seeds to build coverage mapping,
6. if CFG/analyzer is configured, run the target-driven mutation loop.

The online loop is still CFG-first:

- select uncovered target BB,
- pick a historical seed that covered predecessor lines,
- build prompt context,
- generate a mutation,
- compile,
- measure coverage,
- run the oracle,
- keep the seed if it is interesting.

The online loop is not the right place anymore for exhaustive traversal of strategy-owned option combinations across already-interesting seeds.

## 13. Option Post-Pass Framework

`internal/postpass` is now the repository’s deterministic replay layer.

`defuzz option-postpass`:

- scans the persisted corpus,
- loads each seed’s `compile_command.json`,
- reconstructs the baseline compile flags,
- strips strategy-owned flag families according to `configs/postpass.yaml`,
- materializes the deterministic option matrix for the active ISA and strategy,
- recompiles and reruns each `(seed, combination)` pair,
- reuses the configured oracle,
- writes attempt artifacts under `postpass/<run-name>/`.

The replay-time compile option changes in `option-postpass` come from a narrower source set than the general online compiler pipeline.

For each replay attempt, the effective changing options are built from:

1. the persisted original `compile_command.json`,
2. `strip_rules` from `configs/postpass.yaml`,
3. the selected materialized combination from `configs/postpass.yaml`,
4. automatically re-added prefix flags.

More concretely, the current implementation does:

- start from `config_cflags + applied_llm_cflags`,
- if that is empty, fall back to `effective_flags - prefix_flags`,
- strip strategy-owned flag families,
- pass the stripped result as replay-time baseline `CFlags`,
- attach the materialized combo as `FlagProfile.Flags`,
- disable replay-time LLM CFlags.

So for `option-postpass`, changing compile options do **not** come from:

- the current compiler YAML's `compiler.cflags`,
- the current online `flag_strategy` matrix,
- newly generated LLM flags.

Those broader flag-source rules still apply to online `defuzz fuzz`, but not to how `option-postpass` reconstructs and mutates replay attempts.

Important operator rule:

- `--run-dir` must point to the run root that contains `corpus/`,
- not to the `corpus/` directory itself.

For example, this is correct:

- `./fuzz_out/riscv64/canary`

This is incorrect:

- `./fuzz_out/riscv64/canary/corpus`

Worker semantics are now config-driven:

- each strategy in `configs/postpass.yaml` can define default `workers`,
- CLI `--workers` overrides that default,
- fallback is `1`.

Logging behavior is intentionally immediate:

- compile failures are logged with seed, combo, directory, and error,
- oracle errors are logged with seed, combo, directory, and error,
- bug findings are logged immediately with seed, combo, directory, and description.

## 14. Reproducibility Rules

These rules should be treated as project policy.

1. Do not rely on the current value of `configs/config.yaml` for reproducible experiments. Pass `--isa` and `--strategy`.
2. Treat `compile_command.json` as the truth for historical compile semantics.
3. Treat `corpus/` as the persisted interesting-seed pool.
4. Treat `cfg_file_paths` as part of the experimental definition. Target functions without matching CFG dumps are not actually in the active reachable target surface.
5. If you rebuild instrumented objects, relink `cc1` afterward. Rebuilding only the object file is insufficient.
6. For strategy option traversal, prefer `option-postpass` over reintroducing random or implicit online exploration.

## 15. Extensibility Rules

To add a new defense strategy cleanly:

1. add or extend the oracle in `internal/oracle`,
2. add compiler configs for each supported ISA,
3. add initial seeds, `function_template.c`, and `understanding.md`,
4. decide whether online fuzzing uses `flag_strategy`,
5. add a strategy entry to `configs/postpass.yaml`,
6. document which compiler source files and functions define the real target surface,
7. generate the necessary `.cfg` dumps for every configured target file.

To add new target functions in an existing strategy:

1. identify the compiler source file,
2. add its object to the instrumentation whitelist if needed,
3. regenerate the corresponding `.cfg`,
4. add the path to `cfg_file_paths`,
5. then add the functions under `targets`.

## 16. Known Limits

The current repository still has some non-blocking limitations:

- divergence support exists in code but is not fully wired from the CLI path;
- random mutation phase exists in code but is not enabled by the current CLI wiring;
- `go test ./...` is still unsuitable at repo root because vendored upstream GCC content pulls in unrelated Go tests;
- LLM-backed online mutation can still be blocked by provider authentication or account state, which is external to the fuzzing and CFG pipeline.

## 17. Bottom Line

The current project should be understood as a reproducible two-stage defense-fuzzing pipeline:

- stage 1: use CFG-guided online fuzzing to discover interesting source-level seeds;
- stage 2: use deterministic option-postpass replay to traverse defense-owned option combinations on that seed pool.

Multi-file CFG support and fortify support are now part of that baseline architecture, not side experiments.
