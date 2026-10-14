"""Rev 3 subagent output schemas — the pydantic gate of the 3-layer JSON defense.

Each of the four online-loop agents (``seed-generator``,
``feedback-analyzer``, ``llm-oracle``, ``minimizer``) must emit exactly one
fenced ```json ...``` block whose payload validates against one of the
models below. The markdown definitions in ``agents/definitions/*.md`` mirror
these shapes at the LLM level; the runner
(:mod:`defuzz_loop.agents.runner`) validates on the Python side and
re-invokes the subagent with a repair prompt on ``ValidationError``.

The models are deliberately small and use ``ConfigDict(extra="ignore")`` so
extra keys the LLM may add (e.g. "notes") never break parsing during
future spec evolution.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class GeneratorOutput(BaseModel):
    """Payload from the ``seed-generator`` subagent (one round)."""

    model_config = ConfigDict(extra="ignore")

    source: str = Field(
        ...,
        description="Complete standalone C source, including any #include directives.",
    )
    rationale: str = Field(
        default="",
        description="One-paragraph explanation of which invariant path this seed targets.",
    )


class FeedbackOutput(BaseModel):
    """Payload from the ``feedback-analyzer`` subagent (one round)."""

    model_config = ConfigDict(extra="ignore")

    summary: str = Field(
        ...,
        description="One to three concise sentences describing the next-round direction.",
    )


class LLMOracleOutput(BaseModel):
    """Payload from the ``llm-oracle`` subagent (one invariant per invocation).

    R8 rule: only a confident ``FAIL`` may escalate the aggregate verdict to
    ``violated``. ``PASS`` / ``NOT_APPLICABLE`` / ``ERROR`` are informational
    only and never overturn a deterministic oracle decision.
    """

    model_config = ConfigDict(extra="ignore")

    verdict: Literal["PASS", "FAIL", "NOT_APPLICABLE", "ERROR"] = Field(
        ...,
        description="Four-state verdict for this invariant on this binary.",
    )
    confidence: float = Field(
        ...,
        ge=0.0,
        le=1.0,
        description="Certainty (0.0..1.0) that a real FAIL fired. Required for every verdict.",
    )
    evidence: str = Field(
        ...,
        description="Concrete citation — symbol or exact disasm line supporting the verdict.",
    )
    reason: str = Field(
        ...,
        description="One-sentence justification of why this verdict was chosen.",
    )


class MinimizerOutput(BaseModel):
    """Payload from the ``minimizer`` advisor subagent (hints only, no execution)."""

    model_config = ConfigDict(extra="ignore")

    interestingness_hints: list[str] = Field(
        default_factory=list,
        description=(
            "Short hints the interestingness test should check — each entry is one "
            "human-readable line the Python driver folds into the creduce test."
        ),
    )
    notes: str = Field(
        default="",
        description="One-sentence justification or caveat for the driver.",
    )


__all__ = [
    "GeneratorOutput",
    "FeedbackOutput",
    "LLMOracleOutput",
    "MinimizerOutput",
]
