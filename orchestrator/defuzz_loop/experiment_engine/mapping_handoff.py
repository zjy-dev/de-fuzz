"""Bind source mapping evidence to the invariant set and the audit workspace."""

from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

from ..audit_schema import AuditVariant, normalize_isa, normalize_mechanism
from ..prompt_bundle import WorkerPromptBundle
from .source_mapping_stage import (
    _mapping_isas,
    invariant_identity,
    load_source_mappings,
    sha256,
    validate_anchors,
)


def bind_mapping_handoff(
    bundles: Sequence[WorkerPromptBundle], *, parameters: Mapping[str, Any],
    source_roots: Sequence[Path], workspace: Path, invariants: Sequence[Mapping[str, Any]],
) -> tuple[list[WorkerPromptBundle], dict[str, Any]]:
    raw = parameters.get("source_mappings")
    if not raw:
        return list(bundles), {"enabled": False}
    if len(source_roots) != 1:
        raise ValueError("mapped audit requires exactly one compiler source root")
    path = Path(str(raw))
    expected_summary = parameters.get("source_mapping_summary_sha256")
    summary_sha = sha256(path.with_name("source-mapping-summary.json"))
    if expected_summary and summary_sha != expected_summary:
        raise ValueError("source mapping summary SHA-256 mismatch")
    mappings, summary = load_source_mappings(
        path, source_root=source_roots[0],
        expected_sha256=parameters.get("source_mappings_sha256"),
    )
    compiler = parameters.get("compiler")
    isas = parameters.get("isas", parameters.get("isa", []))
    isas = [normalize_isa(i) for i in ([isas] if isinstance(isas, str) else isas)]
    selected = {str(v["invariant_id"]): v for v in invariants}
    for key, value in selected.items():
        if summary["invariant_sha256"].get(key) != invariant_identity(value):
            raise ValueError(f"mapping refers to different invariant semantics: {key}")
    by_key = {(m.phi_id, m.isa): m for m in mappings if not compiler or m.compiler == compiler}
    for key, value in selected.items():
        for isa in _mapping_isas(value.get("target"), isas):
            if (key, isa) not in by_key:
                raise ValueError(f"missing source mapping for {key}/{isa}")
    bound: list[WorkerPromptBundle] = []
    visibility: dict[str, int] = {}
    for bundle in bundles:
        if bundle.variant is AuditVariant.BARE_AGENT:
            bound.append(bundle)
            visibility[bundle.family.key] = 0
            continue
        bundle_inv_id = bundle.metadata.get("invariant_id")
        bundle_mechs = {
            normalize_mechanism(m)
            for m in (bundle.metadata.get("mechanisms") or bundle.family.mechanisms)
        }
        evidence = [
            validate_anchors(m, workspace).model_dump(mode="json")
            for m in mappings
            if m.phi_id in selected and (not compiler or m.compiler == compiler)
            and (not isas or m.isa in _mapping_isas(selected[m.phi_id].get("target"), isas))
            and (bundle_inv_id is None or m.phi_id == bundle_inv_id)
            and (not bundle_mechs or normalize_mechanism(str(selected[m.phi_id].get("mechanism", ""))) in bundle_mechs or len(bundles) == 1)
        ]
        prompt = (
            bundle.prompt + "\n\n# Validated invariant source mappings\n\n"
            "Paths below are relative to your workspace. Inspect the anchors, trace "
            "their gating conditions and use bounded CallGraph queries to check the "
            "callers/callees. Query list_targets first and report missing version/ISA "
            "coverage explicitly.\n"
            + json.dumps(evidence, ensure_ascii=False, sort_keys=True)
        )
        bound.append(bundle.model_copy(update={
            "prompt": prompt, "sha256": hashlib.sha256(prompt.encode()).hexdigest(),
        }))
        visibility[bundle.family.key] = len(evidence)
    return bound, {
        "enabled": True, "sha256": sha256(path),
        "source_tree_sha256": summary["source_tree_sha256"],
        "visible_mappings_by_family": visibility,
    }
