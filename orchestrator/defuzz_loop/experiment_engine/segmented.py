"""Deterministic segmented-review producer for Part I invariant generation.

The segmentation step is deliberately model-free: it walks explicitly supplied
source/document roots in sorted order, splits text on line boundaries, and
content-addresses every segment.  An external agent backend may then review the
manifest one segment at a time.  Evidence paths and snippets are injected by
this module rather than accepted from model output.
"""

from __future__ import annotations

import asyncio
import hashlib
import inspect
import json
import re
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator

from ..audit_schema import normalize_mechanism
from ..specgen.grounding import ground_candidates
from ..specgen.judge import Judge
from ..specgen.schema import Candidate, Falsifiability
from .agent_backend import AgentBackend, AgentRequest
from .workspace import validate_agent_path_isolation

_SOURCE_SUFFIXES = {
    ".c",
    ".cc",
    ".cpp",
    ".cxx",
    ".def",
    ".h",
    ".hh",
    ".hpp",
    ".inc",
    ".md",  # GCC/LLVM machine descriptions.
    ".td",
}
_DOCUMENT_SUFFIXES = {".md", ".rst", ".txt"}
_EXCLUDED_PARTS = {
    ".git",
    "__pycache__",
    "build",
    "findings",
    "node_modules",
    "reports",
    "target",
}
_TARGET_SLUG = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.+-]*$")
_VERSION_SLUG = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:+-]*$")
_COMPILER_ALIASES = {
    "gcc": "GCC",
    "gnu-compiler-collection": "GCC",
    "llvm": "LLVM",
    "clang": "LLVM",
}
_RAW_MECHANISM_SLUG = re.compile(r"^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$")


class SourceDocumentSegment(BaseModel):
    """One stable, replayable unit of source or documentation."""

    segment_id: str
    source_type: Literal["source", "document"]
    root: str
    path: str
    start_line: int = Field(ge=1)
    end_line: int = Field(ge=1)
    text_sha256: str
    text: str


class SegmentSelection(BaseModel):
    """Explicit deterministic subset applied to the discovered corpus."""

    segment_start: int = Field(default=0, ge=0)
    segment_end: int | None = Field(default=None, ge=1)
    shard_index: int = Field(default=0, ge=0)
    shard_count: int = Field(default=1, ge=1)
    max_segments: int | None = Field(default=None, gt=0)
    selection_sha256: str = ""


class SegmentPreflight(BaseModel):
    """Cheap corpus measurements available before any model call."""

    segment_count: int = Field(default=0, ge=0)
    selected_segment_count: int = Field(default=0, ge=0)
    estimated_input_chars: int = Field(default=0, ge=0)
    selection_complete: bool = True
    selection_warning: str | None = None
    minimum_segments: int = Field(default=1, ge=1)
    minimum_warning: str | None = None


class SegmentManifest(BaseModel):
    """Content-addressed input manifest; no timestamps or traversal-order noise."""

    schema_version: int = 1
    compiler: str
    manifest_id: str
    roots: list[str]
    segment_chars: int
    segment_selection: SegmentSelection = Field(default_factory=SegmentSelection)
    preflight: SegmentPreflight = Field(default_factory=SegmentPreflight)
    segments: list[SourceDocumentSegment]


def _require_non_empty_text(value: str) -> str:
    """Reject omitted/blank model fields instead of silently defaulting them."""

    stripped = value.strip()
    if not stripped:
        raise ValueError("must be a non-empty string")
    return stripped


def _normalize_compiler_value(value: str) -> str:
    stripped = value.strip()
    if not stripped:
        return ""
    normalized = normalize_mechanism(stripped)
    canonical = _COMPILER_ALIASES.get(normalized)
    if canonical is None:
        raise ValueError("must be GCC/LLVM or empty")
    return canonical


def _normalize_target_value(value: str) -> str:
    stripped = value.strip()
    if not stripped:
        return ""
    if stripped.casefold() == "generic":
        raise ValueError("must be explicit or omitted; do not use generic")
    if not _TARGET_SLUG.fullmatch(stripped):
        raise ValueError("must be a compact target slug or empty")
    return stripped


def _normalize_version_value(value: str) -> str:
    stripped = value.strip()
    if not stripped:
        return ""
    if not _VERSION_SLUG.fullmatch(stripped):
        raise ValueError("must be a compact version string or empty")
    return stripped


def _normalize_segmented_mechanism(value: str) -> str:
    stripped = _require_non_empty_text(value)
    if not _RAW_MECHANISM_SLUG.fullmatch(stripped):
        raise ValueError("must be a compact mechanism slug before normalization")
    canonical = normalize_mechanism(stripped)
    if not canonical:
        raise ValueError("must normalize to a non-empty mechanism slug")
    if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", canonical):
        raise ValueError("must be a compact mechanism slug")
    if len(canonical.split("-")) > 6:
        raise ValueError("must be a concise mechanism slug")
    return canonical


class SegmentedFalsifiability(BaseModel):
    """Strict segmented-review contract for grounding-critical falsifiability."""

    model_config = ConfigDict(extra="forbid")

    observability: str
    determinism: str
    cost: str
    static_or_dynamic: str

    @field_validator(
        "observability",
        "determinism",
        "cost",
        "static_or_dynamic",
    )
    @classmethod
    def _validate_non_empty(cls, value: str) -> str:
        return _require_non_empty_text(value)


class SegmentedCandidateDraft(BaseModel):
    """Agent-authored fields; provenance/evidence are intentionally absent."""

    model_config = ConfigDict(extra="forbid")

    statement: str
    observation: str
    protected_asset: str
    activation_condition: str
    mechanism: str
    compiler: str = ""
    version: str = ""
    target: str = ""
    version_sensitivity: str = "likely-to-drift"
    falsifiability: SegmentedFalsifiability

    @field_validator(
        "statement",
        "observation",
        "protected_asset",
        "activation_condition",
        "mechanism",
    )
    @classmethod
    def _validate_non_empty(cls, value: str) -> str:
        return _require_non_empty_text(value)

    @field_validator("mechanism")
    @classmethod
    def _validate_mechanism(cls, value: str) -> str:
        return _normalize_segmented_mechanism(value)

    @field_validator("compiler")
    @classmethod
    def _validate_compiler(cls, value: str) -> str:
        return _normalize_compiler_value(value)

    @field_validator("target")
    @classmethod
    def _validate_target(cls, value: str) -> str:
        return _normalize_target_value(value)

    @field_validator("version")
    @classmethod
    def _validate_version(cls, value: str) -> str:
        return _normalize_version_value(value)


class SegmentReview(BaseModel):
    model_config = ConfigDict(extra="forbid")

    candidates: list[SegmentedCandidateDraft] = Field(default_factory=list)


class SegmentedCandidate(Candidate):
    """Candidate plus the two Segmented CoT reasoning facets."""

    protected_asset: str = ""
    activation_condition: str = ""


class SegmentedGenerationResult(BaseModel):
    manifest: SegmentManifest
    candidates: list[SegmentedCandidate] = Field(default_factory=list)
    rejected: list[SegmentedCandidate] = Field(default_factory=list)
    reviewed_segments: int = 0
    max_concurrency: int = 1
    effective_concurrency: int = 1


def _sha256(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def _eligible(path: Path, *, source_type: str) -> bool:
    if any(part in _EXCLUDED_PARTS or part.startswith(".") for part in path.parts):
        return False
    suffixes = _SOURCE_SUFFIXES if source_type == "source" else _DOCUMENT_SUFFIXES
    return path.suffix.lower() in suffixes


def _split_lines(text: str, segment_chars: int) -> list[tuple[int, int, str]]:
    lines = text.splitlines(keepends=True)
    if not lines and text:
        lines = [text]
    result: list[tuple[int, int, str]] = []
    start = 0
    current: list[str] = []
    current_size = 0
    for index, line in enumerate(lines):
        if current and current_size + len(line) > segment_chars:
            result.append((start + 1, index, "".join(current)))
            start = index
            current = []
            current_size = 0
        current.append(line)
        current_size += len(line)
    if current:
        result.append((start + 1, len(lines), "".join(current)))
    return result


def _iter_files(root: Path, *, source_type: str) -> list[Path]:
    if root.is_file():
        return [root] if _eligible(Path(root.name), source_type=source_type) else []
    if not root.is_dir():
        raise ValueError(f"segment root does not exist or is not readable: {root}")
    return sorted(
        (
            path
            for path in root.rglob("*")
            if path.is_file()
            and _eligible(path.relative_to(root), source_type=source_type)
        ),
        key=lambda path: path.relative_to(root).as_posix(),
    )


def build_segment_manifest(
    corpus_root: str | Path,
    *,
    document_roots: Sequence[str | Path] = (),
    compiler: str = "gcc",
    segment_chars: int = 12_000,
    segment_start: int = 0,
    segment_end: int | None = None,
    shard_index: int = 0,
    shard_count: int = 1,
    max_segments: int | None = None,
    minimum_segments: int = 1,
) -> SegmentManifest:
    """Build a deterministic source/document manifest from explicit roots.

    Selection is applied to the stable global segment order. ``segment_end`` is
    exclusive, sharding uses ``global_index % shard_count``, and
    ``max_segments`` is a final deterministic prefix cap. The selected segments
    (and a hash of their IDs) are persisted instead of sampling silently.
    """

    if segment_chars <= 0:
        raise ValueError("segment_chars must be greater than zero")
    if compiler not in {"gcc", "llvm"}:
        raise ValueError("compiler must be 'gcc' or 'llvm'")
    if segment_start < 0:
        raise ValueError("segment_start must be zero or greater")
    if segment_end is not None and segment_end <= segment_start:
        raise ValueError("segment_end must be greater than segment_start")
    if shard_count <= 0:
        raise ValueError("shard_count must be greater than zero")
    if not 0 <= shard_index < shard_count:
        raise ValueError("shard_index must be in the range [0, shard_count)")
    if max_segments is not None and max_segments <= 0:
        raise ValueError("max_segments must be greater than zero")
    if minimum_segments <= 0:
        raise ValueError("minimum_segments must be greater than zero")
    roots: list[tuple[str, Path, Literal["source", "document"]]] = [
        ("source", Path(corpus_root).expanduser().resolve(), "source")
    ]
    docs = sorted(
        {Path(root).expanduser().resolve() for root in document_roots},
        key=lambda path: path.as_posix(),
    )
    roots.extend((f"document-{index:03d}", root, "document") for index, root in enumerate(docs, 1))

    selected: list[SourceDocumentSegment] = []
    corpus_digest = hashlib.sha256()
    segment_count = 0
    eligible_after_range_and_shard = 0
    # Segment paths are the canonical ordering key. Traverse labels in that
    # order so a bounded run never retains the entire LLVM/GCC corpus in memory.
    for label, root, source_type in sorted(roots, key=lambda item: item[0]):
        for path in _iter_files(root, source_type=source_type):
            text = path.read_text(encoding="utf-8", errors="replace")
            relative = path.name if root.is_file() else path.relative_to(root).as_posix()
            for start, end, body in _split_lines(text, segment_chars):
                if not body.strip():
                    continue
                digest = _sha256(body)
                identity = f"{compiler}\0{label}\0{relative}\0{start}\0{end}\0{digest}"
                segment = SourceDocumentSegment(
                    segment_id=f"seg-{_sha256(identity)[:20]}",
                    source_type=source_type,
                    root=str(root),
                    path=f"{label}/{relative}",
                    start_line=start,
                    end_line=end,
                    text_sha256=digest,
                    text=body,
                )
                corpus_record = (
                    segment.segment_id,
                    segment.source_type,
                    segment.path,
                    segment.start_line,
                    segment.end_line,
                    segment.text_sha256,
                )
                corpus_digest.update(
                    json.dumps(corpus_record, separators=(",", ":")).encode("utf-8")
                )
                corpus_digest.update(b"\n")
                in_range = segment_start <= segment_count and (
                    segment_end is None or segment_count < segment_end
                )
                in_shard = segment_count % shard_count == shard_index
                if in_range and in_shard:
                    eligible_after_range_and_shard += 1
                    if max_segments is None or len(selected) < max_segments:
                        selected.append(segment)
                segment_count += 1

    selected_ids = [segment.segment_id for segment in selected]
    selection_hash = _sha256(json.dumps(selected_ids, separators=(",", ":")))
    selection = SegmentSelection(
        segment_start=segment_start,
        segment_end=segment_end,
        shard_index=shard_index,
        shard_count=shard_count,
        max_segments=max_segments,
        selection_sha256=selection_hash,
    )
    minimum_warning = None
    if len(selected) < minimum_segments:
        minimum_warning = (
            f"selected {len(selected)} segments, below configured minimum "
            f"{minimum_segments}; do not treat this run as full-corpus evidence"
        )
    selection_complete = (
        segment_start == 0
        and (segment_end is None or segment_end >= segment_count)
        and shard_count == 1
        and (max_segments is None or max_segments >= eligible_after_range_and_shard)
    )
    selection_warning = (
        None
        if selection_complete
        else "partial corpus selection; this run is not full-corpus evidence"
    )
    preflight = SegmentPreflight(
        segment_count=segment_count,
        selected_segment_count=len(selected),
        estimated_input_chars=sum(len(segment.text) for segment in selected),
        selection_complete=selection_complete,
        selection_warning=selection_warning,
        minimum_segments=minimum_segments,
        minimum_warning=minimum_warning,
    )
    manifest_payload = {
        "corpus_sha256": corpus_digest.hexdigest(),
        "selection": selection.model_dump(mode="json"),
        "minimum_segments": minimum_segments,
    }
    manifest_id = f"manifest-{_sha256(json.dumps(manifest_payload, separators=(',', ':')))[:20]}"
    return SegmentManifest(
        compiler=compiler,
        manifest_id=manifest_id,
        roots=[str(root) for _, root, _ in roots],
        segment_chars=segment_chars,
        segment_selection=selection,
        preflight=preflight,
        segments=selected,
    )


def write_segment_manifest(manifest: SegmentManifest, path: str | Path) -> Path:
    destination = Path(path)
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(manifest.model_dump_json(indent=2) + "\n", encoding="utf-8")
    return destination


def write_segment_preflight(manifest: SegmentManifest, path: str | Path) -> Path:
    """Write the model-free size and selection assessment as a standalone artifact."""

    destination = Path(path)
    destination.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        **manifest.preflight.model_dump(mode="json"),
        "manifest_id": manifest.manifest_id,
        "segment_selection": manifest.segment_selection.model_dump(mode="json"),
    }
    destination.write_text(
        json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return destination


def _supported_kwargs(function: Any, values: dict[str, Any]) -> dict[str, Any]:
    try:
        signature = inspect.signature(function)
    except (TypeError, ValueError):
        return values
    if any(param.kind == inspect.Parameter.VAR_KEYWORD for param in signature.parameters.values()):
        return values
    return {key: value for key, value in values.items() if key in signature.parameters}


def _json_payload(value: Any) -> Any:
    if isinstance(value, Mapping):
        return value
    for name in ("final", "output", "content"):
        candidate = getattr(value, name, None)
        if candidate is not None:
            value = candidate
            break
    if isinstance(value, BaseModel) or isinstance(value, Mapping):
        return value
    if not isinstance(value, str):
        raise TypeError(f"agent backend returned unsupported payload: {type(value).__name__}")
    stripped = value.strip()
    if stripped.startswith("```"):
        stripped = stripped.split("\n", 1)[1].rsplit("```", 1)[0].strip()
    return json.loads(stripped)


async def complete_with_backend[T: BaseModel](
    backend: Any,
    *,
    prompt: str,
    output_model: type[T],
    cwd: Path,
    output_dir: Path,
    timeout_seconds: float | None,
    token_sink: Any = None,
    metadata: Mapping[str, Any] | None = None,
    deny_read_paths: Sequence[Path] = (),
    require_host_read_isolation: bool = False,
) -> T:
    """Invoke an async or sync fake/real backend and validate structured output."""

    if backend is None:
        raise ValueError("an AgentBackend is required for segmented generation")
    resolved_denials = validate_agent_path_isolation(
        cwd=cwd,
        output_dir=output_dir,
        deny_read_paths=deny_read_paths,
    )
    budget_check = getattr(token_sink, "check_budget", None)
    if callable(budget_check):
        budget_check()
    complete = getattr(backend, "complete", None)
    if complete is None:
        run_method = getattr(backend, "run", None)
        if run_method is None:
            raise TypeError("backend must expose complete(...) or run(request)")
        request = AgentRequest(
            prompt=prompt,
            cwd=cwd,
            output_dir=output_dir,
            schema_path=None,
            timeout_seconds=timeout_seconds,
            writable=False,
            token_sink=token_sink,
            deny_read_paths=resolved_denials,
            require_host_read_isolation=require_host_read_isolation,
            metadata=dict(metadata or {}),
        )
        result = run_method(request)
    else:
        kwargs = _supported_kwargs(
            complete,
            {
                "schema": output_model.model_json_schema(),
                "cwd": cwd,
                "output_dir": output_dir,
                "timeout_seconds": timeout_seconds,
                "writable": False,
                "token_sink": token_sink,
                "deny_read_paths": resolved_denials,
                "require_host_read_isolation": require_host_read_isolation,
                "metadata": dict(metadata or {}),
            },
        )
        result = complete(prompt, **kwargs)
    if inspect.isawaitable(result):
        result = await result
    if getattr(result, "success", True) is False:
        raise RuntimeError(getattr(result, "error", None) or "agent backend failed")
    return output_model.model_validate(_json_payload(result))


def _review_prompt(segment: SourceDocumentSegment) -> str:
    return f"""Review exactly one compiler source/document segment for security invariants.

Return JSON matching the provided schema exactly. For every candidate, all of
these required fields must be present and non-empty: statement, observation,
protected_asset, activation_condition, mechanism, and falsifiability
(observability, determinism, cost, static_or_dynamic). `mechanism` must be a
canonical compact slug like `stack-protector`, `ibt`, `bti`, or `pac`. The raw
value itself must already be slug-shaped: no whitespace, parentheses, slashes,
periods, copied sentences, or impact prose. Aliases like `stack-canary` or
`ssp` are allowed because they normalize to canonical slugs. `compiler` may be
omitted only when the segment does not name it; if present it must be `GCC` or
`LLVM`. `target` and `version` are optional, but when unknown they must be
omitted as empty strings rather than guessed; do not emit placeholders such as
`generic`, `unknown`, or prose. If this segment does not entail a fully
specified candidate, return an empty candidates list instead of guessing or
omitting required semantic fields. Return only candidates entailed by this
exact segment. Do not use or reproduce demo findings, do not invent paths, and
do not include evidence snippets or file paths in the candidate payload;
evidence and paths are attached by the runner.

SEGMENT_ID: {segment.segment_id}
SOURCE_TYPE: {segment.source_type}
PATH: {segment.path}:{segment.start_line}-{segment.end_line}
CONTENT:
```
{segment.text}
```
"""


def _has_shared_token_budget(token_sink: Any) -> bool:
    """Return whether an opaque/shared sink can reject calls on budget."""

    if not callable(getattr(token_sink, "check_budget", None)):
        return False
    if hasattr(token_sink, "token_budget"):
        return token_sink.token_budget is not None
    wrapped = getattr(token_sink, "_sink", None)
    if wrapped is not None and hasattr(wrapped, "token_budget"):
        return wrapped.token_budget is not None
    # A custom sink that exposes only check_budget is conservatively treated as
    # budgeted; there is no safe way to prove that it is a no-op.
    return True


async def run_segmented_generation(
    manifest: SegmentManifest,
    *,
    backend: AgentBackend,
    judge: Judge,
    output_dir: str | Path,
    cwd: str | Path | None = None,
    timeout_seconds: float | None = None,
    token_sink: Any = None,
    run_context: Mapping[str, Any] | None = None,
    max_concurrency: int = 1,
    deny_read_paths: Sequence[Path] = (),
    require_host_read_isolation: bool = False,
) -> SegmentedGenerationResult:
    """Review selected segments concurrently and preserve manifest ordering.

    A provider-reported token budget cannot reserve an unknown response cost in
    advance. When a shared sink exposes ``check_budget``, calls are therefore
    admitted serially so another call cannot cross the budget boundary while a
    prior call's usage is still unreported. Unbudgeted calls use the requested
    concurrency.
    """

    if max_concurrency <= 0:
        raise ValueError("max_concurrency must be greater than zero")
    destination = Path(output_dir)
    destination.mkdir(parents=True, exist_ok=True)
    if cwd is None:
        raise ValueError("segmented generation requires an isolated agent cwd")
    workdir = Path(cwd).expanduser().resolve(strict=True)
    budgeted = _has_shared_token_budget(token_sink)
    effective_concurrency = 1 if budgeted else max_concurrency
    reviews: list[list[SegmentedCandidate] | None] = [None] * len(manifest.segments)

    async def review_segment(
        index: int, segment: SourceDocumentSegment
    ) -> list[SegmentedCandidate]:
        metadata = {
            **dict(run_context or {}),
            "part": "part-i",
            "stage": "segmented-cot",
            "segment_id": segment.segment_id,
            "segment_index": index,
        }
        review = await complete_with_backend(
            backend,
            prompt=_review_prompt(segment),
            output_model=SegmentReview,
            cwd=workdir,
            output_dir=destination / "segments" / f"{index:06d}-{segment.segment_id}",
            timeout_seconds=timeout_seconds,
            token_sink=token_sink,
            metadata=metadata,
            deny_read_paths=deny_read_paths,
            require_host_read_isolation=require_host_read_isolation,
        )
        candidates: list[SegmentedCandidate] = []
        for draft in review.candidates:
            candidates.append(
                SegmentedCandidate(
                    seed_id=segment.segment_id,
                    origin_mechanism="segmented-review",
                    hit_mechanism=draft.mechanism,
                    statement=draft.statement,
                    observation=draft.observation,
                    version_sensitivity=draft.version_sensitivity,
                    source_kind=segment.source_type,
                    source_url_or_path=f"{segment.path}:{segment.start_line}",
                    evidence_snippet=segment.text,
                    compiler=draft.compiler or manifest.compiler.upper(),
                    version=draft.version,
                    target=draft.target,
                    falsifiability=Falsifiability.model_validate(
                        draft.falsifiability.model_dump(mode="json")
                    ),
                    chunk_id=segment.segment_id,
                    protected_asset=draft.protected_asset,
                    activation_condition=draft.activation_condition,
                )
            )
        return candidates

    next_index = 0

    async def worker() -> None:
        nonlocal next_index
        while next_index < len(manifest.segments):
            index = next_index
            next_index += 1
            reviews[index] = await review_segment(index, manifest.segments[index])

    worker_count = min(effective_concurrency, len(manifest.segments))
    tasks = [asyncio.create_task(worker()) for _ in range(worker_count)]
    try:
        await asyncio.gather(*tasks)
    except BaseException:
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        raise

    drafted = [candidate for review in reviews if review is not None for candidate in review]

    base_candidates: list[Candidate] = list(drafted)
    accepted_base, rejected_base = await ground_candidates(judge, base_candidates)
    accepted = [SegmentedCandidate.model_validate(item.model_dump()) for item in accepted_base]
    rejected = [SegmentedCandidate.model_validate(item.model_dump()) for item in rejected_base]
    return SegmentedGenerationResult(
        manifest=manifest,
        candidates=accepted,
        rejected=rejected,
        reviewed_segments=len(manifest.segments),
        max_concurrency=max_concurrency,
        effective_concurrency=effective_concurrency,
    )


__all__ = [
    "AgentBackend",
    "SegmentManifest",
    "SegmentPreflight",
    "SegmentReview",
    "SegmentSelection",
    "SegmentedCandidate",
    "SegmentedCandidateDraft",
    "SegmentedGenerationResult",
    "SourceDocumentSegment",
    "build_segment_manifest",
    "complete_with_backend",
    "run_segmented_generation",
    "write_segment_manifest",
    "write_segment_preflight",
]
