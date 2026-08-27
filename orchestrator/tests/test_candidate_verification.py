from __future__ import annotations

import hashlib
import json
from collections.abc import Sequence
from pathlib import Path
from typing import Any

import pytest

from defuzz_loop.audit_schema import AuditCandidate
from defuzz_loop.candidate_verification import (
    VerificationCommand,
    candidate_fingerprint,
    verify_candidate,
)

SOURCE = """int guarded_copy(char *dst, const char *src) {
    if (dst == 0 || src == 0) {
        return -1;
    }
    copy_bytes(dst, src);
    return 0;
}
"""


def _candidate(*, citation: str, excerpt: str, poc_verified: bool = True) -> AuditCandidate:
    return AuditCandidate.model_validate(
        {
            "toolchain": "gcc",
            "toolchain_version": "gcc-17-20260531",
            "mechanism": "stack-protector",
            "isa": ["x86_64"],
            "invariant_violated": "Every protected return checks the guard.",
            "evidence_file_line": [citation],
            "evidence_code": excerpt,
            "minimal_trigger": {
                "source": "int main(void) { return 0; }",
                "flags": ["-O2", "-fstack-protector-all"],
                "isa": "x86_64",
            },
            "impact": "The generated return can omit the promised guard check.",
            "why_not_rescued": "No later layer reconstructs an omitted guard check.",
            "poc_verified": poc_verified,
            "poc_verification_plan": (
                "Compile and run the trigger with the frozen checker command."
            ),
            "discovered": "2026-08-26",
        }
    )


class FakeExecutor:
    def __init__(
        self,
        *,
        exit_code: int,
        echoed_candidate_fingerprint: str | None = "from-command",
    ) -> None:
        self.exit_code = exit_code
        self.echoed_candidate_fingerprint = echoed_candidate_fingerprint
        self.calls: list[tuple[tuple[str, ...], Path, float | None]] = []

    async def run(
        self, command: Sequence[str], *, cwd: Path, timeout_seconds: float | None = None
    ) -> dict[str, Any]:
        argv = tuple(command)
        self.calls.append((argv, cwd, timeout_seconds))
        echoed = self.echoed_candidate_fingerprint
        if echoed == "from-command":
            echoed = argv[argv.index("--candidate-fingerprint") + 1]
        result: dict[str, Any] = {
            "exit_code": self.exit_code,
            "stdout": "checker passed\n" if self.exit_code == 0 else "",
            "stderr": "" if self.exit_code == 0 else "checker failed\n",
        }
        if echoed is not None:
            result["echoed_candidate_fingerprint"] = echoed
        return result


COMMAND = (
    "frozen-checker",
    "--candidate-fingerprint",
    "{candidate_fingerprint}",
)


@pytest.fixture
def source_root(tmp_path: Path) -> Path:
    path = tmp_path / "gcc" / "config" / "example.c"
    path.parent.mkdir(parents=True)
    path.write_text(SOURCE, encoding="utf-8", newline="")
    return tmp_path


@pytest.mark.parametrize(
    "citation",
    [
        "../outside.c:1",
        "/tmp/outside.c:1",
        r"C:\\outside.c:1",
    ],
)
async def test_rejects_absolute_and_parent_escape_paths(
    source_root: Path, citation: str
) -> None:
    executor = FakeExecutor(exit_code=0)

    result = await verify_candidate(
        _candidate(citation=citation, excerpt=SOURCE),
        [source_root],
        executor=executor,
        commands=[COMMAND],
    )

    assert result.status == "invalid"
    assert result.admission_scope == "structural-completeness-only"
    assert len(result.completeness.checks) == 5
    assert result.evidence_checks[0].passed is False
    assert "relative" in result.evidence_checks[0].reason
    assert executor.calls == []


async def test_rejects_fabricated_excerpt(source_root: Path) -> None:
    fabricated = "one\ntwo\nthree\nfour\nfive"

    result = await verify_candidate(
        _candidate(citation="gcc/config/example.c:1", excerpt=fabricated),
        [source_root],
        commands=[COMMAND],
        executor=FakeExecutor(exit_code=0),
    )

    assert result.status == "invalid"
    assert result.evidence_checks[0].passed is False
    assert "does not match" in result.evidence_checks[0].reason


async def test_invalid_evidence_precedes_bundle_checker_routing(
    source_root: Path,
) -> None:
    candidate = _candidate(
        citation="gcc/config/example.c:1",
        excerpt="one\ntwo\nthree\nfour\nfive",
    ).model_copy(update={"checker_ids": ["UNKNOWN"]})

    result = await verify_candidate(
        candidate,
        [source_root],
        commands=[COMMAND],
        allowed_checker_ids={"KNOWN"},
    )

    assert result.status == "invalid"
    assert any("evidence excerpt does not match" in issue for issue in result.issues)
    assert not any("absent from trusted catalog" in issue for issue in result.issues)


async def test_checker_allowlist_permits_empty_selection_for_dispatcher_routing(
    source_root: Path,
) -> None:
    candidate = _candidate(
        citation="gcc/config/example.c:1", excerpt=SOURCE
    ).model_copy(update={"checker_ids": []})
    executor = FakeExecutor(exit_code=0)

    result = await verify_candidate(
        candidate,
        [source_root],
        executor=executor,
        commands=[COMMAND],
        allowed_checker_ids={"KNOWN"},
    )

    assert result.status == "verified"
    assert len(executor.calls) == 1


async def test_required_checker_selection_rejects_empty_checker_ids(
    source_root: Path,
) -> None:
    candidate = _candidate(
        citation="gcc/config/example.c:1", excerpt=SOURCE
    ).model_copy(update={"checker_ids": []})
    executor = FakeExecutor(exit_code=0)

    result = await verify_candidate(
        candidate,
        [source_root],
        executor=executor,
        commands=[COMMAND],
        allowed_checker_ids={"KNOWN"},
        require_checker_ids=True,
    )

    assert result.status == "invalid"
    assert any("requires at least one checker_id" in issue for issue in result.issues)
    assert executor.calls == []


async def test_bundle_verification_rejects_unknown_checker_ids(
    source_root: Path,
) -> None:
    candidate = _candidate(
        citation="gcc/config/example.c:1", excerpt=SOURCE
    ).model_copy(update={"checker_ids": ["UNKNOWN"]})
    executor = FakeExecutor(exit_code=0)

    result = await verify_candidate(
        candidate,
        [source_root],
        executor=executor,
        commands=[COMMAND],
        allowed_checker_ids={"KNOWN"},
    )

    assert result.status == "invalid"
    assert any("trusted catalog" in issue for issue in result.issues)
    assert executor.calls == []


@pytest.mark.parametrize(
    ("verdict", "exit_code", "expected_status", "result_valid"),
    [
        ("FAIL", 0, "verified", True),
        ("PASS", 1, "rejected", True),
        ("NOT_APPLICABLE", 1, "rejected", True),
        ("ERROR", 2, "unverified", False),
    ],
)
async def test_dispatcher_verify_protocol_has_terminal_semantics(
    source_root: Path,
    verdict: str,
    exit_code: int,
    expected_status: str,
    result_valid: bool,
) -> None:
    candidate = _candidate(
        citation="gcc/config/example.c:1",
        excerpt=SOURCE,
        poc_verified=False,
    ).model_copy(update={"checker_ids": ["INV-ONE"]})

    class DispatcherExecutor:
        async def run(
            self,
            command: Sequence[str],
            *,
            cwd: Path,
            timeout_seconds: float | None = None,
        ) -> dict[str, Any]:
            del cwd, timeout_seconds
            argv = tuple(command)
            fingerprint = argv[argv.index("--candidate-fingerprint") + 1]
            return {
                "exit_code": exit_code,
                "stdout": json.dumps(
                    {
                        "candidate_fingerprint": fingerprint,
                        "echoed_candidate_fingerprint": fingerprint,
                        "verdict": verdict,
                        "feedback": "fixture",
                        "evidence": [],
                    }
                ),
            }

    result = await verify_candidate(
        candidate,
        [source_root],
        executor=DispatcherExecutor(),
        commands=[
            VerificationCommand(
                (
                    "dispatcher",
                    "--candidate-json",
                    "{candidate_json}",
                    "--candidate-fingerprint",
                    "{candidate_fingerprint}",
                ),
                protocol="dispatcher-verify",
            )
        ],
        allowed_checker_ids={"INV-ONE"},
    )

    assert result.status == expected_status
    assert result.original_poc_verified_claim is False
    assert result.execution_records[0].execution_completed is True
    assert result.execution_records[0].result_valid is result_valid


async def test_real_fenced_crlf_excerpt_is_grounded_but_not_confirmed_without_command(
    source_root: Path,
) -> None:
    fenced = "```c\r\n" + SOURCE.replace("\n", "\r\n") + "```\r\n"
    candidate = _candidate(citation="gcc/config/example.c:2", excerpt=fenced)
    # Extra fields are worker claims, not executable orchestrator configuration.
    assert candidate.__pydantic_extra__ is not None
    candidate.__pydantic_extra__["verification_command"] = ["must-not-run"]

    result = await verify_candidate(candidate, [source_root])

    assert result.status == "unverified"
    assert result.completeness.admitted
    assert result.evidence_checks[0].passed
    assert result.evidence_checks[0].matched_start_line == 1
    assert result.evidence_checks[0].matched_end_line == 7
    assert result.execution_records == []
    assert result.artifact_hashes["gcc/config/example.c"] == hashlib.sha256(
        SOURCE.encode()
    ).hexdigest()


@pytest.mark.parametrize(
    ("exit_code", "expected_status"),
    [(0, "verified"), (1, "invalid")],
)
async def test_injected_executor_controls_confirmation(
    source_root: Path, exit_code: int, expected_status: str
) -> None:
    executor = FakeExecutor(exit_code=exit_code)

    result = await verify_candidate(
        _candidate(citation="gcc/config/example.c:1", excerpt=SOURCE),
        [source_root],
        executor=executor,
        commands=[VerificationCommand(COMMAND)],
    )

    assert result.status == expected_status
    assert result.execution_records[0].passed is (exit_code == 0)
    expected_fingerprint = candidate_fingerprint(
        _candidate(citation="gcc/config/example.c:1", excerpt=SOURCE)
    )
    assert executor.calls[0][0] == (
        "frozen-checker",
        "--candidate-fingerprint",
        expected_fingerprint,
    )
    assert (
        result.execution_records[0].expected_candidate_fingerprint
        == expected_fingerprint
    )
    assert (
        result.execution_records[0].echoed_candidate_fingerprint
        == expected_fingerprint
    )
    assert "execution-record-0001.json" in result.artifact_hashes


async def test_deterministic_success_overrides_false_poc_claim_but_preserves_it(
    source_root: Path,
) -> None:
    result = await verify_candidate(
        _candidate(
            citation="gcc/config/example.c:1",
            excerpt=SOURCE,
            poc_verified=False,
        ),
        [source_root],
        executor=FakeExecutor(exit_code=0),
        commands=[COMMAND],
    )

    assert result.status == "verified"
    assert result.execution_records[0].passed
    assert result.execution_records[0].execution_completed is True
    assert result.execution_records[0].result_valid is True
    assert result.original_poc_verified_claim is False
    assert not any("poc_verified" in issue for issue in result.issues)


def test_candidate_fingerprint_is_stable_for_equivalent_candidates() -> None:
    candidate = _candidate(citation="gcc/config/example.c:1", excerpt=SOURCE)
    reordered = AuditCandidate.model_validate(
        dict(reversed(list(candidate.model_dump(mode="json").items())))
    )

    assert candidate_fingerprint(candidate) == candidate_fingerprint(reordered)
    assert len(candidate_fingerprint(candidate)) == 64


async def test_candidate_json_placeholder_points_to_canonical_payload(
    source_root: Path,
) -> None:
    candidate = _candidate(citation="gcc/config/example.c:1", excerpt=SOURCE)

    class PayloadExecutor:
        async def run(
            self,
            command: Sequence[str],
            *,
            cwd: Path,
            timeout_seconds: float | None = None,
        ) -> dict[str, Any]:
            del cwd, timeout_seconds
            argv = tuple(command)
            fingerprint = argv[argv.index("--candidate-fingerprint") + 1]
            payload_path = Path(argv[argv.index("--candidate-json") + 1])
            assert payload_path.exists()
            assert AuditCandidate.model_validate_json(payload_path.read_text()) == candidate
            assert hashlib.sha256(payload_path.read_bytes()).hexdigest() == fingerprint
            return {
                "exit_code": 0,
                "stdout": json.dumps(
                    {"echoed_candidate_fingerprint": fingerprint}
                ),
            }

    result = await verify_candidate(
        candidate,
        [source_root],
        executor=PayloadExecutor(),
        commands=[
            [
                "frozen-checker",
                "--candidate-json",
                "{candidate_json}",
                "--candidate-fingerprint",
                "{candidate_fingerprint}",
            ]
        ],
    )

    assert result.status == "verified"
    assert result.execution_records[0].passed


async def test_command_without_fingerprint_placeholder_is_invalid(
    source_root: Path,
) -> None:
    executor = FakeExecutor(exit_code=0)

    result = await verify_candidate(
        _candidate(citation="gcc/config/example.c:1", excerpt=SOURCE),
        [source_root],
        executor=executor,
        commands=[["true"]],
    )

    assert result.status == "invalid"
    assert executor.calls == []
    assert any("{candidate_fingerprint}" in issue for issue in result.issues)


async def test_true_with_fingerprint_placeholder_cannot_verify(
    source_root: Path,
) -> None:
    result = await verify_candidate(
        _candidate(citation="gcc/config/example.c:1", excerpt=SOURCE),
        [source_root],
        commands=[["true", "{candidate_fingerprint}"]],
    )

    assert result.status == "invalid"
    assert result.execution_records[0].exit_code == 0
    assert result.execution_records[0].echoed_candidate_fingerprint is None
    assert result.execution_records[0].passed is False


@pytest.mark.parametrize(
    ("echoed_fingerprint", "issue_text"),
    [
        (None, "did not echo"),
        ("0" * 64, "wrong candidate fingerprint"),
    ],
)
async def test_exit_zero_without_correct_fingerprint_echo_is_unverified(
    source_root: Path, echoed_fingerprint: str | None, issue_text: str
) -> None:
    executor = FakeExecutor(
        exit_code=0, echoed_candidate_fingerprint=echoed_fingerprint
    )

    result = await verify_candidate(
        _candidate(citation="gcc/config/example.c:1", excerpt=SOURCE),
        [source_root],
        executor=executor,
        commands=[COMMAND],
    )

    assert result.status == "invalid"
    assert result.execution_records[0].passed is False
    assert any(issue_text in issue for issue in result.issues)


async def test_plain_stdout_fingerprint_is_not_a_structured_echo(
    source_root: Path,
) -> None:
    class PlainOutputExecutor:
        async def run(
            self,
            command: Sequence[str],
            *,
            cwd: Path,
            timeout_seconds: float | None = None,
        ) -> dict[str, Any]:
            del cwd, timeout_seconds
            argv = tuple(command)
            fingerprint = argv[argv.index("--candidate-fingerprint") + 1]
            return {"exit_code": 0, "stdout": fingerprint}

    result = await verify_candidate(
        _candidate(citation="gcc/config/example.c:1", excerpt=SOURCE),
        [source_root],
        executor=PlainOutputExecutor(),
        commands=[COMMAND],
    )

    assert result.status == "invalid"
    assert result.execution_records[0].passed is False
