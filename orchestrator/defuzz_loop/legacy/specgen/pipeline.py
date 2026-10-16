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

import hashlib
import json
import os
import subprocess
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path

from ..llm import LLMConfig
from . import corpus as corpus_mod
from .dedup import assess_novelty, build_baseline
from .embedding import EmbeddingClient, EmbeddingConfig
from .generate import generate_candidates
from .grounding import ground_candidates
from .judge import Judge, PendingJudgment, TranscriptJudge, build_judge
from .query import _harvest_exact_anchors, distill_query, distill_signature_query
from .retriever import (
    BM25Retriever,
    EmbeddingRetriever,
    HybridRetriever,
    Retriever,
    retrieve,
)
from .schema import Candidate, Chunk, Rejected, Seed, SeedQuery
from .seeds import load_seeds


@dataclass
class PipelineConfig:
    seed_sources: list[str]
    gcc_root: Path | None = None
    findings_root: Path | None = None
    bugs_root: Path | None = None
    invariants_root: Path | None = None
    out_dir: Path = Path(".")
    cache_root: Path = Path(".")
    corpus_root: Path | None = None
    compiler: str = "gcc"
    version: str | None = None
    top_k: int = 8
    # Over-fetch multiplier: retrieve top_k*over_fetch raw hits before the exit
    # filter so a full slate of top_k *survivors* remains after same-mechanism /
    # self-anchor hits are dropped. Exposed so a run can widen recall.
    over_fetch: int = 4
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
    # Retrieval backend: "bm25" (lexical v1) or "embedding" (dense Phase 2).
    retriever: str = "bm25"
    embedding_config: EmbeddingConfig | None = None
    # Query construction: "abstract" (mechanism-neutral root-cause paraphrase,
    # the v1 cross-mechanism default) or "signature" (PropertyGPT-style: distil a
    # structure-preserving signature from the buggy function bodies the seed's
    # evidence names, so retrieval keeps mechanism/target specificity). Signature
    # signature mode is meant to be paired with a dense-capable retriever (embedding/hybrid);
    # under bm25 a code-heavy query collides on corpus boilerplate.
    query_mode: str = "abstract"
    # Reuse a previously-built ``<cache_root>/corpus.jsonl`` instead of rescanning
    # ``gcc_root``. The cached corpus pins chunk line numbers to the GCC version it
    # was built from (evidence anchors are line-exact), so re-running seed slices
    # or evaluating query/filter changes must reuse the SAME corpus rather than
    # re-chunk a different local tree. Falls back to a fresh build if absent.
    reuse_corpus: bool = False
    require_non_empty_corpus: bool = False

    def __post_init__(self) -> None:
        if self.corpus_root is not None and self.gcc_root is not None:
            if self.compiler == "gcc" and Path(self.corpus_root) != Path(self.gcc_root):
                raise ValueError("corpus_root and compatibility gcc_root disagree")
        selected = self.corpus_root if self.corpus_root is not None else self.gcc_root
        if selected is None:
            raise ValueError("corpus_root is required")
        self.corpus_root = Path(selected)
        self.gcc_root = Path(self.gcc_root) if self.gcc_root is not None else None
        self.out_dir = Path(self.out_dir)
        self.cache_root = Path(self.cache_root)
        self.compiler = self.compiler.strip().lower()
        if self.compiler == "clang":
            self.compiler = "llvm"
        if self.compiler not in {"gcc", "llvm"}:
            raise ValueError("compiler must be 'gcc' or 'llvm'")
        if self.compiler == "gcc" and self.gcc_root is None:
            self.gcc_root = self.corpus_root
        if self.version is None:
            self.version = corpus_mod.GCC_VERSION if self.compiler == "gcc" else ""


def _compiler_cache_path(cache_root: Path, filename: str, compiler: str) -> Path:
    if compiler == "gcc":
        return cache_root / filename
    name = Path(filename)
    return cache_root / f"{name.stem}-{compiler}{name.suffix}"


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _corpus_source_identity(cfg: PipelineConfig) -> dict[str, object]:
    assert cfg.corpus_root is not None
    digest = hashlib.sha256()
    files = 0
    for path in corpus_mod.curated_source_paths(cfg.corpus_root, cfg.compiler):
        relative = path.relative_to(cfg.corpus_root).as_posix()
        digest.update(relative.encode("utf-8"))
        digest.update(b"\0")
        if path.is_file():
            digest.update(path.read_bytes())
            files += 1
        else:
            digest.update(b"<missing>")
        digest.update(b"\0")
    return {
        "schema_version": 1,
        "adapter": f"curated-{cfg.compiler}-v1",
        "compiler": cfg.compiler,
        "version": cfg.version or "",
        "source_revision": _repo_sha(cfg.corpus_root),
        "source_content_sha256": digest.hexdigest(),
        "source_files": files,
        "include_bugzilla": cfg.include_bugzilla if cfg.compiler == "gcc" else False,
    }


def _identity_digest(identity: dict[str, object]) -> str:
    payload = json.dumps(identity, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def _write_corpus_metadata(
    path: Path,
    *,
    identity: dict[str, object],
    identity_sha256: str,
    corpus_path: Path,
) -> None:
    path.write_text(
        json.dumps(
            {
                "identity": identity,
                "identity_sha256": identity_sha256,
                "corpus_sha256": _sha256_file(corpus_path),
            },
            indent=2,
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )


def _validate_corpus_chunks(cfg: PipelineConfig, chunks: list[Chunk]) -> None:
    expected_compiler = "GCC" if cfg.compiler == "gcc" else "LLVM"
    invalid_compilers = sorted(
        {
            chunk.metadata.compiler
            for chunk in chunks
            if chunk.metadata.compiler != expected_compiler
        }
    )
    if invalid_compilers:
        raise ValueError(
            f"{cfg.compiler} corpus cache contains foreign compiler metadata: "
            + ", ".join(value or "<empty>" for value in invalid_compilers)
        )
    chunk_ids = [chunk.chunk_id for chunk in chunks]
    if len(chunk_ids) != len(set(chunk_ids)):
        raise ValueError(f"{cfg.compiler} corpus contains duplicate chunk IDs")
    if cfg.compiler == "llvm" and any(
        chunk.metadata.version != (cfg.version or "") for chunk in chunks
    ):
        raise ValueError("llvm corpus cache version metadata does not match the target version")


def _build_retriever(
    cfg: PipelineConfig, *, cache_identity: str
) -> tuple[Retriever, str]:
    """Construct the retrieval backend named by ``cfg.retriever``.

    ``bm25`` (lexical v1), ``embedding`` (dense Phase 2), or ``hybrid`` (RRF
    fusion of both — the P0 recall+precision upgrade).
    """

    def _dense() -> tuple[EmbeddingRetriever, str]:
        ecfg = cfg.embedding_config or EmbeddingConfig.load()
        api_key = os.environ.get(ecfg.api_key_env, "")
        client = EmbeddingClient(ecfg, api_key=api_key)
        cache_path = _compiler_cache_path(cfg.cache_root, "embeddings.json", cfg.compiler)
        embedding_identity = (
            cache_identity
            if cfg.compiler != "gcc" or cfg.require_non_empty_corpus
            else None
        )
        return (
            EmbeddingRetriever(
                client, cache_path=cache_path, cache_identity=embedding_identity
            ),
            ecfg.model,
        )

    if cfg.retriever == "embedding":
        return _dense()
    if cfg.retriever == "hybrid":
        dense, dense_name = _dense()
        return HybridRetriever(BM25Retriever(), dense), f"hybrid(bm25+{dense_name})"
    return BM25Retriever(), "bm25"


def _bodies_for_symbols(
    by_sym: dict[str, list[Chunk]], symbols: list[str]
) -> list[str]:
    """Corpus function texts for the given symbols (signature-query material)."""
    out: list[str] = []
    for sym in symbols:
        out.extend(c.text for c in by_sym.get(sym, []))
    return out


async def _distill(
    cfg: PipelineConfig, judge: Judge, seed: Seed, by_sym: dict[str, list[Chunk]]
) -> SeedQuery:
    """Build the seed's retrieval query per ``cfg.query_mode``.

    ``abstract`` is the mechanism-neutral v1 path. ``signature`` resolves the
    seed's defect-site symbols (its exact anchors) to corpus function bodies and
    asks the judge for a structure-preserving signature. If no anchor resolves to
    a corpus body there is nothing structural to distil, so we fall back to the
    abstract query.
    """
    if cfg.query_mode != "signature":
        return await distill_query(judge, seed)
    anchors = _harvest_exact_anchors(seed)
    defect_bodies = _bodies_for_symbols(by_sym, anchors)
    if not defect_bodies:
        return await distill_query(judge, seed)
    return await distill_signature_query(
        judge, seed, defect_bodies=defect_bodies, reference_bodies=[]
    )


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


def render_candidate_md(
    c: Candidate,
    index: int,
    *,
    default_compiler: str = "GCC",
    default_version: str = corpus_mod.GCC_VERSION,
) -> str:
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
        f"- **compiler**: {c.compiler or default_compiler}\n"
        f"- **version**: {c.version or default_version}\n"
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


async def run_pipeline(
    cfg: PipelineConfig, *, judge_override: Judge | None = None
) -> PipelineResult:
    cfg.out_dir.mkdir(parents=True, exist_ok=True)
    cfg.cache_root.mkdir(parents=True, exist_ok=True)

    # Stage 0 — seeds.
    seeds = load_seeds(
        cfg.seed_sources,
        findings_root=cfg.findings_root,
        bugs_root=cfg.bugs_root,
        invariants_root=cfg.invariants_root,
        compiler=cfg.compiler,
    )
    if cfg.seed_ids:
        wanted = set(cfg.seed_ids)
        seeds = [s for s in seeds if s.seed_id in wanted]

    # Stage 2 — corpus (cache to disk for reproducibility / offline replay).
    assert cfg.corpus_root is not None
    source_identity = _corpus_source_identity(cfg)
    source_identity_sha256 = _identity_digest(source_identity)
    corpus_path = _compiler_cache_path(cfg.cache_root, "corpus.jsonl", cfg.compiler)
    corpus_meta_path = corpus_path.with_suffix(".meta.json")
    if cfg.reuse_corpus and corpus_path.exists():
        if not corpus_meta_path.is_file():
            if cfg.compiler != "gcc":
                raise ValueError(
                    f"cannot reuse corpus cache without identity metadata: {corpus_meta_path}"
                )
            legacy = corpus_mod.load_corpus(corpus_path)
            rebuilt = corpus_mod.build_corpus(
                cfg.corpus_root,
                cache_root=cfg.cache_root,
                include_bugzilla=cfg.include_bugzilla,
                compiler=cfg.compiler,
                version=cfg.version,
            )
            if [chunk.model_dump() for chunk in legacy] != [
                chunk.model_dump() for chunk in rebuilt
            ]:
                raise ValueError(
                    "legacy GCC corpus cache cannot be validated against current source"
                )
            _write_corpus_metadata(
                corpus_meta_path,
                identity=source_identity,
                identity_sha256=source_identity_sha256,
                corpus_path=corpus_path,
            )
        cached_identity = json.loads(corpus_meta_path.read_text(encoding="utf-8"))
        if cached_identity.get("identity") != source_identity:
            raise ValueError(
                f"corpus cache identity mismatch for {cfg.compiler}: {corpus_path}"
            )
        if cached_identity.get("corpus_sha256") != _sha256_file(corpus_path):
            raise ValueError(f"corpus cache content hash mismatch: {corpus_path}")
        chunks = corpus_mod.load_corpus(corpus_path)
    else:
        chunks = corpus_mod.build_corpus(
            cfg.corpus_root,
            cache_root=cfg.cache_root,
            include_bugzilla=cfg.include_bugzilla,
            compiler=cfg.compiler,
            version=cfg.version,
        )
        # Never clobber an existing pinned corpus with an empty build (e.g. a
        # missing/rescanned gcc_root produced 0 chunks). An empty overwrite would
        # destroy the line-exact corpus every reuse run depends on.
        if chunks:
            corpus_mod.write_corpus(chunks, corpus_path)
            _write_corpus_metadata(
                corpus_meta_path,
                identity=source_identity,
                identity_sha256=source_identity_sha256,
                corpus_path=corpus_path,
            )

    if not chunks and cfg.require_non_empty_corpus:
        raise ValueError(
            f"empty {cfg.compiler} retrieval corpus at {cfg.corpus_root}; "
            "the curated adapter found no usable chunks"
        )
    if chunks:
        _validate_corpus_chunks(cfg, chunks)
    corpus_sha256 = _sha256_file(corpus_path) if corpus_path.is_file() else ""
    cache_identity = _identity_digest(
        {
            "source_identity": source_identity,
            "corpus_sha256": corpus_sha256,
        }
    )

    # Stage 3 — index once, reuse across seeds.
    retriever, retriever_name = _build_retriever(cfg, cache_identity=cache_identity)
    retriever.index(chunks)

    # Symbol → corpus chunks, for the signature query mode (function-body lookup).
    by_sym: dict[str, list[Chunk]] = {}
    if cfg.query_mode == "signature":
        for c in chunks:
            if c.metadata.symbol:
                by_sym.setdefault(c.metadata.symbol, []).append(c)

    if judge_override is None:
        judge, tj = build_judge(transcript=cfg.transcript, llm_config=cfg.llm_config)
    else:
        judge, tj = judge_override, None

    result = PipelineResult(seeds=seeds, corpus_size=len(chunks))

    for seed in seeds:
        # Stage 1 — distill (judgment).
        try:
            sq = await _distill(cfg, judge, seed, by_sym)
        except PendingJudgment:
            continue

        # Stage 3 — retrieve + exit filter (deterministic).
        survivors, _dropped = retrieve(
            retriever, sq, top_k=cfg.top_k, over_fetch=cfg.over_fetch
        )

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
    for candidate in result.accepted:
        if candidate.novelty is not None and not candidate.novelty.is_novel:
            result.demoted.append(candidate)

    _write_staging(
        cfg,
        result,
        corpus_path,
        tj,
        retriever_name,
        source_identity=source_identity,
        cache_identity=cache_identity,
        corpus_sha256=corpus_sha256,
    )
    return result


def _write_staging(
    cfg: PipelineConfig,
    result: PipelineResult,
    corpus_path: Path,
    tj: TranscriptJudge | None,
    retriever_name: str,
    *,
    source_identity: dict[str, object],
    cache_identity: str,
    corpus_sha256: str,
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
            render_candidate_md(
                c,
                i,
                default_compiler="GCC" if cfg.compiler == "gcc" else "LLVM",
                default_version=cfg.version or "",
            ),
            encoding="utf-8",
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
        "seed_policy": {
            "compiler": cfg.compiler,
            "bugs_root": str(cfg.bugs_root) if cfg.bugs_root is not None else None,
            "findings_enabled": "findings" in cfg.seed_sources,
            "seed_identities": [s.identity or s.seed_id for s in result.seeds],
        },
        "compiler": cfg.compiler,
        "version": cfg.version,
        "corpus_root": str(cfg.corpus_root),
        "gcc_root": str(cfg.gcc_root) if cfg.gcc_root is not None else None,
        "corpus_path": str(corpus_path),
        "corpus_identity": source_identity,
        "corpus_identity_sha256": cache_identity,
        "corpus_sha256": corpus_sha256,
        "corpus_size": result.corpus_size,
        "retriever": retriever_name,
        "query_mode": cfg.query_mode,
        "top_k": cfg.top_k,
        "over_fetch": cfg.over_fetch,
        "dedup_threshold": cfg.dedup_threshold,
        "include_bugzilla": cfg.include_bugzilla if cfg.compiler == "gcc" else False,
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
