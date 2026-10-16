"""Build a bounded, reproducible GCC security-review source corpus.

Rules encode the five Part I review families. They select implementation
sources by GCC subtree and security-specific terms, never review findings.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path

MAX_FILES_PER_FAMILY = 30
MAX_TOTAL_FILES = 150
MAX_TOTAL_CHARS = 2_000_000
MAX_FILE_CHARS = 200_000
MAX_REQUIRED_FILE_CHARS = 250_000
TOKEN_CHARS = 4
_SOURCE_SUFFIXES = {".c", ".cc", ".cp", ".cpp", ".h", ".md", ".opt", ".s", ".S"}
_ARCHITECTURES = ("generic_core", "x86_i386", "aarch64_arm", "riscv", "other_backends")
_PROMPT_PRIORITY_PATHS: tuple[str, ...] = (
    "gcc/calls.cc",
    "gcc/cfgexpand.cc",
    "gcc/function.cc",
    "gcc/tree-ssa-strlen.cc",
    "gcc/tree-ssa-forwprop.cc",
    "gcc/tree-ssa-dse.cc",
    "gcc/fold-const.cc",
    "gcc/common.opt",
    "gcc/opts.cc",
    "gcc/opts-global.cc",
)
_REQUIRED_FILE_FAMILIES = {
    "gcc/calls.cc": ("stack_protection",),
    "gcc/cfgexpand.cc": ("stack_protection",),
    "gcc/function.cc": ("stack_protection",),
    "gcc/tree-ssa-strlen.cc": ("memory_bounds",),
    "gcc/tree-ssa-forwprop.cc": ("memory_bounds",),
    "gcc/tree-ssa-dse.cc": ("memory_bounds",),
    "gcc/fold-const.cc": ("memory_bounds",),
    "gcc/common.opt": ("sanitizers_hardening",),
    "gcc/opts.cc": ("sanitizers_hardening",),
    "gcc/opts-global.cc": ("sanitizers_hardening",),
}

# This is the existing 16-path retrieval whitelist, reported separately from
# the balanced segmented corpus.  The split-corpora pipeline may read these
# paths from the full checkout; this builder neither selects nor copies them
# merely because they are RAG inputs.
RAG_CURATED_PATHS: tuple[str, ...] = (
    "gcc/tree-object-size.cc",
    "gcc/builtins.cc",
    "gcc/tree-ssa-strlen.cc",
    "gcc/gimple-fold.cc",
    "gcc/cfgexpand.cc",
    "gcc/function.cc",
    "gcc/explow.cc",
    "gcc/ipa-strub.cc",
    "gcc/config/aarch64/aarch64.cc",
    "gcc/config/i386/i386.cc",
    "gcc/config/riscv/riscv.cc",
    "gcc/config/arm/arm.cc",
    "gcc/config/aarch64/aarch64.md",
    "gcc/config/arm/arm.md",
    "gcc/config/i386/predicates.md",
    "gcc/config/i386/cet.h",
)


@dataclass(frozen=True)
class FamilyRule:
    """A transparent, path-constrained content rule for one review family."""

    name: str
    mechanisms: tuple[str, ...]
    path_prefixes: tuple[str, ...]
    content_patterns: tuple[str, ...]

    def matches(self, relative_path: str, text: str) -> bool:
        return any(relative_path.startswith(prefix) for prefix in self.path_prefixes) and any(
            re.search(pattern, text, flags=re.IGNORECASE) is not None
            for pattern in self.content_patterns
        )


# Derived from docs/prompts/full-review.md's named mechanisms and GCC areas.
FAMILY_RULES: tuple[FamilyRule, ...] = (
    FamilyRule(
        "stack_protection",
        ("stack-protector", "stack-clash-protection"),
        ("gcc/", "libgcc/"),
        (
            r"stack[_ -]?(?:protect|clash|guard|canary)|morestack|split[_ -]?stack",
            r"have_stack_protect_(?:set|test)|allocate_stack_worker",
        ),
    ),
    FamilyRule(
        "memory_bounds",
        ("fortify-source", "strict-flex-arrays", "zero-init-padding", "source-annotations"),
        ("gcc/",),
        (
            r"_fortify_source|builtin_(?:dynamic_)?object_size|(?:mem|str|sn)\w*_chk",
            r"strict[_ -]?flex[_ -]?arrays|counted_by|sized_by",
            r"trivial[_ -]?auto[_ -]?var[_ -]?init|zero[_ -]?init[_ -]?padding",
        ),
    ),
    FamilyRule(
        "control_flow_integrity",
        ("cet", "ibt", "shstk", "bti", "pac", "return-address-signing", "cmse"),
        ("gcc/", "libgcc/"),
        (
            r"\bcet\b|endbr|\bibt\b|shstk|branch[_ -]?protection|gnu_property",
            r"\bbti\b|pac[_ -]?ret|return[_ -]?address[_ -]?(?:sign|auth)|\bcmse\b",
        ),
    ),
    FamilyRule(
        "elf_address_space",
        ("pie", "pic", "relro", "nx", "noexecstack", "nodlopen", "as-needed"),
        ("gcc/", "libgcc/"),
        (
            r"(?:^|[^a-z])(?:pie|fpie|pic|fpic|relro|noexecstack|gnu-stack)(?:$|[^a-z])",
            r"pt_gnu_stack|df_1_noopen|as-needed|nodlopen|position[_ -]?independent",
        ),
    ),
    FamilyRule(
        "sanitizers_hardening",
        ("asan", "ubsan", "tsan", "lsan", "auto-var-init", "glibcxx-assertions", "fhardened"),
        ("gcc/", "libsanitizer/", "libstdc++-v3/"),
        (
            r"(?:^|[^a-z])(?:asan|ubsan|tsan|lsan)(?:$|[^a-z])",
            r"(?:^|[^a-z])(?:fhardened|hardened|auto[_ -]?var[_ -]?init|"
            r"glibcxx_assertions)(?:$|[^a-z])",
        ),
    ),
)


@dataclass(frozen=True)
class SelectedFile:
    relative_path: str
    text: str
    raw: bytes
    sha256: str
    families: tuple[str, ...]
    architecture: str


def _read_identity(source_root: Path) -> dict[str, str | bool | None]:
    identity: dict[str, str | bool | None] = {}
    for name in ("LAST_UPDATED", "BASE-VER", "DATESTAMP"):
        candidates = (source_root / name, source_root / "gcc" / name)
        candidate = next((path for path in candidates if path.is_file()), None)
        identity[name] = (
            candidate.read_text(encoding="utf-8", errors="replace").strip()
            if candidate is not None
            else None
        )
    try:
        head = subprocess.run(
            ["git", "-C", str(source_root), "rev-parse", "HEAD"],
            capture_output=True,
            check=True,
            text=True,
        ).stdout.strip()
        status = subprocess.run(
            ["git", "-C", str(source_root), "status", "--porcelain"],
            capture_output=True,
            check=True,
            text=True,
        ).stdout
    except (OSError, subprocess.CalledProcessError):
        identity["git_head"] = None
        identity["git_clean"] = None
    else:
        identity["git_head"] = head
        identity["git_clean"] = not bool(status.strip())
    return identity


def _architecture(relative_path: str) -> str:
    if relative_path.startswith(("gcc/config/i386/", "gcc/config/x86/")):
        return "x86_i386"
    if relative_path.startswith(("gcc/config/aarch64/", "gcc/config/arm/")):
        return "aarch64_arm"
    if relative_path.startswith("gcc/config/riscv/"):
        return "riscv"
    if relative_path.startswith("gcc/config/"):
        return "other_backends"
    return "generic_core"


def _priority(relative_path: str) -> tuple[int, str]:
    if relative_path in _PROMPT_PRIORITY_PATHS:
        return (0, relative_path)
    return (1, relative_path)


def _select(source_root: Path) -> list[SelectedFile]:
    candidates: list[SelectedFile] = []
    for path in sorted(source_root.rglob("*")):
        if (
            path.is_symlink()
            or not path.is_file()
            or path.suffix not in _SOURCE_SUFFIXES
            or "testsuite" in path.parts
        ):
            continue
        try:
            path.resolve(strict=True).relative_to(source_root)
        except (OSError, ValueError):
            continue
        raw = path.read_bytes()
        relative_path = path.relative_to(source_root).as_posix()
        allowed_chars = (
            MAX_REQUIRED_FILE_CHARS
            if relative_path in _REQUIRED_FILE_FAMILIES
            else MAX_FILE_CHARS
        )
        if len(raw) > allowed_chars:
            continue
        text = raw.decode("utf-8", errors="replace")
        matched = {rule.name for rule in FAMILY_RULES if rule.matches(relative_path, text)}
        matched.update(_REQUIRED_FILE_FAMILIES.get(relative_path, ()))
        families = tuple(rule.name for rule in FAMILY_RULES if rule.name in matched)
        if families:
            candidates.append(
                SelectedFile(
                    relative_path,
                    text,
                    raw,
                    hashlib.sha256(raw).hexdigest(),
                    families,
                    _architecture(relative_path),
                )
            )

    planned: dict[str, list[SelectedFile]] = {}
    for rule in FAMILY_RULES:
        buckets: dict[str, list[SelectedFile]] = {
            architecture: [] for architecture in _ARCHITECTURES
        }
        for candidate in candidates:
            if rule.name in candidate.families:
                buckets[candidate.architecture].append(candidate)
        for bucket in buckets.values():
            bucket.sort(key=lambda item: _priority(item.relative_path))
        chosen: list[SelectedFile] = []
        # Prompt-named files are selected before breadth; remaining slots rotate
        # across architecture buckets rather than consuming lexical order.
        priority_items = (
            item
            for bucket in buckets.values()
            for item in bucket
            if _priority(item.relative_path)[0] == 0
        )
        for candidate in sorted(priority_items, key=lambda item: _priority(item.relative_path)):
            if candidate not in chosen and len(chosen) < MAX_FILES_PER_FAMILY:
                chosen.append(candidate)
        positions = {architecture: 0 for architecture in _ARCHITECTURES}
        while len(chosen) < MAX_FILES_PER_FAMILY:
            added = False
            for architecture in _ARCHITECTURES:
                bucket = buckets[architecture]
                while (
                    positions[architecture] < len(bucket)
                    and bucket[positions[architecture]] in chosen
                ):
                    positions[architecture] += 1
                if positions[architecture] < len(bucket):
                    chosen.append(bucket[positions[architecture]])
                    positions[architecture] += 1
                    added = True
                    if len(chosen) == MAX_FILES_PER_FAMILY:
                        break
            if not added:
                break
        planned[rule.name] = chosen

    selected: list[SelectedFile] = []
    used_chars = 0
    # A second round-robin prevents one family from consuming the aggregate cap.
    for index in range(MAX_FILES_PER_FAMILY):
        for rule in FAMILY_RULES:
            choices = planned[rule.name]
            if index >= len(choices):
                continue
            candidate = choices[index]
            if candidate in selected or len(selected) == MAX_TOTAL_FILES:
                continue
            if used_chars + len(candidate.text) > MAX_TOTAL_CHARS:
                continue
            selected.append(candidate)
            used_chars += len(candidate.text)
    family_counts = {
        rule.name: sum(rule.name in item.families for item in selected) for rule in FAMILY_RULES
    }
    missing = [name for name, count in family_counts.items() if count == 0]
    if missing:
        raise ValueError(
            "GCC corpus selection has no files for expected family: " + ", ".join(missing)
        )
    return selected


def _manifest(source_root: Path, selected: Sequence[SelectedFile]) -> dict[str, object]:
    files = [
        {
            "path": item.relative_path,
            "sha256": item.sha256,
            "chars": len(item.text),
            "bytes": len(item.raw),
            "reasons": list(item.families),
        }
        for item in selected
    ]
    aggregate = hashlib.sha256(
        "".join(f"{item.relative_path}\0{item.sha256}\0" for item in selected).encode("utf-8")
    ).hexdigest()
    chars = sum(len(item.text) for item in selected)
    return {
        "schema_version": 1,
        "source_identity": _read_identity(source_root),
        "rag_curated_paths": {
            "purpose": "full-tree RAG inputs; distinct from segmented corpus selection",
            "paths": list(RAG_CURATED_PATHS),
            "count": len(RAG_CURATED_PATHS),
        },
        "selection_rules": [
            {
                "family": rule.name,
                "mechanisms": list(rule.mechanisms),
                "path_prefixes": list(rule.path_prefixes),
                "content_patterns": list(rule.content_patterns),
            }
            for rule in FAMILY_RULES
        ],
        "limits": {
            "max_files_per_family": MAX_FILES_PER_FAMILY,
            "max_total_files": MAX_TOTAL_FILES,
            "max_total_chars": MAX_TOTAL_CHARS,
            "max_file_chars": MAX_FILE_CHARS,
            "max_required_file_chars": MAX_REQUIRED_FILE_CHARS,
        },
        "files": files,
        "aggregate_sha256": aggregate,
        "file_count": len(selected),
        "total_chars": chars,
        "total_bytes": sum(len(item.raw) for item in selected),
        "estimated_tokens": (chars + TOKEN_CHARS - 1) // TOKEN_CHARS,
        "family_counts": {
            rule.name: sum(rule.name in item.families for item in selected) for rule in FAMILY_RULES
        },
        "architecture_counts": {
            architecture: sum(item.architecture == architecture for item in selected)
            for architecture in _ARCHITECTURES
        },
    }


def build_corpus(source_root: Path, output_dir: Path) -> dict[str, object]:
    """Copy selected sources preserving paths and return their manifest."""

    source_root = source_root.resolve()
    output_dir = output_dir.expanduser().absolute()
    if not source_root.is_dir():
        raise ValueError(f"GCC source checkout is not a directory: {source_root}")
    if (
        output_dir == source_root
        or source_root in output_dir.parents
        or output_dir in source_root.parents
    ):
        raise ValueError(
            "output directory must not equal, contain, or be contained by the source checkout"
        )
    selected = _select(source_root)
    manifest = _manifest(source_root, selected)
    if output_dir.exists() or output_dir.is_symlink():
        raise ValueError(f"output directory already exists: {output_dir}")
    output_dir.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(
        tempfile.mkdtemp(prefix=f".{output_dir.name}.building-", dir=output_dir.parent)
    )
    try:
        for item in selected:
            destination = staging / "sources" / item.relative_path
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(item.raw)
        (staging / "manifest.json").write_text(
            json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
        )
        os.rename(staging, output_dir)
    except BaseException:
        if staging.exists():
            shutil.rmtree(staging)
        raise
    return manifest


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("gcc_source", type=Path, help="path to a GCC source checkout")
    parser.add_argument("output_dir", type=Path, help="clean output corpus directory")
    args = parser.parse_args(argv)
    try:
        manifest = build_corpus(args.gcc_source, args.output_dir)
    except ValueError as error:
        parser.error(str(error))
    print(
        f"built GCC security corpus: files={manifest['file_count']} "
        f"chars={manifest['total_chars']} estimated_tokens={manifest['estimated_tokens']}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
