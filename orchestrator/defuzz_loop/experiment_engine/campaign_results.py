"""Deterministic campaign result tables for the typed experiment pipeline.

The long-form table intentionally keeps one row for every stage, including
failed and skipped stages.  The comparison table is derived separately and
only admits complete, valid lane repetitions with a non-missing value for the
metric being summarized.
"""

from __future__ import annotations

import csv
import hashlib
import io
import json
import math
import os
import statistics
import uuid
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

from defuzz_loop.token_usage import read_jsonl

CAMPAIGN_RESULTS_JSON = "campaign-results.json"
CAMPAIGN_RESULTS_CSV = "campaign-results.csv"
CAMPAIGN_COMPARISON_JSON = "campaign-comparison.json"
CAMPAIGN_COMPARISON_CSV = "campaign-comparison.csv"

CAMPAIGN_RESULTS_SCHEMA = "defuzz.campaign-stage-results.v1"
CAMPAIGN_COMPARISON_SCHEMA = "defuzz.campaign-comparison.v1"

_PARTS = ("part_i", "part_ii", "part_iii")
_STAGE_METRICS = (
    "accepted_invariants",
    "first_passed",
    "first_failed",
    "final_passed",
    "final_failed",
    "candidates",
    "admitted",
    "admission_rejected",
    "verified",
    "verification_rejected",
    "invalid",
    "unverified",
    "demo_parity_recall",
    "demo_parity_profile",
    "demo_parity_superset_coverage",
    "time_to_first_verified_ms",
)
_NUMERIC_STAGE_METRICS = tuple(
    metric for metric in _STAGE_METRICS if metric != "demo_parity_profile"
)
_COST_METRICS = (
    "actual_total_tokens",
    "attributed_total_tokens",
    "usage_missing_count",
    "llm_latency_ms",
    "elapsed_ms",
)
_COMPARISON_METRICS = (*_NUMERIC_STAGE_METRICS, *_COST_METRICS)

LONG_FORM_COLUMNS = (
    "target",
    "variant",
    "repetition",
    "part",
    "execution_status",
    "result_valid",
    "outcome",
    "repetition_valid",
    "reused",
    "reused_from_variant",
    *_STAGE_METRICS,
    *_COST_METRICS,
    "metrics_json",
)
COMPARISON_COLUMNS = (
    "target",
    "variant",
    "metric",
    "total_repetitions",
    "valid_repetitions",
    "n",
    "mean",
    "std",
)

_PART_METRIC_KEYS: dict[str, dict[str, str]] = {
    "part_i": {
        "accepted_invariants": "accepted_invariants",
    },
    "part_ii": {
        "first_passed": "first_passed",
        "first_failed": "first_failed",
        "final_passed": "final_passed",
        "final_failed": "failed",
    },
    "part_iii": {
        "candidates": "candidates",
        "admitted": "candidate_admitted",
        "admission_rejected": "candidate_rejected",
        "verified": "candidate_verified",
        "verification_rejected": "candidate_rejected_by_verification",
        "invalid": "candidate_invalid",
        "unverified": "candidate_unverified",
        "demo_parity_recall": "demo_parity_recall",
        "demo_parity_profile": "demo_parity_profile",
        "demo_parity_superset_coverage": "demo_parity_superset_coverage",
        "time_to_first_verified_ms": "time_to_first_verified_ms",
    },
}


def _as_mapping(value: Any) -> Mapping[str, Any]:
    if isinstance(value, Mapping):
        return value
    dump = getattr(value, "model_dump", None)
    if callable(dump):
        result = dump(mode="json", exclude_none=False)
        if isinstance(result, Mapping):
            return result
    raise TypeError(f"campaign value must be a mapping or pydantic model, got {type(value)!r}")


def _atomic_write(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    try:
        temporary.write_bytes(content)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def _json_bytes(value: Any) -> bytes:
    return (
        json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2, allow_nan=False) + "\n"
    ).encode("utf-8")


def _csv_bytes(columns: Sequence[str], rows: Sequence[Mapping[str, Any]]) -> bytes:
    stream = io.StringIO(newline="")
    writer = csv.DictWriter(stream, fieldnames=list(columns), lineterminator="\n")
    writer.writeheader()
    writer.writerows(rows)
    return stream.getvalue().encode("utf-8")


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _artifact(path: Path, *, root: Path, schema: str) -> dict[str, Any]:
    return {
        "path": path.relative_to(root).as_posix(),
        "sha256": _sha256(path),
        "size_bytes": path.stat().st_size,
        "schema": schema,
    }


def _optional_number(value: Any) -> int | float | None:
    if value is None or isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    converted: int | float = value
    if isinstance(value, float) and not math.isfinite(value):
        return None
    return converted


def _optional_int(value: Any) -> int | None:
    number = _optional_number(value)
    return int(number) if number is not None else None


def _load_result_metrics(lane_dir: Path, record: Mapping[str, Any]) -> dict[str, Any]:
    direct = record.get("metrics")
    if isinstance(direct, Mapping):
        return dict(direct)
    metadata = record.get("metadata")
    if isinstance(metadata, Mapping) and isinstance(metadata.get("metrics"), Mapping):
        return dict(metadata["metrics"])
    relative = record.get("result_path")
    if not isinstance(relative, str) or not relative:
        return {}
    try:
        payload = json.loads((lane_dir / relative).read_text(encoding="utf-8"))
    except (OSError, ValueError, TypeError):
        return {}
    metrics = payload.get("metrics") if isinstance(payload, Mapping) else None
    return dict(metrics) if isinstance(metrics, Mapping) else {}


def _usage_from_summary(stage_root: Path) -> dict[str, Any]:
    records_path = stage_root / "token_usage.jsonl"
    if records_path.is_file():
        try:
            records = read_jsonl(records_path)
        except (OSError, TypeError, ValueError):
            records = []
        else:
            record_tokens = [
                record.total_tokens for record in records if record.total_tokens is not None
            ]
            latencies = [record.latency_ms for record in records]
            return {
                "consumed_total_tokens": (
                    sum(record_tokens) if record_tokens else None if records else 0
                ),
                "usage_missing_count": sum(bool(record.usage_missing) for record in records),
                "llm_latency_ms": (
                    math.fsum(value for value in latencies if value is not None)
                    if all(value is not None for value in latencies)
                    else None
                ),
            }
    path = stage_root / "token_usage_summary.json"
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError, TypeError):
        return {}
    if not isinstance(payload, list):
        return {}
    summary_tokens: list[int | float] = []
    known_latencies: list[int | float] = []
    latency_complete = True
    missing = 0
    for raw in payload:
        if not isinstance(raw, Mapping):
            continue
        tokens = _optional_number(raw.get("total_tokens"))
        latency = _optional_number(raw.get("total_latency_ms"))
        if tokens is not None:
            summary_tokens.append(tokens)
        if latency is not None:
            known_latencies.append(latency)
        else:
            latency_complete = False
        missing += _optional_int(raw.get("usage_missing_count")) or 0
    return {
        "consumed_total_tokens": (
            sum(summary_tokens) if summary_tokens else None if payload else 0
        ),
        "usage_missing_count": missing,
        "llm_latency_ms": (
            math.fsum(known_latencies)
            if latency_complete and known_latencies
            else 0.0 if not payload
            else None
        ),
    }


def _stage_metrics(part: str, raw: Mapping[str, Any]) -> dict[str, Any]:
    normalized: dict[str, Any] = {name: None for name in _STAGE_METRICS}
    for destination, source in _PART_METRIC_KEYS.get(part, {}).items():
        value = raw.get(source)
        if destination == "demo_parity_profile":
            normalized[destination] = value if isinstance(value, str) and value else None
        else:
            normalized[destination] = _optional_number(value)
    if part == "part_ii":
        invariants = _optional_number(raw.get("invariants"))
        first_passed = normalized["first_passed"]
        if invariants is not None and first_passed is not None:
            normalized["first_failed"] = max(0, invariants - first_passed)
    return normalized


def build_campaign_rows(lanes: Sequence[Any]) -> list[dict[str, Any]]:
    """Normalize pipeline lane results into deterministic stage rows."""

    rows: list[dict[str, Any]] = []
    for lane_value in lanes:
        lane = _as_mapping(lane_value)
        target = str(lane.get("target_id", lane.get("target", "")))
        variant = str(lane.get("variant", "full"))
        repetition = int(lane["repetition"])
        repetition_valid = bool(lane.get("result_valid", False))
        lane_dir = Path(str(lane.get("lane_dir", ".")))
        stage_values = lane.get("stages", {})
        stages = stage_values if isinstance(stage_values, Mapping) else {}
        for part in _PARTS:
            raw_record = stages.get(part, {})
            record = _as_mapping(raw_record) if raw_record else {}
            metadata_value = record.get("metadata", {})
            metadata = metadata_value if isinstance(metadata_value, Mapping) else {}
            usage_value = metadata.get("token_usage", {})
            usage = dict(usage_value) if isinstance(usage_value, Mapping) else {}
            fallback_usage = _usage_from_summary(lane_dir / part)
            raw_metrics = _load_result_metrics(lane_dir, record)
            reused_from = metadata.get("reused_from_variant")
            reused = bool(
                reused_from
                or usage.get("reused") is True
                or record.get("outcome") == "reused-frozen-upstream"
            )
            actual_tokens = (
                0
                if reused
                else _optional_int(
                    usage.get(
                        "consumed_total_tokens",
                        usage.get(
                            "actual_total_tokens",
                            fallback_usage.get("consumed_total_tokens"),
                        ),
                    )
                )
            )
            llm_latency = _optional_number(
                usage.get(
                    "llm_latency_ms",
                    usage.get("total_latency_ms", fallback_usage.get("llm_latency_ms")),
                )
            )
            elapsed = _optional_number(
                usage.get("elapsed_ms", metadata.get("elapsed_ms"))
            )
            if reused:
                llm_latency = 0.0
                elapsed = 0.0 if elapsed is None else elapsed
            elif record.get("execution_status") == "skipped" and elapsed is None:
                elapsed = 0.0
            missing = _optional_int(
                usage.get("usage_missing_count", fallback_usage.get("usage_missing_count"))
            )
            row: dict[str, Any] = {
                "target": target,
                "variant": variant,
                "repetition": repetition,
                "part": part,
                "execution_status": str(record.get("execution_status", "skipped")),
                "result_valid": bool(record.get("result_valid", False)),
                "outcome": str(record.get("outcome", "unknown")),
                "repetition_valid": repetition_valid,
                "reused": reused,
                "reused_from_variant": str(reused_from) if reused_from else None,
                **_stage_metrics(part, raw_metrics),
                "actual_total_tokens": actual_tokens,
                "attributed_total_tokens": actual_tokens,
                "usage_missing_count": missing,
                "llm_latency_ms": llm_latency,
                "elapsed_ms": elapsed,
                "metrics_json": json.dumps(
                    raw_metrics, ensure_ascii=False, sort_keys=True, separators=(",", ":")
                ),
            }
            rows.append(row)

    rows.sort(
        key=lambda row: (
            str(row["target"]),
            int(row["repetition"]),
            str(row["variant"]),
            _PARTS.index(str(row["part"])),
        )
    )
    full_rows = {
        (row["target"], row["repetition"], row["part"]): row
        for row in rows
        if row["variant"] == "full"
    }
    for row in rows:
        if not row["reused"]:
            continue
        source = full_rows.get((row["target"], row["repetition"], row["part"]))
        if source is None or row["reused_from_variant"] != "full":
            row["attributed_total_tokens"] = None
            continue
        row["attributed_total_tokens"] = source["actual_total_tokens"]
        row["usage_missing_count"] = source["usage_missing_count"]
        for metric in _STAGE_METRICS:
            if row[metric] is None:
                row[metric] = source[metric]
    return rows


def _sum_complete(values: Sequence[int | float | None]) -> int | float | None:
    if not values or any(value is None for value in values):
        return None
    return sum(value for value in values if value is not None)


def _repetition_metrics(rows: Sequence[Mapping[str, Any]]) -> dict[str, int | float | None]:
    by_part = {str(row["part"]): row for row in rows}
    values: dict[str, int | float | None] = {}
    for metric in _NUMERIC_STAGE_METRICS:
        source_part = next(
            (part for part, mapping in _PART_METRIC_KEYS.items() if metric in mapping), None
        )
        values[metric] = (
            _optional_number(by_part[source_part].get(metric))
            if source_part is not None and source_part in by_part
            else None
        )
    values["actual_total_tokens"] = _sum_complete(
        [_optional_number(row.get("actual_total_tokens")) for row in rows]
    )
    values["attributed_total_tokens"] = _sum_complete(
        [_optional_number(row.get("attributed_total_tokens")) for row in rows]
    )
    values["usage_missing_count"] = _sum_complete(
        [_optional_int(row.get("usage_missing_count")) for row in rows]
    )
    values["llm_latency_ms"] = _sum_complete(
        [_optional_number(row.get("llm_latency_ms")) for row in rows]
    )
    values["elapsed_ms"] = _sum_complete(
        [_optional_number(row.get("elapsed_ms")) for row in rows]
    )
    return values


def build_comparison_rows(rows: Sequence[Mapping[str, Any]]) -> list[dict[str, Any]]:
    """Summarize valid repetitions without coercing missing values to zero."""

    grouped: dict[tuple[str, str], dict[int, list[Mapping[str, Any]]]] = {}
    for row in rows:
        key = (str(row["target"]), str(row["variant"]))
        grouped.setdefault(key, {}).setdefault(int(row["repetition"]), []).append(row)

    result: list[dict[str, Any]] = []
    for (target, variant), repetitions in sorted(grouped.items()):
        valid = {
            repetition: stage_rows
            for repetition, stage_rows in repetitions.items()
            if stage_rows and all(bool(row.get("repetition_valid")) for row in stage_rows)
        }
        repetition_values = [_repetition_metrics(valid[key]) for key in sorted(valid)]
        for metric in _COMPARISON_METRICS:
            values: list[float] = []
            for repetition_value in repetition_values:
                numeric = repetition_value.get(metric)
                if numeric is not None:
                    values.append(float(numeric))
            result.append(
                {
                    "target": target,
                    "variant": variant,
                    "metric": metric,
                    "total_repetitions": len(repetitions),
                    "valid_repetitions": len(valid),
                    "n": len(values),
                    "mean": statistics.fmean(values) if values else None,
                    "std": statistics.stdev(values) if len(values) > 1 else 0.0 if values else None,
                }
            )
    return result


def write_campaign_results(
    run_root: str | os.PathLike[str], lanes: Sequence[Any]
) -> dict[str, Any]:
    """Write all four campaign tables and return top-manifest-ready references."""

    root = Path(run_root)
    root.mkdir(parents=True, exist_ok=True)
    rows = build_campaign_rows(lanes)
    comparisons = build_comparison_rows(rows)
    results_json = root / CAMPAIGN_RESULTS_JSON
    results_csv = root / CAMPAIGN_RESULTS_CSV
    comparison_json = root / CAMPAIGN_COMPARISON_JSON
    comparison_csv = root / CAMPAIGN_COMPARISON_CSV
    _atomic_write(
        results_json,
        _json_bytes(
            {
                "schema": CAMPAIGN_RESULTS_SCHEMA,
                "columns": list(LONG_FORM_COLUMNS),
                "rows": rows,
            }
        ),
    )
    _atomic_write(results_csv, _csv_bytes(LONG_FORM_COLUMNS, rows))
    _atomic_write(
        comparison_json,
        _json_bytes(
            {
                "schema": CAMPAIGN_COMPARISON_SCHEMA,
                "group_by": ["target", "variant"],
                "statistics": {"std": "sample; zero for n=1"},
                "rows": comparisons,
            }
        ),
    )
    _atomic_write(comparison_csv, _csv_bytes(COMPARISON_COLUMNS, comparisons))
    return {
        "results_json": _artifact(
            results_json, root=root, schema=CAMPAIGN_RESULTS_SCHEMA
        ),
        "results_csv": _artifact(
            results_csv, root=root, schema=CAMPAIGN_RESULTS_SCHEMA
        ),
        "comparison_json": _artifact(
            comparison_json, root=root, schema=CAMPAIGN_COMPARISON_SCHEMA
        ),
        "comparison_csv": _artifact(
            comparison_csv, root=root, schema=CAMPAIGN_COMPARISON_SCHEMA
        ),
    }


__all__ = [
    "CAMPAIGN_COMPARISON_CSV",
    "CAMPAIGN_COMPARISON_JSON",
    "CAMPAIGN_COMPARISON_SCHEMA",
    "CAMPAIGN_RESULTS_CSV",
    "CAMPAIGN_RESULTS_JSON",
    "CAMPAIGN_RESULTS_SCHEMA",
    "COMPARISON_COLUMNS",
    "LONG_FORM_COLUMNS",
    "build_campaign_rows",
    "build_comparison_rows",
    "write_campaign_results",
]
