"""Measured, backend-independent JSON validation and bounded schema repair."""

from __future__ import annotations

import json
import time
import uuid
from collections.abc import Callable, Mapping
from pathlib import Path
from typing import TYPE_CHECKING, Any, TypeVar

from pydantic import BaseModel

from ..token_usage import TokenUsageContext, TokenUsageSink, current_token_usage_sink

if TYPE_CHECKING:
    from ..experiment_engine.agent_backend import AgentBackend, AgentResult

T = TypeVar("T", bound=BaseModel)


class SubagentRunError(RuntimeError):
    """A terminal infrastructure failure or exhausted output-repair budget."""


def extract_json(text: str) -> Any:
    """Accept one JSON value, with an optional single Markdown fence."""
    text = text.strip()
    if text.startswith("```"):
        first, _, body = text.partition("\n")
        if first not in {"```", "```json"} or not body.rstrip().endswith("```"):
            raise ValueError("invalid JSON fence")
        text = body.rstrip()[:-3].strip()
    return json.loads(text)


def schema_repairable(result: AgentResult) -> bool:
    """Never feed infrastructure/evidence failures into a model repair."""
    return (
        getattr(result, "error_category", None) == "schema"
        and getattr(result, "runtime_ok", False)
        and getattr(result, "artifact_ok", False)
        and getattr(result, "evidence_ok", False)
    )


def repair_prompt(*, original: str, candidate: Any, errors: str) -> str:
    previous = (
        candidate if isinstance(candidate, str) else json.dumps(candidate, ensure_ascii=False)
    )
    return (
        f"{original}\n\n"
        "--- previous candidate (untrusted output to correct) ---\n"
        f"{previous}\n\n"
        "--- exact validation errors ---\n"
        f"{errors}\n\n"
        "Correct the output for the original task using all its inputs. "
        "Return the complete required JSON object."
    )


class SubagentRunner:
    """One permanent evidence directory and shared deadline per logical task."""

    def __init__(
        self,
        backend: AgentBackend,
        *,
        cwd: Path,
        output_dir: Path,
        timeout_secs: float = 180,
        max_retries: int = 2,
        token_sink: TokenUsageSink | None = None,
        metadata: Mapping[str, Any] | None = None,
        deny_read_paths: list[Path] | None = None,
        require_host_read_isolation: bool = False,
    ) -> None:
        if timeout_secs <= 0 or max_retries < 0:
            raise ValueError("runner requires a positive deadline and nonnegative retries")
        self.backend = backend
        self.cwd = Path(cwd)
        self.output_dir = Path(output_dir)
        self.timeout_secs = timeout_secs
        self.max_retries = max_retries
        self.token_sink = token_sink
        self.metadata = dict(metadata or {})
        self.deny_read_paths = list(deny_read_paths or [])
        self.require_host_read_isolation = require_host_read_isolation

    @classmethod
    def from_config(
        cls, config: Path, *, model: str | None = None, **kwargs: Any
    ) -> SubagentRunner:
        from ..experiment_engine.pi_backend import PiAgentBackend

        backend = PiAgentBackend(config, model=model)
        backend.validate_credentials()
        return cls(backend, **kwargs)

    async def run(
        self,
        agent_name: str,
        prompt: str,
        schema: type[T],
        *,
        metadata: Mapping[str, Any] | None = None,
        validator: Callable[[T], T] | None = None,
    ) -> T:
        from ..experiment_engine.agent_backend import AgentRequest

        invocation_id = uuid.uuid4().hex
        root = self.output_dir / invocation_id
        root.mkdir(parents=True, exist_ok=False)
        schema_path = root / "schema.json"
        schema_path.write_text(json.dumps(schema.model_json_schema()), encoding="utf-8")
        context = {
            "run_id": invocation_id,
            "experiment": "online-loop",
            "variant": "full",
            "part": "online",
            "stage": agent_name,
            **self.metadata,
            **dict(metadata or {}),
            "agent_id": agent_name,
            "agent": agent_name,
        }
        sink = self.token_sink or current_token_usage_sink()
        if ambient_context := getattr(sink, "context", None):
            ambient = (
                ambient_context
                if isinstance(ambient_context, Mapping)
                else {
                    key: getattr(ambient_context, key)
                    for key in ("run_id", "experiment", "variant", "part", "stage")
                }
            )
            context = {**ambient, **context}
            for key in ("run_id", "experiment", "variant", "part"):
                if key not in self.metadata and key not in (metadata or {}):
                    context[key] = ambient[key]
        if sink is None:
            sink = TokenUsageSink(
                root / "token_usage.jsonl",
                context=TokenUsageContext(
                    **{
                        key: context[key]
                        for key in ("run_id", "experiment", "variant", "part", "stage")
                    },
                    agent=agent_name,
                    provider=getattr(self.backend, "provider", None),
                    model=getattr(self.backend, "model", None),
                ),
            )
        deadline = time.monotonic() + self.timeout_secs
        current_prompt = prompt
        parent_attempt_id: str | None = None
        last_error = ""
        for _ in range(self.max_retries + 1):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise SubagentRunError(f"{agent_name}: task deadline exhausted")
            attempt_id = uuid.uuid4().hex
            attempt_dir = root / attempt_id
            response = await self.backend.run(
                AgentRequest(
                    prompt=current_prompt,
                    cwd=self.cwd,
                    output_dir=attempt_dir,
                    schema_path=schema_path,
                    timeout_seconds=remaining,
                    token_sink=sink,
                    deny_read_paths=self.deny_read_paths,
                    require_host_read_isolation=self.require_host_read_isolation,
                    metadata={
                        **context,
                        "attempt_id": attempt_id,
                        "parent_attempt_id": parent_attempt_id,
                    },
                )
            )
            if not response.success and not schema_repairable(response):
                raise SubagentRunError(f"{agent_name}: {response.error or 'backend failed'}")
            candidate = response.final
            raw = attempt_dir / "final.raw.txt"
            if candidate is None and raw.is_file():
                candidate = raw.read_text(encoding="utf-8")
            try:
                if not response.success:
                    raise ValueError(response.error or "invalid schema")
                parsed = schema.model_validate(
                    extract_json(candidate) if isinstance(candidate, str) else candidate
                )
                if validator:
                    parsed = validator(parsed)
                return parsed
            except ValueError as exc:
                last_error = str(exc)
                attempt_dir.mkdir(parents=True, exist_ok=True)
                (attempt_dir / "validation.json").write_text(
                    json.dumps(
                        {
                            "attempt_id": attempt_id,
                            "parent_attempt_id": parent_attempt_id,
                            "error_category": "schema",
                            "error": last_error,
                        },
                        ensure_ascii=False,
                        indent=2,
                    ),
                    encoding="utf-8",
                )
                current_prompt = repair_prompt(
                    original=prompt, candidate=candidate, errors=last_error
                )
                parent_attempt_id = attempt_id
        raise SubagentRunError(
            f"{agent_name} failed after {self.max_retries + 1} attempts: {last_error}"
        )
