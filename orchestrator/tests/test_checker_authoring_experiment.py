from __future__ import annotations

import errno
import hashlib
import inspect
import json
import re
import subprocess
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest

import defuzz_loop.experiment_engine.checker_authoring as checker_authoring
from defuzz_loop.checker_bundle import load_checker_bundle
from defuzz_loop.experiment_engine import AgentResult, ExperimentPlan
from defuzz_loop.experiment_engine.checker_authoring import (
    CHECKER_INPUT_SCOPE_FILENAME,
    RESULTS_FILENAME,
    CheckerAuthoringRunner,
    CommandResult,
    load_accepted_invariants,
    render_checker_prompt,
    run,
)
from defuzz_loop.token_usage import (
    TokenUsageContext,
    TokenUsageSink,
    read_jsonl,
    use_token_usage,
)


def _git(*argv: str, cwd: Path) -> str:
    completed = subprocess.run(("git", *argv), cwd=cwd, check=True, capture_output=True, text=True)
    return completed.stdout.strip()


@pytest.fixture
def source_checkout(tmp_path: Path) -> Path:
    root = tmp_path / "source"
    oracle = root / "core" / "internal" / "oracle"
    oracle.mkdir(parents=True)
    (root / "core" / "go.mod").write_text(
        "module example.test/defuzz\n\ngo 1.23\n", encoding="utf-8"
    )
    (oracle / "invariant.go").write_text(
        "package oracle\n\ntype InvariantChecker interface { ID() string }\n",
        encoding="utf-8",
    )
    (oracle / "metadata.go").write_text(
        "package oracle\n\nvar checkerMetadata = map[string]string{}\n",
        encoding="utf-8",
    )
    _git("init", cwd=root)
    _git("config", "user.email", "test@example.invalid", cwd=root)
    _git("config", "user.name", "Test", cwd=root)
    _git("add", ".", cwd=root)
    _git("commit", "-m", "fixture", cwd=root)
    return root


def _write_invariants(path: Path, invariant_ids: tuple[str, ...] = ("INV-TEST-001",)) -> None:
    rows = [
        {
            "schema_version": 1,
            "invariant_id": invariant_id,
            "statement": "the emitted guard is checked before return",
            "observation": "missing guard comparison permits a bypass",
            "generation_path": "combined",
            "provenance": [{"source_id": "DREV-TEST"}],
            "compiler": "gcc",
            "version": "15",
            "target": ["aarch64"],
            "mechanism": "canary",
            "source_kind": "bug-report",
            "source_url_or_path": "fixture://DREV-TEST",
            "evidence_snippet": "compare guard value",
            "falsifiability": "compile and inspect epilogue",
            "grounding": "accepted",
            "novelty": "cross-target",
        }
        for invariant_id in invariant_ids
    ]
    path.write_text(
        "".join(json.dumps(row, ensure_ascii=False) + "\n" for row in rows),
        encoding="utf-8",
    )


class FakeWorkspaceFactory:
    def __init__(self) -> None:
        self.roots: list[Path] = []

    def create(
        self, *, source_root: Path, destination: Path, invariant: dict[str, Any]
    ) -> SimpleNamespace:
        del invariant
        self.roots.append(destination)
        subprocess.run(
            (
                "git",
                "clone",
                "--quiet",
                "--no-hardlinks",
                str(source_root),
                str(destination),
            ),
            check=True,
        )
        return SimpleNamespace(root=destination)


class FakeCommandExecutor:
    def __init__(self, outcomes: list[bool], *, build_outcome: bool = True) -> None:
        self.outcomes = outcomes
        self.build_outcome = build_outcome
        self.calls: list[tuple[tuple[str, ...], Path]] = []
        self.round = 0

    async def run(
        self, command: tuple[str, ...], *, cwd: Path, timeout_seconds: float | None = None
    ) -> CommandResult:
        del timeout_seconds
        self.calls.append((tuple(command), cwd))
        if len(command) >= 2 and command[:2] == ("go", "build"):
            if self.build_outcome and "-o" in command:
                output = Path(command[command.index("-o") + 1])
                output.parent.mkdir(parents=True, exist_ok=True)
                output.write_bytes(b"fixture dispatcher\n")
            return CommandResult(
                argv=tuple(command),
                cwd=str(cwd),
                exit_code=0 if self.build_outcome else 1,
                stderr="fixture build failure" if not self.build_outcome else "",
            )
        if len(command) >= 3 and command[-2:] == ("--mode", "catalog"):
            invariant_ids: set[str] = set()
            for path in (cwd / "internal" / "oracle").glob("checker_*.go"):
                invariant_ids.update(
                    match.group(0)
                    for match in re.finditer(r"INV-[A-Z0-9-]+", path.read_text(encoding="utf-8"))
                )
            payload = {
                "schema_version": 1,
                "kind": "defuzz-checker-catalog",
                "checkers": [
                    {
                        "id": invariant_id,
                        "oracle": "canary",
                        "mechanism": "canary",
                        "requires": [],
                        "applicable_isas": ["aarch64"],
                        "mode": "single",
                        "cost": "cheap",
                        "category": "static",
                    }
                    for invariant_id in sorted(invariant_ids)
                ],
            }
            return CommandResult(
                argv=tuple(command),
                cwd=str(cwd),
                exit_code=0,
                stdout=json.dumps(payload),
            )
        # Three default commands make one validation round.
        outcome = self.outcomes[min(self.round // 3, len(self.outcomes) - 1)]
        self.round += 1
        return CommandResult(
            argv=tuple(command),
            cwd=str(cwd),
            exit_code=0 if outcome else 1,
            stderr="fixture failure" if not outcome else "",
        )


class FakeAgentBackend:
    provider = "fake"
    model = "fake-model"

    def __init__(
        self,
        *,
        write_outside: bool = False,
        workspace_factory: FakeWorkspaceFactory | None = None,
        command_executor: FakeCommandExecutor | None = None,
    ) -> None:
        self.requests: list[Any] = []
        self.write_outside = write_outside
        self.workspace_factory = workspace_factory
        self.command_executor = command_executor

    async def run(self, request: Any) -> AgentResult:
        self.requests.append(request)
        oracle = request.cwd / "core" / "internal" / "oracle"
        invariant_id = request.metadata["invariant_id"]
        checker_name = (
            "checker_test_001.go"
            if invariant_id == "INV-TEST-001"
            else f"checker_{invariant_id.lower().replace('-', '_')}.go"
        )
        checker = oracle / checker_name
        checker.write_text(
            f"package oracle\n\ntype Test001Checker struct{{}}\n// {invariant_id}\n",
            encoding="utf-8",
        )
        if len(self.requests) == 1:
            checker.write_text(checker.read_text(encoding="utf-8") + "// first\n")
        else:
            checker.write_text(checker.read_text(encoding="utf-8") + "// repaired\n")
        if self.write_outside:
            (request.cwd / "README.md").write_text("forbidden\n", encoding="utf-8")
        return AgentResult(
            success=True,
            final={"summary": "implemented"},
            usage={"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
        )


class RecordingFakeAgentBackend(FakeAgentBackend):
    async def run(self, request: Any) -> AgentResult:
        result = await super().run(request)
        request.token_sink.record_external_usage(
            {
                "type": "turn.completed",
                "usage": result.usage,
            },
            context=request.token_sink.context.with_overrides(stage=request.metadata["stage"]),
        )
        return result


class CumulativeAgentBackend:
    provider = "fake"
    model = "fake-model"

    def __init__(
        self,
        *,
        corrupt_owner_for: str | None = None,
        expected_files: dict[str, set[str]] | None = None,
    ) -> None:
        self.requests: list[Any] = []
        self.corrupt_owner_for = corrupt_owner_for
        self.expected_files = expected_files or {}

    async def run(self, request: Any) -> AgentResult:
        self.requests.append(request)
        invariant_id = request.metadata["invariant_id"]
        oracle = request.cwd / "core" / "internal" / "oracle"
        present = {path.name for path in oracle.glob("checker_generated_*.go")}
        assert present == self.expected_files.get(invariant_id, present)
        suffix = invariant_id.rsplit("-", 1)[-1].lower()
        (oracle / f"checker_generated_{suffix}.go").write_text(
            f"package oracle\n\n// {invariant_id}\n", encoding="utf-8"
        )
        metadata = oracle / "metadata.go"
        metadata.write_text(
            metadata.read_text(encoding="utf-8") + f"// metadata {invariant_id}\n",
            encoding="utf-8",
        )
        if invariant_id == self.corrupt_owner_for:
            (oracle / "checker_generated_a.go").write_text(
                "package oracle\n\n// corrupted by later invariant\n",
                encoding="utf-8",
            )
        return AgentResult(
            success=True,
            final={"summary": f"implemented {invariant_id}"},
            usage={"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
        )


class SharedIntegrationAgentBackend(CumulativeAgentBackend):
    async def run(self, request: Any) -> AgentResult:
        result = await super().run(request)
        registry = request.cwd / "core" / "internal" / "oracle" / "canary_oracle.go"
        registry.write_text(
            registry.read_text(encoding="utf-8")
            + f"// register {request.metadata['invariant_id']}\n",
            encoding="utf-8",
        )
        return result


class RecordingIsolationCheckerBackend(FakeAgentBackend):
    supports_host_read_isolation = True

    def __init__(self) -> None:
        super().__init__()
        self.deny_read_paths: list[list[Path]] = []
        self.host_isolation: list[bool] = []

    async def run(self, request: Any) -> AgentResult:
        self.deny_read_paths.append(list(request.deny_read_paths))
        self.host_isolation.append(bool(request.require_host_read_isolation))
        return await super().run(request)


def _plan(
    source: Path,
    invariant_path: Path,
    *,
    max_attempts: int = 2,
    token_budget: int = 100_000,
    mechanisms: tuple[str, ...] | None = None,
    isas: tuple[str, ...] | None = None,
) -> ExperimentPlan:
    parameters: dict[str, Any] = {
        "accepted_invariants": str(invariant_path),
        "max_attempts": max_attempts,
    }
    if mechanisms is not None:
        parameters["mechanisms"] = list(mechanisms)
    if isas is not None:
        parameters["isas"] = list(isas)
    return ExperimentPlan.from_dict(
        {
            "run_id": "checker-run",
            "experiment": "checker-authoring",
            "source_root": str(source),
            "budget": {"token_budget": token_budget},
            "parameters": parameters,
        }
    )


def test_normalizes_input_and_renders_current_checker_contract(tmp_path: Path) -> None:
    source = tmp_path / "accepted-invariants.jsonl"
    source.write_text(
        json.dumps(
            {
                "id": "INV-ALIAS",
                "claim": "a falsifiable claim",
                "evidence": "an observation",
                "isa": "riscv64",
                "provenance": {"source_id": "one"},
            }
        )
        + "\n",
        encoding="utf-8",
    )

    item = load_accepted_invariants(source)[0]
    prompt = render_checker_prompt(item)

    assert item.invariant_id == "INV-ALIAS"
    assert item.value["statement"] == "a falsifiable claim"
    assert item.value["target"] == "riscv64"
    assert item.value["provenance"] == [{"source_id": "one"}]
    assert "InvariantChecker" in prompt
    assert "metadata.go" in prompt
    assert "mechanism().Checkers" in prompt
    for required in ("Pass", "Fail", "NotApplicable", "Error", "nil"):
        assert required in prompt


@pytest.mark.parametrize(
    ("target", "requested", "expected"),
    [
        ("", ("x86_64",), True),
        ("Linux", ("x86_64",), True),
        ("Android", ("aarch64",), True),
        ("amd64", ("x86_64",), True),
        ("x86_64, aarch64", ("aarch64",), True),
        ("x86_64 / aarch64", ("aarch64",), True),
        ("x86", ("i386",), True),
        ("x86", ("x86_64",), True),
        ("i386", ("x86_64",), False),
        ("RISC-V", ("riscv32",), True),
        ("RISC-V", ("riscv64",), True),
        ("riscv32", ("riscv64",), False),
        ("riscv64gc", ("riscv64",), True),
        ("riscv64gc", ("x86_64",), False),
        ("arm64", ("aarch64",), True),
        ("aarch64", ("arm64",), True),
        ("arm", ("aarch64",), False),
        ("powerpc64le", ("x86_64",), False),
        ("config-smoke", ("fixture",), True),
        ("unknown-architecture", ("x86_64",), False),
    ],
)
def test_target_isa_scope_compatibility(
    target: str, requested: tuple[str, ...], expected: bool
) -> None:
    normalized = tuple(checker_authoring.normalize_isa(value) for value in requested)
    assert checker_authoring._target_matches_isa_scope(target, normalized) is expected


def test_scope_accepts_singular_mechanism_compatibility_alias(tmp_path: Path) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)

    selected, report = checker_authoring._project_input_scope(
        load_accepted_invariants(invariants),
        parameters={
            "mechanisms": [],
            "mechanism": "SSP",
            "isas": (),
            "isa": "arm64",
        },
        source_path=invariants.resolve(),
    )

    assert [item.invariant_id for item in selected] == ["INV-TEST-001"]
    assert report["requested"]["mechanisms"] == ["stack-protector"]
    assert report["requested"]["isas"] == ["aarch64"]


@pytest.mark.asyncio
async def test_scope_filters_before_backend_and_records_normalized_projection(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    rows = [
        {
            "invariant_id": "INV-OUT-ISA",
            "statement": "only the 32-bit x86 target is supported",
            "mechanism": "stack-canary",
            "target": "i386",
        },
        {
            "invariant_id": "INV-IN",
            "statement": "the guard is checked on x86 targets",
            "mechanism": "stack-protector",
            "target": "x86",
        },
        {
            "invariant_id": "INV-OUT-MECHANISM",
            "statement": "indirect branches begin with ENDBR",
            "mechanism": "CET / IBT",
            "target": "amd64",
        },
    ]
    invariants.write_text(
        "".join(json.dumps(row) + "\n" for row in rows), encoding="utf-8"
    )
    source_hash = hashlib.sha256(invariants.read_bytes()).hexdigest()
    backend = CumulativeAgentBackend()
    runner = CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=FakeCommandExecutor([True]),
    )

    result = await runner.run(
        _plan(
            source_checkout,
            invariants,
            max_attempts=1,
            mechanisms=("SSP",),
            isas=("x86-64",),
        ),
        1,
        tmp_path / "output",
    )

    assert result.success
    assert [request.metadata["invariant_id"] for request in backend.requests] == [
        "INV-IN"
    ]
    assert result.metrics["invariants"] == 1
    assert result.metrics["input_invariants"] == 3
    assert result.metrics["selected_invariants"] == 1
    assert result.metrics["excluded_invariants"] == 2
    assert result.metadata["requested_mechanisms"] == ["stack-protector"]
    assert result.metadata["requested_isas"] == ["x86_64"]
    assert result.metadata["accepted_invariants_sha256"] == source_hash
    assert any(
        artifact.path == CHECKER_INPUT_SCOPE_FILENAME
        and artifact.kind == "checker-input-scope"
        for artifact in result.artifacts
    )

    report = json.loads(
        (tmp_path / "output" / CHECKER_INPUT_SCOPE_FILENAME).read_text(encoding="utf-8")
    )
    assert report["source_artifact"]["path"] == str(invariants.resolve())
    assert report["source_artifact"]["sha256"] == source_hash
    assert report["requested"] == {
        "mechanisms": ["stack-protector"],
        "isas": ["x86_64"],
    }
    assert report["counts"] == {"total": 3, "selected": 1, "excluded": 2}
    assert report["total_invariant_ids"] == [
        "INV-IN",
        "INV-OUT-ISA",
        "INV-OUT-MECHANISM",
    ]
    assert report["selected_invariant_ids"] == ["INV-IN"]
    assert report["excluded_invariant_ids"] == [
        "INV-OUT-ISA",
        "INV-OUT-MECHANISM",
    ]
    assert {
        item["invariant_id"]: item["reasons"]
        for item in report["excluded_invariants"]
    } == {
        "INV-OUT-ISA": ["isa_out_of_scope"],
        "INV-OUT-MECHANISM": ["mechanism_out_of_scope"],
    }


@pytest.mark.asyncio
async def test_nonempty_scope_with_zero_selected_fails_before_workspace_or_backend(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    workspace_factory = FakeWorkspaceFactory()
    backend = FakeAgentBackend()
    output = tmp_path / "output"

    result = await CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=workspace_factory,
        command_executor=FakeCommandExecutor([True]),
    ).run(
        _plan(source_checkout, invariants, mechanisms=("ibt",)),
        1,
        output,
    )

    assert result.status == "failed"
    assert result.error == "checker input scope selected no accepted invariants"
    assert backend.requests == []
    assert workspace_factory.roots == []
    assert result.metrics["input_invariants"] == 1
    assert result.metrics["selected_invariants"] == 0
    assert result.metrics["excluded_invariants"] == 1
    assert any(
        artifact.path == CHECKER_INPUT_SCOPE_FILENAME for artifact in result.artifacts
    )
    report = json.loads((output / CHECKER_INPUT_SCOPE_FILENAME).read_text())
    assert report["selected_invariant_ids"] == []
    assert report["excluded_invariants"] == [
        {
            "invariant_id": "INV-TEST-001",
            "normalized_mechanism": "stack-protector",
            "reasons": ["mechanism_out_of_scope"],
            "target": ["aarch64"],
            "target_isas": ["aarch64"],
        }
    ]


@pytest.mark.asyncio
async def test_runner_rejects_frozen_input_hash_mismatch_before_backend(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    backend = FakeAgentBackend()
    base_plan = _plan(source_checkout, invariants)
    plan = base_plan.model_copy(
        update={
            "parameters": {
                **base_plan.parameters,
                "accepted_invariants_sha256": "0" * 64,
            }
        }
    )

    with pytest.raises(ValueError, match="accepted_invariants SHA-256 mismatch"):
        await CheckerAuthoringRunner(
            backend=backend,
            workspace_factory=FakeWorkspaceFactory(),
            command_executor=FakeCommandExecutor([True]),
        ).run(plan, 1, tmp_path / "output")

    assert backend.requests == []


@pytest.mark.asyncio
async def test_runner_isolates_each_invariant_and_records_repair_lineage(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    output = tmp_path / "output"
    source_before = _git("status", "--porcelain=v1", cwd=source_checkout)
    source_digest = hashlib.sha256(
        (source_checkout / "core/internal/oracle/invariant.go").read_bytes()
    ).hexdigest()
    workspace_factory = FakeWorkspaceFactory()
    backend = FakeAgentBackend()
    validator = FakeCommandExecutor([False, True])
    runner = CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=workspace_factory,
        command_executor=validator,
    )

    result = await runner.run(_plan(source_checkout, invariants), 1, output)

    assert result.success
    assert result.metrics["invariants"] == 1
    assert result.metrics["first_passed"] == 0
    assert result.metrics["final_passed"] == 1
    assert result.metrics["agent_attempts"] == 2
    assert result.metrics["input_invariants"] == 1
    assert result.metrics["selected_invariants"] == 1
    assert result.metrics["excluded_invariants"] == 0
    assert len(backend.requests) == 2
    assert backend.requests[0].writable is True
    assert backend.requests[0].cwd != source_checkout
    assert backend.requests[0].cwd == backend.requests[1].cwd
    assert all(request.cwd == workspace_factory.roots[0] for request in backend.requests)
    assert all(request.cwd not in source_checkout.parents for request in backend.requests)
    assert not workspace_factory.roots[0].exists()

    row = json.loads((output / RESULTS_FILENAME).read_text(encoding="utf-8"))
    assert row["invariant_id"] == "INV-TEST-001"
    assert row["lineage"]["source_line"] == 1
    assert row["lineage"]["generation_path"] == "combined"
    assert row["lineage"]["provenance"] == [{"source_id": "DREV-TEST"}]
    assert row["first_pass_status"] == "failed"
    assert row["final_status"] == "passed"
    assert row["attempt_count"] == row["attempt_cap"] == 2
    assert row["files"] == ["core/internal/oracle/checker_test_001.go"]
    assert len(row["token_refs"]) == 2
    assert all(ref["path"] == "token_usage.jsonl" for ref in row["token_refs"])
    assert row["first_patch"]["path"].endswith("first.patch")
    assert row["final_patch"]["path"].endswith("final.patch")
    scope = json.loads((output / CHECKER_INPUT_SCOPE_FILENAME).read_text())
    assert scope["scope_requested"] is False
    assert scope["selected_invariant_ids"] == ["INV-TEST-001"]
    assert scope["excluded_invariant_ids"] == []
    first_patch = output / row["first_patch"]["path"]
    final_patch = output / row["final_patch"]["path"]
    assert "// first" in first_patch.read_text(encoding="utf-8")
    assert "// repaired" in final_patch.read_text(encoding="utf-8")
    assert _git("status", "--porcelain=v1", cwd=source_checkout) == source_before
    assert (
        hashlib.sha256(
            (source_checkout / "core/internal/oracle/invariant.go").read_bytes()
        ).hexdigest()
        == source_digest
    )


@pytest.mark.asyncio
async def test_public_run_enforces_attempt_cap_and_rejects_out_of_scope_patch(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    output = tmp_path / "output"
    backend = FakeAgentBackend(
        write_outside=True,
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=FakeCommandExecutor([True]),
    )

    result = await run(
        _plan(source_checkout, invariants, max_attempts=1),
        1,
        output,
        backend=backend,
    )

    assert not result.success
    assert result.status == "failed"
    assert result.metrics["failed"] == 1
    assert result.metrics["budget_exhausted"] == 0
    row = json.loads((output / RESULTS_FILENAME).read_text(encoding="utf-8"))
    assert row["attempt_count"] == 1
    assert row["first_pass_status"] == "failed"
    assert row["final_status"] == "failed"
    assert row["files"] == [
        "README.md",
        "core/internal/oracle/checker_test_001.go",
    ]
    assert row["attempts"][0]["policy_errors"] == [
        {
            "type": "disallowed-file",
            "path": "README.md",
            "message": "checker authoring may only change configured checker paths",
        }
    ]


@pytest.mark.asyncio
async def test_token_budget_stops_before_next_agent_attempt(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    output = tmp_path / "output"
    workspace_factory = FakeWorkspaceFactory()
    backend = FakeAgentBackend()
    runner = CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=workspace_factory,
        command_executor=FakeCommandExecutor([False, True]),
    )

    result = await runner.run(
        _plan(source_checkout, invariants, max_attempts=3, token_budget=15),
        1,
        output,
    )

    assert not result.success
    assert result.status == "failed"
    assert result.metrics["failed"] == 1
    assert result.metrics["budget_exhausted"] == 1
    assert result.metrics["unprocessed"] == 0
    assert len(backend.requests) == 1
    row = json.loads((output / RESULTS_FILENAME).read_text(encoding="utf-8"))
    assert row["attempt_count"] == 1
    assert row["attempt_cap"] == 3
    assert row["final_status"] == "failed"
    assert row["budget_exhausted"] is True
    assert row["stopped_reason"] == "token budget exceeded: consumed 15 of 15 tokens"
    assert (output / "token_usage.jsonl").is_file()
    assert (output / "token_usage_summary.json").is_file()
    assert (output / "token_usage_summary.csv").is_file()


@pytest.mark.asyncio
async def test_mixed_outcomes_return_partial(source_checkout: Path, tmp_path: Path) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants, ("INV-PASS", "INV-FAIL"))
    output = tmp_path / "output"
    runner = CheckerAuthoringRunner(
        backend=FakeAgentBackend(),
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=FakeCommandExecutor([True, False, True]),
    )

    result = await runner.run(_plan(source_checkout, invariants, max_attempts=1), 1, output)

    assert result.success
    assert result.status == "completed"
    assert result.metrics["final_passed"] == 1
    assert result.metrics["failed"] == 1
    assert result.metrics["budget_exhausted"] == 0


@pytest.mark.asyncio
async def test_reused_checker_is_source_catalog_validated_without_agent_edits(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    row = {
        "invariant_id": "INVGEN-P01",
        "statement": "Indirect-callable function entries must begin with ENDBR.",
        "observation": "missing entry marker",
        "generation_path": "combined",
        "provenance": [],
        "reused_checker_ids": ["INV-IBT-P01"],
    }
    invariants.write_text(json.dumps(row) + "\n", encoding="utf-8")
    backend = FakeAgentBackend()
    executor = FakeCommandExecutor([True])
    # The source-derived dispatcher catalog is the validation authority; this
    # avoids treating a text grep as proof that the checker is runnable.
    original_run = executor.run

    async def catalog_with_p01(
        command: tuple[str, ...],
        *,
        cwd: Path,
        timeout_seconds: float | None = None,
    ) -> CommandResult:
        if len(command) >= 3 and command[-2:] == ("--mode", "catalog"):
            return CommandResult(
                argv=command,
                cwd=str(cwd),
                exit_code=0,
                stdout=json.dumps({
                    "schema_version": 1,
                    "kind": "defuzz-checker-catalog",
                    "checkers": [{"id": "INV-IBT-P01", "requires": []}],
                }),
            )
        return await original_run(command, cwd=cwd, timeout_seconds=timeout_seconds)

    executor.run = catalog_with_p01  # type: ignore[method-assign]
    result = await CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=executor,
    ).run(_plan(source_checkout, invariants), 1, tmp_path / "output")

    assert result.status == "completed"
    assert backend.requests == []
    assert result.metadata["deterministic_only"] is True
    row = json.loads((tmp_path / "output" / RESULTS_FILENAME).read_text())
    assert row["reused"] is True
    assert row["reused_checker_id"] == "INV-IBT-P01"
    catalog = json.loads((tmp_path / "output" / "checker-catalog.json").read_text())
    assert catalog["checkers"][0]["id"] == "INV-IBT-P01"
    assert catalog["checkers"][0]["reused_checker_id"] == "INV-IBT-P01"
    assert catalog["checkers"][0]["checker_id"] == "INV-IBT-P01"


@pytest.mark.asyncio
async def test_multiple_generated_invariants_share_one_reused_runtime_checker(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    invariants.write_text(
        "".join(
            json.dumps(
                {
                    "invariant_id": generated_id,
                    "statement": statement,
                    "observation": "missing entry marker",
                    "generation_path": "combined",
                    "provenance": [],
                    "reused_checker_ids": ["INV-IBT-P01"],
                }
            )
            + "\n"
            for generated_id, statement in (
                (
                    "INVGEN-P01-A",
                    "Every indirect-callable function entry must begin with ENDBR.",
                ),
                (
                    "INVGEN-P01-B",
                    "Indirect-callable function entries must begin with ENDBR.",
                ),
            )
        ),
        encoding="utf-8",
    )
    executor = FakeCommandExecutor([True])
    original_run = executor.run

    async def catalog_with_p01(
        command: tuple[str, ...],
        *,
        cwd: Path,
        timeout_seconds: float | None = None,
    ) -> CommandResult:
        if len(command) >= 3 and command[-2:] == ("--mode", "catalog"):
            return CommandResult(
                argv=command,
                cwd=str(cwd),
                exit_code=0,
                stdout=json.dumps(
                    {
                        "schema_version": 1,
                        "kind": "defuzz-checker-catalog",
                        "checkers": [{"id": "INV-IBT-P01", "requires": []}],
                    }
                ),
            )
        return await original_run(command, cwd=cwd, timeout_seconds=timeout_seconds)

    executor.run = catalog_with_p01  # type: ignore[method-assign]
    backend = FakeAgentBackend()
    output = tmp_path / "output"
    result = await CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=executor,
    ).run(_plan(source_checkout, invariants), 1, output)

    assert result.success
    assert backend.requests == []
    manifest = json.loads((output / "checker-bundle-manifest.json").read_text())
    assert manifest["included_invariant_ids"] == ["INV-IBT-P01"]
    assert len(manifest["invariants"]) == 1
    assert manifest["invariants"][0]["generated_invariant_ids"] == [
        "INVGEN-P01-A",
        "INVGEN-P01-B",
    ]
    catalog = json.loads((output / "checker-catalog.json").read_text())
    assert len(catalog["checkers"]) == 1
    assert catalog["checkers"][0]["generated_invariant_ids"] == [
        "INVGEN-P01-A",
        "INVGEN-P01-B",
    ]


@pytest.mark.asyncio
async def test_part_ii_rejects_untrusted_reuse_metadata(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    invariants.write_text(
        json.dumps(
            {
                "invariant_id": "INVGEN-NEGATED",
                "statement": "Address-taken function entries must not begin with ENDBR.",
                "observation": "unexpected entry marker",
                "generation_path": "combined",
                "provenance": [],
                "reused_checker_ids": ["INV-IBT-P01"],
            }
        )
        + "\n",
        encoding="utf-8",
    )

    with pytest.raises(ValueError, match="reuse metadata does not match"):
        await CheckerAuthoringRunner(
            backend=FakeAgentBackend(),
            workspace_factory=FakeWorkspaceFactory(),
            command_executor=FakeCommandExecutor([True]),
        ).run(_plan(source_checkout, invariants), 1, tmp_path / "output")


@pytest.mark.asyncio
async def test_emits_cumulative_ready_bundle_in_deterministic_input_order(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants, ("INV-GEN-B", "INV-GEN-A"))
    output = tmp_path / "output"
    workspace_factory = FakeWorkspaceFactory()
    backend = CumulativeAgentBackend(
        expected_files={
            "INV-GEN-A": set(),
            "INV-GEN-B": {"checker_generated_a.go"},
        }
    )
    executor = FakeCommandExecutor([True, True, True])
    runner = CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=workspace_factory,
        command_executor=executor,
    )

    result = await runner.run(_plan(source_checkout, invariants, max_attempts=1), 1, output)

    assert result.success
    assert len(workspace_factory.roots) == 1
    rows = [json.loads(line) for line in (output / RESULTS_FILENAME).read_text().splitlines()]
    assert [row["invariant_id"] for row in rows] == ["INV-GEN-A", "INV-GEN-B"]
    assert rows[0]["parent_tree_sha256"] != rows[0]["result_tree_sha256"]
    assert rows[1]["parent_tree_sha256"] == rows[0]["result_tree_sha256"]
    assert rows[1]["result_tree_sha256"] != rows[1]["parent_tree_sha256"]
    assert rows[0]["included_in_bundle"] is True
    assert rows[1]["included_in_bundle"] is True

    bundle_patch = output / "checker-bundle.patch"
    patch_text = bundle_patch.read_text(encoding="utf-8")
    assert "checker_generated_a.go" in patch_text
    assert "checker_generated_b.go" in patch_text

    catalog = json.loads((output / "checker-catalog.json").read_text())
    assert [item["invariant_id"] for item in catalog["checkers"]] == [
        "INV-GEN-A",
        "INV-GEN-B",
    ]
    assert catalog["source_tree_sha256"] == rows[0]["parent_tree_sha256"]
    assert catalog["result_tree_sha256"] == rows[1]["result_tree_sha256"]

    manifest = json.loads((output / "checker-bundle-manifest.json").read_text())
    assert manifest["kind"] == "defuzz-checker-bundle"
    assert manifest["status"] == "ready"
    assert manifest["coverage_complete"] is True
    assert manifest["included_invariant_ids"] == ["INV-GEN-A", "INV-GEN-B"]
    assert manifest["failed_invariant_ids"] == []
    assert manifest["source_tree_sha256"] == rows[0]["parent_tree_sha256"]
    assert [item["invariant_id"] for item in manifest["invariants"]] == [
        "INV-GEN-A",
        "INV-GEN-B",
    ]
    assert manifest["validation"]["status"] == "passed"
    assert manifest["validation"]["build"]["status"] == "passed"
    assert manifest["artifacts"]["cumulative_patch"]["path"] == ("checker-bundle.patch")
    assert manifest["artifacts"]["catalog"]["path"] == "checker-catalog.json"
    assert manifest["artifacts"]["dispatcher"]["path"] == ("bin/defuzz-candidate-dispatcher")
    assert any(artifact.path == "checker-bundle-manifest.json" for artifact in result.artifacts)
    loaded = load_checker_bundle(output)
    assert loaded.manifest.bundle_id == manifest["bundle_id"]
    assert loaded.dispatcher == output / "bin/defuzz-candidate-dispatcher"
    build_call = next(call for call in executor.calls if call[0][:2] == ("go", "build"))
    assert build_call[0][:4] == ("go", "build", "-trimpath", "-buildvcs=false")
    assert build_call[0][-1] == "./cmd/defuzz-candidate-dispatcher"
    assert build_call[1] == workspace_factory.roots[0] / "core"


@pytest.mark.asyncio
async def test_failed_invariant_rolls_back_and_ready_bundle_records_partial_coverage(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants, ("INV-GEN-A", "INV-GEN-B", "INV-GEN-C"))
    output = tmp_path / "output"
    backend = CumulativeAgentBackend(
        expected_files={
            "INV-GEN-A": set(),
            "INV-GEN-B": {"checker_generated_a.go"},
            "INV-GEN-C": {"checker_generated_a.go"},
        }
    )
    executor = FakeCommandExecutor([True, False, True, True])
    runner = CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=executor,
    )

    result = await runner.run(_plan(source_checkout, invariants, max_attempts=1), 1, output)

    assert result.status == "completed"
    assert result.metrics["final_passed"] == 2
    assert result.metrics["failed"] == 1
    manifest = json.loads((output / "checker-bundle-manifest.json").read_text())
    assert manifest["status"] == "ready"
    assert manifest["coverage_complete"] is False
    assert manifest["included_invariant_ids"] == ["INV-GEN-A", "INV-GEN-C"]
    assert manifest["failed_invariant_ids"] == ["INV-GEN-B"]
    rows = [json.loads(line) for line in (output / RESULTS_FILENAME).read_text().splitlines()]
    assert rows[1]["included_in_bundle"] is False
    assert rows[1]["result_tree_sha256"] == rows[1]["parent_tree_sha256"]
    assert rows[2]["parent_tree_sha256"] == rows[0]["result_tree_sha256"]
    patch_text = (output / "checker-bundle.patch").read_text(encoding="utf-8")
    assert "checker_generated_a.go" in patch_text
    assert "checker_generated_b.go" not in patch_text
    assert "checker_generated_c.go" in patch_text


@pytest.mark.asyncio
async def test_later_invariant_cannot_modify_owned_checker_file(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants, ("INV-GEN-A", "INV-GEN-B"))
    output = tmp_path / "output"
    backend = CumulativeAgentBackend(
        corrupt_owner_for="INV-GEN-B",
        expected_files={
            "INV-GEN-A": set(),
            "INV-GEN-B": {"checker_generated_a.go"},
        },
    )
    runner = CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=FakeCommandExecutor([True, True, True]),
    )

    result = await runner.run(_plan(source_checkout, invariants, max_attempts=1), 1, output)

    assert result.status == "completed"
    rows = [json.loads(line) for line in (output / RESULTS_FILENAME).read_text().splitlines()]
    assert rows[1]["final_status"] == "failed"
    assert rows[1]["included_in_bundle"] is False
    assert rows[1]["attempts"][0]["policy_errors"] == [
        {
            "type": "owned-file-modified",
            "path": "core/internal/oracle/checker_generated_a.go",
            "owner_invariant_id": "INV-GEN-A",
            "message": "later invariants may not modify files owned by an accepted checker",
        }
    ]
    patch_text = (output / "checker-bundle.patch").read_text(encoding="utf-8")
    assert "corrupted by later invariant" not in patch_text
    assert "checker_generated_b.go" not in patch_text


@pytest.mark.asyncio
async def test_later_invariant_may_extend_shared_integration_files(
    source_checkout: Path, tmp_path: Path
) -> None:
    oracle = source_checkout / "core" / "internal" / "oracle"
    (oracle / "canary_oracle.go").write_text("package oracle\n", encoding="utf-8")
    _git("add", ".", cwd=source_checkout)
    _git("commit", "-m", "add integration file", cwd=source_checkout)
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants, ("INV-GEN-A", "INV-GEN-B"))
    output = tmp_path / "output"
    runner = CheckerAuthoringRunner(
        backend=SharedIntegrationAgentBackend(
            expected_files={
                "INV-GEN-A": set(),
                "INV-GEN-B": {"checker_generated_a.go"},
            }
        ),
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=FakeCommandExecutor([True, True, True]),
    )

    result = await runner.run(_plan(source_checkout, invariants, max_attempts=1), 1, output)

    assert result.success
    rows = [json.loads(line) for line in (output / RESULTS_FILENAME).read_text().splitlines()]
    assert rows[1]["attempts"][0]["policy_errors"] == []
    patch_text = (output / "checker-bundle.patch").read_text(encoding="utf-8")
    assert "register INV-GEN-A" in patch_text
    assert "register INV-GEN-B" in patch_text


@pytest.mark.asyncio
async def test_final_validation_failure_marks_bundle_incomplete(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants, ("INV-GEN-A",))
    output = tmp_path / "output"
    runner = CheckerAuthoringRunner(
        backend=CumulativeAgentBackend(),
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=FakeCommandExecutor([True, False]),
    )

    result = await runner.run(_plan(source_checkout, invariants, max_attempts=1), 1, output)

    assert result.status == "partial"
    manifest = json.loads((output / "checker-bundle-manifest.json").read_text())
    assert manifest["status"] == "incomplete"
    assert manifest["coverage_complete"] is True
    assert manifest["validation"]["status"] == "failed"
    assert manifest["validation"]["build"] is None


@pytest.mark.asyncio
async def test_ambient_sink_is_the_only_token_ledger(source_checkout: Path, tmp_path: Path) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    output = tmp_path / "rep-001" / "artifacts"
    ambient_path = output.parent / "token_usage.jsonl"
    ambient = TokenUsageSink(
        ambient_path,
        context=TokenUsageContext(
            run_id="checker-run",
            experiment="checker-authoring",
            variant="full",
            part="II",
            stage="checker-authoring",
        ),
        token_budget=100_000,
    )
    runner = CheckerAuthoringRunner(
        backend=RecordingFakeAgentBackend(),
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=FakeCommandExecutor([True]),
    )

    with use_token_usage(ambient):
        result = await runner.run(_plan(source_checkout, invariants), 1, output)

    assert result.success
    assert len(read_jsonl(ambient_path)) == 1
    assert not (output / "token_usage.jsonl").exists()
    assert not (output / "token_usage_summary.json").exists()
    assert not (output / "token_usage_summary.csv").exists()
    row = json.loads((output / RESULTS_FILENAME).read_text(encoding="utf-8"))
    assert row["token_refs"] == [
        {
            "path": "../token_usage.jsonl",
            "call_id": ambient.records[0].call_id,
            "attempt": 1,
        }
    ]
    assert all(artifact.path != "../token_usage.jsonl" for artifact in result.artifacts)


@pytest.mark.asyncio
async def test_budget_exhaustion_marks_following_invariant_unprocessed(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants, ("INV-PASS", "INV-UNPROCESSED"))
    output = tmp_path / "output"
    backend = FakeAgentBackend()
    runner = CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=FakeWorkspaceFactory(),
        command_executor=FakeCommandExecutor([True]),
    )

    result = await runner.run(
        _plan(source_checkout, invariants, max_attempts=1, token_budget=15),
        1,
        output,
    )

    assert not result.success
    assert result.status == "partial"
    assert result.metrics["final_passed"] == 1
    assert result.metrics["failed"] == 1
    assert result.metrics["budget_exhausted"] == 1
    assert result.metrics["unprocessed"] == 1
    assert len(backend.requests) == 1
    rows = [json.loads(line) for line in (output / RESULTS_FILENAME).read_text().splitlines()]
    assert rows[0]["final_status"] == "passed"
    assert rows[1]["attempt_count"] == 0
    assert rows[1]["budget_exhausted"] is True


@pytest.mark.asyncio
async def test_workspace_cleanup_retries_transient_enotempty(
    source_checkout: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    invariant = load_accepted_invariants(invariants)[0]

    def create_workspace(
        *, source_root: Path, destination: Path, invariant: dict[str, Any]
    ) -> SimpleNamespace:
        del source_root, invariant
        (destination / "created-by-provider").write_text("fixture\n", encoding="utf-8")
        return SimpleNamespace(root=destination)

    runner = CheckerAuthoringRunner(backend=FakeAgentBackend(), workspace_factory=create_workspace)
    lease = await runner._workspace(source_checkout, tmp_path / "output", invariant, ("core",))
    workspace_root = lease.root
    real_rmtree = checker_authoring.shutil.rmtree
    tombstone_prefix = f".{workspace_root.name}.cleanup-"
    tombstone_attempts: list[Path] = []

    def transient_enotempty(path: Any, *args: Any, **kwargs: Any) -> None:
        target = Path(path).resolve(strict=False)
        if target.parent == workspace_root.parent and target.name.startswith(tombstone_prefix):
            tombstone_attempts.append(target)
            if len(tombstone_attempts) == 1:
                raise OSError(errno.ENOTEMPTY, "Directory not empty", str(path))
        real_rmtree(path, *args, **kwargs)

    monkeypatch.setattr(checker_authoring.shutil, "rmtree", transient_enotempty)

    assert lease.cleanup is not None
    cleanup_result = lease.cleanup()
    if inspect.isawaitable(cleanup_result):
        await cleanup_result

    assert len(tombstone_attempts) >= 2
    assert not workspace_root.exists()
    assert all(not tombstone.exists() for tombstone in tombstone_attempts)
    assert not list(workspace_root.parent.glob(f"{tombstone_prefix}*"))


@pytest.mark.asyncio
async def test_checker_authoring_denies_findings_reads(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    reference = tmp_path / "reference"
    (reference / "findings").mkdir(parents=True)
    output = tmp_path / "output"
    backend = RecordingIsolationCheckerBackend()
    backend.workspace_factory = FakeWorkspaceFactory()
    backend.command_executor = FakeCommandExecutor([True])

    result = await run(
        ExperimentPlan.from_dict(
            {
                **_plan(source_checkout, invariants, max_attempts=1).model_dump(mode="json"),
                "parameters": {
                    **_plan(source_checkout, invariants, max_attempts=1).parameters,
                    "reference_root": str(reference),
                    "require_host_read_isolation": True,
                },
            }
        ),
        1,
        output,
        backend=backend,
    )

    assert result.success
    assert backend.deny_read_paths
    expected = [source_checkout.resolve(), reference.resolve()]
    assert all(paths == expected for paths in backend.deny_read_paths)
    assert backend.host_isolation == [True]


@pytest.mark.asyncio
async def test_checker_authoring_rejects_deny_path_containing_agent_workspace(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    output = tmp_path / "output"

    def fixed_workspace(
        *, source_root: Path, destination: Path, invariant: dict[str, Any]
    ) -> SimpleNamespace:
        del source_root, destination, invariant
        workspace = output / "checker-authoring" / ".workspaces" / "fixed"
        (workspace / "core/internal/oracle").mkdir(parents=True)
        return SimpleNamespace(root=workspace)

    backend = FakeAgentBackend(command_executor=FakeCommandExecutor([True]))
    backend.workspace_factory = fixed_workspace  # type: ignore[assignment]
    plan = _plan(source_checkout, invariants, max_attempts=1).model_copy(
        update={
            "parameters": {
                **_plan(source_checkout, invariants, max_attempts=1).parameters,
                "deny_read_paths": [
                    str(output / "checker-authoring" / ".workspaces")
                ],
            }
        }
    )

    result = await CheckerAuthoringRunner(
        backend=backend,
        workspace_factory=backend.workspace_factory,
        command_executor=backend.command_executor,
    ).run(plan, 1, output)

    assert not result.success
    rows = [json.loads(line) for line in (output / RESULTS_FILENAME).read_text().splitlines()]
    assert "collides with agent cwd" in rows[0]["attempts"][0]["agent_error"]
    assert backend.requests == []


@pytest.mark.asyncio
async def test_provider_cleanup_error_still_removes_local_workspace(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    invariant = load_accepted_invariants(invariants)[0]
    provider_cleanup_calls = 0

    async def provider_cleanup() -> None:
        nonlocal provider_cleanup_calls
        provider_cleanup_calls += 1
        raise RuntimeError("provider cleanup failed")

    def create_workspace(
        *, source_root: Path, destination: Path, invariant: dict[str, Any]
    ) -> SimpleNamespace:
        del source_root, invariant
        (destination / "created-by-provider").write_text("fixture\n", encoding="utf-8")
        return SimpleNamespace(root=destination, cleanup=provider_cleanup)

    runner = CheckerAuthoringRunner(backend=FakeAgentBackend(), workspace_factory=create_workspace)
    lease = await runner._workspace(source_checkout, tmp_path / "output", invariant, ("core",))
    workspace_root = lease.root

    assert lease.cleanup is not None
    with pytest.raises(RuntimeError, match="provider cleanup failed"):
        cleanup_result = lease.cleanup()
        if inspect.isawaitable(cleanup_result):
            await cleanup_result

    assert provider_cleanup_calls == 1
    assert not workspace_root.exists()


@pytest.mark.asyncio
async def test_workspace_factory_error_does_not_leak_destination(
    source_checkout: Path, tmp_path: Path
) -> None:
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    invariant = load_accepted_invariants(invariants)[0]
    destinations: list[Path] = []

    def failing_workspace_factory(
        *, source_root: Path, destination: Path, invariant: dict[str, Any]
    ) -> SimpleNamespace:
        del source_root, invariant
        destinations.append(destination)
        (destination / "partial-workspace").write_text("fixture\n", encoding="utf-8")
        raise RuntimeError("workspace creation failed")

    runner = CheckerAuthoringRunner(
        backend=FakeAgentBackend(), workspace_factory=failing_workspace_factory
    )

    with pytest.raises(RuntimeError, match="workspace creation failed"):
        await runner._workspace(source_checkout, tmp_path / "output", invariant, ("core",))

    assert len(destinations) == 1
    assert not destinations[0].exists()


@pytest.mark.asyncio
async def test_source_identity_hashes_the_materialized_allowlist(
    source_checkout: Path, tmp_path: Path
) -> None:
    """Files outside the copied checker tree are not Part II inputs."""

    outside = source_checkout / "volatile-build"
    outside.mkdir()
    (outside / "dangling").symlink_to(outside / "already-removed")
    invariants = tmp_path / "accepted-invariants.jsonl"
    _write_invariants(invariants)
    executor = FakeCommandExecutor([True])
    backend = FakeAgentBackend(command_executor=executor)

    result = await CheckerAuthoringRunner(
        backend=backend, command_executor=executor
    ).run(_plan(source_checkout, invariants, max_attempts=1), 1, tmp_path / "out")

    assert result.success
    manifest = json.loads(
        (tmp_path / "out" / "checker-bundle-manifest.json").read_text(encoding="utf-8")
    )
    assert manifest["source_root_sha256"] == manifest["source_tree_sha256"]
