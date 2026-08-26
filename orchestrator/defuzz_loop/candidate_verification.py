"""Offline grounding and execution verification for Part III candidates.

The five checks in :mod:`defuzz_loop.admission` are a structural completeness
gate.  They deliberately do not prove that quoted source exists or that a PoC
ran successfully.  This module adds that second, deterministic layer without
executing any command supplied by an audit candidate.
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import os
import re
import tempfile
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path, PurePosixPath, PureWindowsPath
from typing import Any, Literal, Protocol

from pydantic import BaseModel, ConfigDict, Field

from .admission import evaluate_candidate
from .audit_schema import AdmissionResult, AuditCandidate

ADMISSION_SCOPE = "structural-completeness-only"
CANDIDATE_FINGERPRINT_PLACEHOLDER = "{candidate_fingerprint}"
CANDIDATE_JSON_PLACEHOLDER = "{candidate_json}"

VerificationStatus = Literal["verified", "unverified", "invalid"]
_CITATION = re.compile(
    r"^(?P<path>.+):(?P<start>[1-9]\d*)(?:(?:-|:)(?P<end>[1-9]\d*))?$"
)
_FENCE = re.compile(r"^\s*(?P<fence>`{3,}|~{3,})[^`~]*$")


class EvidenceCheck(BaseModel):
    """Grounding result for one worker-provided source citation."""

    model_config = ConfigDict(frozen=True)

    citation: str
    file: str = ""
    line: int | None = None
    end_line: int | None = None
    excerpt_line_count: int = 0
    passed: bool
    reason: str = ""
    matched_start_line: int | None = None
    matched_end_line: int | None = None
    source_root_index: int | None = None
    sha256: str | None = None


@dataclass(frozen=True, slots=True)
class VerificationCommand:
    """A caller-frozen, shell-free PoC or checker command.

    ``cwd`` is relative to one sanitized source root.  Candidate fields are
    never converted into this type.
    """

    argv: tuple[str, ...]
    cwd: str = "."
    timeout_seconds: float | None = 300.0
    source_root_index: int = 0

    def __post_init__(self) -> None:
        if not self.argv or any(not item or "\x00" in item for item in self.argv):
            raise ValueError("verification argv must contain non-empty, NUL-free items")
        if not any(CANDIDATE_FINGERPRINT_PLACEHOLDER in item for item in self.argv):
            raise ValueError(
                "verification argv must contain a {candidate_fingerprint} placeholder"
            )
        if self.timeout_seconds is not None and self.timeout_seconds <= 0:
            raise ValueError("verification timeout must be positive")
        if self.source_root_index < 0:
            raise ValueError("verification source_root_index cannot be negative")
        _validate_relative_path(self.cwd, description="verification cwd")


class VerificationExecutionRecord(BaseModel):
    """Serializable record created by executing one frozen command."""

    model_config = ConfigDict(frozen=True)

    argv: tuple[str, ...]
    cwd: str
    source_root_index: int
    exit_code: int
    stdout: str = ""
    stderr: str = ""
    timed_out: bool = False
    expected_candidate_fingerprint: str = ""
    echoed_candidate_fingerprint: str | None = None
    passed: bool


class CandidateVerificationResult(BaseModel):
    """Outcome of completeness, source grounding, and command execution."""

    model_config = ConfigDict(frozen=True)

    status: VerificationStatus
    admission_scope: str = ADMISSION_SCOPE
    completeness: AdmissionResult
    evidence_checks: list[EvidenceCheck] = Field(default_factory=list)
    execution_records: list[VerificationExecutionRecord] = Field(default_factory=list)
    artifact_hashes: dict[str, str] = Field(default_factory=dict)
    issues: list[str] = Field(default_factory=list)

    @property
    def confirmed(self) -> bool:
        """Only a fully verified candidate is confirmed."""

        return self.status == "verified"


class VerificationExecutor(Protocol):
    """Injectable adapter for deterministic, shell-free command execution."""

    async def run(
        self,
        command: Sequence[str],
        *,
        cwd: Path,
        timeout_seconds: float | None = None,
    ) -> Any: ...


class SubprocessVerificationExecutor:
    """Default executor; arguments are passed directly without a shell."""

    async def run(
        self,
        command: Sequence[str],
        *,
        cwd: Path,
        timeout_seconds: float | None = None,
    ) -> dict[str, Any]:
        argv = tuple(str(item) for item in command)
        try:
            process = await asyncio.create_subprocess_exec(
                *argv,
                cwd=cwd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
        except OSError as exc:
            return {"exit_code": 127, "stderr": str(exc)}

        timed_out = False
        try:
            stdout, stderr = await asyncio.wait_for(
                process.communicate(), timeout=timeout_seconds
            )
        except TimeoutError:
            timed_out = True
            process.kill()
            stdout, stderr = await process.communicate()
        return {
            "exit_code": process.returncode if process.returncode is not None else 1,
            "stdout": stdout.decode("utf-8", errors="replace"),
            "stderr": stderr.decode("utf-8", errors="replace"),
            "timed_out": timed_out,
        }


@dataclass(frozen=True, slots=True)
class _EvidenceClaim:
    citation: str
    excerpt: str


def _validate_relative_path(value: str, *, description: str) -> PurePosixPath:
    if not value or "\x00" in value:
        raise ValueError(f"{description} must be a non-empty relative path")
    windows = PureWindowsPath(value)
    normalized = PurePosixPath(value.replace("\\", "/"))
    if (
        windows.is_absolute()
        or bool(windows.drive)
        or normalized.is_absolute()
        or ".." in windows.parts
        or ".." in normalized.parts
    ):
        raise ValueError(f"{description} must be a relative path without '..' traversal")
    return normalized


def _normalize_roots(
    source_roots: Sequence[str | os.PathLike[str]] | str | os.PathLike[str],
) -> tuple[Path, ...]:
    values: Sequence[str | os.PathLike[str]]
    if isinstance(source_roots, (str, os.PathLike)):
        values = (source_roots,)
    else:
        values = source_roots
    if not values:
        raise ValueError("at least one sanitized source root is required")
    roots: list[Path] = []
    for value in values:
        root = Path(value).expanduser().resolve(strict=True)
        if not root.is_dir():
            raise ValueError(f"source root is not a directory: {root}")
        if root not in roots:
            roots.append(root)
    return tuple(roots)


def _parse_citation(citation: str) -> tuple[PurePosixPath, int, int | None]:
    match = _CITATION.fullmatch(citation.strip())
    if match is None:
        raise ValueError("evidence citation must use file:line or file:start-end")
    relative = _validate_relative_path(match.group("path"), description="evidence file")
    start = int(match.group("start"))
    end_text = match.group("end")
    end = int(end_text) if end_text else None
    if end is not None and end < start:
        raise ValueError("evidence line range ends before it starts")
    return relative, start, end


def _strip_fence(excerpt: str) -> list[str]:
    lines = excerpt.splitlines()
    while lines and not lines[0].strip():
        lines.pop(0)
    while lines and not lines[-1].strip():
        lines.pop()
    if not lines:
        return []
    opening = _FENCE.fullmatch(lines[0])
    if opening is not None:
        fence = opening.group("fence")
        if len(lines) < 2 or lines[-1].strip() != fence:
            raise ValueError("evidence excerpt has an unterminated code fence")
        lines = lines[1:-1]
    return lines


def _claims(candidate: AuditCandidate) -> list[_EvidenceClaim]:
    claims: list[_EvidenceClaim] = []
    structured_citations: set[str] = set()
    for site in candidate.evidence:
        citation = (
            f"{site.file}:{site.line}"
            if site.file and site.line not in (None, "")
            else site.file or "<missing evidence file:line>"
        )
        excerpt = site.excerpt if site.excerpt.strip() else site.excerpt_lines
        claims.append(_EvidenceClaim(citation=citation, excerpt=excerpt))
        structured_citations.add(citation.strip())

    for citation in candidate.evidence_file_line:
        if citation.strip() not in structured_citations:
            claims.append(_EvidenceClaim(citation=citation, excerpt=candidate.evidence_code))
    if not claims:
        claims.append(_EvidenceClaim(citation="<missing evidence file:line>", excerpt=""))
    return claims


def _resolve_evidence(
    relative: PurePosixPath, roots: Sequence[Path]
) -> tuple[int, Path] | None:
    matches: list[tuple[int, Path]] = []
    for index, root in enumerate(roots):
        unresolved = root.joinpath(*relative.parts)
        try:
            resolved = unresolved.resolve(strict=True)
            resolved.relative_to(root)
        except (FileNotFoundError, OSError, ValueError):
            continue
        if resolved.is_file():
            matches.append((index, resolved))
    if len(matches) > 1:
        raise ValueError("evidence path is ambiguous across sanitized source roots")
    return matches[0] if matches else None


def _match_excerpt(
    source_lines: Sequence[str], excerpt_lines: Sequence[str], start: int, end: int | None
) -> tuple[int, int] | None:
    width = len(excerpt_lines)
    required_end = end if end is not None else start
    for offset in range(0, len(source_lines) - width + 1):
        if list(source_lines[offset : offset + width]) != list(excerpt_lines):
            continue
        matched_start = offset + 1
        matched_end = offset + width
        if matched_start <= start <= required_end <= matched_end:
            return matched_start, matched_end
    return None


def _check_evidence(
    claim: _EvidenceClaim, roots: Sequence[Path]
) -> tuple[EvidenceCheck, tuple[str, str] | None]:
    try:
        relative, line, end_line = _parse_citation(claim.citation)
    except ValueError as exc:
        return (
            EvidenceCheck(citation=claim.citation, passed=False, reason=str(exc)),
            None,
        )

    try:
        excerpt = _strip_fence(claim.excerpt)
    except ValueError as exc:
        return (
            EvidenceCheck(
                citation=claim.citation,
                file=relative.as_posix(),
                line=line,
                end_line=end_line,
                passed=False,
                reason=str(exc),
            ),
            None,
        )
    non_empty_lines = sum(bool(item.strip()) for item in excerpt)
    if non_empty_lines < 5:
        return (
            EvidenceCheck(
                citation=claim.citation,
                file=relative.as_posix(),
                line=line,
                end_line=end_line,
                excerpt_line_count=non_empty_lines,
                passed=False,
                reason="evidence excerpt must contain at least 5 non-empty lines",
            ),
            None,
        )

    try:
        resolved = _resolve_evidence(relative, roots)
    except ValueError as exc:
        return (
            EvidenceCheck(
                citation=claim.citation,
                file=relative.as_posix(),
                line=line,
                end_line=end_line,
                excerpt_line_count=non_empty_lines,
                passed=False,
                reason=str(exc),
            ),
            None,
        )
    if resolved is None:
        return (
            EvidenceCheck(
                citation=claim.citation,
                file=relative.as_posix(),
                line=line,
                end_line=end_line,
                excerpt_line_count=non_empty_lines,
                passed=False,
                reason="evidence file does not exist within a sanitized source root",
            ),
            None,
        )

    root_index, path = resolved
    content = path.read_bytes()
    source_lines = content.decode("utf-8", errors="replace").splitlines()
    matched = _match_excerpt(source_lines, excerpt, line, end_line)
    digest = hashlib.sha256(content).hexdigest()
    artifact_key = (
        relative.as_posix()
        if len(roots) == 1
        else f"source-{root_index + 1}/{relative.as_posix()}"
    )
    if matched is None:
        return (
            EvidenceCheck(
                citation=claim.citation,
                file=relative.as_posix(),
                line=line,
                end_line=end_line,
                excerpt_line_count=non_empty_lines,
                passed=False,
                reason="evidence excerpt does not match the cited source location",
                source_root_index=root_index,
                sha256=digest,
            ),
            (artifact_key, digest),
        )
    return (
        EvidenceCheck(
            citation=claim.citation,
            file=relative.as_posix(),
            line=line,
            end_line=end_line,
            excerpt_line_count=non_empty_lines,
            passed=True,
            matched_start_line=matched[0],
            matched_end_line=matched[1],
            source_root_index=root_index,
            sha256=digest,
        ),
        (artifact_key, digest),
    )


def _value(source: Any, name: str, default: Any = None) -> Any:
    if isinstance(source, Mapping):
        return source.get(name, default)
    return getattr(source, name, default)


def _candidate_payload(candidate: AuditCandidate) -> str:
    """Return the canonical JSON representation used for verification binding."""

    return json.dumps(
        candidate.model_dump(mode="json"),
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    )


def candidate_fingerprint(candidate: AuditCandidate) -> str:
    """Return a stable SHA-256 fingerprint for one validated candidate."""

    return hashlib.sha256(_candidate_payload(candidate).encode("utf-8")).hexdigest()


def _coerce_command(value: Any) -> VerificationCommand:
    if isinstance(value, VerificationCommand):
        return value
    if isinstance(value, str):
        raise TypeError("verification commands must be shell-free argument sequences")
    if isinstance(value, Mapping) or hasattr(value, "argv"):
        raw_argv = _value(value, "argv", _value(value, "command"))
        if isinstance(raw_argv, str) or raw_argv is None:
            raise TypeError("verification command argv must be an argument sequence")
        return VerificationCommand(
            tuple(str(item) for item in raw_argv),
            cwd=str(_value(value, "cwd", ".")),
            timeout_seconds=_value(value, "timeout_seconds", 300.0),
            source_root_index=int(_value(value, "source_root_index", 0)),
        )
    return VerificationCommand(tuple(str(item) for item in value))


def _command_cwd(command: VerificationCommand, roots: Sequence[Path]) -> Path:
    if command.source_root_index >= len(roots):
        raise ValueError("verification command references an unknown source root")
    relative = _validate_relative_path(command.cwd, description="verification cwd")
    root = roots[command.source_root_index]
    try:
        cwd = root.joinpath(*relative.parts).resolve(strict=True)
        cwd.relative_to(root)
    except (FileNotFoundError, OSError, ValueError) as exc:
        raise ValueError("verification cwd escapes or is absent from its source root") from exc
    if not cwd.is_dir():
        raise ValueError("verification cwd is not a directory")
    return cwd


def _render_command(
    command: VerificationCommand,
    *,
    fingerprint: str,
    candidate_json: Path,
) -> tuple[str, ...]:
    replacements = {
        CANDIDATE_FINGERPRINT_PLACEHOLDER: fingerprint,
        CANDIDATE_JSON_PLACEHOLDER: str(candidate_json),
    }
    return tuple(_replace_placeholders(item, replacements) for item in command.argv)


def _replace_placeholders(value: str, replacements: Mapping[str, str]) -> str:
    for placeholder, replacement in replacements.items():
        value = value.replace(placeholder, replacement)
    return value


_MISSING = object()


def _echoed_fingerprint(raw: Any, stdout: str) -> str | None:
    explicit = _value(raw, "echoed_candidate_fingerprint", _MISSING)
    if isinstance(explicit, str):
        return explicit
    if explicit is not _MISSING and explicit is not None:
        return None

    try:
        payload = json.loads(stdout)
    except (json.JSONDecodeError, TypeError):
        return None
    if not isinstance(payload, Mapping):
        return None
    echoed = payload.get("echoed_candidate_fingerprint")
    return echoed if isinstance(echoed, str) else None


def _normalize_execution_result(
    raw: Any,
    command: VerificationCommand,
    *,
    executed_argv: tuple[str, ...],
    expected_fingerprint: str,
) -> VerificationExecutionRecord:
    if isinstance(raw, bool):
        exit_code = 0 if raw else 1
        stdout = stderr = ""
        timed_out = False
    else:
        raw_exit_code = _value(raw, "exit_code", _value(raw, "returncode"))
        exit_code = int(raw_exit_code) if raw_exit_code is not None else 1
        stdout = str(_value(raw, "stdout", "") or "")
        stderr = str(_value(raw, "stderr", _value(raw, "error", "")) or "")
        timed_out = bool(_value(raw, "timed_out", False))
    echoed_fingerprint = _echoed_fingerprint(raw, stdout)
    return VerificationExecutionRecord(
        argv=executed_argv,
        cwd=command.cwd,
        source_root_index=command.source_root_index,
        exit_code=exit_code,
        stdout=stdout,
        stderr=stderr,
        timed_out=timed_out,
        expected_candidate_fingerprint=expected_fingerprint,
        echoed_candidate_fingerprint=echoed_fingerprint,
        passed=(
            exit_code == 0
            and not timed_out
            and echoed_fingerprint == expected_fingerprint
        ),
    )


def _record_hash(record: VerificationExecutionRecord) -> str:
    payload = json.dumps(
        record.model_dump(mode="json"),
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    )
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


async def verify_candidate(
    candidate: AuditCandidate | Mapping[str, Any],
    source_roots: Sequence[str | os.PathLike[str]] | str | os.PathLike[str],
    executor: VerificationExecutor | None = None,
    commands: Sequence[VerificationCommand | Sequence[str] | Mapping[str, Any]] = (),
) -> CandidateVerificationResult:
    """Verify one candidate against trusted source roots and frozen commands.

    Candidate-supplied command-like fields and ``poc_verification_plan`` are
    descriptive only.  The function executes only ``commands`` supplied by its
    orchestrator caller.
    """

    item = (
        candidate
        if isinstance(candidate, AuditCandidate)
        else AuditCandidate.model_validate(candidate)
    )
    roots = _normalize_roots(source_roots)
    completeness = evaluate_candidate(item)

    evidence_checks: list[EvidenceCheck] = []
    artifact_hashes: dict[str, str] = {}
    for claim in _claims(item):
        check, artifact = _check_evidence(claim, roots)
        evidence_checks.append(check)
        if artifact is not None:
            artifact_hashes[artifact[0]] = artifact[1]

    evidence_valid = bool(evidence_checks) and all(check.passed for check in evidence_checks)
    if not completeness.admitted or not evidence_valid:
        invalid_issues = [
            f"completeness: {issue}" for issue in completeness.issues
        ]
        invalid_issues.extend(
            f"evidence {check.citation}: {check.reason}"
            for check in evidence_checks
            if not check.passed
        )
        return CandidateVerificationResult(
            status="invalid",
            completeness=completeness,
            evidence_checks=evidence_checks,
            artifact_hashes=artifact_hashes,
            issues=invalid_issues,
        )

    try:
        frozen_commands = tuple(_coerce_command(command) for command in commands)
    except (TypeError, ValueError) as exc:
        return CandidateVerificationResult(
            status="invalid",
            completeness=completeness,
            evidence_checks=evidence_checks,
            artifact_hashes=artifact_hashes,
            issues=[f"invalid verification command configuration: {exc}"],
        )
    if not frozen_commands:
        return CandidateVerificationResult(
            status="unverified",
            completeness=completeness,
            evidence_checks=evidence_checks,
            artifact_hashes=artifact_hashes,
            issues=["no orchestrator-supplied verification command was executed"],
        )

    runner = executor or SubprocessVerificationExecutor()
    records: list[VerificationExecutionRecord] = []
    fingerprint = candidate_fingerprint(item)
    artifact_hashes["candidate.json"] = fingerprint
    with tempfile.TemporaryDirectory(prefix="defuzz-candidate-verification-") as temp_dir:
        candidate_json = Path(temp_dir) / "candidate.json"
        candidate_json.write_text(_candidate_payload(item), encoding="utf-8")
        for index, command in enumerate(frozen_commands, 1):
            cwd = _command_cwd(command, roots)
            executed_argv = _render_command(
                command,
                fingerprint=fingerprint,
                candidate_json=candidate_json,
            )
            try:
                raw = await runner.run(
                    executed_argv, cwd=cwd, timeout_seconds=command.timeout_seconds
                )
                record = _normalize_execution_result(
                    raw,
                    command,
                    executed_argv=executed_argv,
                    expected_fingerprint=fingerprint,
                )
            except Exception as exc:
                record = VerificationExecutionRecord(
                    argv=executed_argv,
                    cwd=command.cwd,
                    source_root_index=command.source_root_index,
                    exit_code=1,
                    stderr=f"executor raised {type(exc).__name__}: {exc}",
                    expected_candidate_fingerprint=fingerprint,
                    passed=False,
                )
            records.append(record)
            artifact_hashes[f"execution-record-{index:04d}.json"] = _record_hash(record)

    all_commands_passed = all(record.passed for record in records)
    verified = item.poc_verified and all_commands_passed
    issues: list[str] = []
    if not item.poc_verified:
        issues.append("candidate.poc_verified is false; execution cannot promote it")
    issues.extend(
        f"verification command {index} timed out"
        for index, record in enumerate(records, 1)
        if record.timed_out
    )
    issues.extend(
        f"verification command {index} failed with exit code {record.exit_code}"
        for index, record in enumerate(records, 1)
        if not record.passed and not record.timed_out and record.exit_code != 0
    )
    issues.extend(
        f"verification command {index} did not echo the expected candidate fingerprint"
        for index, record in enumerate(records, 1)
        if not record.passed
        and not record.timed_out
        and record.exit_code == 0
        and record.echoed_candidate_fingerprint is None
    )
    issues.extend(
        f"verification command {index} echoed the wrong candidate fingerprint"
        for index, record in enumerate(records, 1)
        if not record.passed
        and not record.timed_out
        and record.exit_code == 0
        and record.echoed_candidate_fingerprint is not None
    )
    return CandidateVerificationResult(
        status="verified" if verified else "unverified",
        completeness=completeness,
        evidence_checks=evidence_checks,
        execution_records=records,
        artifact_hashes=artifact_hashes,
        issues=issues,
    )


# Public noun-oriented alias: admission is completeness, not confirmation.
evaluate_completeness = evaluate_candidate

__all__ = [
    "ADMISSION_SCOPE",
    "CANDIDATE_FINGERPRINT_PLACEHOLDER",
    "CANDIDATE_JSON_PLACEHOLDER",
    "CandidateVerificationResult",
    "EvidenceCheck",
    "SubprocessVerificationExecutor",
    "VerificationCommand",
    "VerificationExecutionRecord",
    "VerificationExecutor",
    "candidate_fingerprint",
    "evaluate_completeness",
    "verify_candidate",
]
