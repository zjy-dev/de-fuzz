"""Pydantic models for the cross-mechanism invariant generation pipeline.

The data objects flow stage 0 → 6:

    Seed        (stage 0) a known bug/patch/finding, mechanism-tagged
    SeedQuery   (stage 1) a mechanism-agnostic root-cause query distilled from a Seed
    Chunk       (stage 2) one indexed unit of corpus (source func / header / bug)
    Hit         (stage 3) a retrieved Chunk that survived the exit filter
    AnalogyJudgment / CandidateDraft  (stage 4) the LLM's two structured outputs
    Candidate   (stage 4) a grounded, cross-mechanism candidate invariant
    GroundingResult (stage 5) the two static grounding gates' verdict
    Rejected    (any stage) a discarded item with a machine-readable reason

Only ``root_cause_phrase`` + ``agnostic_tokens`` ever enter retrieval. The
``origin_mechanism`` / ``exact_anchors`` are exit-filter fuel and MUST NOT be
fed to BM25 (that is what keeps a seed from rediscovering itself).
"""

from __future__ import annotations

from pydantic import BaseModel, Field


class Seed(BaseModel):
    """A known defect/finding used as the starting entity (stage 0)."""

    seed_id: str
    origin_mechanism: str
    # The ISA(s) the seed's defect applies to (e.g. [mips, loongarch64]). Used by
    # the exit filter to distinguish "same mechanism, SAME target = self" (drop)
    # from "same mechanism, DIFFERENT target = the cross-ISA goal" (keep). Empty
    # for mechanism-neutral seeds (survey invariants, middle-end bugs).
    origin_isas: list[str] = Field(default_factory=list)
    source_kind: str = "finding"  # finding | historical-bug | bug-disclosure | invariant
    violated_invariant: str = ""
    # Free text the query distiller reads. Kept verbatim from the source doc.
    impact: str = ""
    why_not_rescued: str = ""
    notes: str = ""
    # Mechanism-specific anchors harvested from the seed (symbols, attrs, IDs).
    # These are the exit-filter fuel; they never enter retrieval.
    anchors: list[str] = Field(default_factory=list)
    tags: list[str] = Field(default_factory=list)
    source_path: str = ""  # provenance of the seed doc


class SeedQuery(BaseModel):
    """A mechanism-agnostic query distilled from one Seed (stage 1).

    ``root_cause_phrase`` + ``agnostic_tokens`` are the ONLY fields that enter
    retrieval. The rest are exit-filter fuel.
    """

    seed_id: str
    origin_mechanism: str
    # ISA(s) the seed's defect lives on; carried through from the Seed so the
    # exit filter can keep same-mechanism hits on a DIFFERENT target (the
    # cross-ISA differential goal) while still dropping the seed's own site.
    origin_isas: list[str] = Field(default_factory=list)
    violated_invariant: str = ""
    # The single mechanism-agnostic sentence that enters retrieval.
    root_cause_phrase: str
    # Mechanism-stripped cross-cutting tokens (truncation, narrowing, ...).
    agnostic_tokens: list[str] = Field(default_factory=list)
    # NEVER enters retrieval; a hit containing any of these = rediscovering self.
    exact_anchors: list[str] = Field(default_factory=list)

    def query_terms(self) -> list[str]:
        """The bag of terms handed to the retriever (phrase + agnostic tokens)."""
        return self.root_cause_phrase.split() + list(self.agnostic_tokens)


class ChunkMeta(BaseModel):
    """Provenance + routing metadata attached to every indexed chunk."""

    source_kind: str  # source | internals | header | bug-disclosure | survey | finding
    mechanism: str  # the defense mechanism this chunk belongs to
    compiler: str = ""  # GCC | LLVM | glibc | ""
    version: str = ""
    isa: str = ""
    path: str = ""  # file path or URL
    line: int = 0  # 1-based start line of the chunk in its file (0 = n/a)
    symbol: str = ""  # enclosing function / wrapper name when known


class Chunk(BaseModel):
    """One indexed unit of corpus (stage 2)."""

    chunk_id: str
    text: str
    metadata: ChunkMeta


class Hit(BaseModel):
    """A retrieved chunk that survived the exit filter (stage 3)."""

    chunk_id: str
    text: str
    metadata: ChunkMeta
    score: float


class AnalogyJudgment(BaseModel):
    """Stage 4 step 1 — the analogy-alignment structured output.

    ``does_analogy_hold=False`` means the BM25 hit is a lexical false positive
    (words collided, no structural isomorphism) and is discarded.
    """

    does_analogy_hold: bool
    aligned_operation: str = ""  # the operation in the hit isomorphic to the seed root cause
    protected_asset: str = ""  # what mechanism B protects here
    why_analogous: str = ""  # one sentence: why isomorphic to the seed root cause


class Falsifiability(BaseModel):
    """README §3 four-dimension self-assessment attached to a candidate draft."""

    observability: str = ""  # how a violation is externally observable (must be non-empty)
    determinism: str = ""  # is there a decisive truth condition
    cost: str = ""  # infra needed to check it
    static_or_dynamic: str = ""  # static | dynamic | both


class CandidateDraft(BaseModel):
    """Stage 4 step 2 — the specialize structured output (mechanism B's own rule)."""

    statement: str  # describes mechanism B's property, NOT a renamed seed statement
    observation: str  # externally observable phenomenon when violated
    version_sensitivity: str = "likely-to-drift"  # stable | target-specific | likely-to-drift
    falsifiability: Falsifiability = Field(default_factory=Falsifiability)


class GroundingResult(BaseModel):
    """Stage 5 — the two static grounding gates' verdict."""

    evidence_entailed: bool  # gate 1: statement entailed by the hit text
    falsifiable: bool  # gate 2: has a concrete, observable violation
    accepted: bool  # both gates passed
    reason: str = ""  # rejection reason when accepted is False


class Novelty(BaseModel):
    """Stage 6 — dedup verdict against the existing corpus (468 inv + 25 DREV)."""

    is_novel: bool
    nearest_id: str = ""
    nearest_score: float = 0.0


class Candidate(BaseModel):
    """A grounded, cross-mechanism candidate invariant (stage 4 output).

    Evidence fields (``source_url_or_path`` / ``evidence_snippet``) are program
    -injected from the Hit, never from the LLM, so a citation can never be
    fabricated.
    """

    seed_id: str
    origin_mechanism: str
    hit_mechanism: str  # the sister mechanism jumped to — cross-mechanism evidence
    statement: str
    observation: str
    version_sensitivity: str = "likely-to-drift"
    source_kind: str = "source"
    source_url_or_path: str = ""  # program-injected: f"{hit.path}:{hit.line}"
    evidence_snippet: str = ""  # program-injected: hit.text
    compiler: str = ""
    version: str = ""
    target: str = ""
    analogy: AnalogyJudgment | None = None
    falsifiability: Falsifiability = Field(default_factory=Falsifiability)
    grounding: GroundingResult | None = None
    novelty: Novelty | None = None
    chunk_id: str = ""
    score: float = 0.0
    # True when the hit is a cross-ISA SIBLING of the seed (same mechanism, a
    # different concrete backend): the chunk is a CORRECT reference implementing
    # the enforcing step, and the invariant is a coverage requirement ("every
    # backend must do E; one that omits it is the bug"). Selects the differential
    # specialize/grounding path — grounding verifies E is PRESENT here rather than
    # demanding a visible violation in reference code.
    differential: bool = False


class Rejected(BaseModel):
    """A discarded item, kept for RQ2 ablation analysis."""

    seed_id: str
    stage: str  # retrieval | analogy | specialize | grounding | dedup
    reason: str
    chunk_id: str = ""
    detail: str = ""
