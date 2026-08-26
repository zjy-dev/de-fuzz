"""Shell-free online checker feedback for Part III audit candidates.

The command is configuration supplied by the caller.  Candidate content is
only exposed through a content-addressed temporary JSON file; it is never
interpreted as executable configuration.
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import os
import shutil
import tempfile
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Literal, Protocol

from pydantic import BaseModel, ConfigDict, Field, field_validator

from .audit_schema import AuditCandidate

OnlineOracleVerdict = Literal["PASS", "FAIL", "NOT_APPLICABLE", "ERROR"]
_FINGERPRINT_PLACEHOLDER = "{candidate_fingerprint}"
_CANDIDATE_JSON_PLACEHOLDER = "{candidate_json}"
_ALLOWED_VERDICTS = frozenset(("PASS", "FAIL", "NOT_APPLICABLE", "ERROR"))


def _canonical_candidate_bytes(candidate: AuditCandidate) -> bytes:
    return json.dumps(
        candidate.model_dump(mode="json"),
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")


def candidate_fingerprint(candidate: AuditCandidate) -> str:
    """Return a stable SHA-256 identity for the complete normalized candidate."""

    return hashlib.sha256(_canonical_candidate_bytes(candidate)).hexdigest()


class OnlineOracleResult(BaseModel):
    """Public feedback returned by an online checker.

    Extra response keys are intentionally discarded so a checker cannot smuggle
    a hidden finding into :func:`render_oracle_feedback`.
    """

    model_config = ConfigDict(frozen=True, extra="ignore")

    candidate_fingerprint: str
    verdict: OnlineOracleVerdict
    feedback: str = ""
    evidence: list[str] = Field(default_factory=list)
    error: str = ""

    @field_validator("evidence", mode="before")
    @classmethod
    def _coerce_evidence(cls, value: Any) -> Any:
        if value is None:
            return []
        if isinstance(value, str):
            return [value]
        return value


class OnlineOracle(Protocol):
    """An injectable source of real checker feedback for one candidate."""

    async def evaluate(
        self, candidate: AuditCandidate, workspace: str | os.PathLike[str]
    ) -> OnlineOracleResult: ...


class OnlineOracleExecutor(Protocol):
    """Injectable shell-free process adapter."""

    async def run(
        self,
        command: Sequence[str],
        *,
        cwd: Path,
        timeout_seconds: float | None = None,
    ) -> Any: ...


@dataclass(frozen=True, slots=True)
class _ExecutionResult:
    exit_code: int
    stdout: str = ""
    stderr: str = ""
    timed_out: bool = False


class SubprocessOnlineOracleExecutor:
    """Default executor using argv directly, never a shell."""

    async def run(
        self,
        command: Sequence[str],
        *,
        cwd: Path,
        timeout_seconds: float | None = None,
    ) -> _ExecutionResult:
        del timeout_seconds  # The caller owns the deadline for every executor.
        argv = tuple(command)
        try:
            process = await asyncio.create_subprocess_exec(
                *argv,
                cwd=cwd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
        except OSError as exc:
            return _ExecutionResult(exit_code=127, stderr=str(exc))

        try:
            stdout, stderr = await process.communicate()
        except asyncio.CancelledError:
            if process.returncode is None:
                process.kill()
                await process.communicate()
            raise
        return _ExecutionResult(
            exit_code=process.returncode if process.returncode is not None else 1,
            stdout=stdout.decode("utf-8", errors="replace"),
            stderr=stderr.decode("utf-8", errors="replace"),
        )


def _read_execution_field(value: Any, *names: str) -> Any:
    if isinstance(value, Mapping):
        for name in names:
            if name in value:
                return value[name]
        return None
    for name in names:
        if hasattr(value, name):
            return getattr(value, name)
    return None


def _as_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="replace")
    return str(value)


def _normalize_execution(value: Any) -> _ExecutionResult:
    exit_code = _read_execution_field(value, "exit_code", "returncode")
    if exit_code is None:
        success = _read_execution_field(value, "success", "passed")
        if success is not None:
            exit_code = 0 if bool(success) else 1
    if exit_code is None:
        raise ValueError("online oracle executor did not return an exit status")
    return _ExecutionResult(
        exit_code=int(exit_code),
        stdout=_as_text(_read_execution_field(value, "stdout")),
        stderr=_as_text(_read_execution_field(value, "stderr", "error")),
        timed_out=bool(_read_execution_field(value, "timed_out")),
    )


def _error_result(fingerprint: str, message: str) -> OnlineOracleResult:
    return OnlineOracleResult(
        candidate_fingerprint=fingerprint,
        verdict="ERROR",
        feedback=message,
        error=message,
    )


def _parse_checker_stdout(stdout: str, fingerprint: str) -> OnlineOracleResult:
    try:
        payload = json.loads(stdout)
    except (TypeError, json.JSONDecodeError) as exc:
        return _error_result(fingerprint, f"online oracle stdout is not valid JSON: {exc}")
    if not isinstance(payload, Mapping):
        return _error_result(fingerprint, "online oracle stdout must be a JSON object")

    echoed = payload.get("candidate_fingerprint")
    if not isinstance(echoed, str):
        return _error_result(
            fingerprint, "online oracle candidate_fingerprint echo is missing"
        )
    if echoed != fingerprint:
        return _error_result(
            fingerprint, "online oracle candidate_fingerprint echo does not match"
        )

    verdict = payload.get("verdict")
    if not isinstance(verdict, str) or verdict not in _ALLOWED_VERDICTS:
        return _error_result(
            fingerprint,
            "online oracle verdict must be PASS, FAIL, NOT_APPLICABLE, or ERROR",
        )
    if "feedback" not in payload or not isinstance(payload["feedback"], str):
        return _error_result(fingerprint, "online oracle feedback must be a string")
    if "evidence" not in payload:
        return _error_result(fingerprint, "online oracle evidence is missing")
    evidence = payload["evidence"]
    if isinstance(evidence, str):
        normalized_evidence = [evidence]
    elif isinstance(evidence, list) and all(isinstance(item, str) for item in evidence):
        normalized_evidence = evidence
    else:
        return _error_result(
            fingerprint, "online oracle evidence must be a string or an array of strings"
        )
    return OnlineOracleResult(
        candidate_fingerprint=fingerprint,
        verdict=verdict,  # type: ignore[arg-type]
        feedback=payload["feedback"],
        evidence=normalized_evidence,
    )


class CommandOnlineOracle:
    """Invoke a caller-frozen argv template for each candidate."""

    def __init__(
        self,
        argv_template: Sequence[str],
        *,
        timeout_seconds: float | None = 300.0,
        executor: OnlineOracleExecutor | None = None,
    ) -> None:
        if isinstance(argv_template, (str, bytes)):
            raise TypeError("online oracle argv template must be a sequence of arguments")
        frozen = tuple(argv_template)
        if not frozen or any(
            not isinstance(item, str) or not item or "\x00" in item for item in frozen
        ):
            raise ValueError(
                "online oracle argv template requires non-empty, NUL-free strings"
            )
        if not any(_FINGERPRINT_PLACEHOLDER in item for item in frozen):
            raise ValueError(
                "online oracle argv template must contain {candidate_fingerprint}"
            )
        if timeout_seconds is not None and timeout_seconds <= 0:
            raise ValueError("online oracle timeout must be positive")
        self.argv_template = frozen
        self.timeout_seconds = timeout_seconds
        self.executor = executor or SubprocessOnlineOracleExecutor()

    async def evaluate(
        self, candidate: AuditCandidate, workspace: str | os.PathLike[str]
    ) -> OnlineOracleResult:
        candidate_bytes = _canonical_candidate_bytes(candidate)
        fingerprint = hashlib.sha256(candidate_bytes).hexdigest()
        try:
            cwd = Path(workspace).expanduser().resolve(strict=True)
        except OSError as exc:
            return _error_result(fingerprint, f"online oracle workspace is invalid: {exc}")
        if not cwd.is_dir():
            return _error_result(fingerprint, "online oracle workspace is not a directory")

        temporary_root = Path(tempfile.mkdtemp(prefix="defuzz-online-oracle-"))
        try:
            temporary_root = temporary_root.resolve(strict=True)
            if temporary_root == cwd or temporary_root.is_relative_to(cwd):
                return _error_result(
                    fingerprint,
                    "online oracle candidate JSON directory must be outside the workspace",
                )
            candidate_path = temporary_root / "candidate.json"
            candidate_path.write_bytes(candidate_bytes)
            argv = tuple(
                item.replace(_FINGERPRINT_PLACEHOLDER, fingerprint).replace(
                    _CANDIDATE_JSON_PLACEHOLDER, os.fspath(candidate_path)
                )
                for item in self.argv_template
            )
            try:
                pending = self.executor.run(
                    argv, cwd=cwd, timeout_seconds=self.timeout_seconds
                )
                raw_result = (
                    await pending
                    if self.timeout_seconds is None
                    else await asyncio.wait_for(pending, timeout=self.timeout_seconds)
                )
            except TimeoutError:
                return _error_result(fingerprint, "online oracle timed out")
            except Exception as exc:
                return _error_result(
                    fingerprint,
                    f"online oracle executor failed: {type(exc).__name__}: {exc}",
                )
            try:
                execution = _normalize_execution(raw_result)
            except (TypeError, ValueError) as exc:
                return _error_result(fingerprint, str(exc))
            if execution.timed_out:
                return _error_result(fingerprint, "online oracle timed out")
            if execution.exit_code != 0:
                return _error_result(
                    fingerprint,
                    f"online oracle exited with status {execution.exit_code}",
                )
            return _parse_checker_stdout(execution.stdout, fingerprint)
        except OSError as exc:
            return _error_result(
                fingerprint, f"online oracle candidate JSON could not be written: {exc}"
            )
        finally:
            shutil.rmtree(temporary_root, ignore_errors=True)


def render_oracle_feedback(results: Sequence[OnlineOracleResult]) -> str:
    """Render only the public oracle contract for delivery to an audit worker."""

    sections: list[str] = []
    for result in results:
        lines = [
            f"Candidate {result.candidate_fingerprint}",
            f"Verdict: {result.verdict}",
            f"Feedback: {result.feedback}",
        ]
        if result.evidence:
            lines.append("Evidence:")
            lines.extend(f"- {item}" for item in result.evidence)
        sections.append("\n".join(lines))
    return "\n\n".join(sections)
