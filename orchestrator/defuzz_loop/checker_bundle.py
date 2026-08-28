"""Typed contracts and trusted loading for checker-bundle artifacts.

The manifest is untrusted input.  Loading therefore validates both its typed
contract and the on-disk files it names before returning any paths to callers.
"""

from __future__ import annotations

import hashlib
import hmac
import json
import os
import stat
from collections.abc import Mapping
from pathlib import Path, PurePosixPath, PureWindowsPath
from typing import Annotated, Any, Literal

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
    model_validator,
)

CHECKER_BUNDLE_MANIFEST_FILENAME = "checker-bundle-manifest.json"

Sha256Hex = Annotated[str, StringConstraints(pattern=r"^[0-9a-f]{64}$")]
NonEmptyString = Annotated[str, StringConstraints(min_length=1)]


class _StrictModel(BaseModel):
    """Base configuration for security-sensitive manifest records."""

    model_config = ConfigDict(strict=True, extra="forbid")


def _validate_posix_relative_path(value: str) -> str:
    if not value:
        raise ValueError("artifact path must not be empty")
    if "\x00" in value:
        raise ValueError("artifact path must not contain NUL")
    if "\\" in value:
        raise ValueError("artifact path must use POSIX separators")

    posix_path = PurePosixPath(value)
    if posix_path.is_absolute():
        raise ValueError("artifact path must be relative")
    if PureWindowsPath(value).drive:
        raise ValueError("artifact path must not contain a Windows drive")
    if not posix_path.parts or posix_path == PurePosixPath("."):
        raise ValueError("artifact path must name a file")
    if ".." in posix_path.parts:
        raise ValueError("artifact path must not contain '..'")
    if posix_path.as_posix() != value:
        raise ValueError("artifact path must be normalized POSIX syntax")
    return value


class CheckerBundleArtifact(_StrictModel):
    """Content-addressed file reference relative to the manifest directory."""

    path: str
    sha256: Sha256Hex
    size_bytes: int | None = Field(default=None, ge=0)
    kind: str | None = None

    @field_validator("path")
    @classmethod
    def _path_is_safe(cls, value: str) -> str:
        return _validate_posix_relative_path(value)


class CheckerBundleArtifacts(_StrictModel):
    """Artifacts produced incrementally and all required once a bundle is ready."""

    cumulative_patch: CheckerBundleArtifact | None = None
    catalog: CheckerBundleArtifact | None = None
    dispatcher: CheckerBundleArtifact | None = None
    scoped_invariants: CheckerBundleArtifact | None = None
    input_scope: CheckerBundleArtifact | None = None

    @model_validator(mode="after")
    def _paths_are_unique(self) -> CheckerBundleArtifacts:
        paths = [
            artifact.path
            for artifact in (
                self.cumulative_patch,
                self.catalog,
                self.dispatcher,
                self.scoped_invariants,
                self.input_scope,
            )
            if artifact is not None
        ]
        if len(set(paths)) != len(paths):
            raise ValueError("checker-bundle artifact paths must be unique")
        return self


class CheckerBundleInvariant(BaseModel):
    """Per-invariant lineage included in a checker bundle.

    Producer-specific lineage and validation metadata are deliberately retained
    so later pipeline stages do not need a schema revision for every addition.
    """

    model_config = ConfigDict(strict=True, extra="allow")

    invariant_id: NonEmptyString
    final_status: Literal["passed", "failed", "unprocessed"]
    infrastructure_error: bool = False
    parent_tree_sha256: Sha256Hex
    result_tree_sha256: Sha256Hex
    files: list[str]


class CheckerBundleValidation(BaseModel):
    """Aggregate validation evidence, with room for producer diagnostics."""

    model_config = ConfigDict(strict=True, extra="allow")

    status: Literal["passed", "failed", "not-run"]
    commands: list[dict[str, Any]]
    build: dict[str, Any] | None = None

    @model_validator(mode="after")
    def _build_status_is_known(self) -> CheckerBundleValidation:
        if self.build is not None and self.build.get("status") not in {"passed", "failed"}:
            raise ValueError("checker-bundle validation build must have passed/failed status")
        return self


class CheckerBundleManifest(_StrictModel):
    """Version-one checker-bundle manifest."""

    schema_version: Literal[1] = 1
    kind: Literal["defuzz-checker-bundle"] = "defuzz-checker-bundle"
    status: Literal["ready", "incomplete"]
    bundle_id: Sha256Hex
    source_root: str
    source_root_sha256: Sha256Hex
    source_tree_sha256: Sha256Hex
    final_tree_sha256: Sha256Hex
    source_invariants_sha256: Sha256Hex
    requested_mechanisms: list[NonEmptyString]
    requested_isas: list[NonEmptyString]
    coverage_complete: bool
    budget_exhausted: bool
    included_invariant_ids: list[NonEmptyString]
    failed_invariant_ids: list[NonEmptyString]
    invariants: list[CheckerBundleInvariant]
    artifacts: CheckerBundleArtifacts
    validation: CheckerBundleValidation

    @field_validator("requested_mechanisms", "requested_isas")
    @classmethod
    def _scope_values_are_canonical(cls, values: list[str]) -> list[str]:
        if any(value != value.strip() for value in values):
            raise ValueError(
                "checker-bundle requested scope values must not have surrounding whitespace"
            )
        if len(set(values)) != len(values):
            raise ValueError("checker-bundle requested scope values must be unique")
        return values

    @model_validator(mode="after")
    def _manifest_is_consistent(self) -> CheckerBundleManifest:
        invariant_ids = [item.invariant_id for item in self.invariants]
        if len(set(invariant_ids)) != len(invariant_ids):
            raise ValueError("checker-bundle invariant_id values must be unique")
        if len(set(self.included_invariant_ids)) != len(self.included_invariant_ids):
            raise ValueError("checker-bundle included_invariant_ids must be unique")
        if len(set(self.failed_invariant_ids)) != len(self.failed_invariant_ids):
            raise ValueError("checker-bundle failed_invariant_ids must be unique")

        included_ids = set(self.included_invariant_ids)
        failed_ids = set(self.failed_invariant_ids)
        if included_ids & failed_ids:
            raise ValueError(
                "checker-bundle included_invariant_ids and failed_invariant_ids must be disjoint"
            )

        expected_included = {
            item.invariant_id for item in self.invariants if item.final_status == "passed"
        }
        expected_failed = {
            item.invariant_id for item in self.invariants if item.final_status == "failed"
        }
        if included_ids != expected_included:
            raise ValueError(
                "checker-bundle included_invariant_ids must exactly match passed invariants"
            )
        if failed_ids != expected_failed:
            raise ValueError(
                "checker-bundle failed_invariant_ids must exactly match failed invariants"
            )

        has_incomplete_coverage = any(
            item.final_status in {"failed", "unprocessed"} for item in self.invariants
        )
        if self.coverage_complete == has_incomplete_coverage:
            raise ValueError(
                "checker-bundle coverage_complete must be true exactly when every invariant passed"
            )

        build_passed = (
            self.validation.build is not None and self.validation.build.get("status") == "passed"
        )
        artifacts_complete = all(
            artifact is not None
            for artifact in (
                self.artifacts.cumulative_patch,
                self.artifacts.catalog,
                self.artifacts.dispatcher,
                self.artifacts.scoped_invariants,
                self.artifacts.input_scope,
            )
        )
        all_invariants_terminal = all(
            item.final_status != "unprocessed" for item in self.invariants
        )
        no_infrastructure_errors = all(not item.infrastructure_error for item in self.invariants)
        ready_conditions_met = (
            all_invariants_terminal
            and no_infrastructure_errors
            and not self.budget_exhausted
            and bool(self.included_invariant_ids)
            and self.validation.status == "passed"
            and build_passed
            and artifacts_complete
        )
        if (self.status == "ready") != ready_conditions_met:
            raise ValueError(
                "checker-bundle status must be 'ready' exactly when all invariants are "
                "terminal without infrastructure errors or budget exhaustion, at least "
                "one checker is included, validation passed, and the dispatcher build and "
                "all required output and input-provenance artifacts are present"
            )
        return self


class ValidatedCheckerBundle(_StrictModel):
    """A manifest whose referenced files passed containment and integrity checks."""

    manifest: CheckerBundleManifest
    manifest_path: Path
    root: Path
    cumulative_patch: Path | None
    catalog: Path | None
    dispatcher: Path | None
    scoped_invariants: Path | None
    input_scope: Path | None


def compute_bundle_id(
    payload_or_manifest: Mapping[str, Any] | CheckerBundleManifest,
) -> str:
    """Return the SHA-256 of canonical manifest JSON without ``bundle_id``."""

    if isinstance(payload_or_manifest, BaseModel):
        # Preserve the serialized shape that produced the typed model.  In
        # particular, an omitted optional/default field must not materialize
        # during recomputation and change the bundle identity.
        payload: dict[str, Any] = payload_or_manifest.model_dump(mode="json", exclude_unset=True)
    elif isinstance(payload_or_manifest, Mapping):
        payload = dict(payload_or_manifest)
    else:
        raise TypeError("bundle payload must be a mapping or CheckerBundleManifest")
    payload.pop("bundle_id", None)
    canonical = json.dumps(
        payload,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    )
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _manifest_file(path: str | os.PathLike[str]) -> Path:
    """Make the manifest path absolute without rebasing through a file symlink."""

    candidate = Path(path).expanduser()
    try:
        parent = candidate.parent.resolve(strict=True)
    except (OSError, RuntimeError) as exc:
        raise ValueError(f"checker-bundle manifest directory does not resolve: {path!r}") from exc
    manifest_path = parent / candidate.name
    if manifest_path.is_symlink():
        raise ValueError(f"checker-bundle manifest must not be a symlink: {manifest_path}")
    if not manifest_path.is_file():
        raise ValueError(f"checker-bundle manifest is not a regular file: {manifest_path}")
    return manifest_path


def _resolve_artifact(
    artifact: CheckerBundleArtifact,
    *,
    role: str,
    root: Path,
) -> Path:
    relative = PurePosixPath(artifact.path)
    candidate = root.joinpath(*relative.parts)
    try:
        resolved = candidate.resolve(strict=True)
    except (FileNotFoundError, OSError, RuntimeError) as exc:
        raise ValueError(
            f"checker-bundle {role} artifact does not resolve: {artifact.path!r}"
        ) from exc
    try:
        resolved.relative_to(root)
    except ValueError as exc:
        raise ValueError(
            f"checker-bundle {role} artifact escapes manifest root: {artifact.path!r}"
        ) from exc
    if not resolved.is_file():
        raise ValueError(f"checker-bundle {role} artifact is not a regular file: {artifact.path!r}")

    digest = hashlib.sha256()
    size_bytes = 0
    try:
        with resolved.open("rb") as stream:
            if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
                raise ValueError(
                    f"checker-bundle {role} artifact is not a regular file: {artifact.path!r}"
                )
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
                size_bytes += len(chunk)
    except OSError as exc:
        raise ValueError(
            f"checker-bundle {role} artifact cannot be read: {artifact.path!r}"
        ) from exc

    actual_sha256 = digest.hexdigest()
    if not hmac.compare_digest(actual_sha256, artifact.sha256):
        raise ValueError(
            f"checker-bundle {role} artifact SHA-256 mismatch for {artifact.path!r}: "
            f"expected {artifact.sha256}, got {actual_sha256}"
        )
    if artifact.size_bytes is not None and size_bytes != artifact.size_bytes:
        raise ValueError(
            f"checker-bundle {role} artifact size mismatch for {artifact.path!r}: "
            f"expected {artifact.size_bytes}, got {size_bytes}"
        )
    return resolved


def _load_json_object(path: Path, *, role: str) -> Mapping[str, Any]:
    try:
        with path.open("r", encoding="utf-8") as stream:
            payload = json.load(stream)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"checker-bundle {role} artifact must contain valid UTF-8 JSON") from exc
    if not isinstance(payload, Mapping):
        raise ValueError(f"checker-bundle {role} artifact must contain a JSON object")
    return payload


def _scope_string_list(value: Any, *, field: str) -> list[str]:
    if not isinstance(value, list) or any(
        not isinstance(item, str) or not item or item != item.strip() for item in value
    ):
        raise ValueError(
            f"checker-bundle input_scope {field} must be a list of non-empty strings"
        )
    if len(set(value)) != len(value):
        raise ValueError(f"checker-bundle input_scope {field} values must be unique")
    return value


def _validate_input_provenance(
    manifest: CheckerBundleManifest,
    *,
    scoped_invariants: Path | None,
    input_scope: Path | None,
) -> None:
    """Cross-check the two content-addressed inputs against manifest lineage."""

    if scoped_invariants is None or input_scope is None:
        return
    scope_payload = _load_json_object(input_scope, role="input_scope")
    if scope_payload.get("schema_version") != 1:
        raise ValueError("checker-bundle input_scope schema_version must be 1")
    if scope_payload.get("kind") != "defuzz-checker-input-scope":
        raise ValueError(
            "checker-bundle input_scope kind must be 'defuzz-checker-input-scope'"
        )

    source_artifact = scope_payload.get("source_artifact")
    if not isinstance(source_artifact, Mapping):
        raise ValueError(
            "checker-bundle input_scope source_artifact must contain a JSON object"
        )
    if source_artifact.get("sha256") != manifest.source_invariants_sha256:
        raise ValueError(
            "checker-bundle source_invariants_sha256 does not match input_scope "
            "source_artifact.sha256"
        )

    requested = scope_payload.get("requested")
    if not isinstance(requested, Mapping):
        raise ValueError("checker-bundle input_scope requested must contain a JSON object")
    mechanisms = _scope_string_list(
        requested.get("mechanisms"), field="requested.mechanisms"
    )
    isas = _scope_string_list(requested.get("isas"), field="requested.isas")
    if scope_payload.get("scope_requested") is not bool(mechanisms or isas):
        raise ValueError(
            "checker-bundle input_scope scope_requested does not match requested scope"
        )
    if mechanisms != manifest.requested_mechanisms:
        raise ValueError(
            "checker-bundle requested_mechanisms do not match input_scope requested.mechanisms"
        )
    if isas != manifest.requested_isas:
        raise ValueError(
            "checker-bundle requested_isas do not match input_scope requested.isas"
        )

    selected_ids = _scope_string_list(
        scope_payload.get("selected_invariant_ids"), field="selected_invariant_ids"
    )
    scoped_ids: list[str] = []
    try:
        with scoped_invariants.open("r", encoding="utf-8") as stream:
            for line_number, line in enumerate(stream, 1):
                if not line.strip():
                    continue
                row = json.loads(line)
                if not isinstance(row, Mapping):
                    raise ValueError(
                        f"checker-bundle scoped_invariants line {line_number} must be an object"
                    )
                invariant_id = row.get("invariant_id")
                if (
                    not isinstance(invariant_id, str)
                    or not invariant_id
                    or invariant_id != invariant_id.strip()
                ):
                    raise ValueError(
                        "checker-bundle scoped_invariants invariant_id must be a "
                        "non-empty string"
                    )
                scoped_ids.append(invariant_id)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError(
            "checker-bundle scoped_invariants artifact must contain valid UTF-8 JSONL"
        ) from exc
    if len(set(scoped_ids)) != len(scoped_ids):
        raise ValueError("checker-bundle scoped_invariants invariant_id values must be unique")
    if scoped_ids != selected_ids:
        raise ValueError(
            "checker-bundle scoped_invariants IDs do not match input_scope "
            "selected_invariant_ids"
        )


def validate_checker_bundle(
    manifest: CheckerBundleManifest | Mapping[str, Any],
    manifest_path: str | os.PathLike[str],
    *,
    require_ready: bool = True,
) -> ValidatedCheckerBundle:
    """Validate a parsed manifest and all artifacts relative to its directory."""

    source_payload: CheckerBundleManifest | Mapping[str, Any] = manifest
    typed_manifest = (
        manifest
        if isinstance(manifest, CheckerBundleManifest)
        else CheckerBundleManifest.model_validate(manifest)
    )
    path = _manifest_file(manifest_path)
    root = path.parent.resolve(strict=True)

    if require_ready and typed_manifest.status != "ready":
        raise ValueError(f"checker-bundle status is {typed_manifest.status!r}; expected 'ready'")

    actual_bundle_id = compute_bundle_id(source_payload)
    if not hmac.compare_digest(actual_bundle_id, typed_manifest.bundle_id):
        raise ValueError(
            "checker-bundle bundle_id mismatch: "
            f"expected {typed_manifest.bundle_id}, got {actual_bundle_id}"
        )

    references = (
        ("cumulative_patch", typed_manifest.artifacts.cumulative_patch),
        ("catalog", typed_manifest.artifacts.catalog),
        ("dispatcher", typed_manifest.artifacts.dispatcher),
        ("scoped_invariants", typed_manifest.artifacts.scoped_invariants),
        ("input_scope", typed_manifest.artifacts.input_scope),
    )
    resolved_by_role: dict[str, Path] = {}
    seen_resolved: dict[Path, str] = {}
    for role, artifact in references:
        if artifact is None:
            continue
        resolved = _resolve_artifact(artifact, role=role, root=root)
        previous_role = seen_resolved.get(resolved)
        if previous_role is not None:
            raise ValueError(
                "checker-bundle artifact paths resolve to the same file: "
                f"{previous_role!r} and {role!r}"
            )
        seen_resolved[resolved] = role
        resolved_by_role[role] = resolved

    _validate_input_provenance(
        typed_manifest,
        scoped_invariants=resolved_by_role.get("scoped_invariants"),
        input_scope=resolved_by_role.get("input_scope"),
    )

    return ValidatedCheckerBundle(
        manifest=typed_manifest,
        manifest_path=path,
        root=root,
        cumulative_patch=resolved_by_role.get("cumulative_patch"),
        catalog=resolved_by_role.get("catalog"),
        dispatcher=resolved_by_role.get("dispatcher"),
        scoped_invariants=resolved_by_role.get("scoped_invariants"),
        input_scope=resolved_by_role.get("input_scope"),
    )


def load_checker_bundle(
    path_or_dir: str | os.PathLike[str],
    *,
    require_ready: bool = True,
) -> ValidatedCheckerBundle:
    """Load a manifest path or bundle directory and validate its complete trust boundary."""

    candidate = Path(path_or_dir).expanduser()
    if candidate.is_dir():
        candidate = candidate / CHECKER_BUNDLE_MANIFEST_FILENAME
    manifest_path = _manifest_file(candidate)
    try:
        with manifest_path.open("r", encoding="utf-8") as stream:
            payload = json.load(stream)
    except OSError as exc:
        raise ValueError(f"checker-bundle manifest cannot be read: {manifest_path}") from exc
    if not isinstance(payload, Mapping):
        raise ValueError("checker-bundle manifest must contain a JSON object")
    return validate_checker_bundle(payload, manifest_path, require_ready=require_ready)


__all__ = [
    "CHECKER_BUNDLE_MANIFEST_FILENAME",
    "CheckerBundleArtifact",
    "CheckerBundleArtifacts",
    "CheckerBundleInvariant",
    "CheckerBundleManifest",
    "CheckerBundleValidation",
    "ValidatedCheckerBundle",
    "compute_bundle_id",
    "load_checker_bundle",
    "validate_checker_bundle",
]
