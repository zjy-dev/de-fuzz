from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
from pydantic import BaseModel

from defuzz_loop.experiment_engine.agent_backend import AgentResult
from defuzz_loop.experiment_engine.invariant_generation import (
    InvariantGenerationConfig,
    config_from_plan,
    run_invariant_generation,
)
from defuzz_loop.experiment_engine.models import ExperimentPlan
from defuzz_loop.experiment_engine.segmented import SegmentReview
from defuzz_loop.specgen.grounding import EntailmentJudgment
from defuzz_loop.specgen.pipeline import PipelineResult
from defuzz_loop.specgen.schema import (
    Candidate,
    Falsifiability,
    GroundingResult,
    Novelty,
)

_STATEMENT = "The emitted guard must be checked before return."


class FakeBackend:
    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        if "Review exactly one compiler" in prompt:
            final = SegmentReview(
                candidates=[
                    {
                        "statement": _STATEMENT,
                        "observation": "The return path lacks a guard comparison.",
                        "mechanism": "stack-protector",
                        "falsifiability": {
                            "observability": "inspect emitted return path",
                            "determinism": "comparison exists or does not",
                            "cost": "one inspection",
                            "static_or_dynamic": "static",
                        },
                    }
                ]
            ).model_dump_json()
        else:
            final = EntailmentJudgment(entailed=True, support="guard check").model_dump_json()
        return AgentResult(success=True, final=final)


class FakeJudge:
    async def complete(
        self, *, task: str, key: str, system: str, user: str, output_model: type[BaseModel]
    ) -> BaseModel:
        return EntailmentJudgment(entailed=True, support="guard check")


def _rag_candidate() -> Candidate:
    return Candidate(
        seed_id="GCC-PR-1",
        origin_mechanism="historical-bug",
        hit_mechanism="stack-protector",
        statement=_STATEMENT.lower(),
        observation="The return path lacks a guard comparison.",
        source_kind="source",
        source_url_or_path="gcc/guard.cc:10",
        evidence_snippet="emit guard check before return",
        compiler="GCC",
        version="test",
        target="generic",
        falsifiability=Falsifiability(
            observability="inspect emitted return path",
            determinism="comparison exists or does not",
            cost="one inspection",
            static_or_dynamic="static",
        ),
        grounding=GroundingResult(
            evidence_entailed=True, falsifiable=True, accepted=True, reason="guard check"
        ),
        novelty=Novelty(is_novel=True),
        chunk_id="gcc/guard.cc:10:emit_guard",
    )


def _fixture_roots(tmp_path: Path) -> tuple[Path, Path]:
    source = tmp_path / "gcc"
    source.mkdir()
    (source / "guard.cc").write_text(
        "void emit_guard()\n{\n  // emit guard check before return\n}\n",
        encoding="utf-8",
    )
    reference = tmp_path / "reference"
    (reference / "docs" / "bugs").mkdir(parents=True)
    (reference / "docs" / "invariants").mkdir(parents=True)
    return source, reference


async def test_combined_merges_overlap_and_writes_stable_artifact(tmp_path: Path) -> None:
    source, reference = _fixture_roots(tmp_path)
    seen: dict[str, Any] = {}

    async def fake_rag(config: Any, *, judge_override: Any = None) -> PipelineResult:
        seen["seed_sources"] = config.seed_sources
        seen["findings_root"] = config.findings_root
        seen["bugs_root"] = config.bugs_root
        seen["judge"] = judge_override
        return PipelineResult(seeds=[], corpus_size=1, accepted=[_rag_candidate()])

    config = InvariantGenerationConfig(
        generation_path="combined",
        corpus_root=source,
        output_dir=tmp_path / "out",
        run_id="part1",
        repetition=1,
        reference_root=reference,
        segment_chars=1000,
    )
    result = await run_invariant_generation(
        config,
        backend=FakeBackend(),
        grounding_judge=FakeJudge(),
        rag_runner=fake_rag,
    )

    assert seen["seed_sources"] == ["bugs"]
    assert seen["findings_root"] is None
    assert seen["bugs_root"] == reference / "docs" / "bugs"
    assert seen["judge"] is not None
    assert result.rag_candidates == 1
    assert result.segmented_candidates == 1
    assert result.overlap == 1
    assert len(result.accepted) == 1
    record = result.accepted[0]
    assert record.generation_path == "combined"
    assert record.generation_paths == ["rag", "segmented-cot"]
    assert {item.generation_path for item in record.provenance} == {
        "rag",
        "segmented-cot",
    }

    output = config.output_dir / "accepted-invariants.jsonl"
    payload = json.loads(output.read_text(encoding="utf-8").strip())
    assert payload["invariant_id"] == record.invariant_id
    assert payload["generation_path"] == "combined"
    assert (config.output_dir / "segment-manifest.json").is_file()
    assert (config.output_dir / "invariant-generation-manifest.json").is_file()


def test_without_rag_only_disables_rag_producer(tmp_path: Path) -> None:
    source, reference = _fixture_roots(tmp_path)
    plan = ExperimentPlan(
        run_id="without-rag-r1",
        experiment="invariant-generation",
        variant="without-rag",
        parameters={
            "generation_path": "combined",
            "corpus_root": str(source),
            "reference_root": str(reference),
        },
    )

    config = config_from_plan(plan, 1, tmp_path / "out")

    assert config.generation_path == "segmented-cot"
    assert config.seed_sources == ["bugs"]


async def test_rag_pipeline_uses_injected_judge_without_building_default(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from defuzz_loop.specgen import pipeline

    source, reference = _fixture_roots(tmp_path)
    bugs = reference / "docs" / "bugs"
    (bugs / "invalid.md").write_text("# not a seed\n", encoding="utf-8")

    def fail_default(**kwargs: Any) -> Any:
        raise AssertionError("default judge must not be built when override is supplied")

    monkeypatch.setattr(pipeline, "build_judge", fail_default)
    config = pipeline.PipelineConfig(
        seed_sources=["bugs"],
        gcc_root=source,
        findings_root=None,
        bugs_root=bugs,
        invariants_root=reference / "docs" / "invariants",
        out_dir=tmp_path / "rag-out",
        cache_root=tmp_path / "cache",
        include_bugzilla=False,
    )

    result = await pipeline.run_pipeline(config, judge_override=FakeJudge())

    assert result.seeds == []
    assert (config.out_dir / "manifest.json").is_file()


@pytest.mark.parametrize(
    "parameters",
    [
        {"findings_root": "/tmp/findings"},
        {"seed_sources": ["bugs", "findings"]},
    ],
)
def test_plan_rejects_findings_as_formal_rag_input(
    tmp_path: Path, parameters: dict[str, Any]
) -> None:
    plan = ExperimentPlan(
        run_id="leak-test",
        experiment="invariant-generation",
        parameters=parameters,
    )
    with pytest.raises(ValueError, match="findings"):
        config_from_plan(plan, 1, tmp_path / "out")
