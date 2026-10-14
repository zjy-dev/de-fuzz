"""Mapping scope, snapshot integrity, and downstream evidence regression tests."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from defuzz_loop.experiment_engine import AgentResult, ExperimentPlan, WorkspaceBuilder
from defuzz_loop.experiment_engine.source_mapping_stage import (
    _mapping_isas,
    load_source_mappings,
    run,
)


class MappingBackend:
    provider = "pi"

    def __init__(self) -> None:
        self.requests = []

    async def run(self, request):
        self.requests.append(request)
        data = json.loads(request.prompt.split("<invariant>")[1].split("</invariant>")[0])
        return AgentResult(success=True, final={
            "phi_id": data["phi_id"], "compiler": data["compiler"], "isa": data["isa"],
            "mapping_confidence": "high",
            "level_1_pass": {"name": "guard", "file": "pass.cc", "line_start": 1,
                             "line_end": 2, "evidence": "guard insertion"},
        })


@pytest.mark.parametrize(("target", "requested", "expected"), [
    ("loongarch64", [], ["loongarch64"]),
    (["aarch64", "riscv64"], [], ["aarch64", "riscv64"]),
    ("generic", ["riscv64"], ["riscv64"]),
    ("generic", [], ["x86_64", "aarch64", "riscv64", "loongarch64"]),
    ("unknown", [], None),
    (["aarch64", "typo"], [], None),
    ("x86_64", ["loongarch64"], None),
])
def test_mapping_isas_preserves_scope(target, requested, expected):
    if expected is None:
        with pytest.raises(ValueError, match="mapping ISA"):
            _mapping_isas(target, requested)
    else:
        assert _mapping_isas(target, requested) == expected


@pytest.fixture
def mapping_inputs(tmp_path):
    source = tmp_path / "source"
    source.mkdir()
    (source / "pass.cc").write_text("// guard\nvoid guard() {}\n")
    accepted = tmp_path / "accepted.jsonl"
    accepted.write_text("\n".join(json.dumps(row) for row in [
        {"invariant_id": "LA-001", "statement": "Canary preserves the guard.",
         "compiler": "gcc", "mechanism": "stack_canary", "target": "loongarch64"},
        {"invariant_id": "EXCLUDED", "statement": "Bounded copies.",
         "compiler": "gcc", "mechanism": "fortify", "target": "x86_64"},
    ]) + "\n")
    plan = ExperimentPlan(
        run_id="mapping", experiment="source-mapping", source_root=source,
        parameters={"accepted_invariants": str(accepted), "mechanisms": ["canary"],
                    "isas": ["loongarch64"], "require_host_read_isolation": False},
    )
    return source, accepted, plan


async def test_mapping_stage_scope_and_content_hashes(mapping_inputs, tmp_path):
    source, _, plan = mapping_inputs
    backend = MappingBackend()
    destination = tmp_path / "output"
    result = await run(plan, 1, destination, backend)
    assert result.success, result.error
    path = destination / "source-mappings.jsonl"
    mappings, summary = load_source_mappings(path, source_root=source)
    assert [(m.phi_id, m.isa) for m in mappings] == [("LA-001", "loongarch64")]
    assert summary["scope"]["counts"] == {"total": 2, "selected": 1, "excluded": 1}
    assert len(backend.requests) == 1
    assert backend.requests[0].metadata["use_callgraph"] is True
    assert backend.requests[0].cwd != source
    (source / "pass.cc").write_text("// changed\n")
    with pytest.raises(ValueError, match="source no longer matches"):
        load_source_mappings(path, source_root=source)
    path.write_text(path.read_text() + "\n")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        load_source_mappings(path)


async def test_mapping_stage_rejects_absent_root(mapping_inputs, tmp_path):
    _, _, plan = mapping_inputs
    backend = MappingBackend()
    result = await run(plan.model_copy(update={"source_root": None}), 1, tmp_path / "out", backend)
    assert not result.success
    assert "explicit compiler source" in result.error
    assert backend.requests == []


def test_workspace_snapshot_matches_safe_symlink_copy(tmp_path: Path):
    source = tmp_path / "source"
    (source / "real").mkdir(parents=True)
    (source / "real/pass.cc").write_text("void guard() {}\n")
    (source / "alias").symlink_to(source / "real", target_is_directory=True)
    (source / "runs").mkdir()
    (source / "runs/ignored").write_text("irrelevant")
    builder = WorkspaceBuilder(source)
    copy = builder.materialize(tmp_path / "copy")
    assert builder.snapshot_files() == {f.path: f.sha256 for f in copy.files}
    assert set(builder.snapshot_files()) == {"real/pass.cc", "alias/pass.cc"}
