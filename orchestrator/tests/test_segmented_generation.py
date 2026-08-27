from __future__ import annotations

import asyncio
import json
from pathlib import Path
from typing import Any, cast

import pytest
from pydantic import BaseModel, ValidationError

from defuzz_loop.experiment_engine.agent_backend import AgentResult
from defuzz_loop.experiment_engine.segmented import (
    SegmentReview,
    build_segment_manifest,
    run_segmented_generation,
)
from defuzz_loop.specgen.grounding import EntailmentJudgment
from defuzz_loop.token_usage import BudgetExceeded


class FakeBackend:
    def __init__(self) -> None:
        self.prompts: list[str] = []
        self.metadata: list[dict[str, Any]] = []

    def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        self.prompts.append(prompt)
        self.metadata.append(kwargs["metadata"])
        payload = SegmentReview.model_validate(
            {
                "candidates": [
                    {
                        "statement": "The emitted guard must be checked before return.",
                        "observation": "The return path lacks a guard comparison.",
                        "protected_asset": "saved return address",
                        "activation_condition": "stack protection is enabled",
                        "mechanism": "stack-protector",
                        "falsifiability": {
                            "observability": "inspect the return path",
                            "determinism": "comparison is present or absent",
                            "cost": "one binary inspection",
                            "static_or_dynamic": "static",
                        },
                    }
                ]
            }
        )
        return AgentResult(success=True, final=payload.model_dump_json())


class FakeJudge:
    async def complete(
        self, *, task: str, key: str, system: str, user: str, output_model: type[BaseModel]
    ) -> BaseModel:
        assert task == "entailment"
        return EntailmentJudgment(entailed=True, support="return path")


def test_manifest_is_deterministic_sorted_and_excludes_findings(tmp_path: Path) -> None:
    source = tmp_path / "compiler"
    (source / "z").mkdir(parents=True)
    (source / "a.c").write_text("line one\nline two\n", encoding="utf-8")
    (source / "z" / "b.cc").write_text("line three\n", encoding="utf-8")
    (source / "findings").mkdir()
    (source / "findings" / "answer.c").write_text("secret\n", encoding="utf-8")
    docs = tmp_path / "docs"
    docs.mkdir()
    (docs / "abi.md").write_text("ABI contract\n", encoding="utf-8")

    first = build_segment_manifest(source, document_roots=[docs], segment_chars=10)
    second = build_segment_manifest(source, document_roots=[docs], segment_chars=10)

    assert first == second
    assert first.manifest_id == second.manifest_id
    assert [segment.path for segment in first.segments] == sorted(
        segment.path for segment in first.segments
    )
    assert all("findings" not in segment.path for segment in first.segments)
    assert len({segment.segment_id for segment in first.segments}) == len(first.segments)
    assert first.preflight.segment_count == len(first.segments)
    assert first.preflight.selected_segment_count == len(first.segments)
    assert first.preflight.selection_complete is True
    assert first.preflight.selection_warning is None
    assert first.preflight.minimum_warning is None
    assert first.preflight.estimated_input_chars == sum(
        len(segment.text) for segment in first.segments
    )


def test_manifest_selection_is_deterministic_across_multiple_document_roots(
    tmp_path: Path,
) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    for index in range(4):
        (source / f"source-{index}.c").write_text(
            f"source {index}\n", encoding="utf-8"
        )
    docs_a = tmp_path / "docs-a"
    docs_b = tmp_path / "docs-b"
    docs_a.mkdir()
    docs_b.mkdir()
    (docs_a / "contract.md").write_text("contract A\n", encoding="utf-8")
    (docs_b / "contract.rst").write_text("contract B\n", encoding="utf-8")

    full = build_segment_manifest(
        source, document_roots=[docs_b, docs_a], segment_chars=1000
    )
    first = build_segment_manifest(
        source,
        document_roots=[docs_b, docs_a],
        segment_chars=1000,
        segment_start=1,
        segment_end=6,
        shard_index=1,
        shard_count=2,
        max_segments=2,
        minimum_segments=3,
    )
    second = build_segment_manifest(
        source,
        document_roots=[docs_a, docs_b],
        segment_chars=1000,
        segment_start=1,
        segment_end=6,
        shard_index=1,
        shard_count=2,
        max_segments=2,
        minimum_segments=3,
    )

    expected = [
        segment
        for index, segment in enumerate(full.segments)
        if 1 <= index < 6 and index % 2 == 1
    ][:2]
    assert first == second
    assert [item.segment_id for item in first.segments] == [
        item.segment_id for item in expected
    ]
    assert first.segment_selection.model_dump(exclude={"selection_sha256"}) == {
        "segment_start": 1,
        "segment_end": 6,
        "shard_index": 1,
        "shard_count": 2,
        "max_segments": 2,
    }
    assert len(first.segment_selection.selection_sha256) == 64
    assert first.preflight.segment_count == len(full.segments)
    assert first.preflight.selected_segment_count == 2
    assert first.preflight.selection_complete is False
    assert (
        first.preflight.selection_warning
        == "partial corpus selection; this run is not full-corpus evidence"
    )
    assert first.preflight.minimum_warning is not None
    assert {segment.source_type for segment in full.segments} == {"source", "document"}
    assert sum(segment.source_type == "document" for segment in full.segments) == 2


@pytest.mark.parametrize(
    "selection",
    [
        {"segment_start": 1},
        {"segment_end": 2},
        {"shard_count": 2},
        {"max_segments": 2},
    ],
)
def test_every_partial_selection_has_a_full_corpus_warning(
    tmp_path: Path, selection: dict[str, int]
) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    for index in range(3):
        (source / f"{index}.c").write_text(f"line {index}\n", encoding="utf-8")

    manifest = build_segment_manifest(source, segment_chars=1000, **cast(Any, selection))

    assert manifest.preflight.selection_complete is False
    assert (
        manifest.preflight.selection_warning
        == "partial corpus selection; this run is not full-corpus evidence"
    )


def test_non_truncating_max_segments_remains_complete(tmp_path: Path) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    for index in range(2):
        (source / f"{index}.c").write_text(f"line {index}\n", encoding="utf-8")

    manifest = build_segment_manifest(source, segment_chars=1000, max_segments=2)

    assert manifest.preflight.selection_complete is True
    assert manifest.preflight.selection_warning is None


async def test_segmented_generation_injects_evidence_and_calls_every_segment(
    tmp_path: Path,
) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    (source / "guard.c").write_text("guard check\nreturn value\n", encoding="utf-8")
    manifest = build_segment_manifest(source, segment_chars=12)
    backend = FakeBackend()

    result = await run_segmented_generation(
        manifest,
        backend=cast(Any, backend),
        judge=cast(Any, FakeJudge()),
        output_dir=tmp_path / "out",
        cwd=tmp_path,
        run_context={"run_id": "part1-r1"},
    )

    assert len(backend.prompts) == len(manifest.segments)
    assert all(item["stage"] == "segmented-cot" for item in backend.metadata)
    assert len(result.candidates) == len(manifest.segments)
    for candidate, segment in zip(result.candidates, manifest.segments, strict=True):
        assert candidate.seed_id == segment.segment_id
        assert candidate.chunk_id == segment.segment_id
        assert candidate.evidence_snippet == segment.text
        assert candidate.source_url_or_path == f"{segment.path}:{segment.start_line}"
        assert candidate.protected_asset == "saved return address"
        assert candidate.activation_condition == "stack protection is enabled"
        assert candidate.grounding is not None and candidate.grounding.accepted
        assert "answer" not in json.dumps(candidate.model_dump())


class DelayedBackend:
    def __init__(self) -> None:
        self.active = 0
        self.max_active = 0
        self.completion_order: list[int] = []
        self.output_dirs: list[Path] = []
        self.release = [asyncio.Event() for _ in range(3)]

    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        index = int(kwargs["metadata"]["segment_index"])
        self.active += 1
        self.max_active = max(self.max_active, self.active)
        self.output_dirs.append(Path(kwargs["output_dir"]))
        if self.active == 3:
            self.release[2].set()
        await self.release[index].wait()
        self.active -= 1
        self.completion_order.append(index)
        if index > 0:
            self.release[index - 1].set()
        payload = SegmentReview.model_validate(
            {
                "candidates": [
                    {
                        "statement": f"Invariant from segment {index}",
                        "observation": "A required check is absent.",
                        "protected_asset": "return address",
                        "activation_condition": "the epilogue executes",
                        "mechanism": "stack-protector",
                        "falsifiability": {
                            "observability": "inspect emitted code",
                            "determinism": "check exists or does not",
                            "cost": "one inspection",
                            "static_or_dynamic": "static",
                        },
                    }
                ]
            }
        )
        return AgentResult(success=True, final=payload.model_dump_json())


async def test_concurrent_reviews_preserve_manifest_order(tmp_path: Path) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    for index in range(3):
        (source / f"{index}.c").write_text(f"line {index}\n", encoding="utf-8")
    manifest = build_segment_manifest(source, segment_chars=1000)
    backend = DelayedBackend()

    result = await run_segmented_generation(
        manifest,
        backend=cast(Any, backend),
        judge=cast(Any, FakeJudge()),
        output_dir=tmp_path / "out",
        cwd=tmp_path,
        max_concurrency=3,
    )

    assert backend.max_active == 3
    assert backend.completion_order == [2, 1, 0]
    assert len(set(backend.output_dirs)) == len(manifest.segments)
    assert [candidate.seed_id for candidate in result.candidates] == [
        segment.segment_id for segment in manifest.segments
    ]
    assert result.max_concurrency == 3
    assert result.effective_concurrency == 3


class BudgetSink:
    def __init__(self) -> None:
        self.consumed = 0
        self.budget = 10
        self.checks = 0

    def check_budget(self) -> None:
        self.checks += 1
        if self.consumed >= self.budget:
            raise BudgetExceeded(consumed=self.consumed, budget=self.budget)


class BudgetedBackend:
    def __init__(self) -> None:
        self.calls = 0
        self.active = 0
        self.max_active = 0

    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        self.calls += 1
        self.active += 1
        self.max_active = max(self.max_active, self.active)
        await asyncio.sleep(0)
        sink = kwargs["token_sink"]
        sink.consumed += 10
        self.active -= 1
        return AgentResult(success=True, final=SegmentReview().model_dump_json())


async def test_shared_token_budget_serializes_admission_and_stops_next_call(
    tmp_path: Path,
) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    for index in range(3):
        (source / f"{index}.c").write_text(f"line {index}\n", encoding="utf-8")
    manifest = build_segment_manifest(source, segment_chars=1000)
    backend = BudgetedBackend()
    sink = BudgetSink()

    with pytest.raises(BudgetExceeded, match="consumed 10 of 10"):
        await run_segmented_generation(
            manifest,
            backend=cast(Any, backend),
            judge=cast(Any, FakeJudge()),
            output_dir=tmp_path / "out",
            cwd=tmp_path,
            token_sink=sink,
            max_concurrency=3,
        )

    assert backend.calls == 1
    assert backend.max_active == 1
    assert sink.checks == 2


class MissingRequiredFieldBackend:
    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        return AgentResult(
            success=True,
            final=json.dumps(
                {
                    "candidates": [
                        {
                            "statement": "Invariant missing falsifiability.",
                            "observation": "A required check is absent.",
                            "protected_asset": "return address",
                            "activation_condition": "stack protection is enabled",
                            "mechanism": "stack-protector",
                        }
                    ]
                }
            ),
        )


class EmptyRequiredFieldBackend:
    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        return AgentResult(
            success=True,
            final=json.dumps(
                {
                    "candidates": [
                        {
                            "statement": "Invariant with blank observability.",
                            "observation": "A required check is absent.",
                            "protected_asset": "return address",
                            "activation_condition": "stack protection is enabled",
                            "mechanism": "stack-protector",
                            "falsifiability": {
                                "observability": "   ",
                                "determinism": "check exists or does not",
                                "cost": "one inspection",
                                "static_or_dynamic": "static",
                            },
                        }
                    ]
                }
            ),
        )


class VerboseMechanismBackend:
    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        return AgentResult(
            success=True,
            final=json.dumps(
                {
                    "candidates": [
                        {
                            "statement": "Invariant with non-canonical mechanism.",
                            "observation": "A required check is absent.",
                            "protected_asset": "return address",
                            "activation_condition": "stack protection is enabled",
                            "mechanism": (
                                "An attacker who can redirect control flow to this offset can "
                                "begin execution there and bypass the intended target set."
                            ),
                            "falsifiability": {
                                "observability": "inspect emitted code",
                                "determinism": "check exists or does not",
                                "cost": "one inspection",
                                "static_or_dynamic": "static",
                            },
                        }
                    ]
                }
            ),
        )


class GenericTargetBackend:
    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        return AgentResult(
            success=True,
            final=json.dumps(
                {
                    "candidates": [
                        {
                            "statement": "Invariant with guessed target.",
                            "observation": "A required check is absent.",
                            "protected_asset": "return address",
                            "activation_condition": "stack protection is enabled",
                            "mechanism": "stack-protector",
                            "target": "generic",
                            "falsifiability": {
                                "observability": "inspect emitted code",
                                "determinism": "check exists or does not",
                                "cost": "one inspection",
                                "static_or_dynamic": "static",
                            },
                        }
                    ]
                }
            ),
        )


class EmptyCandidateBackend:
    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        return AgentResult(success=True, final=SegmentReview().model_dump_json())


async def test_segmented_generation_rejects_missing_required_falsifiability(
    tmp_path: Path,
) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    (source / "guard.c").write_text("guard check\n", encoding="utf-8")
    manifest = build_segment_manifest(source, segment_chars=1000)

    with pytest.raises(ValidationError, match="falsifiability"):
        await run_segmented_generation(
            manifest,
            backend=cast(Any, MissingRequiredFieldBackend()),
            judge=cast(Any, FakeJudge()),
            output_dir=tmp_path / "out",
            cwd=tmp_path,
        )


async def test_segmented_generation_rejects_empty_required_semantics(
    tmp_path: Path,
) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    (source / "guard.c").write_text("guard check\n", encoding="utf-8")
    manifest = build_segment_manifest(source, segment_chars=1000)

    with pytest.raises(ValidationError, match="observability"):
        await run_segmented_generation(
            manifest,
            backend=cast(Any, EmptyRequiredFieldBackend()),
            judge=cast(Any, FakeJudge()),
            output_dir=tmp_path / "out",
            cwd=tmp_path,
        )


async def test_segmented_generation_rejects_noncanonical_mechanism_text(
    tmp_path: Path,
) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    (source / "guard.c").write_text("guard check\n", encoding="utf-8")
    manifest = build_segment_manifest(source, segment_chars=1000)

    with pytest.raises(ValidationError, match="mechanism"):
        await run_segmented_generation(
            manifest,
            backend=cast(Any, VerboseMechanismBackend()),
            judge=cast(Any, FakeJudge()),
            output_dir=tmp_path / "out",
            cwd=tmp_path,
        )


async def test_segmented_generation_rejects_generic_target_placeholder(
    tmp_path: Path,
) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    (source / "guard.c").write_text("guard check\n", encoding="utf-8")
    manifest = build_segment_manifest(source, segment_chars=1000)

    with pytest.raises(ValidationError, match="generic"):
        await run_segmented_generation(
            manifest,
            backend=cast(Any, GenericTargetBackend()),
            judge=cast(Any, FakeJudge()),
            output_dir=tmp_path / "out",
            cwd=tmp_path,
        )


async def test_segmented_generation_allows_empty_candidate_list(
    tmp_path: Path,
) -> None:
    source = tmp_path / "compiler"
    source.mkdir()
    (source / "noop.c").write_text("int noop(void) { return 0; }\n", encoding="utf-8")
    manifest = build_segment_manifest(source, segment_chars=1000)

    result = await run_segmented_generation(
        manifest,
        backend=cast(Any, EmptyCandidateBackend()),
        judge=cast(Any, FakeJudge()),
        output_dir=tmp_path / "out",
        cwd=tmp_path,
    )

    assert result.candidates == []
    assert result.rejected == []
    assert result.reviewed_segments == len(manifest.segments)
