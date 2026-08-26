"""LLM provider config module — single factory for the three agents to share.

Replaces the deleted Go internal/llm: all LLM access now lives in Python via
langchain chat models, wired into langgraph (analyze C2/C3). Configuration is loaded
from configs/llm.yaml; the API key resolves from the env var named by `api_key_env`.
"""

from __future__ import annotations

import os
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
    provider: Provider = Provider.OPENAI
    model: str = "gpt-4o"
    temperature: float = 0.7
    max_tokens: int | None = None
    base_url: str | None = None
    api_key_env: str = "OPENAI_API_KEY"
    # Reasoning depth for OpenAI reasoning models (GPT-5 family): low|medium|high|xhigh.
    # None leaves the gateway default; only forwarded when the provider supports it.
    reasoning_effort: str | None = None
    timeout: int = 120

    @classmethod
    def load(cls, path: str | Path | None = None) -> LLMConfig:
        cfg_path = Path(path) if path else _DEFAULT_CONFIG_PATH
        if not cfg_path.exists():
            raise FileNotFoundError(f"LLM config not found: {cfg_path}")
        data: dict[str, Any] = yaml.safe_load(cfg_path.read_text()) or {}
        return cls.model_validate(data.get("llm", data))


def build_chat_model(config: LLMConfig | None = None) -> Any:
    """Return a langchain chat model the three agents reuse.

    Provider packages are optional extras; import lazily so an unused provider
    is never required.
    """
    cfg = config or LLMConfig.load()
    api_key = os.environ.get(cfg.api_key_env)

    if cfg.provider is Provider.OPENAI:
        from langchain_openai import ChatOpenAI

        kwargs: dict[str, Any] = dict(
            model=cfg.model,
            temperature=cfg.temperature,
            max_tokens=cfg.max_tokens,
            base_url=cfg.base_url,
            api_key=api_key,
            timeout=cfg.timeout,
        )
        # Reasoning models take effort via the request body; the OpenAI-compatible
        # gateway reads it from extra_body so it survives langchain's param filtering.
        if cfg.reasoning_effort:
            kwargs["extra_body"] = {"reasoning_effort": cfg.reasoning_effort}
        return ChatOpenAI(**kwargs)
    if cfg.provider is Provider.ANTHROPIC:
        from langchain_anthropic import ChatAnthropic

        return ChatAnthropic(
            model=cfg.model,
            temperature=cfg.temperature,
            max_tokens=cfg.max_tokens or 4096,
            base_url=cfg.base_url,
            api_key=api_key,
            timeout=cfg.timeout,
        )
    raise ValueError(f"unsupported provider: {cfg.provider}")


async def ainvoke_structured[T: BaseModel](
    model: Any,
    output_model: type[T],
    messages: Any,
    *,
    stage: str,
    agent: str | None = None,
    method: str = "function_calling",
) -> T:
    """Invoke one structured completion, recording usage when a sink is active.

    With no ambient sink this deliberately follows the pre-integration call
    shape: ``include_raw`` is not supplied and LangChain returns the parsed
    Pydantic model directly. With a sink, the provider's raw AI message is kept
    long enough to record usage, then only ``parsed`` is returned to callers.
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
