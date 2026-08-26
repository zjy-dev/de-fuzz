from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from pydantic import BaseModel

from defuzz_loop.experiment_engine.agent_backend import AgentResult
from defuzz_loop.experiment_engine.segmented import (
    SegmentReview,
    build_segment_manifest,
    run_segmented_generation,
)
from defuzz_loop.specgen.grounding import EntailmentJudgment


class FakeBackend:
    def __init__(self) -> None:
        self.prompts: list[str] = []
        self.metadata: list[dict[str, Any]] = []

    def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        self.prompts.append(prompt)
        self.metadata.append(kwargs["metadata"])
        payload = SegmentReview(
            candidates=[
                {
                    "statement": "The emitted guard must be checked before return.",
                    "observation": "The return path lacks a guard comparison.",
                    "protected_asset": "saved return address",
                    "activation_condition": "stack protection is enabled",
                    "mechanism": "stack-protector",
                    "target": "generic",
                    "falsifiability": {
                        "observability": "inspect the return path",
                        "determinism": "comparison is present or absent",
                        "cost": "one binary inspection",
                        "static_or_dynamic": "static",
                    },
                }
            ]
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
        backend=backend,
        judge=FakeJudge(),
        output_dir=tmp_path / "out",
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
