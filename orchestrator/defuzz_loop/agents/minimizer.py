"""Minimizer agent: shrinks a violating seed to a minimal still-triggering PoC.

Runs only on the violated terminus (FR-025/026). creduce is the deterministic
workhorse (MCP creduce_run); the LLM only provides semantic guidance and the
interestingness intent. After reduction the candidate is re-verified with
compile_exec, whose `still_triggers` (re-running the SAME failing checker)
guards against reducing into a different bug. The agent writes only
`minimized_poc`; the branch then ends for human review.
"""

from __future__ import annotations

from ..clients.mcp_client import MCPClient
from ..state import Blackboard, MinimizedPoC


class MinimizerAgent:
    def __init__(self, mechanism: str, mcp: MCPClient) -> None:
        self._mechanism = mechanism
        self._mcp = mcp

    async def minimize(self, bb: Blackboard) -> MinimizedPoC:
        bug = bb.pending_bug
        seed = bb.current_seed
        if bug is None or seed is None:
            return MinimizedPoC(original_seed_id="", reduced_source="", still_triggers=False)

        # creduce drives the reduction; interestingness is bound to "still triggers
        # the original failing checker on the original ISA" (FR-026).
        interestingness = (
            f"still-triggers checker={bug.failing_checker} isa={bug.isa} "
            f"mechanism={self._mechanism}"
        )
        reduced = await self._mcp.call_tool(
            "creduce_run",
            {"source": seed.source, "interestingness_cmd": interestingness},
            agent="minimizer",
            round=bb.round,
            tool_log=bb.tool_call_log,
        )
        reduced_source = reduced.get("reduced_source", seed.source)

        # Re-verify the reduced candidate still triggers the same bug.
        check = await self._mcp.call_tool(
            "compile_exec",
            {"source": reduced_source, "isa": bug.isa, "checker_id": bug.failing_checker},
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
