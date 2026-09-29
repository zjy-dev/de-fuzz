"""Generic 3-layer JSON defense subagent runner (Rev 3 Scheme A).

Every online-loop DeFuzz subagent (``seed-generator``, ``feedback-analyzer``,
``llm-oracle``, ``minimizer``) is expected to emit exactly one fenced
```json ...``` block that validates against a pydantic model defined in
:mod:`defuzz_loop.opencode.output_schemas`. The three layers of defense are:

1. **Prompt** — the markdown subagent tells the LLM the exact schema and
   demands "final message = one fenced JSON block, nothing else". See
   ``.opencode/agents/*.md``.
2. **Pydantic** — this module validates the JSON block against the caller's
   schema type before returning.
3. **Retry** — on :class:`pydantic.ValidationError` (or
   :class:`OpenCodeInvocationError`), :meth:`SubagentRunner.run` re-invokes
   the subagent up to ``max_retries`` times with a *repair prompt* that
   quotes the specific errors from ``ValidationError.errors()`` plus the
   last invalid output text.

Retry is DISABLED for programmer errors: a missing subagent markdown or a
misconfigured :class:`OpenCodeClient` bubbles up as-is.

The reusable retry / extract / validate pattern was extracted from
:class:`defuzz_loop.source_mapping.mapper.SourceMappingRunner`.
"""

from __future__ import annotations

import logging
from pathlib import Path
from typing import TypeVar

from pydantic import BaseModel, ValidationError

from .client import (
    OpenCodeClient,
    OpenCodeInvocation,
    OpenCodeInvocationError,
    extract_json_block,
)

log = logging.getLogger(__name__)

T = TypeVar("T", bound=BaseModel)


class SubagentRunError(RuntimeError):
    """Raised after all retries are exhausted."""


class SubagentRunner:
    """Async runner for a single OpenCode markdown subagent, with the
    3-layer JSON defense described in this module's docstring.

    Parameters
    ----------
    client:
        The shared :class:`OpenCodeClient` subprocess wrapper.
    cwd:
        Working directory containing the ``.opencode/agents/*.md`` (and the
        singular ``.opencode/agent/source-mapper.md`` for legacy). Passed
        to ``opencode run --dir``.
    timeout_secs:
        Per-attempt wall-clock timeout in seconds.
    max_retries:
        Additional attempts to make on validation / invocation failure.
        Default 2 → up to 3 total attempts.
    model:
        Optional provider model override (``opencode run --model ...``).
        ``None`` uses the OpenCode global default.
    """

    def __init__(
        self,
        client: OpenCodeClient,
        *,
        cwd: Path,
        timeout_secs: int = 180,
        max_retries: int = 2,
        model: str | None = None,
    ) -> None:
        self._client = client
        self._cwd = Path(cwd)
        self._timeout_secs = float(timeout_secs)
        self._max_retries = int(max_retries)
        self._model = model

    async def run(
        self,
        agent_name: str,
        prompt: str,
        schema: type[T],
    ) -> T:
        """Invoke ``agent_name`` with ``prompt`` and return a validated
        ``schema`` instance.

        On :class:`ValidationError` or malformed JSON, re-invoke the subagent
        with a *repair prompt* that quotes the pydantic errors. Bubbles
        :class:`SubagentRunError` after ``max_retries`` further attempts.
        """

        current_prompt = prompt
        last_err: Exception | None = None
        last_text: str = ""

        for attempt in range(1, self._max_retries + 2):
            log.info(
                "subagent.run agent=%s attempt=%d/%d",
                agent_name,
                attempt,
                self._max_retries + 1,
            )
            try:
                assistant_text = await self._invoke(agent_name, current_prompt)
                last_text = assistant_text
                json_str = extract_json_block(assistant_text)
                parsed: T = schema.model_validate_json(json_str)
                return parsed
            except ValidationError as e:
                last_err = e
                log.warning(
                    "subagent.validation_error agent=%s attempt=%d errors=%s",
                    agent_name,
                    attempt,
                    _short_errors(e),
                )
                current_prompt = self._repair_prompt(
                    original=prompt,
                    schema=schema,
                    invalid_text=last_text,
                    errors_summary=_short_errors(e),
                )
            except OpenCodeInvocationError as e:
                last_err = e
                log.warning(
                    "subagent.invocation_error agent=%s attempt=%d: %s",
                    agent_name,
                    attempt,
                    e,
                )
                # No repair prompt — the previous output may not exist.
                current_prompt = prompt

        assert last_err is not None
        raise SubagentRunError(
            f"subagent {agent_name!r} failed after "
            f"{self._max_retries + 1} attempts: {last_err}"
        ) from last_err

    async def _invoke(self, agent_name: str, prompt: str) -> str:
        invocation = OpenCodeInvocation(
            agent=agent_name,
            message=prompt,
            cwd=self._cwd,
            model=self._model,
            timeout_secs=self._timeout_secs,
        )
        return await self._client.run_agent(invocation)

    @staticmethod
    def _repair_prompt(
        *,
        original: str,
        schema: type[BaseModel],
        invalid_text: str,
        errors_summary: str,
    ) -> str:
        """Build the retry message: original context + what was wrong."""

        schema_desc = _schema_field_summary(schema)
        return (
            "The previous reply did not validate. Fix it and try again.\n"
            "\n"
            "--- pydantic errors (subset) ---\n"
            f"{errors_summary}\n"
            "\n"
            "--- required schema fields ---\n"
            f"{schema_desc}\n"
            "\n"
            "--- your previous reply (verbatim) ---\n"
            f"{_tail(invalid_text, 2000)}\n"
            "\n"
            "--- original task ---\n"
            f"{original}\n"
            "\n"
            "Reply with exactly one fenced ```json ...``` block and nothing else."
        )


# ------------------------------ helpers -------------------------------------


def _tail(s: str, n: int) -> str:
    return s if len(s) <= n else "...(truncated)...\n" + s[-n:]


def _short_errors(exc: ValidationError) -> str:
    """Compact summary of a pydantic ``ValidationError`` for a repair prompt."""

    lines: list[str] = []
    for err in exc.errors():
        loc = ".".join(str(part) for part in err.get("loc", ()))
        msg = err.get("msg", "")
        typ = err.get("type", "")
        lines.append(f"- {loc or '<root>'}: {msg} ({typ})")
    return "\n".join(lines) if lines else "(no structured errors captured)"


def _schema_field_summary(schema: type[BaseModel]) -> str:
    """One-line-per-field description of a pydantic model, for the repair prompt."""

    lines: list[str] = []
    for name, info in schema.model_fields.items():
        annotation = getattr(info.annotation, "__name__", str(info.annotation))
        desc = info.description or ""
        lines.append(f"- {name}: {annotation} — {desc}".rstrip(" —"))
    return "\n".join(lines) if lines else "(no fields)"


__all__ = ["SubagentRunner", "SubagentRunError"]
