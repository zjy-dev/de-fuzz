"""LLM provider config module — single factory for the three agents to share.

Replaces the deleted Go internal/llm: all LLM access now lives in Python via
langchain chat models, wired into langgraph (analyze C2/C3). Configuration is loaded
from configs/llm.yaml; the API key resolves from the env var named by `api_key_env`.
"""

from __future__ import annotations

import os
from enum import StrEnum
from pathlib import Path
from typing import Any

import yaml
from pydantic import BaseModel

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

        return ChatOpenAI(
            model=cfg.model,
            temperature=cfg.temperature,
            max_tokens=cfg.max_tokens,
            base_url=cfg.base_url,
            api_key=api_key,
            timeout=cfg.timeout,
        )
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
