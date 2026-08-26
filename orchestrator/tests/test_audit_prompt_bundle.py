from __future__ import annotations

import json
import shutil
import stat
import sys
from pathlib import Path
from typing import Any

import pytest

from defuzz_loop.agent_audit import run
from defuzz_loop.audit_schema import CANONICAL_AUDIT_FAMILIES
from defuzz_loop.experiment_engine import AgentRequest, AgentResult, ExperimentPlan
from defuzz_loop.prompt_bundle import audit_doctrine_parity, build_worker_prompt_bundle

_UNUSED_ONLINE_ORACLE_COMMAND = (
    "unused-online-oracle",
    "{candidate_fingerprint}",
)
_ERROR_ONLINE_ORACLE_COMMAND = (
    sys.executable,
    "-c",
    (
        "import json, sys; "
        "print(json.dumps({'candidate_fingerprint': sys.argv[1], "
        "'verdict': 'ERROR', 'feedback': 'checker unavailable', "
        "'evidence': ['fixture checker error']}))"
    ),
    "{candidate_fingerprint}",
)


def _reference(tmp_path: Path) -> Path:
    root = tmp_path / "reviewer"
    (root / ".claude" / "agents").mkdir(parents=True)
    (root / "docs" / "prompts").mkdir(parents=True)
    (root / "docs" / "bugs" / "gcc" / "stack-protector").mkdir(parents=True)
    (root / "docs" / "invariants").mkdir(parents=True)
    (root / "findings" / "DREV-2099-999").mkdir(parents=True)
    (root / ".claude" / "agents" / "defend-reviewer.md").write_text(
        "CANONICAL_DOCTRINE\nDo not read findings/.\n", encoding="utf-8"
    )
    family_blocks = "\n\n".join(
        f"### SUBAGENT {key} — family {key}\nFAMILY_{key}_ONLY"
        for key in "ABCDE"
    )
    (root / "docs" / "prompts" / "full-review.md").write_text(
        family_blocks
        + "\n\n## Uniform subagent instructions\nUNIFORM_RULE\n\n## Phase 2: stop\n",
        encoding="utf-8",
    )
    (root / "docs" / "invariants" / "README.md").write_text(
        "INVARIANT_INDEX", encoding="utf-8"
    )
    (root / "docs" / "invariants" / "stack-canary.md").write_text(
        "INVARIANT_SENTINEL", encoding="utf-8"
    )
    (root / "docs" / "invariants" / "stack-clash-protection.md").write_text(
        "STACK_CLASH_INVARIANT", encoding="utf-8"
    )
    (root / "docs" / "bugs" / "gcc" / "stack-protector" / "CVE-1.md").write_text(
        "HISTORICAL_SENTINEL\nfindings/DREV-2099-998", encoding="utf-8"
    )
    (root / "findings" / "DREV-2099-999" / "README.md").write_text(
        "PRIVATE_FINDING_SENTINEL", encoding="utf-8"
    )
    (root / "oracle.txt").write_text("ORACLE_SENTINEL", encoding="utf-8")
    return root


def _source(tmp_path: Path) -> Path:
    root = tmp_path / "source"
    root.mkdir()
    (root / "compiler.c").write_text("int compiler(void) { return 0; }\n")
    (root / "findings").mkdir()
    (root / "findings" / "secret.txt").write_text("SOURCE_FINDING_SENTINEL")
    (root / "reports").mkdir()
    (root / "reports" / "private.txt").write_text("SOURCE_REPORT_SENTINEL")
    (root / "runs").mkdir()
    (root / "runs" / "prior.json").write_text("SOURCE_RUN_SENTINEL")
    (root / ".git").mkdir()
    (root / ".git" / "config").write_text("SOURCE_GIT_SENTINEL")
    return root


def test_canonical_five_family_mapping_is_frozen() -> None:
    assert [family.key for family in CANONICAL_AUDIT_FAMILIES] == list("ABCDE")
    assert CANONICAL_AUDIT_FAMILIES[0].mechanisms == (
        "stack-protector",
        "stack-clash-protection",
    )


def test_doctrine_parity_reports_stale_wrapper_without_using_it(tmp_path: Path) -> None:
    root = _reference(tmp_path)
    trae = root / ".trae/agents/defend-reviewer.md"
    trae.parent.mkdir(parents=True)
    trae.write_text("---\nname: reviewer\n---\nSTALE\n", encoding="utf-8")

    report = audit_doctrine_parity(root)

    assert report["canonical"] == ".claude/agents/defend-reviewer.md"
    assert report["all_match"] is False
    assert report["mismatches"] == [".trae/agents/defend-reviewer.md"]
    assert CANONICAL_AUDIT_FAMILIES[-1].mechanisms == (
        "asan",
        "ubsan",
        "tsan",
        "lsan",
        "auto-var-init",
        "glibcxx-assertions",
        "fhardened",
    )


def test_prompt_variants_have_exact_visibility_and_never_read_findings(tmp_path: Path) -> None:
    root = _reference(tmp_path)
    common: dict[str, Any] = {
        "source_roots": [tmp_path / "source"],
        "toolchains": ["gcc"],
        "oracle_documents": [root / "oracle.txt"],
    }
    full = build_worker_prompt_bundle(root, "A", "full", **common)
    without_oracle = build_worker_prompt_bundle(root, "A", "without-oracle", **common)
    bare = build_worker_prompt_bundle(root, "A", "bare-agent", **common)

    assert "CANONICAL_DOCTRINE" in full.prompt
    assert "FAMILY_A_ONLY" in full.prompt and "FAMILY_B_ONLY" not in full.prompt
    assert "INVARIANT_SENTINEL" in full.prompt
    assert "HISTORICAL_SENTINEL" in full.prompt
    assert "ORACLE_SENTINEL" in full.prompt
    assert "PRIVATE_FINDING_SENTINEL" not in full.prompt
    assert "DREV-2099-998" not in full.prompt

    assert "CANONICAL_DOCTRINE" in without_oracle.prompt
    assert "ORACLE_SENTINEL" not in without_oracle.prompt
    assert "no dedicated checker or online oracle feedback" in without_oracle.prompt

    assert "CANONICAL_DOCTRINE" not in bare.prompt
    assert "INVARIANT_SENTINEL" not in bare.prompt
    assert "HISTORICAL_SENTINEL" not in bare.prompt
    assert "ORACLE_SENTINEL" not in bare.prompt
    assert "independent general code-audit worker" in bare.prompt
    assert bare.family.key == "neutral"
    assert "Assigned family" not in bare.prompt
    assert "Mechanisms:" not in bare.prompt
    assert "AuditReport" not in bare.prompt
    assert str(root) not in full.prompt
    assert str(root) not in bare.prompt
    assert str(tmp_path / "source") not in full.prompt

    with pytest.raises(ValueError, match="Part III supports only"):
        build_worker_prompt_bundle(root, "A", "without-rag")
    with pytest.raises(ValueError, match="forbidden"):
        build_worker_prompt_bundle(
            root,
            "A",
            "full",
            extra_documents=[root / "findings" / "DREV-2099-999" / "README.md"],
        )


class _FakeBackend:
    def __init__(self) -> None:
        self.requests: list[AgentRequest] = []
        self.workspace_files: list[set[str]] = []
        self.workspace_read_only: list[bool] = []

    async def run(self, request: AgentRequest) -> AgentResult:
        self.requests.append(request)
        self.workspace_files.append(
            {path.relative_to(request.cwd).as_posix() for path in request.cwd.rglob("*")}
        )
        self.workspace_read_only.append(
            not bool(request.cwd.stat().st_mode & stat.S_IWUSR)
            and not bool((request.cwd / "compiler.c").stat().st_mode & stat.S_IWUSR)
        )
        family = request.metadata["family"]
        if request.metadata["variant"] == "bare-agent":
            return AgentResult(
                success=True,
                final={
                    "worker_bundle_sha256": request.metadata[
                        "worker_bundle_sha256"
                    ],
                    "issues": [],
                    "coverage_gaps": ["generic gap"],
                },
            )
        return AgentResult(
            success=True,
            final={
                "family": family,
                "variant": request.metadata["variant"],
                "worker_bundle_sha256": request.metadata[
                    "worker_bundle_sha256"
                ],
                "candidates": [],
                "coverage_gaps": [f"gap-{family}"],
            },
        )


class _TaintedBackend:
    async def run(self, request: AgentRequest) -> AgentResult:
        return AgentResult(
            success=True,
            final={
                "family": request.metadata["family"],
                "variant": request.metadata["variant"],
                "worker_bundle_sha256": request.metadata[
                    "worker_bundle_sha256"
                ],
                "candidates": [
                    {
                        "id": "DREV-2026-001",
                        "toolchain": "gcc",
                        "toolchain_version": "gcc-17",
                        "mechanism": "stack-protector",
                        "isa": ["x86_64"],
                        "invariant_violated": "A concrete invariant.",
                        "evidence_file_line": ["gcc/x.cc:10"],
                        "evidence_code": "1\n2\n3\n4\n5",
                        "minimal_trigger": {
                            "source": "int main(void) { return 0; }",
                            "flags": "-O2",
                            "isa": "x86_64",
                        },
                        "impact": "A required check is skipped.",
                        "why_not_rescued": "No later layer restores the check.",
                        "poc_verified": True,
                        "discovered": "2026-08-26",
                    }
                ],
            },
        )


class _InvalidWorkerBackend:
    def __init__(self, invalidity: str) -> None:
        self.invalidity = invalidity

    async def run(self, request: AgentRequest) -> AgentResult:
        if self.invalidity == "parse-issue":
            return AgentResult(success=True, final="not: [valid")
        payload = {
            "family": request.metadata["family"],
            "variant": request.metadata["variant"],
            "worker_bundle_sha256": request.metadata["worker_bundle_sha256"],
            "candidates": [],
        }
        if self.invalidity == "missing-bundle-hash":
            payload.pop("worker_bundle_sha256")
        elif self.invalidity == "bundle-hash-mismatch":
            payload["worker_bundle_sha256"] = "0" * 64
        elif self.invalidity == "family-mismatch":
            payload["family"] = "B"
        elif self.invalidity == "variant-mismatch":
            payload["variant"] = "without-oracle"
        return AgentResult(success=True, final=payload)


class _CandidateBackend:
    def __init__(self) -> None:
        self.requests: list[AgentRequest] = []

    async def run(self, request: AgentRequest) -> AgentResult:
        self.requests.append(request)
        source = (request.cwd / "compiler.c").read_text(encoding="utf-8")
        return AgentResult(
            success=True,
            final={
                "family": request.metadata["family"],
                "variant": request.metadata["variant"],
                "worker_bundle_sha256": request.metadata["worker_bundle_sha256"],
                "candidates": [
                    {
                        "toolchain": "gcc",
                        "toolchain_version": "fixture",
                        "mechanism": "stack-protector",
                        "isa": ["x86_64"],
                        "invariant_violated": "A concrete invariant.",
                        "evidence_file_line": ["compiler.c:1"],
                        "evidence_code": source,
                        "minimal_trigger": {
                            "source": "int main(void) { return 0; }",
                            "flags": "-O2",
                            "isa": "x86_64",
                        },
                        "impact": "A required check is skipped.",
                        "why_not_rescued": "No later layer restores it.",
                        "poc_verified": True,
                        "discovered": "2026-08-26",
                    }
                ],
            },
        )


@pytest.mark.asyncio
async def test_full_runner_fails_without_online_oracle_command(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    backend = _FakeBackend()

    result = await run(
        ExperimentPlan(
            run_id="full-without-oracle-command",
            experiment="agent-audit",
            variant="full",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
            },
        ),
        1,
        tmp_path / "missing-oracle-run",
        backend,
    )

    assert not result.success
    assert result.status == "failed"
    assert "requires at least one online_oracle_command" in result.error
    assert backend.requests == []


@pytest.mark.asyncio
async def test_full_runner_fails_closed_on_online_oracle_error(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text(
        "one\ntwo\nthree\nfour\nfive\n", encoding="utf-8"
    )
    backend = _CandidateBackend()
    output = tmp_path / "oracle-error-run"

    result = await run(
        ExperimentPlan(
            run_id="full-oracle-error",
            experiment="agent-audit",
            variant="full",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "online_oracle_command": _ERROR_ONLINE_ORACLE_COMMAND,
            },
        ),
        1,
        output,
        backend,
    )

    assert not result.success
    assert result.status == "failed"
    assert len(backend.requests) == 1
    assert result.metrics["candidate_admitted"] == 1
    assert result.metrics["online_oracle_calls"] == 1
    assert result.metrics["online_oracle_errors"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["online_oracle"]["operational"] is False
    assert summary["online_oracle"]["error_count"] == 1
    assert summary["online_oracle"]["records"][0]["verdict"] == "ERROR"
    assert "checker unavailable" in summary["errors"][0]


@pytest.mark.asyncio
async def test_without_oracle_succeeds_without_command_and_runs_only_initial_worker(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text(
        "one\ntwo\nthree\nfour\nfive\n", encoding="utf-8"
    )
    backend = _CandidateBackend()
    output = tmp_path / "without-oracle-run"

    result = await run(
        ExperimentPlan(
            run_id="without-oracle",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
            },
        ),
        1,
        output,
        backend,
    )

    assert result.success
    assert len(backend.requests) == 1
    assert backend.requests[0].metadata["variant"] == "without-oracle"
    assert result.metrics["online_oracle_calls"] == 0
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["online_oracle"] == {
        "enabled": False,
        "rounds_configured": 0,
        "records": [],
        "error_count": 0,
        "operational": True,
    }


@pytest.mark.asyncio
async def test_runner_uses_fake_backend_in_canonical_order_without_archive(tmp_path: Path) -> None:
    root = _reference(tmp_path)
    source = _source(tmp_path)
    backend = _FakeBackend()
    output = tmp_path / "run"
    plan = ExperimentPlan(
        run_id="audit-1",
        experiment="agent-audit",
        variant="full",
        source_root=source,
        parameters={
            "reference_root": str(root),
            "compiler": "gcc",
            "online_oracle_command": _UNUSED_ONLINE_ORACLE_COMMAND,
        },
    )

    result = await run(plan, 1, output, backend)

    assert result.success
    assert [request.metadata["family"] for request in backend.requests] == list("ABCDE")
    assert all(request.writable is False for request in backend.requests)
    assert len({request.cwd for request in backend.requests}) == 1
    workspace = backend.requests[0].cwd
    assert workspace != source
    assert not workspace.exists()
    assert all(files == {"compiler.c"} for files in backend.workspace_files)
    assert all(backend.workspace_read_only)
    assert all(
        request.metadata["isolation_level"] == "workspace-copy"
        for request in backend.requests
    )
    assert all("PRIVATE_FINDING_SENTINEL" not in request.prompt for request in backend.requests)
    assert all(str(root) not in request.prompt for request in backend.requests)
    assert all(str(source) not in request.prompt for request in backend.requests)
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["family_order"] == list("ABCDE")
    assert summary["archive_performed"] is False
    assert summary["isolation_level"] == "workspace-copy"
    assert not (root / "findings" / "DREV-2099-999" / "timeline.md").exists()


@pytest.mark.asyncio
async def test_runner_rejects_tainted_backend_candidate(tmp_path: Path) -> None:
    root = _reference(tmp_path)
    source = _source(tmp_path)
    output = tmp_path / "run"
    plan = ExperimentPlan(
        run_id="tainted",
        experiment="agent-audit",
        variant="without-oracle",
        source_root=source,
        parameters={
            "reference_root": str(root),
            "compiler": "gcc",
            "families": ["A"],
        },
    )

    result = await run(plan, 1, output, _TaintedBackend())

    assert not result.success
    assert result.status == "failed"
    assert result.metrics["valid_workers"] == 0
    assert result.metrics["invalid_workers"] == 1
    assert result.metrics["candidate_admitted"] == 0
    assert result.metrics["candidate_rejected"] == 0
    report = json.loads(
        (output / "worker-a-initial" / "audit-report.json").read_text()
    )
    assert report["tainted"] is True
    admission = json.loads(
        (output / "worker-a-initial" / "admission.json").read_text()
    )
    assert admission["admitted"] == []
    assert admission["candidate_admission"] == {
        "scope": "structural-completeness-only",
        "deterministic_poc_validator_executed": False,
    }


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "invalidity",
    [
        "parse-issue",
        "missing-bundle-hash",
        "bundle-hash-mismatch",
        "family-mismatch",
        "variant-mismatch",
    ],
)
async def test_any_invalid_worker_output_fails_stage(
    tmp_path: Path, invalidity: str
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    output = tmp_path / "invalid-run"
    result = await run(
        ExperimentPlan(
            run_id=f"invalid-{invalidity}",
            experiment="agent-audit",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "online_oracle_command": _UNUSED_ONLINE_ORACLE_COMMAND,
            },
        ),
        1,
        output,
        _InvalidWorkerBackend(invalidity),
    )

    assert not result.success
    assert result.status == "failed"
    assert result.metrics["valid_workers"] == 0
    assert result.metrics["invalid_workers"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["worker_validity"][0]["valid"] is False
    assert summary["worker_validity"][0]["invalid_reasons"]
    admission = json.loads(
        (output / "worker-a-initial" / "admission.json").read_text()
    )
    assert admission["worker_valid"] is False


@pytest.mark.asyncio
async def test_valid_worker_requires_bundle_hash_echo_in_schema_and_metadata(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    output = tmp_path / "valid-run"
    result = await run(
        ExperimentPlan(
            run_id="valid-worker",
            experiment="agent-audit",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "online_oracle_command": _UNUSED_ONLINE_ORACLE_COMMAND,
            },
        ),
        1,
        output,
        _FakeBackend(),
    )

    assert result.success
    schema = json.loads((output / "audit-report.schema.json").read_text())
    assert "worker_bundle_sha256" in schema["required"]
    assert result.metadata["candidate_admission"] == {
        "scope": "structural-completeness-only",
        "deterministic_poc_validator_executed": False,
    }
    assert "confirmed" not in result.metrics


@pytest.mark.asyncio
async def test_structural_candidate_stays_unverified_without_verification_command(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text(
        "one\ntwo\nthree\nfour\nfive\n", encoding="utf-8"
    )
    output = tmp_path / "candidate-run"

    result = await run(
        ExperimentPlan(
            run_id="candidate",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
            },
        ),
        1,
        output,
        _CandidateBackend(),
    )

    assert result.success
    assert result.metrics["candidate_admitted"] == 1
    assert result.metrics["candidate_verified"] == 0
    assert result.metrics["candidate_unverified"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["verified_candidates"] == []
    assert summary["candidate_verification"][0]["status"] == "unverified"


@pytest.mark.asyncio
async def test_bare_runner_is_one_neutral_worker_and_can_keep_workspace(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    full_backend = _FakeBackend()
    full_result = await run(
        ExperimentPlan(
            run_id="full-for-parity",
            experiment="agent-audit",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "online_oracle_command": _UNUSED_ONLINE_ORACLE_COMMAND,
            },
        ),
        1,
        tmp_path / "full-run",
        full_backend,
    )
    backend = _FakeBackend()
    result = await run(
        ExperimentPlan(
            run_id="bare",
            experiment="agent-audit",
            variant="bare-agent",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "mechanisms": ["stack-protector"],
                "families": ["A"],
                "keep_workspace": True,
            },
        ),
        1,
        tmp_path / "bare-run",
        backend,
    )

    assert result.success
    assert len(backend.requests) == 1
    request = backend.requests[0]
    assert request.metadata["family"] == "neutral"
    assert "FAMILY_A_ONLY" not in request.prompt
    assert "stack-protector" not in request.prompt
    assert "AuditReport" not in request.prompt
    assert request.cwd.is_dir()
    assert result.metadata["workspace_path"] == str(request.cwd)
    assert result.metadata["workspace_kept"] is True
    assert result.metadata["host_absolute_path_read_isolation"] is False
    assert full_result.metadata["workspace_sha256"] == result.metadata["workspace_sha256"]
    request.cwd.chmod(request.cwd.stat().st_mode | stat.S_IRWXU)
    for path in request.cwd.rglob("*"):
        path.chmod(path.stat().st_mode | stat.S_IRWXU)
    shutil.rmtree(request.cwd)
