"""Rev 3 seed-generator agent: OpenCode-backed, thin Python wrapper.

The heavy lifting (constructing the seed C source) happens inside the
``seed-generator`` markdown subagent (see
``.opencode/agents/seed-generator.md``); this Python class pre-fetches the
invariant metadata via MCP (still deterministic, no LLM), assembles the
per-round user-turn prompt, invokes the subagent through the shared
:class:`SubagentRunner`, and wraps the parsed output into a
:class:`Seed`.

Removed vs. earlier revisions: no more direct LangChain / LangGraph
imports. The old ``_SYSTEM_PROMPT`` has moved into the markdown subagent.
The prompt built here is only the *user turn* carrying per-invocation
state.
"""

from __future__ import annotations

import uuid

from ..clients.mcp_client import MCPClient
from ..opencode.output_schemas import GeneratorOutput
from ..opencode.runner import SubagentRunner
from ..state import Blackboard, Seed, SeedOrigin


def guidance_block(bb: Blackboard) -> str:
    """Render last round's feedback for the prompt — the "feedback → Generator" edge.

    Gated by ``ablation_flags.feedback_to_generator`` (FR-010 / SC-004):
    when off, the Generator ignores any guidance so the edge's
    contribution can be measured.
    """

    if (
        bb.ablation_flags.feedback_to_generator
        and bb.guidance is not None
        and bb.guidance.summary
    ):
        return f"Feedback from last round: {bb.guidance.summary}\n"
    return ""


class GeneratorAgent:
    """Rev 3 OpenCode-backed generator agent."""

    def __init__(
        self,
        mechanism: str,
        mcp: MCPClient,
        runner: SubagentRunner,
    ) -> None:
        self._mechanism = mechanism
        self._mcp = mcp
        self._runner = runner

    async def generate(self, bb: Blackboard) -> Seed:
        """Produce one seed for the orchestrator-assigned invariant."""

        target = bb.current_target()
        catalog = await self._catalog(bb)
        by_id = {c["id"]: c for c in catalog}
        meta = by_id.get(target) if target else None

        prompt = self._render_prompt(bb, target, meta)
        out: GeneratorOutput = await self._runner.run(
            "seed-generator", prompt, GeneratorOutput
        )

        selected = [target] if target is not None and target in by_id else []
        return Seed(
            id=uuid.uuid4().hex[:12],
            source=out.source,
            parent_id=None,
            selected_checkers=selected,
            origin=SeedOrigin.GENERATOR,
        )

    async def _catalog(self, bb: Blackboard) -> list[dict]:
        resp = await self._mcp.call_tool(
            "query_invariants",
            {"mechanism": self._mechanism},
            agent="generator",
            round=bb.round,
            tool_log=bb.tool_call_log,
        )
        return resp.get("checkers", [])

    def _render_prompt(
        self,
        bb: Blackboard,
        target: str | None,
        meta: dict | None,
    ) -> str:
        """Assemble the user-turn prompt (system prompt lives in the .md)."""

        if meta is not None:
            target_meta = (
                f"- id: {meta['id']}\n"
                f"  mode: {meta.get('mode')}\n"
                f"  cost: {meta.get('cost')}\n"
                f"  category: {meta.get('category')}"
            )
        else:
            target_meta = f"- id: {target or '(none)'}"

        prior = _prior_seed_snippets(bb, keep=2)
        guidance = guidance_block(bb)

        return (
            f"Mechanism: {self._mechanism}\n"
            f"Round: {bb.round}\n"
            f"Assigned invariant this round:\n{target_meta}\n"
            "\n"
            f"{guidance}"
            f"{prior}"
            "\n"
            "Generate ONE self-contained C seed that stresses the assigned "
            "invariant. Reply with exactly one fenced ```json ...``` block "
            "matching the GeneratorOutput schema — no prose outside."
        )


def _prior_seed_snippets(bb: Blackboard, *, keep: int) -> str:
    """A short reminder of the last few seeds so the LLM doesn't repeat itself."""

    if not bb.corpus:
        return ""
    tail = bb.corpus[-keep:]
    parts: list[str] = ["Previous seed IDs and rationales (for de-duplication only):"]
    for s in tail:
        first_line = s.source.splitlines()[0] if s.source else ""
        parts.append(f"- {s.id}: {first_line[:80]}")
    return "\n".join(parts) + "\n"


__all__ = ["GeneratorAgent", "GeneratorOutput", "guidance_block"]
