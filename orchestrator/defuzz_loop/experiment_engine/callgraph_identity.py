"""Resolve a CallGraph target from source bytes instead of agent-selected labels."""

from __future__ import annotations

import hashlib
import json
import re
import sqlite3
import subprocess
from functools import lru_cache
from pathlib import Path
from typing import Any


@lru_cache(maxsize=8)
def _database_digest(path: str, size: int, mtime: int, ctime: int) -> str:
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def database_digest(path: Path) -> str:
    path = path.resolve(strict=True)
    info = path.stat()
    return _database_digest(str(path), info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def source_identity(root: Path) -> tuple[str, str]:
    for path in (root / "gcc/BASE-VER", root / "BASE-VER"):
        if path.is_file():
            return "gcc", path.read_text().strip()
    path = root / "cmake/Modules/LLVMVersion.cmake"
    if not path.is_file():
        path = root / "llvm/cmake/Modules/LLVMVersion.cmake"
    if path.is_file():
        text = path.read_text()
        version = [
            re.search(rf"set\(LLVM_VERSION_{field}\s+(\d+)\)", text)
            for field in ("MAJOR", "MINOR", "PATCH")
        ]
        if all(version):
            return "llvm", ".".join(match[1] for match in version if match)
    raise ValueError("CallGraph binding requires a compiler source tree with version metadata")


def bind_callgraph(
    database: Path,
    source_root: Path,
    *,
    compiler: str,
    isas: list[str],
    source_revision: str | None = None,
) -> dict[str, Any]:
    """Verify release-source anchors and select only applicable graph targets."""
    source_root = source_root.resolve(strict=True)
    observed_compiler, version = source_identity(source_root)
    compiler = compiler.strip().lower()
    compiler = "llvm" if compiler == "clang" else compiler
    if observed_compiler != compiler:
        raise ValueError("CallGraph source compiler does not match the selected compiler")
    if not isas:
        raise ValueError("CallGraph binding requires explicit ISA scope")
    if source_revision is None:
        repository = subprocess.run(
            ["git", "-C", str(source_root), "rev-parse", "--show-toplevel"],
            capture_output=True, text=True, timeout=10,
        )
        if repository.returncode == 0 and Path(repository.stdout.strip()).resolve() in {
            source_root,
            source_root.parent if source_root.name == "gcc" else source_root,
        }:
            head = subprocess.run(
                ["git", "-C", str(source_root), "rev-parse", "HEAD"],
                capture_output=True, text=True, timeout=10,
            )
            if head.returncode == 0:
                source_revision = head.stdout.strip()
    database = database.resolve(strict=True)
    checksum = database_digest(database)
    con = sqlite3.connect(f"file:{database.resolve()}?mode=ro", uri=True)
    try:
        try:
            rows = con.execute("SELECT target,payload FROM provenance").fetchall()
        except sqlite3.OperationalError as exc:
            raise ValueError("CallGraph database lacks source provenance; rebuild it") from exc
    finally:
        con.close()
    bindings = []
    uncovered = set(isas)
    for target, raw in sorted(rows):
        provenance = json.loads(raw)
        applicable = uncovered.intersection(provenance.get("isas", []))
        if (
            provenance.get("compiler") != compiler
            or provenance.get("version") != version
            or not applicable
        ):
            continue
        if source_revision is not None and provenance.get("source_revision") != source_revision:
            raise ValueError("CallGraph source revision mismatch")
        anchors = provenance.get("source_files_sha256", {})
        if not anchors:
            raise ValueError(f"CallGraph target {target} has no source-byte identity")
        checked = 0
        for relative, expected in anchors.items():
            relative_path = Path(relative)
            if relative_path.is_absolute() or ".." in relative_path.parts:
                raise ValueError("CallGraph source anchor escapes root")
            path = source_root / relative_path
            anchor_root = source_root
            if not path.exists() and compiler == "gcc" and source_root.name == "gcc":
                path = source_root.parent / relative
                anchor_root = source_root.parent
            path = path.resolve(strict=True)
            if not path.is_relative_to(anchor_root):
                raise ValueError("CallGraph source anchor escapes root")
            if not path.is_file():
                raise ValueError(f"CallGraph source is missing {relative}")
            with path.open("rb") as stream:
                actual = hashlib.file_digest(stream, "sha256").hexdigest()
            if actual != expected:
                raise ValueError(f"CallGraph source bytes differ: {relative}")
            checked += 1
        bindings.append(
            {
                "target": target,
                "compiler": compiler,
                "version": version,
                "source_revision": provenance.get("source_revision"),
                "isas": sorted(applicable),
                "csv_sha256": provenance["csv_sha256"],
                "verified_source_files": checked,
                "revision_evidence": provenance.get("revision_evidence"),
            }
        )
        uncovered -= applicable
    if uncovered:
        raise ValueError(
            f"CallGraph has no matching {compiler} {version} data for {sorted(uncovered)}; "
            "generate that graph or select its matching frozen source/toolchain"
        )
    return {
        "database": str(database.resolve()),
        "database_sha256": checksum,
        "compiler": compiler,
        "version": version,
        "bindings": bindings,
        "targets": [row["target"] for row in bindings],
    }


def verify_driver_versions(binding: dict[str, Any], toolchains: Path) -> dict[str, str]:
    """Probe the actual configured drivers, including trusted wrapper arguments."""
    import yaml

    entries = yaml.safe_load(toolchains.read_text())["toolchains"]
    field = "gcc_path" if binding["compiler"] == "gcc" else "clang_path"
    versions = {}
    for isa in sorted({isa for item in binding["bindings"] for isa in item["isas"]}):
        entry = entries.get(isa, {})
        if not entry.get(field):
            raise ValueError(f"CallGraph-aligned toolchain is missing {field} for {isa}")
        args = ["-dumpfullversion"] if binding["compiler"] == "gcc" else ["--version"]
        probe = subprocess.run(
            [entry[field], *entry.get("driver_args", []), *args],
            capture_output=True, text=True, timeout=30,
        )
        text = probe.stdout.strip()
        match = re.search(r"clang version (\d+\.\d+\.\d+)", text)
        version = text if binding["compiler"] == "gcc" else (match[1] if match else "")
        if probe.returncode or version != binding["version"]:
            raise ValueError(
                f"CallGraph/toolchain version mismatch for {isa}: "
                f"expected {binding['version']}, observed {version or probe.stderr.strip()}"
            )
        versions[isa] = version
    return versions
