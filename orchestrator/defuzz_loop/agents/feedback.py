"""Feedback agent: Pi-backed, thin Python wrapper.

Runs only on the not-violated branch. Pre-fetches the coverage delta via
MCP (unchanged, deterministic), renders a per-round prompt, and invokes
the ``feedback-analyzer`` markdown subagent. The subagent's structured
output is wrapped into :class:`Guidance` for the next round's Generator.

The ``coverage_signal`` helper is preserved verbatim: it gates the
coverage delta on ``ablation_flags.coverage_feedback`` so the linkage
edge's contribution can be measured (FR-010 / SC-004).
"""

from __future__ import annotations

from ..clients.mcp_client import MCPClient
from ..state import Blackboard, Guidance
from .contracts.output_schemas import FeedbackOutput
from .runner import SubagentRunner


def coverage_signal(bb: Blackboard, diff: dict) -> str:
    """The coverage delta fed into the next-round guidance.

    Gated by ``ablation_flags.coverage_feedback`` (FR-010 / SC-004): when
    off, the feedback agent advises without any coverage signal.
    """

    if not bb.ablation_flags.coverage_feedback:
        return "(coverage feedback disabled)"
    return diff.get("delta", bb.coverage.last_delta)


class FeedbackAgent:
    """Pi-backed feedback agent."""

    def __init__(
        self,
        mechanism: str,
        mcp: MCPClient,
        runner: SubagentRunner,
    ) -> None:
        self._mechanism = mechanism
        self._mcp = mcp
        self._runner = runner

    async def summarize(self, bb: Blackboard) -> Guidance:
        diff = await self._coverage_diff(bb)
        last = bb.last_verdict
        aggregate = last.aggregate if last is not None else "none"
        checkers = sorted({r.id for r in last.results}) if last is not None else []

        prompt = self._render_prompt(bb, diff, aggregate, checkers)
        out: FeedbackOutput = await self._runner.run(
            "feedback-analyzer", prompt, FeedbackOutput
        )
        return Guidance(
            round=bb.round,
            summary=out.summary,
            coverage_delta_ref="coverage.last_delta",
        )

    async def _coverage_diff(self, bb: Blackboard) -> dict:
        return await self._mcp.call_tool(
            "coverage_diff",
            {},
            agent="feedback",
            round=bb.round,
            tool_log=bb.tool_call_log,
        )

    def _render_prompt(
        self,
        bb: Blackboard,
        diff: dict,
        aggregate: object,
        checkers: list[str],
    ) -> str:
        signal = coverage_signal(bb, diff)
        return (
            f"Mechanism: {self._mechanism}\n"
            f"Round: {bb.round}\n"
            f"Aggregate verdict: {aggregate}\n"
            f"Checkers exercised: {', '.join(checkers) or '(none)'}\n"
            f"Coverage delta (read-only): {signal}\n"
            "\n"
            "Give ONE next-round direction (1–3 sentences). Reply with exactly "
            "one fenced ```json ...``` block matching FeedbackOutput — no prose "
            "outside."
        )


__all__ = ["FeedbackAgent", "coverage_signal"]
