from __future__ import annotations

import json
import os
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
    assert plan.content_hash() == _plan(
        run={
            "run_id": "audit-r1",
            "output_root": tmp_path,
            "token_budget": 101,
            "time_budget_minutes": 2,
            "repetitions": 2,
        }
    ).content_hash()

    policies = {name: VariantPolicy.for_variant(name) for name in (
        "full",
        "without-rag",
        "without-oracle",
        "bare-agent",
    )}
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
import pathlib
import sys
import time

args = sys.argv[1:]
prompt = sys.stdin.read()
if prompt == "timeout":
    time.sleep(10)
    raise SystemExit(0)
output_flag = "--output-last-message"
output = pathlib.Path(args[args.index(output_flag) + 1])
if prompt != "no-final":
    value = {"answer": 7 if prompt == "bad-schema" else "ok", "prompt": prompt, "argv": args}
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
    tmp_path: Path,
) -> None:
    binary = _fake_agent(tmp_path / "fake codex")
    workspace = tmp_path / "workspace $(touch nope)"
    workspace.mkdir()
    store = RunStore(tmp_path / "run", _plan(repetitions=1))
    sink = store.token_sink(1, part="III", stage="agent-audit")
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
    backend = ExecAgentBackend(binary, model="test-model")
    argv = backend.argv_for(request)
    assert argv.index("--sandbox") < argv.index("exec")
    assert argv.index("--ask-for-approval") < argv.index("exec")
    assert argv.index("-C") < argv.index("exec")
    assert argv[argv.index("--sandbox") + 1] == "read-only"
    assert "--ignore-user-config" in argv
    assert "--ignore-rules" in argv
    assert argv[-1] == "-"

    result = await backend.run(request)
    assert result.success
    assert result.final["prompt"] == "hello"
    assert result.usage and result.usage["total_tokens"] == 10
    assert result.events_path and result.events_path.read_text().startswith("diagnostic text\n")
    assert read_jsonl(sink.path)[0].total_tokens == 10
    assert not (tmp_path / "nope").exists()

    with pytest.raises(ValueError, match="capability-affecting"):
        ExecAgentBackend(binary, extra_args=["--sandbox=danger-full-access"])


@pytest.mark.asyncio
async def test_exec_backend_requires_completion_and_valid_schema(tmp_path: Path) -> None:
    binary = _fake_agent(tmp_path / "fake-traex")
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
    binary.write_text(
        "#!/bin/sh\ntouch " + str(marker) + "\n", encoding="utf-8"
    )
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
    sink.record_external_usage(
        {"type": "turn.completed", "usage": {"total_tokens": 10}}
    )

    with pytest.raises(BudgetExceeded):
        await ExecAgentBackend(binary).run(
            AgentRequest(
                prompt="never",
                cwd=workspace,
                output_dir=tmp_path / "out",
                token_sink=sink,
            )
        )

    assert not marker.exists()


def test_exec_backend_wraps_host_read_denials_with_os_sandbox(tmp_path: Path) -> None:
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

    if backend.supports_host_read_isolation:
        assert argv[:2] == ["/usr/bin/sandbox-exec", "-p"]
        assert "deny file-read*" in argv[2]
        assert str(denied.resolve()) in argv[2]
    else:
        pytest.fail("this platform is expected to expose the configured host sandbox")


@pytest.mark.asyncio
async def test_os_sandbox_denies_reference_reads_before_agent_launch(
    tmp_path: Path,
) -> None:
    if not ExecAgentBackend("traex").supports_host_read_isolation:
        pytest.skip("host sandbox is unavailable")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    reference = tmp_path / "reference"
    reference.mkdir()
    secret = reference / "secret.txt"
    secret.write_text("private finding", encoding="utf-8")
    binary = tmp_path / "probe-agent"
    binary.write_text(
        "#!/bin/sh\n/bin/cat " + str(secret) + "\n", encoding="utf-8"
    )
    binary.chmod(0o755)

    result = await ExecAgentBackend(binary).run(
        AgentRequest(
            prompt="probe",
            cwd=workspace,
            output_dir=tmp_path / "out",
            deny_read_paths=[reference],
            require_host_read_isolation=True,
        )
    )

    assert not result.success
    assert "private finding" not in result.raw_stdout
