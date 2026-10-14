"""Offline tests for the deterministic log-instrumentation stage."""

from __future__ import annotations

import asyncio
import json
from pathlib import Path

from defuzz_loop.experiment_engine import log_instrumentation_stage as stage
from defuzz_loop.experiment_engine.models import ExperimentPlan
from defuzz_loop.experiment_engine.source_mapping_stage import sha256
from defuzz_loop.experiment_engine.workspace import WorkspaceBuilder

_FILE = """\
#include "config.h"
#include "system.h"

void aarch64_emit (void)
{
  int ready = compute ();
  if (aarch64_gcs_enabled ())
    emit_note ();
}
"""


def _build_mapping(root: Path, out: Path) -> tuple[Path, str]:
    """Write a completed source-mapping handoff over a tiny source tree."""

    (root / "gcc/config/aarch64").mkdir(parents=True)
    target = root / "gcc/config/aarch64/aarch64.cc"
    target.write_text(_FILE)

    mapping = {
        "phi_id": "INV-GCS-001", "compiler": "gcc", "isa": "aarch64",
        "mapping_confidence": "high",
        "level_1_pass": {
            "name": "aarch64_emit", "file": "gcc/config/aarch64/aarch64.cc",
            "line_start": 4, "line_end": 9, "evidence": "file-end emitter",
        },
        "level_2_functions": [
            {"name": "aarch64_emit", "file": "gcc/config/aarch64/aarch64.cc",
             "line": 4, "role": "emits the note"},
        ],
        "level_3_critical_structs": [],
        "level_3_critical_conditions": [
            {"expression": "if (aarch64_gcs_enabled ())",
             "file": "gcc/config/aarch64/aarch64.cc", "line": 7,
             "role": "gate; a false negative silently omits GCS"},
        ],
    }
    mappings_path = out / "source-mappings.jsonl"
    mappings_path.write_text(json.dumps(mapping) + "\n")

    builder = WorkspaceBuilder(root)
    files = builder.snapshot_files()
    snapshot = out / "source-snapshot.json"
    # The loader recomputes the tree identity, so only the files list must match.
    snapshot.write_text(json.dumps({
        "files": [{"path": p, "sha256": h} for p, h in sorted(files.items())],
    }))

    summary = {
        "status": "completed", "failed": 0, "mapped": 1,
        "source_mappings_sha256": sha256(mappings_path),
        "source_snapshot_sha256": sha256(snapshot),
        "source_tree_sha256": "tree-id",
        "invariant_sha256": {"INV-GCS-001": "x"},
    }
    (out / "source-mapping-summary.json").write_text(json.dumps(summary))
    return mappings_path, sha256(mappings_path)


def _run(root: Path, mappings_path: Path, mappings_sha: str, out: Path,
         accepted: Path) -> stage.StageResult:
    plan = ExperimentPlan.model_validate({
        "schema_version": 1, "run_id": "t", "experiment": "log-instrumentation",
        "variant": "full", "repetitions": 1, "source_root": str(root),
        "parameters": {
            "source_mappings": str(mappings_path), "source_mappings_sha256": mappings_sha,
            "source_root": str(root), "accepted_invariants": str(accepted), "compiler": "gcc",
        },
    })
    return asyncio.run(stage.run(plan, 1, out / "art"))


def test_instruments_condition_and_annotations(tmp_path: Path) -> None:
    root = tmp_path / "src"
    root.mkdir()
    handoff = tmp_path / "map"
    handoff.mkdir()
    mappings_path, mappings_sha = _build_mapping(root, handoff)
    accepted = tmp_path / "accepted.jsonl"
    accepted.write_text(
        json.dumps({"invariant_id": "INV-GCS-001", "mechanism": "shadow_stack"}) + "\n"
    )

    result = _run(root, mappings_path, mappings_sha, tmp_path, accepted)

    assert result.status == "completed", result.errors
    assert result.metrics["anchors"] == 3  # pass + function + condition
    assert result.metrics["active_probes"] == 1  # the if-gate
    catalog = json.loads((tmp_path / "art" / "log-id-catalog.json").read_text())
    assert catalog["mechanisms"] == ["shadow-stack"]
    assert all(cid.endswith(":hit") for cid in catalog["log_ids"])
    assert len(catalog["log_ids"]) == 3
    diff = (tmp_path / "art" / "instrumentation.diff").read_text()
    assert "DEFUZZ_HIT (\"shadow-stack#" in diff
    assert "DEFUZZ-LOG-PROBE" in diff  # probe block injected after system.h
    assert "DEFUZZ-ANCHOR" in diff
    plan_rows = [json.loads(line) for line in
                 (tmp_path / "art" / "instrumentation-plan.jsonl").read_text().splitlines()]
    assert {row["kind"] for row in plan_rows} == {"pass", "function", "condition"}
    assert {row["probe"] for row in plan_rows} == {"active", "annotation"}


def test_rejects_incomplete_mapping(tmp_path: Path) -> None:
    root = tmp_path / "src"
    root.mkdir()
    handoff = tmp_path / "map"
    handoff.mkdir()
    mappings_path, mappings_sha = _build_mapping(root, handoff)
    summary_path = handoff / "source-mapping-summary.json"
    summary = json.loads(summary_path.read_text())
    summary["failed"] = 3
    summary_path.write_text(json.dumps(summary))
    accepted = tmp_path / "accepted.jsonl"
    accepted.write_text(
        json.dumps({"invariant_id": "INV-GCS-001", "mechanism": "shadow_stack"}) + "\n"
    )

    result = _run(root, mappings_path, mappings_sha, tmp_path, accepted)
    assert result.status == "failed"
    assert any("not completed" in err for err in result.errors)
