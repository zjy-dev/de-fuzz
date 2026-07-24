"""Stage 5 — two static grounding gates over a candidate.

Gate 1 · evidence entailment (``TASK_ENTAILMENT``): the candidate ``statement``
must be *entailed by the hit text itself*, which is in the prompt. This kills
"generated from memory" — a statement that reads plausibly but is not supported
by the concrete chunk is rejected. This is the ChatDetector static-consistency
route: check the claim against the first-party source, not against model priors.

Gate 2 · falsifiability / triage (deterministic): the candidate must carry a
concrete, externally-observable violation (``observation`` + a non-empty
``falsifiability.observability``). No trigger sample is required — constructing
a reproducer is downstream audit + Phase-2 differential compilation, not the
generation loop's job (plan §"此闸不要求触发样本").

A candidate passes only when both gates pass; the verdict (and the rejection
reason) is recorded on ``Candidate.grounding`` for the RQ2 ablation analysis.
"""

from __future__ import annotations

from pydantic import BaseModel, Field

from .judge import TASK_ENTAILMENT, Judge, PendingJudgment
from .schema import Candidate, GroundingResult

_ENTAIL_SYSTEM = """You verify that a proposed invariant is ENTAILED by a \
concrete piece of source evidence — not merely plausible.

You are given a candidate invariant statement and the exact source chunk it was \
derived from. Decide whether the chunk actually SUPPORTS the statement: does the \
code shown perform (or clearly bear on) the operation the statement constrains? \

- entailed=true: the chunk contains the operation/site the statement is about, \
so the statement is a claim about THIS code, grounded in what is shown.
- entailed=false: the statement generalizes beyond, or diverges from, what the \
chunk shows (it was written from memory / prior knowledge, not from this \
evidence). Reserve true for statements the evidence genuinely supports.

Be strict. A statement that could be true but is not visible in this chunk is \
NOT entailed. Cite the supporting line/symbol in `support`."""

_ENTAIL_USER = """CANDIDATE STATEMENT:
{statement}

DERIVED-FROM SOURCE CHUNK ({path}):
```
{chunk}
```

Is the statement entailed by (grounded in) this chunk?"""

# --- differential (cross-ISA coverage) entailment --------------------------
# A differential candidate is grounded on a CORRECT cross-ISA reference: the chunk
# implements the enforcing step, and the invariant is the requirement that every
# backend do so. The default gate would reject it ("the chunk shows correct
# behavior, no violation here") — which is precisely backwards: correct-by-
# construction reference code is the positive exemplar the coverage rule stands on.
# So the differential gate flips the burden: entailed=true iff the enforcing
# operation the statement names is CONCRETELY PRESENT in this chunk (anti-
# hallucination), regardless of whether a violation is visible.
_DIFF_ENTAIL_SYSTEM = """You verify that a proposed CROSS-TARGET COVERAGE invariant \
is GROUNDED in a concrete reference implementation — not invented from memory.

You are given a coverage-requirement statement (of the form "every backend \
implementing mechanism M must perform enforcing step E; one that omits E is \
vulnerable") and the exact source chunk it was derived from. The chunk is a \
CORRECT reference implementation on one backend — it is EXPECTED to do E right. \
Your job is NOT to find a bug in this chunk. Your job is to confirm the chunk \
actually performs the enforcing step E that the statement requires.

- entailed=true: the chunk concretely performs (or defines) the enforcing step E \
the statement names — so E is a real, implementable operation and the coverage \
rule is grounded in first-party evidence. This is the expected verdict for a \
faithful reference.
- entailed=false: the enforcing step E the statement names is NOT present in this \
chunk — the statement was written from memory / prior knowledge and this chunk \
does not actually exhibit E. Also false if the statement's E and the chunk's \
operation are unrelated.

Do NOT set entailed=false merely because the chunk is correct / shows no bug — a \
correct reference is exactly what grounds a coverage requirement. Cite the \
supporting line/symbol (the code that performs E) in `support`."""

_DIFF_ENTAIL_USER = """COVERAGE-REQUIREMENT STATEMENT:
{statement}

REFERENCE CHUNK ({path}) — a correct implementation on one backend:
```
{chunk}
```

Does this chunk concretely perform the enforcing step the statement requires?"""

_MAX_CHUNK_CHARS = 4000


class EntailmentJudgment(BaseModel):
    """The judge's structured output for TASK_ENTAILMENT."""

    entailed: bool = Field(description="is the statement grounded in the chunk")
    support: str = Field(default="", description="the line/symbol that supports it")
    reason: str = Field(default="", description="one sentence justification")


def _clip(text: str) -> str:
    return text if len(text) <= _MAX_CHUNK_CHARS else text[:_MAX_CHUNK_CHARS] + "\n/* ...clipped */"


def _falsifiable(candidate: Candidate) -> bool:
    return bool(
        candidate.observation.strip() and candidate.falsifiability.observability.strip()
    )


async def ground_candidate(judge: Judge, candidate: Candidate) -> GroundingResult:
    """Run the two gates; return the verdict (never raises PendingJudgment)."""
    # Gate 2 is deterministic and cheap — check it first so a non-falsifiable
    # candidate never spends a judgment call.
    if not _falsifiable(candidate):
        return GroundingResult(
            evidence_entailed=False,
            falsifiable=False,
            accepted=False,
            reason="not-falsifiable: empty observation/observability",
        )

    # Gate 1: evidence entailment (judgment). A differential candidate is grounded
    # on a correct cross-ISA reference, so it uses the present-the-enforcing-step
    # prompt; the default prompt would wrongly reject correct reference code for
    # "showing no violation".
    entail_system = _DIFF_ENTAIL_SYSTEM if candidate.differential else _ENTAIL_SYSTEM
    entail_user = (_DIFF_ENTAIL_USER if candidate.differential else _ENTAIL_USER).format(
        statement=candidate.statement,
        path=candidate.source_url_or_path,
        chunk=_clip(candidate.evidence_snippet),
    )
    try:
        verdict: EntailmentJudgment = await judge.complete(
            task=TASK_ENTAILMENT,
            key=f"{candidate.seed_id}::{candidate.chunk_id}",
            system=entail_system,
            user=entail_user,
            output_model=EntailmentJudgment,
        )
    except PendingJudgment:
        return GroundingResult(
            evidence_entailed=False,
            falsifiable=True,
            accepted=False,
            reason="pending-entailment-judgment",
        )

    if not verdict.entailed:
        return GroundingResult(
            evidence_entailed=False,
            falsifiable=True,
            accepted=False,
            reason=f"not-entailed: {verdict.reason}",
        )

    return GroundingResult(
        evidence_entailed=True,
        falsifiable=True,
        accepted=True,
        reason=verdict.support,
    )


async def ground_candidates(
    judge: Judge, candidates: list[Candidate]
) -> tuple[list[Candidate], list[Candidate]]:
    """Ground each candidate; return (accepted, rejected) with grounding attached."""
    accepted: list[Candidate] = []
    rejected: list[Candidate] = []
    for c in candidates:
        result = await ground_candidate(judge, c)
        c.grounding = result
        (accepted if result.accepted else rejected).append(c)
    return accepted, rejected
