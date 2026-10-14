# Pi SDK migration implementation record

Authority: [DeFuzz：基于 Pi SDK 完全替换 OpenCode 的重构计划](https://bytedance.larkoffice.com/docx/Bf6GdPPiJod5J7xPlhAchhd8nSg), revision 4, retrieved 2026-10-12.

Scope: replace every former agent execution entry with the same Pi SDK backend,
retaining Python orchestration, compiler/checker verdict semantics, isolation,
ablation boundaries and provenance. No automatic fallback to the old runtime.
Other independent backends remain supported.

## Baseline and milestones

The pre-migration dirty worktree was saved outside the repository at
`/tmp/defuzz-pre-pi-20261012-174906/` (`head.txt`, `status.txt`, `tracked.diff`,
`untracked.tar.gz`). Existing changes must be preserved.

- M0: SDK and gateway baseline verified, implementation protocol specified below.
- M1: worker and Python backend implemented; initial protocol/lifecycle validation passed.
- M2: ten definitions, strict loader, MCP direct exposure and filesystem policy implemented;
  local SDK/MCP fixture validation passed. Real CallGraph verification remains.
- M3: pipeline, four independent entries, online loop/campaign and legacy full
  runner use the shared Pi backend. Contracts and bounded repair are backend-independent.
- M4: old client/backend, project configuration, install script and runtime tests
  removed. Node/worker/definitions are in container and CI builds.
- M5: bounded live text/tools/online/write/repair validation completed; container
  SDK smoke passed natively on AArch64. The amd64 image builds, but QEMU nested
  namespaces on this host fail isolation preflight. No new full compiler experiment.

Package baseline: `@earendil-works/pi-coding-agent@1.1.0`; Node 24.15.0
locally, minimum 22.19.0. Lock all installed dependencies. Consult shipped
SDK types rather than repository main.

## Gateway evidence

Real local gateway `http://127.0.0.1:8787/v1`, official `openai-responses`
adapter, provider `defuzz`, model `coconut-gpt-5-6-terra-max`.
Disposable probe evidence: `/tmp/defuzz-pi-sdk-probe/evidence/`.

| Probe | Result |
|---|---|
| Text | `{"ok":true}`, one request, provider total 59 |
| Tool | `probe_echo("pi-tool-ok")` executed once, result returned to model, two requests with totals 130 and 141 |
| Abort | One initiated request aborted in 508 ms; SDK zero usage without provider usage, must be classified missing |

All three sessions reached `agent_settled`. Completed requests reported 330
tokens total; the aborted request has unknown usage, not zero cost. The 130-token
call reports 11 reasoning tokens already included in output, so they must not be
added again. Pricing is unknown.

These are SDK/gateway probes, not experiment results. Rate limiting, broken frames,
tool isolation and full backend integration remain separate validation work.

## Implementation verification (M1 / M2)

`agent-runtime/` pins coding-agent and pi-mcp to 1.1.0. Python
`PiAgentBackend` launches the compiled SDK worker directly, freezes referenced
inputs, and verifies streaming protocol/artifact integrity. All former runtime
entry points now select this backend; there is no legacy runtime fallback.

Commands completed:

- `cd agent-runtime && npm test`: 4 passed.
- `cd orchestrator && .venv/bin/python -m pytest tests/test_pi_backend.py -q`:
  15 passed, 18.63 seconds. Uses the real SDK and a local HTTP/MCP fixture,
  consumes no paid-model tokens.
- Targeted Ruff check of the new Python backend/config/tests: passed.

Coverage includes >1 MiB Unicode responses, >64 KiB protocol frames, split UTF-8,
malformed frames, missing terminal/sequence failures, 2 MB stderr draining,
reported/missing usage and cache/reasoning accounting, internal call-cap
enforcement, write tools, symlink/path escape policy, timeout, MCP readiness,
direct MCP calls, full truncated-output originals, stderr archival, and a
MCP call wall deadline. Remaining verification includes explicit parent task
cancellation, token-budget exhaustion, provider rate limiting, broader entry
integration, no-legacy-runtime container smoke and bounded real stage calls.

Additional SDK findings:

- Stdio MCP transports create detached process groups. The parent tracks their
  IDs and inspects numeric host process relationships during cleanup. IDs from a
  bubblewrap PID namespace are not used as host process IDs.
- Default MCP log capture does not persist stdio stderr. The approved transport
  factory uses the official `pi-mcp` `onStderr` callback, inherits only runtime
  environment names, and archives stderr into the permanent attempt.
- MCP `timeout` configuration is in seconds; DeFuzz configuration is milliseconds.
  A separate tool wall timer prevents progress notifications from extending it.
- SDK shutdown reason is `quit` in the locked TypeScript API.
- macOS OS-denied directories cannot even be resolved with lstat in the worker.
  Python canonicalizes deny roots before launching; worker gates use those paths.

## JSONL v1 and runtime decisions

Python owns one worker and one fresh session per attempt. Node directly imports
the SDK; no agent coordinator or CLI session export. Each control/output envelope
includes `protocol_version`, `request_id`, `run_id`, `stage_id`, `attempt_id`,
monotonic `seq`, `timestamp`, and a `session_id` once created.

Controls: `run`, `cancel`. Outputs: `ready`, `session_started`, `event`, `usage`,
`heartbeat`, and exactly one terminal `result` or `error`. Large bodies use
content-hashed artifact references. Parent reads stdout/stderr concurrently using
incremental byte framing, with explicit frame and total-output limits. Worker
awaits writes/backpressure and naturally exits only after persistent evidence and
the terminal frame are flushed.

Record runtime, schema, artifact and evidence validity separately. An exit code of
zero without a verified terminal frame cannot succeed. Missing usage prevents
further model requests under strict accounting.

Runtime config explicitly references model and MCP config, fixed model/thinking,
definitions root and request/attempt limits. Resolve and hash referenced files,
worker source/build/lockfile, and definitions. Credentials use named environment
references; no secret values enter snapshots or logs.

AgentDefinition v1: `schema_version`, `agent_id`, `description`, optional
`temperature`, `tools`, `deny_tools`, `required_tools`; unknown fields fail.
All definitions live in `agents/definitions/`. Read tools are `read`, `grep`,
`find`, `ls`; editing is limited to checker-author's approved workspace.
MCP names are `mcp__defuzz_callgraph__<tool>`. Direct tool exposure only; hidden
server default and explicit tool allowlist, with runtime tool-call enforcement.

Sessions use explicit model/settings/resource/session managers, no context files,
skills, automatic extensions, prompt templates or global defaults. SDK
`DefaultResourceLoader` supports explicit extension factories with discovery
disabled. `bindExtensions({})` precedes required-tool readiness checks and prompt.

Provider hooks persist raw structured provider events. Extension callbacks catch
exceptions internally: throwing inside a hook is insufficient for budget or
evidence enforcement. Gate the actual model stream entry; disable both provider
and session automatic retries/compaction/cache warming for first implementation.

MCP connections require explicit session shutdown before dispose. Pi's SDK
`dispose()` alone aborts/disconnects listeners but does not emit `session_shutdown`.
Use the public extension runner to emit shutdown, then dispose, plus process-group
cleanup on the Python side.

## Required verification

Retain existing business assertions. Add real SDK/local endpoint verification for
text/tools/usage/abort, >64KiB frames and >1MiB output, corrupt/missing terminal
frames, cancellation and stderr draining, denied reads/writes and MCP readiness,
schema repair with original context, permanent checker artifacts after cleanup,
budget exhaustion before internal follow-up calls, and an environment without
the old executable/configuration. Freeze any methodology change separately from
runtime migration.

## Final integration evidence (2026-10-12)

- Complete Python suite: **670 passed**, 196.83 seconds, before the final sandbox
  preflight and ambient-context refinements. Targeted post-refinement suite:
  **181 passed**, 136.92 seconds; final SDK/audit/checker-update evidence suite:
  **77 passed**, 51.23 seconds; installed `find` tool verification: **1 passed**.
- Node protocol/usage/policy suite: **4 passed**. Pi SDK fixture tests now include
  parent cancellation/MCP reaping, 429 errors, budget exhaustion, offline trace,
  CLI mapping/resume and mapping-to-audit handoff.
- Ruff: full Python source/test tree passed. Targeted mypy: **12 migrated source
  files passed**, with unrelated imports skipped. Full repository mypy still
  reports pre-existing missing stubs and type issues; it is not claimed green.
- Legacy full-runner final anchor validation: **3 tests passed**, 0.55 seconds.
- `uv lock --check`: passed; Node dependency install uses the committed lock.
- Final amd64 image: `localhost/defuzz-orchestrator:pi-final`.
  Immutable ID and final allowlisted source overlay are recorded in
  `.work/podman/pi-final-images.json` and the acceptance `evidence.json`.
- Native AArch64 container smoke: one local model fixture request, 60 reported
  tokens, ten definitions, bubblewrap isolation and offline trace passed.
  Container ID: `92f249fd9eae89ec03c6f3b27346ea0d7363343cb419b7bbde0ffa11222a0e6c`.
  Final source mounts used during verification are hashed in the acceptance report.

Live gateway validation uses explicit new sessions and temporary evidence roots:

| Validation | Outcome | Evidence |
|---|---|---|
| Online seed-generator | C source generated and compiled | `/tmp/defuzz-pi-m5-20261012/online/` |
| Checker-author write | Go implementation/tests written; gofmt and go test passed | `/tmp/defuzz-pi-m5-20261012/checker-attempt/` |
| Schema repair | Original task + invalid output + exact marker error retained; second attempt accepted | `/tmp/defuzz-pi-m5-20261012/recovery/` |
| Three-file mapping | 5 real CallGraph calls, then fixed budget stopped the attempt | `/tmp/defuzz-pi-m5-20261012/mapping/` |
| One-file bounded mapping | Source anchors validated; graph/source version difference reported | `/tmp/defuzz-pi-m5-mapping-bounded/` |

The first group reports **91,008 tokens over 16 requests**, including the failed
mapping attempt; the narrower mapping adds **10,241 tokens over 2 requests**.
All 18 requests have reported usage: **101,249 tokens** in total, pricing unknown.
The earlier M0 probes are separate (330 reported tokens, one aborted unknown call).
These are runtime checks, not formal compiler-defense results. The checker
write probe exercises a standalone Go observation function, not a newly accepted
DeFuzz scientific checker.

All old runtime files were moved outside the repo to
`/tmp/defuzz-pi-retired-runtime/` to preserve the dirty baseline; they are neither
imported nor part of builds. Definitions are validated from `agents/definitions/`.
`defend-reviewer` doctrine remains a read-only reference-corpus input; the former
project did not contain such a runtime agent. Its optional parity mirror is now
`agents/policies/defend-reviewer.md`.

Worker output archives live in evaluator-only `.evaluator-runtime/` beside the
stage output, with explicit OS read denial. Sanitized worker reports retain the
existing findings guard. Checker request/session/provider/MCP evidence and full
validation stdout/stderr survive workspace deletion; each attempt records its
parent, candidate patch and errors. Empty implementation and missing/incomplete
registration fail acceptance and are returned to the bounded repair loop.

Linux launches a user/PID namespace with read-only root bindings, selected
writable paths and hidden `/proc`. The namespace probe is run before model calls.
The local amd64-on-AArch64 QEMU environment rejects namespace creation; use a
native amd64 Linux host for the frozen GCC amd64 container campaign.
The SDK's search-tool probe can receive EPERM under macOS even for a usable
installed binary. Each attempt therefore copies the installed fd/rg binaries
into its isolated runtime `bin/`, records SHA-256 in `tool-binaries.jsonl`,
and sets `PI_OFFLINE=1` to prevent automatic tool downloads. `find` was verified
against an actual source fixture after this change.

## Remaining legacy names: explicit inventory

Production imports/configs/builds contain no old runtime path. The two names in
`docker/pi-smoke.py` assert that the retired executable/directory are absent.
The following historical records retain provenance rather than executable use:

- `docs/tech-docs/architecture/opencode-migration-plan.md` (marked superseded);
  `.gitignore` keeps that historical document tracked.
- `docs/paper/experiment_status.md`, historical provider observations.
- `docs/tech-docs/guides/podman.md`, dated 2026-10-11 acceptance table only.
- `docs/reviews/acceptance-20261011/{acceptance.md,repair-verification.md,report.md,report.html}`.
- `docs/reviews/gcc-aarch64-20261012-root-cause-analysis.md`,
  `docs/reviews/gcc-aarch64-20261012-root-cause-evidence.json`,
  `docs/reviews/gcc-aarch64-full-20261012-report.html`.
- This migration record and its authority title.

Ignored pre-existing `.autoresearch` run state and pytest caches may retain
historical strings. They are not loaded by the Pi worker or staged into builds.
