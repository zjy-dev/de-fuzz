"""Part I experiment runner: Segmented CoT, historical-bug RAG, or both.

Both producers are normalized into ``accepted-invariants.jsonl``.  Exact
statement duplicates are merged deterministically; an invariant found by both
producers has ``generation_path="combined"`` and retains both provenance
chains.  The RAG adapter intentionally accepts historical bug documents only:
demo/new findings are neither probes nor a novelty baseline.
"""

from __future__ import annotations

import hashlib
import inspect
import json
import re
import tempfile
from collections.abc import Awaitable, Callable, Mapping, Sequence
from pathlib import Path
from typing import Any, Literal, TypeVar, cast

from pydantic import BaseModel, ConfigDict, Field, model_validator

from ..audit_schema import normalize_mechanism
from ..specgen.dedup import assess_novelty, build_baseline
from ..specgen.judge import Judge
from ..specgen.pipeline import PipelineConfig, PipelineResult, run_pipeline
from ..specgen.schema import Candidate
from ..token_usage import current_token_usage_sink
from .models import ArtifactRef, ExperimentPlan, StageResult, VariantName
from .segmented import (
    AgentBackend,
    SegmentManifest,
    build_segment_manifest,
    complete_with_backend,
    run_segmented_generation,
    write_segment_manifest,
    write_segment_preflight,
)
from .workspace import validate_agent_path_isolation, validate_disjoint_input_roots

GenerationPath = Literal["rag", "segmented-cot", "combined"]
T = TypeVar("T", bound=BaseModel)
_DEFAULT_REFERENCE_ROOT = Path("/Users/bytedance/projects/research/defend-reviewer/main")
_SPACE = re.compile(r"\s+")
_TARGET_SLUG = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.+-]*$")
_VERSION_SLUG = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:+-]*$")
_RAW_MECHANISM_SLUG = re.compile(r"^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$")
_COMPILER_ALIASES = {
    "gcc": "GCC",
    "gnu-compiler-collection": "GCC",
    "llvm": "LLVM",
    "clang": "LLVM",
}


class InvariantProvenance(BaseModel):
    generation_path: Literal["rag", "segmented-cot"]
    producer_id: str
    source_kind: str
    source_url_or_path: str
    evidence_sha256: str
    evidence_snippet: str
    seed_id: str = ""
    origin_mechanism: str = ""


class AcceptedInvariant(BaseModel):
    """Stable hand-off record consumed by Part II."""

    schema_version: int = 1
    invariant_id: str
    statement: str
    observation: str
    generation_path: GenerationPath
    generation_paths: list[Literal["rag", "segmented-cot"]]
    provenance: list[InvariantProvenance]
    compiler: str = ""
    version: str = ""
    target: str = ""
    mechanism: str = ""
    source_kind: str = ""
    source_url_or_path: str = ""
    evidence_snippet: str = ""
    protected_asset: str = ""
    activation_condition: str = ""
    version_sensitivity: str = "likely-to-drift"
    falsifiability: dict[str, Any] = Field(default_factory=dict)
    grounding: dict[str, Any] | None = None
    novelty: dict[str, Any] | None = None

    @model_validator(mode="after")
    def _validate_semantic_fields(self) -> AcceptedInvariant:
        self.compiler = _normalize_compiler_value(self.compiler)
        self.version = _normalize_version_value(self.version)
        self.target = _normalize_target_value(self.target)
        self.mechanism = _normalize_mechanism_value(self.mechanism)
        return self


class InvariantGenerationConfig(BaseModel):
    """Resolved, replayable Part I configuration for one repetition."""

    model_config = ConfigDict(arbitrary_types_allowed=True)

    generation_path: GenerationPath = "combined"
    corpus_root: Path
    compiler: Literal["gcc", "llvm"] = "gcc"
    version: str = ""
    output_dir: Path
    run_id: str
    campaign_variant: VariantName = "full"
    repetition: int = Field(ge=1)
    token_budget: int = Field(default=100_000, gt=0)
    time_budget_minutes: float = Field(default=60.0, gt=0)
    reference_root: Path = _DEFAULT_REFERENCE_ROOT
    document_roots: list[Path] = Field(default_factory=list)
    bugs_root: Path | None = None
    invariants_root: Path | None = None
    cache_root: Path | None = None
    findings_root: Path | None = None
    findings_deny_path: Path | None = None
    seed_sources: list[str] = Field(default_factory=lambda: ["bugs"])
    retriever: str = "bm25"
    query_mode: str = "abstract"
    top_k: int = Field(default=8, gt=0)
    over_fetch: int = Field(default=4, gt=0)
    dedup_threshold: float = 85.0
    segment_chars: int = Field(default=12_000, gt=0)
    segment_start: int = Field(default=0, ge=0)
    segment_end: int | None = Field(default=None, ge=1)
    shard_index: int = Field(default=0, ge=0)
    shard_count: int = Field(default=1, ge=1)
    max_segments: int | None = Field(default=None, gt=0)
    minimum_segments: int = Field(default=1, gt=0)
    max_concurrency: int = Field(default=1, gt=0)
    include_bugzilla: bool = False
    require_host_read_isolation: bool = False

    @model_validator(mode="after")
    def _enforce_formal_seed_boundary(self) -> InvariantGenerationConfig:
        if self.segment_end is not None and self.segment_end <= self.segment_start:
            raise ValueError("segment_end must be greater than segment_start")
        if self.shard_index >= self.shard_count:
            raise ValueError("shard_index must be in the range [0, shard_count)")
        if self.findings_root is not None:
            raise ValueError("Part I RAG forbids findings_root; use historical docs/bugs only")
        sources = {source.strip().lower() for source in self.seed_sources}
        if sources != {"bugs"}:
            raise ValueError(
                "Part I RAG seed_sources must be exactly ['bugs']; findings are forbidden"
            )
        if self.bugs_root is None:
            self.bugs_root = self.reference_root / "docs" / "bugs" / self.compiler
        if self.invariants_root is None:
            self.invariants_root = self.reference_root / "docs" / "invariants"
        if self.cache_root is None:
            self.cache_root = self.output_dir / "cache"
        if self.findings_deny_path is None:
            self.findings_deny_path = self.reference_root / "findings"
        return self

    @property
    def timeout_seconds(self) -> float:
        return self.time_budget_minutes * 60.0


class InvariantGenerationResult(BaseModel):
    config: dict[str, Any]
    accepted: list[AcceptedInvariant] = Field(default_factory=list)
    rag_candidates: int = 0
    segmented_candidates: int = 0
    overlap: int = 0
    execution_status: Literal["completed"] = "completed"
    result_valid: bool = False
    continuation_ready: bool = False
    segment_count: int = 0
    selected_segment_count: int = 0
    input_chars: int = 0
    selection_complete: bool = True
    selection_warning: str | None = None
    minimum_warning: str | None = None
    segment_manifest: SegmentManifest | None = None
    artifacts: list[Path] = Field(default_factory=list)


class BackendJudge(Judge):
    """Adapt the shared external AgentBackend to the existing specgen Judge."""

    def __init__(
        self,
        backend: AgentBackend,
        *,
        cwd: Path,
        output_dir: Path,
        timeout_seconds: float,
        token_sink: Any = None,
        context: Mapping[str, Any] | None = None,
        deny_read_paths: Sequence[Path] = (),
        require_host_read_isolation: bool = False,
    ) -> None:
        self.backend = backend
        self.cwd = cwd
        self.output_dir = output_dir
        self.timeout_seconds = timeout_seconds
        self.token_sink = token_sink
        self.context = dict(context or {})
        self.deny_read_paths = list(deny_read_paths)
        self.require_host_read_isolation = require_host_read_isolation

    async def complete(
        self, *, task: str, key: str, system: str, user: str, output_model: type[T]
    ) -> T:
        return await complete_with_backend(
            self.backend,
            prompt=f"SYSTEM:\n{system}\n\nUSER:\n{user}",
            output_model=output_model,
            cwd=self.cwd,
            output_dir=self.output_dir,
            timeout_seconds=self.timeout_seconds,
            token_sink=self.token_sink,
            deny_read_paths=self.deny_read_paths,
            require_host_read_isolation=self.require_host_read_isolation,
            metadata={**self.context, "stage": task, "judgment_key": key},
        )


RagRunner = Callable[..., PipelineResult | Awaitable[PipelineResult]]


def _canonical_statement(statement: str) -> str:
    return _SPACE.sub(" ", statement).strip().casefold()


def _normalize_compiler_value(value: str) -> str:
    stripped = value.strip()
    if not stripped:
        return ""
    normalized = normalize_mechanism(stripped)
    canonical = _COMPILER_ALIASES.get(normalized)
    if canonical is None:
        raise ValueError("compiler must be GCC/LLVM or empty")
    return canonical


def _normalize_target_value(value: str) -> str:
    stripped = value.strip()
    if not stripped:
        return ""
    if stripped.casefold() == "generic":
        raise ValueError("target must be explicit or empty; do not use generic")
    if not _TARGET_SLUG.fullmatch(stripped):
        raise ValueError("target must be a compact slug or empty")
    return stripped


def _normalize_version_value(value: str) -> str:
    stripped = value.strip()
    if not stripped:
        return ""
    if not _VERSION_SLUG.fullmatch(stripped):
        raise ValueError("version must be a compact string or empty")
    return stripped


def _normalize_mechanism_value(value: str) -> str:
    stripped = value.strip()
    if not _RAW_MECHANISM_SLUG.fullmatch(stripped):
        raise ValueError("mechanism must be a compact slug before normalization")
    canonical = normalize_mechanism(stripped)
    if not canonical:
        raise ValueError("mechanism must be non-empty")
    if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", canonical):
        raise ValueError("mechanism must normalize to a compact slug")
    if len(canonical.split("-")) > 6:
        raise ValueError("mechanism must be a concise canonical slug")
    return canonical


def _content_id(statement: str) -> str:
    digest = hashlib.sha256(_canonical_statement(statement).encode("utf-8")).hexdigest()
    return f"INVGEN-{digest[:16].upper()}"


def _provenance(candidate: Candidate, path: Literal["rag", "segmented-cot"]) -> InvariantProvenance:
    evidence = candidate.evidence_snippet
    return InvariantProvenance(
        generation_path=path,
        producer_id=candidate.chunk_id or candidate.seed_id,
        source_kind=candidate.source_kind,
        source_url_or_path=candidate.source_url_or_path,
        evidence_sha256=hashlib.sha256(evidence.encode("utf-8")).hexdigest(),
        evidence_snippet=evidence,
        seed_id=candidate.seed_id,
        origin_mechanism=candidate.origin_mechanism,
    )


def _normalize_candidate(
    candidate: Candidate, path: Literal["rag", "segmented-cot"]
) -> AcceptedInvariant:
    return AcceptedInvariant(
        invariant_id=_content_id(candidate.statement),
        statement=_SPACE.sub(" ", candidate.statement).strip(),
        observation=_SPACE.sub(" ", candidate.observation).strip(),
        generation_path=path,
        generation_paths=[path],
        provenance=[_provenance(candidate, path)],
        compiler=candidate.compiler,
        version=candidate.version,
        target=candidate.target,
        mechanism=candidate.hit_mechanism,
        source_kind=candidate.source_kind,
        source_url_or_path=candidate.source_url_or_path,
        evidence_snippet=candidate.evidence_snippet,
        protected_asset=str(getattr(candidate, "protected_asset", "")),
        activation_condition=str(getattr(candidate, "activation_condition", "")),
        version_sensitivity=candidate.version_sensitivity,
        falsifiability=candidate.falsifiability.model_dump(mode="json"),
        grounding=(candidate.grounding.model_dump(mode="json") if candidate.grounding else None),
        novelty=(candidate.novelty.model_dump(mode="json") if candidate.novelty else None),
    )


def merge_accepted_invariants(records: Sequence[AcceptedInvariant]) -> list[AcceptedInvariant]:
    """Deduplicate by normalized statement and retain every producer provenance."""

    merged: dict[str, AcceptedInvariant] = {}
    for incoming in sorted(records, key=lambda item: (item.invariant_id, item.generation_path)):
        key = _canonical_statement(incoming.statement)
        current = merged.get(key)
        if current is None:
            merged[key] = incoming.model_copy(deep=True)
            continue
        paths = sorted(set(current.generation_paths + incoming.generation_paths))
        current.generation_paths = paths  # type: ignore[assignment]
        current.generation_path = "combined" if len(paths) > 1 else paths[0]
        by_key = {
            (
                item.generation_path,
                item.producer_id,
                item.source_url_or_path,
                item.evidence_sha256,
            ): item
            for item in current.provenance + incoming.provenance
        }
        current.provenance = [by_key[key] for key in sorted(by_key)]
    return sorted(merged.values(), key=lambda item: item.invariant_id)


def _resolved_corpus_root(config: InvariantGenerationConfig) -> Path:
    root = config.corpus_root.expanduser().resolve()
    if config.compiler == "gcc" and not (root / "tree-object-size.cc").exists():
        nested = root / "gcc"
        if nested.is_dir():
            return nested
    return root


def _agent_deny_paths(config: InvariantGenerationConfig) -> list[Path]:
    """Return every original Part I input tree hidden from model processes."""

    roots = [
        config.corpus_root,
        config.reference_root,
        _resolved_corpus_root(config),
        *config.document_roots,
    ]
    findings = config.findings_deny_path or (config.reference_root / "findings")
    roots.append(findings)
    return list(
        dict.fromkeys(path.expanduser().resolve(strict=False) for path in roots)
    )


async def _call_rag_runner(
    runner: RagRunner, config: PipelineConfig, judge: Judge | None
) -> PipelineResult:
    kwargs: dict[str, Any] = {}
    try:
        signature = inspect.signature(runner)
        accepts_kwargs = any(
            parameter.kind == inspect.Parameter.VAR_KEYWORD
            for parameter in signature.parameters.values()
        )
        if judge is not None and (accepts_kwargs or "judge_override" in signature.parameters):
            kwargs["judge_override"] = judge
    except (TypeError, ValueError):
        if judge is not None:
            kwargs["judge_override"] = judge
    result = runner(config, **kwargs)
    if inspect.isawaitable(result):
        result = await result
    return result


async def _run_rag(
    config: InvariantGenerationConfig,
    *,
    judge: Judge | None,
    rag_runner: RagRunner,
) -> list[Candidate]:
    rag_out = config.output_dir / "rag"
    pipeline_config = PipelineConfig(
        seed_sources=["bugs"],
        corpus_root=_resolved_corpus_root(config),
        compiler=config.compiler,
        version=config.version or None,
        findings_root=None,
        bugs_root=config.bugs_root,
        invariants_root=config.invariants_root,
        out_dir=rag_out,
        cache_root=config.cache_root or config.output_dir / "cache",
        top_k=config.top_k,
        over_fetch=config.over_fetch,
        dedup_threshold=config.dedup_threshold,
        include_bugzilla=config.include_bugzilla,
        retriever=config.retriever,
        query_mode=config.query_mode,
        require_non_empty_corpus=True,
    )
    result = await _call_rag_runner(rag_runner, pipeline_config, judge)
    if result.corpus_size <= 0:
        raise ValueError(
            f"empty {config.compiler} retrieval corpus at {_resolved_corpus_root(config)}"
        )
    return [
        candidate
        for candidate in result.accepted
        if candidate.novelty is None or candidate.novelty.is_novel
    ]


def _write_jsonl(path: Path, records: Sequence[AcceptedInvariant]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    content = "".join(record.model_dump_json() + "\n" for record in records)
    path.write_text(content, encoding="utf-8")


def _config_dump(config: InvariantGenerationConfig) -> dict[str, Any]:
    return config.model_dump(mode="json", exclude={"findings_root"})


async def _run_invariant_generation(
    config: InvariantGenerationConfig,
    *,
    agent_cwd: Path,
    backend: AgentBackend | None = None,
    rag_runner: RagRunner | None = None,
    grounding_judge: Judge | None = None,
    token_sink: Any = None,
) -> InvariantGenerationResult:
    """Execute one Part I repetition and write stable hand-off artifacts."""

    if token_sink is None:
        token_sink = current_token_usage_sink()
    config.output_dir.mkdir(parents=True, exist_ok=True)
    context = {
        "run_id": config.run_id,
        "experiment": "invariant-generation",
        "variant": config.campaign_variant,
        "part": "part-i",
        "repetition": config.repetition,
        "token_budget": config.token_budget,
    }
    judge = grounding_judge
    if judge is None and backend is not None:
        judge = BackendJudge(
            backend,
            cwd=agent_cwd,
            output_dir=config.output_dir,
            timeout_seconds=config.timeout_seconds,
            token_sink=token_sink,
            context=context,
            deny_read_paths=_agent_deny_paths(config),
            require_host_read_isolation=config.require_host_read_isolation,
        )

    records: list[AcceptedInvariant] = []
    rag_count = 0
    segmented_count = 0
    segment_manifest: SegmentManifest | None = None
    artifacts: list[Path] = []
    reviewed_segments = 0
    effective_concurrency = 0

    if config.generation_path in {"segmented-cot", "combined"}:
        if backend is None:
            raise ValueError("segmented-cot generation requires an AgentBackend")
        if judge is None:
            raise ValueError("segmented-cot generation requires a grounding Judge")
        segment_manifest = build_segment_manifest(
            _resolved_corpus_root(config),
            document_roots=config.document_roots,
            compiler=config.compiler,
            segment_chars=config.segment_chars,
            segment_start=config.segment_start,
            segment_end=config.segment_end,
            shard_index=config.shard_index,
            shard_count=config.shard_count,
            max_segments=config.max_segments,
            minimum_segments=config.minimum_segments,
        )
        if not segment_manifest.segments:
            raise ValueError(
                f"empty {config.compiler} segmented corpus at {_resolved_corpus_root(config)}"
            )
        manifest_path = write_segment_manifest(
            segment_manifest, config.output_dir / "segment-manifest.json"
        )
        artifacts.append(manifest_path)
        preflight_path = write_segment_preflight(
            segment_manifest, config.output_dir / "segment-preflight.json"
        )
        artifacts.append(preflight_path)

    if config.generation_path in {"rag", "combined"}:
        rag_candidates = await _run_rag(
            config, judge=judge, rag_runner=rag_runner or run_pipeline
        )
        rag_count = len(rag_candidates)
        records.extend(_normalize_candidate(candidate, "rag") for candidate in rag_candidates)

    if config.generation_path in {"segmented-cot", "combined"}:
        assert backend is not None
        assert judge is not None
        assert segment_manifest is not None
        segmented = await run_segmented_generation(
            segment_manifest,
            backend=backend,
            judge=judge,
            output_dir=config.output_dir / "segmented-cot",
            cwd=agent_cwd,
            timeout_seconds=config.timeout_seconds,
            token_sink=token_sink,
            run_context=context,
            max_concurrency=config.max_concurrency,
            deny_read_paths=_agent_deny_paths(config),
            require_host_read_isolation=config.require_host_read_isolation,
        )
        reviewed_segments = segmented.reviewed_segments
        effective_concurrency = segmented.effective_concurrency
        segmented_for_novelty: list[Candidate] = list(segmented.candidates)
        assess_novelty(
            build_baseline(invariants_root=config.invariants_root, findings_root=None),
            segmented_for_novelty,
            threshold=config.dedup_threshold,
        )
        segmented_candidates = [
            candidate
            for candidate in segmented.candidates
            if candidate.novelty is None or candidate.novelty.is_novel
        ]
        segmented_count = len(segmented_candidates)
        records.extend(
            _normalize_candidate(candidate, "segmented-cot")
            for candidate in segmented_candidates
        )

    accepted = merge_accepted_invariants(records)
    result_valid = bool(accepted)
    continuation_ready = result_valid
    overlap = sum(record.generation_path == "combined" for record in accepted)
    accepted_path = config.output_dir / "accepted-invariants.jsonl"
    _write_jsonl(accepted_path, accepted)
    artifacts.append(accepted_path)

    manifest_path = config.output_dir / "invariant-generation-manifest.json"
    preflight = segment_manifest.preflight if segment_manifest is not None else None
    selection = (
        segment_manifest.segment_selection.model_dump(mode="json")
        if segment_manifest is not None
        else None
    )
    manifest_path.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "run_id": config.run_id,
                "repetition": config.repetition,
                "generation_path": config.generation_path,
                "execution_status": "completed",
                "result_valid": result_valid,
                "continuation_ready": continuation_ready,
                "inputs": _config_dump(config),
                "segment_selection": selection,
                "preflight": preflight.model_dump(mode="json") if preflight else None,
                "metrics": {
                    "rag_candidates": rag_count,
                    "segmented_candidates": segmented_count,
                    "accepted_invariants": len(accepted),
                    "overlap": overlap,
                    "segment_count": preflight.segment_count if preflight else 0,
                    "selected_segment_count": (
                        preflight.selected_segment_count if preflight else 0
                    ),
                    "input_chars": preflight.estimated_input_chars if preflight else 0,
                    "selection_complete": (
                        preflight.selection_complete if preflight else True
                    ),
                    "reviewed_segments": reviewed_segments,
                    "max_concurrency": config.max_concurrency,
                    "effective_concurrency": effective_concurrency,
                    "result_valid": result_valid,
                    "continuation_ready": continuation_ready,
                },
                "artifacts": [path.name for path in artifacts],
            },
            ensure_ascii=False,
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    artifacts.append(manifest_path)
    return InvariantGenerationResult(
        config=_config_dump(config),
        accepted=accepted,
        rag_candidates=rag_count,
        segmented_candidates=segmented_count,
        overlap=overlap,
        result_valid=result_valid,
        continuation_ready=continuation_ready,
        segment_count=preflight.segment_count if preflight else 0,
        selected_segment_count=preflight.selected_segment_count if preflight else 0,
        input_chars=preflight.estimated_input_chars if preflight else 0,
        selection_complete=preflight.selection_complete if preflight else True,
        selection_warning=preflight.selection_warning if preflight else None,
        minimum_warning=preflight.minimum_warning if preflight else None,
        segment_manifest=segment_manifest,
        artifacts=artifacts,
    )


async def run_invariant_generation(
    config: InvariantGenerationConfig,
    *,
    backend: AgentBackend | None = None,
    rag_runner: RagRunner | None = None,
    grounding_judge: Judge | None = None,
    token_sink: Any = None,
) -> InvariantGenerationResult:
    """Execute Part I with model processes rooted in an empty temporary cwd."""

    if (
        config.require_host_read_isolation
        and backend is not None
        and not bool(getattr(backend, "supports_host_read_isolation", False))
    ):
        raise ValueError("Part I requires a backend with host read isolation")
    with tempfile.TemporaryDirectory(prefix="defuzz-part-i-agent-") as temporary:
        agent_cwd = Path(temporary).resolve()
        validate_disjoint_input_roots(
            [
                ("corpus_root", _resolved_corpus_root(config)),
                *(
                    (f"document_roots[{index}]", root)
                    for index, root in enumerate(config.document_roots)
                ),
            ]
        )
        validate_agent_path_isolation(
            cwd=agent_cwd,
            output_dir=config.output_dir,
            deny_read_paths=_agent_deny_paths(config),
        )
        return await _run_invariant_generation(
            config,
            agent_cwd=agent_cwd,
            backend=backend,
            rag_runner=rag_runner,
            grounding_judge=grounding_judge,
            token_sink=token_sink,
        )


def _plan_parameters(plan: ExperimentPlan) -> dict[str, Any]:
    return dict(plan.parameters)


def _default_corpus_root(reference_root: Path, compiler: str) -> Path:
    if compiler == "llvm":
        return reference_root / "compilers" / "llvm-project-main"
    return reference_root / "compilers" / "gcc-17-20260531" / "gcc"


def config_from_plan(
    plan: ExperimentPlan | Mapping[str, Any], repetition: int, output_dir: str | Path
) -> InvariantGenerationConfig:
    normalized = plan if isinstance(plan, ExperimentPlan) else ExperimentPlan.from_mapping(plan)
    params = _plan_parameters(normalized)
    if params.get("findings_root") is not None:
        raise ValueError("Part I rejects findings_root; formal RAG probes must be historical bugs")
    if "findings" in {str(item).lower() for item in params.get("seed_sources", ["bugs"])}:
        raise ValueError("Part I rejects findings in seed_sources")

    reference_root = Path(params.get("reference_root", _DEFAULT_REFERENCE_ROOT))
    compiler = str(params.get("compiler", "gcc"))
    if compiler not in {"gcc", "llvm"}:
        raise ValueError("compiler must be 'gcc' or 'llvm'")
    compiler_name = cast(Literal["gcc", "llvm"], compiler)
    requested: GenerationPath = params.get("generation_path", "combined")
    if not normalized.policy.use_rag and requested in {"rag", "combined"}:
        requested = "segmented-cot"
    campaign_variant = cast(
        VariantName, str(params.get("campaign_variant", normalized.variant))
    )

    raw_docs = params.get("document_roots", params.get("documents_root", []))
    if isinstance(raw_docs, (str, Path)):
        raw_docs = [raw_docs]
    corpus_value = params.get("corpus_root")
    corpus_root = (
        Path(corpus_value)
        if corpus_value is not None
        else _default_corpus_root(reference_root, compiler_name)
    )
    return InvariantGenerationConfig(
        generation_path=requested,
        corpus_root=corpus_root,
        compiler=compiler_name,
        version=str(params.get("version", "")),
        output_dir=Path(output_dir),
        run_id=normalized.run_id,
        campaign_variant=campaign_variant,
        repetition=repetition,
        token_budget=normalized.budget.token_budget,
        time_budget_minutes=normalized.budget.time_budget_minutes,
        reference_root=reference_root,
        document_roots=[Path(path) for path in raw_docs],
        bugs_root=Path(params["bugs_root"]) if params.get("bugs_root") else None,
        invariants_root=(
            Path(params["invariants_root"])
            if params.get("invariants_root")
            else None
        ),
        cache_root=Path(params["cache_root"]) if params.get("cache_root") else None,
        findings_root=None,
        findings_deny_path=(
            Path(params["findings_deny_path"])
            if params.get("findings_deny_path") is not None
            else None
        ),
        seed_sources=list(params.get("seed_sources", ["bugs"])),
        retriever=str(params.get("retriever", "bm25")),
        query_mode=str(params.get("query_mode", "abstract")),
        top_k=int(params.get("top_k", 8)),
        over_fetch=int(params.get("over_fetch", 4)),
        dedup_threshold=float(params.get("dedup_threshold", 85.0)),
        segment_chars=int(params.get("segment_chars", 12_000)),
        segment_start=int(params.get("segment_start", 0)),
        segment_end=(
            int(params["segment_end"]) if params.get("segment_end") is not None else None
        ),
        shard_index=int(params.get("shard_index", 0)),
        shard_count=int(params.get("shard_count", 1)),
        max_segments=(
            int(params["max_segments"])
            if params.get("max_segments") is not None
            else None
        ),
        minimum_segments=int(params.get("minimum_segments", 1)),
        max_concurrency=int(params.get("max_concurrency", 1)),
        include_bugzilla=bool(params.get("include_bugzilla", False)),
        require_host_read_isolation=bool(params.get("require_host_read_isolation", False)),
    )


async def run(
    plan: ExperimentPlan | Mapping[str, Any],
    repetition: int,
    output_dir: Path,
    backend: AgentBackend | None = None,
) -> StageResult:
    """Shared stage API used by the unified experiment dispatcher."""

    try:
        config = config_from_plan(plan, repetition, output_dir)
        result = await run_invariant_generation(config, backend=backend)
        artifacts = [
            ArtifactRef.from_path(path, base_dir=config.output_dir, kind=path.suffix.lstrip("."))
            for path in result.artifacts
        ]
        return StageResult(
            stage="invariant-generation",
            status="completed",
            artifacts=artifacts,
            metrics={
                "rag_candidates": result.rag_candidates,
                "segmented_candidates": result.segmented_candidates,
                "accepted_invariants": len(result.accepted),
                "overlap": result.overlap,
                "segment_count": result.segment_count,
                "selected_segment_count": result.selected_segment_count,
                "input_chars": result.input_chars,
                "selection_complete": result.selection_complete,
                "max_concurrency": config.max_concurrency,
                "result_valid": result.result_valid,
                "continuation_ready": result.continuation_ready,
            },
            metadata={
                "generation_path": config.generation_path,
                "repetition": repetition,
                "accepted_invariants": "accepted-invariants.jsonl",
                "execution_status": result.execution_status,
                "result_valid": result.result_valid,
                "continuation_ready": result.continuation_ready,
                "selection_complete": result.selection_complete,
                "selection_warning": result.selection_warning,
                "minimum_warning": result.minimum_warning,
                "segment_selection": (
                    result.segment_manifest.segment_selection.model_dump(mode="json")
                    if result.segment_manifest is not None
                    else None
                ),
            },
            messages=[
                message
                for message in (result.selection_warning, result.minimum_warning)
                if message is not None
            ],
        )
    except Exception as exc:
        return StageResult(
            stage="invariant-generation",
            status="failed",
            error=f"{type(exc).__name__}: {exc}",
            metadata={"repetition": repetition},
        )


__all__ = [
    "AcceptedInvariant",
    "BackendJudge",
    "GenerationPath",
    "InvariantGenerationConfig",
    "InvariantGenerationResult",
    "InvariantProvenance",
    "config_from_plan",
    "merge_accepted_invariants",
    "run",
    "run_invariant_generation",
]
