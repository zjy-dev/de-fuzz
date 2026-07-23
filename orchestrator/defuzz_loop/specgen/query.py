"""Stage 1 — distill a Seed into a mechanism-agnostic SeedQuery.

This is SpecAuditor's *generalize* step, but the target is "transferable across
mechanisms" rather than "across similar APIs". The judge reads the seed's
``impact`` + ``why_not_rescued`` + ``notes`` and returns two fields:

- ``root_cause_phrase`` — one mechanism-agnostic sentence naming the root-cause
  operation (the ONLY natural-language text that enters retrieval).
- ``agnostic_tokens`` — the cross-cutting tokens left after every
  mechanism-specific noun is stripped (``counted_by`` → "a size-carrying
  field", ``bdos`` → "a computed object size", ...).

The hard rule (enforced in the prompt and re-checked by ``mechanism_leak``) is
that neither field may contain a mechanism-specific noun. That stripping is the
only thing that lets a FORTIGY seed's query land in a *sister* mechanism's code
instead of rediscovering FORTIFY.

``exact_anchors`` is NOT produced by the judge: it is program-injected from the
seed's harvested anchors + its violated-invariant id. Those are exit-filter
fuel and must never enter retrieval.
"""

from __future__ import annotations

import re

from pydantic import BaseModel, Field

from .judge import TASK_DISTILL, Judge
from .schema import Seed, SeedQuery

_SYSTEM_PROMPT = """You distill a known compiler-hardening defect into a \
MECHANISM-AGNOSTIC root-cause query. The query will be used to search the source \
of OTHER defense mechanisms for the same class of root-cause operation.

You are given one seed defect: its violated invariant, its impact, why later \
layers do not rescue it, and notes. All of that is specific to ONE mechanism \
(e.g. _FORTIFY_SOURCE, stack-protector, CET, PAC). Your job is to name the \
underlying operation in terms that are TRUE OF MANY MECHANISMS.

Return exactly two things:
1. root_cause_phrase: ONE sentence naming the root-cause operation in \
mechanism-neutral language. Describe what was done wrong to a value/quantity/ \
register/layout, NOT which mechanism it happened in.
2. agnostic_tokens: 4-10 short cross-cutting tokens (single words or hyphenated) \
that capture the operation, with every mechanism-specific noun removed.

HARD RULES (a violation makes the query useless):
- NO mechanism-specific nouns anywhere. Replace them with generic descriptions:
  counted_by / __counted_by  -> "a size-carrying field"
  __builtin_object_size / bdos / object-size -> "a computed object size / bound"
  __memcpy_chk / _chk / fortify -> "a bounded library call / guard bound"
  canary / __stack_chk_guard -> "a stack integrity secret"
  endbr / landing pad         -> "an indirect-branch target marker"
  paciasp / autiasp / PAC     -> "a signed pointer / return-address signature"
  CFA / .cfi_* / unwind        -> "frame-state metadata"
- Prefer the ABSTRACT OPERATION: truncation, narrowing, sign-extension, \
signed/unsigned confusion, register residue / secret left live, ordering / \
relative-position constraint, missing clobber, stale value baked at compile \
time, note/marker emitted before the value it describes is valid.
- The phrase must be falsifiable in the abstract: it names an operation a \
reviewer could look for in unfamiliar code."""

_USER_PROMPT = """Seed {seed_id} (origin mechanism: {origin_mechanism}).

VIOLATED INVARIANT:
{violated_invariant}

IMPACT:
{impact}

WHY LATER LAYERS DO NOT RESCUE:
{why_not_rescued}

NOTES:
{notes}

Distill the mechanism-agnostic root-cause query now. Remember: no \
mechanism-specific nouns in either field."""

# Mechanism-specific nouns that must not survive into the query. Used by the
# leak checker (a negative test the plan's test_specgen_query.py asserts).
_LEAK_TERMS = (
    "counted_by",
    "__counted_by",
    "bdos",
    "object_size",
    "object-size",
    "__builtin_object_size",
    "__builtin_dynamic_object_size",
    "fortify",
    "_fortify_source",
    "memcpy_chk",
    "__memcpy_chk",
    "strcpy_chk",
    "_chk",
    "canary",
    "__stack_chk_guard",
    "stack_chk",
    "stack-protector",
    "endbr",
    "endbr64",
    "endbr32",
    "paciasp",
    "autiasp",
    "pac-ret",
    "cfi_def_cfa",
    "access_with_size",
)


class QueryDistillation(BaseModel):
    """The judge's structured output for TASK_DISTILL."""

    root_cause_phrase: str = Field(
        description="one mechanism-neutral sentence naming the root-cause operation"
    )
    agnostic_tokens: list[str] = Field(
        default_factory=list,
        description="4-10 mechanism-stripped cross-cutting tokens",
    )


def mechanism_leak(text: str) -> list[str]:
    """Return the mechanism-specific terms that leaked into ``text`` (empty=clean)."""
    low = text.lower()
    return [t for t in _LEAK_TERMS if t in low]


def _harvest_exact_anchors(seed: Seed) -> list[str]:
    """Exit-filter fuel: the seed's anchors + its violated-invariant id(s)."""
    anchors: set[str] = set(seed.anchors)
    if seed.violated_invariant:
        for m in re.findall(r"\bINV-[A-Z0-9]+-[A-Z]+\d+\b", seed.violated_invariant):
            anchors.add(m)
    return sorted(anchors)


async def distill_query(judge: Judge, seed: Seed) -> SeedQuery:
    """Stage 1: run the judge's distillation, program-inject the exact anchors."""
    user = _USER_PROMPT.format(
        seed_id=seed.seed_id,
        origin_mechanism=seed.origin_mechanism,
        violated_invariant=seed.violated_invariant or "(none)",
        impact=seed.impact or "(none)",
        why_not_rescued=seed.why_not_rescued or "(none)",
        notes=seed.notes or "(none)",
    )
    out: QueryDistillation = await judge.complete(
        task=TASK_DISTILL,
        key=seed.seed_id,
        system=_SYSTEM_PROMPT,
        user=user,
        output_model=QueryDistillation,
    )
    return SeedQuery(
        seed_id=seed.seed_id,
        origin_mechanism=seed.origin_mechanism,
        origin_isas=list(seed.origin_isas),
        violated_invariant=seed.violated_invariant,
        root_cause_phrase=out.root_cause_phrase.strip(),
        agnostic_tokens=[t.strip() for t in out.agnostic_tokens if t.strip()],
        exact_anchors=_harvest_exact_anchors(seed),
    )


# --- function-signature distillation (PropertyGPT-style query) -------------
# The abstract-phrase query above deliberately strips identifiers so a seed can
# jump to a *sister mechanism*. The empirical cost (probe_funcbody_query.py +
# eval_retrieval_p0.py) is that retrieval recall already saturates yet the sites
# it lands on are mechanism-*relevant*, not mechanism-*failure* sites — the audit
# critique. PropertyGPT instead queries with the function under analysis. We keep
# that idea but distill a *behavioral signature* rather than pasting the raw body:
# a whole 50k-char body under BM25 collides on boilerplate (`rtx`/`insn`/`return`
# sit in 40-65% of chunks) and degenerates to same-file neighbours (73% measured).
# The signature keeps the security-relevant verbs/operands and drops boilerplate,
# so it is usable by BOTH bm25 and dense (and is the intended query for hybrid).

_SIGNATURE_SYSTEM = """You distill the SECURITY-RELEVANT SIGNATURE of a compiler \
hardening defect from the buggy function(s) it lives in. Unlike a mechanism-\
agnostic paraphrase, you KEEP the concrete operations, data-flow and control-flow \
that make the defect checkable, because the signature will retrieve OTHER sites \
(sister mechanisms AND sister targets) that share the same enforcing/omitting \
structure.

You are given the seed's violated invariant plus the body of the function(s) the \
seed's evidence names as the defect site and (when present) a reference site that \
does it correctly.

Return:
1. signature_phrase: 1-2 sentences naming the enforcing action that is present-\
and-correct at the reference site but ABSENT/broken at the defect site — phrased \
as the *guarantee that must hold*, in terms a reviewer can look for (e.g. "a \
register that transiently held a secret is overwritten before the return insn", \
"a bound operand is decremented by the same offset the pointer advanced").
2. structure_tokens: 6-14 tokens naming the concrete operations/opcodes/hooks \
involved (emit_move_insn, clobber, targetm.have_*, return edge, POINTER_PLUS, \
objsize, ...). Keep target/mechanism identifiers that denote the STRUCTURE; drop \
prose words.

Anchor on the FAILURE: name the enforcing step and where it is missing, not just \
the topic area."""

_SIGNATURE_USER = """Seed {seed_id} (origin mechanism: {origin_mechanism}).

VIOLATED INVARIANT (the guarantee that was broken):
{violated_invariant}

DEFECT-SITE FUNCTION BODIES (the code that omits/breaks the enforcing step):
{defect_bodies}

REFERENCE-SITE FUNCTION BODIES (does it correctly — may be empty):
{reference_bodies}

Distill the security-relevant signature now."""


class SignatureDistillation(BaseModel):
    """The judge's structured output for the function-signature query mode."""

    signature_phrase: str = Field(
        description="1-2 sentences naming the enforcing guarantee that is absent at the defect site"
    )
    structure_tokens: list[str] = Field(
        default_factory=list,
        description="6-14 concrete operation/opcode/hook tokens naming the structure",
    )


async def distill_signature_query(
    judge: Judge, seed: Seed, *, defect_bodies: list[str], reference_bodies: list[str]
) -> SeedQuery:
    """Stage 1 (query-mode=signature): distill a structure-preserving query.

    ``defect_bodies`` / ``reference_bodies`` are the corpus function texts the
    caller resolved from the seed's anchors (defect site) and its "reference /
    fixed path" evidence. Bodies are clipped so the prompt stays bounded; the
    resulting ``root_cause_phrase`` + ``agnostic_tokens`` feed the identical
    retriever path, so bm25 / embedding / hybrid all consume it unchanged.
    """
    user = _SIGNATURE_USER.format(
        seed_id=seed.seed_id,
        origin_mechanism=seed.origin_mechanism,
        violated_invariant=seed.violated_invariant or "(none)",
        defect_bodies="\n\n".join(b[:2500] for b in defect_bodies) or "(none)",
        reference_bodies="\n\n".join(b[:2500] for b in reference_bodies) or "(none)",
    )
    out: SignatureDistillation = await judge.complete(
        task=TASK_DISTILL,
        key=seed.seed_id,
        system=_SIGNATURE_SYSTEM,
        user=user,
        output_model=SignatureDistillation,
    )
    return SeedQuery(
        seed_id=seed.seed_id,
        origin_mechanism=seed.origin_mechanism,
        origin_isas=list(seed.origin_isas),
        violated_invariant=seed.violated_invariant,
        root_cause_phrase=out.signature_phrase.strip(),
        agnostic_tokens=[t.strip() for t in out.structure_tokens if t.strip()],
        exact_anchors=_harvest_exact_anchors(seed),
    )
