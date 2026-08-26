"""Feedback agent: turns a not_violated round into Guidance for the next seed.

Runs only on the not_violated branch (FR-023/024). It is a context-isolated
subagent: it reads the coverage delta (strictly read-only, via the MCP
coverage_diff tool — R6/FR-022) plus the latest verdict, and writes a single
Guidance object. It never writes coverage and never adjudicates; its only output
field is `guidance`, consumed by the next round's Generator.
"""

from __future__ import annotations

from pydantic import BaseModel, Field

from ..clients.mcp_client import MCPClient
from ..llm import LLMConfig, ainvoke_structured, build_chat_model
from ..state import Blackboard, Guidance

_SYSTEM_PROMPT = """You are a fuzzing feedback strategist for ONE defense mechanism.
The last round did NOT trigger a violation. Given the coverage delta and the
oracle verdict, propose the single most promising direction for the next seed:
which code paths look under-explored, which checker structures to stress next.

Hard rules:
- Output ONE concise actionable summary (a few sentences), not code.
- You only advise; you never select ISAs and never adjudicate bugs.
"""

_USER_PROMPT = """Round {round}.
Coverage delta (read-only): {delta}
Latest verdict aggregate: {aggregate}
Checkers exercised last round: {checkers}
Give next-round guidance now."""


class FeedbackOutput(BaseModel):
    summary: str = Field(description="next-round direction, a few sentences, no code")


def coverage_signal(bb: Blackboard, diff: dict) -> str:
    """The coverage delta fed into the next-round guidance — the "coverage feedback" edge.

    Gated by ablation_flags.coverage_feedback (FR-010 / SC-004): when off, the
    feedback agent advises without any coverage signal, so the edge's contribution
    can be measured independently.
    """
    if not bb.ablation_flags.coverage_feedback:
        return "(coverage feedback disabled)"
    return diff.get("delta", bb.coverage.last_delta)


class FeedbackAgent:
    def __init__(
        self,
        mechanism: str,
        mcp: MCPClient,
        llm_config: LLMConfig | None = None,
    ) -> None:
        self._mechanism = mechanism
        self._mcp = mcp
        self._model = build_chat_model(llm_config)

    async def _coverage_diff(self, round: int, tool_log: list) -> dict:
        return await self._mcp.call_tool(
            "coverage_diff",
            {},
            agent="feedback",
            round=round,
            tool_log=tool_log,
        )

    async def summarize(self, bb: Blackboard) -> Guidance:
        diff = await self._coverage_diff(bb.round, bb.tool_call_log)
        last = bb.last_verdict
        aggregate = last.aggregate if last is not None else "none"
        checkers = sorted({r.id for r in last.results}) if last is not None else []

        messages = [
            ("system", _SYSTEM_PROMPT),
            (
                "user",
                _USER_PROMPT.format(
                    round=bb.round,
                    delta=coverage_signal(bb, diff),
                    aggregate=aggregate,
                    checkers=", ".join(checkers) or "(none)",
                ),
            ),
        ]

        result = await ainvoke_structured(
            self._model,
            FeedbackOutput,
            messages,
            stage="summarize",
            agent="feedback",
        )

        return Guidance(
            round=bb.round,
            summary=result.summary,
            coverage_delta_ref="coverage.last_delta",
        )
