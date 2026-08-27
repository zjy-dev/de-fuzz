from __future__ import annotations

import asyncio
import hashlib
import json
import sys
from collections.abc import Sequence
from pathlib import Path
from typing import Any

import pytest

from defuzz_loop.audit_schema import AuditCandidate
from defuzz_loop.checker_bundle import CheckerBundleManifest, ValidatedCheckerBundle
from defuzz_loop.online_oracle import (
    CommandOnlineOracle,
    OnlineOracleResult,
    candidate_fingerprint,
    checker_bundle_dispatcher_argv,
    normalize_compiler,
    render_oracle_feedback,
)


def _candidate(**updates: object) -> AuditCandidate:
    payload: dict[str, object] = {
        "id": "DREV-2026-001",
        "title": "A candidate title that must not become an argv item",
        "toolchain": "gcc",
        "toolchain_version": "gcc-17-20260826",
        "mechanism": "stack-protector",
        "isa": ["x86_64"],
        "invariant_violated": "Protected returns must check the guard.",
        "minimal_trigger": {
            "source": "int main(void) { return 0; }",
            "flags": ["-O2", "$(touch must-not-exist)"],
        },
        "discovered": "2026-08-26",
    }
    payload.update(updates)
    return AuditCandidate.model_validate(payload)


class RecordingExecutor:
    def __init__(self, response: dict[str, Any]) -> None:
        self.response = response
        self.calls: list[tuple[tuple[str, ...], Path, float | None]] = []
        self.candidate_bytes: bytes | None = None
        self.candidate_path: Path | None = None

    async def run(
        self,
        command: Sequence[str],
        *,
        cwd: Path,
        timeout_seconds: float | None = None,
    ) -> dict[str, Any]:
        argv = tuple(command)
        self.calls.append((argv, cwd, timeout_seconds))
        for item in argv:
            path = Path(item)
            if path.name == "candidate.json" and path.is_file():
                self.candidate_path = path
                self.candidate_bytes = path.read_bytes()
        return self.response


def _response(candidate: AuditCandidate, *, verdict: str = "PASS") -> dict[str, Any]:
    return {
        "exit_code": 0,
        "stdout": json.dumps(
            {
                "candidate_fingerprint": candidate_fingerprint(candidate),
                "verdict": verdict,
                "feedback": "checker feedback",
                "evidence": ["checker evidence"],
            }
        ),
        "stderr": "",
    }


def test_candidate_fingerprint_is_stable_and_tracks_the_full_candidate() -> None:
    left = _candidate(experimental={"b": 2, "a": 1})
    right_payload = left.model_dump(mode="json")
    right_payload["experimental"] = {"a": 1, "b": 2}
    right = AuditCandidate.model_validate(right_payload)

    assert candidate_fingerprint(left) == candidate_fingerprint(right)
    assert candidate_fingerprint(left) != candidate_fingerprint(
        left.model_copy(update={"title": "different"})
    )


@pytest.mark.parametrize(
    "stdout",
    [
        "true",
        json.dumps({"verdict": "PASS", "feedback": "no echo", "evidence": []}),
    ],
)
async def test_json_true_or_missing_fingerprint_echo_cannot_pass(
    tmp_path: Path, stdout: str
) -> None:
    oracle = CommandOnlineOracle(
        ("checker", "--fingerprint", "{candidate_fingerprint}"),
        executor=RecordingExecutor({"exit_code": 0, "stdout": stdout}),
    )

    result = await oracle.evaluate(_candidate(), tmp_path)

    assert result.verdict == "ERROR"
    assert "fingerprint" in result.feedback or "object" in result.feedback


async def test_successful_true_command_without_echo_cannot_pass(tmp_path: Path) -> None:
    oracle = CommandOnlineOracle(("true", "{candidate_fingerprint}"))

    result = await oracle.evaluate(_candidate(), tmp_path)

    assert result.verdict == "ERROR"
    assert "valid JSON" in result.feedback


async def test_matching_echo_and_candidate_file_hash_can_pass(tmp_path: Path) -> None:
    candidate = _candidate()
    executor = RecordingExecutor(_response(candidate))
    oracle = CommandOnlineOracle(
        (
            "checker",
            "--fingerprint={candidate_fingerprint}",
            "{candidate_json}",
        ),
        timeout_seconds=7.5,
        executor=executor,
    )

    result = await oracle.evaluate(candidate, tmp_path)

    fingerprint = candidate_fingerprint(candidate)
    assert result == OnlineOracleResult(
        candidate_fingerprint=fingerprint,
        verdict="PASS",
        feedback="checker feedback",
        evidence=["checker evidence"],
    )
    argv, cwd, timeout = executor.calls[0]
    assert argv[1] == f"--fingerprint={fingerprint}"
    assert executor.candidate_bytes is not None
    assert hashlib.sha256(executor.candidate_bytes).hexdigest() == fingerprint
    assert executor.candidate_path is not None
    assert not executor.candidate_path.is_relative_to(tmp_path.resolve())
    assert not executor.candidate_path.exists()
    assert cwd == tmp_path.resolve()
    assert timeout == 7.5


def test_template_requires_fingerprint_and_is_frozen() -> None:
    with pytest.raises(ValueError, match="candidate_fingerprint"):
        CommandOnlineOracle(("checker", "{candidate_json}"))

    template = ["checker", "{candidate_fingerprint}"]
    oracle = CommandOnlineOracle(template)
    template.append("later-mutation")

    assert oracle.argv_template == ("checker", "{candidate_fingerprint}")


def test_checker_bundle_dispatcher_argv_uses_one_dual_mode_protocol(
    tmp_path: Path,
) -> None:
    dispatcher = tmp_path / "dispatcher"
    catalog = tmp_path / "catalog.json"
    patch = tmp_path / "bundle.patch"
    manifest_path = tmp_path / "checker-bundle-manifest.json"
    toolchains = tmp_path / "toolchains.yaml"
    for path in (dispatcher, catalog, patch, manifest_path, toolchains):
        path.write_text("fixture\n", encoding="utf-8")
    manifest = CheckerBundleManifest.model_construct(
        schema_version=1,
        kind="defuzz-checker-bundle",
        status="ready",
        bundle_id="0" * 64,
        source_root="/source",
        source_root_sha256="1" * 64,
        source_tree_sha256="2" * 64,
        final_tree_sha256="3" * 64,
        coverage_complete=True,
        included_invariant_ids=["INV-ONE"],
        failed_invariant_ids=[],
        invariants=[],
        artifacts=None,
        validation=None,
    )
    bundle = ValidatedCheckerBundle.model_construct(
        manifest=manifest,
        manifest_path=manifest_path,
        root=tmp_path,
        cumulative_patch=patch,
        catalog=catalog,
        dispatcher=dispatcher,
    )

    online = checker_bundle_dispatcher_argv(bundle, toolchains, mode="online", compiler="gnu-gcc")
    verify = checker_bundle_dispatcher_argv(bundle, toolchains, mode="verify", compiler="gcc")

    assert online[:3] == (str(dispatcher), "--mode", "online")
    assert verify[:3] == (str(dispatcher), "--mode", "verify")
    assert online[3:] == verify[3:]
    assert online.count("--compiler") == 1
    assert online[online.index("--compiler") + 1] == "gcc"
    assert online.count("{candidate_json}") == 1
    assert online.count("{candidate_fingerprint}") == 1


@pytest.mark.parametrize(
    ("configured", "canonical"),
    [
        ("gcc", "gcc"),
        ("gnu-gcc", "gcc"),
        ("llvm", "llvm"),
        ("clang", "llvm"),
        ("compiler-rt", "llvm"),
        ("lld", "llvm"),
    ],
)
def test_compiler_aliases_match_dispatcher_contract(configured: str, canonical: str) -> None:
    assert normalize_compiler(configured) == canonical


@pytest.mark.parametrize("configured", ["", "cc", "llvm-clang", "msvc"])
def test_unknown_compiler_is_rejected(configured: str) -> None:
    with pytest.raises(ValueError, match="unknown compiler"):
        normalize_compiler(configured)


async def test_candidate_content_never_becomes_command_structure(tmp_path: Path) -> None:
    candidate = _candidate(
        title="; touch injected",
        verification_command=["sh", "-c", "touch injected"],
    )
    executor = RecordingExecutor(_response(candidate))
    oracle = CommandOnlineOracle(
        (
            sys.executable,
            "literal argument with spaces",
            "{candidate_fingerprint}",
            "{candidate_json}",
        ),
        executor=executor,
    )

    result = await oracle.evaluate(candidate, tmp_path)

    assert result.verdict == "PASS"
    argv = executor.calls[0][0]
    assert argv[0] == sys.executable
    assert argv[1] == "literal argument with spaces"
    assert "; touch injected" not in argv
    assert "sh" not in argv
    assert "-c" not in argv


async def test_nonzero_exit_and_echo_mismatch_are_errors(tmp_path: Path) -> None:
    candidate = _candidate()
    nonzero = CommandOnlineOracle(
        ("checker", "{candidate_fingerprint}"),
        executor=RecordingExecutor({"exit_code": 3, "stdout": _response(candidate)["stdout"]}),
    )
    mismatch_payload = json.loads(_response(candidate)["stdout"])
    mismatch_payload["candidate_fingerprint"] = "0" * 64
    mismatch = CommandOnlineOracle(
        ("checker", "{candidate_fingerprint}"),
        executor=RecordingExecutor({"exit_code": 0, "stdout": json.dumps(mismatch_payload)}),
    )

    assert (await nonzero.evaluate(candidate, tmp_path)).verdict == "ERROR"
    mismatch_result = await mismatch.evaluate(candidate, tmp_path)
    assert mismatch_result.verdict == "ERROR"
    assert "does not match" in mismatch_result.feedback


async def test_bundle_oracle_rejects_unknown_checker_before_execution(
    tmp_path: Path,
) -> None:
    candidate = _candidate(checker_ids=["UNKNOWN"])
    executor = RecordingExecutor(_response(candidate))
    oracle = CommandOnlineOracle(
        ("dispatcher", "{candidate_fingerprint}"),
        executor=executor,
        allowed_checker_ids={"INV-ONE"},
    )

    result = await oracle.evaluate(candidate, tmp_path)

    assert result.verdict == "ERROR"
    assert "trusted catalog" in result.feedback
    assert executor.calls == []


async def test_bundle_oracle_requires_dispatcher_fingerprint_echo(
    tmp_path: Path,
) -> None:
    candidate = _candidate(checker_ids=["INV-ONE"])
    response = _response(candidate)
    payload = json.loads(response["stdout"])
    payload.pop("echoed_candidate_fingerprint", None)
    response["stdout"] = json.dumps(payload)
    oracle = CommandOnlineOracle(
        ("dispatcher", "{candidate_fingerprint}"),
        executor=RecordingExecutor(response),
        allowed_checker_ids={"INV-ONE"},
        require_dispatcher_echo=True,
    )

    result = await oracle.evaluate(candidate, tmp_path)

    assert result.verdict == "ERROR"
    assert "echoed_candidate_fingerprint" in result.feedback


class HangingExecutor:
    async def run(
        self,
        command: Sequence[str],
        *,
        cwd: Path,
        timeout_seconds: float | None = None,
    ) -> dict[str, Any]:
        del command, cwd, timeout_seconds
        await asyncio.sleep(60)
        return {"exit_code": 0, "stdout": "{}"}


async def test_timeout_is_an_error_even_for_an_injected_executor(tmp_path: Path) -> None:
    oracle = CommandOnlineOracle(
        ("checker", "{candidate_fingerprint}"),
        timeout_seconds=0.01,
        executor=HangingExecutor(),
    )

    result = await oracle.evaluate(_candidate(), tmp_path)

    assert result.verdict == "ERROR"
    assert "timed out" in result.feedback


def test_render_feedback_uses_only_the_public_result_contract() -> None:
    result = OnlineOracleResult.model_validate(
        {
            "candidate_fingerprint": "a" * 64,
            "verdict": "FAIL",
            "feedback": "reproduced by the checker",
            "evidence": ["exit=1"],
            "hidden_finding": "must not leak",
        }
    )

    rendered = render_oracle_feedback([result])

    assert "reproduced by the checker" in rendered
    assert "exit=1" in rendered
    assert "hidden_finding" not in rendered
    assert "must not leak" not in rendered
