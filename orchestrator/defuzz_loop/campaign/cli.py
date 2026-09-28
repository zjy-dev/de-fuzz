"""``defuzz-loop campaign`` sub-command.

Wires the parallel launcher to production dependencies. When invoked from
the CLI, it enumerates the mechanism's checker IDs (or takes an explicit
list) and dispatches one campaign per ID with a fan-out cap.

This file intentionally imports the heavy dependencies (LangGraph, gRPC
client, etc.) *inside* :func:`main` so that unit tests can import
:mod:`defuzz_loop.campaign.parallel` without paying the import cost.
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
        description="Run one LangGraph campaign per invariant in parallel.",
    )
    p.add_argument(
        "--mechanism", required=True,
        help="Defense mechanism family (e.g. stack_canary).",
    )
    p.add_argument(
        "--phi-ids",
        nargs="*",
        default=None,
        help=(
            "Explicit list of checker/invariant IDs. If omitted, all checkers "
            "scoped to --mechanism are used."
        ),
    )
    p.add_argument("--budget-rounds", type=int, default=8)
    p.add_argument("--budget-secs", type=float, default=0.0)
    p.add_argument("--parallel", type=int, default=4, help="Max concurrent campaigns.")
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
    p.add_argument("--llm-config", type=Path, default=None)
    p.add_argument("--log-level", default="INFO", choices=["DEBUG", "INFO", "WARN", "ERROR"])
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    logging.basicConfig(
        level=args.log_level,
        format="%(asctime)s %(levelname)-5s %(name)s: %(message)s",
    )

    # Local imports so ``import defuzz_loop.campaign`` stays cheap.
    from ..agents.feedback import FeedbackAgent
    from ..agents.generator import GeneratorAgent
    from ..agents.llm_oracle import LLMOracleAgent
    from ..agents.minimizer import MinimizerAgent
    from ..clients.grpc_client import CoreClient
    from ..clients.mcp_client import MCPClient
    from ..graph import build_graph
    from ..llm import LLMConfig
    from ..routing import CheckerCatalog
    from ..state import Blackboard
    from .parallel import build_default_runner, run_parallel_campaigns
    from .single import CampaignConfig

    llm_config = LLMConfig.load(args.llm_config) if args.llm_config else LLMConfig.load()
    disabled = set(args.disable_agent)

    async def _drive() -> int:
        with CoreClient(args.grpc) as client:
            catalog = CheckerCatalog(client)
            mcp = MCPClient(args.mcp)

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
            log.info("dispatching %d campaigns (parallel=%d)", len(phi_ids), args.parallel)

            generator = GeneratorAgent(args.mechanism, mcp, llm_config)
            feedback = (
                None if "feedback" in disabled else FeedbackAgent(args.mechanism, mcp, llm_config)
            )
            minimizer = None if "minimizer" in disabled else MinimizerAgent(args.mechanism, mcp)
            llm_oracle = (
                None if "llm_oracle" in disabled else LLMOracleAgent(args.mechanism, llm_config)
            )

            def _bb_factory(**kwargs: object) -> Blackboard:
                return Blackboard(**kwargs)

            runner = build_default_runner(
                build_graph=build_graph,
                generator=generator,
                catalog=catalog,
                client=client,
                blackboard_factory=_bb_factory,
                feedback=feedback,
                minimizer=minimizer,
                llm_oracle=llm_oracle,
                # NOTE: campaign runs do not share a checkpointer; each
                # campaign's thread_id is unique and no cross-campaign resume
                # is expected. The parent CLI (`defuzz-loop run`) still owns
                # the persistent checkpointer for whole-sweep runs.
                checkpointer=None,
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
                configs, runner, max_parallel=args.parallel
            )

            n_ok = sum(1 for r in results if r.ok)
            log.info("campaigns done: %d/%d ok", n_ok, len(results))
            for r in results:
                verdict = "OK" if r.ok else f"FAIL ({r.error!r})"
                log.info("  %s [%.1fs] %s", r.phi_id, r.duration_secs, verdict)

            return 0 if n_ok == len(results) else 1

    return asyncio.run(_drive())


def _scoped_to_mechanism(checker_id: str, mechanism: str) -> bool:
    """Mirror :func:`defuzz_loop.cli._scoped_target_queue`'s inclusion rule.

    Kept as a private helper so campaign CLI does not accidentally couple to
    an internal function of the main CLI.
    """

    return checker_id.lower().startswith(mechanism.lower())


if __name__ == "__main__":
    sys.exit(main())
