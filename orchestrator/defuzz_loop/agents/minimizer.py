"""Rev 3 minimizer agent: MCP-driven creduce with an optional advisor subagent.

Historically minimizer.py never called an LLM — creduce is the workhorse
and the interestingness test binds reduction to "still triggers the
original failing checker on the original ISA" (FR-025/026).

Rev 3 keeps that pure-MCP path intact. As a small optional enhancement,
we invite the ``minimizer`` markdown advisor to propose short
interestingness *hints*; when the subagent+runner are supplied, the hints
are folded into the interestingness string as a comment tail before the
creduce call. Failures inside the advisor are swallowed — this is a
best-effort seasoning, not a hard dependency (a slow / offline advisor
must never block a bug's reduction).
"""

from __future__ import annotations

import logging

from ..clients.mcp_client import MCPClient
from ..opencode.output_schemas import MinimizerOutput
from ..opencode.runner import SubagentRunError, SubagentRunner
from ..state import Blackboard, MinimizedPoC

log = logging.getLogger(__name__)


class MinimizerAgent:
    """Rev 3 minimizer: creduce driver + optional advisor subagent."""

    def __init__(
        self,
        mechanism: str,
        mcp: MCPClient,
        runner: SubagentRunner | None = None,
    ) -> None:
        self._mechanism = mechanism
        self._mcp = mcp
        # Optional. If None, the advisor step is skipped entirely.
        self._runner = runner

    async def minimize(self, bb: Blackboard) -> MinimizedPoC:
        bug = bb.pending_bug
        seed = bb.current_seed
        if bug is None or seed is None:
            return MinimizedPoC(
                original_seed_id="", reduced_source="", still_triggers=False
            )

        hints_tail = ""
        if self._runner is not None:
            hints_tail = await self._collect_hints(bb, bug, seed.source)

        interestingness = (
            f"still-triggers checker={bug.failing_checker} isa={bug.isa} "
            f"mechanism={self._mechanism}{hints_tail}"
        )
        reduced = await self._mcp.call_tool(
            "creduce_run",
            {"source": seed.source, "interestingness_cmd": interestingness},
            agent="minimizer",
            round=bb.round,
            tool_log=bb.tool_call_log,
        )
        reduced_source = reduced.get("reduced_source", seed.source)

        check = await self._mcp.call_tool(
            "compile_exec",
            {
                "source": reduced_source,
                "isa": bug.isa,
                "checker_id": bug.failing_checker,
            },
            agent="minimizer",
            round=bb.round,
            tool_log=bb.tool_call_log,
        )
        still = bool(check.get("still_triggers", False))

        return MinimizedPoC(
            original_seed_id=bug.seed_id,
            reduced_source=reduced_source,
            still_triggers=still,
        )

    async def _collect_hints(
        self,
        bb: Blackboard,
        bug: object,
        source: str,
    ) -> str:
        """Ask the optional advisor for interestingness hints; swallow failures."""

        assert self._runner is not None
        prompt = (
            f"Mechanism: {self._mechanism}\n"
            f"Failing checker: {getattr(bug, 'failing_checker', '')}\n"
            f"Failing ISA: {getattr(bug, 'isa', '')}\n"
            f"Evidence: {getattr(bug, 'evidence', '')}\n\n"
            "SEED SOURCE:\n```c\n"
            f"{source}\n"
            "```\n\n"
            "Propose interestingness-test hints. Reply with exactly one fenced "
            "```json ...``` block matching MinimizerOutput — no prose outside."
        )
        try:
            out: MinimizerOutput = await self._runner.run(
                "minimizer", prompt, MinimizerOutput
            )
        except (SubagentRunError, Exception) as e:  # noqa: BLE001
            log.warning("minimizer advisor failed; falling back to bare creduce: %s", e)
            return ""
        if not out.interestingness_hints:
            return ""
        joined = "; ".join(h.replace(";", ",") for h in out.interestingness_hints)
        return f"  # hints: {joined}"


__all__ = ["MinimizerAgent"]
