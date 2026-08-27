from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

import pytest

from defuzz_loop.experiment_engine.gcc_security_corpus import build_corpus


def _write(root: Path, relative: str, content: str) -> None:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def _gcc_fixture(root: Path, *, include_sanitizers: bool = True) -> None:
    _write(root, "LAST_UPDATED", "fixture-revision")
    _write(root, "gcc/BASE-VER", "16.0.0")
    _write(root, "gcc/DATESTAMP", "20260828")
    _write(root, "gcc/cfgexpand.cc", "void guard() { stack_protect_set(); }\n")
    _write(root, "gcc/fold-const.cc", "int x = __builtin_dynamic_object_size(p, 0);\n")
    _write(root, "gcc/config/i386/i386.cc", "void cfi() { emit_endbr (); }\n")
    _write(root, "gcc/config/i386/gnu-stack.S", ".section .note.GNU-stack\n")
    if include_sanitizers:
        _write(root, "gcc/asan.cc", "void asan_pass() {}\n")
    _write(root, "gcc/testsuite/gcc.dg/not-selected.c", "stack_protect_set();\n")
    _write(root, "gcc/unrelated.cc", "int ordinary_compiler_file;\n")


def test_builder_copies_a_deterministic_bounded_five_family_corpus(tmp_path: Path) -> None:
    source = tmp_path / "gcc"
    _gcc_fixture(source)

    first = build_corpus(source, tmp_path / "first")
    second = build_corpus(source, tmp_path / "second")

    assert first == second
    assert first["source_identity"] == {
        "LAST_UPDATED": "fixture-revision",
        "BASE-VER": "16.0.0",
        "DATESTAMP": "20260828",
        "git_head": None,
        "git_clean": None,
    }
    assert first["rag_curated_paths"]["count"] == 16  # type: ignore[index]
    assert "gcc/config/i386/cet.h" in first["rag_curated_paths"]["paths"]  # type: ignore[index]
    assert first["family_counts"] == {
        "stack_protection": 1,
        "memory_bounds": 1,
        "control_flow_integrity": 1,
        "elf_address_space": 1,
        "sanitizers_hardening": 1,
    }
    assert first["file_count"] == 5
    estimated_tokens = first["estimated_tokens"]
    assert isinstance(estimated_tokens, int)
    assert estimated_tokens > 0
    assert not (tmp_path / "first" / "sources" / "gcc" / "testsuite").exists()
    assert not (tmp_path / "first" / "sources" / "gcc" / "unrelated.cc").exists()
    assert (tmp_path / "first" / "manifest.json").read_bytes() == (
        tmp_path / "second" / "manifest.json"
    ).read_bytes()
    assert json.loads((tmp_path / "first" / "manifest.json").read_text())["file_count"] == 5


def test_builder_round_robins_architectures_before_lexical_backend_bulk(tmp_path: Path) -> None:
    source = tmp_path / "gcc"
    _gcc_fixture(source)
    for index in range(50):
        _write(
            source,
            f"gcc/config/bfin/stack-{index:02}.cc",
            "void guard() { stack_protect_set(); }\n",
        )
    _write(source, "gcc/calls.cc", "void call_guard() {}\n")
    _write(source, "gcc/function.cc", "void function_guard() {}\n")
    _write(source, "gcc/tree-ssa-strlen.cc", "void strlen_pass() {}\n")
    _write(source, "gcc/tree-ssa-forwprop.cc", "void forward_pass() {}\n")
    _write(source, "gcc/tree-ssa-dse.cc", "void dse_pass() {}\n")
    _write(source, "gcc/common.opt", "fhardened\n")
    _write(source, "gcc/config/aarch64/aarch64.cc", "void bti() {}\n")
    _write(source, "gcc/config/riscv/riscv.cc", "void bti() {}\n")

    manifest = build_corpus(source, tmp_path / "corpus")
    files = manifest["files"]
    assert isinstance(files, list)
    paths = {str(entry["path"]) for entry in files if isinstance(entry, dict)}

    assert {
        "gcc/calls.cc",
        "gcc/cfgexpand.cc",
        "gcc/function.cc",
        "gcc/tree-ssa-strlen.cc",
        "gcc/tree-ssa-forwprop.cc",
        "gcc/tree-ssa-dse.cc",
        "gcc/config/i386/i386.cc",
        "gcc/config/aarch64/aarch64.cc",
        "gcc/config/riscv/riscv.cc",
    }.issubset(paths)
    architecture_counts = manifest["architecture_counts"]
    assert isinstance(architecture_counts, dict)
    assert int(architecture_counts["other_backends"]) < 30


def test_builder_fails_closed_when_a_family_has_no_matching_source(tmp_path: Path) -> None:
    source = tmp_path / "gcc"
    _gcc_fixture(source, include_sanitizers=False)

    with pytest.raises(ValueError, match="sanitizers_hardening"):
        build_corpus(source, tmp_path / "corpus")


def test_builder_never_follows_source_symlinks(tmp_path: Path) -> None:
    source = tmp_path / "gcc"
    _gcc_fixture(source)
    outside = tmp_path / "outside"
    _write(outside, "secret.cc", "void leak() { emit_endbr(); }\n")
    (source / "gcc" / "linked-secret.cc").symlink_to(outside / "secret.cc")
    (source / "gcc" / "linked-dir").symlink_to(outside, target_is_directory=True)

    manifest = build_corpus(source, tmp_path / "corpus")
    paths = {
        str(entry["path"])
        for entry in manifest["files"]  # type: ignore[union-attr]
        if isinstance(entry, dict)
    }

    assert "gcc/linked-secret.cc" not in paths
    assert not any(path.startswith("gcc/linked-dir/") for path in paths)
    assert not (tmp_path / "corpus/sources/gcc/linked-secret.cc").exists()


def test_builder_refuses_existing_or_symlinked_output(tmp_path: Path) -> None:
    source = tmp_path / "gcc"
    _gcc_fixture(source)
    valuable = tmp_path / "valuable"
    valuable.mkdir()
    marker = valuable / "keep.txt"
    marker.write_text("keep\n", encoding="utf-8")
    output = tmp_path / "corpus"
    output.symlink_to(valuable, target_is_directory=True)

    with pytest.raises(ValueError, match="output directory already exists"):
        build_corpus(source, output)

    assert marker.read_text(encoding="utf-8") == "keep\n"


def test_builder_records_git_head_and_dirty_state(tmp_path: Path) -> None:
    source = tmp_path / "gcc"
    _gcc_fixture(source)
    subprocess.run(["git", "init", "-q", str(source)], check=True)
    subprocess.run(["git", "-C", str(source), "add", "."], check=True)
    subprocess.run(
        [
            "git",
            "-C",
            str(source),
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "commit",
            "-qm",
            "fixture",
        ],
        check=True,
    )
    head = subprocess.run(
        ["git", "-C", str(source), "rev-parse", "HEAD"],
        capture_output=True,
        check=True,
        text=True,
    ).stdout.strip()

    clean = build_corpus(source, tmp_path / "clean")["source_identity"]
    assert clean["git_head"] == head  # type: ignore[index]
    assert clean["git_clean"] is True  # type: ignore[index]

    _write(source, "gcc/asan.cc", "void changed_asan_pass() {}\n")
    dirty = build_corpus(source, tmp_path / "dirty")["source_identity"]
    assert dirty["git_head"] == head  # type: ignore[index]
    assert dirty["git_clean"] is False  # type: ignore[index]


def test_builder_cli_exposes_help() -> None:
    result = subprocess.run(
        [sys.executable, "-m", "defuzz_loop.experiment_engine.gcc_security_corpus", "--help"],
        capture_output=True,
        check=False,
        text=True,
    )

    assert result.returncode == 0
    assert "gcc_source" in result.stdout
