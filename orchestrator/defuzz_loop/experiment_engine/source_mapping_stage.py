"""Measured Pi source mapping and validated downstream handoff."""

from __future__ import annotations

import hashlib
import json
import tempfile
import time
import uuid
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any, cast

from ..agents.contracts.schemas import Invariant, SourceMappingResult
from ..agents.runner import repair_prompt, schema_repairable
from ..audit_schema import normalize_isa
from .agent_backend import AgentBackend, AgentRequest
from .checker_authoring import (
    _GENERIC_TARGETS,
    _isa_compatible,
    _project_input_scope,
    _target_isas,
    _target_values,
    load_accepted_invariants,
)
from .models import ArtifactRef, ExperimentPlan, StageResult
from .workspace import WorkspaceBuilder

SUPPORTED_ISAS = ("x86_64", "aarch64", "riscv64", "loongarch64")


def sha256(path: Path) -> str:
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def invariant_identity(value: Mapping[str, Any]) -> str:
    semantic = {key: value.get(key) for key in
                ("invariant_id", "statement", "mechanism", "target", "compiler")}
    return hashlib.sha256(json.dumps(semantic, sort_keys=True).encode()).hexdigest()


def _mapping_isas(target: Any, requested: Sequence[str]) -> list[str]:
    known = _target_isas(target)
    raw = _target_values(target)
    if any(
        normalize_isa(value) not in _GENERIC_TARGETS and not _target_isas(value)
        for value in raw
    ):
        raise ValueError(f"unsupported mapping ISA: {target!r}")
    for value in (*known, *requested):
        if not any(_isa_compatible(value, isa) for isa in SUPPORTED_ISAS):
            raise ValueError(f"unsupported mapping ISA: {value!r}")
    result = [
        isa for isa in SUPPORTED_ISAS
        if (not known or any(_isa_compatible(v, isa) for v in known))
        and (not requested or any(_isa_compatible(v, isa) for v in requested))
    ]
    if not result:
        raise ValueError(f"no supported mapping ISA for {target!r}")
    return result


def validate_anchors(mapping: SourceMappingResult, root: Path) -> SourceMappingResult:
    """Reject escaped/missing citations and normalize paths to the source root."""
    result = mapping.model_copy(deep=True)

    def relative(value: str, start: int | None = None, end: int | None = None) -> str:
        path = (root / value).resolve(strict=True)
        rel = path.relative_to(root.resolve())
        if not path.is_file():
            raise ValueError(f"mapping anchor is not a file: {value}")
        if start is not None:
            count = len(path.read_bytes().splitlines())
            if not 1 <= start <= (end or start) <= count:
                raise ValueError(f"mapping anchor outside file line range: {value}:{start}-{end}")
        return rel.as_posix()

    anchor = result.level_1_pass
    anchor.file = relative(anchor.file, anchor.line_start, anchor.line_end)
    for item in cast(tuple[Any, ...], (
        *result.level_2_functions, *result.level_3_critical_structs,
        *result.level_3_critical_conditions,
    )):
        item.file = relative(item.file, item.line)
    result.evidence_paths = [relative(p) for p in result.evidence_paths]
    return result


def load_source_mappings(
    path: Path, *, source_root: Path | None = None, expected_sha256: str | None = None,
) -> tuple[list[SourceMappingResult], dict[str, Any]]:
    """Verify a completed mapping handoff, including its content-addressed inputs."""
    path = path.expanduser().resolve(strict=True)
    summary_path = path.with_name("source-mapping-summary.json")
    summary = json.loads(summary_path.read_text())
    if summary.get("status") != "completed" or summary.get("failed") != 0:
        raise ValueError("source mapping handoff is not completed")
    actual = sha256(path)
    if actual != summary.get("source_mappings_sha256") or (
        expected_sha256 is not None and actual != expected_sha256
    ):
        raise ValueError("source mappings SHA-256 mismatch")
    snapshot = path.with_name("source-snapshot.json")
    if sha256(snapshot) != summary.get("source_snapshot_sha256"):
        raise ValueError("mapping source snapshot SHA-256 mismatch")
    mappings = [SourceMappingResult.model_validate_json(line)
                for line in path.read_text().splitlines() if line.strip()]
    keys = {(m.phi_id, m.isa) for m in mappings}
    if not mappings or len(keys) != len(mappings) or len(mappings) != summary.get("mapped"):
        raise ValueError("source mapping handoff has missing or duplicate entries")
    if any(m.isa is None for m in mappings):
        raise ValueError("source mapping handoff is missing ISA identities")
    if source_root is not None:
        source_root = source_root.resolve(strict=True)
        # Recompute the same filtered tree identity without making another copy.
        builder = WorkspaceBuilder(source_root)
        saved = json.loads(snapshot.read_text())
        actual_files = builder.snapshot_files()
        expected_files = {item["path"]: item["sha256"] for item in saved["files"]}
        if actual_files != expected_files:
            raise ValueError("compiler source no longer matches the mapping snapshot")
        mappings = [validate_anchors(m, source_root) for m in mappings]
    return mappings, summary


async def run(
    plan: ExperimentPlan | Mapping[str, Any], repetition: int, output_dir: Path,
    backend: AgentBackend | None = None,
) -> StageResult:
    try:
        if not isinstance(plan, ExperimentPlan):
            plan = ExperimentPlan.model_validate(plan)
        params = plan.parameters
        if backend is None or getattr(backend, "provider", None) != "pi":
            raise ValueError("source-mapping requires the measured Pi backend")
        accepted = Path(str(params["accepted_invariants"])).expanduser().resolve(strict=True)
        if params.get("accepted_invariants_sha256") not in (None, sha256(accepted)):
            raise ValueError("accepted invariants SHA-256 mismatch")
        configured_root = params.get("source_root") or plan.source_root
        if not configured_root:
            raise ValueError("source-mapping requires an explicit compiler source root")
        root = Path(configured_root).expanduser().resolve(strict=True)
        if not root.is_dir():
            raise ValueError("source-mapping requires a compiler source directory")
        records, scope = _project_input_scope(
            load_accepted_invariants(accepted), parameters=params, source_path=accepted,
        )
        if not records:
            raise ValueError("no accepted invariants in the requested mapping scope")
        denied = [root, accepted]
        if params.get("reference_root"):
            denied.append(Path(params["reference_root"]).expanduser().resolve(strict=False))
        denied.extend(Path(path) for path in params.get("deny_read_paths", []))
        output_dir.mkdir(parents=True, exist_ok=True)
        schema = output_dir / "mapping-schema.json"
        schema.write_text(json.dumps(SourceMappingResult.model_json_schema()))
        mappings: list[SourceMappingResult] = []
        failures: list[str] = []
        queries = 0
        data_queries = 0
        attempts = 0
        binding = None
        backend_config = getattr(backend, "config", {})
        graph = backend_config.get("mcp", {}).get("defuzz_callgraph", {})
        if graph.get("database_path"):
            from .callgraph_identity import bind_callgraph

            mapping_isas = sorted({
                isa for record in records
                for isa in _mapping_isas(record.value.get("target"), scope["requested"]["isas"])
            })
            binding = bind_callgraph(
                Path(graph["database_path"]), root,
                compiler=str(params.get("compiler", "gcc")), isas=mapping_isas,
            )
        with tempfile.TemporaryDirectory(prefix="defuzz-mapping-") as temporary:
            workspace = WorkspaceBuilder(root).materialize(Path(temporary) / "source")
            snapshot = output_dir / "source-snapshot.json"
            snapshot.write_text(workspace.model_dump_json(indent=2) + "\n")
            for record in records:
                compiler = str(record.value.get("compiler") or params.get("compiler", "gcc"))
                compiler = compiler.strip().lower()
                compiler = "llvm" if compiler == "clang" else compiler
                for isa in _mapping_isas(record.value.get("target"), scope["requested"]["isas"]):
                    invariant = Invariant(
                        phi_id=record.invariant_id, phi_text=record.value["statement"],
                        mechanism=str(record.value.get("mechanism") or ""),
                        compiler=cast(Any, compiler), isa=cast(Any, isa),
                        src_root=str(workspace.root),
                    )
                    prompt = invariant.as_prompt_payload()
                    last_error = ""
                    parent_attempt_id = None
                    deadline = time.monotonic() + min(600, plan.budget.timeout_seconds)
                    for _attempt in range(3):
                        remaining = deadline - time.monotonic()
                        if remaining <= 0:
                            failures.append(f"{invariant.phi_id}/{isa}: mapping deadline exhausted")
                            break
                        attempts += 1
                        attempt_id = uuid.uuid4().hex
                        request = AgentRequest(
                            prompt=prompt, cwd=workspace.root,
                            output_dir=output_dir / "agents" / f"mapping-{attempts:05d}",
                            schema_path=schema, writable=False,
                            timeout_seconds=remaining,
                            deny_read_paths=list(dict.fromkeys(denied)),
                            require_host_read_isolation=bool(
                                params.get("require_host_read_isolation", True)
                            ),
                            metadata={
                                "run_id": plan.run_id, "experiment": plan.experiment,
                                "variant": params.get("campaign_variant", plan.variant),
                                "part": "SM", "stage": "source-mapping", "agent": "source-mapper",
                                "agent_id": "source-mapper", "use_callgraph": True,
                                "attempt_id": attempt_id, "parent_attempt_id": parent_attempt_id,
                                "callgraph_binding": binding,
                            },
                        )
                        response = await backend.run(request)
                        queries += int(getattr(response, "callgraph_queries", 0))
                        data_queries += int(getattr(response, "callgraph_data_queries", 0))
                        if not response.success and not schema_repairable(response):
                            failures.append(f"{invariant.phi_id}/{isa}: {response.error}")
                            break
                        try:
                            if not response.success:
                                raise ValueError(response.error or "source-mapper failed")
                            result = SourceMappingResult.model_validate(response.final)
                            if result.phi_id != invariant.phi_id or result.compiler != compiler:
                                raise ValueError("source-mapper invariant/compiler mismatch")
                            if result.isa not in (None, isa):
                                raise ValueError("source-mapper returned another ISA")
                            result.isa = cast(Any, isa)
                            mappings.append(validate_anchors(result, workspace.root))
                            break
                        except ValueError as exc:
                            last_error = str(exc)
                            candidate = response.final
                            raw = request.output_dir / "final.raw.txt"
                            if candidate is None and raw.is_file():
                                candidate = raw.read_text(encoding="utf-8")
                            prompt = repair_prompt(
                                original=invariant.as_prompt_payload(),
                                candidate=candidate, errors=last_error,
                            )
                            parent_attempt_id = attempt_id
                    else:
                        failures.append(f"{invariant.phi_id}/{isa}: {last_error}")
        mappings_path = output_dir / "source-mappings.jsonl"
        mappings_path.write_text("".join(m.model_dump_json() + "\n" for m in mappings))
        status = "completed" if mappings and not failures else "failed"
        summary = {
            "schema_version": 2, "stage": "source-mapping", "status": status,
            "repetition": repetition, "accepted_invariants": str(accepted),
            "accepted_invariants_sha256": sha256(accepted),
            "invariant_sha256": {r.invariant_id: invariant_identity(r.value) for r in records},
            "source_root": str(root), "source_tree_sha256": workspace.sha256,
            "source_snapshot_sha256": sha256(snapshot),
            "source_mappings_sha256": sha256(mappings_path),
            "scope": scope, "mapped": len(mappings), "failed": len(failures),
            "failures": failures, "callgraph_queries": queries, "agent_attempts": attempts,
            "callgraph_data_queries": data_queries,
            "callgraph_binding": binding,
        }
        summary_path = output_dir / "source-mapping-summary.json"
        summary_path.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
        return StageResult(
            stage="source-mapping", status=status, errors=failures,
            artifacts=[ArtifactRef.from_path(p, base_dir=output_dir)
                       for p in (mappings_path, summary_path, snapshot)],
            metrics={"invariants_total": len(records), "mapped": len(mappings),
                     "failed": len(failures), "callgraph_queries": queries},
            metadata={"repetition": repetition, "source_mappings": mappings_path.name,
                      "source_mapping_summary": summary_path.name, "scope": scope},
        )
    except Exception as exc:
        return StageResult(stage="source-mapping", status="failed",
                           error=f"{type(exc).__name__}: {exc}",
                           metadata={"repetition": repetition})


__all__ = ["run", "load_source_mappings", "validate_anchors"]
