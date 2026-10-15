"""Versioned graph provenance and strict source identity checks."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
from typing import Any


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_manifest(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if value.get("schema_version") != 1 or not isinstance(value.get("targets"), dict):
        raise ValueError("invalid callgraph provenance manifest")
    return value


def validate_target(
    provenance: dict[str, Any],
    *,
    compiler: str,
    version: str,
    isa: str,
    source_revision: str | None = None,
    source_root: Path | None = None,
) -> None:
    if provenance.get("compiler") != compiler or provenance.get("version") != version:
        raise ValueError("CallGraph compiler/version does not match the requested source")
    if isa not in provenance.get("isas", []):
        raise ValueError(f"CallGraph has no verified {isa} build coverage")
    if source_revision is not None and provenance.get("source_revision") != source_revision:
        raise ValueError("CallGraph source revision mismatch")
    if source_root is not None:
        anchors = provenance.get("source_files_sha256", {})
        if not anchors:
            raise ValueError("CallGraph has no verified source-file hashes")
        for relative, expected in anchors.items():
            path = (source_root / relative).resolve(strict=True)
            if not path.is_relative_to(source_root.resolve()):
                raise ValueError("CallGraph source anchor escapes root")
            if file_sha256(path) != expected:
                raise ValueError(f"CallGraph source bytes differ: {relative}")
