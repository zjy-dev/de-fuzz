# DeFuzz

An agentic system for finding bugs in compilers' own software-defense implementations
(stack canary, FORTIFY, CFI, shadow stack, IBT, …).

It tests the *compiler*, not the program: agents generate C seeds, an instrumented
GCC compiles them across a (checker→ISA) matrix, and a zero-false-positive oracle runs
invariant checkers to decide whether a defense mechanism *silently fails*.

## Architecture

DeFuzz is two pieces:

- **Go core** (`core/`, module `github.com/zjy-dev/de-fuzz`) — a deterministic process
  exposing two faces over the same `internal/` packages:
  - **gRPC** (`:50051`): deterministic nodes — `BuildService` / `CoverageService` /
    `OracleService` / `CheckerMetadataService`.
  - **MCP** (`:50052`): read-only agent tools — `search_source`, `query_invariants`,
    `coverage_diff`, `creduce_run`, `compile_exec`.
- **Python orchestrator** (`orchestrator/`, package `defuzz_loop`) — a LangGraph pipeline
  that drives the loop and three agents (Generator / Feedback / Minimizer).

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
invariant. The agent never chooses *which* invariant to attack.

## Running it

Build the Go core, then drive it from the orchestrator:

```sh
make build-core                       # → bin/defuzz-core (gRPC + MCP)
bin/defuzz-core --mechanism canary    # start the deterministic core

cd orchestrator
uv run defuzz-loop run --mechanism canary --experiment demo
```

Each run lands in `orchestrator/runs/<experiment>_<mechanism>_<UTC>/` with its own
`checkpoints.sqlite` + `manifest.json`. Three read-only subcommands inspect it:

```sh
uv run defuzz-loop inspect   --run-dir <dir>            # list the checkpoint chain
uv run defuzz-loop replay    --run-dir <dir> --checkpoint <id>
uv run defuzz-loop trace-bug --run-dir <dir> --bug <seed_id>   # back to deterministic evidence
```

The paper experiments have a separate, stable command surface. The launcher is
wired to the exact three-stage paper pipeline
`Invariant Generation -> Checker Writing -> Agent Audit`, creates an isolated
artifact tree and token summary for every repetition, and writes a final run
manifest. `--show-plan` remains side-effect free and reports whether the
selected agent binary is available:

```sh
cd orchestrator
uv run defuzz-experiment --help
uv run defuzz-experiment invariant-generation --help
uv run defuzz-experiment checker-authoring --from-run <part-i-run> --show-plan
uv run defuzz-experiment agent-audit --target-tree <compiler-tree> --show-plan
uv run defuzz-experiment ablation --help
```

The only supported ablations are `without-rag`, `without-oracle`, and
`bare-agent`. `without-rag` changes Part I only; `without-oracle` and
`bare-agent` still use the same frozen Part II verifier for offline admission.
A real run still depends on the selected `traex` or `codex` binary, model
credentials, source/reference trees, and compiler toolchain being available in
the launch environment. The reviewer corpus defaults to
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
  It stays in `mode: formal`, requires a pinned `backend.model`, refuses dirty
  Git inputs, forbids `DEFUZZ_FAST_PLAN`, preflights compiler-specific drivers
  from `toolchains.yaml`, rejects capped/sharded Part I selections, and will fail
  closed instead of silently using fixture runners.

Existing run IDs are refused unless `--resume` is given; upstream artifacts and
input snapshots are hash-checked. Formal runs keep a clean-room boundary around
evaluator-only findings: Part I, Part II, and Part III all deny reads of
`<reference-root>/findings`, and Part III also audits a sanitized source copy.
Full audit runs use the validated Part II bundle for candidate-bound online
feedback and frozen offline verification; `--online-oracle-command` remains a
legacy standalone fallback. `without-oracle` removes only the online feedback loop. Structured-output
failures retain provider-reported token usage, while calls that never receive
usage remain explicitly non-comparable and therefore invalidate a formal
repetition. `--demo-parity` writes an orchestrator-only engineering comparison
after workers exit and is not itself a formal paper result. See
[`docs/paper/experiment_status.md`](docs/paper/experiment_status.md) for the
current contract and [`docs/paper/experiment_runbook.md`](docs/paper/experiment_runbook.md)
for the execution procedure and exact commands.

The instrumented GCC under test is built out-of-tree; see
[docs/tech-docs/guides/building-instrumented-gcc.md](docs/tech-docs/guides/building-instrumented-gcc.md).
ISA→toolchain paths live in [`configs/toolchains.yaml`](configs/toolchains.yaml).

## Docs

- Architecture (authoritative): [docs/tech-docs/architecture/agentic-loop-redesign.md](docs/tech-docs/architecture/agentic-loop-redesign.md)
- System overview (components + dataflow): [docs/tech-docs/architecture/overview.md](docs/tech-docs/architecture/overview.md)
- Tech stack: [docs/tech-docs/reference/tech-stack.md](docs/tech-docs/reference/tech-stack.md)
- Full index: [docs/tech-docs/README.md](docs/tech-docs/README.md)

中文说明见 [README-zh.md](README-zh.md)。
