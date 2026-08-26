"""Contract and dispatch tests for the unified experiment CLI."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
from typing import Any

import pytest

from defuzz_loop import experiments_cli as cli
from defuzz_loop.experiment_engine import AgentRequest, AgentResult, StageResult
from defuzz_loop.token_usage import current_token_usage_sink, read_jsonl

HELP_PATHS = [
    [],
    ["invariant-generation"],
    ["checker-authoring"],
    ["agent-audit"],
    ["ablation"],
    ["ablation", "without-rag"],
    ["ablation", "without-oracle"],
    ["ablation", "bare-agent"],
]
LEAF_PATHS = [path for path in HELP_PATHS if path and path != ["ablation"]]
REPO_ROOT = Path(__file__).resolve().parents[2]


@pytest.mark.parametrize("path", HELP_PATHS)
def test_every_command_has_clear_help(
    path: list[str], capsys: pytest.CaptureFixture[str]
) -> None:
    with pytest.raises(SystemExit) as exc:
        cli.main([*path, "--help"])

    assert exc.value.code == 0
    help_text = capsys.readouterr().out
    assert "usage:" in help_text
    if not path:
        assert "DeFuzz unified experiment launcher" in help_text
        assert "examples:" in help_text
    elif path[-1] != "ablation":
        assert "--show-plan" in help_text
        assert "--backend {traex,codex}" in help_text
        assert "--agent-binary" in help_text
        assert "--model" in help_text
        assert "example:" in help_text


@pytest.mark.parametrize("path", LEAF_PATHS)
def test_leaf_help_exposes_only_relevant_paths(
    path: list[str], capsys: pytest.CaptureFixture[str]
) -> None:
    with pytest.raises(SystemExit):
        cli.main([*path, "--help"])
    help_text = capsys.readouterr().out
    leaf = path[-1]
    if leaf in {"invariant-generation", "without-rag"}:
        assert "--reference-root" in help_text
        assert "--corpus-root" in help_text
        assert "--target-tree" not in help_text
    elif leaf == "checker-authoring":
        assert "--from-run" in help_text
        assert "--inputs" in help_text
        assert "--source-root" in help_text
    else:
        if leaf != "bare-agent":
            assert "--from-run" in help_text
            assert "--inputs" in help_text
        assert "--target-tree" in help_text
        assert "--demo-parity" in help_text
        assert "--parity-threshold" in help_text


def _subcommand_names(parser: argparse.ArgumentParser) -> set[str]:
    subparsers = next(
        action
        for action in parser._actions
        if isinstance(action, argparse._SubParsersAction)
    )
    return set(subparsers.choices)


def test_command_enumeration_is_exact() -> None:
    parser = cli.build_parser()
    assert _subcommand_names(parser) == {
        "invariant-generation",
        "checker-authoring",
        "agent-audit",
        "ablation",
    }
    top_level = next(
        action
        for action in parser._actions
        if isinstance(action, argparse._SubParsersAction)
    )
    assert _subcommand_names(top_level.choices["ablation"]) == {
        "without-rag",
        "without-oracle",
        "bare-agent",
    }


def test_show_plan_is_side_effect_free_and_reports_backend_availability(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    output_root = tmp_path / "does-not-exist"
    monkeypatch.setattr(cli.shutil, "which", lambda binary: f"/bin/{binary}")

    result = cli.main(
        [
            "invariant-generation",
            "--run-id",
            "inv-r1",
            "--output-root",
            str(output_root),
            "--backend",
            "codex",
            "--model",
            "test-model",
            "--token-budget",
            "12000",
            "--time-budget-minutes",
            "7.5",
            "--repetitions",
            "2",
            "--generation-path",
            "rag",
            "--show-plan",
        ]
    )

    assert result == 0
    plan = json.loads(capsys.readouterr().out)
    assert plan["status"] == "ready"
    assert plan["backend_available"] is True
    assert plan["parameters"]["agent_binary"] == "codex"
    assert plan["parameters"]["model"] == "test-model"
    assert plan["run"] == {
        "output_root": str(output_root.resolve()),
        "repetitions": 2,
        "run_id": "inv-r1",
        "time_budget_minutes": 7.5,
        "token_budget": 12000,
    }
    assert plan["parameters"]["generation_path"] == "rag"
    assert plan["launches"][0]["output_dir"].endswith(
        "inv-r1/rep-001/artifacts"
    )
    assert not output_root.exists()


def test_fast_plan_marks_recursive_snapshot_as_unfrozen(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    corpus = tmp_path / "corpus"
    corpus.mkdir()
    (corpus / "input.c").write_text("int x;\n", encoding="utf-8")
    monkeypatch.setenv("DEFUZZ_FAST_PLAN", "1")

    assert (
        cli.main(
            ["invariant-generation", "--corpus-root", str(corpus), "--show-plan"]
        )
        == 0
    )
    plan = json.loads(capsys.readouterr().out)
    snapshot = plan["parameters"]["input_snapshot"]["roots"]["corpus_root"]
    assert snapshot["kind"] == "directory-stat"
    assert "does not freeze" in snapshot["warning"]


def test_checker_root_stays_relative_to_real_source_tree(
    capsys: pytest.CaptureFixture[str],
) -> None:
    checker_root = Path("core/internal/oracle")
    assert (REPO_ROOT / checker_root).is_dir()

    assert (
        cli.main(
            [
                "checker-authoring",
                "--source-root",
                str(REPO_ROOT),
                "--checker-root",
                checker_root.as_posix(),
                "--show-plan",
            ]
        )
        == 0
    )

    plan = json.loads(capsys.readouterr().out)
    assert plan["source_root"] == str(REPO_ROOT)
    assert plan["parameters"]["checker_root"] == checker_root.as_posix()


@pytest.mark.parametrize(
    ("cwd_has_project", "suffix"),
    [(False, Path("runs/experiments")), (True, Path("orchestrator/runs/experiments"))],
)
def test_default_output_root_follows_launch_directory(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
    cwd_has_project: bool,
    suffix: Path,
) -> None:
    if cwd_has_project:
        project = tmp_path / "orchestrator"
        project.mkdir()
        (project / "pyproject.toml").touch()
    monkeypatch.chdir(tmp_path)
    assert cli.main(["agent-audit", "--show-plan"]) == 0
    plan = json.loads(capsys.readouterr().out)
    assert plan["run"]["output_root"] == str(tmp_path / suffix)


class _FakeBackend:
    def __init__(self, calls: list[dict[str, Any]], *, binary: str, model: str | None) -> None:
        self.calls = calls
        self.binary = binary
        self.model = model
        self.provider = "fake"

    async def run(self, request: AgentRequest) -> AgentResult:
        assert request.token_sink is current_token_usage_sink()
        sink = request.token_sink
        assert sink is not None
        sink.record_external_usage(
            {
                "type": "turn.completed",
                "usage": {"input_tokens": 7, "output_tokens": 3},
            }
        )
        self.calls.append(
            {
                "binary": self.binary,
                "model": self.model,
                "sink": request.token_sink,
                "metadata": request.metadata,
            }
        )
        return AgentResult(success=True, final={"ok": True})

    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        del schema
        return await self.run(
            AgentRequest(
                prompt=prompt,
                cwd=Path(kwargs.get("cwd", Path.cwd())),
                output_dir=Path(kwargs.get("output_dir", ".agent-output")),
                token_sink=kwargs.get("token_sink"),
                metadata=dict(kwargs.get("metadata", {})),
            )
        )


def _install_fakes(
    monkeypatch: pytest.MonkeyPatch, calls: list[dict[str, Any]]
) -> None:
    monkeypatch.setattr(cli.shutil, "which", lambda binary: f"/bin/{binary}")

    def backend_factory(
        binary: str = "traex", model: str | None = None, **_: Any
    ) -> _FakeBackend:
        return _FakeBackend(calls, binary=binary, model=model)

    async def invariant_runner(
        plan: Any, repetition: int, output_dir: Path, backend: Any
    ) -> StageResult:
        assert current_token_usage_sink() is not None
        if plan.variant == "without-rag":
            assert plan.parameters["generation_path"] == "segmented-cot"
        await backend.run(
            AgentRequest(
                prompt="fake",
                cwd=Path.cwd(),
                output_dir=output_dir / "agent",
                metadata={"stage": "part-i"},
            )
        )
        (output_dir / "accepted-invariants.jsonl").write_text("{}\n")
        return StageResult(stage="invariant-generation", status="completed")

    async def checker_runner(
        plan: Any, repetition: int, output_dir: Path, backend: Any
    ) -> StageResult:
        assert plan.parameters["accepted_invariants"].endswith(
            "accepted-invariants.jsonl"
        )
        await backend.run(
            AgentRequest(
                prompt="fake",
                cwd=Path.cwd(),
                output_dir=output_dir / "agent",
                metadata={"stage": "part-ii"},
            )
        )
        (output_dir / "results.jsonl").write_text("{}\n")
        return StageResult(stage="checker-authoring", status="completed")

    async def audit_runner(
        plan: Any, repetition: int, output_dir: Path, backend: Any
    ) -> StageResult:
        if plan.parameters.get("from_run"):
            assert plan.parameters["checker_artifacts"][0].endswith(
                "rep-001/artifacts/results.jsonl"
            )
        await backend.run(
            AgentRequest(
                prompt="fake",
                cwd=Path.cwd(),
                output_dir=output_dir / "agent",
                metadata={"stage": "part-iii"},
            )
        )
        (output_dir / "agent-audit-summary.json").write_text("{}\n")
        return StageResult(stage="agent-audit", status="completed")

    monkeypatch.setattr(cli, "ExecAgentBackend", backend_factory)
    monkeypatch.setattr(cli.invariant_generation, "run", invariant_runner)
    monkeypatch.setattr(cli.checker_authoring, "run", checker_runner)
    monkeypatch.setattr(cli.agent_audit, "run", audit_runner)


def _write_upstream_run(
    root: Path, *, stage: str, filename: str, content: bytes = b"{}\n"
) -> Path:
    artifact = root / "rep-001/artifacts" / filename
    artifact.parent.mkdir(parents=True)
    artifact.write_bytes(content)
    digest = hashlib.sha256(content).hexdigest()
    result_name = f"{stage}-result.json"
    (root / "rep-001" / result_name).write_text(
        json.dumps(
            {
                "stage": stage,
                "status": "completed",
                "artifacts": [
                    {
                        "path": filename,
                        "sha256": digest,
                        "size_bytes": len(content),
                    }
                ],
            }
        )
    )
    (root / "rep-001/manifest.json").write_text(
        json.dumps(
            {
                "status": "completed",
                "stage": stage,
                "stage_result": result_name,
            }
        )
    )
    (root / "manifest.json").write_text(json.dumps({"status": "completed"}))
    return artifact


@pytest.mark.parametrize(
    ("command", "run_id", "stage"),
    [
        (["invariant-generation"], "invariant-generation", "invariant-generation"),
        (
            ["checker-authoring", "--from-run", "previous"],
            "checker-authoring",
            "checker-authoring",
        ),
        (["agent-audit", "--from-run", "previous"], "agent-audit", "agent-audit"),
        (
            ["ablation", "without-rag"],
            "ablation-without-rag",
            "invariant-generation",
        ),
        (
            ["ablation", "without-oracle", "--from-run", "previous"],
            "ablation-without-oracle",
            "agent-audit",
        ),
        (
            ["ablation", "bare-agent"],
            "ablation-bare-agent",
            "agent-audit",
        ),
    ],
)
def test_every_leaf_dispatches_and_writes_complete_artifact_layout(
    command: list[str],
    run_id: str,
    stage: str,
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    calls: list[dict[str, Any]] = []
    _install_fakes(monkeypatch, calls)
    monkeypatch.chdir(tmp_path)
    output_root = tmp_path / "runs"
    previous = tmp_path / "previous"
    execution = list(command)
    if "--from-run" in execution:
        upstream_stage, filename = (
            ("invariant-generation", "accepted-invariants.jsonl")
            if stage == "checker-authoring"
            else ("checker-authoring", "results.jsonl")
        )
        _write_upstream_run(previous, stage=upstream_stage, filename=filename)
    reference_root = tmp_path / "reference"
    for relative in cli._REQUIRED_REFERENCE_PATHS:
        path = reference_root / relative
        if path.suffix:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("fixture\n")
        else:
            path.mkdir(parents=True, exist_ok=True)
    if stage == "invariant-generation":
        execution.extend(["--corpus-root", str(tmp_path)])
    if stage == "agent-audit":
        execution.extend(
            [
                "--target-tree",
                str(tmp_path),
                "--reference-root",
                str(reference_root),
            ]
        )
        if execution[:1] == ["agent-audit"]:
            execution.extend(
                [
                    "--online-oracle-command",
                    "checker --fingerprint {candidate_fingerprint}",
                ]
            )
    if execution[:1] == ["ablation"]:
        baseline = tmp_path / f"baseline-{run_id}"
        baseline.mkdir()
        baseline_experiment = (
            "invariant-generation"
            if stage == "invariant-generation"
            else "agent-audit"
        )
        (baseline / "manifest.json").write_text(
            json.dumps({"status": "completed"})
        )
        baseline_parameters: dict[str, Any] = {
            "model": "fake-model",
            "agent_binary": "fake-agent",
        }
        baseline_source = None
        if stage == "invariant-generation":
            baseline_parameters.update(
                {
                    "corpus_root": str(tmp_path),
                    "reference_root": str(reference_root),
                    "compiler": "gcc",
                }
            )
        else:
            baseline_source = str(tmp_path)
            baseline_parameters.update(
                {
                    "reference_root": str(reference_root),
                    "compiler": "gcc",
                    "mechanism": [],
                    "isa": [],
                    "max_concurrency": 1,
                    "toolchain_versions": None,
                    "verification_command": [],
                }
            )
        (baseline / "plan.json").write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "run_id": "baseline",
                    "experiment": baseline_experiment,
                    "variant": "full",
                    "repetitions": 1,
                    "budget": {
                        "token_budget": 100_000,
                        "time_budget_minutes": 60.0,
                    },
                    "parameters": baseline_parameters,
                    "source_root": baseline_source,
                }
            )
        )
        execution.extend(["--baseline-run", str(baseline)])

    result = cli.main(
        [
            *execution,
            "--output-root",
            str(output_root),
            "--agent-binary",
            "fake-agent",
            "--model",
            "fake-model",
        ]
    )

    assert result == 0
    assert "completed:" in capsys.readouterr().out
    run_root = output_root / run_id
    rep = run_root / "rep-001"
    assert (run_root / "plan.json").is_file()
    assert (rep / "artifacts").is_dir()
    assert (rep / f"{stage}-result.json").is_file()
    assert (rep / "token_usage.jsonl").is_file()
    assert (rep / "token_usage_summary.json").is_file()
    assert (rep / "token_usage_summary.csv").is_file()
    assert read_jsonl(rep / "token_usage.jsonl")[0].total_tokens == 10
    root_manifest = json.loads((run_root / "manifest.json").read_text())
    rep_manifest = json.loads((rep / "manifest.json").read_text())
    assert root_manifest["status"] == "completed"
    assert root_manifest["successful_repetitions"] == [1]
    assert rep_manifest["status"] == "completed"
    assert rep_manifest["stage_result"] == f"{stage}-result.json"
    assert calls[0]["binary"] == "fake-agent"
    assert calls[0]["model"] == "fake-model"
    if "--from-run" in command:
        stored_plan = json.loads((run_root / "plan.json").read_text())
        artifact_snapshot = stored_plan["parameters"]["input_snapshot"][
            "from_run"
        ]["artifacts"][0]
        assert artifact_snapshot["sha256"] == hashlib.sha256(b"{}\n").hexdigest()


def test_repetitions_get_independent_sinks_and_any_failure_returns_one(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    calls: list[dict[str, Any]] = []
    _install_fakes(monkeypatch, calls)
    monkeypatch.chdir(tmp_path)

    async def sometimes_fails(
        plan: Any, repetition: int, output_dir: Path, backend: Any
    ) -> StageResult:
        await backend.run(
            AgentRequest(prompt="fake", cwd=Path.cwd(), output_dir=output_dir / "agent")
        )
        return StageResult(
            stage="invariant-generation",
            status="failed" if repetition == 2 else "completed",
            error="boom" if repetition == 2 else None,
        )

    monkeypatch.setattr(cli.invariant_generation, "run", sometimes_fails)
    assert (
        cli.main(
            [
                "invariant-generation",
                "--output-root",
                str(tmp_path),
                "--repetitions",
                "3",
                "--corpus-root",
                str(tmp_path),
            ]
        )
        == cli.EXIT_RUNTIME_FAILURE
    )
    assert len(calls) == 3
    assert len({id(call["sink"]) for call in calls}) == 3
    manifest = json.loads((tmp_path / "invariant-generation/manifest.json").read_text())
    assert manifest["status"] == "failed"
    assert manifest["successful_repetitions"] == [1, 3]
    assert manifest["failed_repetitions"] == [2]


def test_successful_stage_with_missing_usage_is_not_budget_comparable(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    calls: list[dict[str, Any]] = []
    _install_fakes(monkeypatch, calls)

    async def missing_usage(
        plan: Any, repetition: int, output_dir: Path, backend: Any
    ) -> StageResult:
        del plan, repetition, output_dir, backend
        sink = current_token_usage_sink()
        assert sink is not None
        sink.record_external_usage({"type": "turn.completed", "usage": {}})
        return StageResult(stage="invariant-generation", status="completed")

    monkeypatch.setattr(cli.invariant_generation, "run", missing_usage)
    result = cli.main(
        [
            "invariant-generation",
            "--corpus-root",
            str(tmp_path),
            "--output-root",
            str(tmp_path / "runs"),
            "--agent-binary",
            "fake-agent",
        ]
    )

    assert result == cli.EXIT_RUNTIME_FAILURE
    manifest = json.loads(
        (tmp_path / "runs/invariant-generation/rep-001/manifest.json").read_text()
    )
    assert manifest["token_comparable"] is False
    assert manifest["usage_missing_count"] == 1


def test_existing_run_requires_resume_and_completed_rep_is_not_replayed(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    calls: list[dict[str, Any]] = []
    _install_fakes(monkeypatch, calls)
    corpus = tmp_path / "corpus"
    corpus.mkdir()
    (corpus / "input.c").write_text("int main(void) { return 0; }\n")
    output_root = tmp_path / "runs"
    command = [
        "invariant-generation",
        "--run-id",
        "stable",
        "--corpus-root",
        str(corpus),
        "--output-root",
        str(output_root),
        "--agent-binary",
        "fake-agent",
    ]

    assert cli.main(command) == 0
    token_path = output_root / "stable/rep-001/token_usage.jsonl"
    original_tokens = token_path.read_bytes()
    assert len(calls) == 1

    assert cli.main(command) == cli.EXIT_CONFIGURATION_ERROR
    assert "pass --resume" in capsys.readouterr().err
    assert len(calls) == 1
    assert token_path.read_bytes() == original_tokens

    assert cli.main([*command, "--resume"]) == 0
    assert len(calls) == 1
    assert token_path.read_bytes() == original_tokens
    assert len(read_jsonl(token_path)) == 1

    stored_plan = json.loads((output_root / "stable/plan.json").read_text())
    corpus_snapshot = stored_plan["parameters"]["input_snapshot"]["roots"][
        "corpus_root"
    ]
    assert corpus_snapshot["kind"] == "tree-metadata"
    assert corpus_snapshot["manifest_sha256"]
    (corpus / "input.c").write_text("int main(void) { return 1; }\n")
    assert cli.main([*command, "--resume"]) == cli.EXIT_CONFIGURATION_ERROR
    assert "plan mismatch" in capsys.readouterr().err
    assert len(calls) == 1


def test_resume_rejects_same_input_path_after_content_changes(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    calls: list[dict[str, Any]] = []
    _install_fakes(monkeypatch, calls)
    source = tmp_path / "source"
    source.mkdir()
    inputs = tmp_path / "accepted-invariants.jsonl"
    inputs.write_text('{"invariant_id": "one"}\n')
    output_root = tmp_path / "runs"
    command = [
        "checker-authoring",
        "--inputs",
        str(inputs),
        "--source-root",
        str(source),
        "--output-root",
        str(output_root),
        "--agent-binary",
        "fake-agent",
    ]

    assert cli.main(command) == 0
    stored_plan = json.loads(
        (output_root / "checker-authoring/plan.json").read_text()
    )
    snapshot = stored_plan["parameters"]["input_snapshot"]["inputs"][0]
    assert snapshot["sha256"] == hashlib.sha256(inputs.read_bytes()).hexdigest()

    inputs.write_text('{"invariant_id": "changed"}\n')
    assert cli.main([*command, "--resume"]) == cli.EXIT_CONFIGURATION_ERROR
    assert "plan mismatch" in capsys.readouterr().err
    assert len(calls) == 1


def test_from_run_rejects_tampered_artifact_before_creating_output(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    calls: list[dict[str, Any]] = []
    _install_fakes(monkeypatch, calls)
    upstream = tmp_path / "upstream"
    artifact = _write_upstream_run(
        upstream,
        stage="invariant-generation",
        filename="accepted-invariants.jsonl",
    )
    artifact.write_text("tampered\n")
    output_root = tmp_path / "runs"

    assert (
        cli.main(
            [
                "checker-authoring",
                "--from-run",
                str(upstream),
                "--source-root",
                str(tmp_path),
                "--output-root",
                str(output_root),
                "--agent-binary",
                "fake-agent",
            ]
        )
        == cli.EXIT_CONFIGURATION_ERROR
    )
    assert "artifact hash mismatch" in capsys.readouterr().err
    assert not output_root.exists()
    assert calls == []


@pytest.mark.parametrize(
    "option",
    ["--inputs", "--from-run"],
)
def test_bare_agent_rejects_pipeline_inputs(option: str) -> None:
    with pytest.raises(SystemExit) as exc:
        cli.main(["ablation", "bare-agent", option, "anything", "--show-plan"])
    assert exc.value.code == 2


def test_ablation_baseline_freezes_model_budgets_source_and_repetitions(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    calls: list[dict[str, Any]] = []
    _install_fakes(monkeypatch, calls)
    target = tmp_path / "target"
    target.mkdir()
    reference_root = tmp_path / "reference"
    for relative in cli._REQUIRED_REFERENCE_PATHS:
        path = reference_root / relative
        if path.suffix:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("fixture\n")
        else:
            path.mkdir(parents=True, exist_ok=True)
    baseline = tmp_path / "baseline"
    baseline.mkdir()
    (baseline / "manifest.json").write_text(
        json.dumps({"status": "completed"})
    )
    (baseline / "plan.json").write_text(
        json.dumps(
            {
                "schema_version": 1,
                "run_id": "full-audit",
                "experiment": "agent-audit",
                "variant": "full",
                "repetitions": 1,
                "budget": {
                    "token_budget": 100_000,
                    "time_budget_minutes": 60.0,
                },
                "parameters": {
                    "model": "frozen-model",
                    "agent_binary": "fake-agent",
                    "compiler": "gcc",
                    "mechanism": [],
                    "isa": [],
                    "max_concurrency": 1,
                    "reference_root": str(reference_root),
                    "online_oracle_command": [
                        [
                            "checker",
                            "--fingerprint",
                            "{candidate_fingerprint}",
                        ]
                    ],
                    "oracle_rounds": 1,
                    "verification_command": [],
                },
                "source_root": str(target),
            }
        )
    )
    common = [
        "ablation",
        "without-oracle",
        "--baseline-run",
        str(baseline),
        "--target-tree",
        str(target),
        "--reference-root",
        str(reference_root),
        "--agent-binary",
        "fake-agent",
    ]

    assert (
        cli.main(
            [
                *common,
                "--model",
                "frozen-model",
                "--output-root",
                str(tmp_path / "runs"),
            ]
        )
        == 0
    )
    assert len(calls) == 1

    assert (
        cli.main(
            [
                *common,
                "--model",
                "different-model",
                "--run-id",
                "mismatch",
                "--output-root",
                str(tmp_path / "runs"),
            ]
        )
        == cli.EXIT_CONFIGURATION_ERROR
    )
    assert "does not match its full-arm baseline" in capsys.readouterr().err
    assert len(calls) == 1
    assert not (tmp_path / "runs/mismatch").exists()


def test_wall_clock_timeout_is_recorded_as_runtime_failure(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    calls: list[dict[str, Any]] = []
    _install_fakes(monkeypatch, calls)
    monkeypatch.chdir(tmp_path)

    async def never_finishes(
        plan: Any, repetition: int, output_dir: Path, backend: Any
    ) -> StageResult:
        await cli.asyncio.sleep(1)
        return StageResult(stage="invariant-generation")

    monkeypatch.setattr(cli.invariant_generation, "run", never_finishes)
    result = cli.main(
        [
            "invariant-generation",
            "--output-root",
            str(tmp_path),
            "--time-budget-minutes",
            "0.001",
            "--corpus-root",
            str(tmp_path),
        ]
    )
    assert result == cli.EXIT_RUNTIME_FAILURE
    stage_result = json.loads(
        (tmp_path / "invariant-generation/rep-001/invariant-generation-result.json").read_text()
    )
    assert stage_result["status"] == "failed"
    assert "wall-clock budget exceeded" in stage_result["error"]


def test_semantic_configuration_error_returns_two(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setattr(cli, "_resolved_plan", lambda args: {"experiment": object()})
    assert cli.main(["invariant-generation"]) == cli.EXIT_CONFIGURATION_ERROR
    assert "configuration error" in capsys.readouterr().err


def test_checker_root_remains_relative_to_source_root() -> None:
    args = cli.build_parser().parse_args(
        ["checker-authoring", "--inputs", "accepted.jsonl"]
    )

    plan = cli._resolved_plan(args)

    assert plan["parameters"]["checker_root"] == "core/internal/oracle"


def test_reference_root_defaults_from_environment(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    reference_root = tmp_path / "reviewer"
    monkeypatch.setenv("DEFUZZ_REFERENCE_ROOT", str(reference_root))
    assert cli.main(["agent-audit", "--show-plan"]) == 0
    plan = json.loads(capsys.readouterr().out)
    assert plan["parameters"]["reference_root"] == str(reference_root)


@pytest.mark.parametrize(
    ("command", "message"),
    [
        (["invariant-generation", "--corpus-root", "missing"], "corpus root"),
        (["checker-authoring"], "requires --inputs or --from-run"),
        (["agent-audit"], "requires --target-tree"),
    ],
)
def test_missing_required_execution_inputs_fail_before_creating_run(
    command: list[str],
    message: str,
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    monkeypatch.chdir(tmp_path)
    output_root = tmp_path / "runs"
    assert (
        cli.main([*command, "--output-root", str(output_root)])
        == cli.EXIT_CONFIGURATION_ERROR
    )
    assert message in capsys.readouterr().err
    assert not output_root.exists()


def test_agent_audit_rejects_incomplete_reference_root_before_run_creation(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    reference_root = tmp_path / "reference"
    reference_root.mkdir()
    output_root = tmp_path / "runs"
    assert (
        cli.main(
            [
                "agent-audit",
                "--target-tree",
                str(tmp_path),
                "--reference-root",
                str(reference_root),
                "--output-root",
                str(output_root),
            ]
        )
        == cli.EXIT_CONFIGURATION_ERROR
    )
    assert "missing required documents" in capsys.readouterr().err
    assert not output_root.exists()


def test_full_audit_requires_online_oracle_command(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setattr(cli.shutil, "which", lambda binary: f"/bin/{binary}")
    reference = tmp_path / "reference"
    for relative in cli._REQUIRED_REFERENCE_PATHS:
        path = reference / relative
        if path.suffix:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("fixture\n", encoding="utf-8")
        else:
            path.mkdir(parents=True, exist_ok=True)

    result = cli.main(
        [
            "agent-audit",
            "--target-tree",
            str(tmp_path),
            "--reference-root",
            str(reference),
            "--output-root",
            str(tmp_path / "runs"),
        ]
    )

    assert result == cli.EXIT_CONFIGURATION_ERROR
    assert "requires --online-oracle-command" in capsys.readouterr().err
