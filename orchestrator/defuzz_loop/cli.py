"""Command-line entry point for the online loop (Rev 3).

LangGraph has been removed; the online loop now runs through
:class:`defuzz_loop.loop.IterationLoop`. The ``run`` subcommand still
enumerates the mechanism's checker catalog and sweeps each invariant with
its per-invariant budget, but the pipeline is now explicit Python.

The historical ``inspect`` / ``replay`` / ``trace-bug`` subcommands relied
on the LangGraph SqliteSaver checkpoint chain, which is gone in Rev 3.
They now emit a friendly message pointing at the per-round manifests
until we choose a Rev 3 replacement.
"""

from __future__ import annotations

import argparse
import asyncio
import os
from pathlib import Path
from typing import cast

from .agents.runner import SubagentRunner
from .audit import build_manifest, make_run_dir, write_manifest
from .clients.grpc_client import CoreClient
from .clients.mcp_client import MCPClient
from .experiment_engine.pi_backend import PiAgentBackend
from .llm import LLMConfig
from .loop import IterationLoop
from .round_artifacts import RoundArtifacts
from .routing import CheckerCatalog
from .specgen import cli as specgen_cli
from .state import Blackboard


def _parse_ablation(items: list[str]) -> dict[str, bool]:
    """Parse repeated ``--ablation edge=off|on`` into an override dict (SC-004)."""

    overrides: dict[str, bool] = {}
    for item in items:
        edge, _, val = item.partition("=")
        edge = edge.strip()
        val = val.strip().lower()
        if val not in {"on", "off"}:
            raise SystemExit(f"--ablation must be <edge>=on|off, got '{item}'")
        overrides[edge] = val == "on"
    return overrides


# Mechanism → checker-ID prefix, mirroring the Go core's mechanismOf.
_MECHANISM_PREFIX = {
    "canary": "INV-SP-",
    "ibt": "INV-IBT-",
    "fortify": "INV-FORT-",
}


def _scoped_target_queue(mechanism: str, all_ids: list[str]) -> list[str]:
    prefix = _MECHANISM_PREFIX.get(mechanism)
    if prefix is None:
        return list(all_ids)
    return [cid for cid in all_ids if cid.startswith(prefix)]


def _initial_blackboard(args: argparse.Namespace, target_queue: list[str]) -> Blackboard:
    bb = Blackboard(target_queue=list(target_queue))
    if args.budget_secs > 0:
        import time

        bb.target_started_at = time.time()
    overrides = _parse_ablation(getattr(args, "ablation", []))
    for edge, value in overrides.items():
        if not hasattr(bb.ablation_flags, edge):
            raise SystemExit(f"unknown ablation edge '{edge}'")
        setattr(bb.ablation_flags, edge, value)
    return bb


async def _run(args: argparse.Namespace) -> None:
    from .agents.feedback import FeedbackAgent
    from .agents.generator import GeneratorAgent
    from .agents.llm_oracle import LLMOracleAgent
    from .agents.minimizer import MinimizerAgent

    llm_config = (
        LLMConfig.load(args.llm_config) if args.llm_config else LLMConfig.load()
    )
    disabled = set(args.disable_agent)
    run_dir = make_run_dir(args.run_dir, args.experiment, args.mechanism)
    print(f"run dir: {run_dir}")

    with CoreClient(args.grpc) as client:
        catalog = CheckerCatalog(client)
        mcp = MCPClient(args.mcp)
        runner = SubagentRunner.from_config(
            args.pi_config, cwd=args.agent_cwd, output_dir=run_dir / "agents",
            metadata={"run_id": run_dir.name, "experiment": args.experiment},
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

        target_queue = _scoped_target_queue(args.mechanism, catalog.all_ids)
        initial = _initial_blackboard(args, target_queue)
        write_manifest(
            run_dir,
            build_manifest(
                experiment=args.experiment,
                mechanism=args.mechanism,
                budget_rounds=args.budget_rounds,
                budget_secs=args.budget_secs,
                grpc_addr=args.grpc,
                mcp_addr=args.mcp,
                llm_config=llm_config,
                blackboard=initial,
                disabled_agents=sorted(disabled),
                checker_ids=catalog.all_ids,
                runtime_metadata={
                    "backend": "pi-sdk",
                    "provider": cast(PiAgentBackend, runner.backend).config["model_provider"],
                    "model": cast(PiAgentBackend, runner.backend).config["model_id"],
                    "config_sha256": cast(PiAgentBackend, runner.backend).config_sha256,
                    "input_hashes": cast(PiAgentBackend, runner.backend).config["input_hashes"],
                },
            ),
        )

        artifacts = RoundArtifacts(run_dir, os.environ.get("DEFUZZ_WORK_ROOT"))
        loop = IterationLoop(
            generator=generator,
            feedback=feedback,
            llm_oracle=llm_oracle,
            minimizer=minimizer,
            client=client,
            catalog=catalog,
            on_round_complete=artifacts.complete,
        )
        try:
            final = await loop.run(
                initial,
                budget_rounds=args.budget_rounds,
                budget_secs=args.budget_secs,
            )
        except BaseException as exc:
            artifacts.finish(initial, error=f"{type(exc).__name__}: {exc}")
            raise
        artifacts.finish(final)

    bb = final
    print(f"rounds run: {artifacts.completed_rounds}")
    print(f"corpus size: {len(bb.corpus)}")
    print(f"verdicts: {[v.aggregate for v in bb.verdict_history]}")
    if bb.guidance is not None:
        print(f"guidance: {bb.guidance.summary}")
    if bb.pending_bug is not None:
        print(
            f"BUG: seed={bb.pending_bug.seed_id} "
            f"checker={bb.pending_bug.failing_checker} isa={bb.pending_bug.isa}"
        )
        if bb.minimized_poc is not None:
            print(
                f"  minimized: still_triggers={bb.minimized_poc.still_triggers} "
                f"({len(bb.minimized_poc.reduced_source)} chars)"
            )
    else:
        print("no violation found")


def _rev3_notice(command: str) -> None:
    print(
        f"'{command}' relied on LangGraph checkpoint chains removed in Rev 3.\n"
        "See docs/tech-docs/architecture/pi-sdk-migration.md for the "
        "replacement plan (per-round manifest + audit directory)."
    )


def main() -> None:
    parser = argparse.ArgumentParser(prog="defuzz-loop")
    sub = parser.add_subparsers(dest="command", required=True)

    run = sub.add_parser("run", help="run the online loop")
    run.add_argument("--mechanism", default="canary")
    run.add_argument("--experiment", default="exp")
    run.add_argument("--budget-rounds", type=int, default=1)
    run.add_argument("--budget-secs", type=float, default=0.0)
    run.add_argument("--grpc", default="localhost:50051")
    run.add_argument("--mcp", default="http://127.0.0.1:50052/mcp")
    run.add_argument("--llm-config", default=None)
    run.add_argument("--run-dir", default=None)
    run.add_argument(
        "--disable-agent",
        action="append",
        default=[],
        choices=["feedback", "minimizer", "llm_oracle"],
    )
    run.add_argument(
        "--ablation",
        action="append",
        default=[],
        metavar="EDGE=on|off",
    )
    run.add_argument(
        "--agent-cwd", type=Path, default=Path("."),
        help="Read-only workspace for online agents",
    )
    run.add_argument("--pi-config", type=Path, required=True, help="Pi runtime JSON")

    for stub_name in ("inspect", "replay", "trace-bug"):
        stub = sub.add_parser(stub_name, help=f"{stub_name} (Rev 3: retired)")
        stub.set_defaults(_rev3_notice=stub_name)

    specgen_cli.add_parser(sub)

    args = parser.parse_args()
    if args.command == "run":
        asyncio.run(_run(args))
    elif args.command in {"inspect", "replay", "trace-bug"}:
        _rev3_notice(args.command)
    elif args.command == "specgen":
        asyncio.run(specgen_cli.run(args))


if __name__ == "__main__":
    main()
