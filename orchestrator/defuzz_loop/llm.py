"""LLM config module (Rev 3 shim).

Rev 3 removed the LangGraph / LangChain runtime entirely. The Python-side
LLM path for the online loop now goes through Pi agents
(see :mod:`defuzz_loop.agents.runner`), so ``build_chat_model`` is no longer a
functional path — it raises :class:`NotImplementedError` and is kept only
so protected modules (``audit.py``, ``specgen/*.py``) that still import
``LLMConfig`` for metadata purposes continue to work.

``LLMConfig`` itself is a pure pydantic model over ``configs/llm.yaml``;
it depends on no third-party LLM SDK. ``ainvoke_structured`` remains a
provider-agnostic helper: it uses only duck-typed
``model.with_structured_output(...)`` calls, so tests that supply their
own fake ``model`` keep working (see
``tests/test_token_usage_integration.py``).
"""

from __future__ import annotations

import time
from collections.abc import Mapping
from enum import StrEnum
from pathlib import Path
from typing import Any

import yaml
from pydantic import BaseModel

from .token_usage import TokenUsageContext, current_token_usage_sink

_DEFAULT_CONFIG_PATH = Path(__file__).resolve().parents[1] / "configs" / "llm.yaml"


class Provider(StrEnum):
    OPENAI = "openai"
    ANTHROPIC = "anthropic"


class LLMConfig(BaseModel):
    """Provider/model metadata read from ``configs/llm.yaml``.

    Preserved intentionally so run manifests, token accounting, and
    specgen configuration continue to work without importing LangChain.
    """

    provider: Provider = Provider.OPENAI
    model: str = "gpt-4o"
    temperature: float = 0.7
    max_tokens: int | None = None
    base_url: str | None = None
    api_key_env: str = "OPENAI_API_KEY"
    reasoning_effort: str | None = None
    timeout: int = 120

    @classmethod
    def load(cls, path: str | Path | None = None) -> LLMConfig:
        cfg_path = Path(path) if path else _DEFAULT_CONFIG_PATH
        if not cfg_path.exists():
            # Rev 3: return defaults rather than hard-failing, so protected
            # code paths that expect an LLMConfig object still function
            # (e.g. specgen replay from a transcript).
            return cls()
        data: dict[str, Any] = yaml.safe_load(cfg_path.read_text()) or {}
        return cls.model_validate(data.get("llm", data))


def build_chat_model(config: LLMConfig | None = None) -> Any:
    """Rev 3 stub — the live LLM path uses the shared Pi backend.

    Kept as a stub so protected modules that import the name still resolve;
    calling it explicitly is a bug in Rev 3 code and surfaces as
    :class:`NotImplementedError`.
    """

    raise NotImplementedError(
        "build_chat_model was removed in Rev 3; use "
        "defuzz_loop.agents.runner.SubagentRunner instead."
    )


async def ainvoke_structured[T: BaseModel](
    model: Any,
    output_model: type[T],
    messages: Any,
    *,
    stage: str,
    agent: str | None = None,
    method: str = "function_calling",
) -> T:
    """Provider-agnostic structured completion.

    Uses only duck-typed ``model.with_structured_output(...)``. Callers
    supply the model instance; Rev 3 does not build one for them. Records
    token usage when a sink is active (the same behaviour as the
    pre-Rev-3 helper). Tests can pass a fake model that mimics the
    LangChain-style API without pulling LangChain in as a dependency.
    """

    sink = current_token_usage_sink()
    if sink is None:
        structured = model.with_structured_output(output_model, method=method)
        return await structured.ainvoke(messages)

    sink.check_budget()
    context: TokenUsageContext = sink.context.with_overrides(stage=stage, agent=agent)
    started = time.perf_counter()
    response: Any = None
    response_received = False
    try:
        structured = model.with_structured_output(
            output_model, method=method, include_raw=True
        )
        response = await structured.ainvoke(messages)
        response_received = True
        if not isinstance(response, Mapping):
            raise TypeError(
                "structured output with include_raw=True must return a mapping"
            )
        parsing_error = response.get("parsing_error")
        if parsing_error is not None:
            if isinstance(parsing_error, BaseException):
                raise parsing_error
            raise ValueError(f"structured output parsing failed: {parsing_error}")
        parsed = response.get("parsed")
        if not isinstance(parsed, output_model):
            parsed = output_model.model_validate(parsed)
    except Exception as exc:
        failed_response = None
        if response_received:
            failed_response = response
            if isinstance(response, Mapping) and response.get("raw") is not None:
                failed_response = response["raw"]
        sink.record_failure(
            exc,
            response=failed_response,
            context=context,
            latency_ms=(time.perf_counter() - started) * 1000,
        )
        raise

    sink.record_response(
        response.get("raw"),
        context=context,
        latency_ms=(time.perf_counter() - started) * 1000,
    )
    return parsed


__all__ = ["LLMConfig", "Provider", "build_chat_model", "ainvoke_structured"]
