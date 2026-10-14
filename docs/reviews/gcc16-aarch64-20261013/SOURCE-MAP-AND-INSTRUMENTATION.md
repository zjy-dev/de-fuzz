# Source-mapping & log-instrumentation completion — 2026-10-13

## Source-mapping [3/5] — COMPLETED

- 43/43 invariants mapped (vs. prior GCC17 run: 1/35), 0 failed
- model calls 324; tokens 5,228,078; usage_missing 0; provider errors 0
- every mapping carried 4–7 CallGraph queries and 3–6 data queries (MCP
  usage contract satisfied); each has a callgraph-binding to GCC 16.2
- wall time ~1h10m; dominated by per-invariant full-tree (1.4 GB / 159k files)
  materialize+snapshot passes.

## Log-instrumentation [4/5] — COMPLETED (new deterministic stage)

First real end-to-end run of the stage added this session:

- 43 mappings → 551 anchors (pass + functions + critical structs/conditions)
- 86 active `DEFUZZ_HIT("<mechanism>#<uid>:hit")` probes at safe `if` gates;
  465 `/* DEFUZZ-ANCHOR ... */` annotations at other anchors
- 58 GCC source files touched, 37 mechanisms, isa aarch64, failures []
- artifacts: instrumentation.diff (154 KB), log-id-catalog.json,
  instrumentation-plan.jsonl (250 KB), instrumentation-summary.json,
  defuzz-log-probes.txt
- probe block inserted immediately after `#include "system.h"`, gated on the
  DEFUZZ_LOG env var (no-op when unset); verified `-Wall -Wextra -Werror` clean
  earlier.

Known performance issue (non-blocking): the stage + the mapping loader each
materialize/hash the full 1.4 GB tree, so this deterministic stage took ~35m
though it edits only 58 files. Candidate optimization: copy/hash only the
mapped file set instead of WorkspaceBuilder(root) of the whole checkout.

## Agent-audit [5/5] — STARTED, then budget fix and relaunch

Consumes the ready checker bundle (43/43), the validated mappings, and the
GCC 16.2 host toolchains; runs families A–E with online checker feedback and
one checker-update round.

First audit launch (per-attempt budget 1,000,000) failed all 5 families after
2 calls each with `insufficient remaining token budget`: audit prompts carry
~360–400k input tokens/call (full mapping set + checker bundle + source), so
2 calls ≈ 700–800k and the worker's conservative reservation trips the 1M
per-attempt ceiling. The 40M stage cap was not the limiter (36M remained).

Fix: separate audit runtime config
`.work/gcc16-aarch64-run/pi-runtime.audit.json` with per-attempt
`token_budget` 2,500,000 (other fields unchanged, strict_usage false).
Only the agent-audit stage is relaunched via `run-audit.sh` (self-locating);
Parts I/II/source-mapping/log-instrumentation are reused unchanged.

Audit startup includes the same slow full-tree (1.4 GB / 159k files)
materialize + a `load_source_mappings` re-materialize in
`bind_mapping_handoff` (~35–40 min before the first model call).

