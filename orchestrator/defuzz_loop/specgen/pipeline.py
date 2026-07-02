"""Stage orchestration (0 → 6) and staging output for the specgen pipeline.

``run_pipeline`` wires the deterministic stages (seed parse, corpus build, BM25
retrieval, exit filter, falsifiability, dedup) with the judgment stages (query
distill, analogy, specialize, entailment) behind the ``Judge`` protocol, so the
same code runs live (``LLMJudge``) or offline-replay (``TranscriptJudge``).

Staging artifacts written under ``out_dir`` (plan §CLI):

- ``candidates.jsonl`` — every accepted candidate (full model).
- ``accepted/<n>.md`` — the survey-format markdown block for each accepted
  candidate, ready for human promotion into the SSOT.
- ``rejected.jsonl`` — every discarded item with its stage + reason (for RQ2).
- ``manifest.json`` — git sha, corpus/seed counts, retriever, thresholds.
- ``pending.json`` — the TranscriptJudge worklist (offline path only).
"""

from __future__ import annotations

import json
import subprocess
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path

from ..llm import LLMConfig
from . import corpus as corpus_mod
from .dedup import assess_novelty, build_baseline
from .generate import generate_candidates
from .grounding import ground_candidates
from .judge import PendingJudgment, TranscriptJudge, build_judge
from .query import distill_query
from .retriever import BM25Retriever, Retriever, retrieve
from .schema import Candidate, Rejected, Seed
from .seeds import load_seeds


@dataclass
class PipelineConfig:
    seed_sources: list[str]
    gcc_root: Path
    findings_root: Path | None
    bugs_root: Path | None
    invariants_root: Path | None
    out_dir: Path
    cache_root: Path
    top_k: int = 8
    # BM25 raw scores are length-sensitive and corpus-relative. Calibrated from
    # the real 450-entry baseline's intra nearest-neighbor distribution
    # (p50≈36, p90≈68, p95≈88): flag a candidate as a near-duplicate only when it
    # is more lexically similar to an existing entry than ~95% of distinct
    # invariant pairs are to each other. Override per-run with --dedup-threshold.
    dedup_threshold: float = 85.0
    include_bugzilla: bool = True
    transcript: Path | None = None
    llm_config: LLMConfig | None = None
    seed_ids: list[str] | None = None  # restrict to these seed ids (slice runs)


@dataclass
class PipelineResult:
    seeds: list[Seed]
    corpus_size: int
    accepted: list[Candidate] = field(default_factory=list)
    demoted: list[Candidate] = field(default_factory=list)
    rejected: list[Rejected] = field(default_factory=list)
    grounding_rejected: list[Candidate] = field(default_factory=list)
    pending: int = 0


def _repo_sha(root: Path) -> str:
    try:
        out = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )
        return out.stdout.strip()
    except (subprocess.SubprocessError, OSError):
        return ""


def render_candidate_md(c: Candidate, index: int) -> str:
    """Render an accepted candidate as a survey-format invariant block (README §2)."""
    novelty = ""
    if c.novelty is not None:
        novelty = (
            f"- **novelty**: is_novel={c.novelty.is_novel} "
            f"(nearest {c.novelty.nearest_id or '-'} @ {c.novelty.nearest_score:.2f})\n"
        )
    analogy = ""
    if c.analogy is not None:
        analogy = (
            f"- **analogy**: {c.origin_mechanism} → {c.hit_mechanism}; "
            f"{c.analogy.why_analogous}\n"
        )
    snippet = c.evidence_snippet.strip()
    if len(snippet) > 1200:
        snippet = snippet[:1200] + "\n/* ...clipped */"
    return (
        f"### CAND-{index:03d} — {c.hit_mechanism} (from {c.seed_id})\n\n"
        f"- **statement**: {c.statement}\n"
        f"- **compiler**: {c.compiler or 'GCC'}\n"
        f"- **version**: {c.version or 'gcc-16.1.0'}\n"
        f"- **target**: {c.target or 'generic'}\n"
        f"- **source_kind**: {c.source_kind}\n"
        f"- **source_url_or_path**: `{c.source_url_or_path}`\n"
        f"- **version_sensitivity**: {c.version_sensitivity}\n"
        f"- **observation**: {c.observation}\n"
        f"- **origin_mechanism**: {c.origin_mechanism}\n"
        f"- **hit_mechanism**: {c.hit_mechanism}\n"
        f"- **seed_id**: {c.seed_id}\n"
        f"{analogy}"
        f"{novelty}"
        f"- **evidence_snippet**:\n\n```\n{snippet}\n```\n"
    )


async def run_pipeline(cfg: PipelineConfig) -> PipelineResult:
    cfg.out_dir.mkdir(parents=True, exist_ok=True)
    cfg.cache_root.mkdir(parents=True, exist_ok=True)

    # Stage 0 — seeds.
    seeds = load_seeds(
        cfg.seed_sources,
        findings_root=cfg.findings_root,
        bugs_root=cfg.bugs_root,
    )
    if cfg.seed_ids:
        wanted = set(cfg.seed_ids)
        seeds = [s for s in seeds if s.seed_id in wanted]

    # Stage 2 — corpus (cache to disk for reproducibility / offline replay).
    corpus_path = cfg.cache_root / "corpus.jsonl"
    chunks = corpus_mod.build_corpus(
        cfg.gcc_root, cache_root=cfg.cache_root, include_bugzilla=cfg.include_bugzilla
    )
    corpus_mod.write_corpus(chunks, corpus_path)

    # Stage 3 — index once, reuse across seeds.
    retriever: Retriever = BM25Retriever()
    retriever.index(chunks)

    judge, tj = build_judge(transcript=cfg.transcript, llm_config=cfg.llm_config)

    result = PipelineResult(seeds=seeds, corpus_size=len(chunks))

    for seed in seeds:
        # Stage 1 — distill (judgment).
        try:
            sq = await distill_query(judge, seed)
        except PendingJudgment:
            continue

        # Stage 3 — retrieve + exit filter (deterministic).
        survivors, _dropped = retrieve(retriever, sq, top_k=cfg.top_k)

        # Stage 4 — three-gate transform (judgment).
        cands, rej = await generate_candidates(judge, sq, survivors)
        result.rejected.extend(rej)

        # Stage 5 — two grounding gates (judgment + deterministic).
        accepted, ground_rej = await ground_candidates(judge, cands)
        result.grounding_rejected.extend(ground_rej)
        result.accepted.extend(accepted)

    # Stage 6 — novelty / dedup (deterministic).
    baseline = build_baseline(
        invariants_root=cfg.invariants_root, findings_root=cfg.findings_root
    )
    assess_novelty(baseline, result.accepted, threshold=cfg.dedup_threshold)
    for c in result.accepted:
        if c.novelty is not None and not c.novelty.is_novel:
            result.demoted.append(c)

    _write_staging(cfg, result, corpus_path, tj)
    return result


def _write_staging(
    cfg: PipelineConfig,
    result: PipelineResult,
    corpus_path: Path,
    tj: TranscriptJudge | None,
) -> None:
    out = cfg.out_dir

    with (out / "candidates.jsonl").open("w", encoding="utf-8") as fh:
        for c in result.accepted:
            fh.write(c.model_dump_json() + "\n")

    accepted_dir = out / "accepted"
    accepted_dir.mkdir(exist_ok=True)
    promotable = [c for c in result.accepted if c.novelty is None or c.novelty.is_novel]
    for i, c in enumerate(promotable, start=1):
        (accepted_dir / f"{i:03d}.md").write_text(
            render_candidate_md(c, i), encoding="utf-8"
        )

    with (out / "rejected.jsonl").open("w", encoding="utf-8") as fh:
        for r in result.rejected:
            fh.write(r.model_dump_json() + "\n")
        for c in result.grounding_rejected:
            fh.write(
                Rejected(
                    seed_id=c.seed_id,
                    stage="grounding",
                    reason=(c.grounding.reason if c.grounding else "grounding-failed"),
                    chunk_id=c.chunk_id,
                    detail=c.statement,
                ).model_dump_json()
                + "\n"
            )

    if tj is not None:
        result.pending = tj.dump_pending(out / "pending.json")

    manifest = {
        "created_at": datetime.now(UTC).isoformat(),
        "git_sha": _repo_sha(Path.cwd()),
        "seed_sources": cfg.seed_sources,
        "seed_ids": [s.seed_id for s in result.seeds],
        "gcc_root": str(cfg.gcc_root),
        "corpus_path": str(corpus_path),
        "corpus_size": result.corpus_size,
        "retriever": "bm25",
        "top_k": cfg.top_k,
        "dedup_threshold": cfg.dedup_threshold,
        "include_bugzilla": cfg.include_bugzilla,
        "transcript": str(cfg.transcript) if cfg.transcript else None,
        "llm": (
            {"provider": cfg.llm_config.provider, "model": cfg.llm_config.model}
            if cfg.llm_config
            else None
        ),
        "counts": {
            "seeds": len(result.seeds),
            "accepted": len(result.accepted),
            "promotable": len(promotable),
            "demoted": len(result.demoted),
            "rejected": len(result.rejected) + len(result.grounding_rejected),
            "pending": result.pending,
        },
    }
    (out / "manifest.json").write_text(
        json.dumps(manifest, indent=2, ensure_ascii=False, default=str), encoding="utf-8"
    )
