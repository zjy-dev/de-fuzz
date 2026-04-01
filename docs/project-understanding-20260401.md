# DeFuzz Current Project Understanding

Date: 2026-04-01

This document is a current-state understanding of the `de-fuzz` repository as it exists now. It is based on reading the code, configs, prompts, initial seeds, existing design docs, and sample runtime artifacts under `fuzz_out/`.

## 1. What the Project Is

DeFuzz is a Go-based compiler-defense fuzzer.

Its core idea is:

- use CFG-guided target selection to choose uncovered compiler basic blocks,
- use an LLM to mutate C seeds toward those targets,
- compile the generated seed with a target compiler configuration,
- measure compiler-source coverage via gcovr,
- run a strategy-specific oracle on the compiled binary,
- keep only "qualified" seeds in the persistent corpus.

Today the project is focused mainly on the `canary` strategy, especially GCC canary implementations on multiple ISAs.

## 2. Repository Shape

The repository is organized around a small CLI entrypoint plus many internal packages.

Main top-level directories:

- `cmd/defuzz`: CLI entrypoint and command wiring.
- `internal/config`: config loading and config schema.
- `internal/seed`: seed model, parsing, validation, template merge, persistence.
- `internal/corpus`: persistent corpus manager and queue/recovery logic.
- `internal/compiler`: compiler invocation and compile-record persistence.
- `internal/coverage`: gcovr-based coverage, CFG analyzer, line-to-seed mapping, divergence support.
- `internal/fuzz`: main engine, deterministic flag scheduler, random phase.
- `internal/prompt`: prompt building for generate/constraint/refinement/compile-error flows.
- `internal/llm`: remixer-based LLM abstraction and provider integration.
- `internal/oracle`: strategy-specific bug oracles.
- `internal/seed_executor`, `internal/vm`, `internal/exec`: runtime execution adapters.
- `internal/state`: global state, metrics, terminal UI.
- `internal/report`: bug-report abstraction, currently markdown-oriented.
- `configs`: global config, compiler configs, remixer config.
- `initial_seeds`: per-ISA/per-strategy seed pools, templates, understanding documents.
- `prompts/base`: base system prompts.
- `docs`: design notes, experiment reports, oracle docs, analysis notes.
- `fuzz_out`, `fuzz_runs`: runtime artifacts and historical runs.

## 3. Current CLI Surface

The CLI is minimal right now.

Implemented commands:

- `defuzz generate`
- `defuzz fuzz`
- `defuzz option-postpass`

There is now a standalone post-pass command for replaying persisted corpus seeds across deterministic strategy-managed compiler option combinations.

## 4. Config Model

Config loading is two-stage:

1. `configs/config.yaml` provides global selection:
   - `isa`
   - `strategy`
   - compiler name/version
   - log settings
   - remixer config path
2. That selection resolves to a compiler config file with naming pattern:
   `{compiler}-v{version}-{isa}-{strategy}.yaml`

Important current examples:

- `configs/gcc-v15.2.0-aarch64-canary.yaml`
- `configs/gcc-v15.2.0-riscv64-canary.yaml`
- `configs/gcc-v15.2.0-loongarch64-canary.yaml`
- `configs/gcc-v12.2.0-x64-canary.yaml`
- `configs/gcc-v12.2.0-aarch64-canary.yaml`
- `configs/postpass.yaml`

Current `configs/config.yaml` points to:

- `isa: loongarch64`
- `strategy: canary`
- compiler `gcc 15.2.0`

`LoadConfig()` also resolves environment variables and loads `.env` recursively.

There is also now `LoadConfigWithOverrides(isa, strategy)` so commands such as `option-postpass` can override `config.yaml` from CLI while still resolving the correct compiler config.

## 5. Seed Model and On-Disk Format

The central model is `seed.Seed`.

A seed contains:

- `Content`: the C source code.
- `TestCases`: optional execution cases.
- `CFlags`: LLM-requested flags.
- `FlagProfile`: deterministic strategy-selected flag profile.
- `AppliedLLMCFlags`, `DroppedLLMCFlags`, `LLMCFlagsApplied`: compile-time filtering result.
- `Meta`: lineage/state/coverage/oracle metadata.

Persistent seed directories live under the corpus and use the naming pattern:

- `id-000123-src-000042-cov-00132-<hash>`

Files that may exist inside a seed directory:

- `source.c`
- `testcases.json`
- `cflags.json`
- `flag_profile.json`
- `compile_command.json`

Separate metadata JSON files live under `metadata/` as:

- `id-000123.json`

## 6. Initial Seeds

Each `initial_seeds/<isa>/<strategy>/` directory acts as a seed pool bootstrap and prompt context root.

Typical contents:

- `understanding.md`
- `function_template.c`
- a few hand-written initial seed directories

For canary, the template is an executable program shape where the LLM fills only `seed()` in template mode. The template standardizes:

- command-line arguments `buf_size` and `fill_size`,
- sentinel print `SEED_RETURNED`,
- `main()` marked `no_stack_protector`,
- patterns such as fixed arrays, VLA, and `alloca`.

Current canary initial seeds are hand-built examples, not generated from scratch on every run.

## 7. Generate Flow

`defuzz generate` currently does the following:

- loads the global config,
- creates an LLM client,
- creates a prompt builder,
- uses `initial_seeds/<isa>/<strategy>` as the base path,
- reads `understanding.md` directly,
- builds generate prompts,
- parses LLM responses into seeds,
- writes seed directories under `initial_seeds/...`.

Important current detail:

- `generate` does not yet use `PromptService` the same way `fuzz` does.
- There is an explicit TODO in `cmd/defuzz/app/generate.go` for this.

## 8. Fuzz Flow: High-Level

`defuzz fuzz` is the main runtime.

At startup it:

- loads config and logger,
- creates `corpus.FileManager`,
- builds `fuzz.FlagScheduler` if enabled,
- creates `compiler.GCCCompiler`,
- creates `coverage.GCCCoverage`,
- creates the LLM client,
- creates `prompt.PromptService`,
- creates the strategy oracle,
- initializes and recovers the corpus,
- loads initial seeds into corpus if corpus is empty,
- builds a CFG analyzer if CFG files and target functions are configured,
- creates `fuzz.Engine` and runs it.

The main runtime output root is:

- `{output_root}/{isa}/{strategy}`

with subdirs:

- `build/`
- `corpus/`
- `metadata/`
- `state/`

## 9. Actual Engine Lifecycle

The engine has two phases.

### 9.1 Initial-seed processing

`Engine.processInitialSeeds()` repeatedly pulls pending seeds from `Corpus.Next()` and:

- assigns the default profile,
- compiles the seed,
- measures coverage,
- persists `compile_command.json`,
- records coverage into the analyzer mapping,
- runs the oracle,
- calls `Corpus.ReportResult(...)` to mark the seed processed and update metadata.

This phase builds the initial line-to-seed mapping and saves it immediately.

### 9.2 Constraint-solving loop

Then the engine enters the main loop:

1. ask the analyzer to choose an uncovered BB target,
2. load a base seed from the coverage mapping,
3. build a `TargetContext`,
4. attach the active flag profile to the prompt context,
5. call the LLM for a first mutation,
6. compile, measure coverage, run oracle,
7. retry with refined prompt or compile-error prompt if needed,
8. decay target weight on failure,
9. periodically save state.

## 10. What "Qualified Seed" Means in the Current Engine

This is critical for later post-pass work.

In `Engine.tryMutatedSeed()`, a mutated seed is treated as "qualified" if any of the following is true:

- it covers new lines,
- it hits the current target BB,
- it triggers a bug in the oracle.

If qualified and not a negative-control profile:

- the analyzer records its coverage,
- it is added to the persistent corpus,
- its compilation record is persisted,
- coverage totals may be merged into `total.json`.

This is the current operational meaning of "interesting seed".

That same persisted corpus is now the source pool for `option-postpass`.

## 11. Current Corpus/State Semantics

The corpus manager mixes two roles:

- persistent interesting-seed storage,
- pending queue for later processing/recovery.

Important current behavior:

- `Corpus.Add()` always stores a seed with state `PENDING`.
- `tryMutatedSeed()` adds newly qualified mutated seeds to corpus, but does not call `ReportResult()` afterward.
- Therefore many newly found interesting seeds remain `PENDING` in metadata even though they were already compiled, measured, and oracle-checked in the same run.
- On a later resume, `Recover()` will queue those pending seeds again, and `processInitialSeeds()` will reprocess them.

This is visible in current runtime artifacts:

- initial seeds such as `id-000001` become `PROCESSED`,
- later interesting seeds such as `id-000042` remain `PENDING`.

Implication:

- if we need a post-fuzz seed pool, the most reliable source is the seed directories under `corpus/`, not only the processed-state counter.

## 12. Compile Records and a Real-World Subtlety

`compile_command.json` is the current source of truth for actual compile semantics.

It records:

- compiler path,
- shell-safe command,
- argv,
- prefix flags,
- config cflags,
- profile name/flags/axes,
- source-level analysis results,
- requested/applied/dropped LLM flags,
- effective flags,
- stdout/stderr.

Important subtlety observed in current artifacts:

- initial seeds are compiled before `Corpus.ReportResult()` renames the seed directory based on `cov_incr`,
- so `compile_command.json.source_path` for an initial seed can still point at the old `cov-00000` directory name,
- while the actual seed directory on disk has already been renamed to its final `cov-xxxxx` name.

Implication:

- any future post-pass should trust the actual current seed directory location and use `compile_command.json` mainly for flags and compile metadata, not for discovering the seed path.

## 13. Strategy-Controlled Option Semantics

This is the most important point for the requested feature.

The project currently models strategy-controlled compiler options via:

- `compiler.fuzz.flag_strategy`
- especially `flag_strategy.axes`

For canary, `axes` is split into:

- `axes.common`
- `axes.by_isa.<isa>`

Current common categories are:

- `policy`
- `threshold`
- `pic_mode`

Current ISA-specific categories vary by ISA:

- `aarch64`: `guard_source`
- `riscv64`: `guard_source`
- `x64`: `guard_source`
- `loongarch64`: `layout`

This means:

- the project does not have a single universal literal axis called `layout`,
- instead, the current implementation treats multiple option categories as strategy-managed,
- and only LoongArch64 currently uses a literal `layout` axis in YAML.

For your requested change, "影响相关数据结构 layout 的选项" should therefore be understood as the strategy-controlled option categories currently modeled under `flag_strategy.axes`, not only a YAML key named `layout`.

## 14. Current Flag Strategy Implementation

`internal/fuzz/flag_strategy.go` materializes deterministic `seed.FlagProfile` values from config.

It currently:

- builds the Cartesian product of selected categories,
- resolves ISA placeholders such as:
  - `<config-provided-valid-sysreg>`
  - `<same-sysreg>`
  - `<config-provided-gpr>`
  - `<same-gpr>`
- names profiles by axis values,
- sorts them with a deterministic priority order,
- rotates profiles per target BB,
- occasionally injects a negative-control profile.

Important runtime semantics:

- default profile is used for initial/non-targeted compiles,
- target-specific profile rotation happens during constraint solving,
- prompt context is updated with the active profile before LLM generation.

## 15. Prompt/Profile Coupling

The engine does not let the LLM choose flags blindly anymore.

It attaches active profile info into `TargetContext`:

- selected profile name,
- already-active profile flags,
- blocked flag families,
- negative-control status,
- whether LLM cflags are allowed.

Prompt builders then instruct the model:

- which flags are already active,
- which families are selector-owned,
- that conflicting LLM CFLAGS may be dropped.

This is already the current design intent documented in `docs/aarch64-canary-stage4-implementation-20260320.md`.

## 16. Current Compile Flag Merge Order

Actual compile order in `internal/compiler/compiler.go` is:

1. prefix flags,
2. config `compiler.cflags`,
3. selected profile flags,
4. filtered LLM-provided flags.

This order is critical because `compile_command.json.effective_flags` reflects exactly this merge.

The compiler also performs conflict filtering:

- stack-protector family conflicts are always blocked when a profile is active,
- layout-specific LLM conflicts are additionally blocked if the active profile reserves `layout_mode`.

## 17. Oracle Architecture

Oracles are plugin-based via the registry.

Current oracle types implemented in code:

- `canary`
- `fortify`
- `crash`
- `llm`

For current canary runs:

- the oracle is active, not passive,
- it executes the binary itself through an `oracle.Executor`,
- it uses binary search over `fill_size`,
- it distinguishes `SIGABRT` vs `SIGSEGV`/`SIGBUS`,
- it uses `SEED_RETURNED` sentinel output to suppress false positives,
- it treats negative cases based on:
  - negative profile,
  - source-level `no_stack_protector`,
  - applied negative LLM flags only.

This oracle reuse is important for the requested post-pass: the project goal is not only coverage; it is defense-bug discovery under the active strategy oracle.

This is now the actual runtime behavior of `option-postpass`: it reuses the configured oracle instead of introducing a separate analysis path.

## 18. Coverage and CFG Guidance

Coverage is collected from the instrumented target compiler via gcovr JSON.

The coverage subsystem has two major parts:

- `GCCCoverage`: report generation, filtering, merge, increase computation.
- `Analyzer`: CFG parsing, target selection, line-to-seed mapping, BB weighting.

`Analyzer` currently:

- parses CFG dump files,
- indexes BBs and lines,
- builds predecessor maps,
- tracks line-to-seed coverage mapping,
- selects uncovered reachable BBs,
- uses weighted ranking with decay,
- chooses a base seed from covered predecessor lines,
- stores mapping as JSON for resume.

One important detail:

- `CoverageMapping.GetSeedForLine()` randomly chooses among seeds that covered a line.
- So target selection is weighted/deterministic at the BB level, but base-seed selection from coverage history is still randomized among eligible seeds.

## 19. Divergence Support: Present in Code, Not Fully Wired

The codebase contains uftrace-based divergence support:

- `internal/coverage/divergence.go`
- `Engine.solveConstraint()` has divergence-aware retry logic

But in the current CLI wiring:

- `runFuzz()` does not construct a `DivergenceAnalyzer`,
- does not pass `CompilerPath`,
- does not pass `CoverageTimeout`.

So today the divergence-related branch in the engine is effectively only partially active:

- compile-error retries work,
- refined retries can still happen,
- but uftrace-based divergence extraction is not actually wired in from the CLI path.

## 20. Metrics, Terminal UI, and Reporter: Implemented but Lightly Integrated

The repository contains:

- a `FileMetricsManager`,
- a color terminal UI,
- a markdown bug reporter.

Current status:

- `runFuzz()` creates a metrics manager,
- but it is not passed into the engine and is not meaningfully updated by the main runtime,
- the terminal UI is therefore not part of the actual fuzz loop today,
- the reporter package exists but is not wired into the main fuzz path.

These are important because they show the codebase contains infrastructure that is more complete in tests and helpers than in the current CLI execution path.

## 21. Random Phase: Exists but Is Not Currently Enabled

The engine has a random mutation phase in `internal/fuzz/phase_random.go`.

However:

- `runFuzz()` does not currently set `EnableRandomPhase`,
- no config field is wired for it in the main CLI path.

So the main fuzzing path today is effectively:

- initial seeds,
- constraint-solving loop,
- stop.

## 22. `max_new_seeds` Is Configured but Not Driving the Engine

`compiler.fuzz.max_new_seeds` exists in config and defaults are loaded.

But in the current code:

- it is not used by `runFuzz()` to parameterize the engine,
- it is not used by `Engine.solveConstraint()` as a per-interesting-seed expansion bound,
- the current runtime shape is one generated mutation attempt plus retries per target BB, not a generic "generate N new seeds per interesting seed" loop.

This is relevant because your planned feature explicitly wants to operate on the seed pool, whereas the current engine is target-driven rather than seed-expansion-driven.

## 23. Supported Canopy of Current Compiler/ISA Coverage

From configs and docs, the current implemented surface is mainly GCC canary fuzzing:

- x64
- AArch64
- RISC-V 64
- LoongArch64

The configs are not yet a generic defense-framework abstraction. They are still strategy-specific and GCC-specific in practice.

However, the new post-pass layer introduces a generic strategy matrix config:

- `configs/postpass.yaml`

This file is keyed by strategy and describes:

- default `workers`,
- deterministic option-group order,
- strategy-owned flag families to strip from the recorded baseline,
- common option groups,
- ISA-specific option groups,
- ISA placeholder materialization inputs.

The remixer currently points to:

- OpenAI `gpt-5.4` via Responses API

with old providers commented out.

## 24. Current Runtime Artifact Layout

A typical active run under `fuzz_out/<isa>/<strategy>/` contains:

- `build/seed_<id>.c` and `build/seed_<id>` for temporary compile products,
- `corpus/<seed-dir>/...` for persisted interesting seeds,
- `metadata/id-<id>.json`,
- `state/global_state.json`,
- `state/coverage_mapping.json`,
- `state/total.json`,
- per-seed coverage reports like `state/<seed-id>.json`.

If `option-postpass` is run, the same run root also gains:

- `postpass/<run-name>/summary.json`
- `postpass/<run-name>/<seed-dir>/<combo-name>/attempt.json`
- `postpass/<run-name>/<seed-dir>/<combo-name>/compile_command.json`
- `postpass/<run-name>/<seed-dir>/<combo-name>/source.c`
- `postpass/<run-name>/<seed-dir>/<combo-name>/testcases.json` when present
- `postpass/<run-name>/<seed-dir>/<combo-name>/flag_profile.json`

Observed current `global_state.json` in LoongArch64 sample:

- `last_allocated_id` reflects total seed ID growth,
- `processed_count` reflects only seeds reported through `ReportResult()`,
- which is not the same as "all interesting seeds currently present in corpus".

## 25. Current Option Post-Pass Framework

The repository now contains a dedicated strategy-agnostic replay framework in:

- `internal/postpass`

The framework does the following:

- scans persisted corpus seeds,
- requires `compile_command.json`,
- reconstructs baseline flags from recorded compile state,
- strips strategy-owned flag families,
- materializes deterministic combinations from `configs/postpass.yaml`,
- recompiles and reruns each combination,
- reuses the configured oracle,
- saves replay outputs under `postpass/<run-name>/`.

Current command-line entrypoint:

- `defuzz option-postpass`

Current target-selection knobs:

- `--run-dir`
- `--output`
- `--isa`
- `--strategy`
- `--matrix-config`
- `--workers`
- `--use-qemu`
- `--run-name`

Current worker semantics:

- each strategy in `configs/postpass.yaml` can define a default `workers`,
- CLI `--workers` overrides the strategy default,
- fallback is `1`.

More concretely:

- the post-fuzz seed pool is the persisted `corpus/` directories,
- the baseline compile semantics per seed come from `compile_command.json`,
- strategy-managed option categories come from `configs/postpass.yaml`,
- the strategy oracle is reused after compile+run,
- and the feature does not rely on `metadata.state == PROCESSED` as the only interesting-seed filter.

Current logging behavior of `option-postpass`:

- logs compile failures per combination,
- logs oracle errors per combination,
- logs bug findings immediately with seed, combo, output directory, and description,
- writes a final summary at the end of the run.

## 26. Summary Judgment

The current project is not a generic fuzzer skeleton. It is already a fairly specialized GCC-defense fuzzing pipeline with:

- strong canary-specific modeling,
- deterministic strategy-controlled compile profiles,
- persistent compile records,
- strategy-aware prompts,
- active strategy oracles,
- a useful persisted corpus,
- and now a separate deterministic option-space replay stage.

The right mental model for further work is:

- DeFuzz currently generates and validates interesting source-level seeds,
- while some online option-space exploration still exists inside the fuzz loop,
- and there is now an explicit offline deterministic traversal stage over the existing interesting corpus.

That now matches the repository state better than describing the project as "online fuzzing only" or "random fuzzing with optional flags".
