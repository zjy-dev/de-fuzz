# Part II directory-collision fix and resume — 2026-10-13

## Problem

The first launch stopped at Part II (checker-authoring): all 43 attempts failed
in ~14s with `WorkspaceSecurityError: deny_read_path <repo-root> collides with
agent output_dir ...`. No model call occurred (0 `request.json`,
`usage_missing_count == 43`). Root cause: the output root lived **inside** the
repo (`.work/gcc16-aarch64-run/...`), while Part II denies its checker source
tree — which was the repo root `/Users/bytedance/projects/research/de-fuzz/main`.
The deny root therefore contained every agent `output_dir`.

A first fix (output root moved out-of-repo) exposed a second, deeper conflict
specific to Part II: the Pi worker runtime (`agent-runtime/dist/worker.js`)
lives **inside** the repo, so denying the whole repo root also denied a required
runtime path:
`deny path contains a required runtime path: <repo-root>`.
(The earlier GCC17 run avoided this because the OpenCode binary was external;
the Pi worker is in-repo.)

## Fix (no isolation weakened)

Two frozen inputs moved out of the repo worktree; the repo-root deny boundary
is replaced by denying only the external checker checkout, so the in-repo Pi
worker runtime stays readable while worker reads of the checker tree remain
blocked:

- Output root: `/Users/bytedance/projects/research/defuzz-experiment-runs/gcc16-aarch64-20261013`
- Checker source (`--checker-source-root` for Part II and the audit):
  `/Users/bytedance/projects/research/defuzz-experiment-inputs/checker-src-gcc16-20261013`
  — a clean copy of the self-contained Go module under `core/`
  (module `github.com/zjy-dev/de-fuzz`, 105 tracked files, no replace
  directives). Verified it builds standalone: `go build ./internal/oracle/...`
  and the `defuzz-candidate-dispatcher` both compile out-of-tree.

Verification before relaunch:
- `validate_agent_path_isolation` with the external checker checkout denied:
  passes, and `agent-runtime/dist/worker.js` is **not** under any deny root.
- `checker-authoring --show-plan` and `source-mapping --show-plan` from the new
  root both resolve cleanly.

Part I is unchanged and reused as-is (copied into the out-of-repo root with the
same run_id, `status: completed`, 50 accepted invariants, 173/173 segments,
1,576,298 tokens). No model work was repeated.

## Resume

Driver: [resume-stages.sh](file:///Users/bytedance/projects/research/de-fuzz/main/.work/gcc16-aarch64-run/resume-stages.sh)
chains the four remaining standalone stages via `--from-run`
(`run-all` cannot resume mid-chain without re-running Part I):

1. checker-authoring  (Part II)  — Pi
2. source-mapping                 — Pi
3. log-instrumentation            — deterministic, `--backend none`
4. agent-audit        (Part III)  — Pi

Log: [resume-stages.log](file:///Users/bytedance/projects/research/de-fuzz/main/.work/gcc16-aarch64-run/resume-stages.log)

## Post-fix status (relaunch)

Part II confirmed running past the isolation gate: workers reached
(`request.json` written), no WorkspaceSecurityError, usage rows accumulating.
Part II is the long stage (per-invariant 3-attempt loop with real Go
compile/test/vet over 43 in-scope invariants). Remaining stages run after it.

This remains one engineering `run-all`-equivalent repetition (GCC 16.2.0,
AArch64, full variant), not a formal four-variant campaign.

## Second issue found while monitoring: per-attempt token ceiling

After the path fix, attempts reached the model but almost all still failed with
`insufficient remaining token budget`. Evidence (per-attempt ledger):

- The worker caps each attempt at `min(Pi runtime token_budget, sink remaining)`
  ([pi_backend.py](../../orchestrator/defuzz_loop/experiment_engine/pi_backend.py)).
  The checked-in example pins per-attempt `token_budget: 200000`; the CLI stage
  cap (20M) is not the limiter.
- Inside an attempt the worker reserves a conservative byte/3 input estimate;
  checker-author reads ~15–17 Go files, so attempts consumed 538k–573k measured
  tokens (input-dominated, only 4k–9k output) and were cut off right before
  closing out, with the valid patch already on disk.
- A cutoff attempt is terminal (1 attempt/invariant), reproducing the prior
  GCC17 run's ~1/42 checker yield.

The written checker was verified good: `git apply` clean and `go build
./internal/oracle/...` OK in a scratch copy of the external checker checkout
(new static checker + oracle + metadata registration). So model output quality
was fine; only headroom was lacking.

Fix (configuration only, recorded, not a guardrail weakening):

- Experiment-local Pi runtime config
  `.work/gcc16-aarch64-run/pi-runtime.local.json` (the checked-in example and
  its frozen hash are untouched): per-attempt `token_budget` 200000 → 1000000,
  other fields unchanged (max_model_calls 32, max_output_tokens 8192, strict_usage).
- Stage safety ceiling `--token-budget` 20M → 40M in `resume-stages.sh`
  (43 in-scope invariants × ~0.6–1M). This is a safety ceiling, not promised
  spend; every call is still recorded and repetitions require usage_missing==0.

Part II was restarted clean with the new budgets (the ~1.7M tokens spent on
cut-off attempts are recorded but not reused). Part I remains unchanged.

## Third issue: one provider timeout cascaded to all 43 invariants

On the 1M-budget rerun the first invariant ran 10 healthy calls (inputs
10k→37k, 3–32s each); call 10 then hung ~304s and the gateway returned an
empty `terminated` response with **zero usage** (`usage_complete=false`).

With the runtime default `strict_usage: true`, a single missing-usage record
makes the backend refuse every later call
([pi_backend.py](../../orchestrator/defuzz_loop/experiment_engine/pi_backend.py),
"earlier model usage is missing; strict accounting blocks further calls").
Invariants 2–43 therefore made **zero** model calls (instant AgentError) and
Part II failed 43/43 from one transient blip. The provider timeout itself is
not reproducible (Part I had 539 calls, 0 missing) and is outside our control.

Fix (engineering-run resilience, evidence integrity preserved):

- Local config `pi-runtime.local.json` sets `strict_usage: false`. A transient
  missing-usage call no longer aborts the rest of the stage; it is still
  recorded per call and flags the repetition as non-token-comparable. Formal
  paper evidence still requires a later clean strict rerun with
  `usage_missing_count == 0`, per the runbook.
- Reusable monitor: `.work/gcc16-aarch64-run/status.sh`.

Part II and the just-started source-mapping were cleared and the full remaining
chain (Part II → mapping → log-instrumentation → audit) relaunched under the
resilient local config.


