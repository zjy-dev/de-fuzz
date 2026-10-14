"""The audit runner must consume validated mappings in its actual worker requests."""

from __future__ import annotations

import json

import pytest
from test_audit_prompt_bundle import _FakeBackend, _reference

from defuzz_loop.agent_audit import run as audit
from defuzz_loop.experiment_engine import AgentResult, ExperimentPlan
from defuzz_loop.experiment_engine.source_mapping_stage import run as map_sources


class Mapper:
    provider = "pi"

    async def run(self, request):
        data = json.loads(request.prompt.split("<invariant>")[1].split("</invariant>")[0])
        return AgentResult(success=True, final={
            "phi_id": data["phi_id"], "compiler": data["compiler"], "isa": data["isa"],
            "mapping_confidence": "high",
            "level_1_pass": {"name": "unique_guard_anchor", "file": "compiler.c",
                             "line_start": 1, "line_end": 1, "evidence": "guard"},
        })


class AuditBackend(_FakeBackend):
    provider = "pi"


@pytest.mark.parametrize("variant", ["without-oracle", "bare-agent"])
@pytest.mark.parametrize("tamper", [False, True])
async def test_audit_mapping_visibility_and_semantic_identity(tmp_path, variant, tamper):
    reference = _reference(tmp_path)
    source = tmp_path / "compiler"
    source.mkdir()
    (source / "compiler.c").write_text("void guard() {}\n")
    invariants = tmp_path / "accepted.jsonl"
    row = {
        "invariant_id": "INV-MAPPED", "statement": "A canary guard is preserved",
        "mechanism": "stack-protector", "target": "loongarch64", "compiler": "gcc",
    }
    invariants.write_text(json.dumps(row) + "\n")
    map_plan = ExperimentPlan(
        run_id="map", experiment="source-mapping", source_root=source,
        parameters={"accepted_invariants": str(invariants), "isas": ["loongarch64"],
                    "require_host_read_isolation": False},
    )
    mapped = await map_sources(map_plan, 1, tmp_path / "mapping", Mapper())
    assert mapped.success
    if tamper:
        invariants.write_text(json.dumps({**row, "statement": "A different condition"}) + "\n")
    backend = AuditBackend()
    plan = ExperimentPlan(
        run_id="audit", experiment="agent-audit", variant=variant, source_root=source,
        parameters={
            "compiler": "gcc", "reference_root": str(reference),
            "accepted_invariants": str(invariants), "mechanisms": ["canary"],
            "isas": ["loongarch64"],
            "source_mappings": str(tmp_path / "mapping/source-mappings.jsonl"),
        },
    )
    result = await audit(plan, 1, tmp_path / "audit", backend)
    if tamper:
        assert not result.success
        assert "different invariant semantics" in result.error
        assert backend.requests == []
    else:
        assert result.success, result.errors
        assert len(backend.requests) == 1
        request = backend.requests[0]
        assert ("unique_guard_anchor" in request.prompt) is (variant != "bare-agent")
        assert request.metadata["use_callgraph"] is (variant != "bare-agent")
        assert request.metadata["worker_bundle_sha256"] in request.prompt
        assert str(source) not in request.prompt
        assert result.metadata["source_mappings"]["visible_mappings_by_family"] == {
            request.metadata["family"]: 0 if variant == "bare-agent" else 1,
        }
