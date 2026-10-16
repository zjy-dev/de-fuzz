"""LLM-oracle agent: Pi-backed, per-invariant fallback judge.

The whole R8 zero-false-positives contract from earlier revisions is
preserved. Only two things change vs. the removed LangChain code path:

- Structured output goes through the ``llm-oracle`` markdown
  subagent + :class:`SubagentRunner`, not ``ainvoke_structured``.
- The verbose historical system prompt has moved into
  ``agents/definitions/llm-oracle.md``. The Python side only builds the
  per-invariant *user turn* (invariant brief + seed + binary evidence).

The per-invariant iteration logic (finding NA/Error verdicts, collecting
binary evidence, applying "only confident FAIL promotes") is unchanged.
"""

from __future__ import annotations

from .. import binary_evidence
from ..invariants_kb import brief_for, llm_oracle_target_ids
from ..state import (
    Aggregate,
    Blackboard,
    InvariantResult,
    OracleVerdict,
    Verdict,
)
from .contracts.output_schemas import LLMOracleOutput
from .runner import SubagentRunner

# Only a FAIL at or above this confidence flips the aggregate to violated.
# Kept high on purpose: a false bug is worse than a missed one (R8).
_FAIL_CONFIDENCE_THRESHOLD = 0.8

_VERDICT_MAP = {
    "PASS": Verdict.PASS,
    "FAIL": Verdict.FAIL,
    "NOT_APPLICABLE": Verdict.NOT_APPLICABLE,
    "ERROR": Verdict.ERROR,
}


def _binary_for_isa(bb: Blackboard, isa: str) -> tuple[str, str]:
    """Pick a successfully built binary for ``isa`` (its cells share one binary)."""

    for a in bb.build_artifacts:
        if a.cell.isa == isa and a.success and a.binary_path:
            return a.binary_path, a.cell.isa
    for a in bb.build_artifacts:
        if a.success and a.binary_path:
            return a.binary_path, a.cell.isa
    return "", isa


def _targets(bb: Blackboard) -> list[InvariantResult]:
    """Deterministic NA/Error results this agent is allowed to re-adjudicate."""

    if bb.last_verdict is None:
        return []
    allowed = llm_oracle_target_ids()
    undecided = {Verdict.NOT_APPLICABLE, Verdict.ERROR}
    return [
        r
        for r in bb.last_verdict.results
        if r.id in allowed and r.verdict in undecided
    ]


class LLMOracleAgent:
    """Fallback judge for the non-programmable FORTIFY invariants (Rev 3)."""

    def __init__(self, mechanism: str, runner: SubagentRunner) -> None:
        self._mechanism = mechanism
        self._runner = runner

    async def adjudicate(self, bb: Blackboard) -> list[InvariantResult]:
        """Return one LLM :class:`InvariantResult` per undecided target invariant."""

        targets = _targets(bb)
        if not targets or bb.current_seed is None:
            return []

        source = bb.current_seed.source
        out: list[InvariantResult] = []
        # Per-ISA evidence cache — repeated invariants on one ISA reuse it.
        ev_cache: dict[str, str] = {}
        for r in targets:
            if r.isa not in ev_cache:
                binary_path, isa = _binary_for_isa(bb, r.isa)
                ev_cache[r.isa] = binary_evidence.collect(binary_path, isa).render()
            verdict = await self._judge(r, source, ev_cache[r.isa])
            mapped = _VERDICT_MAP.get(verdict.verdict, Verdict.ERROR)
            # Demote low-confidence FAIL to NOT_APPLICABLE: never a false bug.
            if (
                mapped is Verdict.FAIL
                and verdict.confidence < _FAIL_CONFIDENCE_THRESHOLD
            ):
                mapped = Verdict.NOT_APPLICABLE
            out.append(
                InvariantResult(
                    id=r.id,
                    category=r.category or "static",
                    verdict=mapped,
                    evidence=verdict.evidence,
                    detail={
                        "source": "llm_oracle",
                        "confidence": f"{verdict.confidence:.2f}",
                        "model_verdict": verdict.verdict,
                    },
                    reason=verdict.reason,
                    isa=r.isa,
                )
            )
        return out

    async def _judge(
        self,
        result: InvariantResult,
        source: str,
        evidence: str,
    ) -> LLMOracleOutput:
        brief = brief_for(result.id)
        if brief is None:
            return LLMOracleOutput(
                verdict="NOT_APPLICABLE",
                confidence=0.0,
                evidence="",
                reason="No brief available for this invariant.",
            )
        prompt = _render_prompt(
            inv_id=brief.id,
            title=brief.title,
            statement=brief.statement,
            violation_signal=brief.violation_signal,
            pass_signal=brief.pass_signal,
            na_signal=brief.na_signal,
            det_verdict=str(result.verdict),
            det_reason=result.reason or "(none)",
            source=source,
            evidence=evidence,
        )
        return await self._runner.run("llm-oracle", prompt, LLMOracleOutput)


def _render_prompt(
    *,
    inv_id: str,
    title: str,
    statement: str,
    violation_signal: str,
    pass_signal: str,
    na_signal: str,
    det_verdict: str,
    det_reason: str,
    source: str,
    evidence: str,
) -> str:
    """Build the per-invariant user turn (system prompt lives in the .md)."""

    return (
        f"Invariant {inv_id}: {title}\n\n"
        f"STATEMENT:\n{statement}\n\n"
        f"FAIL signal:\n{violation_signal}\n\n"
        f"PASS signal:\n{pass_signal}\n\n"
        f"NOT_APPLICABLE signal:\n{na_signal}\n\n"
        f"Deterministic backend said: {det_verdict} — reason: {det_reason}\n\n"
        "C SEED SOURCE:\n```c\n"
        f"{source}\n"
        "```\n\n"
        f"BINARY EVIDENCE (read-only):\n{evidence}\n\n"
        f"Decide invariant {inv_id} now. Reply with exactly one fenced "
        "```json ...``` block matching LLMOracleOutput — no prose outside."
    )


def promote_verdict(
    base: OracleVerdict | None,
    llm_results: list[InvariantResult],
    seed_id: str,
) -> OracleVerdict | None:
    """Build the violated :class:`OracleVerdict` if any LLM result is a
    (confident) FAIL. Returns ``None`` when nothing is promoted.
    """

    first_fail = next((r for r in llm_results if r.verdict is Verdict.FAIL), None)
    if first_fail is None:
        return None
    base_results = list(base.results) if base is not None else []
    return OracleVerdict(
        seed_id=seed_id,
        results=base_results + llm_results,
        aggregate=Aggregate.VIOLATED,
        failing_checker=first_fail.id,
        failing_isa=first_fail.isa,
        evidence=f"[llm-oracle] {first_fail.evidence or first_fail.reason}",
    )


__all__ = ["LLMOracleAgent", "promote_verdict"]
