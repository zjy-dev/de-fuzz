"""Generator agent: produces a Seed (C source + selected_checkers, never ISA).

Grounds itself on the SSOT checker catalog via the MCP query_invariants tool
(restricted to the run's single mechanism, per the single-mechanism principle),
optionally inspects the defense source via search_source, then asks the configured
LLM for a structured seed. The agent selects checkers only; ISA expansion is the
deterministic routing layer's job (FR-012/013).
"""

from __future__ import annotations

import uuid

from pydantic import BaseModel, Field

from ..clients.mcp_client import MCPClient
from ..llm import LLMConfig, build_chat_model
from ..state import Blackboard, Seed, SeedOrigin, ToolCall

_SYSTEM_PROMPT = """You are a compiler-hardening fuzzing seed generator.
You target ONE defense mechanism: {mechanism}.
Produce a single self-contained C translation unit that exercises the target
defense's code paths, plus the subset of checkers whose invariants this seed can
trigger.

Hard rules:
- selected_checkers MUST be a subset of the provided catalog IDs. Never invent IDs.
- NEVER choose an ISA / architecture. You pick checkers only; ISA is derived later.
- The C source must compile as a freestanding-ish test (a main() is fine).
- Prefer constructs that stress the mechanism (e.g. for stack canary: char arrays,
  VLAs/alloca, mixed locals, address-taken vars).

Catalog of selectable checkers for {mechanism}:
{catalog}
"""

_USER_PROMPT = """Round {round}.
{guidance_block}
Generate one seed now."""


class GeneratorOutput(BaseModel):
    """Structured seed the LLM must return."""

    source: str = Field(description="complete C source for the seed")
    selected_checkers: list[str] = Field(
        default_factory=list,
        description="subset of catalog checker IDs this seed can trigger; no ISA",
    )
    rationale: str = Field(default="", description="brief why these checkers")


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
        catalog_ids = {c["id"] for c in catalog}
        catalog_text = "\n".join(
            f"- {c['id']} (mode={c.get('mode')}, cost={c.get('cost')}, "
            f"category={c.get('category')})"
            for c in catalog
        )

        guidance = guidance_block(bb)

        messages = [
            (
                "system",
                _SYSTEM_PROMPT.format(mechanism=self._mechanism, catalog=catalog_text),
            ),
            (
                "user",
                _USER_PROMPT.format(round=bb.round, guidance_block=guidance),
            ),
        ]

        structured = self._model.with_structured_output(
            GeneratorOutput, method="function_calling"
        )
        result: GeneratorOutput = await structured.ainvoke(messages)

        # Enforce single-mechanism principle: drop any ID outside the catalog.
        selected = [cid for cid in result.selected_checkers if cid in catalog_ids]

        return Seed(
            id=uuid.uuid4().hex[:12],
            source=result.source,
            parent_id=None,
            selected_checkers=selected,
            origin=SeedOrigin.GENERATOR,
        )
