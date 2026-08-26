"""Crash-safe filesystem storage for reproducible experiment runs."""

from __future__ import annotations

import json
import os
import threading
import uuid
from collections.abc import Callable, Mapping
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from defuzz_loop.token_usage import (
    BudgetExceeded,
    TokenUsageContext,
    TokenUsageRecord,
    TokenUsageSink,
    append_jsonl,
    read_jsonl,
    write_aggregate_csv,
    write_aggregate_json,
)

from .models import ArtifactRef, ExperimentPlan, StageResult


class PlanMismatchError(ValueError):
    """Raised when an existing run was created from a different plan."""


def _timestamp() -> str:
    return datetime.now(UTC).isoformat(timespec="milliseconds")


def _atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    try:
        with temporary.open("wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def _json_bytes(value: Any) -> bytes:
    return (
        json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True, default=str) + "\n"
    ).encode("utf-8")


class RunTokenSink:
    """Append-only token sink scoped to one repetition directory."""

    def __init__(
        self, rep_dir: Path, context: Mapping[str, Any], *, token_budget: int | None = None
    ) -> None:
        self.rep_dir = rep_dir
        self.path = rep_dir / "token_usage.jsonl"
        self.summary_json_path = rep_dir / "token_usage_summary.json"
        self.summary_csv_path = rep_dir / "token_usage_summary.csv"
        self.context = dict(context)
        self._sink = TokenUsageSink(
            self.path,
            context=TokenUsageContext(**self.context),
            token_budget=token_budget,
        )

    def _coerce(self, value: TokenUsageRecord | Mapping[str, Any]) -> TokenUsageRecord:
        if isinstance(value, TokenUsageRecord):
            return value
        data = {**self.context, **dict(value)}
        return TokenUsageRecord.from_dict(data)

    def record(self, record: TokenUsageRecord | Mapping[str, Any]) -> None:
        append_jsonl(self.path, self._coerce(record))

    def __call__(self, record: TokenUsageRecord | Mapping[str, Any]) -> None:
        self.record(record)

    def record_response(self, response: Any, **context: Any) -> TokenUsageRecord:
        selected = TokenUsageContext(**{**self.context, **context})
        return self._sink.record_response(response, context=selected)

    def record_external_usage(
        self,
        payload: Any,
        *,
        context: TokenUsageContext | Mapping[str, Any] | None = None,
        latency_ms: float | None = None,
        success: bool = True,
        error_type: str | None = None,
        **overrides: Any,
    ) -> TokenUsageRecord:
        if isinstance(context, TokenUsageContext):
            selected = context
        else:
            allowed = {
                "run_id",
                "experiment",
                "variant",
                "part",
                "stage",
                "agent",
                "provider",
                "model",
            }
            merged = {**self.context, **dict(context or {}), **overrides}
            selected = TokenUsageContext(
                **{key: value for key, value in merged.items() if key in allowed}
            )
        return self._sink.record_external_usage(
            payload,
            context=selected,
            latency_ms=latency_ms,
            success=success,
            error_type=error_type,
        )

    def check_budget(self) -> None:
        budget = self._sink.token_budget
        if budget is None:
            return
        known = [
            record.total_tokens
            for record in read_jsonl(self.path)
            if record.total_tokens is not None
        ]
        if known and sum(known) >= budget:
            raise BudgetExceeded(consumed=sum(known), budget=budget)

    def summarize(self) -> list[dict[str, Any]]:
        records = read_jsonl(self.path)
        rows = write_aggregate_json(self.summary_json_path, records)
        write_aggregate_csv(self.summary_csv_path, records)
        return rows


class RunStore:
    """Own a run directory and enforce its immutable plan identity."""

    def __init__(
        self, root: str | os.PathLike[str], plan: ExperimentPlan | Mapping[str, Any]
    ) -> None:
        self.root = Path(root).expanduser().resolve(strict=False)
        self.plan = (
            plan if isinstance(plan, ExperimentPlan) else ExperimentPlan.from_mapping(plan)
        )
        self.plan_hash = self.plan.content_hash()
        self.plan_path = self.root / "plan.json"
        self.manifest_path = self.root / "manifest.json"
        self.events_path = self.root / "events.jsonl"
        self._lock = threading.RLock()
        self._initialize()
        for repetition in range(1, self.plan.repetitions + 1):
            self.prepare_rep(repetition)

    @property
    def run_dir(self) -> Path:
        return self.root

    def _initialize(self) -> None:
        self.root.mkdir(parents=True, exist_ok=True)
        if self.manifest_path.exists() or self.plan_path.exists():
            self.verify_resume()
            return
        _atomic_write(self.plan_path, _json_bytes(self.plan.to_dict()))
        _atomic_write(
            self.manifest_path,
            _json_bytes(
                {
                    "schema_version": 1,
                    "run_id": self.plan.run_id,
                    "experiment": self.plan.experiment,
                    "variant": self.plan.variant,
                    "plan_hash": self.plan_hash,
                    "created_at": _timestamp(),
                    "repetitions": {},
                }
            ),
        )
        _atomic_write(self.events_path, b"")

    def verify_resume(self, plan: ExperimentPlan | Mapping[str, Any] | None = None) -> bool:
        expected = self.plan if plan is None else (
            plan if isinstance(plan, ExperimentPlan) else ExperimentPlan.from_mapping(plan)
        )
        expected_hash = expected.content_hash()
        try:
            manifest = json.loads(self.manifest_path.read_text(encoding="utf-8"))
            stored_plan = ExperimentPlan.from_mapping(
                json.loads(self.plan_path.read_text(encoding="utf-8"))
            )
        except FileNotFoundError as exc:
            raise PlanMismatchError(
                "cannot resume an incomplete run: plan or manifest is missing"
            ) from exc
        except (json.JSONDecodeError, TypeError, ValueError) as exc:
            raise PlanMismatchError(f"cannot resume run with invalid metadata: {exc}") from exc
        hashes = {manifest.get("plan_hash"), stored_plan.content_hash()}
        if hashes != {expected_hash}:
            raise PlanMismatchError(
                f"experiment plan mismatch: stored={manifest.get('plan_hash')!r}, "
                f"requested={expected_hash!r}"
            )
        return True

    def rep_dir(self, repetition: int) -> Path:
        if isinstance(repetition, bool) or repetition < 1:
            raise ValueError("repetition must be a positive integer")
        return self.root / f"rep-{repetition:03d}"

    def prepare_rep(self, repetition: int) -> Path:
        if repetition > self.plan.repetitions:
            raise ValueError(
                f"repetition {repetition} exceeds configured count {self.plan.repetitions}"
            )
        rep_dir = self.rep_dir(repetition)
        with self._lock:
            rep_dir.mkdir(parents=True, exist_ok=True)
            (rep_dir / "artifacts").mkdir(exist_ok=True)
            rep_manifest = rep_dir / "manifest.json"
            created = not rep_manifest.exists()
            if not created:
                existing = json.loads(rep_manifest.read_text(encoding="utf-8"))
                if existing.get("plan_hash") != self.plan_hash:
                    raise PlanMismatchError(f"repetition {repetition} belongs to another plan")
            else:
                _atomic_write(
                    rep_manifest,
                    _json_bytes(
                        {
                            "schema_version": 1,
                            "run_id": self.plan.run_id,
                            "repetition": repetition,
                            "plan_hash": self.plan_hash,
                            "status": "prepared",
                            "created_at": _timestamp(),
                        }
                    ),
                )
            events = rep_dir / "events.jsonl"
            if not events.exists():
                _atomic_write(events, b"")
            if created:
                self._update_root_rep(repetition, "prepared")
        return rep_dir

    def _read_manifest(self, path: Path | None = None) -> dict[str, Any]:
        source = path or self.manifest_path
        value = json.loads(source.read_text(encoding="utf-8"))
        if not isinstance(value, dict):
            raise ValueError(f"manifest must be a JSON object: {source}")
        return value

    def read_manifest(self, *, repetition: int | None = None) -> dict[str, Any]:
        path = (
            self.manifest_path
            if repetition is None
            else self.rep_dir(repetition) / "manifest.json"
        )
        if not path.exists():
            return {}
        return self._read_manifest(path)

    def write_manifest(
        self, updates: Mapping[str, Any], *, repetition: int | None = None
    ) -> dict[str, Any]:
        path = (
            self.manifest_path
            if repetition is None
            else self.prepare_rep(repetition) / "manifest.json"
        )
        with self._lock:
            manifest = self._read_manifest(path)
            manifest.update(dict(updates))
            manifest["plan_hash"] = self.plan_hash
            manifest["updated_at"] = _timestamp()
            _atomic_write(path, _json_bytes(manifest))
        return manifest

    def _update_root_rep(self, repetition: int, status: str) -> None:
        manifest = self._read_manifest()
        repetitions = manifest.setdefault("repetitions", {})
        repetitions[str(repetition)] = {
            "path": self.rep_dir(repetition).relative_to(self.root).as_posix(),
            "status": status,
        }
        manifest["updated_at"] = _timestamp()
        _atomic_write(self.manifest_path, _json_bytes(manifest))

    def append_event(
        self, event: Mapping[str, Any], *, repetition: int | None = None
    ) -> None:
        path = (
            self.events_path
            if repetition is None
            else self.prepare_rep(repetition) / "events.jsonl"
        )
        value = dict(event)
        value.setdefault("timestamp", _timestamp())
        line = json.dumps(value, ensure_ascii=False, separators=(",", ":"), default=str).encode(
            "utf-8"
        ) + b"\n"
        with self._lock:
            previous = path.read_bytes() if path.exists() else b""
            _atomic_write(path, previous + line)

    # Compatibility synonym used by some stage runners.
    write_event = append_event

    def write_stage_result(self, repetition: int, result: StageResult) -> Path:
        rep_dir = self.prepare_rep(repetition)
        path = rep_dir / f"{result.stage}-result.json"
        _atomic_write(path, _json_bytes(result.to_dict()))
        status = "completed" if result.success else "failed"
        self.write_manifest({"status": status}, repetition=repetition)
        with self._lock:
            self._update_root_rep(repetition, status)
        self.append_event(
            {"type": "stage.completed", "stage": result.stage, "status": result.status},
            repetition=repetition,
        )
        return path

    def artifact_ref(
        self, path: str | os.PathLike[str], *, kind: str | None = None
    ) -> ArtifactRef:
        return ArtifactRef.from_path(Path(path), base_dir=self.root, kind=kind)

    def token_sink(self, repetition: int, **context: Any) -> RunTokenSink:
        rep_dir = self.prepare_rep(repetition)
        defaults = {
            "run_id": self.plan.run_id,
            "experiment": self.plan.experiment,
            "variant": self.plan.variant,
            "part": self.plan.experiment,
            "stage": "agent",
        }
        return RunTokenSink(
            rep_dir, {**defaults, **context}, token_budget=self.plan.budget.token_budget
        )


TokenSinkCallable = Callable[[TokenUsageRecord], None]

__all__ = [
    "PlanMismatchError",
    "RunStore",
    "RunTokenSink",
    "TokenSinkCallable",
    "TokenUsageSink",
]
