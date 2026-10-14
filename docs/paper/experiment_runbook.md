# DeFuzz Formal Experiment Runbook

This runbook documents how to execute the paper-facing experiment pipeline without
blurring the boundary between engineering validation and formal campaign data.
It is intentionally limited to the exact DeFuzz pipeline
`Invariant Generation -> Checker Writing -> Source Mapping -> Agent Audit` and the four campaign
variants `full`, `without-rag`, `without-oracle`, and `bare-agent`.

## Scope and boundaries

- Use `configs/experiments/example.yaml` only for fixture smoke coverage. It is
  an engineering check for typed config resolution, lane orchestration, hash
  chains, and resume semantics.
- Use `configs/experiments/formal.example.yaml` as the starting point for a real
  campaign. It uses `schema_version: 2`, stays in `mode: formal`, and must be edited with a local Pi
  config path, frozen input paths, and executable toolchain driver paths before
  use. Its four variants must remain `full`, `without-rag`, `without-oracle`,
  and `bare-agent` for the paper comparison.
- Do not claim fixture smoke, demo parity, or the bounded Part I pilot as paper
  results. They are engineering evidence that the pipeline runs end to end.
- Do not convert a pilot shard, partial range, or `max_segments` cap into a
  full-corpus claim. Full-corpus Part I evidence requires a complete,
  non-overlapping shard union. The current formal YAML contract accepts only
  one complete unsharded corpus (`shard_count: 1`, `max_segments: null`); run
  distributed shards as pilots until a validated shard-union manifest exists.

## One complete GCC/AArch64 run

For an implementation-alignment task followed by one complete GCC/AArch64
experiment, use the [task prompt](../prompts/gcc-aarch64-experiment.md).
Here `full` means the complete-feature variant, complete coverage means every
applicable input in the selected scope, and one repetition means one execution
of that scope within the fixed budgets. These are separate from the template's
four-ISA, four-variant, three-repetition paper comparison.

Use a separate config and run ID for the AArch64 scope. A formal single-Full
config sets `variants: [full]`, `repetitions: 1`, and target `isas: [aarch64]`;
freeze the mechanisms, complete corpus, backend and per-stage budgets as usual.
Record unsupported mechanisms, excluded inputs and cross-ISA differential
checker dependencies. Preserve those dependencies or report their absence;
never prune a required comparison to claim AArch64 completion. Such a run may
provide one valid formal repetition, but cannot establish the full paper
comparison. `run-all --compiler gcc --isa aarch64 --repetitions 1` instead
chains the four standalone engineering experiments, subject to its remaining
required inputs and `--show-plan`.

Repository context documents guide the development session. The formal Pi SDK
backend disables automatic context/skills and loads each stage definition directly; root
`AGENTS.md` changes do not propagate to its workers. Record and freeze any
deliberate stage-prompt change before a new experiment, preserving ablation
boundaries. A provider-side block is an interruption with its actual error and
request identifier, not a checker verdict.

## Preconditions

Formal runs fail closed unless all of the following are true:

- Every formal input directory is inside a clean Git worktree. The pipeline
  checks `reference_root`, `checker.source_root`, every `target.corpus_root`,
  every `target.audit_source_roots`, `toolchains_config`, and the config file
  itself.
- The v2 campaign uses `backend.kind: pi` and `backend.config_path` resolves
  to a runtime JSON file referencing the pinned model, MCP and definitions.
  The compiled worker, Node >=22.19.0 and an OS sandbox are required:
  `sandbox-exec` on macOS or `bwrap` on Linux.
  The launcher exercises the actual sandbox before model calls. Linux uses
  user/PID namespaces and hides worker `/proc`; unavailable nested namespaces
  (including amd64 QEMU on the local AArch64 VM) fail preflight.
- Credential references such as `${DEFUZZ_COCONUT_API_KEY}` are populated.
  The configuration stores the variable name, never the credential value.
- The reference checkout contains the required review documents:
  `.claude/agents/defend-reviewer.md`, `docs/prompts/full-review.md`,
  `docs/bugs`, and `docs/invariants`.
- Each ISA used by a formal target has a configured absolute compiler driver in
  `toolchains.yaml`. GCC targets require `gcc_path`; LLVM targets require
  `clang_path`.
- Each formal ISA explicitly selects its execution mode. Set `native: true` only
  when target binaries run directly on the experiment host. Otherwise set
  `native: false` and provide an absolute executable `qemu_path`; use
  `qemu_sysroot` for dynamically linked targets and optional `qemu_args` such as
  `[-cpu, max]`. Static-only checker routes compile but do not invoke QEMU.
  `driver_args` similarly carries trusted arguments for a containerized compiler
  wrapper and is inserted before all compiler flags.
- `DEFUZZ_FAST_PLAN` is unset. Formal mode explicitly forbids it.
- The host/backend combination supports the required read-isolation boundary for
  formal agent stages.

## Local Pi runtime configuration

Build `agent-runtime/` with `npm ci && npm run build`.
Start from `configs/agents/pi-runtime.example.json` and its referenced
`models.example.json` and `mcp.example.json`. Freeze the model,
gateway endpoint, reasoning, sampling policy, tools and runtime across all variants. The formal YAML
references it as follows:

```yaml
schema_version: 2
backend:
  kind: pi
  config_path: ../agents/pi-runtime.example.json
```

Populate `DEFUZZ_COCONUT_API_KEY` through the process environment or secret manager.
Select `openai-responses` or `openai-completions` and the gateway's `baseUrl`
in the models file, then freeze that choice. Runtime config references explicit
model/MCP files and definitions; global defaults cannot override the isolated run.

DeFuzz directly selects `invariant-reviewer`,
`checker-author`, `source-mapper`, and the selected `audit-family-a` through
`audit-family-e` agents. Bare-agent uses `audit-neutral`. A Full audit may also
invoke `checker-update-planner` and `checker-author`. One worker and one fresh
Pi session serve each attempt. Model calls, provider events, tool evidence,
effective prompts and configs are archived without a CLI export.
Use `defuzz-inspect-attempt <attempt-directory>` to inspect evidence offline.
Schema repair includes the original task, previous output and validation errors,
with `parent_attempt_id`; infrastructure failures are terminal.

Schema v1 remains available for legacy three-stage HTTP/Exec replay. Its fixture
example is unchanged; it does not add source mapping or checker updates to old
campaigns.

## Clean-room and findings isolation

- All four stages deny worker reads of
  `<reference-root>/findings`.
- Part III additionally runs against a sanitized, read-only source copy instead
  of the original audit target tree.
- Source mapping uses a separate compiler copy and writes `source-mappings.jsonl`,
  `source-mapping-summary.json`, and `source-snapshot.json`. Scope, invariant
  semantics, ISA and source content are validated before the audit consumes them.
- Full and `without-oracle` receive mappings for their selected mechanisms;
  bare workers receive no mapping or CallGraph tools. V2 ablation lanes reuse
  the Full lane's frozen mapping and checker bundle.
- The Part I `accepted-invariants.jsonl` artifact is hash-bound into Part III.
  Full and `without-oracle` receive the same field-minimized invariant view;
  `bare-agent` receives none of it.
- Part III provider `events` and `final` output are written to a temporary
  quarantine first. Only leak-checked structured reports enter worker artifacts;
  tainted output leaves a sanitized failure record. Complete runtime evidence is
  archived separately under the stage parent's `.evaluator-runtime/`, explicitly
  denied to every worker, including on cancellation or a tainted response.
- `demo-parity` reads the evaluator-only demo findings corpus only after workers
  exit. Treat that output as engineering comparison data, not as formal paper
  statistics.
- Historical bug documents may inform Part I RAG, but evaluator-only findings
  must never become worker-visible prompt material.

## Token comparability

- Token usage is collected per repetition with a dedicated sink and written into
  both detailed and summary artifacts. The Pi backend records each actual
  model request, including tool and repair rounds. Automatic
  title, summary and compaction model calls are disabled.
- A repetition is token-comparable only when every provider call reports usage
  and the repetition stays within budget.
- If a provider returns a response but structured parsing fails, the returned
  usage still counts.
- If a call never receives provider usage, it is recorded as `usage_missing`
  rather than `0`.
- Formal comparisons across Full and ablations must use only repetitions with
  `usage_missing_count == 0` and no budget overshoot.

The preserved fields include input, output, total, cached-input, and reasoning
tokens when returned by the provider. A received response with no usage is
marked missing rather than zero; failures before any response are governed by
the formal stage requirement that provider usage records exist. Token accounting
is applied to all four variants with the same per-Part budgets.
The checked-in formal template uses larger stage envelopes than the bounded
pilots: a 172-segment Part I and even one Part II checker can exceed 120,000
tokens. These are safety ceilings, not expected consumption; review the actual
per-call JSONL before scaling from the first complete Full repetition to the
three-repetition four-variant comparison.

Calibrate those ceilings before freezing the paper campaign, and apply the same
per-Part limits to every variant and repetition. If a limit proves too small,
mark that entire campaign as calibration evidence and restart under a new
`run_id`; do not splice a resumed or enlarged-budget lane into the three formal
repetitions. For the 2026-08-31 GCC 17 four-ISA campaign, 18 in-scope Part II
inputs reached 10,007,640 tokens before the last model call, while an earlier
complete five-family Part III pass consumed 5,037,455 tokens. A later four-ISA
campaign (2026-09-04) observed `without-rag` Part III lanes overshooting the
initial 8M ceiling at 8,106,096 and 8,196,418 tokens once the checker bundle
drove deeper multi-worker auditing, so the Part III ceiling is raised to 12M.
Those historical v1 ceilings were 5M for Part I, 20M for Part II, and 12M for
Part III. V2 adds a separate mapping envelope and counts checker planning and
updates inside the audit budget. Calibrate and freeze the new campaign budgets
before collecting paper evidence; historical HTTP consumption is not a Pi
budget measurement.

## CallGraph and checker evolution

Mapping and non-bare Pi audit calls require at least one completed local
`mcp__defuzz_callgraph__*` tool invocation. `callgraph_queries` counts all such calls;
`callgraph_data_queries` excludes `list_targets`. A target-list call alone is not
evidence of relevant callers/callees. Workers must report missing coverage.
The bundled database currently contains GCC 16.2.0 x86_64/aarch64 and LLVM 23.1.2;
it does not establish GCC 17, RISC-V or LoongArch graph coverage.
Pi production preflight binds the original source version and referenced source
file hashes to database provenance, checks the selected compiler driver version,
and records the database SHA-256 before model calls. The stdio server is restricted
to the bound targets. Missing/mismatched graph data is a configuration failure.
The original CodeQL logs identify release versions but omit the extractor checkout
commit; provenance preserves that limitation instead of asserting an independently
verified extraction revision.

Full online feedback can trigger at most `checker_update_rounds` planning rounds
(default 1, allowed 0–3, bounded also by `oracle_rounds`). Updates replay the
validated parent's cumulative patch in a disposable DeFuzz source copy, preserve
all invariant/checker IDs, run Go formatting/tests/vet, compile the dispatcher and
validate its catalog. An unchanged or invalid update fails the audit. A successful
bundle is executed immediately for revised online feedback and later rounds.
The original frozen Part II bundle remains the final offline verifier for every
variant. `checker-updates/` records decisions, hashes, parent/new bundles and
validation; no candidate means no update request.

## Compiler-specific drivers

The historical formal GCC baseline is `17.0.0 experimental 20260531`, source commit
`f20bc4c2fe00928013c533e241b89ae3a6724ca1`. Both the Part I corpus and Part III
audit roots must come from that frozen checkout. The YAML version label records
the baseline, while the plan independently records the Git revision and hashes
the selected toolchain driver.
This baseline needs a newly extracted matching graph for Pi mapping/audit; the
bundled GCC16 graph is rejected. To use the bundled graphs, freeze a new campaign
with GCC 16.2.0 (`78d4ac73dd391005b895a6148cd9831e28e1208b`) or LLVM 23.1.2
(`85ac560262434c9ccfc0c183ec22d4138ed647fb`) and its matching corpus/toolchain.
Do not change an existing campaign's compiler version to obtain completion.
`configs/toolchains.matched.lock.json` records release archive checksums, while
`configs/toolchains.matched.container.yaml` selects x86_64/AArch64 drivers.
Packaged release source directories are convenient engineering inputs; formal
clean-Git requirements still apply to the frozen inputs and configurations.

Formal pipeline preflight loads `toolchains.yaml` and validates drivers and
execution backends per `target.compiler` and per ISA:

- `compiler: gcc` requires an absolute, executable `gcc_path`.
- `compiler: llvm` requires an absolute, executable `clang_path`.
- `native` is mandatory. A non-native ISA requires an absolute, executable
  `qemu_path`; a configured `qemu_sysroot` must be an absolute directory.
- Every configured driver is hashed into the frozen plan so the campaign records
  exactly which compiler frontends were used. The plan also hashes every QEMU
  runner and records its trusted arguments and sysroot.

For the primary four-ISA matrix, copy
`configs/toolchains.qemu.example.yaml` to a machine-local formal config and
replace all placeholders. Select `x86_64`, `aarch64`, `riscv64`, and
`loongarch64` in the target. On a host where one of those ISAs is native, mark
only that entry `native: true` and remove its QEMU fields. On macOS, Linux ELF
binaries are not native even when the CPU architecture matches; use a trusted
Linux/QEMU container wrapper and keep `native: false`.
The checked-in `scripts/toolchains/docker-compiler-driver.sh` and
`scripts/toolchains/docker-qemu-user.sh` wrappers provide this host-to-container
bridge. Pin container images by digest in `driver_args`/`qemu_args`; an image tag
alone is mutable and is insufficient provenance for a paper run. Keep
container-internal compiler prefixes and sysroots in `driver_args`; use the
wrappers' repeatable `--mount` option only for dependencies stored outside the
image.
Use `scripts/toolchains/docker-image-ref.sh IMAGE` to obtain the local
repository plus immutable image ID used in formal wrapper arguments.
Build missing GCC17 cross drivers with
`scripts/toolchains/build-gcc17-cross.sh <isa>`. The script defaults to the
frozen source under `.work/gcc17-env/src`, writes each ISA to separate build
and install directories, and creates a detached writable source worktree per
ISA for GCC-generated backend documentation. The canonical source can be
redirected with `DEFUZZ_GCC17_SOURCE`; build roots and image selection use
`DEFUZZ_GCC17_CROSS_ROOT`, `DEFUZZ_GCC17_CROSS_IMAGE`, and
`DEFUZZ_GCC17_CROSS_PLATFORM`.
For RISC-V and LoongArch, first run
`scripts/toolchains/build-binutils-cross.sh <isa>` to build the pinned GNU
binutils 2.44 target tools. The current GCC 17 RISC-V backend emits Zc
attributes rejected by Debian 12's binutils 2.40. LoongArch additionally needs
the verified community glibc/Linux-header bundle under
`.work/gcc17-cross-env/loongarch-community-root`. Debian 12's QEMU 7.2 exits
with `SIGILL` on that LoongArch userspace; build the pinned QEMU 10.2.0
`loongarch64-linux-user` binary with
`scripts/toolchains/build-qemu-loongarch64.sh` and include it in the frozen
runtime image.
After all compiler and emulator builds finish, create the unified runtime image
with `scripts/toolchains/build-gcc17-four-isa-image.sh`. The command validates
the frozen GCC revision and the QEMU binary hash, then prints the immutable
`repository@sha256:image-id` reference for `toolchains.yaml`.

Before spending model budget, run:

```sh
scripts/toolchains/docker-qemu-smoke.sh <toolchains.yaml> gcc \
  x86_64 aarch64 riscv64 loongarch64
```

If an ISA is missing or the driver path is relative, absent, non-regular, or
non-executable, `pipeline --show-plan` already fails.

## Campaign artifacts

Each lane writes stage-local artifacts under:

```text
<output_root>/<run_id>/lanes/<target>/<variant>/rep-<NNN>/
```

The pipeline root also writes:

- `plan.json`: frozen, content-hashed execution plan
- `manifest.json`: top-level run status and lane inventory
- `campaign-results.json`
- `campaign-results.csv`
- `campaign-comparison.json`
- `campaign-comparison.csv`

Use `campaign-results.*` for per-part provenance, failures, skipped stages, and
`usage_missing_count`. Use `campaign-comparison.*` for aggregate tables over
complete, valid repetitions only.

## Exact command surface

Run all commands from [orchestrator/pyproject.toml](/Users/bytedance/projects/research/de-fuzz/main/orchestrator/pyproject.toml).

### Inspect the CLI

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment --help
uv run defuzz-experiment pipeline --help
uv run defuzz-experiment run-all --help
uv run defuzz-experiment invariant-generation --help
uv run defuzz-experiment checker-authoring --help
uv run defuzz-experiment source-mapping --help
uv run defuzz-experiment agent-audit --help
uv run defuzz-experiment ablation --help
uv run defuzz-experiment ablation without-rag --help
uv run defuzz-experiment ablation without-oracle --help
uv run defuzz-experiment ablation bare-agent --help
```

### Validate the formal YAML without writes

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment pipeline --config ../configs/experiments/formal.example.yaml --show-plan
```

This command should be the first gate for any campaign edit. It resolves all
paths relative to the YAML file, validates clean Git inputs, checks the required
reference documents, validates the local Pi config and credential environment
variable, and preflights compiler drivers before any run directory is created.

### Launch a full formal pipeline

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment pipeline --config ../configs/experiments/formal.example.yaml
```

### Resume an identical pipeline

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment pipeline --config ../configs/experiments/formal.example.yaml --resume
```

### Run Part I only

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment invariant-generation \
  --backend pi \
  --pi-config <local-pi-runtime.json> \
  --run-id part1-formal \
  --reference-root <reference-root> \
  --corpus-root <compiler-corpus-root> \
  --compiler gcc \
  --token-budget 120000 \
  --time-budget-minutes 90 \
  --repetitions 3 \
  --show-plan
```

Notes:

- Omit `--segment-end`, `--max-segments`, or partial sharding for the final
  full-corpus campaign unless you are explicitly executing one shard of the
  complete shard union.
- `without-rag` is the only supported Part I ablation and is invoked through the
  `ablation` command, not by inventing a new generation path name.

### Run Part II from a frozen Part I output

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment checker-authoring \
  --backend pi \
  --pi-config <local-pi-runtime.json> \
  --run-id part2-formal \
  --from-run <part-i-run-dir> \
  --reference-root <reference-root> \
  --source-root <de-fuzz-checkout> \
  --checker-root core/internal/oracle \
  --token-budget 120000 \
  --time-budget-minutes 90 \
  --repetitions 3 \
  --show-plan
```

### Run Part III from a frozen Part II bundle

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment agent-audit \
  --backend pi \
  --pi-config <local-pi-runtime.json> \
  --run-id part3-formal \
  --reference-root <reference-root> \
  --target-tree <compiler-audit-tree> \
  --from-run <part-ii-run-dir> \
  --mapping-from-run <source-mapping-run-dir> \
  --checker-source-root <de-fuzz-checkout> \
  --checker-update-rounds 1 \
  --compiler gcc \
  --toolchain-version gcc-17.0.0-20260531 \
  --mechanism canary \
  --isa x86_64 \
  --toolchains-config <toolchains.yaml> \
  --token-budget 120000 \
  --time-budget-minutes 90 \
  --repetitions 3 \
  --show-plan
```

Notes:

- Full Part III requires either a checker bundle or legacy online oracle
  commands. Formal paper runs use the frozen Part II bundle path, whose trusted
  dispatcher supplies both online feedback and offline verification.
- A generated checker may depend on a trusted checker already present in the
  frozen source baseline. Such transitive dependencies are recorded separately
  as `dependency_checker_ids` and included in the content-addressed runtime
  catalog. They are executed by the dispatcher but are not counted as generated
  checker successes or exposed to the agent as directly selectable outputs.
- `--from-run` resolves the repetition's Part II bundle and its normalized scoped
  invariants. `--mapping-from-run` resolves and validates all three mapping
  artifacts for that same repetition. For direct artifacts, use
  `--checker-bundle-manifest` and `--source-mappings`; the latter requires its
  adjacent summary and snapshot. Explicit invariants must match the bundle.
- If the legacy `--online-oracle-command` fallback is used for a standalone
  diagnostic, it is candidate-bound and must include `{candidate_fingerprint}`.
- `--verification-command` is reserved for trusted offline verification steps
  and is never sourced from Agent output.

### Run source mapping from a frozen Part I output

Source mapping anchors each accepted Part I invariant onto the compiler source
tree (pass, target functions, critical structs/conditions) through the
`source-mapper` SubAgent. It is a standalone experiment that consumes the same
`accepted-invariants.jsonl` as Part II. In v2 it is part of the pipeline hash
chain and feeds Part III.

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment source-mapping \
  --backend pi \
  --pi-config <local-pi-runtime.json> \
  --run-id source-mapping-formal \
  --from-run <part-i-run-dir> \
  --source-root <compiler-source-tree> \
  --compiler gcc \
  --token-budget 120000 \
  --time-budget-minutes 90 \
  --repetitions 3 \
  --show-plan
```

### The four experiments and the sequential `run-all`

DeFuzz exposes four independently runnable experiments, each with its own
standalone command documented above:

1. `invariant-generation` — Part I: generate and ground security invariants.
2. `checker-authoring` — Part II: turn accepted invariants into executable
   checkers.
3. `source-mapping` — anchor accepted invariants onto the compiler source tree.
4. `agent-audit` — Part III: the main invariant- and checker-guided audit.

Each can be launched on its own with the commands in the sections above, which
is the right path when you need to re-run, resume, or shard a single experiment.

For convenience, `run-all` chains all four in dependency order under one shared
backend, budget, and repetition count: Part I first, then checker authoring and
source mapping off the Part I invariants, then the agent audit off the Part II
checker bundle and validated mapping. It names each sub-run after the experiment
(optionally with `--run-prefix`) and links them with `--from-run` and
`--mapping-from-run`, stopping at the first failure. Budgets apply separately to
each stage and repetition. All required roots/configs are preflighted before
the first model call.

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment run-all \
  --backend pi \
  --pi-config <local-pi-runtime.json> \
  --run-prefix campaign \
  --corpus-root <compiler-corpus-root> \
  --reference-root <reference-root> \
  --source-root <compiler-source-tree> \
  --checker-source-root <de-fuzz-checkout> \
  --compiler gcc \
  --toolchains-config <toolchains.yaml> \
  --toolchain-version gcc-17.0.0-20260531 \
  --mechanism canary \
  --isa x86_64 \
  --token-budget 120000 \
  --time-budget-minutes 90 \
  --repetitions 3 \
  --show-plan
```

> `run-all` is engineering orchestration for running the four experiments back
> to back; it is **not** the paper's formal campaign. Formal paper
> evidence still comes only from the `pipeline` command driven by a frozen
> `mode: formal` YAML (`full`, `without-rag`, `without-oracle`, `bare-agent`).
> Do not report `run-all` output as formal campaign results.

### Run the three ablations

`without-rag`:

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment ablation without-rag \
  --baseline-run <full-part1-run-dir> \
  --backend pi \
  --pi-config <local-pi-runtime.json> \
  --reference-root <reference-root> \
  --corpus-root <compiler-corpus-root> \
  --compiler gcc \
  --token-budget 120000 \
  --time-budget-minutes 90 \
  --repetitions 3 \
  --show-plan
```

`without-oracle`:

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment ablation without-oracle \
  --baseline-run <full-part3-run-dir> \
  --backend pi \
  --pi-config <local-pi-runtime.json> \
  --reference-root <reference-root> \
  --target-tree <compiler-audit-tree> \
  --compiler gcc \
  --mechanism canary \
  --isa x86_64 \
  --checker-bundle-manifest <checker-bundle-manifest.json> \
  --mapping-from-run <source-mapping-run-dir> \
  --toolchains-config <toolchains.yaml> \
  --token-budget 120000 \
  --time-budget-minutes 90 \
  --repetitions 3 \
  --show-plan
```

`bare-agent`:

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment ablation bare-agent \
  --baseline-run <full-part3-run-dir> \
  --backend pi \
  --pi-config <local-pi-runtime.json> \
  --reference-root <reference-root> \
  --target-tree <compiler-audit-tree> \
  --compiler gcc \
  --mechanism canary \
  --isa x86_64 \
  --token-budget 120000 \
  --time-budget-minutes 90 \
  --repetitions 3 \
  --show-plan
```

For `without-oracle` and `bare-agent`, the baseline must be a Full Part III run
with matching model, budgets, repetitions, source content, reference content,
mechanism/ISA scope, and frozen checker bundle/toolchains hashes. The bare
worker never sees those verifier paths; the launcher derives them from the Full
baseline only for evaluator-side offline verification.

## Recommended execution order

1. Copy the Pi runtime/model/MCP examples to local, versioned config paths; freeze
   the API adapter, endpoint, model, reasoning and runtime settings.
2. Populate every credential variable referenced by `${NAME}`.
3. Freeze GCC at commit `f20bc4c2fe00928013c533e241b89ae3a6724ca1`,
   together with the reference tree and toolchain config.
4. Run `pipeline --show-plan` on the formal YAML until it validates cleanly.
5. Launch the typed pipeline with all four variants so each repetition shares
   the same frozen backend, budgets, source, reference, and verifier inputs.
6. Read `campaign-results.*` and `campaign-comparison.*` before extracting any
   paper table or figure so invalid or non-comparable repetitions are not mixed
   into aggregate claims.

## Failure interpretation

- `exit code 2`: configuration or input-preflight error
- top-level `manifest.json` with `execution_status=completed` but
  `result_valid=false`: run completed as a process but is not valid evidence
- repetition-level `usage_missing_count > 0`: do not use that repetition in
  token comparisons
- missing credential variable: populate the configured name; do not
  write the secret into the config
- missing or dirty input worktrees: fix the checkout state instead of editing
  around the guardrails

## Reporting discipline

- Phrase fixture smoke, the previous bounded TraeX pilot, and any bounded HTTP
  pilot only as engineering evidence that their respective paths are executable.
  They are not a completed formal campaign.
- Keep the paper narrative aligned to the pipeline rather than to outcome
  buckets: Invariant Generation, Checker Writing, Source Mapping, Agent Audit.
- Report only the supported ablations and do not add ad-hoc switches under the
  paper methodology.
