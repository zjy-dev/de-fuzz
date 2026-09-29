"""``defuzz-loop campaign`` sub-command (Rev 3).

Wires the parallel launcher to production dependencies: builds an
:class:`IterationLoop` per campaign, capturing shared MCP / gRPC / catalog
objects. LangGraph is gone; the intra-campaign flow is the explicit
Python loop in :mod:`defuzz_loop.loop`.

MCP integration for the LLM-facing subagents is still TODO — the
:class:`SubagentRunner` currently receives all its data through the
Python-assembled prompt (see the TODO markers in
``.opencode/opencode.jsonc`` and each ``.opencode/agents/*.md``).
"""

from __future__ import annotations

import argparse
import asyncio
import logging
import sys
from pathlib import Path

log = logging.getLogger("defuzz-loop.campaign")


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(
        prog="defuzz-loop campaign",
        description="Run one IterationLoop campaign per invariant in parallel.",
    )
    p.add_argument("--mechanism", required=True)
    p.add_argument("--phi-ids", nargs="*", default=None)
    p.add_argument("--budget-rounds", type=int, default=8)
    p.add_argument("--budget-secs", type=float, default=0.0)
    p.add_argument("--parallel", type=int, default=4)
    p.add_argument("--grpc", default="127.0.0.1:50051")
    p.add_argument("--mcp", default="http://127.0.0.1:50052/mcp")
    p.add_argument("--run-dir", type=Path, default=Path("runs"))
    p.add_argument("--experiment", default="campaign")
    p.add_argument(
        "--disable-agent",
        action="append",
        default=[],
        choices=["feedback", "minimizer", "llm_oracle"],
    )
    p.add_argument("--opencode-cwd", type=Path, default=Path.cwd())
    p.add_argument("--model", default=None, help="Optional OpenCode model override.")
    p.add_argument("--log-level", default="INFO", choices=["DEBUG", "INFO", "WARN", "ERROR"])
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    logging.basicConfig(
        level=args.log_level,
        format="%(asctime)s %(levelname)-5s %(name)s: %(message)s",
    )

    # Local imports keep ``import defuzz_loop.campaign`` cheap.
    from ..agents.feedback import FeedbackAgent
    from ..agents.generator import GeneratorAgent
    from ..agents.llm_oracle import LLMOracleAgent
    from ..agents.minimizer import MinimizerAgent
    from ..clients.grpc_client import CoreClient
    from ..clients.mcp_client import MCPClient
    from ..loop import IterationLoop
    from ..opencode.client import OpenCodeClient
    from ..opencode.runner import SubagentRunner
    from ..routing import CheckerCatalog
    from ..state import Blackboard
    from .parallel import run_parallel_campaigns
    from .single import CampaignConfig, CampaignResult, run_single_invariant_campaign

    disabled = set(args.disable_agent)

    async def _drive() -> int:
        with CoreClient(args.grpc) as client:
            catalog = CheckerCatalog(client)
            mcp = MCPClient(args.mcp)
            oc_client = OpenCodeClient()
            runner = SubagentRunner(
                oc_client,
                cwd=args.opencode_cwd,
                model=args.model,
            )

            phi_ids = args.phi_ids
            if not phi_ids:
                phi_ids = [
                    cid
                    for cid in catalog.all_ids
                    if _scoped_to_mechanism(cid, args.mechanism)
                ]
                if not phi_ids:
                    log.error("no checkers matched mechanism=%r", args.mechanism)
                    return 2
            log.info(
                "dispatching %d campaigns (parallel=%d)", len(phi_ids), args.parallel
            )

            generator = GeneratorAgent(args.mechanism, mcp, runner)
            feedback = (
                None
                if "feedback" in disabled
                else FeedbackAgent(args.mechanism, mcp, runner)
            )
            minimizer = (
                None
                if "minimizer" in disabled
                else MinimizerAgent(args.mechanism, mcp, runner)
            )
            llm_oracle = (
                None
                if "llm_oracle" in disabled
                else LLMOracleAgent(args.mechanism, runner)
            )

            def _loop_builder() -> IterationLoop:
                return IterationLoop(
                    generator=generator,
                    feedback=feedback,
                    llm_oracle=llm_oracle,
                    minimizer=minimizer,
                    client=client,
                    catalog=catalog,
                )

            def _bb_factory(**kwargs: object) -> Blackboard:
                return Blackboard(**kwargs)

            async def _runner(cfg: CampaignConfig) -> CampaignResult:
                return await run_single_invariant_campaign(
                    cfg,
                    loop_builder=_loop_builder,
                    blackboard_factory=_bb_factory,
                )

            configs = [
                CampaignConfig(
                    phi_id=pid,
                    mechanism=args.mechanism,
                    budget_rounds=args.budget_rounds,
                    budget_secs=args.budget_secs,
                )
                for pid in phi_ids
            ]
            results = await run_parallel_campaigns(
                configs, _runner, max_parallel=args.parallel
            )
            n_ok = sum(1 for r in results if r.ok)
            log.info("campaigns done: %d/%d ok", n_ok, len(results))
            for r in results:
                verdict = "OK" if r.ok else f"FAIL ({r.error!r})"
                log.info("  %s [%.1fs] %s", r.phi_id, r.duration_secs, verdict)
            return 0 if n_ok == len(results) else 1

    return asyncio.run(_drive())


def _scoped_to_mechanism(checker_id: str, mechanism: str) -> bool:
    return checker_id.lower().startswith(mechanism.lower())


if __name__ == "__main__":
    sys.exit(main())
