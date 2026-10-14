# DeFuzz

An agentic system for finding bugs in compilers' own software-defense implementations
(stack canary, FORTIFY, CFI, shadow stack, IBT, …).

It tests the *compiler*, not the program: agents generate C seeds, an instrumented
GCC compiles them across a (checker→ISA) matrix, and a zero-false-positive oracle runs
invariant checkers to decide whether a defense mechanism *silently fails*.

The research scope is invariant falsification, compiler defect diagnosis and
remediation in project-controlled test environments. Outputs include minimal
reproducers, checker evidence, fixes and regression tests for the open-source
compiler community. See [research scope and agent task examples](docs/research-scope.md)
and [agent context](AGENTS.md).

## Architecture

DeFuzz is two pieces:

- **Go core** (`core/`, module `github.com/zjy-dev/de-fuzz`) — a deterministic process
  exposing two faces over the same `internal/` packages:
  - **gRPC** (`:50051`): deterministic nodes — `BuildService` / `CoverageService` /
    `OracleService` / `CheckerMetadataService`.
  - **MCP** (`:50052`): read-only agent tools — `search_source`, `query_invariants`,
    `coverage_diff`, `creduce_run`, `compile_exec`.
- **Python orchestrator** (`orchestrator/`, package `defuzz_loop`) — an explicit
  `IterationLoop` that drives Generator / Feedback / Minimizer and optional LLM Oracle.

The design principle: **ReAct belongs to the agents, determinism belongs to the
orchestration.** Build/coverage/oracle are hard-wired into a fixed-order pipeline; agents
are called only at fixed positions and never talk to each other directly — they coordinate
through a versioned shared state (the blackboard). That buys reproducible trajectories,
per-edge ablation, and auditable bug conclusions.

## The loop

```
START → generator → routing → build → coverage → oracle → ⟨route⟩
            ▲                                                 │
            └──────────── bump (round+1, enum cursor) ←─ not_violated ┘
                                              violated → END
```

- `generator` — the Generator agent produces a seed for the current target invariant and
  picks a checker set; ISA is never a free dimension (each checker statically binds its ISAs).
- `routing` — expands the (checker→ISA) build matrix; cheap static checkers always run,
  differential checkers run their full ISA set (no pruning).
- `build` / `coverage` / `oracle` — deterministic gRPC nodes. A missing toolchain yields an
  error cell rather than a crash. Any checker `Fail` ⇒ violated.
- `route` — violated → (optional) Minimizer agent → END; not_violated → (optional) Feedback
  agent writes guidance → `bump` → next round.

Invariant scheduling is deterministic: the whole checker catalog is enumerated into a queue
at run init, and a cursor sweeps it, spending a fixed budget (N rounds OR T seconds) per
invariant. The agent never chooses *which* invariant to test.

## Running it

For a rootless Pod with the frozen GCC17 four-ISA runtime, shared tmpfs and
persistent evidence, see the [Podman runbook](docs/tech-docs/guides/podman.md).

Build the Go core, then drive it from the orchestrator:

```sh
make build-core                       # → bin/defuzz-core (gRPC + MCP)
bin/defuzz-core --mechanism canary    # start the deterministic core

cd orchestrator
uv run defuzz-loop run --mechanism canary --experiment demo \
  --pi-config ../configs/agents/pi-runtime.example.json
```

Each run lands in `orchestrator/runs/<experiment>_<mechanism>_<UTC>/` with
`manifest.json`, `events.jsonl`, `summary.json`, and per-round source, state and
accessible binaries under `rounds/`. In the Pod, completed rounds are archived
before their shared temporary build directories are removed. The old LangGraph
`inspect` / `replay` / `trace-bug` commands are retired; the container runbook
describes replaying the deterministic oracle on archived binaries.

The paper experiments have a separate, stable command surface. The launcher is
wired to the four-stage v2 pipeline
`Invariant Generation -> Checker Writing -> Source Mapping -> Agent Audit`, creates an isolated
artifact tree and token summary for every repetition, and writes a final run
manifest. `--show-plan` remains side-effect free and validates the selected
backend and its inputs:

```sh
cd orchestrator
uv run defuzz-experiment --help
uv run defuzz-experiment invariant-generation --help
uv run defuzz-experiment checker-authoring --from-run <part-i-run> --show-plan
uv run defuzz-experiment source-mapping --help
uv run defuzz-experiment agent-audit --target-tree <compiler-tree> --show-plan
uv run defuzz-experiment run-all --help
uv run defuzz-experiment ablation --help
```

The four campaign variants are `full`, `without-rag`, `without-oracle`, and
`bare-agent`. `without-rag` changes Part I only; `without-oracle` and
`bare-agent` still use the same frozen Part II verifier for offline admission.
New formal runs use the locked Pi SDK worker with one isolated session per attempt.
Source mapping and non-bare audit workers call the local CallGraph MCP;
mapped source anchors are hash-bound into the audit. The checked-in provider
example pins `coconut-gpt-5-6-terra-max` with explicit reasoning and sampling policy:

```yaml
schema_version: 2
backend:
  kind: pi
  config_path: ../agents/pi-runtime.example.json
```

Build the worker with `cd agent-runtime && npm ci && npm run build`
(Node >=22.19.0; container Node 24.15.0).
See [`configs/agents/pi-runtime.example.json`](configs/agents/pi-runtime.example.json),
which references the model and MCP configs.
The model file references the credential with `${DEFUZZ_COCONUT_API_KEY}`; the
credential value must remain in the process environment. A real run also needs
the local Responses endpoint, source/reference trees, and compiler toolchain to
be available. The formal GCC baseline is `17.0.0 experimental 20260531` at
commit `f20bc4c2fe00928013c533e241b89ae3a6724ca1`. The reviewer corpus defaults to
`/Users/bytedance/projects/research/defend-reviewer/main` and can be overridden
with `DEFUZZ_REFERENCE_ROOT` or `--reference-root`. Execution validates
required inputs before creating a run: Part II needs `--inputs` or `--from-run`,
Part III needs `--target-tree` and complete reference documents, and Part I
needs an explicit, existing `--corpus-root`. The typed pipeline has two
boundaries:

- `configs/experiments/example.yaml` is fixture-only smoke coverage for the
  typed pipeline, hash chain, and resume behavior. It is engineering validation,
  not a paper result.
- `configs/experiments/formal.example.yaml` is the formal campaign template.
  It uses schema v2, stays in `mode: formal`, requires `backend.kind: pi` plus a valid
  `config_path`, refuses dirty Git inputs, forbids `DEFUZZ_FAST_PLAN`, preflights
  compiler-specific drivers and QEMU execution paths from `toolchains.yaml`,
  rejects capped/sharded Part I selections, and fails closed instead of silently
  using fixture runners or executing a foreign-ISA binary on the host.

Existing run IDs are refused unless `--resume` is given; upstream artifacts and
input snapshots are hash-checked. Formal runs keep a clean-room boundary around
evaluator-only findings: Part I, Part II, and Part III all deny reads of
`<reference-root>/findings`, and Part III also audits a sanitized source copy.
Full audit runs can produce bounded, compiled checker updates for online
feedback; final offline verification keeps the original frozen Part II bundle.
Update decisions, patches, validation and bundle identities are recorded.
`--online-oracle-command` remains a
legacy standalone fallback. `without-oracle` removes only the online feedback loop. Structured-output
failures retain provider-reported token usage. Pi records each actual model request,
including tool and repair rounds; missing
usage remains explicitly non-comparable and invalidates a formal repetition.
Use `defuzz-inspect-attempt <attempt-directory>` for offline evidence inspection.
It validates the event and artifact trail without contacting a model or running tools.
`--demo-parity` writes an orchestrator-only engineering comparison
after workers exit and is not itself a formal paper result. See
[`docs/paper/experiment_status.md`](docs/paper/experiment_status.md) for the
historical experiment status and [`docs/paper/experiment_runbook.md`](docs/paper/experiment_runbook.md)
for the execution procedure and exact commands.
Schema v1 remains available for legacy three-stage replay. The bundled CallGraph
has GCC 16.2.0 x86_64/aarch64 and LLVM 23.1.2 labels; querying it does not establish
coverage of GCC 17 or all four ISAs.

The instrumented GCC under test is built out-of-tree; see
[docs/tech-docs/guides/building-instrumented-gcc.md](docs/tech-docs/guides/building-instrumented-gcc.md).
ISA→toolchain paths live in [`configs/toolchains.yaml`](configs/toolchains.yaml).
For a one-host `x86_64 / aarch64 / riscv64 / loongarch64` campaign, start from
[`configs/toolchains.qemu.example.yaml`](configs/toolchains.qemu.example.yaml).
Each ISA is configured explicitly as native or QEMU user-mode; formal plans hash
both the compiler driver and the QEMU runner.

## Docs

- Architecture (authoritative): [docs/tech-docs/architecture/agentic-loop-redesign.md](docs/tech-docs/architecture/agentic-loop-redesign.md)
- System overview (components + dataflow): [docs/tech-docs/architecture/overview.md](docs/tech-docs/architecture/overview.md)
- Tech stack: [docs/tech-docs/reference/tech-stack.md](docs/tech-docs/reference/tech-stack.md)
- Full index: [docs/tech-docs/README.md](docs/tech-docs/README.md)

中文说明见 [README-zh.md](README-zh.md)。
