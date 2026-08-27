from __future__ import annotations

import json
import os
import stat
from pathlib import Path

import pytest

from defuzz_loop.experiment_engine import (
    AgentRequest,
    ArtifactRef,
    BudgetEnvelope,
    ExecAgentBackend,
    ExperimentPlan,
    PlanMismatchError,
    RunStore,
    StageResult,
    VariantPolicy,
    WorkspaceBuilder,
    WorkspaceSecurityError,
)
from defuzz_loop.token_usage import BudgetExceeded, TokenUsageContext, TokenUsageSink, read_jsonl


def _plan(**updates: object) -> ExperimentPlan:
    values: dict[str, object] = {
        "experiment": "agent-audit",
        "variant": "full",
        "run": {
            "run_id": "audit-r1",
            "output_root": "ignored-output",
            "token_budget": 101,
            "time_budget_minutes": 2,
            "repetitions": 2,
        },
        "parameters": {"compiler": "gcc"},
        "status": "scaffold",
        "backend_available": False,
    }
    values.update(updates)
    return ExperimentPlan.from_mapping(values)


def test_models_normalize_cli_plan_and_freeze_variant_semantics(tmp_path: Path) -> None:
    plan = _plan()
    assert plan.run_id == "audit-r1"
    assert plan.repetitions == 2
    assert plan.budget == BudgetEnvelope(token_budget=101, time_budget_minutes=2)
    assert plan.parameters == {"compiler": "gcc"}
    assert plan.run["token_budget"] == 101
    assert (
        plan.content_hash()
        == _plan(
            run={
                "run_id": "audit-r1",
                "output_root": tmp_path,
                "token_budget": 101,
                "time_budget_minutes": 2,
                "repetitions": 2,
            }
        ).content_hash()
    )

    policies = {
        name: VariantPolicy.for_variant(name)
        for name in (
            "full",
            "without-rag",
            "without-oracle",
            "bare-agent",
        )
    }
    assert policies["full"].use_rag
    assert not policies["without-rag"].use_rag
    assert not policies["without-oracle"].use_online_oracle
    assert policies["without-oracle"].use_dedicated_checkers
    assert not policies["bare-agent"].use_structured_workflow
    with pytest.raises(ValueError, match="unsupported experiment variant"):
        VariantPolicy.for_variant("other")

    artifact = tmp_path / "artifact.json"
    artifact.write_text("{}\n", encoding="utf-8")
    reference = ArtifactRef.from_path(artifact, base_dir=tmp_path, kind="result")
    result = StageResult(stage="audit", artifacts=[reference])
    assert result.success and reference.path == "artifact.json"
    assert not StageResult(stage="audit", status="failed", error="boom").success


def test_run_store_initializes_repetitions_checks_resume_and_writes_usage(
    tmp_path: Path,
) -> None:
    root = tmp_path / "run"
    store = RunStore(root, _plan())
    assert (root / "plan.json").is_file()
    assert (root / "manifest.json").is_file()
    assert store.rep_dir(1).is_dir()
    assert store.rep_dir(2).is_dir()

    store.append_event({"type": "started"}, repetition=1)
    event = json.loads((store.rep_dir(1) / "events.jsonl").read_text().splitlines()[0])
    assert event["type"] == "started"
    assert not list(root.rglob("*.tmp"))

    sink = store.token_sink(1, part="III", stage="agent-audit")
    sink.record_external_usage(
        {
            "type": "turn.completed",
            "usage": {"input_tokens": 7, "output_tokens": 3},
        }
    )
    rows = sink.summarize()
    assert rows[0]["total_tokens"] == 10
    assert read_jsonl(sink.path)[0].stage == "agent-audit"
    assert sink.summary_json_path.is_file()
    assert sink.summary_csv_path.is_file()

    assert RunStore(root, _plan()).verify_resume()
    with pytest.raises(PlanMismatchError, match="plan mismatch"):
        RunStore(root, _plan(parameters={"compiler": "llvm"}))


def test_workspace_builder_excludes_sensitive_corpora_and_hashes_copy(
    tmp_path: Path,
) -> None:
    source = tmp_path / "source"
    (source / "src").mkdir(parents=True)
    (source / "src" / "main.cc").write_text("int main() {}\n")
    (source / "findings").mkdir()
    (source / "findings" / "DREV-001.md").write_text("secret")
    (source / "reports" / "DREV-002").mkdir(parents=True)
    (source / "reports" / "DREV-002" / "report.md").write_text("secret")
    (source / ".git").mkdir()
    (source / ".git" / "config").write_text("secret")
    (source / "orchestrator" / "runs" / "old").mkdir(parents=True)
    (source / "orchestrator" / "runs" / "old" / "result").write_text("secret")

    first = WorkspaceBuilder(source).materialize(tmp_path / "copy-one")
    second = WorkspaceBuilder(source).materialize(tmp_path / "copy-two")
    assert (first.root / "src" / "main.cc").is_file()
    assert not (first.root / "findings").exists()
    assert not (first.root / "reports" / "DREV-002").exists()
    assert not (first.root / ".git").exists()
    assert not (first.root / "orchestrator" / "runs").exists()
    assert first.sha256 == second.sha256
    assert os.fspath(first) == os.fspath(first.root)

    outside = tmp_path / "outside.txt"
    outside.write_text("not allowed")
    (source / "src" / "escape").symlink_to(outside)
    with pytest.raises(WorkspaceSecurityError, match="escapes source root"):
        WorkspaceBuilder(source).materialize(tmp_path / "copy-three")


def _fake_agent(path: Path) -> Path:
    path.write_text(
        """#!/usr/bin/env python3
import json
import os
import pathlib
import sys
import time

args = sys.argv[1:]
prompt = sys.stdin.read()
read_value = None
read_error = None
if prompt.startswith("read:"):
    try:
        read_value = pathlib.Path(prompt.removeprefix("read:")).read_text(encoding="utf-8")
    except OSError as exc:
        read_error = str(exc)
if prompt == "timeout":
    time.sleep(10)
    raise SystemExit(0)
output_flag = "--output-last-message"
output = pathlib.Path(args[args.index(output_flag) + 1])
if prompt != "no-final":
    value = {
        "answer": 7 if prompt == "bad-schema" else "ok",
        "prompt": prompt,
        "read_value": read_value,
        "read_error": read_error,
        "argv": args,
        "environment": {
            "home": pathlib.Path.home().as_posix(),
            "path": os.environ.get("PATH"),
            "trae_home": os.environ.get("TRAE_HOME"),
            "traecli_home": os.environ.get("TRAECLI_HOME"),
            "codex_home": os.environ.get("CODEX_HOME"),
            "secret_token": os.environ.get("SECRET_TOKEN"),
            "openai_api_key": os.environ.get("OPENAI_API_KEY"),
            "github_token": os.environ.get("GITHUB_TOKEN"),
        },
    }
    output.write_text(json.dumps(value), encoding="utf-8")
print("diagnostic text")
if prompt != "no-completed":
    print(json.dumps({"type": "turn.completed", "usage": {"input_tokens": 7, "output_tokens": 3}}))
""",
        encoding="utf-8",
    )
    path.chmod(0o755)
    return path


@pytest.mark.asyncio
async def test_exec_backend_uses_safe_argv_preserves_events_and_records_usage(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    codex_home = tmp_path / "codex-home"
    codex_home.mkdir()
    (codex_home / "auth.json").write_text("{}\n", encoding="utf-8")
    monkeypatch.setenv("CODEX_HOME", str(codex_home))
    binary = _fake_agent(tmp_path / "fake codex")
    workspace = tmp_path / "workspace $(touch nope)"
    workspace.mkdir()
    sink = TokenUsageSink(
        tmp_path / "usage.jsonl",
        context=TokenUsageContext(
            run_id="audit-r1-rep-001",
            experiment="agent-audit",
            variant="full",
            part="III",
            stage="agent-audit",
        ),
    )
    request = AgentRequest(
        prompt="hello",
        cwd=workspace,
        output_dir=tmp_path / "agent-output",
        token_sink=sink,
        metadata={
            "run_id": "audit-r1-rep-001",
            "experiment": "agent-audit",
            "variant": "full",
            "part": "III",
            "stage": "agent-audit",
        },
    )
    backend = ExecAgentBackend(binary, model="test-model", provider="codex")
    argv = backend.argv_for(request)
    assert argv.index("--sandbox") < argv.index("exec")
    assert argv.index("--ask-for-approval") < argv.index("exec")
    assert argv.index("-C") < argv.index("exec")
    assert argv[argv.index("--sandbox") + 1] == "read-only"
    assert "--ignore-user-config" in argv
    assert "--ignore-rules" in argv
    assert "memories.use_memories=false" in argv
    assert "features.plugin_hooks=false" not in argv
    assert argv[-1] == "-"

    result = await backend.run(request)
    assert result.success
    assert result.final["prompt"] == request.prompt
    assert result.usage and result.usage["total_tokens"] == 10
    assert result.events_path and result.events_path.read_text().startswith("diagnostic text\n")
    assert read_jsonl(sink.path)[0].total_tokens == 10
    assert not (tmp_path / "nope").exists()

    with pytest.raises(ValueError, match="capability-affecting"):
        ExecAgentBackend(binary, extra_args=["--sandbox=danger-full-access"])
    with pytest.raises(ValueError, match="config-affecting"):
        ExecAgentBackend(binary, extra_args=["-c", "memories.use_memories=true"])
    with pytest.raises(ValueError, match="config-affecting"):
        ExecAgentBackend(binary, extra_args=["--config=memories.use_memories=true"])
    with pytest.raises(ValueError, match="capability-affecting"):
        ExecAgentBackend(binary, extra_args=["--enable=plugins"])
    with pytest.raises(ValueError, match="capability-affecting"):
        ExecAgentBackend(binary, extra_args=["--model=other"])
    with pytest.raises(ValueError, match="capability-affecting"):
        ExecAgentBackend(binary, extra_args=["-m", "other"])


def test_exec_backend_injects_traex_isolation_config_before_exec(tmp_path: Path) -> None:
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    request = AgentRequest(
        prompt="hello",
        cwd=workspace,
        output_dir=tmp_path / "agent-output",
    )

    argv = ExecAgentBackend("traex", model="test-model").argv_for(request)

    expected_prefix = [
        "traex",
        "--sandbox",
        "read-only",
        "--ask-for-approval",
        "never",
        "-C",
        str(workspace.resolve()),
        "--model",
        "test-model",
        "-c",
        "memories.use_memories=false",
        "-c",
        "memories.generate_memories=false",
        "-c",
        "features.memories=false",
        "-c",
        "project_doc_max_bytes=0",
        "-c",
        "resource_dirs=[]",
        "-c",
        "skills.bundled.enabled=false",
        "-c",
        "skills.include_instructions=false",
        "-c",
        "features.plugins=false",
        "-c",
        "features.hooks=false",
        "-c",
        "features.apps=false",
        "-c",
        "features.plugin_hooks=false",
        "exec",
    ]
    assert argv[: len(expected_prefix)] == expected_prefix
    assert argv.count("-c") == 11
    assert "--ignore-user-config" in argv
    assert "--ignore-rules" in argv
    assert "--output-last-message" in argv
    assert argv[-1] == "-"


def test_exec_backend_explicit_provider_overrides_custom_binary_name(tmp_path: Path) -> None:
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    request = AgentRequest(prompt="hello", cwd=workspace, output_dir=tmp_path / "out")

    codex = ExecAgentBackend("/custom/agent", provider="codex")
    traex = ExecAgentBackend("/custom/agent", provider="traex")

    codex_argv = codex.argv_for(request)
    traex_argv = traex.argv_for(request)
    assert codex.provider == "codex"
    assert traex.provider == "traex"
    assert "memories.use_memories=false" in codex_argv
    assert "features.plugin_hooks=false" not in codex_argv
    assert "features.plugin_hooks=false" in traex_argv
    with pytest.raises(ValueError, match="cannot be inferred"):
        ExecAgentBackend("/custom/agent")

    with pytest.raises(ValueError, match="unsupported agent provider"):
        ExecAgentBackend("/custom/agent", provider="other")  # type: ignore[arg-type]


def test_exec_backend_resolves_traex_credentials_home_in_precedence_order(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    explicit_cli = tmp_path / "explicit-cli"
    explicit_trae = tmp_path / "explicit-trae"
    fallback_home = tmp_path / "home"
    monkeypatch.setenv("HOME", str(fallback_home))
    monkeypatch.setenv("TRAE_HOME", str(explicit_trae))
    monkeypatch.setenv("TRAECLI_HOME", str(explicit_cli))
    assert ExecAgentBackend._traex_cli_home() == explicit_cli

    monkeypatch.delenv("TRAECLI_HOME")
    assert ExecAgentBackend._traex_cli_home() == explicit_trae / "cli"

    monkeypatch.delenv("TRAE_HOME")
    assert ExecAgentBackend._traex_cli_home() == fallback_home / ".trae" / "cli"


def test_exec_backend_auto_denies_original_agent_resources(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    home = tmp_path / "home"
    trae_home = tmp_path / "runtime-trae"
    cli_home = trae_home / "cli"
    workspace = tmp_path / "workspace"
    output = tmp_path / "out"
    for path in (
        home / ".trae",
        home / ".trae-cn",
        home / ".agents" / "skills",
        cli_home,
        workspace,
    ):
        path.mkdir(parents=True)
    monkeypatch.setenv("HOME", str(home))
    monkeypatch.setenv("TRAE_HOME", str(trae_home))
    monkeypatch.delenv("TRAECLI_HOME", raising=False)
    monkeypatch.setattr(
        ExecAgentBackend,
        "supports_host_read_isolation",
        property(lambda _backend: True),
    )

    argv = ExecAgentBackend("/custom/agent", provider="traex").launch_argv_for(
        AgentRequest(prompt="probe", cwd=workspace, output_dir=output)
    )

    assert argv[:2] == ["/usr/bin/sandbox-exec", "-p"]
    profile = argv[2]
    assert str(trae_home.resolve()) in profile
    assert str((home / ".trae").resolve()) in profile
    assert str((home / ".trae-cn").resolve()) in profile
    assert str((home / ".agents" / "skills").resolve()) in profile


def test_exec_backend_does_not_deny_arbitrary_traecli_home_parent(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    workspace = tmp_path / "workspace"
    credential_home = tmp_path / "credentials"
    workspace.mkdir()
    credential_home.mkdir()
    monkeypatch.setenv("TRAECLI_HOME", str(credential_home))
    monkeypatch.setenv("TRAE_HOME", str(tmp_path / "trae-home"))
    monkeypatch.setattr(
        ExecAgentBackend,
        "supports_host_read_isolation",
        property(lambda _backend: True),
    )

    argv = ExecAgentBackend("/custom/agent", provider="traex").launch_argv_for(
        AgentRequest(prompt="probe", cwd=workspace, output_dir=tmp_path / "out")
    )

    assert str(credential_home.resolve()) in argv[2]
    assert f"(subpath {json.dumps(str(tmp_path.resolve()))})" not in argv[2]


def test_exec_backend_explicit_codex_auto_denies_original_codex_home(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    codex_home = tmp_path / "codex-home"
    workspace = tmp_path / "workspace"
    codex_home.mkdir()
    workspace.mkdir()
    monkeypatch.setenv("CODEX_HOME", str(codex_home))
    monkeypatch.setattr(
        ExecAgentBackend,
        "supports_host_read_isolation",
        property(lambda _backend: True),
    )

    argv = ExecAgentBackend("/custom/agent", provider="codex").launch_argv_for(
        AgentRequest(prompt="probe", cwd=workspace, output_dir=tmp_path / "out")
    )

    assert argv[:2] == ["/usr/bin/sandbox-exec", "-p"]
    assert str(codex_home.resolve()) in argv[2]


def test_exec_backend_fails_closed_when_resource_deny_contains_workspace(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    workspace = tmp_path / "runtime-home" / "workspace"
    workspace.mkdir(parents=True)
    monkeypatch.setenv("TRAE_HOME", str(tmp_path / "runtime-home"))
    monkeypatch.delenv("TRAECLI_HOME", raising=False)

    with pytest.raises(RuntimeError, match="contains the workspace"):
        ExecAgentBackend("/custom/agent", provider="traex").launch_argv_for(
            AgentRequest(prompt="probe", cwd=workspace, output_dir=tmp_path / "out")
        )


def test_exec_backend_fails_closed_when_resource_deny_contains_binary(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    runtime_home = tmp_path / "runtime-home"
    binary = runtime_home / "bin" / "traex"
    binary.parent.mkdir(parents=True)
    binary.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
    binary.chmod(0o755)
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    monkeypatch.setenv("TRAE_HOME", str(runtime_home))
    monkeypatch.delenv("TRAECLI_HOME", raising=False)

    with pytest.raises(RuntimeError, match="contains the agent binary"):
        ExecAgentBackend(binary, provider="traex").launch_argv_for(
            AgentRequest(prompt="probe", cwd=workspace, output_dir=tmp_path / "out")
        )


@pytest.mark.asyncio
async def test_os_sandbox_auto_denies_original_traex_resources(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    backend = ExecAgentBackend("traex")
    if not backend.supports_host_read_isolation:
        pytest.skip("host sandbox is unavailable")
    source_home = tmp_path / "source-trae-home"
    credential_home = source_home / "cli"
    skill_dir = source_home / "skills" / "private"
    credential_home.mkdir(parents=True)
    skill_dir.mkdir(parents=True)
    (credential_home / "auth.json").write_text("{}\n", encoding="utf-8")
    secret = skill_dir / "sentinel.txt"
    secret.write_text("USER_SKILL_SECRET", encoding="utf-8")
    monkeypatch.setenv("TRAE_HOME", str(source_home))
    monkeypatch.setenv("TRAECLI_HOME", str(credential_home))
    binary = _fake_agent(tmp_path / "custom-agent")
    workspace = tmp_path / "workspace"
    workspace.mkdir()

    result = await ExecAgentBackend(binary, provider="traex").run(
        AgentRequest(
            prompt=f"read:{secret}",
            cwd=workspace,
            output_dir=tmp_path / "out",
        )
    )

    assert result.success
    assert result.final["read_value"] is None
    assert "Operation not permitted" in result.final["read_error"]
    assert "USER_SKILL_SECRET" not in result.raw_stdout


@pytest.mark.asyncio
async def test_exec_backend_runs_traex_in_disposable_homes_with_only_auth_state(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    trae_home = tmp_path / "source-trae-home"
    credential_home = trae_home / "cli"
    credential_home.mkdir(parents=True)
    auth = credential_home / "auth.json"
    auth.write_text('{"token": "secret"}\n', encoding="utf-8")
    models = credential_home / "models_cache.json"
    models.write_text('{"models": []}\n', encoding="utf-8")
    (credential_home / "memories").mkdir()
    (credential_home / "memories" / "memory_summary.md").write_text(
        "must not be copied", encoding="utf-8"
    )
    (credential_home / "hooks.json").write_text("{}\n", encoding="utf-8")
    monkeypatch.setenv("TRAECLI_HOME", str(credential_home))
    monkeypatch.setenv("TRAE_HOME", str(trae_home))
    monkeypatch.setenv("HOME", str(tmp_path / "inherited-home"))
    monkeypatch.setenv("CODEX_HOME", str(tmp_path / "inherited-codex-home"))
    monkeypatch.setenv("SECRET_TOKEN", "secret-value")
    monkeypatch.setenv("OPENAI_API_KEY", "api-key")
    monkeypatch.setenv("GITHUB_TOKEN", "github-token")

    binary = _fake_agent(tmp_path / "custom-agent")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    result = await ExecAgentBackend(binary, provider="traex").run(
        AgentRequest(
            prompt="sent-once",
            cwd=workspace,
            output_dir=tmp_path / "out",
        )
    )

    assert result.success
    assert result.final["prompt"] == "sent-once"
    assert result.final["prompt"].count("sent-once") == 1
    probe = result.final["environment"]
    isolated_home = Path(probe["home"])
    isolated_trae_home = Path(probe["trae_home"])
    isolated_cli_home = Path(probe["traecli_home"])
    assert isolated_home == tmp_path / "inherited-home"
    assert isolated_trae_home != isolated_cli_home
    assert probe["codex_home"] is None
    assert probe["path"] == os.environ["PATH"]
    assert probe["secret_token"] is None
    assert probe["openai_api_key"] is None
    assert probe["github_token"] is None
    assert isolated_cli_home != credential_home
    assert not isolated_trae_home.exists()
    assert not isolated_cli_home.exists()
    assert not (credential_home / "state_5.sqlite").exists()


def test_exec_backend_cleanroom_copies_only_traex_credentials(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    credential_home = tmp_path / "real-cli-home"
    credential_home.mkdir(parents=True)
    (credential_home / "auth.json").write_text("auth", encoding="utf-8")
    (credential_home / "models_cache.json").write_text("models", encoding="utf-8")
    (credential_home / "memories_1.sqlite").write_text("memory", encoding="utf-8")
    (credential_home / "hooks.json").write_text("hook", encoding="utf-8")
    monkeypatch.setenv("TRAECLI_HOME", str(credential_home))

    with ExecAgentBackend("traex")._subprocess_environment() as environment:
        cli_home = Path(environment["TRAECLI_HOME"])
        assert sorted(path.name for path in cli_home.iterdir()) == [
            "auth.json",
            "models_cache.json",
        ]
        assert (cli_home / "auth.json").read_text(encoding="utf-8") == "auth"
        assert stat.S_IMODE((cli_home / "auth.json").stat().st_mode) == 0o600
        trae_home = Path(environment["TRAE_HOME"])

    assert not cli_home.exists()
    assert not trae_home.exists()


def test_exec_backend_cleanroom_copies_only_codex_credentials(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    credential_home = tmp_path / "real-codex-home"
    credential_home.mkdir(parents=True)
    (credential_home / "auth.json").write_text("auth", encoding="utf-8")
    (credential_home / "models_cache.json").write_text("models", encoding="utf-8")
    (credential_home / "history.jsonl").write_text("history", encoding="utf-8")
    (credential_home / "skills").mkdir()
    monkeypatch.setenv("CODEX_HOME", str(credential_home))

    backend = ExecAgentBackend("/custom/agent", provider="codex")
    with backend._subprocess_environment() as environment:
        isolated_home = Path(environment["CODEX_HOME"])
        assert sorted(path.name for path in isolated_home.iterdir()) == [
            "auth.json",
            "models_cache.json",
        ]
        assert stat.S_IMODE((isolated_home / "auth.json").stat().st_mode) == 0o600
        assert "TRAE_HOME" not in environment
        assert "TRAECLI_HOME" not in environment

    assert not isolated_home.exists()


@pytest.mark.asyncio
async def test_exec_backend_fails_closed_without_traex_credentials(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    credential_home = tmp_path / "source-trae-home" / "cli"
    credential_home.mkdir(parents=True)
    monkeypatch.setenv("TRAECLI_HOME", str(credential_home))
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    marker = tmp_path / "started"
    binary = tmp_path / "fake-traex"
    binary.write_text(f"#!/bin/sh\ntouch {marker}\n", encoding="utf-8")
    binary.chmod(0o755)

    result = await ExecAgentBackend(binary, provider="traex").run(
        AgentRequest(prompt="never", cwd=workspace, output_dir=tmp_path / "out")
    )

    assert not result.success
    assert result.exit_code is None
    assert result.error and "TraeX credentials are unavailable" in result.error
    assert not marker.exists()


@pytest.mark.asyncio
async def test_exec_backend_runs_codex_in_disposable_home_with_only_auth_state(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    codex_home = tmp_path / "real-codex-home"
    codex_home.mkdir()
    (codex_home / "auth.json").write_text('{"token": "secret"}\n', encoding="utf-8")
    (codex_home / "models_cache.json").write_text("{}\n", encoding="utf-8")
    (codex_home / "history.jsonl").write_text("secret history\n", encoding="utf-8")
    monkeypatch.setenv("CODEX_HOME", str(codex_home))
    monkeypatch.setenv("TRAE_HOME", str(tmp_path / "inherited-trae-home"))
    monkeypatch.setenv("TRAECLI_HOME", str(tmp_path / "inherited-traecli-home"))
    monkeypatch.setenv("HOME", str(tmp_path / "inherited-home"))
    monkeypatch.setenv("SECRET_TOKEN", "secret-value")
    monkeypatch.setenv("OPENAI_API_KEY", "api-key")
    binary = _fake_agent(tmp_path / "custom-agent")
    workspace = tmp_path / "workspace"
    workspace.mkdir()

    result = await ExecAgentBackend(binary, provider="codex").run(
        AgentRequest(prompt="codex-once", cwd=workspace, output_dir=tmp_path / "out")
    )

    assert result.success
    assert result.final["prompt"] == "codex-once"
    assert result.final["prompt"].count("codex-once") == 1
    environment = result.final["environment"]
    isolated_codex_home = Path(environment["codex_home"])
    assert Path(environment["home"]) == tmp_path / "inherited-home"
    assert environment["trae_home"] is None
    assert environment["traecli_home"] is None
    assert environment["secret_token"] is None
    assert environment["openai_api_key"] is None
    assert isolated_codex_home != codex_home
    assert not isolated_codex_home.exists()


@pytest.mark.asyncio
async def test_exec_backend_fails_closed_without_codex_credentials(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    codex_home = tmp_path / "empty-codex-home"
    codex_home.mkdir()
    monkeypatch.setenv("CODEX_HOME", str(codex_home))
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    marker = tmp_path / "started"
    binary = tmp_path / "custom-agent"
    binary.write_text(f"#!/bin/sh\ntouch {marker}\n", encoding="utf-8")
    binary.chmod(0o755)

    result = await ExecAgentBackend(binary, provider="codex").run(
        AgentRequest(prompt="never", cwd=workspace, output_dir=tmp_path / "out")
    )

    assert not result.success
    assert result.exit_code is None
    assert result.error and "Codex credentials are unavailable" in result.error
    assert not marker.exists()


@pytest.mark.asyncio
async def test_exec_backend_requires_completion_and_valid_schema(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    codex_home = tmp_path / "codex-home"
    codex_home.mkdir()
    (codex_home / "auth.json").write_text("{}\n", encoding="utf-8")
    monkeypatch.setenv("CODEX_HOME", str(codex_home))
    binary = _fake_agent(tmp_path / "fake-codex")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    schema = tmp_path / "schema.json"
    schema.write_text(
        json.dumps(
            {
                "type": "object",
                "required": ["answer"],
                "properties": {"answer": {"type": "string"}},
            }
        )
    )
    backend = ExecAgentBackend(binary, terminate_grace_seconds=0.05)

    missing = await backend.run(
        AgentRequest(prompt="no-final", cwd=workspace, output_dir=tmp_path / "missing")
    )
    assert not missing.success and "final output" in (missing.error or "")

    incomplete = await backend.run(
        AgentRequest(prompt="no-completed", cwd=workspace, output_dir=tmp_path / "incomplete")
    )
    assert not incomplete.success and "turn.completed" in (incomplete.error or "")

    invalid = await backend.run(
        AgentRequest(
            prompt="bad-schema",
            cwd=workspace,
            output_dir=tmp_path / "invalid",
            schema_path=schema,
        )
    )
    assert not invalid.success and "schema validation" in (invalid.error or "")

    timed_out = await backend.run(
        AgentRequest(
            prompt="timeout",
            cwd=workspace,
            output_dir=tmp_path / "timeout",
            timeout_seconds=0.05,
        )
    )
    assert timed_out.timed_out and not timed_out.success


@pytest.mark.asyncio
async def test_exec_backend_checks_token_budget_before_starting_process(
    tmp_path: Path,
) -> None:
    marker = tmp_path / "started"
    binary = tmp_path / "should-not-run"
    binary.write_text("#!/bin/sh\ntouch " + str(marker) + "\n", encoding="utf-8")
    binary.chmod(0o755)
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    sink = TokenUsageSink(
        tmp_path / "usage.jsonl",
        context=TokenUsageContext(
            run_id="r",
            experiment="agent-audit",
            variant="full",
            part="III",
            stage="audit",
        ),
        token_budget=10,
    )
    sink.record_external_usage({"type": "turn.completed", "usage": {"total_tokens": 10}})

    with pytest.raises(BudgetExceeded):
        await ExecAgentBackend(binary, provider="codex").run(
            AgentRequest(
                prompt="never",
                cwd=workspace,
                output_dir=tmp_path / "out",
                token_sink=sink,
            )
        )

    assert not marker.exists()


def test_exec_backend_wraps_host_read_denials_with_os_sandbox(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(
        ExecAgentBackend,
        "supports_host_read_isolation",
        property(lambda _backend: True),
    )
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    denied = tmp_path / "reference"
    denied.mkdir()
    request = AgentRequest(
        prompt="audit",
        cwd=workspace,
        output_dir=tmp_path / "out",
        deny_read_paths=[denied],
        require_host_read_isolation=True,
    )
    backend = ExecAgentBackend("traex")

    argv = backend.launch_argv_for(request)

    assert argv[:2] == ["/usr/bin/sandbox-exec", "-p"]
    assert "deny file-read*" in argv[2]
    assert str(denied.resolve()) in argv[2]


def test_exec_backend_fails_closed_when_host_read_isolation_is_unavailable(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(
        ExecAgentBackend,
        "supports_host_read_isolation",
        property(lambda _backend: False),
    )
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    denied = tmp_path / "reference"
    denied.mkdir()
    request = AgentRequest(
        prompt="audit",
        cwd=workspace,
        output_dir=tmp_path / "out",
        deny_read_paths=[denied],
        require_host_read_isolation=True,
    )

    with pytest.raises(RuntimeError, match="host read isolation is unavailable"):
        ExecAgentBackend("traex").launch_argv_for(request)


@pytest.mark.asyncio
async def test_os_sandbox_denies_all_original_inputs_but_allows_sanitized_copy(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    if not ExecAgentBackend("traex").supports_host_read_isolation:
        pytest.skip("host sandbox is unavailable")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    reference = tmp_path / "reference"
    reference.mkdir()
    secret = reference / "secret.txt"
    secret.write_text("private finding", encoding="utf-8")
    source = tmp_path / "source"
    source.mkdir()
    source_secret = source / "secret.txt"
    source_secret.write_text("original source secret", encoding="utf-8")
    sanitized = workspace / "compiler.c"
    sanitized.write_text("sanitized compiler copy", encoding="utf-8")
    binary = tmp_path / "probe-codex-agent"
    codex_home = tmp_path / "codex-home"
    codex_home.mkdir()
    (codex_home / "auth.json").write_text("{}\n", encoding="utf-8")
    monkeypatch.setenv("CODEX_HOME", str(codex_home))
    binary.write_text(
        "#!/bin/sh\n"
        "/bin/cat " + str(sanitized) + "\n"
        "/bin/cat " + str(secret) + " || true\n"
        "/bin/cat " + str(source_secret) + " || true\n"
        "exit 1\n",
        encoding="utf-8",
    )
    binary.chmod(0o755)

    result = await ExecAgentBackend(binary).run(
        AgentRequest(
            prompt="probe",
            cwd=workspace,
            output_dir=tmp_path / "out",
            deny_read_paths=[source, reference],
            require_host_read_isolation=True,
        )
    )

    assert not result.success
    assert "sanitized compiler copy" in result.raw_stdout
    assert "private finding" not in result.raw_stdout
    assert "original source secret" not in result.raw_stdout
