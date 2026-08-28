from __future__ import annotations

import hashlib
import json
from pathlib import Path
from typing import Any

import pytest
from pydantic import ValidationError

from defuzz_loop.checker_bundle import (
    CHECKER_BUNDLE_MANIFEST_FILENAME,
    CheckerBundleManifest,
    compute_bundle_id,
    load_checker_bundle,
    validate_checker_bundle,
)


def _sha256(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def _artifact(path: str, content: bytes, *, kind: str) -> dict[str, Any]:
    return {
        "path": path,
        "sha256": _sha256(content),
        "size_bytes": len(content),
        "kind": kind,
    }


def _write_manifest(root: Path, payload: dict[str, Any]) -> Path:
    payload["bundle_id"] = compute_bundle_id(payload)
    manifest_path = root / CHECKER_BUNDLE_MANIFEST_FILENAME
    manifest_path.write_text(
        json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    return manifest_path


def _ready_bundle(root: Path) -> tuple[dict[str, Any], Path]:
    root.mkdir()
    contents = {
        "artifacts/checkers.patch": b"diff --git a/checker.go b/checker.go\n",
        "artifacts/catalog.json": b'{"checkers":["INV-ONE"]}\n',
        "bin/dispatcher": b"#!/bin/sh\nexit 0\n",
        "inputs/scoped-accepted-invariants.jsonl": (
            b'{"invariant_id":"INV-ONE","mechanism":"stack-protector"}\n'
        ),
        "inputs/checker-input-scope.json": json.dumps(
            {
                "schema_version": 1,
                "kind": "defuzz-checker-input-scope",
                "source_artifact": {"sha256": "7" * 64},
                "requested": {
                    "isas": ["x86_64"],
                    "mechanisms": ["stack-protector"],
                },
                "scope_requested": True,
                "selected_invariant_ids": ["INV-ONE"],
            },
            sort_keys=True,
        ).encode("utf-8"),
    }
    for relative, content in contents.items():
        destination = root / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(content)

    payload: dict[str, Any] = {
        "schema_version": 1,
        "kind": "defuzz-checker-bundle",
        "status": "ready",
        "bundle_id": "0" * 64,
        "source_root": "/frozen/source/checkout",
        "source_root_sha256": "1" * 64,
        "source_tree_sha256": "2" * 64,
        "final_tree_sha256": "6" * 64,
        "source_invariants_sha256": "7" * 64,
        "requested_mechanisms": ["stack-protector"],
        "requested_isas": ["x86_64"],
        "coverage_complete": True,
        "budget_exhausted": False,
        "included_invariant_ids": ["INV-ONE"],
        "failed_invariant_ids": [],
        "invariants": [
            {
                "invariant_id": "INV-ONE",
                "final_status": "passed",
                "parent_tree_sha256": "3" * 64,
                "result_tree_sha256": "4" * 64,
                "files": ["core/internal/oracle/checker_one.go"],
                "producer_metadata": {"说明": "preserved"},
            }
        ],
        "artifacts": {
            "cumulative_patch": _artifact(
                "artifacts/checkers.patch",
                contents["artifacts/checkers.patch"],
                kind="cumulative-patch",
            ),
            "catalog": _artifact(
                "artifacts/catalog.json",
                contents["artifacts/catalog.json"],
                kind="checker-catalog",
            ),
            "dispatcher": _artifact(
                "bin/dispatcher",
                contents["bin/dispatcher"],
                kind="checker-dispatcher",
            ),
            "scoped_invariants": _artifact(
                "inputs/scoped-accepted-invariants.jsonl",
                contents["inputs/scoped-accepted-invariants.jsonl"],
                kind="scoped-accepted-invariants",
            ),
            "input_scope": _artifact(
                "inputs/checker-input-scope.json",
                contents["inputs/checker-input-scope.json"],
                kind="checker-input-scope",
            ),
        },
        "validation": {
            "status": "passed",
            "commands": [{"argv": ["go", "test", "./..."], "status": "passed"}],
            "build": {
                "argv": ["go", "build", "./cmd/checker-dispatcher"],
                "cwd": "core",
                "exit_code": 0,
                "timed_out": False,
                "stdout": "",
                "stderr": "",
                "status": "passed",
            },
            "producer_note": "kept for integration",
        },
    }
    return payload, _write_manifest(root, payload)


def test_load_valid_ready_bundle_from_directory_and_manifest_path(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")

    from_directory = load_checker_bundle(manifest_path.parent)
    from_file = load_checker_bundle(manifest_path)

    assert from_directory == from_file
    assert from_directory.root == manifest_path.parent.resolve()
    assert from_directory.manifest_path == manifest_path.resolve()
    assert (
        from_directory.cumulative_patch
        == (manifest_path.parent / "artifacts/checkers.patch").resolve()
    )
    assert from_directory.catalog == (manifest_path.parent / "artifacts/catalog.json").resolve()
    assert from_directory.dispatcher == (manifest_path.parent / "bin/dispatcher").resolve()
    assert from_directory.scoped_invariants == (
        manifest_path.parent / "inputs/scoped-accepted-invariants.jsonl"
    ).resolve()
    assert from_directory.input_scope == (
        manifest_path.parent / "inputs/checker-input-scope.json"
    ).resolve()
    assert from_directory.manifest.source_invariants_sha256 == "7" * 64
    assert from_directory.manifest.requested_mechanisms == ["stack-protector"]
    assert from_directory.manifest.requested_isas == ["x86_64"]
    assert from_directory.manifest.invariants[0].model_extra == {
        "producer_metadata": {"说明": "preserved"}
    }

    canonical = json.dumps(
        {key: value for key, value in payload.items() if key != "bundle_id"},
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    )
    assert compute_bundle_id(from_directory.manifest) == _sha256(canonical.encode("utf-8"))


@pytest.mark.parametrize("role", ["scoped_invariants", "input_scope"])
def test_ready_bundle_requires_input_provenance_artifacts(
    tmp_path: Path, role: str
) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["artifacts"][role] = None
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValidationError, match="input-provenance artifacts"):
        load_checker_bundle(manifest_path)


@pytest.mark.parametrize(
    "field",
    ["source_invariants_sha256", "requested_mechanisms", "requested_isas"],
)
def test_manifest_requires_input_provenance_fields(tmp_path: Path, field: str) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    del payload[field]
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValidationError, match=field):
        load_checker_bundle(manifest_path)


def _rewrite_scope_artifact(
    payload: dict[str, Any], manifest_path: Path, mutate: Any
) -> None:
    reference = payload["artifacts"]["input_scope"]
    scope_path = manifest_path.parent / reference["path"]
    scope = json.loads(scope_path.read_text(encoding="utf-8"))
    mutate(scope)
    content = (json.dumps(scope, sort_keys=True) + "\n").encode("utf-8")
    scope_path.write_bytes(content)
    payload["artifacts"]["input_scope"] = _artifact(
        reference["path"], content, kind="checker-input-scope"
    )
    _write_manifest(manifest_path.parent, payload)


def test_load_rejects_input_scope_source_hash_mismatch(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    _rewrite_scope_artifact(
        payload,
        manifest_path,
        lambda scope: scope["source_artifact"].update({"sha256": "8" * 64}),
    )

    with pytest.raises(ValueError, match="source_invariants_sha256"):
        load_checker_bundle(manifest_path)


@pytest.mark.parametrize(
    ("manifest_field", "scope_field", "value"),
    [
        ("requested_mechanisms", "mechanisms", ["ibt"]),
        ("requested_isas", "isas", ["aarch64"]),
    ],
)
def test_load_rejects_requested_scope_mismatch(
    tmp_path: Path, manifest_field: str, scope_field: str, value: list[str]
) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload[manifest_field] = value
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValueError, match=manifest_field):
        load_checker_bundle(manifest_path)

    payload, manifest_path = _ready_bundle(tmp_path / "other-bundle")
    _rewrite_scope_artifact(
        payload,
        manifest_path,
        lambda scope: scope["requested"].update({scope_field: value}),
    )
    with pytest.raises(ValueError, match=manifest_field):
        load_checker_bundle(manifest_path)


def test_load_rejects_scoped_invariant_ids_that_disagree_with_scope(
    tmp_path: Path,
) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    _rewrite_scope_artifact(
        payload,
        manifest_path,
        lambda scope: scope.update({"selected_invariant_ids": ["INV-TWO"]}),
    )

    with pytest.raises(ValueError, match="scoped_invariants IDs"):
        load_checker_bundle(manifest_path)


def test_load_rejects_tampered_scoped_invariants(tmp_path: Path) -> None:
    _, manifest_path = _ready_bundle(tmp_path / "bundle")
    (manifest_path.parent / "inputs/scoped-accepted-invariants.jsonl").write_text(
        '{"invariant_id":"INV-TWO"}\n', encoding="utf-8"
    )

    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        load_checker_bundle(manifest_path)


def test_load_rejects_input_provenance_path_escape(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    outside = tmp_path / "outside-scope.json"
    outside.write_text("{}\n", encoding="utf-8")
    payload["artifacts"]["input_scope"] = _artifact(
        "../outside-scope.json", outside.read_bytes(), kind="checker-input-scope"
    )
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValidationError, match="must not contain '..'"):
        load_checker_bundle(manifest_path)


def test_incomplete_bundle_requires_explicit_opt_in(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["status"] = "incomplete"
    payload["coverage_complete"] = False
    payload["invariants"].append(
        {
            "invariant_id": "INV-TWO",
            "final_status": "unprocessed",
            "parent_tree_sha256": "5" * 64,
            "result_tree_sha256": "5" * 64,
            "files": [],
        }
    )
    payload["validation"] = {"status": "not-run", "commands": [], "build": None}
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValueError, match="expected 'ready'"):
        load_checker_bundle(manifest_path)

    loaded = load_checker_bundle(manifest_path.parent, require_ready=False)
    assert loaded.manifest.status == "incomplete"
    assert [item.invariant_id for item in loaded.manifest.invariants] == [
        "INV-ONE",
        "INV-TWO",
    ]


def test_incomplete_bundle_allows_artifacts_that_were_not_produced(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["status"] = "incomplete"
    payload["artifacts"] = {
        "cumulative_patch": payload["artifacts"]["cumulative_patch"],
        "catalog": None,
        "dispatcher": None,
    }
    payload["validation"] = {"status": "failed", "commands": [], "build": None}
    _write_manifest(manifest_path.parent, payload)

    loaded = load_checker_bundle(manifest_path, require_ready=False)

    assert loaded.cumulative_patch is not None
    assert loaded.catalog is None
    assert loaded.dispatcher is None


def test_ready_bundle_can_report_failed_invariants_with_incomplete_coverage(
    tmp_path: Path,
) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["coverage_complete"] = False
    payload["failed_invariant_ids"] = ["INV-TWO"]
    payload["invariants"].append(
        {
            "invariant_id": "INV-TWO",
            "final_status": "failed",
            "parent_tree_sha256": "5" * 64,
            "result_tree_sha256": "5" * 64,
            "files": [],
        }
    )
    _write_manifest(manifest_path.parent, payload)

    loaded = load_checker_bundle(manifest_path)

    assert loaded.manifest.status == "ready"
    assert loaded.manifest.coverage_complete is False
    assert loaded.manifest.failed_invariant_ids == ["INV-TWO"]


@pytest.mark.parametrize("load_by_directory", [False, True])
def test_load_rejects_symlink_manifest(tmp_path: Path, load_by_directory: bool) -> None:
    _, target_manifest = _ready_bundle(tmp_path / "target")
    supplied_root = tmp_path / "supplied"
    supplied_root.mkdir()
    manifest_link = supplied_root / CHECKER_BUNDLE_MANIFEST_FILENAME
    manifest_link.symlink_to(target_manifest)

    supplied_path = supplied_root if load_by_directory else manifest_link
    with pytest.raises(ValueError, match="manifest must not be a symlink"):
        load_checker_bundle(supplied_path)


def test_budget_exhaustion_forces_incomplete_status(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["status"] = "incomplete"
    payload["budget_exhausted"] = True
    _write_manifest(manifest_path.parent, payload)

    loaded = load_checker_bundle(manifest_path, require_ready=False)

    assert loaded.manifest.budget_exhausted is True
    with pytest.raises(ValueError, match="expected 'ready'"):
        load_checker_bundle(manifest_path)


def test_infrastructure_error_forces_incomplete_status(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["status"] = "incomplete"
    payload["invariants"][0]["infrastructure_error"] = True
    _write_manifest(manifest_path.parent, payload)

    loaded = load_checker_bundle(manifest_path, require_ready=False)

    assert loaded.manifest.invariants[0].infrastructure_error is True
    with pytest.raises(ValueError, match="expected 'ready'"):
        load_checker_bundle(manifest_path)


def test_load_rejects_artifact_hash_and_size_tampering(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    (manifest_path.parent / "artifacts/catalog.json").write_bytes(b"tampered\n")

    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        load_checker_bundle(manifest_path)

    original = b'{"checkers":["INV-ONE"]}\n'
    (manifest_path.parent / "artifacts/catalog.json").write_bytes(original)
    payload["artifacts"]["catalog"]["size_bytes"] = len(original) + 1
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValueError, match="size mismatch"):
        load_checker_bundle(manifest_path)


def test_load_rejects_bundle_id_tampering(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["bundle_id"] = "f" * 64
    manifest_path.write_text(json.dumps(payload), encoding="utf-8")

    with pytest.raises(ValueError, match="bundle_id mismatch"):
        load_checker_bundle(manifest_path)


@pytest.mark.parametrize(
    "unsafe_path",
    [
        "../outside.json",
        "/absolute/catalog.json",
        "C:/windows/catalog.json",
        "artifacts//catalog.json",
        "artifacts\\catalog.json",
        "artifacts/./catalog.json",
        "artifacts/catalog.json\x00ignored",
    ],
)
def test_load_rejects_unsafe_artifact_paths(tmp_path: Path, unsafe_path: str) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["artifacts"]["catalog"]["path"] = unsafe_path
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValidationError):
        load_checker_bundle(manifest_path)


def test_load_rejects_artifact_symlink_that_escapes_manifest_root(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    outside = tmp_path / "outside-catalog.json"
    outside.write_bytes(b"outside\n")
    link = manifest_path.parent / "artifacts/escape.json"
    link.symlink_to(outside)
    payload["artifacts"]["catalog"] = _artifact(
        "artifacts/escape.json", outside.read_bytes(), kind="checker-catalog"
    )
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValueError, match="escapes manifest root"):
        load_checker_bundle(manifest_path)


def test_manifest_rejects_duplicate_invariant_ids(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["invariants"].append(dict(payload["invariants"][0]))
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValidationError, match="invariant_id values must be unique"):
        load_checker_bundle(manifest_path)


def test_manifest_rejects_duplicate_declared_artifact_paths(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["artifacts"]["catalog"] = dict(payload["artifacts"]["cumulative_patch"])
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValidationError, match="artifact paths must be unique"):
        load_checker_bundle(manifest_path)


def test_load_rejects_distinct_paths_that_resolve_to_same_file(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    alias = manifest_path.parent / "artifacts/catalog-alias.json"
    alias.symlink_to(manifest_path.parent / "artifacts/catalog.json")
    payload["artifacts"]["dispatcher"] = dict(payload["artifacts"]["catalog"])
    payload["artifacts"]["dispatcher"]["path"] = "artifacts/catalog-alias.json"
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValueError, match="resolve to the same file"):
        load_checker_bundle(manifest_path)


@pytest.mark.parametrize(
    ("field", "value", "message"),
    [
        ("coverage_complete", False, "coverage_complete"),
        ("included_invariant_ids", [], "included_invariant_ids"),
        ("failed_invariant_ids", ["INV-ONE"], "disjoint"),
    ],
)
def test_manifest_rejects_inconsistent_coverage_lists(
    tmp_path: Path, field: str, value: Any, message: str
) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload[field] = value
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValidationError, match=message):
        load_checker_bundle(manifest_path)


def test_manifest_is_strict_at_top_level_but_allows_invariant_metadata(
    tmp_path: Path,
) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    payload["unknown_top_level"] = True
    _write_manifest(manifest_path.parent, payload)

    with pytest.raises(ValidationError, match="Extra inputs are not permitted"):
        load_checker_bundle(manifest_path)


def test_validate_checker_bundle_uses_manifest_directory_as_root(tmp_path: Path) -> None:
    payload, manifest_path = _ready_bundle(tmp_path / "bundle")
    manifest = CheckerBundleManifest.model_validate(payload)

    loaded = validate_checker_bundle(manifest, manifest_path)

    assert loaded.root == manifest_path.parent.resolve()
