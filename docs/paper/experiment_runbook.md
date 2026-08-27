# DeFuzz Formal Experiment Runbook

This runbook documents how to execute the paper-facing experiment pipeline without
blurring the boundary between engineering validation and formal campaign data.
It is intentionally limited to the exact DeFuzz pipeline
`Invariant Generation -> Checker Writing -> Agent Audit` and the four campaign
variants `full`, `without-rag`, `without-oracle`, and `bare-agent`.

## Scope and boundaries

- Use `configs/experiments/example.yaml` only for fixture smoke coverage. It is
  an engineering check for typed config resolution, lane orchestration, hash
  chains, and resume semantics.
- Use `configs/experiments/formal.example.yaml` as the starting point for a real
  campaign. It stays in `mode: formal` and must be edited with a real local HTTP
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

## Preconditions

Formal runs fail closed unless all of the following are true:

- Every formal input directory is inside a clean Git worktree. The pipeline
  checks `reference_root`, `checker.source_root`, every `target.corpus_root`,
  every `target.audit_source_roots`, `toolchains_config`, and the config file
  itself.
- The campaign uses `backend.kind: http` and `backend.config_path` resolves to a
  valid local YAML or JSON file. The model is pinned in that file; the formal
  example pins `coconut-gpt-5-6-terra-max` with `reasoning_effort: medium`.
- The environment variable named by the HTTP config's `api_key_env` is present.
  The config stores the environment variable name, never the credential value.
- The reference checkout contains the required review documents:
  `.claude/agents/defend-reviewer.md`, `docs/prompts/full-review.md`,
  `docs/bugs`, and `docs/invariants`.
- Each ISA used by a formal target has a configured absolute compiler driver in
  `toolchains.yaml`. GCC targets require `gcc_path`; LLVM targets require
  `clang_path`.
- `DEFUZZ_FAST_PLAN` is unset. Formal mode explicitly forbids it.
- The host/backend combination supports the required read-isolation boundary for
  formal agent stages.

## Local HTTP Responses configuration

Start from `configs/experiments/http-agent.example.yaml` and copy it to the path
selected by `backend.config_path`. The path is environment-specific and may be
replaced; keep the model and reasoning setting frozen across all four variants.
YAML, YML, and JSON are accepted. The checked-in YAML is equivalent to:

```yaml
http_agent:
  base_url: http://127.0.0.1:8787/v1
  model: coconut-gpt-5-6-terra-max
  api_key_env: DEFUZZ_COCONUT_API_KEY
  reasoning_effort: medium
  continuation_mode: full_input
  user_agent: codex-cli/1.0
```

`base_url` is the replaceable local gateway path; DeFuzz appends `/responses`
unless it is already present. Populate `DEFUZZ_COCONUT_API_KEY` through the shell or
secret manager before preflight. Do not add the value to YAML, JSON, command
history, or run artifacts. Formal preflight content-hashes this config and
requires it to belong to a clean Git worktree.

DeFuzz itself sends the Responses requests and executes its workspace-scoped
tools. It does not launch OpenCode, TraeX, or another agent CLI. A working
OpenCode provider block may be used only as a reference for the gateway URL and
model slug; it is not part of the experiment runtime or evidence chain.

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
  both detailed and summary artifacts. The direct HTTP backend records the
  provider-reported usage of every received Responses round, including tool and
  schema-repair rounds, then accumulates it for the agent turn and stage.
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

## Compiler-specific drivers

The formal GCC baseline is `17.0.0 experimental 20260531`, source commit
`f20bc4c2fe00928013c533e241b89ae3a6724ca1`. Both the Part I corpus and Part III
audit roots must come from that frozen checkout. The YAML version label records
the baseline, while the plan independently records the Git revision and hashes
the selected toolchain driver.

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
reference documents, validates the local HTTP config and credential environment
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
  --backend http \
  --http-config <local-http-config.yaml> \
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
  --backend http \
  --http-config <local-http-config.yaml> \
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
  --backend http \
  --http-config <local-http-config.yaml> \
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

### Run the three ablations

`without-rag`:

```sh
cd /Users/bytedance/projects/research/de-fuzz/main/orchestrator
uv run defuzz-experiment ablation without-rag \
  --baseline-run <full-part1-run-dir> \
  --backend http \
  --http-config <local-http-config.yaml> \
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
  --backend http \
  --http-config <local-http-config.yaml> \
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
  --backend http \
  --http-config <local-http-config.yaml> \
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

1. Copy the HTTP example to a local, versioned config path; keep Terra Max at
   medium reasoning or record a deliberate replacement before any run.
2. Export the credential under the config's `api_key_env` name.
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
- missing `api_key_env`: export the credential under the configured name; do not
  write the secret into the config
- missing or dirty input worktrees: fix the checkout state instead of editing
  around the guardrails

## Reporting discipline

- Phrase fixture smoke, the previous bounded TraeX pilot, and any bounded HTTP
  pilot only as engineering evidence that their respective paths are executable.
  They are not a completed formal campaign.
- Keep the paper narrative aligned to the pipeline rather than to outcome
  buckets: Invariant Generation, Checker Writing, Agent Audit.
- Report only the supported ablations and do not add ad-hoc switches under the
  paper methodology.
