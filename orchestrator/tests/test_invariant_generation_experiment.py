from __future__ import annotations

import json
from pathlib import Path
from typing import Any, cast

import pytest
from pydantic import BaseModel, ValidationError

from defuzz_loop.experiment_engine.agent_backend import AgentResult
from defuzz_loop.experiment_engine.invariant_generation import (
    AcceptedInvariant,
    InvariantGenerationConfig,
    config_from_plan,
    run,
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
            final = SegmentReview.model_validate(
                {
                    "candidates": [
                        {
                            "statement": _STATEMENT,
                            "observation": "The return path lacks a guard comparison.",
                            "protected_asset": "saved return address",
                            "activation_condition": "stack protection is enabled",
                            "mechanism": "stack-protector",
                            "falsifiability": {
                                "observability": "inspect emitted return path",
                                "determinism": "comparison exists or does not",
                                "cost": "one inspection",
                                "static_or_dynamic": "static",
                            },
                        }
                    ]
                }
            ).model_dump_json()
        else:
            final = EntailmentJudgment(entailed=True, support="guard check").model_dump_json()
        return AgentResult(success=True, final=final)


class FakeJudge:
    async def complete(
        self, *, task: str, key: str, system: str, user: str, output_model: type[BaseModel]
    ) -> BaseModel:
        return EntailmentJudgment(entailed=True, support="guard check")


class EmptyBackend:
    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        assert "Review exactly one compiler" in prompt
        return AgentResult(success=True, final=SegmentReview().model_dump_json())


class MissingSemanticFieldBackend:
    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        assert "Review exactly one compiler" in prompt
        final = json.dumps(
            {
                "candidates": [
                    {
                        "statement": _STATEMENT,
                        "observation": "The return path lacks a guard comparison.",
                        "protected_asset": "saved return address",
                        "activation_condition": "stack protection is enabled",
                        "mechanism": "stack-protector",
                    }
                ]
            }
        )
        return AgentResult(success=True, final=final)


class InvalidMechanismBackend:
    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        assert "Review exactly one compiler" in prompt
        final = json.dumps(
            {
                "candidates": [
                    {
                        "statement": _STATEMENT,
                        "observation": "The return path lacks a guard comparison.",
                        "protected_asset": "saved return address",
                        "activation_condition": "stack protection is enabled",
                        "mechanism": (
                            "An attacker can redirect control flow to an unintended landing "
                            "pad and bypass the intended target set."
                        ),
                        "falsifiability": {
                            "observability": "inspect emitted return path",
                            "determinism": "comparison exists or does not",
                            "cost": "one inspection",
                            "static_or_dynamic": "static",
                        },
                    }
                ]
            }
        )
        return AgentResult(success=True, final=final)


class RecordingIsolationBackend(FakeBackend):
    supports_host_read_isolation = True

    def __init__(self) -> None:
        self.calls: list[dict[str, Any]] = []

    async def complete(self, prompt: str, schema: Any = None, **kwargs: Any) -> AgentResult:
        self.calls.append(
            {
                "prompt": prompt,
                "cwd": Path(kwargs["cwd"]),
                "cwd_entries": list(Path(kwargs["cwd"]).iterdir()),
                "deny_read_paths": list(kwargs.get("deny_read_paths", [])),
                "require_host_read_isolation": kwargs.get("require_host_read_isolation", False),
                "metadata": dict(kwargs.get("metadata", {})),
            }
        )
        return await super().complete(prompt, schema=schema, **kwargs)


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
        target="x86_64",
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
        backend=cast(Any, FakeBackend()),
        grounding_judge=cast(Any, FakeJudge()),
        rag_runner=fake_rag,
    )

    assert seen["seed_sources"] == ["bugs"]
    assert seen["findings_root"] is None
    assert seen["bugs_root"] == reference / "docs" / "bugs" / "gcc"
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
    assert config.campaign_variant == "without-rag"
    assert config.seed_sources == ["bugs"]


def test_plan_campaign_variant_overrides_default_segmented_context(tmp_path: Path) -> None:
    source, reference = _fixture_roots(tmp_path)
    plan = ExperimentPlan(
        run_id="segmented-r1",
        experiment="invariant-generation",
        variant="full",
        parameters={
            "generation_path": "segmented-cot",
            "campaign_variant": "without-rag",
            "corpus_root": str(source),
            "reference_root": str(reference),
        },
    )

    config = config_from_plan(plan, 1, tmp_path / "out")

    assert config.generation_path == "segmented-cot"
    assert config.campaign_variant == "without-rag"


def test_plan_parses_explicit_segment_controls_and_accepts_llvm_combined(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    plan = ExperimentPlan(
        run_id="segmented-r1",
        experiment="invariant-generation",
        parameters={
            "generation_path": "segmented-cot",
            "compiler": "llvm",
            "corpus_root": str(source),
            "reference_root": str(reference),
            "document_roots": [str(reference / "docs" / "bugs")],
            "segment_start": 2,
            "segment_end": 20,
            "shard_index": 1,
            "shard_count": 3,
            "max_segments": 4,
            "minimum_segments": 2,
            "max_concurrency": 5,
        },
    )

    config = config_from_plan(plan, 1, tmp_path / "out")

    assert config.compiler == "llvm"
    assert config.generation_path == "segmented-cot"
    assert config.document_roots == [reference / "docs" / "bugs"]
    assert (config.segment_start, config.segment_end) == (2, 20)
    assert (config.shard_index, config.shard_count) == (1, 3)
    assert config.max_segments == 4
    assert config.minimum_segments == 2
    assert config.max_concurrency == 5

    combined = plan.model_copy(
        update={"parameters": {**plan.parameters, "generation_path": "combined"}}
    )
    combined_config = config_from_plan(combined, 1, tmp_path / "combined")
    assert combined_config.compiler == "llvm"
    assert combined_config.generation_path == "combined"
    assert combined_config.bugs_root == reference / "docs" / "bugs" / "llvm"


async def test_llvm_full_calls_typed_rag_while_without_rag_skips_it(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    calls: list[dict[str, Any]] = []

    async def fake_rag(config: Any, *, judge_override: Any = None) -> PipelineResult:
        calls.append(
            {
                "compiler": config.compiler,
                "corpus_root": config.corpus_root,
                "bugs_root": config.bugs_root,
                "findings_root": config.findings_root,
                "judge": judge_override,
            }
        )
        return PipelineResult(seeds=[], corpus_size=1, accepted=[])

    full = InvariantGenerationConfig(
        generation_path="combined",
        corpus_root=source,
        compiler="llvm",
        version="llvm-test",
        output_dir=tmp_path / "full",
        run_id="llvm-full",
        repetition=1,
        reference_root=reference,
        segment_chars=1000,
    )
    await run_invariant_generation(
        full,
        backend=cast(Any, FakeBackend()),
        grounding_judge=cast(Any, FakeJudge()),
        rag_runner=fake_rag,
    )

    without_rag = full.model_copy(
        update={
            "generation_path": "segmented-cot",
            "output_dir": tmp_path / "without-rag",
        }
    )
    await run_invariant_generation(
        without_rag,
        backend=cast(Any, FakeBackend()),
        grounding_judge=cast(Any, FakeJudge()),
        rag_runner=fake_rag,
    )

    assert len(calls) == 1
    assert calls[0]["compiler"] == "llvm"
    assert calls[0]["corpus_root"] == source
    assert calls[0]["bugs_root"] == reference / "docs" / "bugs" / "llvm"
    assert calls[0]["findings_root"] is None
    assert calls[0]["judge"] is not None


async def test_invariant_runner_fails_closed_on_empty_segmented_corpus(
    tmp_path: Path,
) -> None:
    source = tmp_path / "empty"
    source.mkdir()
    reference = tmp_path / "reference"
    reference.mkdir()
    config = InvariantGenerationConfig(
        generation_path="segmented-cot",
        corpus_root=source,
        compiler="llvm",
        output_dir=tmp_path / "out",
        run_id="empty-llvm",
        repetition=1,
        reference_root=reference,
    )

    with pytest.raises(ValueError, match="empty llvm segmented corpus"):
        await run_invariant_generation(
            config,
            backend=cast(Any, FakeBackend()),
            grounding_judge=cast(Any, FakeJudge()),
        )


async def test_zero_accepted_is_completed_but_not_valid_for_continuation(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    (source / "second.cc").write_text("void second() {}\n", encoding="utf-8")
    output = tmp_path / "out"
    plan = ExperimentPlan(
        run_id="zero-output",
        experiment="invariant-generation",
        parameters={
            "generation_path": "segmented-cot",
            "corpus_root": str(source),
            "reference_root": str(reference),
            "max_segments": 1,
            "minimum_segments": 2,
        },
    )

    result = await run(plan, 1, output, cast(Any, EmptyBackend()))

    assert result.status == "completed"
    assert result.success
    assert result.metrics["accepted_invariants"] == 0
    assert result.metrics["segment_count"] == 2
    assert result.metrics["selected_segment_count"] == 1
    assert result.metrics["input_chars"] > 0
    assert result.metrics["selection_complete"] is False
    assert result.metrics["result_valid"] is False
    assert result.metrics["continuation_ready"] is False
    assert result.metadata["execution_status"] == "completed"
    assert result.metadata["result_valid"] is False
    assert result.metadata["continuation_ready"] is False
    assert result.metadata["selection_complete"] is False
    assert (
        result.metadata["selection_warning"]
        == "partial corpus selection; this run is not full-corpus evidence"
    )
    assert result.messages == [
        "partial corpus selection; this run is not full-corpus evidence",
        "selected 1 segments, below configured minimum 2; "
        "do not treat this run as full-corpus evidence",
    ]
    assert (output / "accepted-invariants.jsonl").read_text(encoding="utf-8") == ""

    manifest = json.loads(
        (output / "invariant-generation-manifest.json").read_text(encoding="utf-8")
    )
    assert manifest["execution_status"] == "completed"
    assert manifest["result_valid"] is False
    assert manifest["continuation_ready"] is False
    assert manifest["segment_selection"]["max_segments"] == 1
    assert len(manifest["segment_selection"]["selection_sha256"]) == 64
    assert manifest["preflight"]["selected_segment_count"] == 1
    assert manifest["preflight"]["selection_complete"] is False
    assert (output / "segment-preflight.json").is_file()


async def test_segmented_invariant_generation_rejects_missing_required_semantics(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    config = InvariantGenerationConfig(
        generation_path="segmented-cot",
        corpus_root=source,
        output_dir=tmp_path / "out",
        run_id="part1",
        repetition=1,
        reference_root=reference,
        segment_chars=1000,
    )

    with pytest.raises(ValidationError, match="falsifiability"):
        await run_invariant_generation(
            config,
            backend=cast(Any, MissingSemanticFieldBackend()),
            grounding_judge=cast(Any, FakeJudge()),
        )


async def test_segmented_invariant_generation_rejects_noncanonical_mechanism(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    config = InvariantGenerationConfig(
        generation_path="segmented-cot",
        corpus_root=source,
        output_dir=tmp_path / "out",
        run_id="part1",
        repetition=1,
        reference_root=reference,
        segment_chars=1000,
    )

    with pytest.raises(ValidationError, match="mechanism"):
        await run_invariant_generation(
            config,
            backend=cast(Any, InvalidMechanismBackend()),
            grounding_judge=cast(Any, FakeJudge()),
        )


def test_accepted_invariant_rejects_generic_target_placeholder() -> None:
    with pytest.raises(ValidationError, match="generic"):
        AcceptedInvariant(
            invariant_id="INVGEN-TEST",
            statement=_STATEMENT,
            observation="The return path lacks a guard comparison.",
            generation_path="segmented-cot",
            generation_paths=["segmented-cot"],
            provenance=[],
            compiler="GCC",
            version="",
            target="generic",
            mechanism="stack-protector",
            source_kind="source",
            source_url_or_path="gcc/guard.cc:10",
            evidence_snippet="emit guard check before return",
            protected_asset="saved return address",
            activation_condition="stack protection is enabled",
            falsifiability={
                "observability": "inspect emitted return path",
                "determinism": "comparison exists or does not",
                "cost": "one inspection",
                "static_or_dynamic": "static",
            },
        )


def test_accepted_invariant_allows_empty_target_and_version() -> None:
    record = AcceptedInvariant(
        invariant_id="INVGEN-TEST",
        statement=_STATEMENT,
        observation="The return path lacks a guard comparison.",
        generation_path="segmented-cot",
        generation_paths=["segmented-cot"],
        provenance=[],
        compiler="gcc",
        version="",
        target="",
        mechanism="stack-canary",
        source_kind="source",
        source_url_or_path="gcc/guard.cc:10",
        evidence_snippet="emit guard check before return",
        protected_asset="saved return address",
        activation_condition="stack protection is enabled",
        falsifiability={
            "observability": "inspect emitted return path",
            "determinism": "comparison exists or does not",
            "cost": "one inspection",
            "static_or_dynamic": "static",
        },
    )

    assert record.compiler == "GCC"
    assert record.version == ""
    assert record.target == ""
    assert record.mechanism == "stack-protector"


async def test_rag_pipeline_uses_injected_judge_without_building_default(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from defuzz_loop.specgen import pipeline

    source, reference = _fixture_roots(tmp_path)
    (source / "tree-object-size.cc").write_text(
        "int object_size()\n{\n  return 1;\n}\n", encoding="utf-8"
    )
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

    result = await pipeline.run_pipeline(config, judge_override=cast(Any, FakeJudge()))

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


async def test_segmented_and_grounding_requests_isolate_all_original_roots(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    (reference / "findings").mkdir(parents=True)
    backend = RecordingIsolationBackend()
    config = InvariantGenerationConfig(
        generation_path="segmented-cot",
        corpus_root=source,
        output_dir=tmp_path / "out",
        run_id="part1",
        repetition=1,
        reference_root=reference,
        segment_chars=1000,
        require_host_read_isolation=True,
    )

    result = await run_invariant_generation(config, backend=cast(Any, backend))

    assert result.result_valid is True
    assert backend.calls
    expected = [
        source.resolve(),
        reference.resolve(),
        (reference / "findings").resolve(),
    ]
    assert any(call["metadata"].get("stage") == "segmented-cot" for call in backend.calls)
    assert any(call["metadata"].get("stage") == "entailment" for call in backend.calls)
    for call in backend.calls:
        assert call["deny_read_paths"] == expected
        assert call["cwd"] not in {source.resolve(), reference.resolve()}
        assert call["cwd_entries"] == []
        assert call["require_host_read_isolation"] is True
    assert all(not call["cwd"].exists() for call in backend.calls)


@pytest.mark.parametrize("collision", ["cwd", "output"])
async def test_part_i_path_collisions_fail_before_model_invocation(
    tmp_path: Path, collision: str
) -> None:
    source, reference = _fixture_roots(tmp_path)
    backend = RecordingIsolationBackend()
    output = (source / "agent-output") if collision == "output" else tmp_path / "out"
    corpus = tmp_path if collision == "cwd" else source
    config = InvariantGenerationConfig(
        generation_path="segmented-cot",
        corpus_root=corpus,
        output_dir=output,
        run_id="collision",
        repetition=1,
        reference_root=reference,
    )

    with pytest.raises(ValueError, match="collides with agent"):
        await run_invariant_generation(config, backend=cast(Any, backend))

    assert backend.calls == []


async def test_part_i_rejects_overlapping_corpus_and_document_roots(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    backend = RecordingIsolationBackend()
    config = InvariantGenerationConfig(
        generation_path="segmented-cot",
        corpus_root=source,
        document_roots=[source / "docs"],
        output_dir=tmp_path / "out",
        run_id="overlap",
        repetition=1,
        reference_root=reference,
    )

    with pytest.raises(ValueError, match="original input roots overlap"):
        await run_invariant_generation(config, backend=cast(Any, backend))

    assert backend.calls == []


async def test_part_i_required_host_isolation_rejects_unproven_backend(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    config = InvariantGenerationConfig(
        generation_path="segmented-cot",
        corpus_root=source,
        output_dir=tmp_path / "out",
        run_id="formal",
        repetition=1,
        reference_root=reference,
        require_host_read_isolation=True,
    )

    with pytest.raises(ValueError, match="backend with host read isolation"):
        await run_invariant_generation(config, backend=cast(Any, FakeBackend()))


async def test_standalone_segmented_generation_records_full_variant_context(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    backend = RecordingIsolationBackend()
    config = InvariantGenerationConfig(
        generation_path="segmented-cot",
        corpus_root=source,
        output_dir=tmp_path / "out",
        run_id="part1",
        campaign_variant="full",
        repetition=1,
        reference_root=reference,
        segment_chars=1000,
    )

    await run_invariant_generation(config, backend=cast(Any, backend))

    assert backend.calls
    assert {call["metadata"].get("variant") for call in backend.calls} == {"full"}


async def test_without_rag_segmented_generation_records_without_rag_context(
    tmp_path: Path,
) -> None:
    source, reference = _fixture_roots(tmp_path)
    backend = RecordingIsolationBackend()
    config = InvariantGenerationConfig(
        generation_path="segmented-cot",
        corpus_root=source,
        output_dir=tmp_path / "out",
        run_id="part1",
        campaign_variant="without-rag",
        repetition=1,
        reference_root=reference,
        segment_chars=1000,
    )

    await run_invariant_generation(config, backend=cast(Any, backend))

    assert backend.calls
    assert {call["metadata"].get("variant") for call in backend.calls} == {"without-rag"}
