"""Campaign-level aggregation tests for the typed three-part pipeline."""

from __future__ import annotations

import csv
import hashlib
import json
import statistics
import subprocess
import sys
from pathlib import Path
from typing import Literal, cast

import pytest

from defuzz_loop.experiment_engine.campaign_results import (
    CAMPAIGN_COMPARISON_SCHEMA,
    CAMPAIGN_RESULTS_SCHEMA,
    COMPARISON_COLUMNS,
    LONG_FORM_COLUMNS,
    write_campaign_results,
)
from defuzz_loop.experiment_engine.models import ExecutionStatus, VariantName
from defuzz_loop.experiment_engine.pipeline import (
    PipelineConfig,
    PipelineLaneResult,
    PipelineStageRecord,
    build_pipeline_plan,
    load_pipeline_config,
)

Part = Literal["part_i", "part_ii", "part_iii"]

_TARGET = "gcc-16-x86_64"
_PARTS: tuple[Part, ...] = ("part_i", "part_ii", "part_iii")
_VARIANTS: tuple[VariantName, ...] = (
    "full",
    "without-rag",
    "without-oracle",
    "bare-agent",
)


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _pipeline_config_payload(
    *, mode: Literal["formal", "fixture"], model: str | None
) -> dict[str, object]:
    backend: dict[str, object] = {"kind": "traex", "binary": "fake-agent"}
    if model is not None:
        backend["model"] = model
    return {
        "schema_version": 1,
        "run_id": f"{mode}-provenance",
        "mode": mode,
        "output_root": "runs",
        "backend": backend,
        "generation": {
            "path": "segmented-cot",
            "reference_root": "reference",
            "document_roots": ["documents"],
        },
        "toolchains_config": "toolchains.yaml",
        "targets": [
            {
                "id": "gcc-target",
                "compiler": "gcc",
                "version": "16.1.0",
                "corpus_root": "corpus",
                "audit_source_roots": ["audit-source"],
                "mechanisms": ["stack-protector"],
                "isas": ["x86_64"],
            }
        ],
        "checker": {"source_root": "checker-source"},
    }


def _write_pipeline_config_tree(
    root: Path, *, mode: Literal["formal", "fixture"], model: str | None
) -> tuple[Path, tuple[Path, ...]]:
    root.mkdir(parents=True, exist_ok=True)
    inputs = tuple(
        root / name
        for name in ("reference", "documents", "corpus", "audit-source", "checker-source")
    )
    for directory in inputs:
        directory.mkdir()
        (directory / "input.txt").write_text(f"{directory.name}\n", encoding="utf-8")
    toolchains = root / "toolchains.yaml"
    toolchains.write_text(
        json.dumps(
            {
                "toolchains": {
                    "x86_64": {
                        "gcc_path": str(Path(sys.executable).resolve()),
                        "native": True,
                    }
                }
            },
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )
    config_path = root / "pipeline.json"
    config_path.write_text(
        json.dumps(_pipeline_config_payload(mode=mode, model=model), sort_keys=True) + "\n",
        encoding="utf-8",
    )
    return config_path, (*inputs, toolchains)


def _git(repository: Path, *args: str) -> str:
    return subprocess.run(
        ("git", "-C", str(repository), *args),
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()


def _result_metrics(variant: VariantName, repetition: int, part: Part) -> dict[str, object]:
    """Return stage-shaped metrics plus decoys owned by other stages."""

    accepted = 6 + 4 * repetition + (1 if variant == "without-rag" else 0)
    if part == "part_i":
        return {
            "accepted_invariants": accepted,
            "first_passed": 900,
            "candidate_verified": 900,
        }
    if part == "part_ii":
        return {
            "invariants": accepted,
            "first_passed": accepted - 2,
            "final_passed": accepted - 1,
            "failed": 1,
            "accepted_invariants": 900,
            "candidate_verified": 900,
        }
    verified = 999 if variant == "bare-agent" and repetition == 2 else 2 * repetition
    return {
        "candidates": 4 * repetition,
        "candidate_admitted": 3 * repetition,
        "candidate_rejected": repetition,
        "candidate_verified": verified,
        "candidate_rejected_by_verification": repetition,
        "candidate_invalid": 0,
        "candidate_unverified": 0,
        "demo_parity_recall": 0.5 + repetition / 10,
        "demo_parity_profile": "demo-workset",
        "demo_parity_superset_coverage": 0.5 + repetition / 10,
        "time_to_first_verified_ms": float(250 * repetition),
        "accepted_invariants": 900,
        "first_passed": 900,
    }


def _token_usage(
    *, variant: VariantName, repetition: int, part: Part, reused: bool
) -> dict[str, object]:
    if reused:
        return {"reused": True}
    part_index = _PARTS.index(part) + 1
    total_tokens: int | None = 100 * part_index + repetition
    missing = 0
    if variant == "full" and repetition == 2 and part == "part_i":
        total_tokens = None
        missing = 1
    return {
        "consumed_total_tokens": total_tokens,
        "usage_missing_count": missing,
        "llm_latency_ms": float(10 * part_index + repetition),
        "elapsed_ms": float(20 * part_index + repetition),
    }


def _stage_record(
    lane_dir: Path,
    *,
    variant: VariantName,
    repetition: int,
    part: Part,
    reused: bool,
    invalid: bool,
) -> PipelineStageRecord:
    result_path = lane_dir / part / "result.json"
    result_path.parent.mkdir(parents=True, exist_ok=True)
    metrics = {} if reused else _result_metrics(variant, repetition, part)
    result_path.write_text(
        json.dumps({"metrics": metrics}, ensure_ascii=False, sort_keys=True) + "\n",
        encoding="utf-8",
    )

    is_skipped = invalid and part == "part_iii"
    execution_status: ExecutionStatus = "skipped" if is_skipped else "completed"
    metadata: dict[str, object] = {
        "token_usage": _token_usage(
            variant=variant, repetition=repetition, part=part, reused=reused
        )
    }
    if reused:
        metadata["reused_from_variant"] = "full"
    return PipelineStageRecord(
        stage=part,
        execution_status=execution_status,
        result_valid=not is_skipped,
        continuation_ready=not is_skipped,
        outcome="invalid" if is_skipped else "reused-frozen-upstream" if reused else "ok",
        result_path=f"{part}/result.json",
        result_sha256=_sha256(result_path),
        chain_sha256=f"{variant}-{repetition}-{part}",
        metadata=metadata,
    )


@pytest.fixture
def campaign_fixture(tmp_path: Path) -> tuple[Path, list[PipelineLaneResult]]:
    run_root = tmp_path / "campaign"
    lanes: list[PipelineLaneResult] = []
    for repetition in (1, 2):
        for variant in _VARIANTS:
            lane_dir = run_root / "lanes" / _TARGET / variant / f"rep-{repetition:03d}"
            invalid = variant == "bare-agent" and repetition == 2
            stages: dict[str, PipelineStageRecord] = {
                part: _stage_record(
                    lane_dir,
                    variant=variant,
                    repetition=repetition,
                    part=part,
                    reused=variant in {"without-oracle", "bare-agent"}
                    and part in {"part_i", "part_ii"},
                    invalid=invalid,
                )
                for part in _PARTS
            }
            lanes.append(
                PipelineLaneResult(
                    target_id=_TARGET,
                    repetition=repetition,
                    variant=variant,
                    execution_status="failed" if invalid else "completed",
                    result_valid=not invalid,
                    outcome="invalid" if invalid else "negative",
                    lane_dir=lane_dir,
                    chain_sha256=f"{variant}-{repetition}",
                    stages=stages,
                )
            )
    return run_root, lanes


def _long_rows(run_root: Path) -> list[dict[str, object]]:
    payload = json.loads((run_root / "campaign-results.json").read_text(encoding="utf-8"))
    return cast(list[dict[str, object]], payload["rows"])


def _row(
    rows: list[dict[str, object]], *, variant: str, repetition: int, part: str
) -> dict[str, object]:
    return next(
        row
        for row in rows
        if (row["variant"], row["repetition"], row["part"])
        == (variant, repetition, part)
    )


def _comparison_row(
    rows: list[dict[str, object]], *, variant: str, metric: str
) -> dict[str, object]:
    return next(
        row
        for row in rows
        if (row["target"], row["variant"], row["metric"])
        == (_TARGET, variant, metric)
    )


def test_writes_complete_long_form_json_csv_and_manifest_refs(
    campaign_fixture: tuple[Path, list[PipelineLaneResult]],
) -> None:
    run_root, lanes = campaign_fixture

    refs = write_campaign_results(run_root, lanes)

    expected = {
        "results_json": ("campaign-results.json", CAMPAIGN_RESULTS_SCHEMA),
        "results_csv": ("campaign-results.csv", CAMPAIGN_RESULTS_SCHEMA),
        "comparison_json": ("campaign-comparison.json", CAMPAIGN_COMPARISON_SCHEMA),
        "comparison_csv": ("campaign-comparison.csv", CAMPAIGN_COMPARISON_SCHEMA),
    }
    assert set(refs) == set(expected)
    for key, (relative_path, schema) in expected.items():
        reference = refs[key]
        path = run_root / relative_path
        assert reference == {
            "path": relative_path,
            "sha256": _sha256(path),
            "size_bytes": path.stat().st_size,
            "schema": schema,
        }

    results_payload = json.loads(
        (run_root / "campaign-results.json").read_text(encoding="utf-8")
    )
    assert results_payload["schema"] == CAMPAIGN_RESULTS_SCHEMA
    assert results_payload["columns"] == list(LONG_FORM_COLUMNS)
    rows = cast(list[dict[str, object]], results_payload["rows"])
    assert len(rows) == 4 * 2 * 3
    assert len(
        {(row["target"], row["variant"], row["repetition"], row["part"]) for row in rows}
    ) == 24

    with (run_root / "campaign-results.csv").open(encoding="utf-8", newline="") as stream:
        reader = csv.DictReader(stream)
        csv_rows = list(reader)
    assert reader.fieldnames == list(LONG_FORM_COLUMNS)
    assert len(csv_rows) == 24

    comparison_payload = json.loads(
        (run_root / "campaign-comparison.json").read_text(encoding="utf-8")
    )
    assert comparison_payload["schema"] == CAMPAIGN_COMPARISON_SCHEMA
    assert comparison_payload["group_by"] == ["target", "variant"]
    assert comparison_payload["statistics"] == {"std": "sample; zero for n=1"}
    with (run_root / "campaign-comparison.csv").open(
        encoding="utf-8", newline=""
    ) as stream:
        comparison_reader = csv.DictReader(stream)
        comparison_csv_rows = list(comparison_reader)
    assert comparison_reader.fieldnames == list(COMPARISON_COLUMNS)
    assert len(comparison_csv_rows) == len(comparison_payload["rows"])


def test_normalizes_part_metrics_missing_usage_and_reused_cost_attribution(
    campaign_fixture: tuple[Path, list[PipelineLaneResult]],
) -> None:
    run_root, lanes = campaign_fixture
    write_campaign_results(run_root, lanes)
    rows = _long_rows(run_root)

    part_i = _row(rows, variant="full", repetition=1, part="part_i")
    assert part_i["accepted_invariants"] == 10
    assert part_i["first_passed"] is None
    assert part_i["verified"] is None

    part_ii = _row(rows, variant="full", repetition=1, part="part_ii")
    assert part_ii["accepted_invariants"] is None
    assert part_ii["first_passed"] == 8
    assert part_ii["first_failed"] == 2
    assert part_ii["final_passed"] == 9
    assert part_ii["final_failed"] == 1
    assert part_ii["verified"] is None

    part_iii = _row(rows, variant="full", repetition=1, part="part_iii")
    assert part_iii["accepted_invariants"] is None
    assert part_iii["first_passed"] is None
    assert part_iii["candidates"] == 4
    assert part_iii["admitted"] == 3
    assert part_iii["admission_rejected"] == 1
    assert part_iii["verified"] == 2
    assert part_iii["verification_rejected"] == 1
    assert part_iii["demo_parity_profile"] == "demo-workset"

    missing_usage = _row(rows, variant="full", repetition=2, part="part_i")
    assert missing_usage["actual_total_tokens"] is None
    assert missing_usage["attributed_total_tokens"] is None
    assert missing_usage["usage_missing_count"] == 1

    for variant in ("without-oracle", "bare-agent"):
        for repetition in (1, 2):
            for part in ("part_i", "part_ii"):
                reused = _row(rows, variant=variant, repetition=repetition, part=part)
                full = _row(rows, variant="full", repetition=repetition, part=part)
                assert reused["reused"] is True
                assert reused["reused_from_variant"] == "full"
                assert reused["actual_total_tokens"] == 0
                assert reused["attributed_total_tokens"] == full["actual_total_tokens"]
                assert reused["usage_missing_count"] == full["usage_missing_count"]

    assert all(cast(float, row["elapsed_ms"]) >= 0 for row in rows)


def test_comparison_excludes_invalid_repetitions_and_uses_sample_std(
    campaign_fixture: tuple[Path, list[PipelineLaneResult]],
) -> None:
    run_root, lanes = campaign_fixture
    write_campaign_results(run_root, lanes)
    long_rows = _long_rows(run_root)
    skipped = _row(long_rows, variant="bare-agent", repetition=2, part="part_iii")
    assert skipped["execution_status"] == "skipped"
    assert skipped["result_valid"] is False
    assert skipped["repetition_valid"] is False
    assert skipped["outcome"] == "invalid"
    assert skipped["verified"] == 999

    payload = json.loads(
        (run_root / "campaign-comparison.json").read_text(encoding="utf-8")
    )
    comparisons = cast(list[dict[str, object]], payload["rows"])

    accepted = _comparison_row(comparisons, variant="full", metric="accepted_invariants")
    assert accepted["total_repetitions"] == 2
    assert accepted["valid_repetitions"] == 2
    assert accepted["n"] == 2
    assert accepted["mean"] == pytest.approx(statistics.fmean([10, 14]))
    assert accepted["std"] == pytest.approx(statistics.stdev([10, 14]))
    first_failed = _comparison_row(
        comparisons, variant="full", metric="first_failed"
    )
    assert first_failed["n"] == 2
    assert first_failed["mean"] == 2.0
    assert first_failed["std"] == 0.0

    bare_verified = _comparison_row(comparisons, variant="bare-agent", metric="verified")
    assert bare_verified["total_repetitions"] == 2
    assert bare_verified["valid_repetitions"] == 1
    assert bare_verified["n"] == 1
    assert bare_verified["mean"] == 2.0
    assert bare_verified["std"] == 0.0

    actual_tokens = _comparison_row(
        comparisons, variant="full", metric="actual_total_tokens"
    )
    assert actual_tokens["valid_repetitions"] == 2
    assert actual_tokens["n"] == 1
    assert actual_tokens["mean"] == 603.0
    assert actual_tokens["std"] == 0.0


def test_repeated_writes_are_byte_identical_without_duplicate_rows(
    campaign_fixture: tuple[Path, list[PipelineLaneResult]],
) -> None:
    run_root, lanes = campaign_fixture
    first_refs = write_campaign_results(run_root, lanes)
    paths = [run_root / cast(str, reference["path"]) for reference in first_refs.values()]
    first_bytes = {path.name: path.read_bytes() for path in paths}

    second_refs = write_campaign_results(run_root, list(reversed(lanes)))

    assert second_refs == first_refs
    assert {path.name: path.read_bytes() for path in paths} == first_bytes
    rows = _long_rows(run_root)
    assert len(rows) == 24
    assert len(
        {(row["target"], row["variant"], row["repetition"], row["part"]) for row in rows}
    ) == len(rows)


def test_comparison_keeps_incomplete_usage_and_latency_out_of_statistics(
    campaign_fixture: tuple[Path, list[PipelineLaneResult]],
) -> None:
    run_root, lanes = campaign_fixture
    stage = lanes[0].stages["part_ii"]
    stage.metadata["token_usage"] = {
        "consumed_total_tokens": 10,
        "usage_missing_count": None,
        "llm_latency_ms": None,
        "elapsed_ms": 1.0,
    }

    write_campaign_results(run_root, lanes)

    rows = _long_rows(run_root)
    incomplete = _row(rows, variant="full", repetition=1, part="part_ii")
    assert incomplete["usage_missing_count"] is None
    assert incomplete["llm_latency_ms"] is None
    comparisons = json.loads(
        (run_root / "campaign-comparison.json").read_text(encoding="utf-8")
    )["rows"]
    latency = _comparison_row(comparisons, variant="full", metric="llm_latency_ms")
    missing = _comparison_row(
        comparisons, variant="full", metric="usage_missing_count"
    )
    assert latency["n"] == 1
    assert missing["n"] == 1


def test_formal_config_requires_a_pinned_model_but_fixture_config_does_not() -> None:
    with pytest.raises(ValueError, match=r"formal mode requires backend\.model"):
        PipelineConfig.model_validate(_pipeline_config_payload(mode="formal", model=None))

    fixture = PipelineConfig.model_validate(
        _pipeline_config_payload(mode="fixture", model=None)
    )
    assert fixture.backend.model is None


def test_pipeline_plan_records_git_revision_for_every_input_snapshot(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    repository = tmp_path / "repository"
    config_path, input_paths = _write_pipeline_config_tree(
        repository, mode="formal", model="pinned-model"
    )
    backend = repository / "fake-agent"
    backend.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
    backend.chmod(0o755)
    _git(repository, "init")
    _git(repository, "config", "user.name", "Campaign Test")
    _git(repository, "config", "user.email", "campaign@example.invalid")
    _git(repository, "add", ".")
    _git(repository, "commit", "-m", "fixture")
    head = _git(repository, "rev-parse", "HEAD")
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: str(backend)
    )

    plan = build_pipeline_plan(load_pipeline_config(config_path), config_path=config_path)

    assert plan["source_revisions"] == {
        str(repository.resolve()): {"head_commit": head}
    }
    for path in input_paths:
        snapshot = plan["input_snapshots"][str(path.resolve())]
        assert len(snapshot["sha256"]) == 64
        assert snapshot["source_revision"] == {
            "repository_root": str(repository.resolve()),
            "head_commit": head,
            "path_relative_to_repository": path.relative_to(repository).as_posix(),
        }
    assert plan["config_file"]["source_revision"] == {
        "repository_root": str(repository.resolve()),
        "head_commit": head,
        "path_relative_to_repository": config_path.name,
    }

    outside = tmp_path / "outside-git"
    fixture_path, fixture_inputs = _write_pipeline_config_tree(
        outside, mode="fixture", model=None
    )
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: None
    )
    fixture_plan = build_pipeline_plan(
        load_pipeline_config(fixture_path), config_path=fixture_path
    )
    assert fixture_plan["source_revisions"] == {}
    assert all(
        "source_revision" not in fixture_plan["input_snapshots"][str(path.resolve())]
        for path in fixture_inputs
    )
