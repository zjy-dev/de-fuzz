"""Typed, content-addressed orchestration for the three experiment parts.

The pipeline deliberately treats process completion, result validity, and the
scientific outcome as separate facts.  A stage can complete successfully and
still produce an invalid handoff (for example, Part I producing no accepted
invariants).  Conversely, a healthy Part III run with no findings is a valid
negative outcome.
"""

from __future__ import annotations

import asyncio
import hashlib
import importlib
import json
import math
import os
import re
import shutil
import subprocess
import time
import uuid
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Literal, cast

import yaml
from pydantic import BaseModel, ConfigDict, Field, PositiveInt, model_validator

from defuzz_loop.audit_schema import normalize_isa, normalize_mechanism
from defuzz_loop.checker_bundle import load_checker_bundle
from defuzz_loop.parity import ParityProfile, ParityScope, ThresholdMetric
from defuzz_loop.token_usage import TokenUsageContext, TokenUsageSink, use_token_usage

from .agent_backend import AgentBackend, AgentRequest, AgentResult, ExecAgentBackend
from .campaign_results import write_campaign_results
from .http_agent_backend import (
    HTTPAgentConfig,
    HTTPResponsesAgentBackend,
    load_http_agent_config,
    load_http_agent_config_snapshot,
)
from .models import (
    ArtifactRef,
    BudgetEnvelope,
    ExperimentPlan,
    StageResult,
    VariantName,
    canonical_variant_order,
)

PipelineMode = Literal["formal", "fixture"]
GenerationPath = Literal["combined", "segmented-cot", "rag"]
CompilerName = Literal["gcc", "llvm"]
ExecutionStatus = Literal["completed", "failed", "skipped"]
StageRunner = Callable[[ExperimentPlan, int, Path, AgentBackend | None], Awaitable[StageResult]]


def _default_variants() -> list[VariantName]:
    return ["full"]


_LANE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")
_STAGES = ("part_i", "part_ii", "part_iii")
_STAGE_NAMES = {
    "part_i": "invariant-generation",
    "part_ii": "checker-authoring",
    "part_iii": "agent-audit",
}
_REQUIRED_OUTPUTS = {
    "part_i": "accepted-invariants.jsonl",
    "part_ii": "checker-bundle-manifest.json",
    "part_iii": "agent-audit-summary.json",
}


class _StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class PipelineBackendConfig(_StrictModel):
    kind: Literal["traex", "codex", "http"] = "traex"
    binary: str | None = None
    model: str | None = None
    config_path: Path | None = None
    extra_args: list[str] = Field(default_factory=list)

    @model_validator(mode="after")
    def _validate_strings(self) -> PipelineBackendConfig:
        if self.binary is not None and not self.binary.strip():
            raise ValueError("backend.binary must be non-empty when provided")
        if self.model is not None and not self.model.strip():
            raise ValueError("backend.model must be non-empty when provided")
        if self.kind == "http" and self.config_path is None:
            raise ValueError("backend.config_path is required when backend.kind=http")
        if self.kind != "http" and self.config_path is not None:
            raise ValueError("backend.config_path is supported only when backend.kind=http")
        if any(not value or "\x00" in value for value in self.extra_args):
            raise ValueError("backend.extra_args must contain non-empty, NUL-free arguments")
        if self.kind == "http" and self.extra_args:
            raise ValueError("backend.extra_args is unsupported when backend.kind=http")
        return self

    @property
    def selected_binary(self) -> str:
        return self.binary or self.kind


class PipelineBudgets(_StrictModel):
    part_i: BudgetEnvelope = Field(default_factory=BudgetEnvelope)
    part_ii: BudgetEnvelope = Field(default_factory=BudgetEnvelope)
    part_iii: BudgetEnvelope = Field(default_factory=BudgetEnvelope)

    def for_stage(self, stage: str) -> BudgetEnvelope:
        return cast(BudgetEnvelope, getattr(self, stage))


class PipelineGenerationConfig(_StrictModel):
    path: GenerationPath = "combined"
    reference_root: Path
    document_roots: list[Path] = Field(default_factory=list)
    max_segments: PositiveInt | None = None
    shard_index: int = Field(default=0, ge=0)
    shard_count: PositiveInt = 1

    @model_validator(mode="after")
    def _validate_shard(self) -> PipelineGenerationConfig:
        if self.shard_index >= self.shard_count:
            raise ValueError("generation.shard_index must be less than shard_count")
        return self


class PipelineTarget(_StrictModel):
    id: str
    compiler: CompilerName
    version: str
    corpus_root: Path
    rag_corpus_root: Path | None = None
    audit_source_roots: list[Path]
    mechanisms: list[str] = Field(default_factory=list)
    isas: list[str] = Field(default_factory=list)
    toolchains_config: Path | None = None

    @model_validator(mode="before")
    @classmethod
    def _accept_single_audit_root(cls, value: Any) -> Any:
        if not isinstance(value, Mapping):
            return value
        data = dict(value)
        roots = data.get("audit_source_roots")
        aliases = [(key, data.pop(key)) for key in ("target_tree", "source_root") if key in data]
        if roots is not None and aliases:
            names = ", ".join(key for key, _ in aliases)
            raise ValueError(
                "audit_source_roots cannot be combined with compatibility aliases: " + names
            )
        if len(aliases) > 1:
            raise ValueError("use only one of target_tree or source_root")
        if roots is None and aliases:
            roots = [aliases[0][1]]
        elif isinstance(roots, (str, Path)):
            roots = [roots]
        elif roots is not None:
            roots = list(roots)
        if roots is not None:
            data["audit_source_roots"] = roots
        return data

    @model_validator(mode="after")
    def _validate_target(self) -> PipelineTarget:
        if not _LANE_ID.fullmatch(self.id):
            raise ValueError(
                "target id must start with an alphanumeric character and contain only "
                "letters, digits, '.', '_' or '-'"
            )
        if not self.version.strip():
            raise ValueError("target version must be non-empty")
        if not self.audit_source_roots:
            raise ValueError("target audit_source_roots must not be empty")
        if any(not value.strip() for value in (*self.mechanisms, *self.isas)):
            raise ValueError("target mechanisms and isas must contain non-empty values")
        normalized_mechanisms = [normalize_mechanism(value) for value in self.mechanisms]
        normalized_isas = [normalize_isa(value) for value in self.isas]
        if any(not value for value in (*normalized_mechanisms, *normalized_isas)):
            raise ValueError("target mechanisms and isas must normalize to non-empty values")
        if len(normalized_mechanisms) != len(set(normalized_mechanisms)):
            raise ValueError("target mechanisms must be unique")
        if len(normalized_isas) != len(set(normalized_isas)):
            raise ValueError("target isas must be unique")
        self.mechanisms = normalized_mechanisms
        self.isas = normalized_isas
        return self


class PipelineCheckerConfig(_StrictModel):
    source_root: Path
    checker_root: Path = Path("core/internal/oracle")
    max_attempts: int = Field(default=3, ge=1, le=10)

    @model_validator(mode="after")
    def _relative_checker_root(self) -> PipelineCheckerConfig:
        if self.checker_root.is_absolute() or ".." in self.checker_root.parts:
            raise ValueError("checker.checker_root must be relative to checker.source_root")
        return self


class PipelineAuditConfig(_StrictModel):
    max_concurrency: PositiveInt = 1
    oracle_rounds: PositiveInt = 1
    demo_parity: bool = False
    parity_profile: ParityProfile = "demo-workset"
    parity_threshold: float | None = Field(default=None, ge=0.0, le=1.0)
    parity_threshold_metric: ThresholdMetric = "recall"
    require_verified_candidates: bool = True


class PipelineConfig(_StrictModel):
    """Versioned YAML contract for a complete experiment campaign."""

    schema_version: Literal[1] = 1
    run_id: str
    mode: PipelineMode = "formal"
    output_root: Path = Path("orchestrator/runs/pipelines")
    repetitions: PositiveInt = 1
    variants: list[VariantName] = Field(default_factory=_default_variants)
    backend: PipelineBackendConfig = Field(default_factory=PipelineBackendConfig)
    budgets: PipelineBudgets = Field(default_factory=PipelineBudgets)
    generation: PipelineGenerationConfig
    targets: list[PipelineTarget]
    toolchains_config: Path | None = None
    checker: PipelineCheckerConfig
    audit: PipelineAuditConfig = Field(default_factory=PipelineAuditConfig)

    @model_validator(mode="after")
    def _validate_campaign(self) -> PipelineConfig:
        if not _LANE_ID.fullmatch(self.run_id):
            raise ValueError(
                "run_id must start with an alphanumeric character and contain only "
                "letters, digits, '.', '_' or '-'"
            )
        if not self.targets:
            raise ValueError("targets must not be empty")
        if not self.variants:
            raise ValueError("variants must not be empty")
        if len(self.variants) != len(set(self.variants)):
            raise ValueError("variants must be unique")
        if (
            any(variant in {"without-oracle", "bare-agent"} for variant in self.variants)
            and "full" not in self.variants
        ):
            raise ValueError("without-oracle and bare-agent require variants to include full")
        ids = [target.id for target in self.targets]
        if len(ids) != len(set(ids)):
            raise ValueError("target ids must be unique")
        missing_toolchains = [
            target.id
            for target in self.targets
            if target.toolchains_config is None and self.toolchains_config is None
        ]
        if missing_toolchains:
            raise ValueError(
                "toolchains_config is required for targets: " + ", ".join(missing_toolchains)
            )
        if self.mode == "formal" and not self.audit.require_verified_candidates:
            raise ValueError("formal mode requires audit.require_verified_candidates=true")
        if (
            self.mode == "formal"
            and self.backend.kind != "http"
            and self.backend.model is None
        ):
            raise ValueError("formal mode requires backend.model to pin the exact model")
        if self.mode == "formal" and self.generation.max_segments is not None:
            raise ValueError(
                "formal mode forbids generation.max_segments; a capped Part I run is "
                "pilot evidence, not a complete campaign"
            )
        if self.mode == "formal" and (
            self.generation.shard_count != 1 or self.generation.shard_index != 0
        ):
            raise ValueError(
                "formal mode currently requires the complete unsharded Part I corpus; "
                "distributed shard unions are not yet a single validated pipeline run"
            )
        self.variants = canonical_variant_order(self.variants)
        return self

    def content_hash(self) -> str:
        return _canonical_sha256(self.model_dump(mode="json"))


class PipelineStageRecord(_StrictModel):
    stage: Literal["part_i", "part_ii", "part_iii"]
    execution_status: ExecutionStatus
    result_valid: bool
    continuation_ready: bool
    outcome: str
    result_path: str | None = None
    result_sha256: str | None = None
    required_artifact: ArtifactRef | None = None
    input_artifacts: list[ArtifactRef] = Field(default_factory=list)
    chain_sha256: str
    error: str | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)


class PipelineLaneResult(_StrictModel):
    target_id: str
    repetition: PositiveInt
    variant: VariantName = "full"
    execution_status: Literal["completed", "failed"]
    result_valid: bool
    outcome: str
    lane_dir: Path
    chain_sha256: str
    stages: dict[str, PipelineStageRecord]


class PipelineRunResult(_StrictModel):
    execution_status: Literal["completed", "failed"]
    result_valid: bool
    outcome: str
    manifest_path: Path
    lanes: list[PipelineLaneResult]
    campaign_artifacts: dict[str, dict[str, Any]] = Field(default_factory=dict)


@dataclass(frozen=True)
class _FrozenUpstream:
    invariant_path: Path
    invariant_ref: ArtifactRef
    bundle_path: Path
    bundle_ref: ArtifactRef
    bundle_payload: dict[str, Any]
    bundle_integrity: dict[str, Any]
    toolchains_path: Path
    toolchains_sha256: str


@dataclass(frozen=True)
class PipelineRunners:
    part_i: StageRunner
    part_ii: StageRunner
    part_iii: StageRunner
    backend_factory: Callable[[PipelineBackendConfig], AgentBackend | None] | None = None

    @classmethod
    def default(cls) -> PipelineRunners:
        async def invoke(
            module_name: str,
            plan: ExperimentPlan,
            repetition: int,
            output_dir: Path,
            backend: AgentBackend | None,
        ) -> StageResult:
            module = importlib.import_module(module_name)
            return await module.run(plan, repetition, output_dir, backend)

        async def part_i(
            plan: ExperimentPlan, repetition: int, output_dir: Path, backend: AgentBackend | None
        ) -> StageResult:
            return await invoke(
                "defuzz_loop.experiment_engine.invariant_generation",
                plan,
                repetition,
                output_dir,
                backend,
            )

        async def part_ii(
            plan: ExperimentPlan, repetition: int, output_dir: Path, backend: AgentBackend | None
        ) -> StageResult:
            return await invoke(
                "defuzz_loop.experiment_engine.checker_authoring",
                plan,
                repetition,
                output_dir,
                backend,
            )

        async def part_iii(
            plan: ExperimentPlan, repetition: int, output_dir: Path, backend: AgentBackend | None
        ) -> StageResult:
            return await invoke("defuzz_loop.agent_audit", plan, repetition, output_dir, backend)

        return cls(part_i=part_i, part_ii=part_ii, part_iii=part_iii)

    @classmethod
    def fixture_smoke(cls) -> PipelineRunners:
        """Return deterministic no-model runners for an executable smoke lane."""

        def atomic_write_bytes(path: Path, value: bytes) -> None:
            path.parent.mkdir(parents=True, exist_ok=True)
            temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
            try:
                temporary.write_bytes(value)
                os.replace(temporary, path)
            finally:
                temporary.unlink(missing_ok=True)

        async def part_i(
            plan: ExperimentPlan,
            repetition: int,
            output_dir: Path,
            backend: AgentBackend | None,
        ) -> StageResult:
            del backend
            target_id = str(plan.parameters["pipeline_target_id"])
            invariant_id = f"FIXTURE-{target_id.upper()}-{repetition:03d}"
            artifact = output_dir / "accepted-invariants.jsonl"
            atomic_write_bytes(
                artifact,
                _canonical_json(
                    {
                        "schema_version": 1,
                        "invariant_id": invariant_id,
                        "statement": "Fixture invariant for pipeline contract validation.",
                        "observation": "The no-model runner exercises artifact handoff only.",
                        "generation_path": "fixture",
                        "compiler": plan.parameters["compiler"],
                        "version": plan.parameters["version"],
                        "target": target_id,
                        "mechanism": "fixture",
                    }
                )
                + b"\n",
            )
            return StageResult(
                stage="invariant-generation",
                status="completed",
                execution_status="completed",
                result_valid=True,
                continuation_ready=True,
                outcome="invariants-produced",
                artifacts=[
                    ArtifactRef.from_path(artifact, base_dir=output_dir, kind="accepted-invariants")
                ],
                metrics={"accepted_invariants": 1},
            )

        async def part_ii(
            plan: ExperimentPlan,
            repetition: int,
            output_dir: Path,
            backend: AgentBackend | None,
        ) -> StageResult:
            del backend, repetition
            input_path = Path(str(plan.parameters["accepted_invariants"])).resolve(strict=True)
            records = [
                json.loads(line)
                for line in input_path.read_text(encoding="utf-8").splitlines()
                if line.strip()
            ]
            invariant_ids = [str(record["invariant_id"]) for record in records]
            if not invariant_ids:
                raise ValueError("fixture Part II requires at least one invariant")
            assert plan.source_root is not None
            source_root = plan.source_root.resolve(strict=True)
            source_tree_sha256 = str(_content_snapshot(source_root)["sha256"])
            patch_path = output_dir / "bundle.patch"
            catalog_path = output_dir / "checker-catalog.json"
            scoped_invariants_path = output_dir / "scoped-accepted-invariants.jsonl"
            input_scope_path = output_dir / "checker-input-scope.json"
            # Mirror the production bundle layout so fixture campaigns exercise
            # nested artifact copying and containment checks as well.
            dispatcher_path = output_dir / "bin" / "checker-dispatcher"
            dispatcher_path.parent.mkdir(parents=True, exist_ok=True)
            atomic_write_bytes(patch_path, b"# fixture pipeline: no source changes\n")
            atomic_write_bytes(scoped_invariants_path, input_path.read_bytes())
            requested_mechanisms = list(plan.parameters.get("mechanisms", []))
            requested_isas = list(plan.parameters.get("isas", []))
            _atomic_write_json(
                input_scope_path,
                {
                    "schema_version": 1,
                    "kind": "defuzz-checker-input-scope",
                    "source_artifact": {
                        "path": str(input_path),
                        "sha256": _sha256_file(input_path),
                        "size_bytes": input_path.stat().st_size,
                    },
                    "requested": {
                        "mechanisms": requested_mechanisms,
                        "isas": requested_isas,
                    },
                    "scope_requested": bool(requested_mechanisms or requested_isas),
                    "counts": {
                        "total": len(invariant_ids),
                        "selected": len(invariant_ids),
                        "excluded": 0,
                    },
                    "total_invariant_ids": invariant_ids,
                    "selected_invariant_ids": invariant_ids,
                    "excluded_invariant_ids": [],
                    "excluded_invariants": [],
                },
            )
            _atomic_write_json(
                catalog_path,
                {
                    "schema_version": 1,
                    "kind": "defuzz-checker-catalog",
                    "source_tree_sha256": source_tree_sha256,
                    "result_tree_sha256": source_tree_sha256,
                    "checkers": [
                        {
                            "checker_id": invariant_id,
                            "invariant_id": invariant_id,
                            "mechanism": "fixture",
                            "requires": [],
                        }
                        for invariant_id in invariant_ids
                    ],
                },
            )
            atomic_write_bytes(
                dispatcher_path,
                b"#!/bin/sh\necho 'fixture dispatcher is not used by smoke mode' >&2\nexit 2\n",
            )
            dispatcher_path.chmod(0o755)
            artifact_paths = {
                "cumulative_patch": patch_path,
                "catalog": catalog_path,
                "dispatcher": dispatcher_path,
                "scoped_invariants": scoped_invariants_path,
                "input_scope": input_scope_path,
            }
            artifacts = {
                name: ArtifactRef.from_path(path, base_dir=output_dir, kind=name).to_dict()
                for name, path in artifact_paths.items()
            }
            manifest: dict[str, Any] = {
                "schema_version": 1,
                "kind": "defuzz-checker-bundle",
                "status": "ready",
                "source_root": str(source_root),
                "source_root_sha256": source_tree_sha256,
                "source_tree_sha256": source_tree_sha256,
                "final_tree_sha256": source_tree_sha256,
                "source_invariants_sha256": _sha256_file(input_path),
                "requested_mechanisms": requested_mechanisms,
                "requested_isas": requested_isas,
                "coverage_complete": True,
                "budget_exhausted": False,
                "included_invariant_ids": invariant_ids,
                "failed_invariant_ids": [],
                "invariants": [
                    {
                        "invariant_id": invariant_id,
                        "final_status": "passed",
                        "parent_tree_sha256": source_tree_sha256,
                        "result_tree_sha256": source_tree_sha256,
                        "files": [],
                    }
                    for invariant_id in invariant_ids
                ],
                "artifacts": artifacts,
                "validation": {
                    "status": "passed",
                    "commands": [],
                    "build": {"status": "passed", "fixture": True},
                },
            }
            manifest["bundle_id"] = _canonical_sha256(manifest)
            manifest_path = output_dir / "checker-bundle-manifest.json"
            _atomic_write_json(manifest_path, manifest)
            return StageResult(
                stage="checker-authoring",
                status="completed",
                execution_status="completed",
                result_valid=True,
                continuation_ready=True,
                outcome="checker-bundle-ready",
                artifacts=[
                    ArtifactRef.from_path(manifest_path, base_dir=output_dir, kind="checker-bundle")
                ],
                metrics={"bundle_ready": True, "included_checkers": len(invariant_ids)},
            )

        async def part_iii(
            plan: ExperimentPlan,
            repetition: int,
            output_dir: Path,
            backend: AgentBackend | None,
        ) -> StageResult:
            del backend
            bundle_path = Path(str(plan.parameters["checker_bundle_manifest"])).resolve(strict=True)
            bundle = _validate_bundle_manifest(bundle_path)
            invariant_path = Path(str(plan.parameters["accepted_invariants"])).resolve(
                strict=True
            )
            if plan.parameters["accepted_invariants_sha256"] != _sha256_file(invariant_path):
                raise ValueError("fixture accepted invariants SHA-256 mismatch")
            toolchains = Path(str(plan.parameters["toolchains_config"])).resolve(strict=True)
            source_roots = [
                str(Path(str(path)).resolve(strict=True))
                for path in cast(Sequence[Any], plan.parameters["source_roots"])
            ]
            summary_path = output_dir / "agent-audit-summary.json"
            _atomic_write_json(
                summary_path,
                {
                    "schema_version": 1,
                    "run_id": plan.run_id,
                    "repetition": repetition,
                    "variant": plan.variant,
                    "campaign_variant": str(plan.parameters.get("campaign_variant", plan.variant)),
                    "execution_status": "completed",
                    "execution_completed": True,
                    "result_valid": True,
                    "continuation_ready": True,
                    "outcome": "no-verified-findings",
                    "verified_candidates": [],
                    "fixture_smoke": True,
                    "checker_bundle": {
                        "bundle_id": bundle["bundle_id"],
                        "manifest_sha256": _sha256_file(bundle_path),
                        "manifest_path": str(bundle_path),
                        "toolchains_config": str(toolchains),
                        "toolchains_config_sha256": _sha256_file(toolchains),
                    },
                    "source_roots": source_roots,
                    "generated_invariants": {
                        "visible_to_worker": plan.variant != "bare-agent",
                        "sha256": _sha256_file(invariant_path),
                    },
                },
            )
            return StageResult(
                stage="agent-audit",
                status="completed",
                execution_status="completed",
                result_valid=True,
                continuation_ready=True,
                outcome="no-verified-findings",
                artifacts=[
                    ArtifactRef.from_path(summary_path, base_dir=output_dir, kind="audit-summary")
                ],
                metrics={"candidate_verified": 0},
            )

        return cls(part_i=part_i, part_ii=part_ii, part_iii=part_iii)


class _TokenSinkBackend:
    """Attach the stage sink even when a runner omits it from a request."""

    def __init__(self, backend: AgentBackend, sink: TokenUsageSink) -> None:
        self._backend = backend
        self._sink = sink

    def __getattr__(self, name: str) -> Any:
        return getattr(self._backend, name)

    async def run(self, request: AgentRequest) -> AgentResult:
        sink = request.token_sink or self._sink
        return await self._backend.run(request.model_copy(update={"token_sink": sink}))

    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        kwargs.setdefault("token_sink", self._sink)
        complete = cast(Any, self._backend).complete
        return await complete(prompt, schema, **kwargs)


def _canonical_json(value: Any) -> bytes:
    return json.dumps(
        value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), default=str
    ).encode("utf-8")


def _canonical_sha256(value: Any) -> str:
    return hashlib.sha256(_canonical_json(value)).hexdigest()


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _atomic_write_json(path: Path, value: Any) -> None:
    _atomic_write_bytes(
        path,
        json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True, default=str).encode("utf-8")
        + b"\n",
    )


def _atomic_write_bytes(path: Path, value: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    try:
        temporary.write_bytes(value)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def _resolve_path(path: Path, base: Path) -> Path:
    expanded = path.expanduser()
    return (expanded if expanded.is_absolute() else base / expanded).resolve(strict=False)


def load_pipeline_config(path: str | os.PathLike[str]) -> PipelineConfig:
    """Load YAML and resolve every path relative to the configuration file."""

    source = Path(path).expanduser().resolve(strict=True)
    try:
        payload = yaml.safe_load(source.read_text(encoding="utf-8"))
    except yaml.YAMLError as exc:
        raise ValueError(f"pipeline config is not valid YAML: {source}: {exc}") from exc
    if not isinstance(payload, Mapping):
        raise ValueError(f"pipeline config must be a YAML mapping: {source}")
    config = PipelineConfig.model_validate(payload)
    base = source.parent
    generation = config.generation.model_copy(
        update={
            "reference_root": _resolve_path(config.generation.reference_root, base),
            "document_roots": [
                _resolve_path(item, base) for item in config.generation.document_roots
            ],
        }
    )
    targets = [
        target.model_copy(
            update={
                "corpus_root": _resolve_path(target.corpus_root, base),
                "rag_corpus_root": (
                    _resolve_path(target.rag_corpus_root, base)
                    if target.rag_corpus_root is not None
                    else None
                ),
                "audit_source_roots": [
                    _resolve_path(item, base) for item in target.audit_source_roots
                ],
                "toolchains_config": (
                    _resolve_path(target.toolchains_config, base)
                    if target.toolchains_config is not None
                    else None
                ),
            }
        )
        for target in config.targets
    ]
    checker = config.checker.model_copy(
        update={"source_root": _resolve_path(config.checker.source_root, base)}
    )
    backend = config.backend.model_copy(
        update={
            "config_path": (
                _resolve_path(config.backend.config_path, base)
                if config.backend.config_path is not None
                else None
            )
        }
    )
    if backend.kind == "http":
        http_config = _http_backend_config(backend)
        backend = backend.model_copy(update={"model": http_config.model})
    return config.model_copy(
        update={
            "output_root": _resolve_path(config.output_root, base),
            "generation": generation,
            "targets": targets,
            "toolchains_config": (
                _resolve_path(config.toolchains_config, base)
                if config.toolchains_config is not None
                else None
            ),
            "checker": checker,
            "backend": backend,
        }
    )


def _require_file(path: Path, label: str) -> Path:
    if not path.is_file():
        raise ValueError(f"{label} is not an existing file: {path}")
    return path


def _require_directory(path: Path, label: str) -> Path:
    if not path.is_dir():
        raise ValueError(f"{label} is not an existing directory: {path}")
    return path


def _git(working_tree: Path, *args: str) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(
        ["git", "-C", os.fspath(working_tree), *args],
        check=False,
        capture_output=True,
    )


def _repository_for(path: Path) -> Path | None:
    cwd = path if path.is_dir() else path.parent
    result = _git(cwd, "rev-parse", "--show-toplevel")
    if result.returncode != 0:
        return None
    return Path(os.fsdecode(result.stdout).strip()).resolve()


def _source_revision(path: Path) -> dict[str, str] | None:
    """Return the immutable Git identity for an input when one is available."""

    repository = _repository_for(path)
    if repository is None:
        return None
    head = _git(repository, "rev-parse", "HEAD")
    if head.returncode != 0:
        return None
    try:
        relative = path.resolve(strict=False).relative_to(repository).as_posix() or "."
    except ValueError:
        return None
    commit = os.fsdecode(head.stdout).strip()
    if not re.fullmatch(r"[0-9a-fA-F]{40,64}", commit):
        return None
    return {
        "repository_root": str(repository),
        "head_commit": commit,
        "path_relative_to_repository": relative,
    }


def _assert_clean_repositories(paths: Sequence[Path], *, output_root: Path) -> None:
    repositories: dict[Path, None] = {}
    for path in paths:
        repository = _repository_for(path)
        if repository is None:
            raise ValueError(f"formal input is not in a Git repository: {path}")
        repositories[repository] = None
    for repository in repositories:
        args = ["status", "--porcelain=v1", "--untracked-files=all", "--", "."]
        try:
            relative_output = output_root.resolve(strict=False).relative_to(repository)
        except ValueError:
            pass
        else:
            args.append(f":(exclude){relative_output.as_posix()}")
        status = _git(repository, *args)
        if status.returncode != 0:
            detail = os.fsdecode(status.stderr).strip()
            raise ValueError(f"cannot inspect Git worktree {repository}: {detail}")
        if status.stdout:
            raise ValueError(f"formal mode requires a clean Git worktree: {repository}")


def _path_is_within(path: Path, parent: Path) -> bool:
    try:
        path.resolve(strict=False).relative_to(parent.resolve(strict=False))
    except ValueError:
        return False
    return True


def _tree_content_snapshot(root: Path, *, exclude: Path | None = None) -> dict[str, Any]:
    digest = hashlib.sha256()
    count = 0
    total_bytes = 0
    for directory, names, filenames in os.walk(root, followlinks=False):
        parent = Path(directory)
        names[:] = sorted(
            name
            for name in names
            if name != ".git" and (exclude is None or not _path_is_within(parent / name, exclude))
        )
        for filename in sorted(filenames):
            path = parent / filename
            if exclude is not None and _path_is_within(path, exclude):
                continue
            relative = path.relative_to(root).as_posix()
            if path.is_symlink():
                content = os.readlink(path).encode("utf-8", errors="surrogateescape")
                file_hash = hashlib.sha256(b"symlink\0" + content).hexdigest()
                size = len(content)
            elif path.is_file():
                file_hash = _sha256_file(path)
                size = path.stat().st_size
            else:
                continue
            digest.update(relative.encode("utf-8", errors="surrogateescape"))
            digest.update(b"\0")
            digest.update(file_hash.encode("ascii"))
            digest.update(b"\n")
            count += 1
            total_bytes += size
    return {
        "kind": "content-tree",
        "path": str(root),
        "sha256": digest.hexdigest(),
        "files": count,
        "size_bytes": total_bytes,
    }


def _git_content_snapshot(root: Path, *, exclude: Path | None = None) -> dict[str, Any] | None:
    """Hash tracked and non-ignored untracked files below a Git-backed input.

    Runtime outputs, build caches, and other ignored files are deliberately not
    experiment inputs. Excluding them also prevents a campaign from changing
    its own plan identity when ``output_root`` lives below a source checkout.
    """

    repository = _repository_for(root)
    if repository is None:
        return None
    try:
        scope = root.resolve(strict=False).relative_to(repository).as_posix() or "."
    except ValueError:
        return None
    tracked = _git(repository, "ls-files", "-z", "--cached", "--", scope)
    if tracked.returncode != 0 or not tracked.stdout:
        # A compiler corpus placed in a Git-ignored directory is still an input;
        # fall back to the complete filesystem snapshot for that case.
        return None
    untracked = _git(
        repository,
        "ls-files",
        "-z",
        "--others",
        "--exclude-standard",
        "--",
        scope,
    )
    if untracked.returncode != 0:
        return None

    entries = sorted(
        {
            os.fsdecode(value)
            for value in (*tracked.stdout.split(b"\0"), *untracked.stdout.split(b"\0"))
            if value
        }
    )
    digest = hashlib.sha256()
    count = 0
    total_bytes = 0
    for entry in entries:
        path = repository / entry
        if exclude is not None and _path_is_within(path, exclude):
            continue
        try:
            relative = path.relative_to(root).as_posix()
        except ValueError:
            continue
        if path.is_symlink():
            content = os.readlink(path).encode("utf-8", errors="surrogateescape")
            file_hash = hashlib.sha256(b"symlink\0" + content).hexdigest()
            size = len(content)
        elif path.is_file():
            file_hash = _sha256_file(path)
            size = path.stat().st_size
        else:
            # Deleted tracked files are represented by their absence.
            continue
        digest.update(relative.encode("utf-8", errors="surrogateescape"))
        digest.update(b"\0")
        digest.update(file_hash.encode("ascii"))
        digest.update(b"\n")
        count += 1
        total_bytes += size
    return {
        "kind": "git-content-tree",
        "path": str(root),
        "sha256": digest.hexdigest(),
        "files": count,
        "size_bytes": total_bytes,
    }


def _content_snapshot(path: Path, *, exclude: Path | None = None) -> dict[str, Any]:
    if path.is_file():
        return {
            "kind": "file",
            "path": str(path),
            "sha256": _sha256_file(path),
            "size_bytes": path.stat().st_size,
        }
    return _git_content_snapshot(path, exclude=exclude) or _tree_content_snapshot(
        path, exclude=exclude
    )


def _formal_inputs(config: PipelineConfig) -> list[Path]:
    values = [config.generation.reference_root, config.checker.source_root]
    values.extend(config.generation.document_roots)
    for target in config.targets:
        values.append(target.corpus_root)
        if target.rag_corpus_root is not None:
            values.append(target.rag_corpus_root)
        values.extend(target.audit_source_roots)
    return list(dict.fromkeys(path.resolve(strict=False) for path in values))


def _toolchains_for(config: PipelineConfig, target: PipelineTarget) -> Path:
    selected = target.toolchains_config or config.toolchains_config
    assert selected is not None  # Enforced by PipelineConfig.
    return selected


_TOOLCHAIN_FIELDS = frozenset(
    {
        "gcc_path",
        "clang_path",
        "prefix",
        "sysroot",
        "cflags",
        "qemu_path",
        "qemu_sysroot",
        "native",
    }
)
_TOOLCHAIN_STRING_FIELDS = _TOOLCHAIN_FIELDS - {"cflags", "native"}


def _load_formal_toolchains(path: Path) -> Mapping[str, Any]:
    try:
        payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, yaml.YAMLError) as exc:
        raise ValueError(f"toolchains config cannot be parsed: {path}: {exc}") from exc
    if not isinstance(payload, Mapping):
        raise ValueError(f"toolchains config must be a mapping: {path}")
    unknown_root = sorted(str(key) for key in payload if key != "toolchains")
    if unknown_root:
        raise ValueError(f"toolchains config has unknown root field(s) {unknown_root}: {path}")
    toolchains = payload.get("toolchains")
    if not isinstance(toolchains, Mapping):
        raise ValueError(f"toolchains config must contain a 'toolchains' mapping: {path}")
    for isa, entry in toolchains.items():
        if not isinstance(isa, str) or not isa.strip():
            raise ValueError(f"toolchains config ISA keys must be non-empty strings: {path}")
        if not isinstance(entry, Mapping):
            raise ValueError(f"toolchains config ISA {isa!r} entry must be a mapping: {path}")
        unknown_fields = sorted(str(key) for key in entry if key not in _TOOLCHAIN_FIELDS)
        if unknown_fields:
            raise ValueError(
                f"toolchains config ISA {isa!r} has unknown field(s) {unknown_fields}: {path}"
            )
        for field in _TOOLCHAIN_STRING_FIELDS:
            if field in entry and not isinstance(entry[field], str):
                raise ValueError(
                    f"toolchains config ISA {isa!r} field {field!r} must be a string: {path}"
                )
        if "cflags" in entry and (
            not isinstance(entry["cflags"], list)
            or not all(isinstance(item, str) for item in entry["cflags"])
        ):
            raise ValueError(
                f"toolchains config ISA {isa!r} field 'cflags' must be a list of strings: {path}"
            )
        if "native" in entry and not isinstance(entry["native"], bool):
            raise ValueError(
                f"toolchains config ISA {isa!r} field 'native' must be a boolean: {path}"
            )
    return cast(Mapping[str, Any], toolchains)


def _formal_toolchain_drivers(config: PipelineConfig) -> list[dict[str, Any]]:
    cached: dict[Path, Mapping[str, Any]] = {}
    drivers: list[dict[str, Any]] = []
    for target in config.targets:
        if not target.mechanisms:
            raise ValueError(f"formal target {target.id!r} mechanisms must not be empty")
        if not target.isas:
            raise ValueError(f"formal target {target.id!r} isas must not be empty")
        config_path = _toolchains_for(config, target).resolve(strict=True)
        toolchains = cached.get(config_path)
        if toolchains is None:
            toolchains = _load_formal_toolchains(config_path)
            cached[config_path] = toolchains
        driver_field = "gcc_path" if target.compiler == "gcc" else "clang_path"
        for isa in target.isas:
            entry = toolchains.get(isa)
            context = (
                f"target={target.id!r}, isa={isa!r}, compiler={target.compiler!r}, "
                f"config={config_path}"
            )
            if entry is None:
                raise ValueError(
                    f"formal toolchain preflight failed ({context}): ISA {isa!r} is not configured"
                )
            if not isinstance(entry, Mapping):
                # _load_formal_toolchains validates every entry; retain this guard
                # for type narrowing and defense in depth.
                raise ValueError(
                    f"formal toolchain preflight failed ({context}): "
                    "toolchain entry must be a mapping"
                )
            raw_driver = entry.get(driver_field)
            if not isinstance(raw_driver, str) or not raw_driver.strip():
                raise ValueError(
                    f"formal toolchain preflight failed ({context}): "
                    f"{driver_field} must be a non-empty string"
                )
            configured_driver = Path(raw_driver)
            if not configured_driver.is_absolute():
                raise ValueError(
                    f"formal toolchain preflight failed ({context}): "
                    f"{driver_field} must be absolute, got {raw_driver!r}"
                )
            try:
                driver = configured_driver.resolve(strict=True)
            except OSError as exc:
                raise ValueError(
                    f"formal toolchain preflight failed ({context}): "
                    f"{driver_field} does not resolve: {raw_driver!r}"
                ) from exc
            if not driver.is_file():
                raise ValueError(
                    f"formal toolchain preflight failed ({context}): "
                    f"driver is not a regular file: {driver}"
                )
            if not os.access(driver, os.X_OK):
                raise ValueError(
                    f"formal toolchain preflight failed ({context}): "
                    f"driver is not executable: {driver}"
                )
            drivers.append(
                {
                    "target_id": target.id,
                    "isa": isa,
                    "compiler": target.compiler,
                    "path": str(driver),
                    "sha256": _sha256_file(driver),
                    "size_bytes": driver.stat().st_size,
                }
            )
    return drivers


def _http_backend_config(config: PipelineBackendConfig) -> HTTPAgentConfig:
    """Load the HTTP config while keeping its credential value environment-only."""

    assert config.config_path is not None  # Enforced by PipelineBackendConfig.
    path = _require_file(config.config_path.resolve(strict=False), "HTTP agent config")
    try:
        http_config = load_http_agent_config(path)
    except (OSError, ValueError) as exc:
        raise ValueError(f"HTTP agent config is invalid: {path}: {exc}") from exc
    if config.model is not None and config.model != http_config.model:
        raise ValueError(
            "backend.model must match the HTTP agent config model: "
            f"pipeline={config.model!r}, http_config={http_config.model!r}"
        )
    return http_config


def _http_backend_identity(
    config_path: Path,
    http_config: HTTPAgentConfig,
    *,
    config_sha256: str,
    config_size_bytes: int,
) -> dict[str, Any]:
    """Persist reproducibility identity without persisting any credential."""

    return {
        "kind": "http-responses",
        "config_file": {
            "kind": "file",
            "path": str(config_path),
            "sha256": config_sha256,
            "size_bytes": config_size_bytes,
        },
        "endpoint": http_config.responses_url,
        "model": http_config.model,
        "reasoning_effort": http_config.reasoning_effort,
        "api_key_env": http_config.api_key_env,
        # Credentials are environment-only, so the complete validated model is
        # safe to persist and freezes retry/tool/continuation behavior too.
        "settings": http_config.model_dump(mode="json"),
    }


def _backend_model(config: PipelineBackendConfig) -> str | None:
    """Return the model frozen while the pipeline config was resolved."""

    if config.model is not None:
        return config.model
    return _http_backend_config(config).model if config.kind == "http" else None


def build_pipeline_plan(
    config: PipelineConfig, *, config_path: str | os.PathLike[str] | None = None
) -> dict[str, Any]:
    """Resolve and content-hash a pipeline without writing run artifacts."""

    if config.mode == "formal" and os.environ.get("DEFUZZ_FAST_PLAN"):
        raise ValueError("formal mode forbids DEFUZZ_FAST_PLAN")

    required_directories = _formal_inputs(config)
    for path in required_directories:
        _require_directory(path, "pipeline input directory")
    toolchain_paths = [
        _toolchains_for(config, target).resolve(strict=False) for target in config.targets
    ]
    for path in toolchain_paths:
        _require_file(path, "toolchains config")
    toolchain_drivers = _formal_toolchain_drivers(config) if config.mode == "formal" else []
    http_config: HTTPAgentConfig | None = None
    http_config_sha256: str | None = None
    http_config_size_bytes: int | None = None
    if config.backend.kind == "http":
        assert config.backend.config_path is not None
        http_config, http_config_sha256, http_config_size_bytes = (
            load_http_agent_config_snapshot(config.backend.config_path)
        )
        if config.backend.model is not None and config.backend.model != http_config.model:
            raise ValueError(
                "backend.model must match the HTTP agent config model: "
                f"pipeline={config.backend.model!r}, http_config={http_config.model!r}"
            )
        resolved_backend = None
        backend_available = True
        if config.mode == "formal" and not os.environ.get(http_config.api_key_env):
            raise ValueError(
                "formal mode requires HTTP agent API key environment variable: "
                f"{http_config.api_key_env}"
            )
    else:
        backend_candidate = Path(config.backend.selected_binary).expanduser()
        if backend_candidate.is_absolute() or os.sep in config.backend.selected_binary:
            try:
                resolved_candidate = backend_candidate.resolve(strict=True)
            except OSError:
                resolved_backend = None
            else:
                resolved_backend = (
                    str(resolved_candidate)
                    if resolved_candidate.is_file() and os.access(resolved_candidate, os.X_OK)
                    else None
                )
        else:
            resolved_backend = shutil.which(config.backend.selected_binary)
        backend_available = resolved_backend is not None
    if config.mode == "formal" and not backend_available:
        raise ValueError(
            f"formal mode requires an available agent binary: {config.backend.selected_binary}"
        )
    if config.mode == "formal":
        clean_inputs = [*required_directories, *toolchain_paths]
        if config_path is not None:
            clean_inputs.append(Path(config_path).expanduser().resolve(strict=True))
        if config.backend.config_path is not None:
            clean_inputs.append(config.backend.config_path.resolve(strict=True))
        _assert_clean_repositories(clean_inputs, output_root=config.output_root)

    snapshots: dict[str, dict[str, Any]] = {}
    for path in (*required_directories, *toolchain_paths):
        key = str(path.resolve(strict=False))
        snapshot = _content_snapshot(path.resolve(strict=False), exclude=config.output_root)
        revision = _source_revision(path)
        if revision is not None:
            snapshot["source_revision"] = revision
        snapshots.setdefault(key, snapshot)
    config_file: dict[str, Any] | None = None
    if config_path is not None:
        path = _require_file(Path(config_path).expanduser().resolve(strict=True), "pipeline config")
        config_file = _content_snapshot(path)
        revision = _source_revision(path)
        if revision is not None:
            config_file["source_revision"] = revision

    revision_values = [
        snapshot["source_revision"]
        for snapshot in snapshots.values()
        if "source_revision" in snapshot
    ]
    if config_file is not None and "source_revision" in config_file:
        revision_values.append(config_file["source_revision"])
    source_revisions = {
        str(revision["repository_root"]): {"head_commit": revision["head_commit"]}
        for revision in revision_values
    }

    backend_identity: dict[str, Any] = {
        "runner": "fixture-smoke" if config.mode == "fixture" else "production",
        "required": config.mode == "formal",
    }
    if config.mode == "formal" and http_config is None:
        assert resolved_backend is not None
        backend_path = Path(resolved_backend).resolve(strict=True)
        backend_identity.update(_content_snapshot(backend_path))
    elif http_config is not None:
        assert config.backend.config_path is not None
        assert http_config_sha256 is not None
        assert http_config_size_bytes is not None
        backend_identity.update(
            _http_backend_identity(
                config.backend.config_path,
                http_config,
                config_sha256=http_config_sha256,
                config_size_bytes=http_config_size_bytes,
            )
        )

    semantic_hash = config.content_hash()
    lanes = [
        {
            "target_id": target.id,
            "compiler": target.compiler,
            "version": target.version,
            "variant": variant,
            "generation_path": _generation_path_for_campaign(config, variant),
            "repetition": repetition,
            "lane_id": f"{target.id}-{variant}-r{repetition:03d}",
            "output_dir": str(
                config.output_root
                / config.run_id
                / "lanes"
                / target.id
                / variant
                / f"rep-{repetition:03d}"
            ),
        }
        for target in config.targets
        for variant in config.variants
        for repetition in range(1, config.repetitions + 1)
    ]
    identity = {
        "schema_version": 1,
        "config_sha256": semantic_hash,
        "config_file": config_file,
        "backend_identity": backend_identity,
        "input_snapshots": snapshots,
        "source_revisions": source_revisions,
        "toolchain_drivers": toolchain_drivers,
        "lanes": lanes,
    }
    return {
        **identity,
        "plan_sha256": _canonical_sha256(identity),
        "status": "ready",
        "mode": config.mode,
        "run_id": config.run_id,
        "output_root": str(config.output_root),
        "backend": {
            **config.backend.model_dump(mode="json"),
            "binary": config.backend.selected_binary,
            "resolved_path": resolved_backend,
            "available": backend_available,
            "required": config.mode == "formal",
            "model": http_config.model if http_config is not None else config.backend.model,
        },
        "runner": "fixture-smoke" if config.mode == "fixture" else "production",
        "config": config.model_dump(mode="json"),
    }


def _chain_hash(previous: str, record: Mapping[str, Any]) -> str:
    payload = dict(record)
    payload.pop("chain_sha256", None)
    digest = hashlib.sha256()
    digest.update(previous.encode("ascii"))
    digest.update(b"\n")
    digest.update(_canonical_json(payload))
    return digest.hexdigest()


def _result_execution_status(result: StageResult) -> ExecutionStatus:
    explicit = result.execution_status or result.metadata.get("execution_status")
    if explicit in {"completed", "failed", "skipped"}:
        return cast(ExecutionStatus, explicit)
    if result.status in {"completed", "succeeded", "success", "partial"}:
        return "completed"
    if result.status == "skipped":
        return "skipped"
    return "failed"


def _artifact_from_result(
    result: StageResult, output_dir: Path, filename: str
) -> tuple[Path, ArtifactRef]:
    path = (output_dir / filename).resolve(strict=False)
    try:
        path.relative_to(output_dir.resolve())
    except ValueError as exc:
        raise ValueError(f"required artifact escapes stage output: {path}") from exc
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"required stage artifact is missing or not regular: {path}")
    declared = next(
        (artifact for artifact in result.artifacts if Path(artifact.path).name == filename),
        None,
    )
    if declared is None:
        raise ValueError(f"stage result does not declare required artifact: {filename}")
    actual = ArtifactRef.from_path(path, base_dir=output_dir, kind=declared.kind)
    if actual.sha256 != declared.sha256 or actual.size_bytes != declared.size_bytes:
        raise ValueError(f"stage artifact hash mismatch: {path}")
    return path, actual


def _read_json(path: Path, label: str) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"{label} is unreadable or invalid JSON: {path}: {exc}") from exc
    if not isinstance(payload, dict):
        raise ValueError(f"{label} must be a JSON object: {path}")
    return payload


def _count_jsonl(path: Path) -> int:
    count = 0
    for line_number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not raw.strip():
            continue
        try:
            value = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise ValueError(
                f"accepted invariants contain invalid JSON at line {line_number}: {exc}"
            ) from exc
        if not isinstance(value, dict):
            raise ValueError(f"accepted invariants line {line_number} must be a JSON object")
        count += 1
    return count


def _validate_bundle_manifest(path: Path) -> dict[str, Any]:
    """Use the bundle loader when available and retain a strict v1 fallback."""

    try:
        module = importlib.import_module("defuzz_loop.checker_bundle")
    except ImportError:
        module = None
    if module is not None and hasattr(module, "load_checker_bundle"):
        module.load_checker_bundle(path, require_ready=True)

    payload = _read_json(path, "checker bundle manifest")
    if payload.get("schema_version") != 1:
        raise ValueError("checker bundle schema_version must be 1")
    if payload.get("kind") != "defuzz-checker-bundle":
        raise ValueError("checker bundle kind must be 'defuzz-checker-bundle'")
    if payload.get("status") != "ready":
        raise ValueError("checker bundle status must be 'ready'")
    if payload.get("validation", {}).get("status") != "passed":
        raise ValueError("checker bundle cumulative validation did not pass")
    bundle_id = payload.get("bundle_id")
    unsigned = dict(payload)
    unsigned.pop("bundle_id", None)
    expected = _canonical_sha256(unsigned)
    if bundle_id != expected:
        raise ValueError(f"checker bundle_id mismatch: expected {expected}, got {bundle_id!r}")
    artifacts = payload.get("artifacts")
    if not isinstance(artifacts, Mapping):
        raise ValueError("checker bundle artifacts must be an object")
    manifest_root = path.parent.resolve()
    for name, raw in artifacts.items():
        if not isinstance(raw, Mapping):
            raise ValueError(f"checker bundle artifact {name!r} must be an object")
        relative = raw.get("path")
        if not isinstance(relative, str) or not relative:
            raise ValueError(f"checker bundle artifact {name!r} has no path")
        artifact = (manifest_root / relative).resolve(strict=False)
        try:
            artifact.relative_to(manifest_root)
        except ValueError as exc:
            raise ValueError(f"checker bundle artifact escapes bundle: {relative}") from exc
        if not artifact.is_file() or artifact.is_symlink():
            raise ValueError(f"checker bundle artifact is not a regular file: {artifact}")
        actual_hash = _sha256_file(artifact)
        if raw.get("sha256") != actual_hash:
            raise ValueError(f"checker bundle artifact hash mismatch: {artifact}")
        if raw.get("size_bytes") != artifact.stat().st_size:
            raise ValueError(f"checker bundle artifact size mismatch: {artifact}")
    included = payload.get("included_invariant_ids")
    if not isinstance(included, list) or not included:
        raise ValueError("ready checker bundle must include at least one invariant")
    return payload


def _bundle_integrity(path: Path) -> dict[str, Any]:
    """Revalidate a bundle and return its complete content-addressed identity."""

    payload = _validate_bundle_manifest(path)
    artifacts = cast(Mapping[str, Mapping[str, Any]], payload["artifacts"])
    artifact_integrity: dict[str, dict[str, Any]] = {}
    for name, record in sorted(artifacts.items()):
        artifact_path = (path.parent / str(record["path"])).resolve(strict=True)
        mode = artifact_path.stat().st_mode & 0o777
        artifact_integrity[name] = {
            "path": str(record["path"]),
            "sha256": str(record["sha256"]),
            "size_bytes": record.get("size_bytes"),
            "mode": mode,
        }
        if name == "dispatcher" and not os.access(artifact_path, os.X_OK):
            raise ValueError(f"checker bundle dispatcher is not executable: {artifact_path}")
    return {
        "manifest_sha256": _sha256_file(path),
        "bundle_id": str(payload["bundle_id"]),
        "artifacts": artifact_integrity,
    }


def _verify_bundle_integrity(path: Path, expected: Mapping[str, Any]) -> None:
    actual = _bundle_integrity(path)
    if actual != dict(expected):
        raise ValueError(
            f"checker bundle integrity changed: expected {dict(expected)!r}, got {actual!r}"
        )


def _validate_part_iii_provenance(
    summary: Mapping[str, Any],
    *,
    bundle_path: Path,
    bundle_integrity: Mapping[str, Any],
    toolchains_path: Path,
    toolchains_sha256: str,
) -> None:
    provenance = summary.get("checker_bundle")
    if not isinstance(provenance, Mapping):
        raise ValueError("Part III summary has no checker_bundle provenance")
    expected = {
        "bundle_id": bundle_integrity.get("bundle_id"),
        "manifest_sha256": bundle_integrity.get("manifest_sha256"),
        "toolchains_config_sha256": toolchains_sha256,
    }
    for key, value in expected.items():
        if provenance.get(key) != value:
            raise ValueError(
                f"Part III checker_bundle provenance {key} mismatch: "
                f"expected {value!r}, got {provenance.get(key)!r}"
            )
    expected_paths = {
        "manifest_path": bundle_path.resolve(strict=True),
        "toolchains_config": toolchains_path.resolve(strict=True),
    }
    for key, value in expected_paths.items():
        raw = provenance.get(key)
        if not isinstance(raw, str) or Path(raw).expanduser().resolve(strict=False) != value:
            raise ValueError(
                f"Part III checker_bundle provenance {key} mismatch: expected {value}, got {raw!r}"
            )


def _write_stage_result(path: Path, result: StageResult) -> ArtifactRef:
    _atomic_write_json(path, result.to_dict())
    return ArtifactRef.from_path(path, base_dir=path.parent.parent, kind="stage-result")


def _make_record(
    *,
    stage: str,
    execution_status: ExecutionStatus,
    result_valid: bool,
    continuation_ready: bool,
    outcome: str,
    previous_chain: str,
    result_ref: ArtifactRef | None = None,
    required_artifact: ArtifactRef | None = None,
    inputs: Sequence[ArtifactRef] = (),
    error: str | None = None,
    metadata: Mapping[str, Any] | None = None,
) -> PipelineStageRecord:
    values: dict[str, Any] = {
        "stage": stage,
        "execution_status": execution_status,
        "result_valid": result_valid,
        "continuation_ready": continuation_ready,
        "outcome": outcome,
        "result_path": result_ref.path if result_ref else None,
        "result_sha256": result_ref.sha256 if result_ref else None,
        "required_artifact": required_artifact,
        "input_artifacts": list(inputs),
        "chain_sha256": "",
        "error": error,
        "metadata": dict(metadata or {}),
    }
    unchained = PipelineStageRecord.model_validate(values)
    serialized = unchained.model_dump(mode="json", exclude_none=True)
    return unchained.model_copy(update={"chain_sha256": _chain_hash(previous_chain, serialized)})


def _skipped_record(stage: str, previous_chain: str, blocker: str) -> PipelineStageRecord:
    return _make_record(
        stage=stage,
        execution_status="skipped",
        result_valid=False,
        continuation_ready=False,
        outcome="blocked",
        previous_chain=previous_chain,
        error=blocker,
        metadata={
            "metrics": {},
            "token_usage": {
                "records": 0,
                "consumed_total_tokens": 0,
                "usage_missing_count": 0,
                "llm_latency_ms": 0.0,
                "elapsed_ms": 0.0,
            },
        },
    )


def _lane_id(target: PipelineTarget, variant: VariantName, repetition: int) -> str:
    return f"{target.id}-{variant}-r{repetition:03d}"


def _lane_dir(
    run_root: Path, target: PipelineTarget, variant: VariantName, repetition: int
) -> Path:
    return run_root / "lanes" / target.id / variant / f"rep-{repetition:03d}"


def _reset_incomplete_lane(lane_dir: Path) -> None:
    """Remove only generated lane state before retrying an incomplete lane."""

    if not lane_dir.exists():
        return
    for stage in _STAGES:
        stage_dir = lane_dir / stage
        if stage_dir.exists():
            shutil.rmtree(stage_dir)
    (lane_dir / "manifest.json").unlink(missing_ok=True)


def _audit_variant_for_campaign(variant: VariantName) -> VariantName:
    return "full" if variant == "without-rag" else variant


def _generation_path_for_campaign(config: PipelineConfig, variant: VariantName) -> GenerationPath:
    return "segmented-cot" if variant == "without-rag" else config.generation.path


def _stage_plan(
    config: PipelineConfig,
    target: PipelineTarget,
    variant: VariantName,
    repetition: int,
    stage: str,
    lane_dir: Path,
    *,
    accepted_invariants: Path | None = None,
    checker_bundle_manifest: Path | None = None,
) -> ExperimentPlan:
    budget = config.budgets.for_stage(stage)
    toolchains = _toolchains_for(config, target)
    findings_deny_path = config.generation.reference_root / "findings"
    common: dict[str, Any] = {
        "backend": config.backend.kind,
        "agent_binary": config.backend.selected_binary,
        "model": _backend_model(config.backend),
        "compiler": target.compiler,
        "version": target.version,
        "toolchains_config": str(toolchains),
        "pipeline_target_id": target.id,
        "pipeline_variant": variant,
        "pipeline_repetition": repetition,
        "findings_deny_path": str(findings_deny_path),
        # Every formal agent stage must be unable to reach the evaluator-only
        # findings corpus through an absolute host path.
        "require_host_read_isolation": config.mode == "formal",
    }
    if stage == "part_i":
        common.update(
            {
                "generation_path": _generation_path_for_campaign(config, variant),
                "corpus_root": str(target.corpus_root),
                "rag_corpus_root": str(target.rag_corpus_root or target.corpus_root),
                "reference_root": str(config.generation.reference_root),
                "document_roots": [str(path) for path in config.generation.document_roots],
                "max_segments": config.generation.max_segments,
                "shard_index": config.generation.shard_index,
                "shard_count": config.generation.shard_count,
            }
        )
        experiment = "invariant-generation"
        source_root: Path | None = target.corpus_root
    elif stage == "part_ii":
        assert accepted_invariants is not None
        common.update(
            {
                "accepted_invariants": str(accepted_invariants),
                "accepted_invariants_sha256": _sha256_file(accepted_invariants),
                "checker_root": config.checker.checker_root.as_posix(),
                "max_attempts": config.checker.max_attempts,
                "mechanisms": target.mechanisms,
                "isas": target.isas,
            }
        )
        experiment = "checker-authoring"
        source_root = config.checker.source_root
    else:
        assert checker_bundle_manifest is not None
        assert accepted_invariants is not None
        parity_scope = ParityScope(
            toolchains=(target.compiler,),
            version=(target.version,),
            mechanisms=tuple(target.mechanisms),
            isas=tuple(target.isas),
        )
        common.update(
            {
                "accepted_invariants": str(accepted_invariants),
                "accepted_invariants_sha256": _sha256_file(accepted_invariants),
                "checker_bundle_manifest": str(checker_bundle_manifest),
                "checker_bundle_sha256": _sha256_file(checker_bundle_manifest),
                "source_roots": [str(path) for path in target.audit_source_roots],
                "mechanisms": target.mechanisms,
                "isas": target.isas,
                "parity_scope": parity_scope.model_dump(
                    mode="json", exclude_defaults=True
                ),
                "max_concurrency": config.audit.max_concurrency,
                "oracle_rounds": config.audit.oracle_rounds,
                "demo_parity": config.audit.demo_parity,
                "parity_profile": config.audit.parity_profile,
                "parity_threshold": config.audit.parity_threshold,
                "parity_threshold_metric": config.audit.parity_threshold_metric,
                "require_verified_candidates": config.audit.require_verified_candidates,
                "reference_root": str(config.generation.reference_root),
                "campaign_variant": variant,
            }
        )
        experiment = "agent-audit"
        source_root = None
    plan_variant = _audit_variant_for_campaign(variant) if stage == "part_iii" else variant
    return ExperimentPlan(
        schema_version=1,
        run_id=f"{config.run_id}-{target.id}-{variant}-r{repetition:03d}-{stage}",
        experiment=experiment,
        variant=plan_variant,
        repetitions=1,
        budget=budget,
        parameters={key: value for key, value in common.items() if value is not None},
        source_root=source_root,
        output_root=lane_dir / stage,
    )


def _formal_host_isolation_error(stage: str, backend: AgentBackend | None) -> str | None:
    if backend is None:
        return None
    if getattr(backend, "supports_host_read_isolation", False):
        return None
    return (
        f"{stage} requires host read isolation in formal mode, but the selected backend "
        "does not provide an enforced boundary for findings/ reads"
    )


async def _invoke_stage(
    *,
    config: PipelineConfig,
    plan: ExperimentPlan,
    repetition: int,
    stage: str,
    output_dir: Path,
    runner: StageRunner,
    runners: PipelineRunners,
    frozen_http_config: HTTPAgentConfig | None = None,
) -> tuple[StageResult, dict[str, Any]]:
    output_dir.mkdir(parents=True, exist_ok=True)
    stage_root = output_dir.parent
    budget = config.budgets.for_stage(stage)
    sink = TokenUsageSink(
        stage_root / "token_usage.jsonl",
        context=TokenUsageContext(
            run_id=plan.run_id,
            experiment=plan.experiment,
            variant=str(plan.parameters.get("campaign_variant", plan.variant)),
            part=stage.upper(),
            stage=_STAGE_NAMES[stage],
            provider=config.backend.kind,
            model=(
                frozen_http_config.model
                if frozen_http_config is not None
                else _backend_model(config.backend)
            ),
        ),
        token_budget=budget.token_budget,
    )
    if runners.backend_factory is not None:
        raw_backend = runners.backend_factory(config.backend)
    elif runners is _DEFAULT_RUNNERS:
        if config.backend.kind == "http":
            raw_backend = HTTPResponsesAgentBackend(
                frozen_http_config or _http_backend_config(config.backend)
            )
        else:
            raw_backend = ExecAgentBackend(
                binary=config.backend.selected_binary,
                model=_backend_model(config.backend),
                provider=config.backend.kind,
                extra_args=config.backend.extra_args,
            )
    else:
        raw_backend = None
    if config.mode == "formal":
        isolation_error = _formal_host_isolation_error(_STAGE_NAMES[stage], raw_backend)
        if isolation_error is not None:
            return (
                StageResult(
                    stage=_STAGE_NAMES[stage],
                    status="failed",
                    execution_status="failed",
                    result_valid=False,
                    continuation_ready=False,
                    outcome="host-isolation-unavailable",
                    error=isolation_error,
                ),
                {
                    "records": 0,
                    "consumed_total_tokens": 0,
                    "usage_missing_count": 0,
                    "token_comparable": False,
                    "llm_latency_ms": None,
                    "elapsed_ms": 0.0,
                },
            )
    backend = _TokenSinkBackend(raw_backend, sink) if raw_backend is not None else None
    started = time.monotonic()
    try:
        with use_token_usage(sink):
            async with asyncio.timeout(budget.timeout_seconds):
                result = await runner(plan, repetition, output_dir, backend)
    except TimeoutError:
        result = StageResult(
            stage=_STAGE_NAMES[stage],
            status="failed",
            execution_status="failed",
            result_valid=False,
            continuation_ready=False,
            outcome="timeout",
            error=f"wall-clock budget exceeded after {budget.timeout_seconds:g}s",
        )
    except Exception as exc:
        result = StageResult(
            stage=_STAGE_NAMES[stage],
            status="failed",
            execution_status="failed",
            result_valid=False,
            continuation_ready=False,
            outcome="runner-error",
            error=f"{type(exc).__name__}: {exc}",
        )
    rows = sink.finalize(
        json_path=stage_root / "token_usage_summary.json",
        csv_path=stage_root / "token_usage_summary.csv",
    )
    usage_missing = sum(int(row["usage_missing_count"]) for row in rows)
    latencies = [
        float(row["total_latency_ms"]) for row in rows if row.get("total_latency_ms") is not None
    ]
    consumed_tokens = sink.consumed_total_tokens
    budget_overshot = consumed_tokens is not None and consumed_tokens > budget.token_budget
    return result, {
        "records": len(sink.records),
        "consumed_total_tokens": consumed_tokens,
        "usage_missing_count": usage_missing,
        "llm_latency_ms": math.fsum(latencies) if latencies else None,
        "elapsed_ms": max(0.0, (time.monotonic() - started) * 1000.0),
        "token_budget": budget.token_budget,
        "token_budget_overshot": budget_overshot,
        "deterministic_only": bool(result.metadata.get("deterministic_only", False)),
        "token_comparable": (
            (bool(rows) and usage_missing == 0 and not budget_overshot)
            or bool(result.metadata.get("deterministic_only", False))
        ),
    }


def _formal_usage_error(config: PipelineConfig, usage: Mapping[str, Any]) -> str | None:
    if config.mode == "fixture":
        return None
    records = int(usage.get("records", 0))
    usage_missing = int(usage.get("usage_missing_count", 0))
    if records == 0:
        if usage.get("deterministic_only"):
            return None
        return "formal mode requires provider-reported token usage for every stage"
    if usage_missing:
        return (
            "formal mode requires complete provider token usage; "
            f"{usage_missing} call(s) have missing usage"
        )
    if usage.get("token_budget_overshot"):
        return (
            f"formal token budget exceeded: consumed "
            f"{usage.get('consumed_total_tokens')} of {usage.get('token_budget')}"
        )
    if not usage.get("token_comparable"):
        return "formal token usage is not comparable"
    return None


def _initial_manifest(
    target: PipelineTarget, variant: VariantName, repetition: int, plan_hash: str, genesis: str
) -> dict[str, Any]:
    return {
        "schema_version": 1,
        "lane_id": _lane_id(target, variant, repetition),
        "target_id": target.id,
        "variant": variant,
        "compiler": target.compiler,
        "version": target.version,
        "repetition": repetition,
        "plan_sha256": plan_hash,
        "execution_status": "running",
        "result_valid": False,
        "outcome": "pending",
        "chain_genesis_sha256": genesis,
        "chain_sha256": genesis,
        "stages": {},
    }


def _record_stage(
    manifest_path: Path, manifest: dict[str, Any], record: PipelineStageRecord
) -> None:
    manifest.setdefault("stages", {})[record.stage] = record.model_dump(
        mode="json", exclude_none=True
    )
    manifest["chain_sha256"] = record.chain_sha256
    _atomic_write_json(manifest_path, manifest)


def _validate_resume_lane(path: Path, *, plan_hash: str) -> PipelineLaneResult | None:
    if not path.is_file():
        return None
    payload = _read_json(path, "pipeline lane manifest")
    if payload.get("plan_sha256") != plan_hash:
        raise ValueError(f"pipeline lane plan hash mismatch: {path}")
    if payload.get("execution_status") != "completed" or not payload.get("result_valid"):
        return None
    chain = payload.get("chain_genesis_sha256")
    if not isinstance(chain, str):
        raise ValueError(f"pipeline lane has no chain genesis: {path}")
    stages = payload.get("stages")
    if not isinstance(stages, Mapping):
        raise ValueError(f"pipeline lane stages are invalid: {path}")
    for stage in _STAGES:
        raw = stages.get(stage)
        if not isinstance(raw, Mapping):
            raise ValueError(f"pipeline lane is missing {stage}: {path}")
        record = PipelineStageRecord.model_validate(raw)
        if not record.continuation_ready:
            raise ValueError(f"pipeline lane {stage} is not continuation-ready: {path}")
        if record.required_artifact is None:
            raise ValueError(f"pipeline lane {stage} has no required artifact: {path}")
        if record.result_path is None or record.result_sha256 is None:
            raise ValueError(f"pipeline lane {stage} has no stage result hash: {path}")
        result_path = (path.parent / record.result_path).resolve(strict=False)
        try:
            result_path.relative_to(path.parent.resolve())
        except ValueError as exc:
            raise ValueError(f"pipeline lane result escapes lane: {result_path}") from exc
        if not result_path.is_file() or _sha256_file(result_path) != record.result_sha256:
            raise ValueError(f"pipeline lane stage result hash mismatch: {result_path}")
        artifact = path.parent / stage / "artifacts" / record.required_artifact.path
        if not artifact.is_file() or _sha256_file(artifact) != record.required_artifact.sha256:
            raise ValueError(f"pipeline lane artifact hash mismatch: {artifact}")
        if stage == "part_ii":
            expected_bundle = record.metadata.get("bundle_integrity")
            if not isinstance(expected_bundle, Mapping):
                raise ValueError(f"pipeline lane Part II has no bundle integrity: {path}")
            _verify_bundle_integrity(artifact, expected_bundle)
        expected_chain = _chain_hash(chain, raw)
        if record.chain_sha256 != expected_chain:
            raise ValueError(f"pipeline lane hash chain mismatch at {stage}: {path}")
        chain = record.chain_sha256
    if payload.get("chain_sha256") != chain:
        raise ValueError(f"pipeline lane final hash mismatch: {path}")
    return PipelineLaneResult(
        target_id=str(payload["target_id"]),
        repetition=int(payload["repetition"]),
        variant=cast(VariantName, payload.get("variant", "full")),
        execution_status="completed",
        result_valid=True,
        outcome=str(payload["outcome"]),
        lane_dir=path.parent,
        chain_sha256=chain,
        stages={name: PipelineStageRecord.model_validate(stages[name]) for name in _STAGES},
    )


async def _run_lane(
    config: PipelineConfig,
    target: PipelineTarget,
    variant: VariantName,
    repetition: int,
    *,
    run_root: Path,
    plan_hash: str,
    runners: PipelineRunners,
    resume: bool,
    frozen_upstream: _FrozenUpstream | None = None,
    require_full_upstream: bool = False,
    frozen_http_config: HTTPAgentConfig | None = None,
) -> PipelineLaneResult:
    lane_dir = _lane_dir(run_root, target, variant, repetition)
    manifest_path = lane_dir / "manifest.json"
    if resume:
        completed = _validate_resume_lane(manifest_path, plan_hash=plan_hash)
        if completed is not None:
            return completed
        _reset_incomplete_lane(lane_dir)
    lane_dir.mkdir(parents=True, exist_ok=True)
    genesis = _canonical_sha256(
        {
            "plan_sha256": plan_hash,
            "target_id": target.id,
            "variant": variant,
            "repetition": repetition,
        }
    )
    manifest = _initial_manifest(target, variant, repetition, plan_hash, genesis)
    _atomic_write_json(manifest_path, manifest)
    records: dict[str, PipelineStageRecord] = {}
    chain = genesis

    if require_full_upstream and frozen_upstream is None:
        blocker = "blocked by missing valid full upstream lane"
        for stage in _STAGES:
            record = _skipped_record(stage, chain, blocker)
            records[stage] = record
            chain = record.chain_sha256
            _record_stage(manifest_path, manifest, record)
        return _finish_lane(manifest_path, manifest, lane_dir, records, chain, False, "blocked")

    part_i_dir = lane_dir / "part_i"
    part_ii_dir = lane_dir / "part_ii"
    if frozen_upstream is None:
        part_i_plan = _stage_plan(config, target, variant, repetition, "part_i", lane_dir)
        result_i, usage_i = await _invoke_stage(
            config=config,
            plan=part_i_plan,
            repetition=repetition,
            stage="part_i",
            output_dir=part_i_dir / "artifacts",
            runner=runners.part_i,
            runners=runners,
            frozen_http_config=frozen_http_config,
        )
        usage_error_i = _formal_usage_error(config, usage_i)
        try:
            invariant_path, invariant_ref = _artifact_from_result(
                result_i, part_i_dir / "artifacts", _REQUIRED_OUTPUTS["part_i"]
            )
            accepted = _count_jsonl(invariant_path)
            valid_i = (
                _result_execution_status(result_i) == "completed"
                and result_i.result_valid is not False
                and result_i.continuation_ready is not False
                and accepted > 0
                and usage_error_i is None
            )
            error_i = (
                None
                if valid_i
                else result_i.error or usage_error_i or "Part I produced zero invariants"
            )
        except (OSError, ValueError) as exc:
            invariant_path = part_i_dir / "artifacts" / _REQUIRED_OUTPUTS["part_i"]
            invariant_ref = None
            accepted = 0
            valid_i = False
            error_i = str(exc)
        enhanced_i = result_i.model_copy(
            update={
                "execution_status": _result_execution_status(result_i),
                "result_valid": valid_i,
                "continuation_ready": valid_i,
                "outcome": "invariants-produced" if valid_i else "no-valid-invariants",
            }
        )
        result_i_ref = _write_stage_result(part_i_dir / "result.json", enhanced_i)
        record_i = _make_record(
            stage="part_i",
            execution_status=_result_execution_status(result_i),
            result_valid=valid_i,
            continuation_ready=valid_i,
            outcome="invariants-produced" if valid_i else "no-valid-invariants",
            previous_chain=chain,
            result_ref=result_i_ref,
            required_artifact=invariant_ref,
            error=error_i,
            metadata={
                "accepted_invariants": accepted,
                "generation_path": _generation_path_for_campaign(config, variant),
                "metrics": result_i.metrics,
                "token_usage": usage_i,
            },
        )
        records["part_i"] = record_i
        chain = record_i.chain_sha256
        _record_stage(manifest_path, manifest, record_i)
        if not valid_i:
            for stage in ("part_ii", "part_iii"):
                record = _skipped_record(stage, chain, "blocked by invalid Part I handoff")
                records[stage] = record
                chain = record.chain_sha256
                _record_stage(manifest_path, manifest, record)
            return _finish_lane(manifest_path, manifest, lane_dir, records, chain, False, "blocked")

        frozen_invariant_hash = _sha256_file(invariant_path)
        part_ii_plan = _stage_plan(
            config,
            target,
            variant,
            repetition,
            "part_ii",
            lane_dir,
            accepted_invariants=invariant_path,
        )
        result_ii, usage_ii = await _invoke_stage(
            config=config,
            plan=part_ii_plan,
            repetition=repetition,
            stage="part_ii",
            output_dir=part_ii_dir / "artifacts",
            runner=runners.part_ii,
            runners=runners,
            frozen_http_config=frozen_http_config,
        )
        usage_error_ii = _formal_usage_error(config, usage_ii)
        try:
            if _sha256_file(invariant_path) != frozen_invariant_hash:
                raise ValueError("Part II mutated the frozen Part I artifact")
            bundle_path, bundle_ref = _artifact_from_result(
                result_ii, part_ii_dir / "artifacts", _REQUIRED_OUTPUTS["part_ii"]
            )
            bundle = _validate_bundle_manifest(bundle_path)
            bundle_integrity = _bundle_integrity(bundle_path)
            valid_ii = (
                _result_execution_status(result_ii) == "completed"
                and result_ii.result_valid is not False
                and result_ii.continuation_ready is not False
                and usage_error_ii is None
            )
            error_ii = (
                None
                if valid_ii
                else result_ii.error or usage_error_ii or "Part II execution failed"
            )
        except (OSError, ValueError) as exc:
            bundle_path = part_ii_dir / "artifacts" / _REQUIRED_OUTPUTS["part_ii"]
            bundle_ref = None
            bundle = {}
            bundle_integrity = {}
            valid_ii = False
            error_ii = str(exc)
        enhanced_ii = result_ii.model_copy(
            update={
                "execution_status": _result_execution_status(result_ii),
                "result_valid": valid_ii,
                "continuation_ready": valid_ii,
                "outcome": "checker-bundle-ready" if valid_ii else "checker-bundle-invalid",
            }
        )
        result_ii_ref = _write_stage_result(part_ii_dir / "result.json", enhanced_ii)
        record_ii = _make_record(
            stage="part_ii",
            execution_status=_result_execution_status(result_ii),
            result_valid=valid_ii,
            continuation_ready=valid_ii,
            outcome="checker-bundle-ready" if valid_ii else "checker-bundle-invalid",
            previous_chain=chain,
            result_ref=result_ii_ref,
            required_artifact=bundle_ref,
            inputs=[cast(ArtifactRef, invariant_ref)],
            error=error_ii,
            metadata={
                "bundle_id": bundle.get("bundle_id"),
                "coverage_complete": bundle.get("coverage_complete"),
                "included_invariant_ids": bundle.get("included_invariant_ids", []),
                "failed_invariant_ids": bundle.get("failed_invariant_ids", []),
                "bundle_integrity": bundle_integrity,
                "metrics": result_ii.metrics,
                "token_usage": usage_ii,
            },
        )
        records["part_ii"] = record_ii
        chain = record_ii.chain_sha256
        _record_stage(manifest_path, manifest, record_ii)
        if not valid_ii:
            record = _skipped_record("part_iii", chain, "blocked by invalid Part II handoff")
            records["part_iii"] = record
            chain = record.chain_sha256
            _record_stage(manifest_path, manifest, record)
            return _finish_lane(manifest_path, manifest, lane_dir, records, chain, False, "blocked")
    else:
        part_i_artifacts = part_i_dir / "artifacts"
        part_i_artifacts.mkdir(parents=True, exist_ok=True)
        invariant_path = part_i_artifacts / _REQUIRED_OUTPUTS["part_i"]
        shutil.copy2(frozen_upstream.invariant_path, invariant_path)
        invariant_ref = ArtifactRef.from_path(
            invariant_path, base_dir=part_i_artifacts, kind=frozen_upstream.invariant_ref.kind
        )
        accepted = _count_jsonl(invariant_path)
        enhanced_i = StageResult(
            stage=_STAGE_NAMES["part_i"],
            status="completed",
            execution_status="completed",
            result_valid=True,
            continuation_ready=True,
            outcome="reused-frozen-upstream",
            artifacts=[invariant_ref],
            metadata={"campaign_variant": variant, "reused_from_variant": "full"},
        )
        result_i_ref = _write_stage_result(part_i_dir / "result.json", enhanced_i)
        record_i = _make_record(
            stage="part_i",
            execution_status="completed",
            result_valid=True,
            continuation_ready=True,
            outcome="reused-frozen-upstream",
            previous_chain=chain,
            result_ref=result_i_ref,
            required_artifact=invariant_ref,
            metadata={
                "accepted_invariants": accepted,
                "generation_path": "combined",
                "reused_from_variant": "full",
                "metrics": {"accepted_invariants": accepted},
                "token_usage": {
                    "reused": True,
                    "records": 0,
                    "consumed_total_tokens": 0,
                    "usage_missing_count": 0,
                    "llm_latency_ms": 0.0,
                    "elapsed_ms": 0.0,
                },
            },
        )
        records["part_i"] = record_i
        chain = record_i.chain_sha256
        _record_stage(manifest_path, manifest, record_i)

        part_ii_artifacts = part_ii_dir / "artifacts"
        part_ii_artifacts.mkdir(parents=True, exist_ok=True)
        for artifact in frozen_upstream.bundle_payload.get("artifacts", {}).values():
            if isinstance(artifact, Mapping) and isinstance(artifact.get("path"), str):
                source = frozen_upstream.bundle_path.parent / str(artifact["path"])
                destination = part_ii_artifacts / str(artifact["path"])
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, destination)
        bundle_path = part_ii_artifacts / _REQUIRED_OUTPUTS["part_ii"]
        shutil.copy2(frozen_upstream.bundle_path, bundle_path)
        bundle_ref = ArtifactRef.from_path(
            bundle_path, base_dir=part_ii_artifacts, kind=frozen_upstream.bundle_ref.kind
        )
        bundle = _validate_bundle_manifest(bundle_path)
        bundle_integrity = _bundle_integrity(bundle_path)
        enhanced_ii = StageResult(
            stage=_STAGE_NAMES["part_ii"],
            status="completed",
            execution_status="completed",
            result_valid=True,
            continuation_ready=True,
            outcome="reused-frozen-upstream",
            artifacts=[bundle_ref],
            metadata={"campaign_variant": variant, "reused_from_variant": "full"},
        )
        result_ii_ref = _write_stage_result(part_ii_dir / "result.json", enhanced_ii)
        record_ii = _make_record(
            stage="part_ii",
            execution_status="completed",
            result_valid=True,
            continuation_ready=True,
            outcome="reused-frozen-upstream",
            previous_chain=chain,
            result_ref=result_ii_ref,
            required_artifact=bundle_ref,
            inputs=[invariant_ref],
            metadata={
                "bundle_id": bundle.get("bundle_id"),
                "coverage_complete": bundle.get("coverage_complete"),
                "included_invariant_ids": bundle.get("included_invariant_ids", []),
                "failed_invariant_ids": bundle.get("failed_invariant_ids", []),
                "bundle_integrity": bundle_integrity,
                "reused_from_variant": "full",
                "metrics": {},
                "token_usage": {
                    "reused": True,
                    "records": 0,
                    "consumed_total_tokens": 0,
                    "usage_missing_count": 0,
                    "llm_latency_ms": 0.0,
                    "elapsed_ms": 0.0,
                },
            },
        )
        records["part_ii"] = record_ii
        chain = record_ii.chain_sha256
        _record_stage(manifest_path, manifest, record_ii)

    part_iii_dir = lane_dir / "part_iii"
    frozen_bundle_integrity = _bundle_integrity(bundle_path)
    validated_bundle = load_checker_bundle(bundle_path, require_ready=True)
    if validated_bundle.scoped_invariants is None:
        raise ValueError("ready checker bundle has no scoped_invariants artifact")
    scoped_invariant_path = validated_bundle.scoped_invariants
    toolchains_path = (
        frozen_upstream.toolchains_path
        if frozen_upstream is not None
        else _toolchains_for(config, target).resolve(strict=True)
    )
    frozen_toolchains_hash = (
        frozen_upstream.toolchains_sha256
        if frozen_upstream is not None
        else _sha256_file(toolchains_path)
    )
    part_iii_plan = _stage_plan(
        config,
        target,
        variant,
        repetition,
        "part_iii",
        lane_dir,
        accepted_invariants=scoped_invariant_path,
        checker_bundle_manifest=bundle_path,
    )
    result_iii, usage_iii = await _invoke_stage(
        config=config,
        plan=part_iii_plan,
        repetition=repetition,
        stage="part_iii",
        output_dir=part_iii_dir / "artifacts",
        runner=runners.part_iii,
        runners=runners,
        frozen_http_config=frozen_http_config,
    )
    usage_error_iii = _formal_usage_error(config, usage_iii)
    try:
        _verify_bundle_integrity(bundle_path, frozen_bundle_integrity)
        if _sha256_file(toolchains_path) != frozen_toolchains_hash:
            raise ValueError("Part III mutated the frozen toolchains config")
        summary_path, summary_ref = _artifact_from_result(
            result_iii, part_iii_dir / "artifacts", _REQUIRED_OUTPUTS["part_iii"]
        )
        summary = _read_json(summary_path, "Part III summary")
        if (
            summary.get("execution_status") != "completed"
            or summary.get("execution_completed") is not True
        ):
            raise ValueError("Part III summary does not record completed execution")
        if not isinstance(summary.get("result_valid"), bool):
            raise ValueError("Part III summary has no boolean result_valid")
        if summary.get("continuation_ready") is not summary.get("result_valid"):
            raise ValueError("Part III continuation_ready must equal result_valid")
        if not isinstance(summary.get("outcome"), str) or not summary["outcome"]:
            raise ValueError("Part III summary has no outcome")
        _validate_part_iii_provenance(
            summary,
            bundle_path=bundle_path,
            bundle_integrity=frozen_bundle_integrity,
            toolchains_path=toolchains_path,
            toolchains_sha256=frozen_toolchains_hash,
        )
        valid_iii = bool(
            summary["result_valid"]
            and summary["continuation_ready"]
            and result_iii.result_valid is not False
            and result_iii.continuation_ready is not False
            and usage_error_iii is None
        )
        outcome_iii = str(summary["outcome"])
        error_iii = (
            None
            if valid_iii
            else result_iii.error or usage_error_iii or "Part III result is invalid"
        )
    except (OSError, ValueError) as exc:
        summary_ref = None
        valid_iii = False
        outcome_iii = "invalid"
        error_iii = str(exc)
    enhanced_iii = result_iii.model_copy(
        update={
            "execution_status": _result_execution_status(result_iii),
            "result_valid": valid_iii,
            "continuation_ready": valid_iii,
            "outcome": outcome_iii,
        }
    )
    result_iii_ref = _write_stage_result(part_iii_dir / "result.json", enhanced_iii)
    record_iii = _make_record(
        stage="part_iii",
        execution_status=_result_execution_status(result_iii),
        result_valid=valid_iii,
        continuation_ready=valid_iii,
        outcome=outcome_iii,
        previous_chain=chain,
        result_ref=result_iii_ref,
        required_artifact=summary_ref,
        inputs=[cast(ArtifactRef, bundle_ref)],
        error=error_iii,
        metadata={
            "campaign_variant": variant,
            "metrics": result_iii.metrics,
            "token_usage": usage_iii,
        },
    )
    records["part_iii"] = record_iii
    chain = record_iii.chain_sha256
    _record_stage(manifest_path, manifest, record_iii)
    return _finish_lane(manifest_path, manifest, lane_dir, records, chain, valid_iii, outcome_iii)


def _finish_lane(
    manifest_path: Path,
    manifest: dict[str, Any],
    lane_dir: Path,
    records: dict[str, PipelineStageRecord],
    chain: str,
    result_valid: bool,
    outcome: str,
) -> PipelineLaneResult:
    manifest.update(
        {
            "execution_status": "completed",
            "result_valid": result_valid,
            "outcome": outcome,
            "chain_sha256": chain,
        }
    )
    _atomic_write_json(manifest_path, manifest)
    return PipelineLaneResult(
        target_id=str(manifest["target_id"]),
        repetition=int(manifest["repetition"]),
        variant=cast(VariantName, manifest.get("variant", "full")),
        execution_status="completed",
        result_valid=result_valid,
        outcome=outcome,
        lane_dir=lane_dir,
        chain_sha256=chain,
        stages=records,
    )


def _freeze_upstream(
    lane: PipelineLaneResult, *, config: PipelineConfig, target: PipelineTarget
) -> _FrozenUpstream:
    record_i = lane.stages["part_i"]
    record_ii = lane.stages["part_ii"]
    if record_i.required_artifact is None or record_ii.required_artifact is None:
        raise ValueError("full upstream lane is missing frozen artifacts")
    invariant_path = (
        lane.lane_dir / "part_i" / "artifacts" / record_i.required_artifact.path
    ).resolve(strict=True)
    bundle_path = (
        lane.lane_dir / "part_ii" / "artifacts" / record_ii.required_artifact.path
    ).resolve(strict=True)
    bundle_payload = _validate_bundle_manifest(bundle_path)
    bundle_integrity = _bundle_integrity(bundle_path)
    toolchains_path = _toolchains_for(config, target).resolve(strict=True)
    return _FrozenUpstream(
        invariant_path=invariant_path,
        invariant_ref=record_i.required_artifact,
        bundle_path=bundle_path,
        bundle_ref=record_ii.required_artifact,
        bundle_payload=bundle_payload,
        bundle_integrity=bundle_integrity,
        toolchains_path=toolchains_path,
        toolchains_sha256=_sha256_file(toolchains_path),
    )


def _aggregate_outcome(lanes: Sequence[PipelineLaneResult]) -> str:
    valid = [lane for lane in lanes if lane.result_valid]
    if len(valid) != len(lanes):
        return "partial" if valid else "blocked"
    positive_markers = {"positive", "verified-findings", "findings-verified"}
    return "positive" if any(lane.outcome in positive_markers for lane in lanes) else "negative"


_DEFAULT_RUNNERS = PipelineRunners.default()
_FIXTURE_RUNNERS = PipelineRunners.fixture_smoke()


async def run_pipeline(
    config: PipelineConfig,
    *,
    runners: PipelineRunners | None = None,
    resume: bool = False,
    config_path: str | os.PathLike[str] | None = None,
) -> PipelineRunResult:
    """Execute every ``(target, variant, repetition)`` lane through Parts I--III."""

    plan = build_pipeline_plan(config, config_path=config_path)
    frozen_http_config: HTTPAgentConfig | None = None
    if config.backend.kind == "http":
        assert config.backend.config_path is not None
        expected_config_hash = str(
            plan["backend_identity"]["config_file"]["sha256"]
        )
        if _sha256_file(config.backend.config_path) != expected_config_hash:
            raise ValueError(
                "HTTP agent config changed after pipeline planning; restart with a new run_id"
            )
        frozen_http_config, loaded_hash, _ = load_http_agent_config_snapshot(
            config.backend.config_path
        )
        if loaded_hash != expected_config_hash:
            raise ValueError(
                "HTTP agent config changed while it was being frozen; restart the campaign"
            )
        # Programmatic callers may construct PipelineConfig directly instead
        # of going through load_pipeline_config(). Freeze the resolved model in
        # the in-memory campaign config so stage plans never need to reread the
        # HTTP file after this point.
        config = config.model_copy(
            update={
                "backend": config.backend.model_copy(
                    update={"model": frozen_http_config.model}
                )
            }
        )
    selected_runners = runners or (
        _FIXTURE_RUNNERS if config.mode == "fixture" else _DEFAULT_RUNNERS
    )
    if (
        selected_runners is _DEFAULT_RUNNERS
        and config.backend.kind != "http"
        and shutil.which(config.backend.selected_binary) is None
    ):
        raise ValueError(f"agent binary is unavailable: {config.backend.selected_binary}")
    run_root = config.output_root / config.run_id
    plan_path = run_root / "plan.json"
    manifest_path = run_root / "manifest.json"
    if resume:
        if not plan_path.is_file() or not manifest_path.is_file():
            raise ValueError(f"cannot resume missing or incomplete pipeline run: {run_root}")
        stored = _read_json(plan_path, "stored pipeline plan")
        if stored.get("plan_sha256") != plan["plan_sha256"]:
            raise ValueError(
                "pipeline plan mismatch: "
                f"stored={stored.get('plan_sha256')!r}, requested={plan['plan_sha256']!r}"
            )
    elif run_root.exists():
        raise ValueError(f"pipeline run already exists: {run_root}; pass --resume to continue it")
    else:
        run_root.mkdir(parents=True)
        _atomic_write_json(plan_path, plan)
        _atomic_write_json(
            manifest_path,
            {
                "schema_version": 1,
                "run_id": config.run_id,
                "plan_sha256": plan["plan_sha256"],
                "execution_status": "running",
                "result_valid": False,
                "outcome": "pending",
                "lanes": [],
            },
        )

    lane_results: list[PipelineLaneResult] = []

    def write_run_manifest() -> None:
        current = _read_json(manifest_path, "pipeline manifest")
        current["lanes"] = [
            result.model_dump(mode="json", exclude={"stages"}) for result in lane_results
        ]
        _atomic_write_json(manifest_path, current)

    for target in config.targets:
        for repetition in range(1, config.repetitions + 1):
            full_upstream: _FrozenUpstream | None = None
            for variant in config.variants:
                lane = await _run_lane(
                    config,
                    target,
                    variant,
                    repetition,
                    run_root=run_root,
                    plan_hash=str(plan["plan_sha256"]),
                    runners=selected_runners,
                    resume=resume,
                    frozen_upstream=(
                        full_upstream if variant in {"without-oracle", "bare-agent"} else None
                    ),
                    require_full_upstream=variant in {"without-oracle", "bare-agent"},
                    frozen_http_config=frozen_http_config,
                )
                lane_results.append(lane)
                write_run_manifest()
                if variant == "full" and lane.result_valid:
                    full_upstream = _freeze_upstream(lane, config=config, target=target)

    result_valid = all(lane.result_valid for lane in lane_results)
    outcome = _aggregate_outcome(lane_results)
    campaign_artifacts = write_campaign_results(run_root, lane_results)
    manifest = _read_json(manifest_path, "pipeline manifest")
    manifest.update(
        {
            "execution_status": "completed",
            "result_valid": result_valid,
            "outcome": outcome,
            "lanes": [lane.model_dump(mode="json", exclude={"stages"}) for lane in lane_results],
            "campaign_artifacts": campaign_artifacts,
        }
    )
    _atomic_write_json(manifest_path, manifest)
    return PipelineRunResult(
        execution_status="completed",
        result_valid=result_valid,
        outcome=outcome,
        manifest_path=manifest_path,
        lanes=lane_results,
        campaign_artifacts=campaign_artifacts,
    )


def run_pipeline_sync(
    config: PipelineConfig,
    *,
    runners: PipelineRunners | None = None,
    resume: bool = False,
    config_path: str | os.PathLike[str] | None = None,
) -> PipelineRunResult:
    return asyncio.run(
        run_pipeline(config, runners=runners, resume=resume, config_path=config_path)
    )


__all__ = [
    "PipelineAuditConfig",
    "PipelineBackendConfig",
    "PipelineBudgets",
    "PipelineCheckerConfig",
    "PipelineConfig",
    "PipelineGenerationConfig",
    "PipelineLaneResult",
    "PipelineRunResult",
    "PipelineRunners",
    "PipelineStageRecord",
    "PipelineTarget",
    "build_pipeline_plan",
    "load_pipeline_config",
    "run_pipeline",
    "run_pipeline_sync",
]
