"""Part II experiment runner: accepted invariants to executable Go checkers.

The runner deliberately gives an authoring agent a disposable copy of the
source tree.  The repository passed in ``ExperimentPlan.source_root`` is only
ever read; patches and validation are produced from the disposable copy.
"""

from __future__ import annotations

import asyncio
import difflib
import errno
import hashlib
import inspect
import json
import os
import re
import shlex
import shutil
import stat
import tempfile
import uuid
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Any, Protocol

from defuzz_loop.token_usage import (
    BudgetExceeded,
    TokenUsageContext,
    TokenUsageSink,
    current_token_usage_sink,
)

from .agent_backend import AgentBackend, AgentRequest, AgentResult, ExecAgentBackend
from .models import ArtifactRef, ExperimentPlan, StageResult
from .workspace import WorkspaceBuilder

RESULTS_FILENAME = "results.jsonl"
DEFAULT_CHECKER_ROOT = "core/internal/oracle"
DEFAULT_VALIDATION_COMMANDS: tuple[ValidationCommand, ...]


@dataclass(frozen=True, slots=True)
class NormalizedInvariant:
    """One canonical Part I record plus its JSONL provenance."""

    value: dict[str, Any]
    source_path: Path
    source_line: int
    source_sha256: str

    @property
    def invariant_id(self) -> str:
        return str(self.value["invariant_id"])

    @property
    def lineage(self) -> dict[str, Any]:
        return {
            "source_artifact": os.fspath(self.source_path),
            "source_line": self.source_line,
            "source_sha256": self.source_sha256,
            "generation_path": self.value.get("generation_path"),
            "provenance": self.value.get("provenance", []),
        }


@dataclass(frozen=True, slots=True)
class ValidationCommand:
    """A shell-free command executed relative to the isolated workspace."""

    argv: tuple[str, ...]
    cwd: str = "core"
    require_empty_stdout: bool = False

    def __post_init__(self) -> None:
        if not self.argv or any(not item or "\x00" in item for item in self.argv):
            raise ValueError("validation argv must contain non-empty, NUL-free arguments")
        relative = PurePosixPath(self.cwd)
        if relative.is_absolute() or ".." in relative.parts:
            raise ValueError("validation cwd must stay within the isolated workspace")


DEFAULT_VALIDATION_COMMANDS = (
    ValidationCommand(
        ("gofmt", "-l", "*.go"),
        cwd="core/internal/oracle",
        require_empty_stdout=True,
    ),
    ValidationCommand(("go", "test", "./internal/oracle/...")),
    ValidationCommand(("go", "vet", "./internal/oracle/...")),
)


@dataclass(frozen=True, slots=True)
class CommandResult:
    argv: tuple[str, ...]
    cwd: str
    exit_code: int
    stdout: str = ""
    stderr: str = ""
    timed_out: bool = False

    @property
    def success(self) -> bool:
        return self.exit_code == 0 and not self.timed_out


class CommandExecutor(Protocol):
    async def run(
        self, command: Sequence[str], *, cwd: Path, timeout_seconds: float | None = None
    ) -> CommandResult | Mapping[str, Any] | Any: ...


class SubprocessCommandExecutor:
    """Default deterministic command adapter.  It never invokes a shell."""

    async def run(
        self, command: Sequence[str], *, cwd: Path, timeout_seconds: float | None = None
    ) -> CommandResult:
        argv = tuple(str(item) for item in command)
        try:
            process = await asyncio.create_subprocess_exec(
                *argv,
                cwd=cwd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
        except OSError as exc:
            return CommandResult(argv, os.fspath(cwd), 127, stderr=str(exc))

        timed_out = False
        try:
            stdout, stderr = await asyncio.wait_for(
                process.communicate(), timeout=timeout_seconds
            )
        except TimeoutError:
            timed_out = True
            process.kill()
            stdout, stderr = await process.communicate()
        return CommandResult(
            argv=argv,
            cwd=os.fspath(cwd),
            exit_code=process.returncode if process.returncode is not None else 1,
            stdout=stdout.decode("utf-8", errors="replace"),
            stderr=stderr.decode("utf-8", errors="replace"),
            timed_out=timed_out,
        )


@dataclass(slots=True)
class _WorkspaceLease:
    root: Path
    cleanup: Callable[[], Any] | None = None


_TRANSIENT_CLEANUP_ERRNOS = frozenset(
    {errno.EACCES, errno.EBUSY, errno.ENOTEMPTY, errno.EPERM}
)
_CLEANUP_ATTEMPTS = 6


def _rmtree_permission_retry(
    operation: Callable[..., Any], path: str, error: BaseException
) -> None:
    """Retry one rmtree operation after restoring owner permissions."""

    if not isinstance(error, OSError):
        raise error
    if error.errno not in {errno.EACCES, errno.EPERM}:
        raise error
    current = os.stat(path, follow_symlinks=False).st_mode
    os.chmod(path, current | stat.S_IRWXU, follow_symlinks=False)
    operation(path)


async def _remove_workspace_tree(path: Path) -> None:
    """Remove a disposable tree with a bounded, observable retry policy.

    Git hooks and filesystem indexers can briefly recreate bookkeeping paths
    while a checkout is being removed.  A single ``rmtree(ignore_errors=True)``
    can therefore report success while leaving a partial workspace behind.
    Renaming first detaches the live workspace atomically; retries handle only
    recoverable filesystem races, and exhaustion is surfaced to the caller.
    """

    original = path.expanduser().resolve(strict=False)
    cleanup_targets: list[Path] = []
    last_error: OSError | None = None
    observed = original.exists()
    if not observed:
        return

    for attempt in range(_CLEANUP_ATTEMPTS):
        if original.exists():
            tombstone = original.with_name(
                f".{original.name}.cleanup-{uuid.uuid4().hex}"
            )
            try:
                os.replace(original, tombstone)
            except FileNotFoundError:
                pass
            except OSError as exc:
                if exc.errno not in _TRANSIENT_CLEANUP_ERRNOS:
                    raise
                last_error = exc
                if original not in cleanup_targets:
                    cleanup_targets.append(original)
            else:
                cleanup_targets.append(tombstone)

        for target in tuple(dict.fromkeys(cleanup_targets)):
            if not target.exists():
                continue
            try:
                shutil.rmtree(target, onexc=_rmtree_permission_retry)
            except FileNotFoundError:
                continue
            except OSError as exc:
                if exc.errno not in _TRANSIENT_CLEANUP_ERRNOS:
                    raise
                last_error = exc

        # Require a short quiet period before declaring success.  This catches
        # late bookkeeping writes from a hook that started before the rename.
        await asyncio.sleep(min(0.02 * (2**attempt), 0.20))
        remaining = [
            candidate
            for candidate in (original, *cleanup_targets)
            if candidate.exists()
        ]
        if not remaining:
            return

    remaining_text = ", ".join(
        os.fspath(candidate)
        for candidate in (original, *cleanup_targets)
        if candidate.exists()
    )
    detail = f": {last_error}" if last_error is not None else ""
    raise OSError(f"workspace cleanup left residual paths: {remaining_text}{detail}")


@dataclass(frozen=True, slots=True)
class _FileState:
    content: bytes
    sha256: str


def _first_text(record: Mapping[str, Any], names: Sequence[str]) -> str | None:
    for name in names:
        value = record.get(name)
        if value is not None and str(value).strip():
            return str(value).strip()
    return None


def normalize_invariant(
    record: Mapping[str, Any], *, source_path: Path, source_line: int
) -> NormalizedInvariant:
    """Normalize the stable Part I schema and a few pre-schema aliases."""

    if not isinstance(record, Mapping):
        raise TypeError(f"{source_path}:{source_line}: invariant must be a JSON object")
    invariant_id = _first_text(record, ("invariant_id", "id", "checker_id"))
    statement = _first_text(record, ("statement", "invariant", "claim", "text"))
    if invariant_id is None:
        raise ValueError(f"{source_path}:{source_line}: missing invariant_id")
    if statement is None:
        raise ValueError(f"{source_path}:{source_line}: missing invariant statement")

    provenance_value = record.get("provenance", [])
    if provenance_value is None:
        provenance: list[Any] = []
    elif isinstance(provenance_value, list):
        provenance = list(provenance_value)
    else:
        provenance = [provenance_value]

    aliases = {
        "id",
        "checker_id",
        "invariant",
        "claim",
        "text",
        "evidence",
        "observed_behavior",
        "isa",
        "applicable_isas",
        "source_path",
        "source_url",
    }
    canonical_names = {
        "schema_version",
        "invariant_id",
        "statement",
        "observation",
        "generation_path",
        "provenance",
        "compiler",
        "version",
        "target",
        "mechanism",
        "source_kind",
        "source_url_or_path",
        "evidence_snippet",
        "falsifiability",
        "grounding",
        "novelty",
    }
    normalized: dict[str, Any] = {
        "schema_version": int(record.get("schema_version", 1)),
        "invariant_id": invariant_id,
        "statement": statement,
        "observation": _first_text(
            record, ("observation", "evidence", "observed_behavior")
        ),
        "generation_path": _first_text(record, ("generation_path",)) or "unknown",
        "provenance": provenance,
        "compiler": record.get("compiler"),
        "version": record.get("version"),
        "target": record.get("target", record.get("isa", record.get("applicable_isas"))),
        "mechanism": record.get("mechanism"),
        "source_kind": record.get("source_kind"),
        "source_url_or_path": record.get(
            "source_url_or_path", record.get("source_path", record.get("source_url"))
        ),
        "evidence_snippet": record.get("evidence_snippet"),
        "falsifiability": record.get("falsifiability"),
        "grounding": record.get("grounding"),
        "novelty": record.get("novelty"),
    }
    extra = {
        key: value
        for key, value in record.items()
        if key not in canonical_names and key not in aliases
    }
    if extra:
        normalized["extra"] = extra
    payload = json.dumps(record, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return NormalizedInvariant(
        value=normalized,
        source_path=source_path,
        source_line=source_line,
        source_sha256=hashlib.sha256(payload.encode("utf-8")).hexdigest(),
    )


def load_accepted_invariants(path: str | os.PathLike[str]) -> list[NormalizedInvariant]:
    source = Path(path).expanduser().resolve(strict=True)
    if source.is_dir():
        source = (source / "accepted-invariants.jsonl").resolve(strict=True)
    records: list[NormalizedInvariant] = []
    seen: set[str] = set()
    for line_number, line in enumerate(source.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        try:
            raw = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"{source}:{line_number}: invalid JSON: {exc.msg}") from exc
        item = normalize_invariant(raw, source_path=source, source_line=line_number)
        if item.invariant_id in seen:
            raise ValueError(
                f"{source}:{line_number}: duplicate invariant_id {item.invariant_id!r}"
            )
        seen.add(item.invariant_id)
        records.append(item)
    return records


def render_checker_prompt(
    invariant: NormalizedInvariant, *, checker_root: str = DEFAULT_CHECKER_ROOT
) -> str:
    """Render a contract-grounded, repository-specific authoring prompt."""

    payload = json.dumps(invariant.value, ensure_ascii=False, indent=2, sort_keys=True)
    return f"""You are implementing exactly one DeFuzz invariant checker in an isolated,
disposable copy.

Accepted invariant (preserve its ID and semantics):
```json
{payload}
```

Repository contract (inspect the current files; do not invent a parallel API):
- `core/internal/oracle/invariant.go` owns `InvariantChecker`: `ID() string`,
  `Category() InvariantCategory`, and `Check(*CheckContext) InvariantResult`.
- Return exactly one of `VerdictPass`, `VerdictFail`, `VerdictNotApplicable`, or
  `VerdictError`, with deterministic Evidence/Reason/Detail. Missing prerequisites are
  NotApplicable; infrastructure/inspection failures are Error. `Check(nil)` must not panic.
- `core/internal/oracle/metadata.go` is the checker metadata SSOT. Add the checker ID,
  ApplicableISAs, Mode, Cost, and Category there; do not create another metadata table.
- Register the checker in the relevant mechanism's `mechanism().Checkers`. If this is a new
  mechanism, follow the existing mechanism constructor and `Register` pattern.

Required implementation and tests:
1. Work only under `{checker_root}`. Keep the patch focused on this invariant.
2. Add table-driven or focused unit tests that independently exercise Pass, Fail,
   NotApplicable (N/A), Error, and nil/missing-context behavior. These are mandatory even
   if one fixture needs a small fake Inspector or Executor.
3. Keep the checker deterministic and safe to call repeatedly. Put expensive per-analysis
   state in `CheckContext.Cache`, never mutable receiver state.
4. Format Go files. Do not weaken or delete existing checks/tests to obtain a pass.

Implement and test the checker now. Do not edit the source checkout outside this workspace.
"""


def _repair_prompt(
    invariant: NormalizedInvariant, attempt: int, failures: Sequence[Mapping[str, Any]]
) -> str:
    details = json.dumps(list(failures), ensure_ascii=False, indent=2, sort_keys=True)
    return f"""Repair the current implementation for invariant {invariant.invariant_id}.
This is bounded repair attempt {attempt}. Keep the existing InvariantChecker, metadata SSOT,
mechanism registration, and mandatory Pass/Fail/NotApplicable/Error/nil tests intact.
Do not edit outside core/internal/oracle and do not weaken tests.

Deterministic validation feedback from the previous attempt:
```json
{details}
```

Apply the smallest correct fix, format the Go files, and stop.
"""


def _slug(value: str) -> str:
    stem = re.sub(r"[^a-zA-Z0-9._-]+", "-", value).strip(".-").lower() or "invariant"
    digest = hashlib.sha256(value.encode("utf-8")).hexdigest()[:8]
    return f"{stem[:64]}-{digest}"


def _is_within(path: Path, root: Path) -> bool:
    try:
        path.resolve().relative_to(root.resolve())
    except ValueError:
        return False
    return True


def _snapshot(root: Path) -> dict[str, _FileState]:
    result: dict[str, _FileState] = {}
    for path in sorted(candidate for candidate in root.rglob("*") if candidate.is_file()):
        relative = path.relative_to(root)
        if relative.parts and relative.parts[0] in {".git", ".defuzz-agent"}:
            continue
        content = path.read_bytes()
        result[relative.as_posix()] = _FileState(
            content=content, sha256=hashlib.sha256(content).hexdigest()
        )
    return result


def _changes(
    before: Mapping[str, _FileState], after: Mapping[str, _FileState]
) -> list[dict[str, Any]]:
    changed: list[dict[str, Any]] = []
    for path in sorted(set(before) | set(after)):
        old = before.get(path)
        new = after.get(path)
        if old is not None and new is not None and old.sha256 == new.sha256:
            continue
        kind = "added" if old is None else "deleted" if new is None else "modified"
        changed.append(
            {
                "path": path,
                "change_type": kind,
                "sha256_before": old.sha256 if old else None,
                "sha256_after": new.sha256 if new else None,
                "size_before": len(old.content) if old else 0,
                "size_after": len(new.content) if new else 0,
            }
        )
    return changed


def _patch(before: Mapping[str, _FileState], after: Mapping[str, _FileState]) -> str:
    chunks: list[str] = []
    for change in _changes(before, after):
        path = str(change["path"])
        old = before.get(path)
        new = after.get(path)
        old_bytes = old.content if old else b""
        new_bytes = new.content if new else b""
        if b"\x00" in old_bytes or b"\x00" in new_bytes:
            chunks.append(f"Binary files a/{path} and b/{path} differ\n")
            continue
        old_lines = old_bytes.decode("utf-8", errors="replace").splitlines(keepends=True)
        new_lines = new_bytes.decode("utf-8", errors="replace").splitlines(keepends=True)
        chunks.extend(
            difflib.unified_diff(
                old_lines,
                new_lines,
                fromfile=f"a/{path}" if old else "/dev/null",
                tofile=f"b/{path}" if new else "/dev/null",
            )
        )
    return "".join(chunks)


def _atomic_write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    try:
        temporary.write_text(text, encoding="utf-8")
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def _artifact(path: Path, output_dir: Path, kind: str) -> dict[str, Any]:
    return ArtifactRef.from_path(path, base_dir=output_dir, kind=kind).to_dict()


def _reference_path(path: Path, output_dir: Path) -> str:
    """Return a portable ref, including for an ambient ledger above the stage dir."""

    return Path(os.path.relpath(path.resolve(), start=output_dir.resolve())).as_posix()


def _value(source: Any, name: str, default: Any = None) -> Any:
    if isinstance(source, Mapping):
        return source.get(name, default)
    return getattr(source, name, default)


def _normalize_agent_result(result: Any) -> AgentResult:
    if isinstance(result, AgentResult):
        return result
    if isinstance(result, str):
        return AgentResult(success=True, final=result)
    return AgentResult(
        success=bool(_value(result, "success", True)),
        final=_value(result, "final", _value(result, "output")),
        events=list(_value(result, "events", []) or []),
        usage=_value(result, "usage"),
        exit_code=_value(result, "exit_code"),
        timed_out=bool(_value(result, "timed_out", False)),
        error=_value(result, "error"),
        raw_stdout=str(_value(result, "raw_stdout", _value(result, "stdout", "")) or ""),
        raw_stderr=str(_value(result, "raw_stderr", _value(result, "stderr", "")) or ""),
    )


def _normalize_command_result(raw: Any, spec: ValidationCommand, cwd: Path) -> CommandResult:
    if isinstance(raw, CommandResult):
        return raw
    if isinstance(raw, bool):
        return CommandResult(spec.argv, os.fspath(cwd), 0 if raw else 1)
    exit_code = _value(raw, "exit_code", _value(raw, "returncode", 0))
    return CommandResult(
        argv=tuple(_value(raw, "argv", spec.argv)),
        cwd=os.fspath(_value(raw, "cwd", cwd)),
        exit_code=int(exit_code),
        stdout=str(_value(raw, "stdout", "") or ""),
        stderr=str(_value(raw, "stderr", "") or ""),
        timed_out=bool(_value(raw, "timed_out", False)),
    )


def _trim(value: str, limit: int = 16_000) -> str:
    if len(value) <= limit:
        return value
    return value[:limit] + f"\n...[truncated {len(value) - limit} characters]"


def _command_specs(parameters: Mapping[str, Any]) -> tuple[ValidationCommand, ...]:
    configured = parameters.get("validation_commands")
    if configured is None:
        return DEFAULT_VALIDATION_COMMANDS
    if not isinstance(configured, Sequence) or isinstance(configured, (str, bytes)):
        raise TypeError("validation_commands must be a sequence")
    commands: list[ValidationCommand] = []
    for item in configured:
        if isinstance(item, ValidationCommand):
            commands.append(item)
            continue
        if isinstance(item, str):
            commands.append(ValidationCommand(tuple(shlex.split(item))))
            continue
        if isinstance(item, Mapping):
            raw_argv = item.get("argv", item.get("command"))
            argv = (
                tuple(shlex.split(raw_argv))
                if isinstance(raw_argv, str)
                else tuple(raw_argv or ())
            )
            commands.append(
                ValidationCommand(
                    argv,
                    cwd=str(item.get("cwd", "core")),
                    require_empty_stdout=bool(item.get("require_empty_stdout", False)),
                )
            )
            continue
        commands.append(ValidationCommand(tuple(str(value) for value in item)))
    if not commands:
        raise ValueError("at least one validation command is required")
    return tuple(commands)


class CheckerAuthoringRunner:
    """Dependency-injectable Part II runner used by the public ``run`` adapter."""

    def __init__(
        self,
        *,
        backend: AgentBackend | Any,
        workspace_factory: Any = None,
        command_executor: CommandExecutor | Any = None,
    ) -> None:
        self.backend = backend
        self.workspace_factory = workspace_factory
        self.command_executor = command_executor or SubprocessCommandExecutor()

    async def _workspace(
        self,
        source_root: Path,
        output_dir: Path,
        invariant: NormalizedInvariant,
        allowlist: Sequence[str],
    ) -> _WorkspaceLease:
        output_inside_source = _is_within(output_dir, source_root)
        base = None if output_inside_source else output_dir / "checker-authoring" / ".workspaces"
        if base is not None:
            base.mkdir(parents=True, exist_ok=True)
        destination = Path(
            tempfile.mkdtemp(prefix=f"{_slug(invariant.invariant_id)}-", dir=base)
        ).resolve()
        try:
            if self.workspace_factory is None:
                produced: Any = WorkspaceBuilder(
                    source_root, allowlist=allowlist
                ).materialize(destination)
            else:
                creator = next(
                    (
                        getattr(self.workspace_factory, name)
                        for name in ("create", "materialize", "build")
                        if callable(getattr(self.workspace_factory, name, None))
                    ),
                    self.workspace_factory if callable(self.workspace_factory) else None,
                )
                if creator is None:
                    raise TypeError(
                        "workspace_factory must be callable or expose "
                        "create/materialize/build"
                    )
                try:
                    produced = creator(
                        source_root=source_root,
                        destination=destination,
                        invariant=invariant.value,
                    )
                except TypeError:
                    produced = creator(source_root, destination, invariant.value)
                if inspect.isawaitable(produced):
                    produced = await produced

            root_value = _value(produced, "root", _value(produced, "path", produced))
            root = Path(root_value).expanduser().resolve(strict=True)
            if not root.is_dir():
                raise ValueError(
                    f"workspace factory did not produce a directory: {root}"
                )
            if _is_within(root, source_root) or _is_within(source_root, root):
                raise ValueError(
                    "isolated workspace must not overlap the source checkout"
                )
            provided_cleanup = getattr(produced, "cleanup", None)
        except BaseException:
            await _remove_workspace_tree(destination)
            raise

        async def remove_workspace() -> None:
            for target in dict.fromkeys((root, destination)):
                await _remove_workspace_tree(target)

        cleanup: Callable[[], Any] | None = remove_workspace
        if callable(provided_cleanup):

            async def cleanup_with_provider() -> None:
                try:
                    result = provided_cleanup()
                    if inspect.isawaitable(result):
                        await result
                finally:
                    await remove_workspace()

            cleanup = cleanup_with_provider
        return _WorkspaceLease(root=root, cleanup=cleanup)

    async def _invoke_agent(
        self, request: AgentRequest
    ) -> AgentResult:
        method = getattr(self.backend, "run", None)
        if callable(method):
            result = method(request)
        else:
            complete = getattr(self.backend, "complete", None)
            if not callable(complete):
                raise TypeError("backend must expose async run(request) or complete(prompt, ...)")
            result = complete(
                request.prompt,
                cwd=request.cwd,
                output_dir=request.output_dir,
                timeout_seconds=request.timeout_seconds,
                writable=request.writable,
                token_sink=request.token_sink,
                metadata=request.metadata,
            )
        if inspect.isawaitable(result):
            result = await result
        return _normalize_agent_result(result)

    async def _validate(
        self,
        workspace: Path,
        commands: Sequence[ValidationCommand],
        timeout_seconds: float,
    ) -> tuple[bool, list[dict[str, Any]]]:
        results: list[dict[str, Any]] = []
        passed = True
        for spec in commands:
            cwd = (workspace / spec.cwd).resolve()
            argv: list[str] = []
            for argument in spec.argv:
                if any(marker in argument for marker in ("*", "?", "[")):
                    matches = sorted(
                        path.relative_to(cwd).as_posix()
                        for path in cwd.glob(argument)
                        if path.is_file() and _is_within(path, cwd)
                    )
                    argv.extend(matches or [argument])
                else:
                    argv.append(argument)
            effective = ValidationCommand(
                tuple(argv), cwd=spec.cwd, require_empty_stdout=spec.require_empty_stdout
            )
            if not _is_within(cwd, workspace) or not cwd.is_dir():
                outcome = CommandResult(
                    effective.argv,
                    os.fspath(cwd),
                    2,
                    stderr="validation cwd is missing or unsafe",
                )
            else:
                raw = self.command_executor.run(
                    effective.argv, cwd=cwd, timeout_seconds=timeout_seconds
                )
                if inspect.isawaitable(raw):
                    raw = await raw
                outcome = _normalize_command_result(raw, effective, cwd)
            success = outcome.success and not (
                spec.require_empty_stdout and bool(outcome.stdout.strip())
            )
            passed = passed and success
            results.append(
                {
                    "argv": list(outcome.argv),
                    "cwd": spec.cwd,
                    "exit_code": outcome.exit_code,
                    "timed_out": outcome.timed_out,
                    "require_empty_stdout": spec.require_empty_stdout,
                    "status": "passed" if success else "failed",
                    "stdout": _trim(outcome.stdout),
                    "stderr": _trim(outcome.stderr),
                }
            )
        return passed, results

    async def _one(
        self,
        *,
        plan: ExperimentPlan,
        repetition: int,
        output_dir: Path,
        invariant: NormalizedInvariant,
        source_root: Path,
        commands: Sequence[ValidationCommand],
        max_attempts: int,
        checker_root: str,
        allowed_paths: Sequence[str],
        workspace_allowlist: Sequence[str],
        token_sink: TokenUsageSink,
        timeout_seconds: float,
        keep_workspace: bool,
    ) -> tuple[dict[str, Any], list[Path]]:
        slug = _slug(invariant.invariant_id)
        artifact_dir = output_dir / "checker-authoring" / slug
        artifact_dir.mkdir(parents=True, exist_ok=True)
        lease = await self._workspace(
            source_root, output_dir, invariant, workspace_allowlist
        )
        patch_paths: list[Path] = []
        attempts: list[dict[str, Any]] = []
        token_refs: list[dict[str, Any]] = []
        first_status = "failed"
        final_status = "failed"
        first_patch_ref: dict[str, Any] | None = None
        final_patch_ref: dict[str, Any] | None = None
        baseline: dict[str, _FileState] = {}
        final_snapshot: dict[str, _FileState] = {}
        previous_failures: list[Mapping[str, Any]] = []
        stopped_reason: str | None = None

        try:
            baseline = _snapshot(lease.root)
            final_snapshot = baseline
            for attempt_number in range(1, max_attempts + 1):
                try:
                    token_sink.check_budget()
                except BudgetExceeded as exc:
                    stopped_reason = str(exc)
                    break
                agent_output = lease.root / ".defuzz-agent" / f"attempt-{attempt_number:03d}"
                attempt_context = token_sink.context.with_overrides(
                    stage=f"{invariant.invariant_id}:attempt-{attempt_number}"
                )
                existing_call_ids = {record.call_id for record in token_sink.records}
                prompt = (
                    render_checker_prompt(invariant, checker_root=checker_root)
                    if attempt_number == 1
                    else _repair_prompt(invariant, attempt_number, previous_failures)
                )
                request = AgentRequest(
                    prompt=prompt,
                    cwd=lease.root,
                    output_dir=agent_output,
                    timeout_seconds=timeout_seconds,
                    writable=True,
                    token_sink=token_sink,
                    metadata={
                        "run_id": plan.run_id,
                        "experiment": plan.experiment,
                        "variant": plan.variant,
                        "part": "checker-authoring",
                        "stage": f"{invariant.invariant_id}:attempt-{attempt_number}",
                    },
                )
                agent_result = await self._invoke_agent(request)
                attempt_records = [
                    record
                    for record in token_sink.records
                    if record.call_id not in existing_call_ids
                ]
                if not attempt_records:
                    attempt_records = [token_sink.record_external_usage(
                        agent_result.usage,
                        context=attempt_context,
                        success=agent_result.success,
                        error_type="AgentError" if not agent_result.success else None,
                    )]
                refs = [
                    {
                        "path": _reference_path(token_sink.path, output_dir),
                        "call_id": record.call_id,
                        "attempt": attempt_number,
                    }
                    for record in attempt_records
                ]
                token_refs.extend(refs)

                current = _snapshot(lease.root)
                if attempt_number == 1:
                    first_patch_path = artifact_dir / "first.patch"
                    _atomic_write(first_patch_path, _patch(baseline, current))
                    patch_paths.append(first_patch_path)
                    first_patch_ref = _artifact(first_patch_path, output_dir, "first-patch")

                changed = _changes(baseline, current)
                allowed_errors = [
                    {
                        "type": "disallowed-file",
                        "path": str(item["path"]),
                        "message": "checker authoring may only change configured checker paths",
                    }
                    for item in changed
                    if not any(
                        item["path"] == prefix.rstrip("/")
                        or str(item["path"]).startswith(prefix.rstrip("/") + "/")
                        for prefix in allowed_paths
                    )
                ]
                validation_ok = False
                validation: list[dict[str, Any]] = []
                if agent_result.success:
                    validation_ok, validation = await self._validate(
                        lease.root, commands, timeout_seconds
                    )
                else:
                    validation.append(
                        {
                            "status": "failed",
                            "type": "agent-error",
                            "error": agent_result.error or "agent backend failed",
                        }
                    )
                validation_ok = validation_ok and not allowed_errors
                previous_failures = [
                    *allowed_errors,
                    *(item for item in validation if item.get("status") != "passed"),
                ]
                status = "passed" if validation_ok else "failed"
                if attempt_number == 1:
                    first_status = status
                attempts.append(
                    {
                        "attempt": attempt_number,
                        "agent_status": "passed" if agent_result.success else "failed",
                        "agent_error": agent_result.error,
                        "validation_status": status,
                        "validation": validation,
                        "policy_errors": allowed_errors,
                        "changed_files": [item["path"] for item in changed],
                        "token_refs": refs,
                    }
                )
                final_snapshot = current
                if validation_ok:
                    final_status = "passed"
                    break

            if first_patch_ref is None:
                first_patch_path = artifact_dir / "first.patch"
                _atomic_write(first_patch_path, _patch(baseline, final_snapshot))
                patch_paths.append(first_patch_path)
                first_patch_ref = _artifact(first_patch_path, output_dir, "first-patch")
            final_patch_path = artifact_dir / "final.patch"
            _atomic_write(final_patch_path, _patch(baseline, final_snapshot))
            patch_paths.append(final_patch_path)
            final_patch_ref = _artifact(final_patch_path, output_dir, "final-patch")
            changes = _changes(baseline, final_snapshot)
            record = {
                "schema_version": 1,
                "run_id": plan.run_id,
                "experiment": plan.experiment,
                "variant": plan.variant,
                "repetition": repetition,
                "invariant_id": invariant.invariant_id,
                "invariant": invariant.value,
                "lineage": invariant.lineage,
                "first_pass_status": first_status,
                "final_status": final_status,
                "status": final_status,
                "attempt_count": len(attempts),
                "attempt_cap": max_attempts,
                "attempts": attempts,
                "files": [item["path"] for item in changes],
                "file_changes": changes,
                "first_patch": first_patch_ref,
                "final_patch": final_patch_ref,
                "token_refs": token_refs,
                "stopped_reason": stopped_reason,
                "budget_exhausted": stopped_reason is not None,
            }
            return record, patch_paths
        finally:
            if not keep_workspace and lease.cleanup is not None:
                cleanup_result = lease.cleanup()
                if inspect.isawaitable(cleanup_result):
                    await cleanup_result

    async def run(
        self, plan: ExperimentPlan, repetition: int, output_dir: str | os.PathLike[str]
    ) -> StageResult:
        destination = Path(output_dir).expanduser().resolve(strict=False)
        destination.mkdir(parents=True, exist_ok=True)
        parameters = plan.parameters
        source_root = (
            Path(plan.source_root)
            if plan.source_root is not None
            else Path(__file__).resolve().parents[3]
        ).expanduser().resolve(strict=True)
        raw_input = parameters.get(
            "accepted_invariants",
            parameters.get("accepted_invariants_path", parameters.get("invariants")),
        )
        if raw_input is None:
            raw_input = destination / "accepted-invariants.jsonl"
        input_path = Path(raw_input).expanduser()
        if not input_path.is_absolute():
            input_path = source_root / input_path
        if input_path.is_dir():
            input_path = input_path / "accepted-invariants.jsonl"

        results_path = destination / RESULTS_FILENAME
        if not plan.policy.use_invariants or not plan.policy.use_dedicated_checkers:
            _atomic_write(results_path, "")
            return StageResult(
                stage="checker-authoring",
                status="skipped",
                artifacts=[
                    ArtifactRef.from_path(results_path, base_dir=destination, kind="results")
                ],
                metrics={
                    "invariants": 0,
                    "first_passed": 0,
                    "final_passed": 0,
                    "failed": 0,
                    "budget_exhausted": 0,
                    "unprocessed": 0,
                },
                messages=["variant policy disables dedicated invariant checkers"],
            )

        invariants = load_accepted_invariants(input_path)
        max_attempts = int(parameters.get("max_attempts", parameters.get("attempt_cap", 3)))
        if not 1 <= max_attempts <= 10:
            raise ValueError("max_attempts must be between 1 and 10")
        timeout_seconds = float(
            parameters.get("validation_timeout_seconds", plan.budget.timeout_seconds)
        )
        if timeout_seconds <= 0:
            raise ValueError("validation_timeout_seconds must be positive")
        checker_root = str(parameters.get("checker_root", DEFAULT_CHECKER_ROOT)).strip("/")
        checker_path = PurePosixPath(checker_root)
        if checker_path.is_absolute() or ".." in checker_path.parts:
            raise ValueError("checker_root must be relative to source_root")
        allowed_paths = tuple(
            str(item).strip("/")
            for item in parameters.get("allowed_change_paths", [checker_root])
        )
        workspace_allowlist = tuple(
            str(item)
            for item in parameters.get(
                "workspace_allowlist", [checker_path.parts[0] if checker_path.parts else "core"]
            )
        )
        commands = _command_specs(parameters)
        keep_workspace = bool(parameters.get("keep_workspaces", False))
        provider = str(getattr(self.backend, "provider", "agent"))
        token_sink = current_token_usage_sink()
        owns_token_sink = token_sink is None
        if token_sink is None:
            token_sink = TokenUsageSink(
                destination / "token_usage.jsonl",
                context=TokenUsageContext(
                    run_id=plan.run_id,
                    experiment=plan.experiment,
                    variant=plan.variant,
                    part="checker-authoring",
                    stage="agent",
                    agent=provider,
                    provider=provider,
                    model=getattr(self.backend, "model", None),
                ),
                token_budget=plan.budget.token_budget,
            )

        rows: list[dict[str, Any]] = []
        patch_paths: list[Path] = []
        for invariant in invariants:
            row, paths = await self._one(
                plan=plan,
                repetition=repetition,
                output_dir=destination,
                invariant=invariant,
                source_root=source_root,
                commands=commands,
                max_attempts=max_attempts,
                checker_root=checker_root,
                allowed_paths=allowed_paths,
                workspace_allowlist=workspace_allowlist,
                token_sink=token_sink,
                timeout_seconds=timeout_seconds,
                keep_workspace=keep_workspace,
            )
            rows.append(row)
            patch_paths.extend(paths)

        _atomic_write(
            results_path,
            "".join(
                json.dumps(row, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
                + "\n"
                for row in rows
            ),
        )
        artifacts = [
            ArtifactRef.from_path(results_path, base_dir=destination, kind="results"),
            *(
                ArtifactRef.from_path(path, base_dir=destination, kind="patch")
                for path in patch_paths
            ),
        ]
        if owns_token_sink:
            token_summary_json = destination / "token_usage_summary.json"
            token_summary_csv = destination / "token_usage_summary.csv"
            token_sink.finalize(json_path=token_summary_json, csv_path=token_summary_csv)
            artifacts.extend(
                [
                    ArtifactRef.from_path(
                        token_summary_json,
                        base_dir=destination,
                        kind="token-usage-summary",
                    ),
                    ArtifactRef.from_path(
                        token_summary_csv,
                        base_dir=destination,
                        kind="token-usage-summary",
                    ),
                ]
            )
        if owns_token_sink and token_sink.path.is_file():
            artifacts.append(
                ArtifactRef.from_path(token_sink.path, base_dir=destination, kind="token-usage")
            )
        first_passed = sum(row["first_pass_status"] == "passed" for row in rows)
        final_passed = sum(row["final_status"] == "passed" for row in rows)
        failed = sum(row["final_status"] != "passed" for row in rows)
        budget_exhausted = sum(bool(row["budget_exhausted"]) for row in rows)
        unprocessed = sum(int(row["attempt_count"]) == 0 for row in rows)
        if failed == 0 and budget_exhausted == 0 and unprocessed == 0:
            stage_status = "completed"
        elif final_passed:
            stage_status = "partial"
        else:
            stage_status = "failed"
        return StageResult(
            stage="checker-authoring",
            status=stage_status,
            artifacts=artifacts,
            metrics={
                "invariants": len(rows),
                "first_passed": first_passed,
                "first_pass_rate": first_passed / len(rows) if rows else 0.0,
                "final_passed": final_passed,
                "final_pass_rate": final_passed / len(rows) if rows else 0.0,
                "failed": failed,
                "budget_exhausted": budget_exhausted,
                "unprocessed": unprocessed,
                "agent_attempts": sum(int(row["attempt_count"]) for row in rows),
            },
            metadata={
                "input_path": os.fspath(input_path),
                "results_path": os.fspath(results_path),
                "attempt_cap": max_attempts,
            },
        )


async def run(
    plan: ExperimentPlan,
    repetition: int,
    output_dir: str | os.PathLike[str],
    backend: AgentBackend | Any = None,
) -> StageResult:
    """Run Part II with the shared experiment-stage signature."""

    normalized_plan = (
        plan if isinstance(plan, ExperimentPlan) else ExperimentPlan.from_dict(plan)
    )
    selected_backend = backend or ExecAgentBackend(
        binary=str(normalized_plan.parameters.get("agent_binary", "traex")),
        model=normalized_plan.parameters.get("model"),
    )
    runner = CheckerAuthoringRunner(
        backend=selected_backend,
        workspace_factory=getattr(selected_backend, "workspace_factory", None),
        command_executor=getattr(selected_backend, "command_executor", None),
    )
    try:
        return await runner.run(normalized_plan, repetition, output_dir)
    except (OSError, TypeError, ValueError, json.JSONDecodeError) as exc:
        return StageResult(
            stage="checker-authoring",
            status="failed",
            errors=[f"{type(exc).__name__}: {exc}"],
        )


__all__ = [
    "CheckerAuthoringRunner",
    "CommandExecutor",
    "CommandResult",
    "DEFAULT_VALIDATION_COMMANDS",
    "NormalizedInvariant",
    "RESULTS_FILENAME",
    "SubprocessCommandExecutor",
    "ValidationCommand",
    "load_accepted_invariants",
    "normalize_invariant",
    "render_checker_prompt",
    "run",
]
