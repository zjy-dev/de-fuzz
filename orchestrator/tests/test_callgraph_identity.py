"""Version drift must be rejected before a source-mapping model request."""

import hashlib
import json
import sqlite3
from pathlib import Path

import pytest

from defuzz_loop.experiment_engine.callgraph_identity import (
    bind_callgraph,
    database_digest,
    verify_driver_versions,
)


def graph_fixture(tmp_path: Path) -> tuple[Path, Path]:
    root = tmp_path / "source"
    (root / "gcc").mkdir(parents=True)
    (root / "gcc/BASE-VER").write_text("16.2.0\n")
    (root / "gcc/cfgexpand.cc").write_text("void stack_protect() {}\n")
    provenance = {
        "compiler": "gcc", "version": "16.2.0", "isas": ["aarch64"],
        "source_revision": "release-commit", "csv_sha256": "graph-csv",
        "source_files_sha256": {
            "gcc/cfgexpand.cc": hashlib.sha256((root / "gcc/cfgexpand.cc").read_bytes()).hexdigest()
        },
    }
    database = tmp_path / "graph.sqlite"
    with sqlite3.connect(database) as con:
        con.execute("CREATE TABLE provenance(target TEXT, payload TEXT)")
        con.execute("INSERT INTO provenance VALUES (?,?)", ("gcc-aarch64", json.dumps(provenance)))
    return database, root


def test_matching_source_and_database_identity(tmp_path):
    database, root = graph_fixture(tmp_path)
    result = bind_callgraph(database, root, compiler="GCC", isas=["aarch64"])
    assert result["targets"] == ["gcc-aarch64"]
    assert result["database_sha256"] == database_digest(database)
    assert result["bindings"][0]["verified_source_files"] == 1


@pytest.mark.parametrize("change,match", [
    ("version", "no matching gcc 17"),
    ("bytes", "source bytes differ"),
    ("coverage", "no matching"),
    ("revision", "revision mismatch"),
    ("no_scope", "explicit ISA"),
    ("escape", "escapes root"),
])
def test_drift_is_rejected(tmp_path, change, match):
    database, root = graph_fixture(tmp_path)
    isas = ["aarch64"]
    revision = None
    if change == "version":
        (root / "gcc/BASE-VER").write_text("17.0.0\n")
    elif change == "bytes":
        (root / "gcc/cfgexpand.cc").write_text("modified source\n")
    elif change == "coverage":
        isas = ["riscv64"]
    elif change == "revision":
        revision = "another-commit"
    elif change == "no_scope":
        isas = []
    elif change == "escape":
        with sqlite3.connect(database) as con:
            raw = json.loads(con.execute("SELECT payload FROM provenance").fetchone()[0])
            raw["source_files_sha256"] = {"../outside": "hash"}
            con.execute("UPDATE provenance SET payload=?", (json.dumps(raw),))
    with pytest.raises(ValueError, match=match):
        bind_callgraph(database, root, compiler="gcc", isas=isas, source_revision=revision)


def test_driver_version_and_database_replacement(tmp_path):
    database, root = graph_fixture(tmp_path)
    binding = bind_callgraph(database, root, compiler="gcc", isas=["aarch64"])
    driver = tmp_path / "compiler"
    driver.write_text("#!/bin/sh\nprintf '17.0.0\\n'\n")
    driver.chmod(0o755)
    toolchains = tmp_path / "toolchains.yaml"
    toolchains.write_text(f"toolchains:\n  aarch64:\n    gcc_path: {driver}\n")
    with pytest.raises(ValueError, match="toolchain version mismatch"):
        verify_driver_versions(binding, toolchains)
    with sqlite3.connect(database) as con:
        con.execute("UPDATE provenance SET target='changed'")
    assert database_digest(database) != binding["database_sha256"]
