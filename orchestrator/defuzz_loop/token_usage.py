"""Provider-neutral token usage collection and aggregation.

The module deliberately has no dependency on LangChain or a provider SDK.  It
accepts ordinary mappings as well as response objects with attributes, and
normalizes the two response shapes currently used by LangChain/OpenAI-style
clients.  Missing usage remains distinguishable from a real zero-token value.
"""

from __future__ import annotations

import csv
import json
import math
import os
import threading
import uuid
from collections.abc import Iterable, Iterator, Mapping, Sequence
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass, field, fields, replace
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, ClassVar

PathLike = str | os.PathLike[str]

DEFAULT_GROUP_BY = ("run_id", "part", "stage", "model")

_INPUT_KEYS = ("input_tokens", "prompt_tokens", "input_token_count")
_OUTPUT_KEYS = ("output_tokens", "completion_tokens", "output_token_count")
_TOTAL_KEYS = ("total_tokens", "total_token_count")
_CACHED_KEYS = (
    "cached_input_tokens",
    "cached_tokens",
    "cache_read_input_tokens",
    "cache_read_tokens",
)
_CACHE_CREATION_KEYS = (
    "cache_creation_input_tokens",
    "cache_creation_tokens",
)
_REASONING_KEYS = ("reasoning_tokens", "reasoning_output_tokens")
_INPUT_DETAIL_KEYS = ("input_token_details", "prompt_tokens_details")
_OUTPUT_DETAIL_KEYS = ("output_token_details", "completion_tokens_details")
_NESTED_CACHED_KEYS = (
    "cached_tokens",
    "cache_read",
    "cache_read_tokens",
    "cache_read_input_tokens",
)
_NESTED_REASONING_KEYS = ("reasoning", "reasoning_tokens")
_NESTED_CACHE_CREATION_KEYS = (
    "cache_creation",
    "cache_creation_tokens",
    "cache_creation_input_tokens",
)
_TOKEN_FIELDS = (
    "input_tokens",
    "output_tokens",
    "total_tokens",
    "cached_input_tokens",
    "cache_creation_input_tokens",
    "reasoning_tokens",
)

_LOCKS_GUARD = threading.Lock()
_PATH_LOCKS: dict[str, threading.Lock] = {}
_EXTERNAL_WRAPPER_KEYS = ("payload", "data", "event", "turn", "result", "response")


def _utc_timestamp() -> str:
    return datetime.now(UTC).isoformat(timespec="milliseconds")


def _path_lock(path: PathLike) -> threading.Lock:
    key = str(Path(path).expanduser().resolve(strict=False))
    with _LOCKS_GUARD:
        return _PATH_LOCKS.setdefault(key, threading.Lock())


def _get(container: Any, key: str) -> Any:
    if container is None:
        return None
    if isinstance(container, Mapping):
        return container.get(key)
    return getattr(container, key, None)


def _first(container: Any, keys: Sequence[str]) -> Any:
    for key in keys:
        value = _get(container, key)
        if value is not None:
            return value
    return None


def _first_across(containers: Sequence[Any], keys: Sequence[str]) -> Any:
    for container in containers:
        value = _first(container, keys)
        if value is not None:
            return value
    return None


def _looks_like_usage(value: Any) -> bool:
    usage_keys = (
        _INPUT_KEYS
        + _OUTPUT_KEYS
        + _TOTAL_KEYS
        + _CACHED_KEYS
        + _CACHE_CREATION_KEYS
        + _REASONING_KEYS
    )
    return any(_get(value, key) is not None for key in usage_keys)


def _usage_candidates(response: Any) -> list[Any]:
    """Return usage containers in preference order without importing an SDK."""

    # LangChain structured output with ``include_raw=True`` returns a mapping
    # containing the provider AIMessage under ``raw``. Supporting that shape now
    # lets callers wire this module later without changing its extraction API.
    wrapped = _get(response, "raw")
    if wrapped is not None:
        response = wrapped

    candidates: list[Any] = []

    def add(value: Any) -> None:
        if value is not None and not any(value is existing for existing in candidates):
            candidates.append(value)

    # LangChain's normalized shape is preferred when it is present.
    add(_get(response, "usage_metadata"))

    response_metadata = _get(response, "response_metadata")
    add(_get(response_metadata, "token_usage"))
    add(_get(response_metadata, "usage"))

    # Raw OpenAI-compatible response objects and dictionaries use either name.
    add(_get(response, "token_usage"))
    add(_get(response, "usage"))
    if _looks_like_usage(response):
        add(response)
    return candidates


def _nested_value(
    containers: Sequence[Any], detail_keys: Sequence[str], value_keys: Sequence[str]
) -> Any:
    for container in containers:
        for detail_key in detail_keys:
            details = _get(container, detail_key)
            value = _first(details, value_keys)
            if value is not None:
                return value
    return None


def _validate_token(name: str, value: Any) -> int | None:
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an integer or None")
    if value < 0:
        raise ValueError(f"{name} must be non-negative")
    return value


def normalize_token_usage(response: Any) -> dict[str, int | bool | None]:
    """Extract a canonical usage mapping from a provider response.

    Supported inputs include LangChain ``usage_metadata``, LangChain
    ``response_metadata.token_usage``, raw OpenAI-style ``usage`` objects, and
    those usage dictionaries directly. Nested cache/reasoning details are
    normalized as well. If no usage is exposed, every token field is ``None``
    and ``usage_missing`` is ``True``; zero is never invented.

    ``total_tokens`` is inferred only when both input and output counts exist.
    An explicitly supplied provider total is preserved because providers may
    account for cached or reasoning tokens differently.
    """

    candidates = _usage_candidates(response)
    raw: dict[str, Any] = {
        "input_tokens": _first_across(candidates, _INPUT_KEYS),
        "output_tokens": _first_across(candidates, _OUTPUT_KEYS),
        "total_tokens": _first_across(candidates, _TOTAL_KEYS),
        "cached_input_tokens": _first_across(candidates, _CACHED_KEYS),
        "cache_creation_input_tokens": _first_across(
            candidates, _CACHE_CREATION_KEYS
        ),
        "reasoning_tokens": _first_across(candidates, _REASONING_KEYS),
    }
    if raw["cached_input_tokens"] is None:
        raw["cached_input_tokens"] = _nested_value(
            candidates, _INPUT_DETAIL_KEYS, _NESTED_CACHED_KEYS
        )
    if raw["reasoning_tokens"] is None:
        raw["reasoning_tokens"] = _nested_value(
            candidates, _OUTPUT_DETAIL_KEYS, _NESTED_REASONING_KEYS
        )
    if raw["cache_creation_input_tokens"] is None:
        raw["cache_creation_input_tokens"] = _nested_value(
            candidates, _INPUT_DETAIL_KEYS, _NESTED_CACHE_CREATION_KEYS
        )

    normalized = {name: _validate_token(name, raw[name]) for name in _TOKEN_FIELDS}
    input_tokens = normalized["input_tokens"]
    output_tokens = normalized["output_tokens"]
    total_tokens = normalized["total_tokens"]
    if input_tokens is not None and output_tokens is not None:
        inferred_total = input_tokens + output_tokens
        if total_tokens is None:
            normalized["total_tokens"] = inferred_total

    normalized["usage_missing"] = all(normalized[name] is None for name in _TOKEN_FIELDS)
    return normalized


# A discoverable synonym for callers that think of this operation as extraction.
extract_token_usage = normalize_token_usage


def normalize_external_agent_usage(payload: Any) -> dict[str, int | bool | None]:
    """Extract usage from a completed external-agent event or envelope.

    Agent runtimes commonly emit either ``{"type": "turn.completed",
    "usage": ...}`` or wrap the turn below ``payload``/``data``. The walk is
    intentionally shallow and key-based: it accepts those envelopes without
    treating arbitrary nested application data as token accounting. Direct
    provider usage mappings are accepted as well.
    """

    queue = [payload]
    seen: set[int] = set()
    while queue:
        candidate = queue.pop(0)
        marker = id(candidate)
        if marker in seen:
            continue
        seen.add(marker)

        usage = normalize_token_usage(candidate)
        if not usage["usage_missing"]:
            return usage

        for key in _EXTERNAL_WRAPPER_KEYS:
            nested = _get(candidate, key)
            if nested is not None:
                queue.append(nested)

    return normalize_token_usage(None)


@dataclass(frozen=True, slots=True)
class TokenUsageRecord:
    """One provider call and its experimental provenance.

    Context fields identify the run and call site. Token fields remain optional
    because providers may omit usage, in which case ``usage_missing`` is true.
    Counts, latency, and cost are validated as non-negative.
    """

    run_id: str
    experiment: str
    variant: str
    part: str
    stage: str
    schema_version: int = 1
    call_id: str = field(default_factory=lambda: uuid.uuid4().hex)
    agent: str | None = None
    provider: str | None = None
    model: str | None = None
    input_tokens: int | None = None
    output_tokens: int | None = None
    total_tokens: int | None = None
    cached_input_tokens: int | None = None
    cache_creation_input_tokens: int | None = None
    reasoning_tokens: int | None = None
    latency_ms: float | None = None
    success: bool = True
    error_type: str | None = None
    estimated_cost: float | None = None
    usage_missing: bool | None = None
    timestamp: str = field(default_factory=_utc_timestamp)

    REQUIRED_CONTEXT: ClassVar[tuple[str, ...]] = (
        "run_id",
        "experiment",
        "variant",
        "part",
        "stage",
    )

    def __post_init__(self) -> None:
        if self.schema_version != 1:
            raise ValueError("schema_version must be 1")
        if not isinstance(self.call_id, str) or not self.call_id.strip():
            raise ValueError("call_id must be a non-empty string")
        for name in self.REQUIRED_CONTEXT:
            value = getattr(self, name)
            if not isinstance(value, str) or not value.strip():
                raise ValueError(f"{name} must be a non-empty string")
        for name in ("agent", "provider", "model", "error_type"):
            value = getattr(self, name)
            if value is not None and not isinstance(value, str):
                raise TypeError(f"{name} must be a string or None")

        for name in _TOKEN_FIELDS:
            object.__setattr__(self, name, _validate_token(name, getattr(self, name)))

        if self.input_tokens is not None and self.output_tokens is not None:
            inferred_total = self.input_tokens + self.output_tokens
            if self.total_tokens is None:
                object.__setattr__(self, "total_tokens", inferred_total)

        self._validate_non_negative_number("latency_ms", self.latency_ms)
        self._validate_non_negative_number("estimated_cost", self.estimated_cost)
        if not isinstance(self.success, bool):
            raise TypeError("success must be a bool")

        missing = all(getattr(self, name) is None for name in _TOKEN_FIELDS)
        if self.usage_missing is None:
            object.__setattr__(self, "usage_missing", missing)
        elif not isinstance(self.usage_missing, bool):
            raise TypeError("usage_missing must be a bool or None")
        elif self.usage_missing != missing:
            raise ValueError("usage_missing must be true exactly when all usage fields are missing")

        if not isinstance(self.timestamp, str) or not self.timestamp.strip():
            raise ValueError("timestamp must be a non-empty ISO-8601 string")
        try:
            datetime.fromisoformat(self.timestamp.replace("Z", "+00:00"))
        except ValueError as exc:
            raise ValueError("timestamp must be an ISO-8601 string") from exc

    @staticmethod
    def _validate_non_negative_number(name: str, value: Any) -> None:
        if value is None:
            return
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TypeError(f"{name} must be a number or None")
        if not math.isfinite(value) or value < 0:
            raise ValueError(f"{name} must be finite and non-negative")

    @classmethod
    def from_response(
        cls,
        response: Any,
        *,
        run_id: str,
        experiment: str,
        variant: str,
        part: str,
        stage: str,
        agent: str | None = None,
        provider: str | None = None,
        model: str | None = None,
        latency_ms: float | None = None,
        success: bool = True,
        error_type: str | None = None,
        estimated_cost: float | None = None,
        timestamp: str | None = None,
    ) -> TokenUsageRecord:
        """Build a record by combining call context with normalized usage."""

        usage = normalize_token_usage(response)
        values: dict[str, Any] = {
            "run_id": run_id,
            "experiment": experiment,
            "variant": variant,
            "part": part,
            "stage": stage,
            "agent": agent,
            "provider": provider,
            "model": model,
            "latency_ms": latency_ms,
            "success": success,
            "error_type": error_type,
            "estimated_cost": estimated_cost,
            **usage,
        }
        if timestamp is not None:
            values["timestamp"] = timestamp
        return cls(**values)

    def to_dict(self) -> dict[str, Any]:
        """Return a JSON-serializable representation in schema order."""

        return {item.name: getattr(self, item.name) for item in fields(self)}

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> TokenUsageRecord:
        """Validate and deserialize a record mapping."""

        if not isinstance(value, Mapping):
            raise TypeError("record must be a mapping")
        known = {item.name for item in fields(cls)}
        unknown = set(value) - known
        if unknown:
            raise ValueError(f"unknown token usage fields: {sorted(unknown)}")
        return cls(**dict(value))


def _coerce_record(value: TokenUsageRecord | Mapping[str, Any]) -> TokenUsageRecord:
    if isinstance(value, TokenUsageRecord):
        return value
    if isinstance(value, Mapping):
        return TokenUsageRecord.from_dict(value)
    raise TypeError("expected TokenUsageRecord or record mapping")


def append_jsonl(path: PathLike, record: TokenUsageRecord | Mapping[str, Any]) -> None:
    """Append one validated record as a JSON line, safely across threads.

    The lock is keyed by the resolved path, so separate writer instances in the
    same process cannot interleave writes to that file. Parent directories are
    created on demand.
    """

    destination = Path(path)
    value = _coerce_record(record)
    line = json.dumps(value.to_dict(), ensure_ascii=False, separators=(",", ":")) + "\n"
    lock = _path_lock(destination)
    with lock:
        destination.parent.mkdir(parents=True, exist_ok=True)
        with destination.open("a", encoding="utf-8") as stream:
            stream.write(line)


def read_jsonl(path: PathLike) -> list[TokenUsageRecord]:
    """Read and validate all records from ``path``; a missing file is empty."""

    source = Path(path)
    lock = _path_lock(source)
    with lock:
        if not source.exists():
            return []
        lines = source.read_text(encoding="utf-8").splitlines()

    records: list[TokenUsageRecord] = []
    for line_number, line in enumerate(lines, start=1):
        if not line.strip():
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"invalid JSON in {source} at line {line_number}") from exc
        if not isinstance(value, Mapping):
            raise ValueError(f"record in {source} at line {line_number} is not an object")
        try:
            records.append(TokenUsageRecord.from_dict(value))
        except (TypeError, ValueError) as exc:
            raise ValueError(f"invalid record in {source} at line {line_number}: {exc}") from exc
    return records


@dataclass(frozen=True, slots=True)
class TokenUsageContext:
    """Experimental provenance inherited by calls recorded through a sink."""

    run_id: str
    experiment: str
    variant: str
    part: str
    stage: str
    agent: str | None = None
    provider: str | None = None
    model: str | None = None

    def __post_init__(self) -> None:
        for name in TokenUsageRecord.REQUIRED_CONTEXT:
            value = getattr(self, name)
            if not isinstance(value, str) or not value.strip():
                raise ValueError(f"{name} must be a non-empty string")
        for name in ("agent", "provider", "model"):
            value = getattr(self, name)
            if value is not None and (not isinstance(value, str) or not value.strip()):
                raise ValueError(f"{name} must be a non-empty string or None")

    def with_overrides(self, **overrides: Any) -> TokenUsageContext:
        """Return a per-call context without mutating the sink default."""

        known = {item.name for item in fields(self)}
        unknown = set(overrides) - known
        if unknown:
            raise ValueError(f"unknown token usage context fields: {sorted(unknown)}")
        return replace(self, **overrides)


class BudgetExceeded(RuntimeError):
    """Raised before a model call when the observed token budget is spent."""

    def __init__(self, *, consumed: int, budget: int) -> None:
        self.consumed = consumed
        self.budget = budget
        super().__init__(
            f"token budget exceeded: consumed {consumed} of {budget} tokens"
        )


class TokenUsageSink:
    """Append-only usage collector shared by LangChain and external agents.

    Budget enforcement is intentionally based only on provider-reported
    ``total_tokens``. Calls with missing usage stay visible in records and
    summaries but do not invent a zero-token measurement.
    """

    def __init__(
        self,
        path: PathLike,
        *,
        context: TokenUsageContext,
        token_budget: int | None = None,
    ) -> None:
        if token_budget is not None:
            if isinstance(token_budget, bool) or not isinstance(token_budget, int):
                raise TypeError("token_budget must be an integer or None")
            if token_budget <= 0:
                raise ValueError("token_budget must be greater than zero")
        self.path = Path(path)
        self.context = context
        self.token_budget = token_budget
        self._records = read_jsonl(self.path)
        self._guard = threading.Lock()

    @property
    def records(self) -> tuple[TokenUsageRecord, ...]:
        """Return an immutable snapshot of records observed by this sink."""

        with self._guard:
            return tuple(self._records)

    @property
    def consumed_total_tokens(self) -> int | None:
        """Return known provider totals, or ``None`` when all usage is missing."""

        with self._guard:
            known = [
                record.total_tokens
                for record in self._records
                if record.total_tokens is not None
            ]
            if known:
                return sum(known)
            return None if self._records else 0

    @property
    def remaining_tokens(self) -> int | None:
        if self.token_budget is None:
            return None
        consumed = self.consumed_total_tokens
        if consumed is None:
            return None
        return max(self.token_budget - consumed, 0)

    def check_budget(self) -> None:
        """Reject the *next* model call once known usage reaches the budget."""

        if self.token_budget is None:
            return
        consumed = self.consumed_total_tokens
        if consumed is None:
            return
        if consumed >= self.token_budget:
            raise BudgetExceeded(consumed=consumed, budget=self.token_budget)

    def _append(self, record: TokenUsageRecord) -> TokenUsageRecord:
        with self._guard:
            append_jsonl(self.path, record)
            self._records.append(record)
        return record

    def record_response(
        self,
        response: Any,
        *,
        context: TokenUsageContext | None = None,
        latency_ms: float | None = None,
    ) -> TokenUsageRecord:
        """Record a successful provider response and return the stored record."""

        selected = context or self.context
        return self._append(
            TokenUsageRecord.from_response(
                response,
                run_id=selected.run_id,
                experiment=selected.experiment,
                variant=selected.variant,
                part=selected.part,
                stage=selected.stage,
                agent=selected.agent,
                provider=selected.provider,
                model=selected.model,
                latency_ms=latency_ms,
            )
        )

    def record_failure(
        self,
        error: BaseException,
        *,
        response: Any = None,
        context: TokenUsageContext | None = None,
        latency_ms: float | None = None,
    ) -> TokenUsageRecord:
        """Record a failed call, preserving usage from any received response.

        Provider calls can succeed at the transport layer and still fail while
        parsing or validating structured output.  Those calls consumed real
        tokens, so callers pass the raw response here instead of turning the
        usage into an artificial missing measurement.  ``response=None`` is
        reserved for failures that occurred before a response was available.
        """

        selected = context or self.context
        return self._append(
            TokenUsageRecord.from_response(
                response,
                run_id=selected.run_id,
                experiment=selected.experiment,
                variant=selected.variant,
                part=selected.part,
                stage=selected.stage,
                agent=selected.agent,
                provider=selected.provider,
                model=selected.model,
                latency_ms=latency_ms,
                success=False,
                error_type=type(error).__name__,
            )
        )

    def record_external_usage(
        self,
        payload: Any,
        *,
        context: TokenUsageContext | None = None,
        latency_ms: float | None = None,
        success: bool = True,
        error_type: str | None = None,
    ) -> TokenUsageRecord:
        """Record usage from an external agent ``turn.completed`` event."""

        usage = normalize_external_agent_usage(payload)
        selected = context or self.context
        return self._append(
            TokenUsageRecord(
                run_id=selected.run_id,
                experiment=selected.experiment,
                variant=selected.variant,
                part=selected.part,
                stage=selected.stage,
                agent=selected.agent,
                provider=selected.provider,
                model=selected.model,
                input_tokens=usage["input_tokens"],  # type: ignore[arg-type]
                output_tokens=usage["output_tokens"],  # type: ignore[arg-type]
                total_tokens=usage["total_tokens"],  # type: ignore[arg-type]
                cached_input_tokens=usage["cached_input_tokens"],  # type: ignore[arg-type]
                cache_creation_input_tokens=usage["cache_creation_input_tokens"],  # type: ignore[arg-type]
                reasoning_tokens=usage["reasoning_tokens"],  # type: ignore[arg-type]
                usage_missing=usage["usage_missing"],  # type: ignore[arg-type]
                latency_ms=latency_ms,
                success=success,
                error_type=error_type,
            )
        )

    def finalize(
        self,
        *,
        json_path: PathLike | None = None,
        csv_path: PathLike | None = None,
        group_by: Sequence[str] = DEFAULT_GROUP_BY,
    ) -> list[dict[str, Any]]:
        """Build summary rows and optionally atomically write JSON and CSV."""

        records = self.records
        rows = aggregate_usage(records, group_by=group_by)
        if json_path is not None:
            write_aggregate_json(json_path, records, group_by=group_by)
        if csv_path is not None:
            write_aggregate_csv(csv_path, records, group_by=group_by)
        return rows


_CURRENT_TOKEN_USAGE_SINK: ContextVar[TokenUsageSink | None] = ContextVar(
    "defuzz_token_usage_sink", default=None
)


def current_token_usage_sink() -> TokenUsageSink | None:
    """Return the sink active in the current async/thread context, if any."""

    return _CURRENT_TOKEN_USAGE_SINK.get()


@contextmanager
def use_token_usage(sink: TokenUsageSink | None) -> Iterator[TokenUsageSink | None]:
    """Install ``sink`` for nested model calls and restore it on exit."""

    token = _CURRENT_TOKEN_USAGE_SINK.set(sink)
    try:
        yield sink
    finally:
        _CURRENT_TOKEN_USAGE_SINK.reset(token)


_AGGREGATE_METRICS = (
    "call_count",
    "success_count",
    "failure_count",
    "usage_missing_count",
    "input_tokens",
    "output_tokens",
    "total_tokens",
    "cached_input_tokens",
    "cache_creation_input_tokens",
    "reasoning_tokens",
    "total_latency_ms",
    "average_latency_ms",
    "estimated_cost",
)


def _validate_group_by(group_by: Sequence[str]) -> tuple[str, ...]:
    if isinstance(group_by, (str, bytes)):
        raise TypeError("group_by must be a sequence of field names, not a string")
    names = tuple(group_by)
    if len(set(names)) != len(names):
        raise ValueError("group_by contains duplicate fields")
    record_fields = {item.name for item in fields(TokenUsageRecord)}
    unknown = set(names) - record_fields
    if unknown:
        raise ValueError(f"unknown group_by fields: {sorted(unknown)}")
    return names


def aggregate_usage(
    records: Iterable[TokenUsageRecord | Mapping[str, Any]],
    group_by: Sequence[str] = DEFAULT_GROUP_BY,
) -> list[dict[str, Any]]:
    """Aggregate records by configurable schema fields.

    Known numeric values are summed. If a metric is absent from every record in
    a group its aggregate is ``None``, not zero. ``usage_missing_count`` makes
    partial aggregates explicit. Passing an empty ``group_by`` produces a single
    global group. Groups retain first-seen order for reproducible output.
    """

    grouping = _validate_group_by(group_by)
    grouped: dict[tuple[Any, ...], list[TokenUsageRecord]] = {}
    for value in records:
        record = _coerce_record(value)
        key = tuple(getattr(record, name) for name in grouping)
        grouped.setdefault(key, []).append(record)

    result: list[dict[str, Any]] = []
    ordered_groups = sorted(
        grouped.items(), key=lambda item: tuple("" if v is None else str(v) for v in item[0])
    )
    for key, members in ordered_groups:
        row = dict(zip(grouping, key, strict=True))
        row["call_count"] = len(members)
        row["success_count"] = sum(record.success for record in members)
        row["failure_count"] = len(members) - row["success_count"]
        row["usage_missing_count"] = sum(bool(record.usage_missing) for record in members)

        for name in _TOKEN_FIELDS:
            known = [
                getattr(record, name)
                for record in members
                if getattr(record, name) is not None
            ]
            row[name] = sum(known) if known else None

        latencies = [record.latency_ms for record in members if record.latency_ms is not None]
        row["total_latency_ms"] = math.fsum(latencies) if latencies else None
        row["average_latency_ms"] = (
            math.fsum(latencies) / len(latencies) if latencies else None
        )
        costs = [record.estimated_cost for record in members if record.estimated_cost is not None]
        row["estimated_cost"] = math.fsum(costs) if costs else None
        result.append(row)
    return result


def _aggregate_rows(
    records: Iterable[TokenUsageRecord | Mapping[str, Any]], group_by: Sequence[str]
) -> tuple[tuple[str, ...], list[dict[str, Any]]]:
    grouping = _validate_group_by(group_by)
    return grouping, aggregate_usage(records, grouping)


def _atomic_write_text(destination: Path, content: str) -> None:
    """Atomically replace a summary file after writing it completely."""

    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(f".{destination.name}.{uuid.uuid4().hex}.tmp")
    try:
        temporary.write_text(content, encoding="utf-8")
        os.replace(temporary, destination)
    finally:
        temporary.unlink(missing_ok=True)


def write_aggregate_json(
    path: PathLike,
    records: Iterable[TokenUsageRecord | Mapping[str, Any]],
    group_by: Sequence[str] = DEFAULT_GROUP_BY,
) -> list[dict[str, Any]]:
    """Aggregate ``records``, write a JSON array, and return its rows."""

    _, rows = _aggregate_rows(records, group_by)
    destination = Path(path)
    _atomic_write_text(destination, json.dumps(rows, ensure_ascii=False, indent=2) + "\n")
    return rows


def write_aggregate_csv(
    path: PathLike,
    records: Iterable[TokenUsageRecord | Mapping[str, Any]],
    group_by: Sequence[str] = DEFAULT_GROUP_BY,
) -> list[dict[str, Any]]:
    """Aggregate ``records``, write a CSV table, and return its rows."""

    grouping, rows = _aggregate_rows(records, group_by)
    destination = Path(path)
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(f".{destination.name}.{uuid.uuid4().hex}.tmp")
    try:
        stream = temporary.open("w", encoding="utf-8", newline="")
        with stream:
            writer = csv.DictWriter(stream, fieldnames=[*grouping, *_AGGREGATE_METRICS])
            writer.writeheader()
            writer.writerows(rows)
        os.replace(temporary, destination)
    finally:
        temporary.unlink(missing_ok=True)
    return rows
