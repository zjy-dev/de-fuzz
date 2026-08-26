"""Tests for provider-neutral token usage statistics."""

from __future__ import annotations

import csv
import json
from concurrent.futures import ThreadPoolExecutor
from types import SimpleNamespace

import pytest

from defuzz_loop.token_usage import (
    TokenUsageRecord,
    aggregate_usage,
    append_jsonl,
    normalize_token_usage,
    read_jsonl,
    write_aggregate_csv,
    write_aggregate_json,
)


def _record(**overrides: object) -> TokenUsageRecord:
    values: dict[str, object] = {
        "run_id": "run-1",
        "experiment": "main",
        "variant": "full",
        "part": "I",
        "stage": "distillation",
        "agent": "researcher",
        "provider": "provider-a",
        "model": "model-a",
        "input_tokens": 10,
        "output_tokens": 4,
        "latency_ms": 20.0,
        "estimated_cost": 0.01,
        "timestamp": "2026-08-26T10:00:00+00:00",
    }
    values.update(overrides)
    return TokenUsageRecord(**values)  # type: ignore[arg-type]


def test_normalizes_langchain_usage_metadata_and_nested_details() -> None:
    response = SimpleNamespace(
        usage_metadata={
            "input_tokens": 120,
            "output_tokens": 30,
            "total_tokens": 150,
            "input_token_details": {"cache_read": 45, "cache_creation": 8},
            "output_token_details": {"reasoning": 12},
        }
    )

    assert normalize_token_usage(response) == {
        "input_tokens": 120,
        "output_tokens": 30,
        "total_tokens": 150,
        "cached_input_tokens": 45,
        "cache_creation_input_tokens": 8,
        "reasoning_tokens": 12,
        "usage_missing": False,
    }


def test_normalizes_response_metadata_openai_usage_and_infers_total() -> None:
    response = {
        "response_metadata": {
            "token_usage": {
                "prompt_tokens": 80,
                "completion_tokens": 20,
                "prompt_tokens_details": {"cached_tokens": 16},
                "completion_tokens_details": {"reasoning_tokens": 7},
            }
        }
    }

    usage = normalize_token_usage(response)

    assert usage["input_tokens"] == 80
    assert usage["output_tokens"] == 20
    assert usage["total_tokens"] == 100
    assert usage["cached_input_tokens"] == 16
    assert usage["reasoning_tokens"] == 7
    assert usage["usage_missing"] is False


def test_normalizes_raw_openai_usage_object() -> None:
    response = SimpleNamespace(
        usage=SimpleNamespace(prompt_tokens=5, completion_tokens=2, total_tokens=7)
    )

    assert normalize_token_usage(response)["total_tokens"] == 7


def test_normalizes_include_raw_anthropic_usage() -> None:
    response = {
        "raw": SimpleNamespace(
            response_metadata={
                "usage": {
                    "input_tokens": 9,
                    "output_tokens": 3,
                    "cache_read_input_tokens": 4,
                    "cache_creation_input_tokens": 2,
                }
            }
        ),
        "parsed": {"ignored": True},
    }

    usage = normalize_token_usage(response)

    assert usage["total_tokens"] == 12
    assert usage["cached_input_tokens"] == 4
    assert usage["cache_creation_input_tokens"] == 2


def test_missing_usage_is_explicit_and_never_becomes_zero() -> None:
    usage = normalize_token_usage({"content": "no usage returned"})

    assert usage["usage_missing"] is True
    assert all(usage[name] is None for name in (
        "input_tokens",
        "output_tokens",
        "total_tokens",
        "cached_input_tokens",
        "cache_creation_input_tokens",
        "reasoning_tokens",
    ))

    record = TokenUsageRecord.from_response(
        {},
        run_id="r",
        experiment="e",
        variant="v",
        part="III",
        stage="audit",
    )
    assert record.usage_missing is True
    assert record.total_tokens is None


def test_jsonl_append_and_read_are_thread_safe(tmp_path) -> None:
    path = tmp_path / "nested" / "usage.jsonl"
    records = [_record(run_id=f"run-{index}") for index in range(40)]

    with ThreadPoolExecutor(max_workers=8) as executor:
        list(executor.map(lambda record: append_jsonl(path, record), records))

    restored = read_jsonl(path)
    assert len(restored) == len(records)
    assert {record.run_id for record in restored} == {record.run_id for record in records}
    assert len({record.call_id for record in restored}) == len(records)
    assert all(json.loads(line)["total_tokens"] == 14 for line in path.read_text().splitlines())
    assert read_jsonl(tmp_path / "absent.jsonl") == []


def test_aggregate_by_configurable_fields_preserves_missing_metrics() -> None:
    records = [
        _record(input_tokens=10, output_tokens=4, latency_ms=20.0, estimated_cost=0.01),
        _record(input_tokens=5, output_tokens=1, latency_ms=10.0, estimated_cost=0.02),
        _record(
            input_tokens=None,
            output_tokens=None,
            latency_ms=None,
            estimated_cost=None,
            success=False,
            error_type="timeout",
        ),
        _record(
            run_id="run-2",
            input_tokens=None,
            output_tokens=None,
            latency_ms=None,
            estimated_cost=None,
        ),
    ]

    rows = aggregate_usage(records, group_by=("run_id", "model"))

    first = rows[0]
    assert first["run_id"] == "run-1"
    assert first["call_count"] == 3
    assert first["success_count"] == 2
    assert first["failure_count"] == 1
    assert first["usage_missing_count"] == 1
    assert first["input_tokens"] == 15
    assert first["output_tokens"] == 5
    assert first["total_tokens"] == 20
    assert first["total_latency_ms"] == 30.0
    assert first["average_latency_ms"] == 15.0
    assert first["estimated_cost"] == pytest.approx(0.03)

    second = rows[1]
    assert second["usage_missing_count"] == 1
    assert second["input_tokens"] is None
    assert second["total_tokens"] is None
    assert second["estimated_cost"] is None


def test_writes_aggregate_json_and_csv(tmp_path) -> None:
    records = [_record(), _record(input_tokens=1, output_tokens=2)]
    json_path = tmp_path / "out" / "usage.json"
    csv_path = tmp_path / "out" / "usage.csv"

    expected = write_aggregate_json(json_path, records, group_by=("run_id",))
    assert json.loads(json_path.read_text()) == expected

    assert write_aggregate_csv(csv_path, records, group_by=("run_id",)) == expected
    with csv_path.open(newline="") as stream:
        rows = list(csv.DictReader(stream))
    assert rows[0]["run_id"] == "run-1"
    assert rows[0]["call_count"] == "2"
    assert rows[0]["total_tokens"] == "17"


@pytest.mark.parametrize(
    ("kwargs", "exception"),
    [
        ({"input_tokens": -1}, ValueError),
        ({"input_tokens": True}, TypeError),
        ({"latency_ms": -0.1}, ValueError),
        ({"estimated_cost": float("inf")}, ValueError),
        ({"usage_missing": True}, ValueError),
    ],
)
def test_record_rejects_invalid_values(kwargs, exception) -> None:
    with pytest.raises(exception):
        _record(**kwargs)


def test_invalid_extraction_and_io_inputs_raise_clear_errors(tmp_path) -> None:
    usage = normalize_token_usage(
        {"usage": {"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 4}}
    )
    assert usage["total_tokens"] == 4
    with pytest.raises(ValueError, match="unknown group_by"):
        aggregate_usage([_record()], group_by=("not_a_field",))
    with pytest.raises(TypeError, match="not a string"):
        aggregate_usage([_record()], group_by="run_id")

    bad_path = tmp_path / "bad.jsonl"
    bad_path.write_text("not json\n")
    with pytest.raises(ValueError, match="line 1"):
        read_jsonl(bad_path)
