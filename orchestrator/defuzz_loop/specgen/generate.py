"""Stage 4 — turn a retrieval Hit into a grounded candidate invariant.

A Hit is a *lead*, not a conclusion: BM25 matched words, which may signal a real
same-root-cause site in a sister mechanism, or may just be a lexical collision.
This stage does judgment, not translation, in three discardable gates:

1. **analogy alignment** (``TASK_ANALOGY``) — the "wide" bar: does the hit
   *contain* an operation structurally isomorphic to the seed's root cause?
   ``does_analogy_hold=False`` drops the hit (kills BM25 false positives). It
   does NOT ask the model to pre-judge a bug — truth is the grounding gate's
   and the downstream auditor's job.
2. **specialize** (``TASK_SPECIALIZE``) — write mechanism B's OWN rule (not a
   renamed seed statement) plus the externally-observable violation and a
   README §3 four-dimension self-assessment.
3. **falsifiability face** — require a concrete ``observation`` (no trigger
   program is demanded; that is downstream audit + Phase-2 differential compile).

Evidence fields are program-injected from the Hit (``source_url_or_path`` =
``path:line``, ``evidence_snippet`` = hit text), never taken from the model, so
a citation can never be fabricated.
"""

from __future__ import annotations

from .judge import TASK_ANALOGY, TASK_SPECIALIZE, Judge, PendingJudgment
from .schema import (
    AnalogyJudgment,
    Candidate,
    CandidateDraft,
    Hit,
    Rejected,
    SeedQuery,
)

_ANALOGY_SYSTEM = """You judge whether a retrieved code chunk from one defense \
mechanism contains an operation that is STRUCTURALLY ISOMORPHIC to a known \
root-cause operation from a DIFFERENT defense mechanism.

You are given: the mechanism-agnostic root-cause phrase distilled from a seed \
defect, and a chunk of source from a sister mechanism that a lexical search \
returned. Decide ONE thing: does this chunk actually perform an operation of the \
SAME SHAPE as the root cause?

Set does_analogy_hold=true ONLY if you can point to a concrete operation in the \
chunk (cite the line or symbol) that is the same KIND of operation as the root \
cause — e.g. both narrow a wider value to a fixed-width type, both leave a \
secret live in a register across an edge, both attach metadata to the wrong \
instruction, both bake a stale value at compile time. Lexical coincidence \
(the same word appears but the operation is unrelated) is does_analogy_hold=false.

This is the WIDE bar: you are NOT asked whether there is a bug here. You are \
asked only whether the same-shaped operation is PRESENT. Truth/bug-hunting is a \
later step. When unsure whether the shapes truly match, prefer false — a false \
analogy pollutes everything downstream."""

_ANALOGY_USER = """SEED root-cause (mechanism-agnostic): {root_cause}
SEED origin mechanism (for context only; do NOT require the chunk to match it): \
{origin_mechanism}
Cross-cutting tokens: {tokens}

RETRIEVED CHUNK (mechanism: {hit_mechanism}, {path}):
```
{chunk}
```

Does this chunk contain an operation structurally isomorphic to the seed root \
cause? Cite the concrete line/symbol in aligned_operation."""

_SPECIALIZE_SYSTEM = """You write a NEW, falsifiable invariant for the defense \
mechanism of a retrieved code chunk, by analogy to a known root-cause operation \
from a different mechanism.

You are given the seed root cause (mechanism-agnostic), the retrieved chunk, and \
a confirmed structural analogy. Write the rule THIS chunk's mechanism must obey \
at this site so that the same class of root-cause operation cannot silently \
defeat it.

Rules:
- statement: describe THIS mechanism's own property at THIS site. Do NOT rename \
the seed's statement. (Example: seed = "a size-carrying field's count must not \
be narrowed to a fixed-width type"; a stack-clash hit = "the residual stack \
size passed to the probe loop bound must retain its full width; narrowing it to \
a 32-bit type before the loop test lets an oversized frame skip a guard page".)
- observation: the externally observable phenomenon WHEN the invariant is \
violated (what you would SEE — a wrong immediate in disassembly, a missing \
probe, a value that wraps). Describe the phenomenon, NOT how to detect it. This \
must be concrete and non-empty, or the candidate is rejected as unfalsifiable.
- version_sensitivity: stable | target-specific | likely-to-drift.
- falsifiability: fill all four (observability, determinism, cost, \
static_or_dynamic per README §3). observability must be non-empty.

Do NOT invent file paths, line numbers, or a trigger program. Evidence is \
attached mechanically."""

_SPECIALIZE_USER = """SEED root-cause (mechanism-agnostic): {root_cause}
CONFIRMED ANALOGY:
  aligned_operation: {aligned_operation}
  protected_asset:   {protected_asset}
  why_analogous:     {why_analogous}

RETRIEVED CHUNK (mechanism: {hit_mechanism}, {path}):
```
{chunk}
```

Write the new invariant this chunk's mechanism must obey at this site."""

# Cap the chunk text handed to the judge so the prompt stays bounded.
_MAX_CHUNK_CHARS = 4000


def _key(seed_id: str, chunk_id: str) -> str:
    return f"{seed_id}::{chunk_id}"


def _clip(text: str) -> str:
    return text if len(text) <= _MAX_CHUNK_CHARS else text[:_MAX_CHUNK_CHARS] + "\n/* ...clipped */"


async def generate_candidates(
    judge: Judge, sq: SeedQuery, hits: list[Hit]
) -> tuple[list[Candidate], list[Rejected]]:
    """Run the three-gate transform over every hit; return (candidates, rejected).

    A ``PendingJudgment`` (TranscriptJudge miss) skips that hit without aborting
    the batch, so an offline run produces every candidate it *can* author and a
    worklist for the rest.
    """
    candidates: list[Candidate] = []
    rejected: list[Rejected] = []
    key = _key

    for hit in hits:
        chunk = _clip(hit.text)
        # Gate 1: analogy alignment.
        try:
            judg: AnalogyJudgment = await judge.complete(
                task=TASK_ANALOGY,
                key=key(sq.seed_id, hit.chunk_id),
                system=_ANALOGY_SYSTEM,
                user=_ANALOGY_USER.format(
                    root_cause=sq.root_cause_phrase,
                    origin_mechanism=sq.origin_mechanism,
                    tokens=", ".join(sq.agnostic_tokens),
                    hit_mechanism=hit.metadata.mechanism,
                    path=f"{hit.metadata.path}:{hit.metadata.line}",
                    chunk=chunk,
                ),
                output_model=AnalogyJudgment,
            )
        except PendingJudgment:
            continue
        if not judg.does_analogy_hold:
            rejected.append(
                Rejected(
                    seed_id=sq.seed_id,
                    stage="analogy",
                    reason="analogy-not-hold",
                    chunk_id=hit.chunk_id,
                    detail=judg.why_analogous,
                )
            )
            continue

        # Gate 2: specialize into mechanism B's own rule.
        try:
            draft: CandidateDraft = await judge.complete(
                task=TASK_SPECIALIZE,
                key=key(sq.seed_id, hit.chunk_id),
                system=_SPECIALIZE_SYSTEM,
                user=_SPECIALIZE_USER.format(
                    root_cause=sq.root_cause_phrase,
                    aligned_operation=judg.aligned_operation,
                    protected_asset=judg.protected_asset,
                    why_analogous=judg.why_analogous,
                    hit_mechanism=hit.metadata.mechanism,
                    path=f"{hit.metadata.path}:{hit.metadata.line}",
                    chunk=chunk,
                ),
                output_model=CandidateDraft,
            )
        except PendingJudgment:
            continue

        # Gate 3: falsifiability face — a concrete observable violation is required.
        if not draft.observation.strip() or not draft.falsifiability.observability.strip():
            rejected.append(
                Rejected(
                    seed_id=sq.seed_id,
                    stage="specialize",
                    reason="not-falsifiable",
                    chunk_id=hit.chunk_id,
                    detail=draft.statement,
                )
            )
            continue

        meta = hit.metadata
        candidates.append(
            Candidate(
                seed_id=sq.seed_id,
                origin_mechanism=sq.origin_mechanism,
                hit_mechanism=meta.mechanism,
                statement=draft.statement.strip(),
                observation=draft.observation.strip(),
                version_sensitivity=draft.version_sensitivity,
                source_kind=meta.source_kind,
                # Program-injected evidence — never from the model.
                source_url_or_path=(
                    f"{meta.path}:{meta.line}" if meta.line else meta.path
                ),
                evidence_snippet=hit.text,
                compiler=meta.compiler,
                version=meta.version,
                target=meta.isa,
                analogy=judg,
                falsifiability=draft.falsifiability,
                chunk_id=hit.chunk_id,
                score=hit.score,
            )
        )

    return candidates, rejected
