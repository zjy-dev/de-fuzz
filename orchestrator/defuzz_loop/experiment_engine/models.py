"""Typed, serialization-stable contracts for experiment execution."""

from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping
from pathlib import Path
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, PositiveFloat, PositiveInt, model_validator

VariantName = Literal["full", "without-rag", "without-oracle", "bare-agent"]
StageStatus = Literal["pending", "running", "completed", "succeeded", "failed", "skipped"]


class _CompatModel(BaseModel):
    """Keep experiment artifacts forward-compatible across runner revisions."""

    model_config = ConfigDict(extra="allow", populate_by_name=True)

    def to_dict(self) -> dict[str, Any]:
        return self.model_dump(mode="json", exclude_none=True)


class BudgetEnvelope(_CompatModel):
    """Comparable token and wall-clock limits for one repetition."""

    token_budget: PositiveInt = 100_000
    time_budget_minutes: PositiveFloat = 60.0

    @model_validator(mode="before")
    @classmethod
    def _accept_compat_names(cls, value: Any) -> Any:
        if not isinstance(value, Mapping):
            return value
        data = dict(value)
        if "token_budget" not in data and "max_tokens" in data:
            data["token_budget"] = data["max_tokens"]
        if "time_budget_minutes" not in data:
            if "max_minutes" in data:
                data["time_budget_minutes"] = data["max_minutes"]
            elif "timeout_seconds" in data:
                data["time_budget_minutes"] = float(data["timeout_seconds"]) / 60.0
            elif "max_seconds" in data:
                data["time_budget_minutes"] = float(data["max_seconds"]) / 60.0
        for key in ("max_tokens", "max_minutes", "timeout_seconds", "max_seconds"):
            data.pop(key, None)
        return data

    @property
    def timeout_seconds(self) -> float:
        return float(self.time_budget_minutes) * 60.0

    @property
    def max_tokens(self) -> int:
        return int(self.token_budget)

    @property
    def max_seconds(self) -> float:
        return self.timeout_seconds


class VariantPolicy(_CompatModel):
    """Capabilities exposed to an experiment worker for one frozen variant."""

    name: VariantName = "full"
    use_rag: bool = True
    use_online_oracle: bool = True
    use_invariants: bool = True
    use_dedicated_checkers: bool = True
    use_structured_workflow: bool = True

    @classmethod
    def for_variant(cls, name: str) -> VariantPolicy:
        policies: dict[str, dict[str, bool]] = {
            "full": {},
            "without-rag": {"use_rag": False},
            "without-oracle": {"use_online_oracle": False},
            "bare-agent": {
                "use_rag": False,
                "use_online_oracle": False,
                "use_invariants": False,
                "use_dedicated_checkers": False,
                "use_structured_workflow": False,
            },
        }
        if name not in policies:
            supported = ", ".join(policies)
            raise ValueError(
                f"unsupported experiment variant {name!r}; expected one of: {supported}"
            )
        return cls(name=name, **policies[name])  # type: ignore[arg-type]

    @property
    def rag_enabled(self) -> bool:
        return self.use_rag

    @property
    def oracle_enabled(self) -> bool:
        return self.use_online_oracle

    @property
    def invariants_enabled(self) -> bool:
        return self.use_invariants

    @property
    def dedicated_checkers_enabled(self) -> bool:
        return self.use_dedicated_checkers

    @property
    def structured_workflow_enabled(self) -> bool:
        return self.use_structured_workflow


class ArtifactRef(_CompatModel):
    """Content-addressed reference to an experiment artifact."""

    path: str
    sha256: str
    size_bytes: int = Field(ge=0)
    kind: str | None = None

    @classmethod
    def from_path(
        cls, path: str | Path, *, base_dir: str | Path | None = None, kind: str | None = None
    ) -> ArtifactRef:
        source = Path(path)
        if not source.is_file():
            raise ValueError(f"artifact is not a regular file: {source}")
        digest = hashlib.sha256()
        with source.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
        display = source
        if base_dir is not None:
            try:
                display = source.resolve().relative_to(Path(base_dir).resolve())
            except ValueError as exc:
                raise ValueError(f"artifact {source} is outside base directory {base_dir}") from exc
        return cls(
            path=display.as_posix(),
            sha256=digest.hexdigest(),
            size_bytes=source.stat().st_size,
            kind=kind,
        )


class StageResult(_CompatModel):
    """Serializable result returned by every experiment stage runner."""

    stage: str = "unknown"
    status: StageStatus | str = "completed"
    artifacts: list[ArtifactRef] = Field(default_factory=list)
    metrics: dict[str, Any] = Field(default_factory=dict)
    errors: list[str] = Field(default_factory=list)
    messages: list[str] = Field(default_factory=list)
    error: str | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode="after")
    def _normalize_error(self) -> StageResult:
        if self.error and self.error not in self.errors:
            self.errors.insert(0, self.error)
        elif self.errors and self.error is None:
            self.error = self.errors[0]
        return self

    @property
    def success(self) -> bool:
        return self.status in {"completed", "succeeded", "success"} and not self.errors


class ExperimentPlan(_CompatModel):
    """Normalized plan accepted by all experiment runners."""

    schema_version: PositiveInt = 1
    run_id: str
    experiment: str
    variant: VariantName = "full"
    repetitions: PositiveInt = 1
    budget: BudgetEnvelope = Field(default_factory=BudgetEnvelope)
    parameters: dict[str, Any] = Field(default_factory=dict)
    source_root: Path | None = None
    output_root: Path | None = None

    @model_validator(mode="before")
    @classmethod
    def _normalize_plan(cls, value: Any) -> Any:
        if not isinstance(value, Mapping):
            return value
        data = dict(value)
        run = data.get("run")
        run_data = dict(run) if isinstance(run, Mapping) else {}
        data.pop("run", None)
        for key in ("run_id", "repetitions", "output_root"):
            if key not in data and key in run_data:
                data[key] = run_data[key]

        raw_budget = data.get("budget")
        budget = dict(raw_budget) if isinstance(raw_budget, Mapping) else {}
        for key in (
            "token_budget",
            "time_budget_minutes",
            "max_tokens",
            "max_minutes",
            "max_seconds",
            "timeout_seconds",
        ):
            if key not in budget:
                if key in data:
                    budget[key] = data[key]
                elif key in run_data:
                    budget[key] = run_data[key]
        if budget:
            data["budget"] = budget
        for key in (
            "token_budget",
            "time_budget_minutes",
            "max_tokens",
            "max_minutes",
            "max_seconds",
            "timeout_seconds",
        ):
            data.pop(key, None)

        data.setdefault("variant", "full")
        data.setdefault("parameters", {})
        if "run_id" not in data:
            variant = data["variant"]
            experiment = data.get("experiment", "experiment")
            data["run_id"] = f"{experiment}-{variant}" if variant != "full" else experiment
        return data

    @classmethod
    def from_mapping(cls, value: Mapping[str, Any]) -> ExperimentPlan:
        return cls.model_validate(value)

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> ExperimentPlan:
        return cls.from_mapping(value)

    @property
    def policy(self) -> VariantPolicy:
        return VariantPolicy.for_variant(self.variant)

    @property
    def run(self) -> dict[str, Any]:
        return {
            "run_id": self.run_id,
            "output_root": str(self.output_root) if self.output_root is not None else None,
            "token_budget": self.budget.token_budget,
            "time_budget_minutes": self.budget.time_budget_minutes,
            "repetitions": self.repetitions,
        }

    def content_hash(self) -> str:
        payload = self.model_dump(
            mode="json",
            exclude_none=True,
            exclude={"output_root", "launches", "status", "backend_available"},
        )
        canonical = json.dumps(
            payload, ensure_ascii=False, sort_keys=True, separators=(",", ":")
        )
        return hashlib.sha256(canonical.encode("utf-8")).hexdigest()
