# DeFuzz Formal Experiment Runbook

This runbook documents how to execute the paper-facing experiment pipeline without
blurring the boundary between engineering validation and formal campaign data.
It is intentionally limited to the exact DeFuzz pipeline
`Invariant Generation -> Checker Writing -> Agent Audit` and the three supported
ablations `without-rag`, `without-oracle`, and `bare-agent`.

## Scope and boundaries

- Use `configs/experiments/example.yaml` only for fixture smoke coverage. It is
  an engineering check for typed config resolution, lane orchestration, hash
  chains, and resume semantics.
- Use `configs/experiments/formal.example.yaml` as the starting point for a real
  campaign. It stays in `mode: formal` and must be edited with real paths,
  pinned model identity, and executable toolchain driver paths before use.
- Do not claim fixture smoke, demo parity, or the bounded Part I pilot as paper
  results. They are engineering evidence that the pipeline runs end to end.
- Do not convert a pilot shard, partial range, or `max_segments` cap into a
  full-corpus claim. Full-corpus Part I evidence requires a complete,
  non-overlapping shard union. The current formal YAML contract accepts only
  one complete unsharded corpus (`shard_count: 1`, `max_segments: null`); run
  distributed shards as pilots until a validated shard-union manifest exists.

## Preconditions

Formal runs fail closed unless all of the following are true:

- Every formal input directory is inside a clean Git worktree. The pipeline
  checks `reference_root`, `checker.source_root`, every `target.corpus_root`,
  every `target.audit_source_roots`, `toolchains_config`, and the config file
  itself.
- `backend.model` is pinned in the YAML. Formal mode rejects an unset model.
- The selected agent binary is available on `PATH` or configured explicitly via
  `backend.binary`.
- The reference checkout contains the required review documents:
  `.claude/agents/defend-reviewer.md`, `docs/prompts/full-review.md`,
  `docs/bugs`, and `docs/invariants`.
- Each ISA used by a formal target has a configured absolute compiler driver in
  `toolchains.yaml`. GCC targets require `gcc_path`; LLVM targets require
  `clang_path`.
- `DEFUZZ_FAST_PLAN` is unset. Formal mode explicitly forbids it.
- The host/backend combination supports the required read-isolation boundary for
  formal agent stages.

## Clean-room and findings isolation

- Part I, Part II, and Part III all deny worker reads of
  `<reference-root>/findings`.
- Part III additionally runs against a sanitized, read-only source copy instead
  of the original audit target tree.
- The Part I `accepted-invariants.jsonl` artifact is hash-bound into Part III.
  Full and `without-oracle` receive the same field-minimized invariant view;
  `bare-agent` receives none of it.
- Part III provider `events` and `final` output are written to a temporary
  quarantine first. Only leak-checked structured reports enter permanent run
  artifacts; tainted output leaves a sanitized failure record.
- `demo-parity` reads the evaluator-only demo findings corpus only after workers
  exit. Treat that output as engineering comparison data, not as formal paper
  statistics.
- Historical bug documents may inform Part I RAG, but evaluator-only findings
  must never become worker-visible prompt material.

## Token comparability

- Token usage is collected per repetition with a dedicated sink and written into
  both detailed and summary artifacts.
- A repetition is token-comparable only when every provider call reports usage
  and the repetition stays within budget.
- If a provider returns a response but structured parsing fails, the returned
  usage still counts.
- If a call never receives provider usage, it is recorded as `usage_missing`
  rather than `0`.
- Formal comparisons across Full and ablations must use only repetitions with
  `usage_missing_count == 0` and no budget overshoot.

## Compiler-specific drivers

Formal pipeline preflight loads `toolchains.yaml` and validates drivers per
`target.compiler` and per ISA:

- `compiler: gcc` requires an absolute, executable `gcc_path`.
- `compiler: llvm` requires an absolute, executable `clang_path`.
- Every configured driver is hashed into the frozen plan so the campaign records
  exactly which compiler frontends were used.

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
uv run defuzz-experiment invariant-generation --help
uv run defuzz-experiment checker-authoring --help
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
reference documents, and preflights compiler drivers before any run directory is
created.

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
  --backend traex \
  --model <provider/model> \
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
  --backend traex \
  --model <provider/model> \
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
  --backend traex \
  --model <provider/model> \
  --run-id part3-formal \
  --reference-root <reference-root> \
  --target-tree <compiler-audit-tree> \
  --compiler gcc \
  --mechanism canary \
  --isa x86_64 \
  --checker-bundle-manifest <checker-bundle-manifest.json> \
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
- If the legacy `--online-oracle-command` fallback is used for a standalone
  diagnostic, it is candidate-bound and must include `{candidate_fingerprint}`.
- `--verification-command` is reserved for trusted offline verification steps
  and is never sourced from Agent output.

### Run the three supported ablations

`without-rag`:

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment ablation without-rag \
  --baseline-run <full-part1-run-dir> \
  --backend traex \
  --model <provider/model> \
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
  --backend traex \
  --model <provider/model> \
  --reference-root <reference-root> \
  --target-tree <compiler-audit-tree> \
  --compiler gcc \
  --mechanism canary \
  --isa x86_64 \
  --checker-bundle-manifest <checker-bundle-manifest.json> \
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
  --backend traex \
  --model <provider/model> \
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

1. Run `pipeline --show-plan` on the formal YAML until it validates cleanly.
2. Freeze the target compiler tree, reference tree, toolchain config, and model.
3. Run the Full pipeline for the chosen repetition count.
4. Run `without-rag` against the corresponding Full Part I baseline.
5. Run `without-oracle` and `bare-agent` against the corresponding Full Part III baseline.
6. Read `campaign-results.*` and `campaign-comparison.*` before extracting any
   paper table or figure so invalid or non-comparable repetitions are not mixed
   into aggregate claims.

## Failure interpretation

- `exit code 2`: configuration or input-preflight error
- top-level `manifest.json` with `execution_status=completed` but
  `result_valid=false`: run completed as a process but is not valid evidence
- repetition-level `usage_missing_count > 0`: do not use that repetition in
  token comparisons
- missing or dirty input worktrees: fix the checkout state instead of editing
  around the guardrails

## Reporting discipline

- Phrase the latest pilot only as engineering evidence that the formal path is
  executable.
- Keep the paper narrative aligned to the pipeline rather than to outcome
  buckets: Invariant Generation, Checker Writing, Agent Audit.
- Report only the supported ablations and do not add ad-hoc switches under the
  paper methodology.
