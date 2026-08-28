"""Part II experiment runner: accepted invariants to executable Go checkers.

The runner deliberately gives an authoring agent a disposable copy of the
source tree.  The repository passed in ``ExperimentPlan.source_root`` is only
ever read; patches and validation are produced from the disposable copy.
"""

from __future__ import annotations

import asyncio
import difflib
import errno
import fnmatch
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
from typing import Any, Literal, Protocol, cast

from defuzz_loop.audit_schema import normalize_isa, normalize_mechanism
from defuzz_loop.checker_bundle import (
    CHECKER_BUNDLE_MANIFEST_FILENAME,
    CheckerBundleManifest,
    compute_bundle_id,
)
from defuzz_loop.token_usage import (
    BudgetExceeded,
    TokenUsageContext,
    TokenUsageSink,
    current_token_usage_sink,
)

from .agent_backend import AgentBackend, AgentRequest, AgentResult, ExecAgentBackend
from .checker_reuse import reusable_checker_ids
from .models import ArtifactRef, ExperimentPlan, StageResult
from .workspace import (
    WorkspaceBuilder,
    validate_agent_path_isolation,
    validate_disjoint_input_roots,
)

RESULTS_FILENAME = "results.jsonl"
CHECKER_INPUT_SCOPE_FILENAME = "checker-input-scope.json"
SCOPED_ACCEPTED_INVARIANTS_FILENAME = "scoped-accepted-invariants.jsonl"
CHECKER_BUNDLE_PATCH_FILENAME = "checker-bundle.patch"
CHECKER_CATALOG_FILENAME = "checker-catalog.json"
DEFAULT_DISPATCHER_PATH = "bin/defuzz-candidate-dispatcher"
DEFAULT_CHECKER_ROOT = "core/internal/oracle"
DEFAULT_SHARED_INTEGRATION_PATHS = (
    "core/internal/oracle/metadata.go",
    "core/internal/oracle/registry.go",
    "core/internal/oracle/canary_oracle.go",
    "core/internal/oracle/ibt_oracle.go",
    "core/internal/oracle/fortify_oracle.go",
)
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
    def reused_checker_ids(self) -> list[str]:
        # Part II independently derives reuse from the normalized statement.
        # Part I's metadata is provenance only and is never trusted to skip
        # authoring or validation.
        return reusable_checker_ids(str(self.value.get("statement", "")))

    @property
    def declared_reused_checker_ids(self) -> list[str]:
        value = self.value.get("reused_checker_ids", [])
        if not isinstance(value, list) or not all(
            isinstance(item, str) and item for item in value
        ):
            return []
        return value

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
        ("gofmt", "-w", "*.go"),
        cwd="core/internal/oracle",
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


def _resolve_agent_deny_paths(
    parameters: Mapping[str, Any], source_root: Path,
) -> tuple[list[Path], bool]:
    raw_paths = parameters.get("deny_read_paths")
    if raw_paths is None:
        raw_paths = []
    elif isinstance(raw_paths, (str, os.PathLike)):
        raw_paths = [raw_paths]
    else:
        raw_paths = list(raw_paths)
    raw_reference = parameters.get("reference_root")
    if raw_reference is None and parameters.get("findings_deny_path") is not None:
        raw_reference = Path(parameters["findings_deny_path"]).parent
    mandatory = [source_root]
    if raw_reference is not None:
        mandatory.append(Path(raw_reference))
        validate_disjoint_input_roots(
            [("source_root", source_root), ("reference_root", raw_reference)]
        )
    paths = list(
        dict.fromkeys([*mandatory, *(Path(item) for item in raw_paths)])
    )
    paths = [path.expanduser().resolve(strict=False) for path in paths]
    return paths, bool(parameters.get("require_host_read_isolation", False))


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
            stdout, stderr = await asyncio.wait_for(process.communicate(), timeout=timeout_seconds)
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


_TRANSIENT_CLEANUP_ERRNOS = frozenset({errno.EACCES, errno.EBUSY, errno.ENOTEMPTY, errno.EPERM})
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
            tombstone = original.with_name(f".{original.name}.cleanup-{uuid.uuid4().hex}")
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
        remaining = [candidate for candidate in (original, *cleanup_targets) if candidate.exists()]
        if not remaining:
            return

    remaining_text = ", ".join(
        os.fspath(candidate) for candidate in (original, *cleanup_targets) if candidate.exists()
    )
    detail = f": {last_error}" if last_error is not None else ""
    raise OSError(f"workspace cleanup left residual paths: {remaining_text}{detail}")


@dataclass(frozen=True, slots=True)
class _FileState:
    content: bytes
    sha256: str
    mode: int


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
        "reused_checker_ids",
    }
    normalized: dict[str, Any] = {
        "schema_version": int(record.get("schema_version", 1)),
        "invariant_id": invariant_id,
        "statement": statement,
        "observation": _first_text(record, ("observation", "evidence", "observed_behavior")),
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
        "reused_checker_ids": record.get("reused_checker_ids", []),
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


def _normalized_scope_values(
    parameters: Mapping[str, Any],
    *,
    plural: str,
    singular: str,
    normalize: Callable[[str], str],
) -> tuple[str, ...]:
    """Read one optional plural scope with a legacy singular alias."""

    raw = parameters.get(plural)
    if raw is None or (isinstance(raw, (list, tuple)) and not raw):
        raw = parameters.get(singular)
    if raw is None:
        return ()
    values = [raw] if isinstance(raw, str) else list(raw)
    normalized: list[str] = []
    for value in values:
        if not isinstance(value, str):
            raise TypeError(f"{plural} must contain only strings")
        item = normalize(value)
        if not item:
            raise ValueError(f"{plural} must contain only non-empty values")
        normalized.append(item)
    return tuple(sorted(set(normalized)))


_ISA_TOKEN_PATTERNS = (
    (r"^(?:x86-64|amd64|x64)(?:-|$)", "x86_64"),
    (r"^(?:i[3-6]86|x86-32)(?:-|$)", "i386"),
    (r"^x86(?:-|$)", "x86"),
    (r"^(?:riscv64|risc-v64|risc-v-64)(?:[a-z0-9]*)(?:-|$)", "riscv64"),
    (r"^(?:riscv32|risc-v32|risc-v-32)(?:[a-z0-9]*)(?:-|$)", "riscv32"),
    (r"^(?:riscv|risc-v)(?:-|$)", "riscv"),
    (r"^(?:aarch64|arm64)(?:-|$)", "aarch64"),
    (r"^(?:arm|arm32|armv[4-9][a-z0-9]*|thumbv?[0-9]*)(?:-|$)", "arm"),
    (r"^(?:powerpc64|ppc64)(?:le|be)?(?:-|$)", "ppc64"),
    (r"^(?:powerpc|ppc)(?:-|$)", "ppc"),
    (r"^mips64(?:el)?(?:-|$)", "mips64"),
    (r"^mips(?:el)?(?:-|$)", "mips"),
    (r"^s390x(?:-|$)", "s390x"),
    (r"^s390(?:-|$)", "s390"),
    (r"^sparc64(?:-|$)", "sparc64"),
    (r"^sparc(?:-|$)", "sparc"),
    (r"^loongarch64(?:-|$)", "loongarch64"),
    (r"^wasm64(?:-|$)", "wasm64"),
    (r"^wasm32(?:-|$)", "wasm32"),
    (r"^(?:alpha|hppa|m68k|xtensa|csky|or1k|arc|microblaze)(?:-|$)", None),
)
_GENERIC_TARGETS = frozenset(
    {"all", "any", "generic", "linux", "android", "config-smoke"}
)


def _known_isa(value: str) -> tuple[bool, str | None]:
    """Classify one target token as a known ISA or a non-ISA label."""

    folded = normalize_isa(value).replace("_", "-")
    for pattern, canonical in _ISA_TOKEN_PATTERNS:
        if re.search(pattern, folded):
            return True, canonical or folded.split("-", 1)[0]
    return False, None


def _target_values(target: Any) -> tuple[str, ...]:
    if target is None:
        return ()
    values = target if isinstance(target, (list, tuple)) else [target]
    return tuple(
        token
        for value in values
        for token in re.split(r"\s*(?:,|/|\||;)\s*", str(value).strip())
        if token
    )


def _target_isas(target: Any) -> tuple[str, ...]:
    return tuple(
        sorted(
            {
                known
                for value in _target_values(target)
                if (classified := _known_isa(value))[0]
                for known in (classified[1],)
                if known is not None
            }
        )
    )


def _isa_compatible(requested: str, target: str) -> bool:
    if requested == target:
        return True
    for family, members in (
        ("x86", frozenset({"i386", "x86_64"})),
        ("riscv", frozenset({"riscv32", "riscv64"})),
    ):
        if requested == family and target in members:
            return True
        if target == family and requested in members:
            return True
    return False


def _target_matches_isa_scope(target: Any, requested_isas: Sequence[str]) -> bool:
    """Match asserted ISAs; empty and platform-only targets remain generic."""

    if not requested_isas:
        return True
    raw_targets = _target_values(target)
    if not raw_targets:
        return True
    normalized_targets = tuple(normalize_isa(value) for value in raw_targets)
    if any(requested in normalized_targets for requested in requested_isas):
        return True
    classifications = tuple(_known_isa(value) for value in raw_targets)
    known_targets = tuple(
        sorted({isa for is_isa, isa in classifications if is_isa and isa is not None})
    )
    if not known_targets:
        # Explicit platform/generic labels constrain something other than ISA.
        # Other unknown target spellings fail closed so an unrecognized
        # architecture cannot silently leak across target lanes.
        return all(normalize_isa(value) in _GENERIC_TARGETS for value in raw_targets)
    for requested in requested_isas:
        requested_classified, requested_isa = _known_isa(requested)
        if requested_classified and requested_isa is not None and any(
            _isa_compatible(requested_isa, target_isa) for target_isa in known_targets
        ):
            return True
    return False


def _project_input_scope(
    invariants: Sequence[NormalizedInvariant],
    *,
    parameters: Mapping[str, Any],
    source_path: Path,
) -> tuple[list[NormalizedInvariant], dict[str, Any]]:
    requested_mechanisms = _normalized_scope_values(
        parameters,
        plural="mechanisms",
        singular="mechanism",
        normalize=normalize_mechanism,
    )
    requested_isas = _normalized_scope_values(
        parameters, plural="isas", singular="isa", normalize=normalize_isa
    )
    source_ref = ArtifactRef.from_path(
        source_path, kind="accepted-invariants"
    ).to_dict()
    ordered = sorted(invariants, key=lambda item: item.invariant_id)
    selected: list[NormalizedInvariant] = []
    excluded: list[dict[str, Any]] = []
    for invariant in ordered:
        mechanism = normalize_mechanism(str(invariant.value.get("mechanism") or ""))
        target = invariant.value.get("target")
        reasons: list[str] = []
        if requested_mechanisms and mechanism not in requested_mechanisms:
            reasons.append("mechanism_out_of_scope")
        if requested_isas and not _target_matches_isa_scope(target, requested_isas):
            reasons.append("isa_out_of_scope")
        if reasons:
            excluded.append(
                {
                    "invariant_id": invariant.invariant_id,
                    "reasons": reasons,
                    "normalized_mechanism": mechanism,
                    "target": target,
                    "target_isas": list(_target_isas(target)),
                }
            )
        else:
            selected.append(invariant)

    return selected, {
        "schema_version": 1,
        "kind": "defuzz-checker-input-scope",
        "source_artifact": source_ref,
        "requested": {
            "mechanisms": list(requested_mechanisms),
            "isas": list(requested_isas),
        },
        "scope_requested": bool(requested_mechanisms or requested_isas),
        "counts": {
            "total": len(ordered),
            "selected": len(selected),
            "excluded": len(excluded),
        },
        "total_invariant_ids": [item.invariant_id for item in ordered],
        "selected_invariant_ids": [item.invariant_id for item in selected],
        "excluded_invariant_ids": [item["invariant_id"] for item in excluded],
        "excluded_invariants": excluded,
    }


def render_checker_prompt(
    invariant: NormalizedInvariant, *, checker_root: str = DEFAULT_CHECKER_ROOT
) -> str:
    """Render a contract-grounded, repository-specific authoring prompt."""

    payload = json.dumps(invariant.value, ensure_ascii=False, indent=2, sort_keys=True)
    return f"""You are implementing exactly one DeFuzz invariant checker in an isolated,
disposable cumulative checker tree. Earlier accepted checkers may already be present.

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
5. Existing checker implementation and test files belong to earlier accepted invariants and
   are immutable. You may extend shared metadata and mechanism registration files only.

Implement and test the checker now. Do not edit the source checkout outside this workspace.
"""


def _repair_prompt(
    invariant: NormalizedInvariant, attempt: int, failures: Sequence[Mapping[str, Any]]
) -> str:
    details = json.dumps(list(failures), ensure_ascii=False, indent=2, sort_keys=True)
    return f"""Repair the current implementation for invariant {invariant.invariant_id}.
This is bounded repair attempt {attempt}. Keep the existing InvariantChecker, metadata SSOT,
mechanism registration, and mandatory Pass/Fail/NotApplicable/Error/nil tests intact.
Do not edit outside core/internal/oracle, do not alter earlier checker-owned files, and do not
weaken tests. Shared metadata and mechanism registration files may be extended.

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


def _snapshot(root: Path, *, exclude_roots: Sequence[Path] = ()) -> dict[str, _FileState]:
    result: dict[str, _FileState] = {}
    resolved_excludes = tuple(path.resolve(strict=False) for path in exclude_roots)
    for path in sorted(candidate for candidate in root.rglob("*") if candidate.is_file()):
        resolved = path.resolve(strict=False)
        if any(_is_within(resolved, excluded) for excluded in resolved_excludes):
            continue
        relative = path.relative_to(root)
        if relative.parts and relative.parts[0] in {".git", ".defuzz-agent"}:
            continue
        content = path.read_bytes()
        result[relative.as_posix()] = _FileState(
            content=content,
            sha256=hashlib.sha256(content).hexdigest(),
            mode=stat.S_IMODE(path.stat().st_mode),
        )
    return result


def _tree_sha256(snapshot: Mapping[str, _FileState]) -> str:
    """Hash a tree from sorted relative paths and file-content digests."""

    aggregate = hashlib.sha256()
    for path in sorted(snapshot):
        aggregate.update(path.encode("utf-8"))
        aggregate.update(b"\0")
        aggregate.update(snapshot[path].sha256.encode("ascii"))
        aggregate.update(b"\n")
    return aggregate.hexdigest()


def _restore_snapshot(root: Path, target: Mapping[str, _FileState]) -> None:
    """Restore all snapshotted files, including additions and deletions."""

    current = _snapshot(root)
    for relative in sorted(set(current) - set(target), reverse=True):
        path = root / relative
        path.unlink(missing_ok=True)
    for relative, state in target.items():
        path = root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        if path.is_symlink():
            path.unlink()
        path.write_bytes(state.content)
        path.chmod(state.mode)
    for directory in sorted(
        (path for path in root.rglob("*") if path.is_dir()),
        key=lambda path: len(path.parts),
        reverse=True,
    ):
        directory_relative = directory.relative_to(root)
        if directory_relative.parts and directory_relative.parts[0] in {
            ".git",
            ".defuzz-agent",
        }:
            continue
        try:
            directory.rmdir()
        except OSError:
            pass


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


def _atomic_write_json(path: Path, value: Mapping[str, Any]) -> None:
    _atomic_write(
        path,
        json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n",
    )


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
                tuple(shlex.split(raw_argv)) if isinstance(raw_argv, str) else tuple(raw_argv or ())
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


def _configured_command(
    value: Any,
    *,
    default: Sequence[str],
    default_cwd: str = "core",
    substitutions: Mapping[str, str] | None = None,
) -> ValidationCommand:
    raw = default if value is None else value
    cwd = default_cwd
    if isinstance(raw, ValidationCommand):
        spec = raw
    elif isinstance(raw, str):
        spec = ValidationCommand(tuple(shlex.split(raw)), cwd=cwd)
    elif isinstance(raw, Mapping):
        raw_argv = raw.get("argv", raw.get("command"))
        argv = (
            tuple(shlex.split(raw_argv))
            if isinstance(raw_argv, str)
            else tuple(str(item) for item in (raw_argv or ()))
        )
        spec = ValidationCommand(argv, cwd=str(raw.get("cwd", cwd)))
    elif isinstance(raw, Sequence) and not isinstance(raw, (bytes, bytearray)):
        spec = ValidationCommand(tuple(str(item) for item in raw), cwd=cwd)
    else:
        raise TypeError("configured command must be argv, a string, or a mapping")
    replacements = dict(substitutions or {})
    return ValidationCommand(
        tuple(argument.format_map(replacements) for argument in spec.argv),
        cwd=spec.cwd,
        require_empty_stdout=spec.require_empty_stdout,
    )


def _matches_any_path(path: str, patterns: Sequence[str]) -> bool:
    return any(
        path == pattern.rstrip("/")
        or path.startswith(pattern.rstrip("/") + "/")
        or fnmatch.fnmatchcase(path, pattern)
        for pattern in patterns
    )


def _normalize_path_patterns(values: Sequence[Any], *, field: str) -> tuple[str, ...]:
    result: list[str] = []
    for value in values:
        text = str(value).strip().replace(os.sep, "/").strip("/")
        relative = PurePosixPath(text)
        if not text or relative.is_absolute() or ".." in relative.parts or "\x00" in text:
            raise ValueError(f"{field} entries must be safe relative paths or globs")
        result.append(text)
    return tuple(result)


def _diagnostic_catalog_entry(
    invariant: NormalizedInvariant, row: Mapping[str, Any], *, checker_id: str
) -> dict[str, Any]:
    return {
        # Part III routes executable checkers by canonical checker identity.
        # A generated invariant that reuses an existing checker keeps its own
        # identity in lineage without changing the trusted runtime contract.
        "checker_id": checker_id,
        "invariant_id": checker_id,
        "generated_invariant_id": invariant.invariant_id,
        "statement": invariant.value.get("statement"),
        "mechanism": invariant.value.get("mechanism"),
        "target": invariant.value.get("target"),
        "lineage": invariant.lineage,
        "parent_tree_sha256": row.get("parent_tree_sha256"),
        "result_tree_sha256": row.get("result_tree_sha256"),
        "files": [
            {
                "path": change["path"],
                "sha256": change["sha256_after"],
                "size_bytes": change["size_after"],
            }
            for change in row.get("file_changes", [])
            if change.get("sha256_after") is not None
        ],
        "reused": bool(row.get("reused")),
        "reused_checker_id": row.get("reused_checker_id"),
    }


def _runtime_catalog(
    stdout: str,
    *,
    included: Sequence[tuple[NormalizedInvariant, Mapping[str, Any]]],
    source_tree_sha256: str,
    final_tree_sha256: str,
) -> dict[str, Any]:
    """Validate dispatcher metadata and join it with authoring provenance."""

    try:
        payload = json.loads(stdout)
    except json.JSONDecodeError as exc:
        raise ValueError(f"dispatcher catalog returned invalid JSON: {exc.msg}") from exc
    if not isinstance(payload, Mapping):
        raise ValueError("dispatcher catalog must be a JSON object")
    if payload.get("schema_version") != 1 or payload.get("kind") != ("defuzz-checker-catalog"):
        raise ValueError("dispatcher catalog has an unsupported schema or kind")
    raw_checkers = payload.get("checkers")
    if not isinstance(raw_checkers, list):
        raise ValueError("dispatcher catalog checkers must be a list")
    by_id: dict[str, Mapping[str, Any]] = {}
    for raw in raw_checkers:
        if not isinstance(raw, Mapping):
            raise ValueError("dispatcher catalog checker entries must be objects")
        checker_id = str(raw.get("id", "")).strip()
        if not checker_id:
            raise ValueError("dispatcher catalog checker id must be non-empty")
        if checker_id in by_id:
            raise ValueError(f"dispatcher catalog has duplicate checker id {checker_id!r}")
        by_id[checker_id] = raw

    grouped: dict[str, list[tuple[NormalizedInvariant, Mapping[str, Any]]]] = {}
    for invariant, row in included:
        canonical_id = str(row.get("reused_checker_id") or invariant.invariant_id)
        grouped.setdefault(canonical_id, []).append((invariant, row))

    checkers: list[dict[str, Any]] = []
    included_ids = set(grouped)
    for canonical_id, members in grouped.items():
        invariant, row = members[0]
        runtime = by_id.get(canonical_id)
        if runtime is None:
            raise ValueError(
                "dispatcher catalog is missing checker "
                f"{canonical_id!r} for {invariant.invariant_id!r}"
            )
        entry = dict(runtime)
        requires = entry.get("requires", [])
        if not isinstance(requires, list) or not all(
            isinstance(item, str) and item for item in requires
        ):
            raise ValueError(
                f"dispatcher catalog checker {canonical_id!r} has invalid requires"
            )
        missing = sorted(set(requires) - included_ids)
        if missing:
            raise ValueError(
                f"checker {canonical_id!r} is missing required "
                f"bundle checkers: {', '.join(missing)}"
            )
        entry.update(_diagnostic_catalog_entry(invariant, row, checker_id=canonical_id))
        entry["generated_invariant_ids"] = [item.invariant_id for item, _ in members]
        entry["lineages"] = [item.lineage for item, _ in members]
        checkers.append(entry)
    return {
        "schema_version": 1,
        "kind": "defuzz-checker-catalog",
        "source_tree_sha256": source_tree_sha256,
        "result_tree_sha256": final_tree_sha256,
        "checkers": checkers,
    }


def _reused_row(
    plan: ExperimentPlan,
    repetition: int,
    invariant: NormalizedInvariant,
    snapshot: Mapping[str, _FileState],
) -> dict[str, Any]:
    """Preserve generated lineage while recording a no-edit trusted reuse."""

    checker_id = invariant.reused_checker_ids[0]
    tree_sha256 = _tree_sha256(snapshot)
    return {
        "schema_version": 1,
        "run_id": plan.run_id,
        "experiment": plan.experiment,
        "variant": plan.variant,
        "repetition": repetition,
        "invariant_id": invariant.invariant_id,
        "invariant": invariant.value,
        "lineage": invariant.lineage,
        "bundle_index": 0,
        "parent_tree_sha256": tree_sha256,
        "result_tree_sha256": tree_sha256,
        "included_in_bundle": True,
        "first_pass_status": "passed",
        "final_status": "passed",
        "status": "passed",
        "attempt_count": 0,
        "attempt_cap": 0,
        "attempts": [],
        "files": [],
        "file_changes": [],
        "first_patch": None,
        "final_patch": None,
        "token_refs": [],
        "stopped_reason": None,
        "budget_exhausted": False,
        "infrastructure_error": False,
        "reused": True,
        "reused_checker_id": checker_id,
        "reuse_validation": "fresh-runtime-catalog-required",
    }


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
        invariant: NormalizedInvariant | None,
        allowlist: Sequence[str],
    ) -> _WorkspaceLease:
        output_inside_source = _is_within(output_dir, source_root)
        base = None if output_inside_source else output_dir / "checker-authoring" / ".workspaces"
        if base is not None:
            base.mkdir(parents=True, exist_ok=True)
        destination = Path(
            tempfile.mkdtemp(
                prefix=f"{_slug(invariant.invariant_id if invariant else 'checker-bundle')}-",
                dir=base,
            )
        ).resolve()
        try:
            if self.workspace_factory is None:
                produced: Any = WorkspaceBuilder(source_root, allowlist=allowlist).materialize(
                    destination
                )
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
                        "workspace_factory must be callable or expose create/materialize/build"
                    )
                try:
                    produced = creator(
                        source_root=source_root,
                        destination=destination,
                        invariant=invariant.value if invariant else {},
                    )
                except TypeError:
                    produced = creator(
                        source_root, destination, invariant.value if invariant else {}
                    )
                if inspect.isawaitable(produced):
                    produced = await produced

            root_value = _value(produced, "root", _value(produced, "path", produced))
            root = Path(root_value).expanduser().resolve(strict=True)
            if not root.is_dir():
                raise ValueError(f"workspace factory did not produce a directory: {root}")
            if _is_within(root, source_root) or _is_within(source_root, root):
                raise ValueError("isolated workspace must not overlap the source checkout")
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

    async def _invoke_agent(self, request: AgentRequest) -> AgentResult:
        request.deny_read_paths = validate_agent_path_isolation(
            cwd=request.cwd,
            output_dir=request.output_dir,
            schema_path=request.schema_path,
            deny_read_paths=request.deny_read_paths,
        )
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
                deny_read_paths=request.deny_read_paths,
                require_host_read_isolation=request.require_host_read_isolation,
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

    async def _execute_command(
        self,
        workspace: Path,
        spec: ValidationCommand,
        timeout_seconds: float,
    ) -> tuple[bool, dict[str, Any], CommandResult]:
        cwd = (workspace / spec.cwd).resolve()
        if not _is_within(cwd, workspace) or not cwd.is_dir():
            outcome = CommandResult(
                spec.argv,
                os.fspath(cwd),
                2,
                stderr="command cwd is missing or unsafe",
            )
        else:
            raw = self.command_executor.run(spec.argv, cwd=cwd, timeout_seconds=timeout_seconds)
            if inspect.isawaitable(raw):
                raw = await raw
            outcome = _normalize_command_result(raw, spec, cwd)
        success = outcome.success and not (
            spec.require_empty_stdout and bool(outcome.stdout.strip())
        )
        record = {
            "argv": list(outcome.argv),
            "cwd": spec.cwd,
            "exit_code": outcome.exit_code,
            "timed_out": outcome.timed_out,
            "require_empty_stdout": spec.require_empty_stdout,
            "status": "passed" if success else "failed",
            "stdout": _trim(outcome.stdout),
            "stderr": _trim(outcome.stderr),
        }
        return success, record, outcome

    async def _one(
        self,
        *,
        plan: ExperimentPlan,
        repetition: int,
        output_dir: Path,
        invariant: NormalizedInvariant,
        workspace: Path,
        parent_snapshot: Mapping[str, _FileState],
        owned_files: Mapping[str, str],
        shared_integration_paths: Sequence[str],
        commands: Sequence[ValidationCommand],
        max_attempts: int,
        checker_root: str,
        allowed_paths: Sequence[str],
        token_sink: TokenUsageSink,
        timeout_seconds: float,
    ) -> tuple[dict[str, Any], list[Path], dict[str, _FileState]]:
        slug = _slug(invariant.invariant_id)
        artifact_dir = output_dir / "checker-authoring" / slug
        artifact_dir.mkdir(parents=True, exist_ok=True)
        patch_paths: list[Path] = []
        attempts: list[dict[str, Any]] = []
        token_refs: list[dict[str, Any]] = []
        first_status = "failed"
        final_status = "failed"
        first_patch_ref: dict[str, Any] | None = None
        final_patch_ref: dict[str, Any] | None = None
        baseline = dict(parent_snapshot)
        final_snapshot = dict(parent_snapshot)
        previous_failures: list[Mapping[str, Any]] = []
        stopped_reason: str | None = None
        accepted_snapshot = dict(parent_snapshot)
        try:
            for attempt_number in range(1, max_attempts + 1):
                try:
                    token_sink.check_budget()
                except BudgetExceeded as exc:
                    stopped_reason = str(exc)
                    break
                agent_output = workspace / ".defuzz-agent" / slug / f"attempt-{attempt_number:03d}"
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
                    cwd=workspace,
                    output_dir=agent_output,
                    timeout_seconds=timeout_seconds,
                    writable=True,
                    token_sink=token_sink,
                    deny_read_paths=list(plan.parameters.get("_resolved_deny_read_paths", [])),
                    require_host_read_isolation=bool(
                        plan.parameters.get("require_host_read_isolation", False)
                    ),
                    metadata={
                        "run_id": plan.run_id,
                        "experiment": plan.experiment,
                        "variant": plan.variant,
                        "part": "checker-authoring",
                        "stage": f"{invariant.invariant_id}:attempt-{attempt_number}",
                        "invariant_id": invariant.invariant_id,
                        "parent_tree_sha256": _tree_sha256(parent_snapshot),
                    },
                )
                try:
                    agent_result = await self._invoke_agent(request)
                except Exception as exc:  # backend failure is a measured terminal outcome
                    agent_result = AgentResult(
                        success=False,
                        error=f"{type(exc).__name__}: {exc}",
                    )
                attempt_records = [
                    record
                    for record in token_sink.records
                    if record.call_id not in existing_call_ids
                ]
                if not attempt_records:
                    attempt_records = [
                        token_sink.record_external_usage(
                            agent_result.usage,
                            context=attempt_context,
                            success=agent_result.success,
                            error_type="AgentError" if not agent_result.success else None,
                        )
                    ]
                refs = [
                    {
                        "path": _reference_path(token_sink.path, output_dir),
                        "call_id": record.call_id,
                        "attempt": attempt_number,
                    }
                    for record in attempt_records
                ]
                token_refs.extend(refs)

                validation_ok = False
                validation: list[dict[str, Any]] = []
                if agent_result.success:
                    validation_ok, validation = await self._validate(
                        workspace, commands, timeout_seconds
                    )
                else:
                    validation.append(
                        {
                            "status": "failed",
                            "type": "agent-error",
                            "error": agent_result.error or "agent backend failed",
                        }
                    )
                # Validation may perform deterministic normalization (the
                # default first command is ``gofmt -w``).  Snapshot only after
                # it completes so the published patch and tree hashes describe
                # exactly what the runner tested.
                current = _snapshot(workspace)
                if attempt_number == 1:
                    first_patch_path = artifact_dir / "first.patch"
                    _atomic_write(first_patch_path, _patch(baseline, current))
                    patch_paths.append(first_patch_path)
                    first_patch_ref = _artifact(first_patch_path, output_dir, "first-patch")

                changed = _changes(baseline, current)
                allowed_errors: list[dict[str, Any]] = [
                    {
                        "type": "disallowed-file",
                        "path": str(item["path"]),
                        "message": "checker authoring may only change configured checker paths",
                    }
                    for item in changed
                    if not _matches_any_path(str(item["path"]), allowed_paths)
                ]
                for item in changed:
                    path = str(item["path"])
                    owner = owned_files.get(path)
                    if owner is not None and not _matches_any_path(path, shared_integration_paths):
                        allowed_errors.append(
                            {
                                "type": "owned-file-modified",
                                "path": path,
                                "owner_invariant_id": owner,
                                "message": (
                                    "later invariants may not modify files owned by an "
                                    "accepted checker"
                                ),
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
                    accepted_snapshot = current
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
            candidate_changes = _changes(baseline, final_snapshot)
            if final_status != "passed":
                _restore_snapshot(workspace, parent_snapshot)
                accepted_snapshot = dict(parent_snapshot)
            changes = candidate_changes
            effective_final_status = "unprocessed" if not attempts else final_status
            record = {
                "schema_version": 1,
                "run_id": plan.run_id,
                "experiment": plan.experiment,
                "variant": plan.variant,
                "repetition": repetition,
                "invariant_id": invariant.invariant_id,
                "invariant": invariant.value,
                "lineage": invariant.lineage,
                "bundle_index": 0,
                "parent_tree_sha256": _tree_sha256(parent_snapshot),
                "result_tree_sha256": _tree_sha256(accepted_snapshot),
                "included_in_bundle": effective_final_status == "passed",
                "first_pass_status": first_status,
                "final_status": effective_final_status,
                "status": effective_final_status,
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
                "infrastructure_error": effective_final_status == "failed"
                and any(
                    attempt["agent_status"] == "failed"
                    or any(
                        item.get("timed_out") or item.get("exit_code") == 127
                        for item in attempt["validation"]
                    )
                    for attempt in attempts
                ),
            }
            return record, patch_paths, accepted_snapshot
        except BaseException:
            _restore_snapshot(workspace, parent_snapshot)
            raise

    async def run(
        self, plan: ExperimentPlan, repetition: int, output_dir: str | os.PathLike[str]
    ) -> StageResult:
        destination = Path(output_dir).expanduser().resolve(strict=False)
        destination.mkdir(parents=True, exist_ok=True)
        parameters = plan.parameters
        source_root = (
            (
                Path(plan.source_root)
                if plan.source_root is not None
                else Path(__file__).resolve().parents[3]
            )
            .expanduser()
            .resolve(strict=True)
        )
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

        input_path = input_path.resolve(strict=True)
        expected_input_hash = parameters.get("accepted_invariants_sha256")
        pre_read_input_hash = hashlib.sha256(input_path.read_bytes()).hexdigest()
        if expected_input_hash is not None and pre_read_input_hash != expected_input_hash:
            raise ValueError(
                "accepted_invariants SHA-256 mismatch: "
                f"expected {expected_input_hash}, got {pre_read_input_hash}"
            )
        all_invariants = load_accepted_invariants(input_path)
        invariants, scope_report = _project_input_scope(
            all_invariants, parameters=parameters, source_path=input_path
        )
        actual_input_hash = cast(dict[str, Any], scope_report["source_artifact"])[
            "sha256"
        ]
        if expected_input_hash is not None and expected_input_hash != actual_input_hash:
            raise ValueError(
                "accepted_invariants SHA-256 mismatch: "
                f"expected {expected_input_hash}, got {actual_input_hash}"
            )
        scope_path = destination / CHECKER_INPUT_SCOPE_FILENAME
        _atomic_write_json(scope_path, scope_report)
        scope_artifact = ArtifactRef.from_path(
            scope_path, base_dir=destination, kind="checker-input-scope"
        )
        scoped_invariants_path = destination / SCOPED_ACCEPTED_INVARIANTS_FILENAME
        _atomic_write(
            scoped_invariants_path,
            "".join(
                json.dumps(
                    invariant.value,
                    ensure_ascii=False,
                    sort_keys=True,
                    separators=(",", ":"),
                )
                + "\n"
                for invariant in invariants
            ),
        )
        scoped_invariants_artifact = ArtifactRef.from_path(
            scoped_invariants_path,
            base_dir=destination,
            kind="scoped-accepted-invariants",
        )
        scope_counts = cast(dict[str, int], scope_report["counts"])
        requested_scope = cast(dict[str, list[str]], scope_report["requested"])
        scope_metrics = {
            "input_invariants": scope_counts["total"],
            "total_invariants": scope_counts["total"],
            "selected_invariants": scope_counts["selected"],
            "excluded_invariants": scope_counts["excluded"],
        }
        scope_metadata = {
            **scope_metrics,
            "accepted_invariants_sha256": cast(
                dict[str, Any], scope_report["source_artifact"]
            )["sha256"],
            "checker_input_scope": CHECKER_INPUT_SCOPE_FILENAME,
            "requested_mechanisms": requested_scope["mechanisms"],
            "requested_isas": requested_scope["isas"],
            "scope_counts": scope_counts,
        }
        if scope_report["scope_requested"] and not invariants:
            _atomic_write(results_path, "")
            error = "checker input scope selected no accepted invariants"
            return StageResult(
                stage="checker-authoring",
                status="failed",
                artifacts=[
                    ArtifactRef.from_path(
                        results_path, base_dir=destination, kind="results"
                    ),
                    scope_artifact,
                    scoped_invariants_artifact,
                ],
                metrics={
                    "invariants": 0,
                    **scope_metrics,
                    "first_passed": 0,
                    "final_passed": 0,
                    "failed": 0,
                    "budget_exhausted": 0,
                    "unprocessed": 0,
                    "agent_attempts": 0,
                    "reused": 0,
                    "bundle_ready": False,
                    "coverage_complete": False,
                },
                metadata={
                    "input_path": os.fspath(input_path),
                    "results_path": os.fspath(results_path),
                    **scope_metadata,
                },
                error=error,
            )
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
        allowed_paths = _normalize_path_patterns(
            parameters.get("allowed_change_paths", [checker_root]),
            field="allowed_change_paths",
        )
        shared_integration_paths = _normalize_path_patterns(
            parameters.get("shared_integration_paths", DEFAULT_SHARED_INTEGRATION_PATHS),
            field="shared_integration_paths",
        )
        workspace_allowlist = tuple(
            str(item)
            for item in parameters.get(
                "workspace_allowlist", [checker_path.parts[0] if checker_path.parts else "core"]
            )
        )
        commands = _command_specs(parameters)
        keep_workspace = bool(parameters.get("keep_workspaces", False))
        deny_read_paths, require_host_read_isolation = _resolve_agent_deny_paths(
            parameters, source_root
        )
        if require_host_read_isolation and not bool(
            getattr(self.backend, "supports_host_read_isolation", False)
        ):
            raise ValueError(
                "Part II requires a backend with host read isolation"
            )
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
        lease = await self._workspace(
            source_root,
            destination,
            invariants[0] if invariants else None,
            workspace_allowlist,
        )
        source_snapshot = _snapshot(lease.root)
        # ``source_root_sha256`` identifies the exact baseline presented to the
        # authoring worker.  Hashing the entire checkout here is both misleading
        # (the workspace is allowlisted) and racy with unrelated ignored build
        # trees such as ``.work``.  The materialized workspace is immutable until
        # the first agent attempt and is therefore the canonical input snapshot.
        source_root_snapshot = dict(source_snapshot)
        owned_files: dict[str, str] = {
            path: "source-baseline"
            for path in source_snapshot
            if _matches_any_path(path, allowed_paths)
            and not _matches_any_path(path, shared_integration_paths)
        }
        accepted_snapshot = dict(source_snapshot)
        source_tree_sha256 = _tree_sha256(source_snapshot)
        try:
            for index, invariant in enumerate(invariants):
                if invariant.declared_reused_checker_ids != invariant.reused_checker_ids:
                    raise ValueError(
                        "Part I checker reuse metadata does not match Part II semantic "
                        f"validation for {invariant.invariant_id}"
                    )
                if len(invariant.reused_checker_ids) == 1:
                    row = _reused_row(plan, repetition, invariant, accepted_snapshot)
                    paths: list[Path] = []
                else:
                    row, paths, accepted_snapshot = await self._one(
                        plan=plan.model_copy(
                            update={
                                "parameters": {
                                    **plan.parameters,
                                    "_resolved_deny_read_paths": deny_read_paths,
                                    "require_host_read_isolation": require_host_read_isolation,
                                }
                            }
                        ),
                        repetition=repetition,
                        output_dir=destination,
                        invariant=invariant,
                        workspace=lease.root,
                        parent_snapshot=accepted_snapshot,
                        owned_files=owned_files,
                        shared_integration_paths=shared_integration_paths,
                        commands=commands,
                        max_attempts=max_attempts,
                        checker_root=checker_root,
                        allowed_paths=allowed_paths,
                        token_sink=token_sink,
                        timeout_seconds=timeout_seconds,
                    )
                row["bundle_index"] = index
                rows.append(row)
                patch_paths.extend(paths)
                if row["included_in_bundle"]:
                    for path in row["files"]:
                        if not _matches_any_path(path, shared_integration_paths):
                            owned_files[path] = invariant.invariant_id

            final_snapshot = dict(accepted_snapshot)
            final_tree_sha256 = _tree_sha256(final_snapshot)
            final_validation_ok, final_validation = await self._validate(
                lease.root, commands, timeout_seconds
            )

            bundle_patch_path = destination / CHECKER_BUNDLE_PATCH_FILENAME
            _atomic_write(bundle_patch_path, _patch(source_snapshot, final_snapshot))
            bundle_patch_ref = _artifact(bundle_patch_path, destination, "checker-bundle-patch")

            included = [
                (invariant, row)
                for invariant, row in zip(invariants, rows, strict=True)
                if row["included_in_bundle"]
            ]
            build_record: dict[str, Any] | None = None
            catalog_ref: dict[str, Any] | None = None
            dispatcher_ref: dict[str, Any] | None = None
            integration_error: str | None = None
            if included and final_validation_ok:
                dispatcher_relative = str(
                    parameters.get("dispatcher_path", DEFAULT_DISPATCHER_PATH)
                )
                dispatcher_posix = PurePosixPath(dispatcher_relative)
                if (
                    not dispatcher_relative
                    or "\x00" in dispatcher_relative
                    or "\\" in dispatcher_relative
                    or dispatcher_posix.is_absolute()
                    or ".." in dispatcher_posix.parts
                    or dispatcher_posix.as_posix() != dispatcher_relative
                ):
                    raise ValueError("dispatcher_path must be a normalized relative POSIX path")
                bundle_dispatcher = destination.joinpath(*dispatcher_posix.parts)
                bundle_dispatcher.parent.mkdir(parents=True, exist_ok=True)
                bundle_dispatcher.unlink(missing_ok=True)
                build_spec = _configured_command(
                    parameters.get("dispatcher_build_command"),
                    default=(
                        "go",
                        "build",
                        "-trimpath",
                        "-buildvcs=false",
                        "-o",
                        os.fspath(bundle_dispatcher),
                        "./cmd/defuzz-candidate-dispatcher",
                    ),
                    substitutions={
                        "dispatcher": os.fspath(bundle_dispatcher),
                        "workspace": os.fspath(lease.root),
                    },
                )
                build_ok, build_record, _ = await self._execute_command(
                    lease.root, build_spec, timeout_seconds
                )
                if build_ok and bundle_dispatcher.is_file():
                    dispatcher_ref = _artifact(bundle_dispatcher, destination, "checker-dispatcher")
                    catalog_spec = _configured_command(
                        parameters.get("dispatcher_catalog_command"),
                        default=(os.fspath(bundle_dispatcher), "--mode", "catalog"),
                        substitutions={
                            "dispatcher": os.fspath(bundle_dispatcher),
                            "workspace": os.fspath(lease.root),
                        },
                    )
                    catalog_ok, catalog_command, catalog_outcome = await self._execute_command(
                        lease.root, catalog_spec, timeout_seconds
                    )
                    if catalog_ok:
                        try:
                            catalog = _runtime_catalog(
                                catalog_outcome.stdout,
                                included=included,
                                source_tree_sha256=source_tree_sha256,
                                final_tree_sha256=final_tree_sha256,
                            )
                        except ValueError as exc:
                            integration_error = str(exc)
                        else:
                            catalog_path = destination / CHECKER_CATALOG_FILENAME
                            _atomic_write_json(catalog_path, catalog)
                            catalog_ref = _artifact(catalog_path, destination, "checker-catalog")
                    else:
                        integration_error = "dispatcher catalog command failed"
                    build_record["catalog_command"] = catalog_command
                elif build_ok:
                    build_record["status"] = "failed"
                    build_record["stderr"] = (
                        str(build_record.get("stderr", ""))
                        + "dispatcher build produced no executable"
                    )
                    integration_error = "dispatcher build produced no executable"

            # Bundle routing is keyed by the executable checker ID.  A generated
            # invariant may deterministically reuse an existing checker while
            # retaining its own content ID in lineage/results.
            included_groups: dict[
                str, list[tuple[NormalizedInvariant, Mapping[str, Any]]]
            ] = {}
            for item, row in included:
                canonical_id = str(row.get("reused_checker_id") or item.invariant_id)
                included_groups.setdefault(canonical_id, []).append((item, row))
            included_ids = list(included_groups)
            failed_ids = [
                invariant.invariant_id
                for invariant, row in zip(invariants, rows, strict=True)
                if row["final_status"] == "failed"
            ]
            budget_exhausted_any = any(row["budget_exhausted"] for row in rows)
            infrastructure_error_any = any(row["infrastructure_error"] for row in rows)
            unprocessed_any = any(
                row["attempt_count"] == 0 and not row.get("reused") for row in rows
            )
            build_ok = bool(build_record and build_record.get("status") == "passed")
            ready = (
                bool(included_ids)
                and final_validation_ok
                and build_ok
                and catalog_ref is not None
                and dispatcher_ref is not None
                and not budget_exhausted_any
                and not infrastructure_error_any
                and not unprocessed_any
            )
            validation_status = (
                "passed" if final_validation_ok and build_ok and catalog_ref else "failed"
            )
            manifest_payload: dict[str, Any] = {
                "schema_version": 1,
                "kind": "defuzz-checker-bundle",
                "status": "ready" if ready else "incomplete",
                "source_root": os.fspath(source_root),
                "source_root_sha256": _tree_sha256(source_root_snapshot),
                "source_tree_sha256": source_tree_sha256,
                "final_tree_sha256": final_tree_sha256,
                "coverage_complete": not failed_ids and not unprocessed_any,
                "budget_exhausted": budget_exhausted_any,
                "included_invariant_ids": included_ids,
                "failed_invariant_ids": failed_ids,
                "invariants": [
                    {
                        "invariant_id": canonical_id,
                        "generated_invariant_id": members[0][1]["invariant_id"],
                        "generated_invariant_ids": [
                            item.invariant_id for item, _ in members
                        ],
                        "reused_checker_id": members[0][1].get("reused_checker_id"),
                        "final_status": "passed",
                        "infrastructure_error": any(
                            bool(row["infrastructure_error"]) for _, row in members
                        ),
                        "parent_tree_sha256": members[0][1]["parent_tree_sha256"],
                        "result_tree_sha256": members[-1][1]["result_tree_sha256"],
                        "files": list(
                            dict.fromkeys(
                                path for _, row in members for path in row["files"]
                            )
                        ),
                        "lineage": members[0][1]["lineage"],
                        "lineages": [row["lineage"] for _, row in members],
                    }
                    for canonical_id, members in included_groups.items()
                ]
                + [
                    {
                        "invariant_id": row["invariant_id"],
                        "generated_invariant_id": row["invariant_id"],
                        "generated_invariant_ids": [row["invariant_id"]],
                        "reused_checker_id": row.get("reused_checker_id"),
                        "final_status": (
                            "unprocessed" if row["attempt_count"] == 0 else "failed"
                        ),
                        "infrastructure_error": row["infrastructure_error"],
                        "parent_tree_sha256": row["parent_tree_sha256"],
                        "result_tree_sha256": row["result_tree_sha256"],
                        "files": row["files"],
                        "lineage": row["lineage"],
                        "lineages": [row["lineage"]],
                    }
                    for row in rows
                    if not row["included_in_bundle"]
                ],
                "artifacts": {
                    "cumulative_patch": bundle_patch_ref,
                    "catalog": catalog_ref,
                    "dispatcher": dispatcher_ref,
                    "scoped_invariants": scoped_invariants_artifact.to_dict(),
                    "input_scope": scope_artifact.to_dict(),
                },
                "source_invariants_sha256": actual_input_hash,
                "requested_mechanisms": requested_scope["mechanisms"],
                "requested_isas": requested_scope["isas"],
                "validation": {
                    "status": validation_status,
                    "commands": final_validation,
                    "build": build_record,
                    "integration_error": integration_error,
                },
            }
            manifest_payload["bundle_id"] = compute_bundle_id(manifest_payload)
            manifest = CheckerBundleManifest.model_validate(manifest_payload)
            manifest_path = destination / CHECKER_BUNDLE_MANIFEST_FILENAME
            _atomic_write_json(manifest_path, manifest.model_dump(mode="json"))
        finally:
            if not keep_workspace and lease.cleanup is not None:
                cleanup_result = lease.cleanup()
                if inspect.isawaitable(cleanup_result):
                    await cleanup_result

        _atomic_write(
            results_path,
            "".join(
                json.dumps(row, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n"
                for row in rows
            ),
        )
        artifacts = [
            ArtifactRef.from_path(results_path, base_dir=destination, kind="results"),
            scope_artifact,
            scoped_invariants_artifact,
            ArtifactRef.from_path(
                bundle_patch_path, base_dir=destination, kind="checker-bundle-patch"
            ),
            ArtifactRef.from_path(
                manifest_path, base_dir=destination, kind="checker-bundle-manifest"
            ),
            *(
                ArtifactRef.from_path(path, base_dir=destination, kind="patch")
                for path in patch_paths
            ),
        ]
        if catalog_ref is not None:
            artifacts.append(
                ArtifactRef.from_path(
                    destination / CHECKER_CATALOG_FILENAME,
                    base_dir=destination,
                    kind="checker-catalog",
                )
            )
        if dispatcher_ref is not None:
            artifacts.append(
                ArtifactRef.from_path(
                    destination / str(dispatcher_ref["path"]),
                    base_dir=destination,
                    kind="checker-dispatcher",
                )
            )
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
        if ready:
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
                **scope_metrics,
                "first_passed": first_passed,
                "first_pass_rate": first_passed / len(rows) if rows else 0.0,
                "final_passed": final_passed,
                "final_pass_rate": final_passed / len(rows) if rows else 0.0,
                "failed": failed,
                "budget_exhausted": budget_exhausted,
                "unprocessed": unprocessed,
                "agent_attempts": sum(int(row["attempt_count"]) for row in rows),
                "reused": sum(bool(row.get("reused")) for row in rows),
                "bundle_ready": ready,
                "coverage_complete": manifest.coverage_complete,
            },
            metadata={
                "input_path": os.fspath(input_path),
                "results_path": os.fspath(results_path),
                **scope_metadata,
                "attempt_cap": max_attempts,
                "checker_bundle_manifest": os.fspath(manifest_path),
                "bundle_id": manifest.bundle_id,
                "deterministic_only": bool(rows) and all(bool(row.get("reused")) for row in rows),
                "reused_checker_ids": [
                    row["reused_checker_id"] for row in rows if row.get("reused_checker_id")
                ],
            },
        )


async def run(
    plan: ExperimentPlan,
    repetition: int,
    output_dir: str | os.PathLike[str],
    backend: AgentBackend | Any = None,
) -> StageResult:
    """Run Part II with the shared experiment-stage signature."""

    normalized_plan = plan if isinstance(plan, ExperimentPlan) else ExperimentPlan.from_dict(plan)
    selected_backend = backend or ExecAgentBackend(
        binary=str(normalized_plan.parameters.get("agent_binary", "traex")),
        model=normalized_plan.parameters.get("model"),
        provider=cast(
            Literal["traex", "codex"],
            normalized_plan.parameters.get("backend", "traex"),
        ),
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
    "CHECKER_INPUT_SCOPE_FILENAME",
    "SCOPED_ACCEPTED_INVARIANTS_FILENAME",
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
