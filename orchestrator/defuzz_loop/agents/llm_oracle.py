"""LLM-driven oracle agent: adjudicates the non-programmable FORTIFY invariants.

The deterministic Go oracle is the sole source of programmatic bugs (FR-021, C6).
But four FORTIFY invariants (W01/O01/O02/O03) cannot be decided deterministically
on aarch64: the disasm backend is x86_64-only and W01's symbol signal is
ambiguous. There they return NOT_APPLICABLE / ERROR — never a bug, never a
clearance. This agent fills exactly that gap: for each such NA/Error invariant it
asks the configured LLM to judge the seed source + the binary's aarch64 symbol /
disassembly evidence against a distilled invariant brief, returning a four-state
verdict with a confidence.

Conservatism is the whole point (R8 zero false positives): the agent only ever
*adds* information. It promotes a violation only on a confident FAIL, and it never
touches an invariant the deterministic oracle already decided (PASS/FAIL). NA/Error
stay NA/Error unless the LLM is confidently sure a bug fired.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field

from .. import binary_evidence
from ..invariants_kb import brief_for, llm_oracle_target_ids
from ..llm import LLMConfig, build_chat_model
from ..state import Blackboard, InvariantResult, OracleVerdict, Verdict

# Only a FAIL at or above this confidence flips the aggregate to violated. Kept
# high on purpose: a false bug is worse than a missed one for this research (R8).
_FAIL_CONFIDENCE_THRESHOLD = 0.8

_SYSTEM_PROMPT = """You are a compiler-hardening security oracle for the \
_FORTIFY_SOURCE defense. You decide ONE invariant per call, for a binary built on \
aarch64 Linux with glibc.

You are given: (1) the invariant's precise statement and its FAIL / PASS / \
NOT_APPLICABLE signals, (2) the C seed source, (3) read-only binary evidence \
(symbol table + objdump disassembly around fortify call sites). The deterministic \
backend could not decide this invariant on aarch64, so its verdict was \
NOT_APPLICABLE / ERROR — your job is to decide it from the evidence.

Output a four-state verdict:
- FAIL: the evidence positively shows the invariant is VIOLATED (a real silent \
bypass — the overflow check is absent or defeated). Reserve FAIL for cases the \
evidence genuinely demonstrates.
- PASS: the evidence shows the invariant holds (e.g. a real __*_chk call with a \
correct size argument).
- NOT_APPLICABLE: the seed does not exercise this invariant at all (no relevant \
fortify sink / struct / attribute), or the evidence is insufficient to decide.
- ERROR: the evidence is missing or unreadable.

Hard rules:
- Be conservative. If you are not sure a bug actually fired, do NOT say FAIL — say \
NOT_APPLICABLE. A false FAIL is the worst possible outcome.
- Ground every verdict in the concrete evidence (cite the symbol or the exact \
disassembled instruction). Put that citation in `evidence`.
- `confidence` in [0,1] is how sure you are of a FAIL specifically.

CRITICAL — a SIZE_MAX / (size_t)-1 object-size (`mov x3, #-1` / `movn x3, #0`) is \
NOT by itself a bug. __builtin_object_size legitimately returns -1 ("unknown") \
whenever the destination is an interior pointer formed with a RUNTIME-VARIABLE \
offset (e.g. `p->arr + (cond ? 4 : 3)`, `base + i`, a pointer chosen by a \
branch). That is documented, correct behaviour and the fortify check is simply \
size-unbounded there — it is NOT the compiler defect these invariants target. \
Only call it FAIL when the size SHOULD have been statically known: a direct \
reference to the field with at most a COMPILE-TIME-CONSTANT offset, AND the seed \
actually writes MORE bytes than the field's real capacity (a genuine overflow \
the check must have caught). If the offset into the object is data-dependent, or \
no overflow is even attempted, the correct verdict is NOT_APPLICABLE.

Also note these are historical, version-specific compiler bugs. On a patched \
toolchain the size is computed correctly: a sensible small constant at the chk \
site is a PASS, not evidence of anything wrong.
"""

_USER_PROMPT = """Invariant {inv_id}: {title}

STATEMENT:
{statement}

FAIL signal:
{violation_signal}

PASS signal:
{pass_signal}

NOT_APPLICABLE signal:
{na_signal}

Deterministic backend said: {det_verdict} — reason: {det_reason}

C SEED SOURCE:
```c
{source}
```

BINARY EVIDENCE (read-only):
{evidence}

Decide invariant {inv_id} now."""


class LLMOracleVerdict(BaseModel):
    """Structured judgement the LLM must return for one invariant."""

    verdict: Literal["PASS", "FAIL", "NOT_APPLICABLE", "ERROR"] = Field(
        description="four-state verdict for this invariant on this binary"
    )
    confidence: float = Field(
        default=0.0, description="0..1 certainty that a FAIL (real bug) fired"
    )
    evidence: str = Field(
        default="", description="concrete citation: the symbol or disasm line proving the verdict"
    )
    reason: str = Field(default="", description="one-sentence justification")


_VERDICT_MAP = {
    "PASS": Verdict.PASS,
    "FAIL": Verdict.FAIL,
    "NOT_APPLICABLE": Verdict.NOT_APPLICABLE,
    "ERROR": Verdict.ERROR,
}


def _binary_for_isa(bb: Blackboard, isa: str) -> tuple[str, str]:
    """Pick a successfully built binary for `isa` (its cells share one binary)."""
    for a in bb.build_artifacts:
        if a.cell.isa == isa and a.success and a.binary_path:
            return a.binary_path, a.cell.isa
    # Fall back to any successful binary (single-ISA aarch64 runs have one).
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
    """Fallback judge for the non-programmable FORTIFY invariants."""

    def __init__(self, mechanism: str, llm_config: LLMConfig | None = None) -> None:
        self._mechanism = mechanism
        self._model = build_chat_model(llm_config)

    async def _judge(
        self, result: InvariantResult, source: str, evidence: str
    ) -> LLMOracleVerdict:
        brief = brief_for(result.id)
        if brief is None:
            return LLMOracleVerdict(verdict="NOT_APPLICABLE")
        messages = [
            ("system", _SYSTEM_PROMPT),
            (
                "user",
                _USER_PROMPT.format(
                    inv_id=brief.id,
                    title=brief.title,
                    statement=brief.statement,
                    violation_signal=brief.violation_signal,
                    pass_signal=brief.pass_signal,
                    na_signal=brief.na_signal,
                    det_verdict=result.verdict,
                    det_reason=result.reason or "(none)",
                    source=source,
                    evidence=evidence,
                ),
            ),
        ]
        structured = self._model.with_structured_output(
            LLMOracleVerdict, method="function_calling"
        )
        return await structured.ainvoke(messages)

    async def adjudicate(self, bb: Blackboard) -> list[InvariantResult]:
        """Return one LLM InvariantResult per undecided target invariant."""
        targets = _targets(bb)
        if not targets or bb.current_seed is None:
            return []

        source = bb.current_seed.source
        out: list[InvariantResult] = []
        # Evidence is per-ISA; cache so repeated invariants on one ISA reuse it.
        ev_cache: dict[str, str] = {}
        for r in targets:
            if r.isa not in ev_cache:
                binary_path, isa = _binary_for_isa(bb, r.isa)
                ev_cache[r.isa] = binary_evidence.collect(binary_path, isa).render()
            verdict = await self._judge(r, source, ev_cache[r.isa])
            mapped = _VERDICT_MAP.get(verdict.verdict, Verdict.ERROR)
            # Demote a low-confidence FAIL to NOT_APPLICABLE: never a false bug.
            if mapped is Verdict.FAIL and verdict.confidence < _FAIL_CONFIDENCE_THRESHOLD:
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


def promote_verdict(
    base: OracleVerdict | None, llm_results: list[InvariantResult], seed_id: str
) -> OracleVerdict | None:
    """Build the violated OracleVerdict if any LLM result is a (confident) FAIL.

    Returns None when nothing is promoted (the deterministic verdict stands).
    """
    first_fail = next((r for r in llm_results if r.verdict is Verdict.FAIL), None)
    if first_fail is None:
        return None
    from ..state import Aggregate

    base_results = list(base.results) if base is not None else []
    return OracleVerdict(
        seed_id=seed_id,
        results=base_results + llm_results,
        aggregate=Aggregate.VIOLATED,
        failing_checker=first_fail.id,
        failing_isa=first_fail.isa,
        evidence=f"[llm-oracle] {first_fail.evidence or first_fail.reason}",
    )
