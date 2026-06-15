"""Blackboard schema — the single shared-state linkage channel for the three agents.

Versioned by the LangGraph checkpointer. Agents never message each other directly:
they read inputs and write outputs through this state (FR-006/008).

Write-permission matrix (enforced at node exits, see graph.py / test_blackboard.py):

| field             | sole writer                  | source         |
|-------------------|------------------------------|----------------|
| corpus            | generator node / orchestrator| FR-007         |
| coverage          | ONLY coverage node           | FR-022         |
| verdict_history   | oracle node                  | FR-007         |
| guidance          | ONLY feedback agent          | FR-008         |
| tool_call_log     | tool-call wrapper layer      | R5             |
| pending_bug       | oracle node (on violation)   | FR-025         |
| build_matrix      | routing (lookup, not agent)  | FR-013/015     |
"""

from __future__ import annotations

from enum import StrEnum

from pydantic import BaseModel, Field


class SeedOrigin(StrEnum):
    GENERATOR = "generator"
    MINIMIZED = "minimized"


class Verdict(StrEnum):
    """Mirrors Go internal/oracle/invariant.go four-state verdict."""

    PASS = "pass"
    FAIL = "fail"
    NOT_APPLICABLE = "not_applicable"
    ERROR = "error"


class Aggregate(StrEnum):
    VIOLATED = "violated"
    NOT_VIOLATED = "not_violated"


class Seed(BaseModel):
    id: str
    source: str
    parent_id: str | None = None
    # Generator selects checkers only — never ISA (FR-013).
    selected_checkers: list[str] = Field(default_factory=list)
    origin: SeedOrigin = SeedOrigin.GENERATOR


class BuildCell(BaseModel):
    checker_id: str
    isa: str


class BuildMatrix(BaseModel):
    """Routing lookup product, written by routing.py (not an agent)."""

    cells: list[BuildCell] = Field(default_factory=list)
    forced_full: set[str] = Field(default_factory=set)  # differential checker IDs


class BuildArtifact(BaseModel):
    cell: BuildCell
    binary_path: str = ""
    success: bool = False
    error: str = ""  # build failure fills error, never crashes (R8/FR-015)


class CoverageState(BaseModel):
    cumulative: bytes = b""
    last_delta: str = ""  # JSON delta, feedback agent reads diff only (R6)


class InvariantResult(BaseModel):
    """Sticks to the Go schema (internal/oracle/invariant.go)."""

    id: str
    category: str = ""  # static | dynamic
    verdict: Verdict = Verdict.NOT_APPLICABLE
    evidence: str = ""
    detail: dict[str, str] = Field(default_factory=dict)
    reason: str = ""
    isa: str = ""


class OracleVerdict(BaseModel):
    seed_id: str
    results: list[InvariantResult] = Field(default_factory=list)
    aggregate: Aggregate = Aggregate.NOT_VIOLATED
    failing_checker: str = ""
    failing_isa: str = ""
    evidence: str = ""


class Guidance(BaseModel):
    round: int = 0
    summary: str = ""
    coverage_delta_ref: str = ""  # points at CoverageState.last_delta


class BugEvidence(BaseModel):
    seed_id: str
    failing_checker: str
    isa: str
    evidence: str


class MinimizedPoC(BaseModel):
    original_seed_id: str
    reduced_source: str
    still_triggers: bool = False


class ToolCall(BaseModel):
    """MCP tool-call trace for replay/audit (R5)."""

    round: int = 0
    agent: str = ""
    tool: str = ""
    args: dict[str, str] = Field(default_factory=dict)
    result_digest: str = ""


class AblationFlags(BaseModel):
    """Each bool maps to one linkage edge (FR-010 / SC-004)."""

    feedback_to_generator: bool = True
    coverage_feedback: bool = True
    oracle_grounding: bool = True
    checker_routing: bool = True  # False -> full ISA cartesian product, no pruning


class Blackboard(BaseModel):
    """Orchestration state root. Held and versioned by the orchestrator."""

    round: int = 0
    corpus: list[Seed] = Field(default_factory=list)
    coverage: CoverageState = Field(default_factory=CoverageState)
    verdict_history: list[OracleVerdict] = Field(default_factory=list)
    guidance: Guidance | None = None
    tool_call_log: list[ToolCall] = Field(default_factory=list)
    ablation_flags: AblationFlags = Field(default_factory=AblationFlags)
    pending_bug: BugEvidence | None = None

    # Per-round transients (cleared after write-back).
    current_seed: Seed | None = None
    build_matrix: BuildMatrix | None = None
    build_artifacts: list[BuildArtifact] = Field(default_factory=list)
    last_verdict: OracleVerdict | None = None
    minimized_poc: MinimizedPoC | None = None
