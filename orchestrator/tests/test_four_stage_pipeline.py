"""Versioned campaign handoff and immutable mapping reuse."""

from __future__ import annotations

import json
from dataclasses import replace

import pytest
import yaml
from test_experiment_pipeline import _write_config

from defuzz_loop.experiment_engine import ArtifactRef, StageResult
from defuzz_loop.experiment_engine.pipeline import (
    PipelineRunners,
    load_pipeline_config,
    run_pipeline,
)


def config_v2(tmp_path, variants=None):
    path = _write_config(tmp_path, variants=variants)
    payload = yaml.safe_load(path.read_text())
    payload["schema_version"] = 2
    path.write_text(yaml.safe_dump(payload))
    return load_pipeline_config(path)


async def test_v2_mapping_handoff_reuse_and_resume(tmp_path):
    config = config_v2(tmp_path, ["full", "without-oracle", "bare-agent"])
    fixture = PipelineRunners.fixture_smoke()
    calls = []

    async def mapping(plan, repetition, output_dir, backend):
        calls.append(("mapping", plan.variant))
        return await fixture.source_mapping(plan, repetition, output_dir, backend)

    async def audit(plan, repetition, output_dir, backend):
        calls.append(("audit", plan.variant))
        assert plan.parameters["source_mappings"]
        assert plan.parameters["checker_source_root"] == str(config.checker.source_root)
        assert plan.parameters["checker_update_rounds"] == 1
        return await fixture.part_iii(plan, repetition, output_dir, backend)

    runners = replace(fixture, source_mapping=mapping, part_iii=audit)
    result = await run_pipeline(config, runners=runners)
    assert result.result_valid, [lane.model_dump() for lane in result.lanes]
    assert calls == [
        ("mapping", "full"), ("audit", "full"),
        ("audit", "without-oracle"), ("audit", "bare-agent"),
    ]
    mapping_hashes = []
    for lane in result.lanes:
        assert list(lane.stages) == ["part_i", "part_ii", "source_mapping", "part_iii"]
        ref = lane.stages["source_mapping"].required_artifact
        mapping_hashes.append(ref.sha256)
        assert any(a.sha256 == ref.sha256 for a in lane.stages["part_iii"].input_artifacts)
    assert len(set(mapping_hashes)) == 1
    resumed = await run_pipeline(config, runners=runners, resume=True)
    assert resumed.result_valid
    assert len(calls) == 4
    rows = json.loads((config.output_root / config.run_id / "campaign-results.json").read_text())
    # Campaign rows must account for the mapping stage as well.
    assert "source_mapping" in json.dumps(rows)


@pytest.mark.parametrize("filename", [
    "source-mappings.jsonl", "source-mapping-summary.json", "source-snapshot.json",
])
async def test_v2_resume_rejects_mapping_tamper(tmp_path, filename):
    config = config_v2(tmp_path)
    result = await run_pipeline(config)
    assert result.result_valid
    artifact = result.lanes[0].lane_dir / "source_mapping/artifacts" / filename
    artifact.write_text(artifact.read_text() + "\n")
    with pytest.raises(ValueError, match="hash mismatch"):
        await run_pipeline(config, resume=True)


async def test_v2_failed_mapping_blocks_audit(tmp_path):
    config = config_v2(tmp_path)
    fixture = PipelineRunners.fixture_smoke()
    calls = []

    async def mapping(plan, repetition, output_dir, backend):
        return StageResult(stage="source-mapping", status="failed", error="missing mapping")

    async def audit(*args):
        calls.append("audit")
        return StageResult(stage="agent-audit", status="completed")

    result = await run_pipeline(
        config, runners=replace(fixture, source_mapping=mapping, part_iii=audit),
    )
    assert not result.result_valid
    assert result.lanes[0].stages["part_iii"].execution_status == "skipped"
    assert calls == []


async def test_v1_keeps_three_stage_contract(tmp_path):
    config = load_pipeline_config(_write_config(tmp_path))
    result = await run_pipeline(config)
    assert result.result_valid
    assert list(result.lanes[0].stages) == ["part_i", "part_ii", "part_iii"]
    assert (await run_pipeline(config, resume=True)).result_valid


async def test_v2_resume_rejects_checker_update_tamper(tmp_path):
    config = config_v2(tmp_path)
    fixture = PipelineRunners.fixture_smoke()

    async def audit(plan, repetition, output_dir, backend):
        result = await fixture.part_iii(plan, repetition, output_dir, backend)
        updated = output_dir / "checker-updates/decision.json"
        updated.parent.mkdir()
        updated.write_text('{"needed": false}\n')
        return result.model_copy(update={"artifacts": [
            *result.artifacts,
            ArtifactRef.from_path(updated, base_dir=output_dir, kind="checker-update"),
        ]})

    runners = replace(fixture, part_iii=audit)
    result = await run_pipeline(config, runners=runners)
    assert result.result_valid
    updated = result.lanes[0].lane_dir / "part_iii/artifacts/checker-updates/decision.json"
    updated.write_text('{"needed": true}\n')
    with pytest.raises(ValueError, match="checker update artifact hash mismatch"):
        await run_pipeline(config, runners=runners, resume=True)
