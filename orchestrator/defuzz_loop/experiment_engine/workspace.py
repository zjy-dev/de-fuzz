"""Leak-resistant workspace materialization for experiment agents."""

from __future__ import annotations

import fnmatch
import hashlib
import os
import shutil
from collections.abc import Iterable, Sequence
from pathlib import Path, PurePosixPath

from pydantic import BaseModel, ConfigDict, Field

from .models import ArtifactRef


class WorkspaceSecurityError(ValueError):
    """Raised when a source entry can escape or contaminate the workspace."""


def _is_within(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
    except ValueError:
        return False
    return True


def validate_agent_path_isolation(
    *,
    cwd: str | os.PathLike[str],
    output_dir: str | os.PathLike[str],
    deny_read_paths: Sequence[str | os.PathLike[str]],
    schema_path: str | os.PathLike[str] | None = None,
) -> list[Path]:
    """Resolve and validate an agent request's host-read boundary.

    A deny root must never contain a path the request needs in order to start or
    return evidence.  Treat either-direction overlap with cwd/output as a
    configuration error: denying a cwd parent makes the request unusable, while
    denying a cwd child would make the advertised workspace only partially
    readable.  Schema files need only remain outside denied subtrees.
    """

    resolved_cwd = Path(cwd).expanduser().resolve(strict=True)
    if not resolved_cwd.is_dir():
        raise WorkspaceSecurityError(f"agent cwd is not a directory: {resolved_cwd}")
    resolved_output = Path(output_dir).expanduser().resolve(strict=False)
    required: list[tuple[str, Path, bool]] = [
        ("cwd", resolved_cwd, True),
        ("output_dir", resolved_output, True),
    ]
    if schema_path is not None:
        required.append(
            ("schema_path", Path(schema_path).expanduser().resolve(strict=False), False)
        )

    denied = list(
        dict.fromkeys(
            Path(item).expanduser().resolve(strict=False) for item in deny_read_paths
        )
    )
    for deny_root in denied:
        if deny_root == Path("/"):
            raise WorkspaceSecurityError("refusing to deny the filesystem root")
        for label, required_path, reject_descendant in required:
            collides = _is_within(required_path, deny_root) or (
                reject_descendant and _is_within(deny_root, required_path)
            )
            if collides:
                raise WorkspaceSecurityError(
                    f"deny_read_path {deny_root} collides with agent {label} "
                    f"{required_path}"
                )
    return denied


def validate_disjoint_input_roots(
    roots: Sequence[tuple[str, str | os.PathLike[str]]],
) -> list[Path]:
    """Reject original input roots whose containment would mix treatments."""

    resolved = [
        (label, Path(path).expanduser().resolve(strict=False)) for label, path in roots
    ]
    for index, (label, path) in enumerate(resolved):
        for other_label, other in resolved[index + 1 :]:
            if _is_within(path, other) or _is_within(other, path):
                raise WorkspaceSecurityError(
                    f"original input roots overlap: {label} {path} and "
                    f"{other_label} {other}"
                )
    return [path for _, path in resolved]


class WorkspaceManifest(BaseModel):
    model_config = ConfigDict(arbitrary_types_allowed=True)

    root: Path
    files: list[ArtifactRef] = Field(default_factory=list)
    sha256: str
    excluded: list[str] = Field(default_factory=list)

    @property
    def path(self) -> Path:
        return self.root

    def __fspath__(self) -> str:
        return os.fspath(self.root)


class WorkspaceBuilder:
    """Copy only approved inputs while excluding result and provenance corpora."""

    def __init__(
        self,
        source_root: str | os.PathLike[str],
        *,
        allowlist: Iterable[str | os.PathLike[str]] | None = None,
    ) -> None:
        self.source_root = Path(source_root).expanduser().resolve(strict=True)
        if not self.source_root.is_dir():
            raise ValueError(f"workspace source is not a directory: {self.source_root}")
        normalized: list[str] = []
        for item in allowlist or ():
            candidate = PurePosixPath(os.fspath(item))
            if candidate.is_absolute() or ".." in candidate.parts:
                raise ValueError(f"allowlist entry must be relative and contained: {item}")
            normalized.append(candidate.as_posix())
        self.allowlist = tuple(normalized)

    @staticmethod
    def _denied(relative: PurePosixPath) -> bool:
        folded = tuple(part.casefold() for part in relative.parts)
        if any(part in {".git", "findings"} for part in folded):
            return True
        if folded and folded[0] in {"runs", "experiments", "fuzz_out", "compiler-audit", ".work"}:
            return True
        if "runs" in folded:
            return True
        if folded and folded[-1] in {"findings.md", "findings.json", "findings.jsonl"}:
            return True
        for index, part in enumerate(folded[:-1]):
            if part == "reports" and any(
                child.startswith("drev") for child in folded[index + 1 :]
            ):
                return True
        return False

    def _allowed(self, relative: PurePosixPath) -> bool:
        if not self.allowlist:
            return True
        name = relative.as_posix()
        for pattern in self.allowlist:
            prefix = pattern.rstrip("/")
            if name == prefix or name.startswith(prefix + "/") or fnmatch.fnmatch(name, pattern):
                return True
            if prefix.startswith(name + "/"):
                return True
        return False

    def materialize(self, destination: str | os.PathLike[str]) -> WorkspaceManifest:
        target = Path(destination).expanduser().resolve(strict=False)
        if target.exists() and any(target.iterdir()):
            raise FileExistsError(f"workspace destination is not empty: {target}")
        target.mkdir(parents=True, exist_ok=True)
        excluded: list[str] = []
        target_within_source: Path | None = None
        try:
            target_within_source = target.relative_to(self.source_root)
        except ValueError:
            pass
        self._copy_tree(
            self.source_root,
            PurePosixPath("."),
            target,
            excluded,
            ancestry=(),
            target_within_source=target_within_source,
        )
        files = [
            ArtifactRef.from_path(path, base_dir=target)
            for path in sorted(candidate for candidate in target.rglob("*") if candidate.is_file())
        ]
        aggregate = hashlib.sha256()
        for item in files:
            aggregate.update(item.path.encode("utf-8"))
            aggregate.update(b"\0")
            aggregate.update(item.sha256.encode("ascii"))
            aggregate.update(b"\n")
        return WorkspaceManifest(
            root=target, files=files, sha256=aggregate.hexdigest(), excluded=sorted(set(excluded))
        )

    build = materialize

    def materialize_path(self, destination: str | os.PathLike[str]) -> Path:
        return self.materialize(destination).root

    def _copy_tree(
        self,
        physical_dir: Path,
        logical_dir: PurePosixPath,
        destination: Path,
        excluded: list[str],
        *,
        ancestry: tuple[Path, ...],
        target_within_source: Path | None,
    ) -> None:
        resolved_dir = physical_dir.resolve(strict=True)
        if resolved_dir in ancestry:
            raise WorkspaceSecurityError(f"symlink cycle detected at {logical_dir.as_posix()}")
        for entry in sorted(physical_dir.iterdir(), key=lambda item: item.name):
            relative = (
                logical_dir / entry.name
                if logical_dir.parts
                else PurePosixPath(entry.name)
            )
            display = relative.as_posix().removeprefix("./")
            if target_within_source is not None:
                destination_prefix = PurePosixPath(target_within_source.as_posix())
                if relative == destination_prefix or destination_prefix in relative.parents:
                    excluded.append(display)
                    continue
            if self._denied(relative) or not self._allowed(relative):
                excluded.append(display)
                continue
            source = entry
            if entry.is_symlink():
                try:
                    source = entry.resolve(strict=True)
                    source.relative_to(self.source_root)
                except (FileNotFoundError, RuntimeError, ValueError) as exc:
                    raise WorkspaceSecurityError(f"symlink escapes source root: {display}") from exc
                target_relative = PurePosixPath(source.relative_to(self.source_root).as_posix())
                if self._denied(target_relative):
                    excluded.append(display)
                    continue
            output = destination / display
            if source.is_dir():
                output.mkdir(parents=True, exist_ok=True)
                self._copy_tree(
                    source,
                    relative,
                    destination,
                    excluded,
                    ancestry=(*ancestry, resolved_dir),
                    target_within_source=target_within_source,
                )
            elif source.is_file():
                output.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(source, output, follow_symlinks=True)
                shutil.copymode(source, output, follow_symlinks=True)


__all__ = [
    "WorkspaceBuilder",
    "WorkspaceManifest",
    "WorkspaceSecurityError",
    "validate_agent_path_isolation",
    "validate_disjoint_input_roots",
]
