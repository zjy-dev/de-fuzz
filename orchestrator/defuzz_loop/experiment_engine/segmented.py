"""Deterministic segmented-review producer for Part I invariant generation.

The segmentation step is deliberately model-free: it walks explicitly supplied
source/document roots in sorted order, splits text on line boundaries, and
content-addresses every segment.  An external agent backend may then review the
manifest one segment at a time.  Evidence paths and snippets are injected by
this module rather than accepted from model output.
"""

from __future__ import annotations

import hashlib
import inspect
import json
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any, Literal

from pydantic import BaseModel, Field

from ..specgen.grounding import ground_candidates
from ..specgen.judge import Judge
from ..specgen.schema import Candidate, Falsifiability
from .agent_backend import AgentBackend, AgentRequest

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


class SegmentManifest(BaseModel):
    """Content-addressed input manifest; no timestamps or traversal-order noise."""

    schema_version: int = 1
    compiler: str
    manifest_id: str
    roots: list[str]
    segment_chars: int
    segments: list[SourceDocumentSegment]


class SegmentedCandidateDraft(BaseModel):
    """Agent-authored fields; provenance/evidence are intentionally absent."""

    statement: str
    observation: str
    protected_asset: str = ""
    activation_condition: str = ""
    mechanism: str = "unspecified"
    compiler: str = ""
    version: str = ""
    target: str = "generic"
    version_sensitivity: str = "likely-to-drift"
    falsifiability: Falsifiability = Field(default_factory=Falsifiability)


class SegmentReview(BaseModel):
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
) -> SegmentManifest:
    """Build a deterministic source/document manifest from explicit roots."""

    if segment_chars <= 0:
        raise ValueError("segment_chars must be greater than zero")
    roots: list[tuple[str, Path, Literal["source", "document"]]] = [
        ("source", Path(corpus_root).expanduser().resolve(), "source")
    ]
    docs = sorted(
        {Path(root).expanduser().resolve() for root in document_roots},
        key=lambda path: path.as_posix(),
    )
    roots.extend((f"document-{index:03d}", root, "document") for index, root in enumerate(docs, 1))

    segments: list[SourceDocumentSegment] = []
    for label, root, source_type in roots:
        for path in _iter_files(root, source_type=source_type):
            text = path.read_text(encoding="utf-8", errors="replace")
            relative = path.name if root.is_file() else path.relative_to(root).as_posix()
            for start, end, body in _split_lines(text, segment_chars):
                if not body.strip():
                    continue
                digest = _sha256(body)
                identity = f"{compiler}\0{label}\0{relative}\0{start}\0{end}\0{digest}"
                segments.append(
                    SourceDocumentSegment(
                        segment_id=f"seg-{_sha256(identity)[:20]}",
                        source_type=source_type,
                        root=str(root),
                        path=f"{label}/{relative}",
                        start_line=start,
                        end_line=end,
                        text_sha256=digest,
                        text=body,
                    )
                )

    segments.sort(key=lambda item: (item.path, item.start_line, item.segment_id))
    manifest_payload = [
        (
            item.segment_id,
            item.source_type,
            item.path,
            item.start_line,
            item.end_line,
            item.text_sha256,
        )
        for item in segments
    ]
    manifest_id = f"manifest-{_sha256(json.dumps(manifest_payload, separators=(',', ':')))[:20]}"
    return SegmentManifest(
        compiler=compiler,
        manifest_id=manifest_id,
        roots=[str(root) for _, root, _ in roots],
        segment_chars=segment_chars,
        segments=segments,
    )


def write_segment_manifest(manifest: SegmentManifest, path: str | Path) -> Path:
    destination = Path(path)
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(manifest.model_dump_json(indent=2) + "\n", encoding="utf-8")
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
) -> T:
    """Invoke an async or sync fake/real backend and validate structured output."""

    if backend is None:
        raise ValueError("an AgentBackend is required for segmented generation")
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

For each candidate identify: the protected asset, activation condition, required
security property, and externally observable violation. Return only candidates
entailed by this exact segment. Do not use or reproduce demo findings, do not
invent paths, and return an empty candidates list when the segment has no
security property. Evidence and paths are attached by the runner.

SEGMENT_ID: {segment.segment_id}
SOURCE_TYPE: {segment.source_type}
PATH: {segment.path}:{segment.start_line}-{segment.end_line}
CONTENT:
```
{segment.text}
```
"""


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
) -> SegmentedGenerationResult:
    """Review all manifest segments and apply the shared grounding gates."""

    destination = Path(output_dir)
    destination.mkdir(parents=True, exist_ok=True)
    workdir = Path(cwd) if cwd is not None else Path(manifest.roots[0])
    drafted: list[SegmentedCandidate] = []
    for segment in manifest.segments:
        metadata = {
            **dict(run_context or {}),
            "part": "part-i",
            "stage": "segmented-cot",
            "segment_id": segment.segment_id,
        }
        review = await complete_with_backend(
            backend,
            prompt=_review_prompt(segment),
            output_model=SegmentReview,
            cwd=workdir,
            output_dir=destination,
            timeout_seconds=timeout_seconds,
            token_sink=token_sink,
            metadata=metadata,
        )
        for draft in review.candidates:
            drafted.append(
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
                    falsifiability=draft.falsifiability,
                    chunk_id=segment.segment_id,
                    protected_asset=draft.protected_asset,
                    activation_condition=draft.activation_condition,
                )
            )

    base_candidates: list[Candidate] = list(drafted)
    accepted_base, rejected_base = await ground_candidates(judge, base_candidates)
    accepted = [SegmentedCandidate.model_validate(item.model_dump()) for item in accepted_base]
    rejected = [SegmentedCandidate.model_validate(item.model_dump()) for item in rejected_base]
    return SegmentedGenerationResult(
        manifest=manifest,
        candidates=accepted,
        rejected=rejected,
        reviewed_segments=len(manifest.segments),
    )


__all__ = [
    "AgentBackend",
    "SegmentManifest",
    "SegmentReview",
    "SegmentedCandidate",
    "SegmentedCandidateDraft",
    "SegmentedGenerationResult",
    "SourceDocumentSegment",
    "build_segment_manifest",
    "complete_with_backend",
    "run_segmented_generation",
    "write_segment_manifest",
]
