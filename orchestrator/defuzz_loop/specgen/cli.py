"""specgen subcommand handler — invoked by ``../cli.py``'s ``main()``.

Builds a ``PipelineConfig`` from parsed args and runs the offline invariant
generation pipeline (plan §CLI). The corpus root points at a GCC source tree;
seed / baseline roots default to the sibling ``defend-reviewer-invariants`` repo
but can be overridden. The run is offline by default (``--transcript`` replays
authored judgments); passing no transcript uses the live ``LLMJudge``.
"""

from __future__ import annotations

import argparse
from datetime import UTC, datetime
from pathlib import Path

from ..llm import LLMConfig
from .pipeline import PipelineConfig, run_pipeline

# Sibling repo holding the DREV findings + historical bug corpus + survey docs.
_DEFAULT_DR_ROOT = Path("/Users/bytedance/projects/defend-reviewer-invariants")
_RUNS_ROOT = Path(__file__).resolve().parents[2] / "runs"


def _default_out() -> Path:
    stamp = datetime.now(UTC).strftime("%Y%m%d-%H%M%S")
    return _RUNS_ROOT / f"specgen_{stamp}"


def add_parser(sub: argparse._SubParsersAction) -> None:
    """Register the ``specgen`` subparser on the shared top-level parser."""
    sg = sub.add_parser("specgen", help="offline RAG cross-mechanism invariant generation")
    sg.add_argument(
        "--seed-source",
        default="findings",
        help="comma list of seed pools: findings,bugs (default: findings)",
    )
    sg.add_argument(
        "--corpus-root",
        required=True,
        help="path to the GCC source tree's gcc/ dir (the retrieval corpus)",
    )
    sg.add_argument(
        "--retriever", default="bm25", choices=["bm25"], help="retriever backend (v1: bm25)"
    )
    sg.add_argument(
        "--out", default=None, help="staging output dir (default: orchestrator/runs/specgen_<ts>)"
    )
    sg.add_argument(
        "--dr-root",
        default=str(_DEFAULT_DR_ROOT),
        help="defend-reviewer-invariants repo root (findings/bugs/survey)",
    )
    sg.add_argument(
        "--findings-root",
        default=None,
        help="override DREV findings dir (default: <dr-root>/findings)",
    )
    sg.add_argument(
        "--bugs-root",
        default=None,
        help="override historical bugs dir (default: <dr-root>/docs/bugs)",
    )
    sg.add_argument(
        "--invariants-root",
        default=None,
        help="override survey invariants dir (default: <dr-root>/docs/invariants)",
    )
    sg.add_argument(
        "--cache-root",
        default=None,
        help="corpus/bugzilla cache dir (default: <out>/cache)",
    )
    sg.add_argument("--top-k", type=int, default=8, help="hits kept per seed after exit filter")
    sg.add_argument(
        "--dedup-threshold",
        type=float,
        default=85.0,
        help="BM25 novelty threshold; nearest score >= this demotes a candidate "
        "(calibrated from the baseline's p95 intra nearest-neighbor score)",
    )
    sg.add_argument(
        "--no-bugzilla", action="store_true", help="skip live Bugzilla fetch (source-only corpus)"
    )
    sg.add_argument(
        "--transcript",
        default=None,
        help="replay/record judgments from this JSON file (offline path); omit for live LLM",
    )
    sg.add_argument(
        "--llm-config", default=None, help="path to llm.yaml (live path; default: configs/llm.yaml)"
    )
    sg.add_argument(
        "--seed-id",
        action="append",
        default=[],
        metavar="ID",
        help="restrict to these seed ids (repeatable); default: all seeds in the pools",
    )


def _cfg_from_args(args: argparse.Namespace) -> PipelineConfig:
    dr_root = Path(args.dr_root)
    out_dir = Path(args.out) if args.out else _default_out()
    cache_root = Path(args.cache_root) if args.cache_root else out_dir / "cache"
    sources = [s.strip() for s in args.seed_source.split(",") if s.strip()]

    findings_root = Path(args.findings_root) if args.findings_root else dr_root / "findings"
    bugs_root = Path(args.bugs_root) if args.bugs_root else dr_root / "docs" / "bugs"
    invariants_root = (
        Path(args.invariants_root)
        if args.invariants_root
        else dr_root / "docs" / "invariants"
    )

    llm_config = None
    if args.transcript is None:
        llm_config = LLMConfig.load(args.llm_config) if args.llm_config else LLMConfig.load()

    return PipelineConfig(
        seed_sources=sources,
        gcc_root=Path(args.corpus_root),
        findings_root=findings_root,
        bugs_root=bugs_root,
        invariants_root=invariants_root,
        out_dir=out_dir,
        cache_root=cache_root,
        top_k=args.top_k,
        dedup_threshold=args.dedup_threshold,
        include_bugzilla=not args.no_bugzilla,
        transcript=Path(args.transcript) if args.transcript else None,
        llm_config=llm_config,
        seed_ids=list(args.seed_id) or None,
    )


async def run(args: argparse.Namespace) -> None:
    cfg = _cfg_from_args(args)
    print(f"specgen out dir: {cfg.out_dir}")
    result = await run_pipeline(cfg)
    print(f"seeds: {len(result.seeds)}  corpus: {result.corpus_size} chunks")
    print(
        f"accepted: {len(result.accepted)}  "
        f"(demoted dup: {len(result.demoted)})  "
        f"rejected: {len(result.rejected) + len(result.grounding_rejected)}  "
        f"pending: {result.pending}"
    )
    promotable = [c for c in result.accepted if c.novelty is None or c.novelty.is_novel]
    for c in promotable:
        print(f"  CAND {c.origin_mechanism} -> {c.hit_mechanism}  [{c.seed_id}]  {c.chunk_id}")
    if result.pending:
        print(f"pending judgments to author: {cfg.out_dir / 'pending.json'}")
