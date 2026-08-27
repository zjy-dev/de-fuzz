"""Fixture coverage for the content-addressed three-part pipeline."""

from __future__ import annotations

import hashlib
import json
import subprocess
from pathlib import Path
from typing import Any, cast

import pytest
import yaml

from defuzz_loop import experiments_cli
from defuzz_loop.experiment_engine import ArtifactRef, StageResult
from defuzz_loop.experiment_engine import pipeline as pipeline_mod
from defuzz_loop.experiment_engine.pipeline import (
    PipelineRunners,
    build_pipeline_plan,
    load_pipeline_config,
    run_pipeline,
)
from defuzz_loop.token_usage import current_token_usage_sink


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _artifact(path: Path, root: Path, kind: str) -> ArtifactRef:
    return ArtifactRef.from_path(path, base_dir=root, kind=kind)


def _write_bundle(output_dir: Path, *, coverage_complete: bool = False) -> Path:
    patch = output_dir / "bundle.patch"
    catalog = output_dir / "checker-catalog.json"
    dispatcher = output_dir / "bin" / "checker-dispatcher"
    dispatcher.parent.mkdir(parents=True, exist_ok=True)
    patch.write_text("fixture patch\n", encoding="utf-8")
    catalog.write_text('{"checkers": ["INV-1"]}\n', encoding="utf-8")
    dispatcher.write_text("fixture dispatcher\n", encoding="utf-8")
    dispatcher.chmod(0o755)
    artifacts = {
        name: {
            "path": path.relative_to(output_dir).as_posix(),
            "sha256": _sha256(path),
            "size_bytes": path.stat().st_size,
            "kind": name,
        }
        for name, path in {
            "cumulative_patch": patch,
            "catalog": catalog,
            "dispatcher": dispatcher,
        }.items()
    }
    payload: dict[str, Any] = {
        "schema_version": 1,
        "kind": "defuzz-checker-bundle",
        "status": "ready",
        "coverage_complete": coverage_complete,
        "source_root": "fixture-source",
        "source_root_sha256": "1" * 64,
        "source_tree_sha256": "1" * 64,
        "final_tree_sha256": "2" * 64,
        "invariants": [
            {
                "invariant_id": "INV-1",
                "final_status": "passed",
                "parent_tree_sha256": "1" * 64,
                "result_tree_sha256": "2" * 64,
                "files": [],
            },
            *(
                []
                if coverage_complete
                else [
                    {
                        "invariant_id": "INV-2",
                        "final_status": "failed",
                        "parent_tree_sha256": "2" * 64,
                        "result_tree_sha256": "2" * 64,
                        "files": [],
                    }
                ]
            ),
        ],
        "included_invariant_ids": ["INV-1"],
        "failed_invariant_ids": [] if coverage_complete else ["INV-2"],
        "budget_exhausted": False,
        "artifacts": artifacts,
        "validation": {
            "status": "passed",
            "commands": [],
            "build": {"status": "passed"},
        },
    }
    payload["bundle_id"] = hashlib.sha256(
        json.dumps(payload, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode(
            "utf-8"
        )
    ).hexdigest()
    manifest = output_dir / "checker-bundle-manifest.json"
    manifest.write_text(json.dumps(payload, sort_keys=True) + "\n", encoding="utf-8")
    return manifest


def _fixture_tree(tmp_path: Path) -> tuple[Path, Path, Path, Path]:
    corpus = tmp_path / "corpus"
    corpus.mkdir()
    (corpus / "input.c").write_text("int main(void) { return 0; }\n")
    audit = tmp_path / "audit-source"
    audit.mkdir()
    (audit / "source.c").write_text("void f(void) {}\n")
    checker = tmp_path / "checker-source"
    checker.mkdir()
    (checker / "go.mod").write_text("module fixture\n")
    reference = tmp_path / "reference"
    reference.mkdir()
    (reference / "guide.md").write_text("fixture\n")
    return corpus, audit, checker, reference


def _write_executable(path: Path, content: str = "#!/bin/sh\nexit 0\n") -> Path:
    path.write_text(content, encoding="utf-8")
    path.chmod(0o755)
    return path.resolve()


def _write_config(
    tmp_path: Path,
    *,
    targets: list[dict[str, Any]] | None = None,
    mode: str = "fixture",
    generation_path: str = "combined",
    variants: list[str] | None = None,
    repetitions: int = 1,
    require_verified_candidates: bool = True,
) -> Path:
    tmp_path.mkdir(parents=True, exist_ok=True)
    corpus, audit, checker, reference = _fixture_tree(tmp_path)
    toolchains = tmp_path / "toolchains.yaml"
    toolchain_payload: dict[str, Any] = {"toolchains": {}}
    if mode == "formal":
        driver = _write_executable(tmp_path / "fixture-gcc")
        toolchain_payload = {"toolchains": {"x86_64": {"gcc_path": str(driver), "native": True}}}
    toolchains.write_text(yaml.safe_dump(toolchain_payload, sort_keys=False), encoding="utf-8")
    payload = {
        "schema_version": 1,
        "run_id": "fixture-pipeline",
        "mode": mode,
        "output_root": "runs",
        "repetitions": repetitions,
        "variants": variants or ["full"],
        "backend": {
            "kind": "traex",
            "model": "fixture-model" if mode == "formal" else None,
            "extra_args": [],
        },
        "budgets": {
            stage: {"token_budget": 100, "time_budget_minutes": 1}
            for stage in ("part_i", "part_ii", "part_iii")
        },
        "generation": {
            "path": generation_path,
            "reference_root": reference.name,
            "document_roots": [],
            "max_segments": None if mode == "formal" else 5,
            "shard_index": 0,
            "shard_count": 1,
        },
        "toolchains_config": toolchains.name,
        "targets": targets
        or [
            {
                "id": "gcc-target",
                "compiler": "gcc",
                "version": "fixture",
                "corpus_root": corpus.name,
                "target_tree": audit.name,
                "mechanisms": ["canary"],
                "isas": ["x86_64"],
            }
        ],
        "checker": {
            "source_root": checker.name,
            "checker_root": "internal/oracle",
            "max_attempts": 2,
        },
        "audit": {
            "max_concurrency": 1,
            "oracle_rounds": 1,
            "demo_parity": False,
            "parity_profile": "demo-workset",
            "parity_threshold_metric": "recall",
            "require_verified_candidates": require_verified_candidates,
        },
    }
    path = tmp_path / "pipeline.yaml"
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")
    return path


def _fixture_runners(
    calls: list[tuple[str, str, str, int]],
    *,
    invariant_count: int = 1,
    tamper_bundle: bool = False,
    tamper_bundle_in_part_iii: bool = False,
    part_i_tokens: int | None = None,
) -> PipelineRunners:
    async def part_i(plan: Any, repetition: int, output_dir: Path, backend: Any) -> StageResult:
        del backend
        calls.append(
            (
                "part_i",
                plan.parameters["pipeline_target_id"],
                plan.parameters["pipeline_variant"],
                repetition,
            )
        )
        if part_i_tokens is not None:
            sink = current_token_usage_sink()
            assert sink is not None
            sink.record_external_usage(
                {
                    "type": "turn.completed",
                    "usage": {"total_tokens": part_i_tokens},
                }
            )
        path = output_dir / "accepted-invariants.jsonl"
        path.write_text(
            "".join(
                json.dumps({"invariant_id": f"INV-{index + 1}", "statement": "x"}) + "\n"
                for index in range(invariant_count)
            ),
            encoding="utf-8",
        )
        return StageResult(
            stage="invariant-generation",
            status="completed",
            artifacts=[_artifact(path, output_dir, "accepted-invariants")],
            metrics={"accepted_invariants": invariant_count},
        )

    async def part_ii(plan: Any, repetition: int, output_dir: Path, backend: Any) -> StageResult:
        del backend
        calls.append(
            (
                "part_ii",
                plan.parameters["pipeline_target_id"],
                plan.parameters["pipeline_variant"],
                repetition,
            )
        )
        input_path = Path(plan.parameters["accepted_invariants"])
        assert input_path.is_file()
        assert plan.parameters["accepted_invariants_sha256"] == _sha256(input_path)
        manifest = _write_bundle(output_dir, coverage_complete=False)
        if tamper_bundle:
            (output_dir / "checker-catalog.json").write_text("tampered\n")
        return StageResult(
            stage="checker-authoring",
            status="completed",
            artifacts=[_artifact(manifest, output_dir, "checker-bundle")],
        )

    async def part_iii(plan: Any, repetition: int, output_dir: Path, backend: Any) -> StageResult:
        del backend
        calls.append(
            (
                "part_iii",
                plan.parameters["pipeline_target_id"],
                plan.parameters["pipeline_variant"],
                repetition,
            )
        )
        bundle = Path(plan.parameters["checker_bundle_manifest"])
        assert bundle.name == "checker-bundle-manifest.json" and bundle.is_file()
        assert plan.parameters["checker_bundle_sha256"] == _sha256(bundle)
        assert Path(plan.parameters["toolchains_config"]).is_file()
        assert plan.parameters["require_verified_candidates"] is True
        invariant_path = Path(plan.parameters["accepted_invariants"])
        assert invariant_path.is_file()
        assert plan.parameters["accepted_invariants_sha256"] == _sha256(invariant_path)
        assert plan.parameters["parity_profile"] == "demo-workset"
        assert plan.parameters["parity_threshold_metric"] == "recall"
        if tamper_bundle_in_part_iii:
            (bundle.parent / "checker-catalog.json").write_text(
                "tampered during Part III\n", encoding="utf-8"
            )
        bundle_payload = json.loads(bundle.read_text(encoding="utf-8"))
        toolchains = Path(plan.parameters["toolchains_config"])
        summary = output_dir / "agent-audit-summary.json"
        summary.write_text(
            json.dumps(
                {
                    "execution_status": "completed",
                    "execution_completed": True,
                    "result_valid": True,
                    "continuation_ready": True,
                    "outcome": "no-verified-findings",
                    "variant": plan.variant,
                    "campaign_variant": plan.parameters["campaign_variant"],
                    "fixture_smoke": True,
                    "verified_candidates": [],
                    "checker_bundle": {
                        "bundle_id": bundle_payload["bundle_id"],
                        "manifest_path": str(bundle.resolve()),
                        "manifest_sha256": _sha256(bundle),
                        "toolchains_config": str(toolchains.resolve()),
                        "toolchains_config_sha256": _sha256(toolchains),
                    },
                }
            )
            + "\n",
            encoding="utf-8",
        )
        return StageResult(
            stage="agent-audit",
            status="completed",
            artifacts=[_artifact(summary, output_dir, "audit-summary")],
        )

    return PipelineRunners(part_i=part_i, part_ii=part_ii, part_iii=part_iii)


class _NoIsolationBackend:
    supports_host_read_isolation = False


@pytest.mark.asyncio
async def test_fixture_pipeline_runs_target_repetition_lanes_and_hashes_handoffs(
    tmp_path: Path,
) -> None:
    path = _write_config(tmp_path, repetitions=2)
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []

    result = await run_pipeline(config, runners=_fixture_runners(calls), config_path=path)

    assert result.execution_status == "completed"
    assert result.result_valid is True
    assert result.outcome == "negative"
    assert set(result.campaign_artifacts) == {
        "results_json",
        "results_csv",
        "comparison_json",
        "comparison_csv",
    }
    manifest = json.loads(result.manifest_path.read_text(encoding="utf-8"))
    assert manifest["campaign_artifacts"] == result.campaign_artifacts
    for reference in result.campaign_artifacts.values():
        artifact = result.manifest_path.parent / reference["path"]
        assert artifact.is_file()
        assert _sha256(artifact) == reference["sha256"]
        assert artifact.stat().st_size == reference["size_bytes"]
    stage_rows = json.loads(
        (result.manifest_path.parent / "campaign-results.json").read_text(encoding="utf-8")
    )["rows"]
    assert len(stage_rows) == 6
    assert all(row["elapsed_ms"] >= 0 for row in stage_rows)
    assert calls == [
        ("part_i", "gcc-target", "full", 1),
        ("part_ii", "gcc-target", "full", 1),
        ("part_iii", "gcc-target", "full", 1),
        ("part_i", "gcc-target", "full", 2),
        ("part_ii", "gcc-target", "full", 2),
        ("part_iii", "gcc-target", "full", 2),
    ]
    for lane in result.lanes:
        assert lane.result_valid
        assert lane.stages["part_ii"].metadata["coverage_complete"] is False
        assert lane.stages["part_iii"].outcome == "no-verified-findings"
        assert len({record.chain_sha256 for record in lane.stages.values()}) == 3
        assert (lane.lane_dir / "manifest.json").is_file()


@pytest.mark.asyncio
async def test_resume_plan_ignores_campaign_outputs_inside_git_input(
    tmp_path: Path,
) -> None:
    repository = tmp_path / "repository"
    repository.mkdir()
    subprocess.run(("git", "init"), cwd=repository, check=True, capture_output=True)
    (repository / "tracked.c").write_text("int main(void) { return 0; }\n", encoding="utf-8")
    (repository / ".gitignore").write_text(".work/\n", encoding="utf-8")
    subprocess.run(
        ("git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "add", "."),
        cwd=repository,
        check=True,
        capture_output=True,
    )
    subprocess.run(
        (
            "git",
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "commit",
            "-m",
            "fixture",
        ),
        cwd=repository,
        check=True,
        capture_output=True,
    )
    path = _write_config(tmp_path / "config")
    payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    payload["output_root"] = str(repository / ".work" / "runs")
    payload["generation"]["reference_root"] = str(repository)
    payload["targets"][0]["corpus_root"] = str(repository)
    payload["targets"][0]["target_tree"] = str(repository)
    payload["checker"]["source_root"] = str(repository)
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []
    runners = _fixture_runners(calls)
    plan_before = build_pipeline_plan(config, config_path=path)

    first = await run_pipeline(config, runners=runners, config_path=path)
    resumed = await run_pipeline(config, runners=runners, resume=True, config_path=path)

    assert first.result_valid is True
    assert resumed.result_valid is True
    assert len(calls) == 3
    plan_after = build_pipeline_plan(config, config_path=path)
    assert plan_after["plan_sha256"] == plan_before["plan_sha256"]
    snapshot = plan_after["input_snapshots"][str(repository.resolve())]
    assert snapshot["kind"] == "git-content-tree"


@pytest.mark.asyncio
async def test_zero_invariants_blocks_lane_without_calling_later_runners(
    tmp_path: Path,
) -> None:
    path = _write_config(tmp_path)
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []

    result = await run_pipeline(
        config,
        runners=_fixture_runners(calls, invariant_count=0),
        config_path=path,
    )

    assert not result.result_valid
    assert result.outcome == "blocked"
    assert calls == [("part_i", "gcc-target", "full", 1)]
    lane = result.lanes[0]
    assert lane.stages["part_i"].execution_status == "completed"
    assert lane.stages["part_i"].result_valid is False
    assert lane.stages["part_ii"].execution_status == "skipped"
    assert lane.stages["part_iii"].outcome == "blocked"
    rows = json.loads(
        (result.manifest_path.parent / "campaign-results.json").read_text(encoding="utf-8")
    )["rows"]
    assert len(rows) == 3
    assert [row["execution_status"] for row in rows].count("skipped") == 2


@pytest.mark.asyncio
async def test_tampered_checker_bundle_blocks_part_iii(tmp_path: Path) -> None:
    path = _write_config(tmp_path)
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []

    result = await run_pipeline(
        config,
        runners=_fixture_runners(calls, tamper_bundle=True),
        config_path=path,
    )

    assert not result.result_valid
    assert calls == [
        ("part_i", "gcc-target", "full", 1),
        ("part_ii", "gcc-target", "full", 1),
    ]
    assert "artifact SHA-256 mismatch" in (result.lanes[0].stages["part_ii"].error or "")
    assert result.lanes[0].stages["part_iii"].execution_status == "skipped"


@pytest.mark.asyncio
async def test_resume_skips_hash_valid_completed_lanes_and_rejects_tamper(
    tmp_path: Path,
) -> None:
    path = _write_config(tmp_path)
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []
    runners = _fixture_runners(calls)
    first = await run_pipeline(config, runners=runners, config_path=path)
    result_table = first.manifest_path.parent / "campaign-results.json"
    before_resume = result_table.read_bytes()

    resumed = await run_pipeline(config, runners=runners, resume=True, config_path=path)
    assert resumed.result_valid
    assert len(calls) == 3
    assert result_table.read_bytes() == before_resume
    assert len(json.loads(result_table.read_text(encoding="utf-8"))["rows"]) == 3

    summary = first.lanes[0].lane_dir / "part_iii/artifacts/agent-audit-summary.json"
    summary.write_text("{}\n", encoding="utf-8")
    with pytest.raises(ValueError, match="artifact hash mismatch"):
        await run_pipeline(config, runners=runners, resume=True, config_path=path)


@pytest.mark.asyncio
async def test_resume_retries_incomplete_lane_without_reusing_old_usage(
    tmp_path: Path,
) -> None:
    path = _write_config(tmp_path)
    config = load_pipeline_config(path)
    first_calls: list[tuple[str, str, str, int]] = []
    first = await run_pipeline(
        config,
        runners=_fixture_runners(first_calls, invariant_count=0),
        config_path=path,
    )
    stale_usage = first.lanes[0].lane_dir / "part_i/token_usage.jsonl"
    stale_usage.write_text('{"stale": true}\n', encoding="utf-8")

    retry_calls: list[tuple[str, str, str, int]] = []
    resumed = await run_pipeline(
        config,
        runners=_fixture_runners(retry_calls),
        resume=True,
        config_path=path,
    )

    assert resumed.result_valid
    assert retry_calls == [
        ("part_i", "gcc-target", "full", 1),
        ("part_ii", "gcc-target", "full", 1),
        ("part_iii", "gcc-target", "full", 1),
    ]
    assert not stale_usage.exists()


def test_typed_config_accepts_llvm_rag_and_rejects_formal_fast_plan(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    corpus, audit, _checker, _reference = _fixture_tree(tmp_path)
    target = {
        "id": "llvm",
        "compiler": "llvm",
        "version": "fixture",
        "corpus_root": corpus.name,
        "source_root": audit.name,
        "mechanisms": [],
        "isas": ["x86_64"],
    }
    path = _write_config(tmp_path / "llvm-config", targets=[target], generation_path="rag")
    llvm_config = load_pipeline_config(path)
    assert llvm_config.targets[0].compiler == "llvm"
    assert llvm_config.generation.path == "rag"

    formal_root = tmp_path / "formal-config"
    formal_path = _write_config(formal_root, mode="formal")
    formal = load_pipeline_config(formal_path)
    monkeypatch.setenv("DEFUZZ_FAST_PLAN", "1")
    with pytest.raises(ValueError, match="forbids DEFUZZ_FAST_PLAN"):
        build_pipeline_plan(formal, config_path=formal_path)


def test_formal_plan_records_compiler_driver_identity(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = _write_config(tmp_path, mode="formal")
    config = load_pipeline_config(path)
    driver = tmp_path / "fixture-gcc"
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda paths, *, output_root: None,
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__)

    plan = build_pipeline_plan(config, config_path=path)

    assert plan["toolchain_drivers"] == [
        {
            "target_id": "gcc-target",
            "isa": "x86_64",
            "compiler": "gcc",
            "path": str(driver.resolve()),
            "sha256": _sha256(driver),
            "size_bytes": driver.stat().st_size,
        }
    ]


@pytest.mark.parametrize(
    ("compiler", "toolchain_entry", "error"),
    [
        ("llvm", {"gcc_path": "DRIVER"}, "clang_path"),
        ("gcc", {"clang_path": "DRIVER"}, "gcc_path"),
        ("gcc", None, "ISA 'x86_64' is not configured"),
        ("gcc", {"gcc_path": "relative-gcc"}, "must be absolute"),
    ],
)
def test_formal_plan_rejects_invalid_compiler_driver_configuration(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    compiler: str,
    toolchain_entry: dict[str, str] | None,
    error: str,
) -> None:
    target = {
        "id": f"{compiler}-target",
        "compiler": compiler,
        "version": "fixture",
        "corpus_root": "corpus",
        "source_root": "audit-source",
        "mechanisms": ["canary"],
        "isas": ["x86_64"],
    }
    path = _write_config(tmp_path, mode="formal", targets=[target])
    driver = _write_executable(tmp_path / "configured-driver")
    payload: dict[str, Any] = {"toolchains": {}}
    if toolchain_entry is not None:
        payload["toolchains"]["x86_64"] = {
            key: str(driver) if value == "DRIVER" else value
            for key, value in toolchain_entry.items()
        }
    toolchains = tmp_path / "toolchains.yaml"
    toolchains.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda paths, *, output_root: None,
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__)

    with pytest.raises(ValueError, match=error):
        build_pipeline_plan(load_pipeline_config(path), config_path=path)


@pytest.mark.parametrize(("field", "value"), [("mechanisms", []), ("isas", [])])
def test_formal_plan_requires_non_empty_target_scope(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, field: str, value: list[str]
) -> None:
    path = _write_config(tmp_path, mode="formal")
    payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    payload["targets"][0][field] = value
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda paths, *, output_root: None,
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__)

    with pytest.raises(ValueError, match=f"target 'gcc-target'.*{field} must not be empty"):
        build_pipeline_plan(load_pipeline_config(path), config_path=path)


@pytest.mark.parametrize("field", ["mechanisms", "isas"])
def test_typed_config_rejects_duplicate_target_scope(tmp_path: Path, field: str) -> None:
    path = _write_config(tmp_path)
    payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    payload["targets"][0][field] = ["duplicate", "duplicate"]
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")

    with pytest.raises(ValueError, match=f"target {field} must be unique"):
        load_pipeline_config(path)


def test_typed_config_canonicalizes_target_scope_aliases(tmp_path: Path) -> None:
    path = _write_config(tmp_path)
    payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    payload["targets"][0]["mechanisms"] = ["stack-canary"]
    payload["targets"][0]["isas"] = ["x86-64"]
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")

    config = load_pipeline_config(path)

    assert config.targets[0].mechanisms == ["stack-protector"]
    assert config.targets[0].isas == ["x86_64"]


@pytest.mark.parametrize(
    ("toolchains_payload", "error"),
    [
        (["not", "a", "mapping"], "must be a mapping"),
        ({}, "must contain a 'toolchains' mapping"),
        ({"toolchains": []}, "must contain a 'toolchains' mapping"),
        ({"toolchains": {"x86_64": []}}, "entry must be a mapping"),
        ({"toolchains": {"x86_64": {"gcc_path": 42}}}, "must be a string"),
        (
            {"toolchains": {"x86_64": {"gcc_path": "/bin/cc", "typo": True}}},
            "unknown field",
        ),
    ],
)
def test_formal_plan_rejects_invalid_toolchains_yaml_shape(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    toolchains_payload: Any,
    error: str,
) -> None:
    path = _write_config(tmp_path, mode="formal")
    (tmp_path / "toolchains.yaml").write_text(
        yaml.safe_dump(toolchains_payload, sort_keys=False), encoding="utf-8"
    )
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda paths, *, output_root: None,
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__)

    with pytest.raises(ValueError, match=error):
        build_pipeline_plan(load_pipeline_config(path), config_path=path)


@pytest.mark.parametrize(
    ("driver_kind", "error"),
    [
        ("missing", "does not resolve"),
        ("directory", "not a regular file"),
        ("not-executable", "not executable"),
    ],
)
def test_formal_plan_requires_resolved_regular_executable_driver(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    driver_kind: str,
    error: str,
) -> None:
    path = _write_config(tmp_path, mode="formal")
    driver = tmp_path / f"driver-{driver_kind}"
    if driver_kind == "directory":
        driver.mkdir()
    elif driver_kind == "not-executable":
        driver.write_text("not executable\n", encoding="utf-8")
        driver.chmod(0o644)
    payload = {"toolchains": {"x86_64": {"gcc_path": str(driver)}}}
    (tmp_path / "toolchains.yaml").write_text(
        yaml.safe_dump(payload, sort_keys=False), encoding="utf-8"
    )
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda paths, *, output_root: None,
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__)

    with pytest.raises(ValueError, match=error):
        build_pipeline_plan(load_pipeline_config(path), config_path=path)


def test_formal_plan_rejects_malformed_toolchains_yaml(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = _write_config(tmp_path, mode="formal")
    (tmp_path / "toolchains.yaml").write_text("toolchains: [unterminated\n", encoding="utf-8")
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda paths, *, output_root: None,
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__)

    with pytest.raises(ValueError, match="toolchains config cannot be parsed"):
        build_pipeline_plan(load_pipeline_config(path), config_path=path)


def test_formal_driver_hash_changes_plan_identity(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = _write_config(tmp_path, mode="formal")
    config = load_pipeline_config(path)
    driver = tmp_path / "fixture-gcc"
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda paths, *, output_root: None,
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__)
    before = build_pipeline_plan(config, config_path=path)

    _write_executable(driver, "#!/bin/sh\n# changed\nexit 0\n")
    after = build_pipeline_plan(config, config_path=path)

    assert before["toolchain_drivers"][0]["sha256"] != after["toolchain_drivers"][0]["sha256"]
    assert before["plan_sha256"] != after["plan_sha256"]


def test_formal_config_requires_verified_candidates(tmp_path: Path) -> None:
    path = _write_config(tmp_path, mode="formal", require_verified_candidates=False)

    with pytest.raises(ValueError, match="requires audit.require_verified_candidates=true"):
        load_pipeline_config(path)


@pytest.mark.parametrize(
    ("generation_update", "error"),
    [
        ({"max_segments": 1}, "forbids generation.max_segments"),
        (
            {"max_segments": None, "shard_count": 2, "shard_index": 0},
            "requires the complete unsharded Part I corpus",
        ),
    ],
)
def test_formal_config_rejects_partial_part_i_selection(
    tmp_path: Path, generation_update: dict[str, int | None], error: str
) -> None:
    path = _write_config(tmp_path, mode="formal")
    payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    payload["generation"].update(generation_update)
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")

    with pytest.raises(ValueError, match=error):
        load_pipeline_config(path)


def test_typed_config_selects_parity_profile_and_metric(tmp_path: Path) -> None:
    path = _write_config(tmp_path)
    payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    payload["audit"].update(
        {
            "demo_parity": True,
            "parity_profile": "poc-verified",
            "parity_threshold": 0.75,
            "parity_threshold_metric": "f1",
        }
    )
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")

    config = load_pipeline_config(path)
    target = config.targets[0]
    manifest = tmp_path / "checker-bundle-manifest.json"
    manifest.write_text("{}\n", encoding="utf-8")
    accepted = tmp_path / "accepted-invariants.jsonl"
    accepted.write_text('{"invariant_id": "INV-1"}\n', encoding="utf-8")
    plan = pipeline_mod._stage_plan(
        config,
        target,
        "full",
        1,
        "part_iii",
        tmp_path / "lane",
        accepted_invariants=accepted,
        checker_bundle_manifest=manifest,
    )

    assert plan.parameters["demo_parity"] is True
    assert plan.parameters["parity_profile"] == "poc-verified"
    assert plan.parameters["parity_threshold"] == 0.75
    assert plan.parameters["parity_threshold_metric"] == "f1"


@pytest.mark.parametrize(
    ("field", "value"),
    [("parity_profile", "publication"), ("parity_threshold_metric", "precision")],
)
def test_typed_config_rejects_unknown_parity_policy_values(
    field: str, value: str, tmp_path: Path
) -> None:
    path = _write_config(tmp_path)
    payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    payload["audit"][field] = value
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")

    with pytest.raises(ValueError, match=field):
        load_pipeline_config(path)


def test_formal_plan_requires_backend_but_fixture_plan_records_optional_backend(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fixture_path = _write_config(tmp_path / "fixture")
    fixture = load_pipeline_config(fixture_path)
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: None)

    fixture_plan = build_pipeline_plan(fixture, config_path=fixture_path)
    assert fixture_plan["backend"]["available"] is False
    assert fixture_plan["backend"]["required"] is False
    assert fixture_plan["runner"] == "fixture-smoke"

    formal_path = _write_config(tmp_path / "formal", mode="formal")
    formal = load_pipeline_config(formal_path)
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda *args, **kwargs: None,
    )
    with pytest.raises(ValueError, match="requires an available agent binary"):
        build_pipeline_plan(formal, config_path=formal_path)


def test_formal_plan_accepts_explicit_absolute_agent_binary(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = _write_config(tmp_path, mode="formal")
    payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    agent = _write_executable(tmp_path / "renamed-agent")
    payload["backend"]["binary"] = str(agent)
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda *args, **kwargs: None,
    )

    plan = build_pipeline_plan(load_pipeline_config(path), config_path=path)

    assert plan["backend"]["available"] is True
    assert plan["backend"]["resolved_path"] == str(agent)


@pytest.mark.asyncio
async def test_formal_token_usage_must_be_complete_and_within_budget(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = _write_config(tmp_path, mode="formal")
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda *args, **kwargs: None,
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__)

    result = await run_pipeline(config, runners=_fixture_runners(calls), config_path=path)

    assert not result.result_valid
    assert calls == [("part_i", "gcc-target", "full", 1)]
    assert "provider-reported token usage" in (result.lanes[0].stages["part_i"].error or "")


@pytest.mark.asyncio
async def test_formal_token_usage_rejects_budget_overshoot(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = _write_config(tmp_path, mode="formal")
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda *args, **kwargs: None,
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__)

    result = await run_pipeline(
        config,
        runners=_fixture_runners(calls, part_i_tokens=101),
        config_path=path,
    )

    assert not result.result_valid
    assert "formal token budget exceeded: consumed 101 of 100" in (
        result.lanes[0].stages["part_i"].error or ""
    )


@pytest.mark.asyncio
async def test_resume_revalidates_bundle_referenced_artifacts(tmp_path: Path) -> None:
    path = _write_config(tmp_path)
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []
    runners = _fixture_runners(calls)
    first = await run_pipeline(config, runners=runners, config_path=path)
    catalog = first.lanes[0].lane_dir / "part_ii/artifacts/checker-catalog.json"
    catalog.write_text("tampered\n", encoding="utf-8")

    with pytest.raises(ValueError, match="artifact SHA-256 mismatch"):
        await run_pipeline(config, runners=runners, resume=True, config_path=path)


@pytest.mark.asyncio
async def test_part_iii_cannot_mutate_bundle_referenced_artifacts(tmp_path: Path) -> None:
    path = _write_config(tmp_path)
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []

    result = await run_pipeline(
        config,
        runners=_fixture_runners(calls, tamper_bundle_in_part_iii=True),
        config_path=path,
    )

    assert not result.result_valid
    assert calls == [
        ("part_i", "gcc-target", "full", 1),
        ("part_ii", "gcc-target", "full", 1),
        ("part_iii", "gcc-target", "full", 1),
    ]
    assert "artifact SHA-256 mismatch" in (result.lanes[0].stages["part_iii"].error or "")


def test_fixture_cli_runs_no_model_smoke_pipeline(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    path = _write_config(
        tmp_path,
        variants=["full", "without-rag", "without-oracle", "bare-agent"],
        generation_path="combined",
    )
    monkeypatch.setattr("defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: None)

    assert experiments_cli.main(["pipeline", "--config", str(path)]) == 0

    output = capsys.readouterr().out
    assert "completed:" in output
    manifest = json.loads(
        (tmp_path / "runs/fixture-pipeline/manifest.json").read_text(encoding="utf-8")
    )
    assert manifest["execution_status"] == "completed"
    assert manifest["result_valid"] is True
    assert manifest["outcome"] == "negative"
    full_summary = json.loads(
        (
            tmp_path
            / "runs/fixture-pipeline/lanes/gcc-target/full/rep-001/part_iii/artifacts"
            / "agent-audit-summary.json"
        ).read_text(encoding="utf-8")
    )
    without_rag_summary = json.loads(
        (
            tmp_path
            / "runs/fixture-pipeline/lanes/gcc-target/without-rag/rep-001/part_iii/artifacts"
            / "agent-audit-summary.json"
        ).read_text(encoding="utf-8")
    )
    assert full_summary["fixture_smoke"] is True
    assert full_summary["campaign_variant"] == "full"
    assert full_summary["source_roots"] == [str((tmp_path / "audit-source").resolve())]
    assert full_summary["outcome"] == "no-verified-findings"
    assert without_rag_summary["campaign_variant"] == "without-rag"
    assert without_rag_summary["variant"] == "full"
    for variant in ("full", "without-rag", "without-oracle", "bare-agent"):
        assert (
            tmp_path / f"runs/fixture-pipeline/lanes/gcc-target/{variant}/rep-001/manifest.json"
        ).is_file()


@pytest.mark.asyncio
async def test_fixture_pipeline_preserves_all_part_iii_source_roots(
    tmp_path: Path,
) -> None:
    path = _write_config(tmp_path)
    second_root = tmp_path / "audit-source-two"
    second_root.mkdir()
    (second_root / "source.cc").write_text("void second() {}\n", encoding="utf-8")
    payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    target = payload["targets"][0]
    target.pop("target_tree")
    target["audit_source_roots"] = ["audit-source", second_root.name]
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")
    config = load_pipeline_config(path)

    result = await run_pipeline(config, config_path=path)

    assert result.result_valid is True
    summary = json.loads(
        (result.lanes[0].lane_dir / "part_iii/artifacts/agent-audit-summary.json").read_text(
            encoding="utf-8"
        )
    )
    assert summary["source_roots"] == [
        str((tmp_path / "audit-source").resolve()),
        str(second_root.resolve()),
    ]


@pytest.mark.asyncio
async def test_ablation_campaign_runs_four_arms_with_scientific_reuse(
    tmp_path: Path,
) -> None:
    path = _write_config(
        tmp_path,
        variants=["full", "without-rag", "without-oracle", "bare-agent"],
        generation_path="combined",
    )
    config = load_pipeline_config(path)
    calls: list[tuple[str, str, str, int]] = []

    result = await run_pipeline(config, runners=_fixture_runners(calls), config_path=path)

    assert result.result_valid is True
    assert [lane.variant for lane in result.lanes] == [
        "full",
        "without-rag",
        "without-oracle",
        "bare-agent",
    ]
    assert calls == [
        ("part_i", "gcc-target", "full", 1),
        ("part_ii", "gcc-target", "full", 1),
        ("part_iii", "gcc-target", "full", 1),
        ("part_i", "gcc-target", "without-rag", 1),
        ("part_ii", "gcc-target", "without-rag", 1),
        ("part_iii", "gcc-target", "without-rag", 1),
        ("part_iii", "gcc-target", "without-oracle", 1),
        ("part_iii", "gcc-target", "bare-agent", 1),
    ]

    by_variant = {lane.variant: lane for lane in result.lanes}
    assert by_variant["full"].stages["part_i"].metadata["generation_path"] == "combined"
    assert by_variant["without-rag"].stages["part_i"].metadata["generation_path"] == "segmented-cot"
    assert by_variant["without-oracle"].stages["part_i"].outcome == "reused-frozen-upstream"
    assert by_variant["bare-agent"].stages["part_i"].outcome == "reused-frozen-upstream"
    assert by_variant["without-oracle"].stages["part_ii"].metadata["reused_from_variant"] == "full"
    assert by_variant["bare-agent"].stages["part_ii"].metadata["reused_from_variant"] == "full"

    without_rag_summary = json.loads(
        (
            by_variant["without-rag"].lane_dir / "part_iii/artifacts/agent-audit-summary.json"
        ).read_text(encoding="utf-8")
    )
    assert without_rag_summary["campaign_variant"] == "without-rag"
    assert without_rag_summary["variant"] == "full"

    without_oracle_summary = json.loads(
        (
            by_variant["without-oracle"].lane_dir / "part_iii/artifacts/agent-audit-summary.json"
        ).read_text(encoding="utf-8")
    )
    assert without_oracle_summary["campaign_variant"] == "without-oracle"
    assert without_oracle_summary["variant"] == "without-oracle"

    bare_summary = json.loads(
        (
            by_variant["bare-agent"].lane_dir / "part_iii/artifacts/agent-audit-summary.json"
        ).read_text(encoding="utf-8")
    )
    assert bare_summary["campaign_variant"] == "bare-agent"
    assert bare_summary["variant"] == "bare-agent"


def test_repository_example_is_an_executable_fixture_config() -> None:
    config_path = Path(__file__).resolve().parents[2] / "configs/experiments/example.yaml"
    config = load_pipeline_config(config_path)

    assert config.mode == "fixture"
    assert config.variants == ["full", "without-rag", "without-oracle", "bare-agent"]
    assert config.audit.require_verified_candidates is True
    assert config.audit.parity_profile == "demo-workset"
    assert config.audit.parity_threshold_metric == "recall"
    plan = build_pipeline_plan(config, config_path=config_path)
    assert plan["runner"] == "fixture-smoke"
    assert plan["backend"]["required"] is False
    assert [lane["variant"] for lane in plan["lanes"]] == [
        "full",
        "without-rag",
        "without-oracle",
        "bare-agent",
    ]
    assert plan["lanes"][0]["target_id"] == "config-smoke"


def test_llvm_full_and_without_rag_have_distinct_effective_plans(tmp_path: Path) -> None:
    corpus, audit, _checker, _reference = _fixture_tree(tmp_path)
    target = {
        "id": "llvm",
        "compiler": "llvm",
        "version": "fixture",
        "corpus_root": corpus.name,
        "source_root": audit.name,
        "mechanisms": ["bti"],
        "isas": ["aarch64"],
    }
    path = _write_config(
        tmp_path / "llvm-plan",
        targets=[target],
        variants=["full", "without-rag"],
        generation_path="combined",
    )

    plan = build_pipeline_plan(load_pipeline_config(path), config_path=path)

    assert [(lane["variant"], lane["generation_path"]) for lane in plan["lanes"]] == [
        ("full", "combined"),
        ("without-rag", "segmented-cot"),
    ]


@pytest.mark.asyncio
async def test_formal_pipeline_fails_closed_without_host_read_isolation(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = _write_config(tmp_path, mode="formal")
    config = load_pipeline_config(path)
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline._assert_clean_repositories",
        lambda paths, *, output_root: None,
    )
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.pipeline.shutil.which", lambda _: __file__
    )
    calls: list[tuple[str, str, str, int]] = []
    runners = _fixture_runners(calls)
    runners = PipelineRunners(
        part_i=runners.part_i,
        part_ii=runners.part_ii,
        part_iii=runners.part_iii,
        backend_factory=lambda _cfg: cast(Any, _NoIsolationBackend()),
    )

    result = await run_pipeline(config, runners=runners, config_path=path)

    assert result.result_valid is False
    assert result.outcome == "blocked"
    assert calls == []
    lane = result.lanes[0]
    assert lane.stages["part_i"].execution_status == "failed"
    assert "required stage artifact is missing" in (lane.stages["part_i"].error or "")
    part_i_result = json.loads(
        (lane.lane_dir / "part_i" / "result.json").read_text(encoding="utf-8")
    )
    assert "requires host read isolation" in (part_i_result.get("error") or "")
    assert lane.stages["part_ii"].execution_status == "skipped"
    assert lane.stages["part_iii"].execution_status == "skipped"


def test_formal_part_iii_plan_keeps_host_read_isolation_enabled(tmp_path: Path) -> None:
    path = _write_config(tmp_path, mode="formal")
    config = load_pipeline_config(path)
    target = config.targets[0]
    manifest = tmp_path / "checker-bundle-manifest.json"
    manifest.write_text("{}\n", encoding="utf-8")
    accepted = tmp_path / "accepted-invariants.jsonl"
    accepted.write_text('{"invariant_id": "INV-1"}\n', encoding="utf-8")

    plan = pipeline_mod._stage_plan(
        config,
        target,
        "full",
        1,
        "part_iii",
        tmp_path / "lane",
        accepted_invariants=accepted,
        checker_bundle_manifest=manifest,
    )

    assert plan.parameters["require_host_read_isolation"] is True
    assert plan.parameters["findings_deny_path"] == str(
        config.generation.reference_root / "findings"
    )


@pytest.mark.asyncio
async def test_formal_part_iii_fails_closed_before_runner_without_host_isolation(
    tmp_path: Path,
) -> None:
    path = _write_config(tmp_path, mode="formal")
    config = load_pipeline_config(path)
    target = config.targets[0]
    manifest = tmp_path / "checker-bundle-manifest.json"
    manifest.write_text("{}\n", encoding="utf-8")
    accepted = tmp_path / "accepted-invariants.jsonl"
    accepted.write_text('{"invariant_id": "INV-1"}\n', encoding="utf-8")
    plan = pipeline_mod._stage_plan(
        config,
        target,
        "full",
        1,
        "part_iii",
        tmp_path / "lane",
        accepted_invariants=accepted,
        checker_bundle_manifest=manifest,
    )
    invoked = False

    async def must_not_run(
        plan: Any, repetition: int, output_dir: Path, backend: Any
    ) -> StageResult:
        nonlocal invoked
        invoked = True
        raise AssertionError("Part III runner must not execute without host isolation")

    runners = PipelineRunners(
        part_i=must_not_run,
        part_ii=must_not_run,
        part_iii=must_not_run,
        backend_factory=lambda _cfg: cast(Any, _NoIsolationBackend()),
    )
    result, usage = await pipeline_mod._invoke_stage(
        config=config,
        plan=plan,
        repetition=1,
        stage="part_iii",
        output_dir=tmp_path / "part-iii/artifacts",
        runner=must_not_run,
        runners=runners,
    )

    assert invoked is False
    assert result.status == "failed"
    assert result.outcome == "host-isolation-unavailable"
    assert result.result_valid is False
    assert result.continuation_ready is False
    assert "agent-audit requires host read isolation" in (result.error or "")
    assert usage["records"] == 0
    assert usage["token_comparable"] is False


def test_pipeline_cli_show_plan_is_side_effect_free(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    path = _write_config(tmp_path)
    config = load_pipeline_config(path)

    assert experiments_cli.main(["pipeline", "--config", str(path), "--show-plan"]) == 0

    plan = json.loads(capsys.readouterr().out)
    assert plan["status"] == "ready"
    assert plan["config_sha256"] == config.content_hash()
    assert len(plan["plan_sha256"]) == 64
    assert plan["lanes"][0]["lane_id"] == "gcc-target-full-r001"
    assert not (tmp_path / "runs").exists()


def test_pipeline_help_and_existing_command_enumeration_remain_compatible(
    capsys: pytest.CaptureFixture[str],
) -> None:
    with pytest.raises(SystemExit) as exc:
        experiments_cli.main(["pipeline", "--help"])
    assert exc.value.code == 0
    help_text = capsys.readouterr().out
    assert "--config YAML" in help_text
    assert "--show-plan" in help_text
    assert "--resume" in help_text

    with pytest.raises(SystemExit) as root_exc:
        experiments_cli.main(["--help"])
    assert root_exc.value.code == 0
    root_help = capsys.readouterr().out
    assert "pipeline" in root_help
    assert "run Parts I--III from one typed YAML configuration" in root_help
