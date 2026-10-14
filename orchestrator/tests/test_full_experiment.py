from __future__ import annotations

import argparse
import json
from pathlib import Path
from types import SimpleNamespace

from defuzz_loop.full_experiment import execute


def args(root: Path, **overrides):
    values = dict(
        mode="offline",
        run_id="test",
        output_root=root,
        resume=False,
        force_stage=[],
        all_invariants=False,
        rounds=2,
        seed=7,
        model=None,
        src_root="/tmp/gcc",
    )
    values.update(overrides)
    return argparse.Namespace(**values)


def test_offline_full_run_manifest_and_artifacts(tmp_path: Path) -> None:
    repo = Path(__file__).resolve().parents[2]
    code, root = execute(args(tmp_path), repo)
    assert code == 0
    manifest = json.loads((root / "manifest.json").read_text())
    assert manifest["status"] == "success"
    assert set(manifest["stages"]) == {"invariants", "source-mapping", "fuzz"}
    assert (root / "01-invariants" / "validated.json").is_file()
    assert (root / "02-source-mapping" / "mcp-query-summaries.json").is_file()
    assert (root / "03-fuzz" / "blackboard" / "round-002.json").is_file()


def test_resume_and_force_stage(tmp_path: Path) -> None:
    repo = Path(__file__).resolve().parents[2]
    _, root = execute(args(tmp_path), repo)
    first = json.loads((root / "manifest.json").read_text())["stages"]["invariants"]["started_at"]
    code, _ = execute(args(tmp_path, resume=True), repo)
    assert code == 0
    assert (
        json.loads((root / "manifest.json").read_text())["stages"]["invariants"]["started_at"]
        == first
    )
    code, _ = execute(args(tmp_path, resume=True, force_stage=["invariants"]), repo)
    assert code == 0


def test_live_core_blocker_is_persisted(tmp_path: Path, monkeypatch) -> None:
    from defuzz_loop import full_experiment
    from defuzz_loop.experiment_engine import pi_backend

    repo = Path(__file__).resolve().parents[2]
    monkeypatch.setattr(pi_backend, "PiAgentBackend", lambda *_a, **_k: SimpleNamespace(
        config_sha256="fixture", config={"input_hashes": {}}, model="fixture/model",
        validate_credentials=lambda: None,
    ))
    monkeypatch.setattr(
        full_experiment,
        "_invoke_agent",
        lambda *a, **k: (
            {"invariants": full_experiment.STACK}
            if a[2] == "invariant-reviewer" and "Generate" in a[3]
            else {"phi_id": "SP-001"}
        ),
    )
    code, root = execute(args(tmp_path, mode="live", pi_config=tmp_path / "runtime.json"), repo)
    assert code == 1
    manifest = json.loads((root / "manifest.json").read_text())
    assert manifest["stages"]["fuzz"]["status"] == "failed"
    assert "TODO(core-mcp)" in manifest["failure_reason"]
    assert (root / "logs" / "fuzz.error.log").is_file()
