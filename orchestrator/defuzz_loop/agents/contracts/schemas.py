"""Backend-independent source-mapping I/O contracts.

The definition in ``agents/definitions/source-mapper.md`` mirrors these shapes.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field


class Invariant(BaseModel):
    """A single security invariant. Sent as the *input* payload to the
    source-mapper agent, and also carried on the Python blackboard
    for per-campaign scheduling.
    """

    phi_id: str = Field(..., description="Stable ID, e.g. 'SP-001'.")
    phi_text: str = Field(..., description="Human-readable invariant text.")
    mechanism: str = Field(
        ...,
        description=(
            "Defense mechanism family: 'stack_canary', 'fortify', 'cfi', 'ibt', "
            "'safestack', 'shadow_stack'."
        ),
    )
    compiler: Literal["gcc", "llvm", "clang"] = Field(default="gcc")
    isa: Literal["x86_64", "aarch64", "riscv64", "loongarch64"] = Field(default="x86_64")
    src_root: str = Field(..., description="Absolute path to the compiler source tree.")

    def as_prompt_payload(self) -> str:
        """Render the invariant as an ``<invariant>{...json...}</invariant>``
        prompt fragment that the source-mapper markdown spec expects.
        """

        import json as _json

        return "<invariant>" + _json.dumps(self.model_dump(), ensure_ascii=False) + "</invariant>"


class PassAnchor(BaseModel):
    name: str
    file: str
    line_start: int
    line_end: int
    evidence: str


class TargetFunction(BaseModel):
    name: str
    file: str
    line: int
    role: str


class CriticalStruct(BaseModel):
    name: str
    file: str
    line: int
    role: str


class CriticalCondition(BaseModel):
    expression: str
    file: str
    line: int
    role: str


class SourceMappingResult(BaseModel):
    """Structured output from the source-mapper subagent.

    A single result maps ONE invariant onto Level 1 (pass), Level 2 (target
    functions), Level 3 (critical structs + gate conditions) of the compiler
    source tree. Downstream, the log-instrumentation stage will attach
    ``DEFUZZ_LOG("<mechanism>#<uid>:hit", ...)`` calls at the cited lines.
    """

    phi_id: str
    compiler: Literal["gcc", "llvm", "clang"]
    isa: Literal["x86_64", "aarch64", "riscv64", "loongarch64"] | None = None
    mapping_confidence: Literal["high", "medium", "low"]

    level_1_pass: PassAnchor
    level_2_functions: list[TargetFunction] = Field(default_factory=list, max_length=5)
    level_3_critical_structs: list[CriticalStruct] = Field(default_factory=list, max_length=5)
    level_3_critical_conditions: list[CriticalCondition] = Field(default_factory=list, max_length=8)

    evidence_paths: list[str] = Field(default_factory=list)
    unresolved_notes: list[str] = Field(
        default_factory=list,
        description=(
            "Gaps the agent could not resolve with read/grep alone; a future "
            "call-graph MCP tool will fill these."
        ),
    )
