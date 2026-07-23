"""Generator agent: produces a Seed (C source) for an ASSIGNED invariant.

Grounds itself on the SSOT checker catalog via the MCP query_invariants tool
(restricted to the run's single mechanism, per the single-mechanism principle),
optionally inspects the defense source via search_source, then asks the configured
LLM for the seed source. The agent does NOT choose which invariant to attack: the
orchestrator enumerates the catalog deterministically and hands the agent one
`current_target()` per round. ISA expansion is the deterministic routing layer's
job (FR-012/013).
"""

from __future__ import annotations

import uuid

from pydantic import BaseModel, Field

from ..clients.mcp_client import MCPClient
from ..llm import LLMConfig, build_chat_model
from ..state import Blackboard, Seed, SeedOrigin, ToolCall

_SYSTEM_PROMPT = """You are a compiler-hardening fuzzing seed generator.
You target ONE defense mechanism: {mechanism}.
The orchestrator has assigned you ONE invariant to stress this round; your job is
to produce a single self-contained C translation unit that maximizes the chance
of triggering that invariant's checker.

Hard rules:
- Write C that specifically exercises the assigned invariant below. Do NOT try to
  cover other checkers; a separate round is dedicated to each one.
- NEVER choose an ISA / architecture. ISA is derived later by routing.
- The C source must compile as a freestanding-ish test (a main() is fine).
- Prefer constructs that stress the mechanism (e.g. for stack canary: char arrays,
  VLAs/alloca, mixed locals, address-taken vars).

Assigned invariant this round:
{target_meta}
"""

_USER_PROMPT = """Round {round}. Assigned invariant: {target}.
{guidance_block}
Generate one seed that stresses {target} now."""


class GeneratorOutput(BaseModel):
    """Structured seed the LLM must return (source only; target is assigned)."""

    source: str = Field(description="complete C source for the seed")
    rationale: str = Field(
        default="", description="brief why this source stresses the assigned invariant"
    )


def guidance_block(bb: Blackboard) -> str:
    """Render last round's feedback for the prompt — the "feedback → Generator" edge.

    Gated by ablation_flags.feedback_to_generator (FR-010 / SC-004): when off, the
    Generator ignores any guidance so the edge's contribution can be measured.
    """
    if (
        bb.ablation_flags.feedback_to_generator
        and bb.guidance is not None
        and bb.guidance.summary
    ):
        return f"Feedback from last round: {bb.guidance.summary}\n"
    return ""


class GeneratorAgent:
    def __init__(
        self,
        mechanism: str,
        mcp: MCPClient,
        llm_config: LLMConfig | None = None,
    ) -> None:
        self._mechanism = mechanism
        self._mcp = mcp
        self._model = build_chat_model(llm_config)

    async def _catalog(self, round: int, tool_log: list[ToolCall]) -> list[dict]:
        resp = await self._mcp.call_tool(
            "query_invariants",
            {"mechanism": self._mechanism},
            agent="generator",
            round=round,
            tool_log=tool_log,
        )
        return resp.get("checkers", [])

    async def generate(self, bb: Blackboard) -> Seed:
        catalog = await self._catalog(bb.round, bb.tool_call_log)
        by_id = {c["id"]: c for c in catalog}
        target = bb.current_target()

        meta = by_id.get(target) if target else None
        if meta is not None:
            target_meta = (
                f"- {meta['id']} (mode={meta.get('mode')}, cost={meta.get('cost')}, "
                f"category={meta.get('category')})"
            )
        else:
            target_meta = f"- {target}" if target else "- (none assigned)"

        guidance = guidance_block(bb)

        messages = [
            (
                "system",
                _SYSTEM_PROMPT.format(mechanism=self._mechanism, target_meta=target_meta),
            ),
            (
                "user",
                _USER_PROMPT.format(
                    round=bb.round,
                    target=target or "(none)",
                    guidance_block=guidance,
                ),
            ),
        ]

        structured = self._model.with_structured_output(
            GeneratorOutput, method="function_calling"
        )
        result: GeneratorOutput = await structured.ainvoke(messages)

        # The seed's target is the orchestrator-assigned invariant, not an LLM
        # choice (kept only when it is a real catalog ID).
        selected = [target] if target in by_id else []

        return Seed(
            id=uuid.uuid4().hex[:12],
            source=result.source,
            parent_id=None,
            selected_checkers=selected,
            origin=SeedOrigin.GENERATOR,
        )
