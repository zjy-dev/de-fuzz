"""Integration tests for ambient token accounting on live model calls."""

from __future__ import annotations

import json
from types import SimpleNamespace
from typing import Any

import pytest
from pydantic import BaseModel, ValidationError

from defuzz_loop.llm import ainvoke_structured
from defuzz_loop.specgen.judge import LLMJudge
from defuzz_loop.token_usage import (
    BudgetExceeded,
    TokenUsageContext,
    TokenUsageSink,
    current_token_usage_sink,
    normalize_external_agent_usage,
    use_token_usage,
)


class _Output(BaseModel):
    answer: str


class _Runnable:
    def __init__(self, *, response: Any = None, error: BaseException | None = None) -> None:
        self.response = response
        self.error = error
        self.messages: list[Any] = []

    async def ainvoke(self, messages: Any) -> Any:
        self.messages.append(messages)
        if self.error is not None:
            raise self.error
        return self.response


class _Model:
    def __init__(self, runnable: _Runnable) -> None:
        self.runnable = runnable
        self.structured_calls: list[tuple[type[BaseModel], dict[str, Any]]] = []

    def with_structured_output(self, output_model: type[BaseModel], **kwargs: Any) -> _Runnable:
        self.structured_calls.append((output_model, kwargs))
        return self.runnable


def _context() -> TokenUsageContext:
    return TokenUsageContext(
        run_id="run-1",
        experiment="agent-audit",
        variant="full",
        part="III",
        stage="default",
        provider="openai",
        model="test-model",
    )


def _raw_response(*, total: int = 7) -> dict[str, Any]:
    return {
        "raw": SimpleNamespace(
            usage_metadata={
                "input_tokens": 4,
                "output_tokens": 3,
                "total_tokens": total,
            }
        ),
        "parsed": _Output(answer="ok"),
        "parsing_error": None,
    }


@pytest.mark.asyncio
async def test_disabled_sink_preserves_original_structured_call_shape() -> None:
    model = _Model(_Runnable(response=_Output(answer="unchanged")))

    result = await ainvoke_structured(
        model, _Output, [("user", "hello")], stage="generate", agent="generator"
    )

    assert result == _Output(answer="unchanged")
    assert model.structured_calls == [(_Output, {"method": "function_calling"})]
    assert current_token_usage_sink() is None


@pytest.mark.asyncio
async def test_active_sink_requests_raw_usage_and_returns_parsed_model(tmp_path) -> None:
    model = _Model(_Runnable(response=_raw_response()))
    sink = TokenUsageSink(tmp_path / "usage.jsonl", context=_context())

    with use_token_usage(sink):
        assert current_token_usage_sink() is sink
        result = await ainvoke_structured(
            model,
            _Output,
            [("user", "hello")],
            stage="generate",
            agent="generator",
        )
    assert current_token_usage_sink() is None

    assert result == _Output(answer="ok")
    assert model.structured_calls == [
        (_Output, {"method": "function_calling", "include_raw": True})
    ]
    record = sink.records[0]
    assert (record.stage, record.agent) == ("generate", "generator")
    assert (record.input_tokens, record.output_tokens, record.total_tokens) == (4, 3, 7)
    assert record.success and not record.usage_missing


@pytest.mark.asyncio
async def test_parsing_error_preserves_raw_usage_and_records_failure(tmp_path) -> None:
    parsing_error = ValueError("invalid structured arguments")
    response = _raw_response(total=11)
    response["parsed"] = None
    response["parsing_error"] = parsing_error
    model = _Model(_Runnable(response=response))
    sink = TokenUsageSink(tmp_path / "usage.jsonl", context=_context())

    with (
        use_token_usage(sink),
        pytest.raises(ValueError, match="invalid structured arguments") as raised,
    ):
        await ainvoke_structured(model, _Output, [], stage="generate", agent="generator")

    assert raised.value is parsing_error
    assert len(sink.records) == 1
    record = sink.records[0]
    assert not record.success
    assert record.error_type == "ValueError"
    assert not record.usage_missing
    assert (record.input_tokens, record.output_tokens, record.total_tokens) == (4, 3, 11)


@pytest.mark.asyncio
async def test_model_validation_error_preserves_raw_usage_and_records_failure(
    tmp_path,
) -> None:
    response = _raw_response(total=13)
    response["parsed"] = {"unexpected": "field"}
    model = _Model(_Runnable(response=response))
    sink = TokenUsageSink(tmp_path / "usage.jsonl", context=_context())

    with use_token_usage(sink), pytest.raises(ValidationError):
        await ainvoke_structured(model, _Output, [], stage="generate", agent="generator")

    assert len(sink.records) == 1
    record = sink.records[0]
    assert not record.success
    assert record.error_type == "ValidationError"
    assert not record.usage_missing
    assert (record.input_tokens, record.output_tokens, record.total_tokens) == (4, 3, 13)


@pytest.mark.asyncio
async def test_call_failure_before_response_records_missing_usage_then_reraises(
    tmp_path,
) -> None:
    model = _Model(_Runnable(error=TimeoutError("provider timed out")))
    sink = TokenUsageSink(tmp_path / "usage.jsonl", context=_context())

    with use_token_usage(sink), pytest.raises(TimeoutError, match="provider timed out"):
        await ainvoke_structured(model, _Output, [], stage="summarize", agent="feedback")

    assert len(sink.records) == 1
    record = sink.records[0]
    assert not record.success
    assert record.error_type == "TimeoutError"
    assert record.usage_missing
    assert (record.input_tokens, record.output_tokens, record.total_tokens) == (
        None,
        None,
        None,
    )
    assert (record.stage, record.agent) == ("summarize", "feedback")


@pytest.mark.asyncio
async def test_budget_is_enforced_before_the_next_model_call(tmp_path) -> None:
    sink = TokenUsageSink(tmp_path / "usage.jsonl", context=_context(), token_budget=10)
    sink.record_external_usage({"type": "turn.completed", "usage": {"total_tokens": 10}})
    model = _Model(_Runnable(response=_raw_response()))

    with use_token_usage(sink), pytest.raises(BudgetExceeded) as raised:
        await ainvoke_structured(model, _Output, [], stage="generate", agent="generator")

    assert raised.value.consumed == 10
    assert raised.value.budget == 10
    assert model.structured_calls == []


def test_external_agent_envelopes_share_accounting_and_preserve_total(tmp_path) -> None:
    sink = TokenUsageSink(tmp_path / "usage.jsonl", context=_context(), token_budget=100)

    direct = sink.record_external_usage(
        {
            "type": "turn.completed",
            "usage": {
                "input_tokens": 10,
                "output_tokens": 5,
                # Provider totals are authoritative even when not input + output.
                "total_tokens": 42,
            },
        },
        context=sink.context.with_overrides(stage="audit", agent="bare-agent"),
    )
    nested = sink.record_external_usage(
        {"payload": {"turn": {"usage": {"input_tokens": 2, "output_tokens": 1}}}},
        context=sink.context.with_overrides(stage="audit", agent="bare-agent"),
    )

    assert direct.total_tokens == 42
    assert nested.total_tokens == 3
    assert sink.consumed_total_tokens == 45
    assert sink.remaining_tokens == 55


def test_unknown_external_usage_remains_missing_and_does_not_block(tmp_path) -> None:
    sink = TokenUsageSink(tmp_path / "usage.jsonl", context=_context(), token_budget=1)
    record = sink.record_external_usage(
        {"type": "turn.completed", "usage": {}},
        context=sink.context.with_overrides(stage="audit", agent="external"),
    )

    assert record.usage_missing
    assert sink.consumed_total_tokens is None
    assert sink.remaining_tokens is None
    sink.check_budget()
    rows = sink.finalize(
        json_path=tmp_path / "summary.json",
        csv_path=tmp_path / "summary.csv",
        group_by=("run_id", "stage"),
    )
    assert rows[0]["usage_missing_count"] == 1
    assert rows[0]["total_tokens"] is None
    assert json.loads((tmp_path / "summary.json").read_text()) == rows
    assert (tmp_path / "summary.csv").is_file()


def test_normalize_external_usage_accepts_nested_turn_envelope() -> None:
    usage = normalize_external_agent_usage(
        {
            "data": {
                "payload": {
                    "turn": {
                        "usage": {
                            "prompt_tokens": 8,
                            "completion_tokens": 2,
                            "total_tokens": 17,
                        }
                    }
                }
            }
        }
    )

    assert usage["input_tokens"] == 8
    assert usage["output_tokens"] == 2
    assert usage["total_tokens"] == 17


@pytest.mark.asyncio
async def test_specgen_uses_task_and_key_as_stable_context(tmp_path) -> None:
    model = _Model(_Runnable(response=_raw_response()))
    judge = LLMJudge.__new__(LLMJudge)
    judge._model = model
    context = _context().with_overrides(part="I")
    sink = TokenUsageSink(tmp_path / "usage.jsonl", context=context)

    with use_token_usage(sink):
        result = await judge.complete(
            task="analogy",
            key="seed-1:chunk-2",
            system="system",
            user="user",
            output_model=_Output,
        )

    assert result == _Output(answer="ok")
    assert (sink.records[0].stage, sink.records[0].agent) == (
        "analogy",
        "seed-1:chunk-2",
    )
