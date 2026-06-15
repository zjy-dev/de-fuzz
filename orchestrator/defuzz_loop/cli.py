"""Command-line entry point for the agentic loop.

Four subcommands over a per-run audit directory: `run` drives the LangGraph
pipeline; `inspect`/`replay`/`trace-bug` are read-only views over a run's
checkpoint chain (FR-009 / SC-003 / SC-005).
"""

from __future__ import annotations

import argparse
import asyncio

from .agents.feedback import FeedbackAgent
from .agents.generator import GeneratorAgent
from .agents.minimizer import MinimizerAgent
from .audit import (
    async_checkpointer,
    build_manifest,
    checkpointer,
    make_run_dir,
    thread_from_run_dir,
    thread_id,
    write_manifest,
)
from .clients.grpc_client import CoreClient
from .clients.mcp_client import MCPClient
from .graph import build_graph
from .llm import LLMConfig
from .routing import CheckerCatalog
from .state import Blackboard


def _parse_ablation(items: list[str]) -> dict[str, bool]:
    """Parse repeated --ablation edge=off|on into an override dict (SC-004)."""
    overrides: dict[str, bool] = {}
    for item in items:
        edge, _, val = item.partition("=")
        edge = edge.strip()
        val = val.strip().lower()
        if val not in {"on", "off"}:
            raise SystemExit(f"--ablation must be <edge>=on|off, got '{item}'")
        overrides[edge] = val == "on"
    return overrides


def _initial_blackboard(args: argparse.Namespace) -> Blackboard:
    bb = Blackboard()
    overrides = _parse_ablation(getattr(args, "ablation", []))
    for edge, value in overrides.items():
        if not hasattr(bb.ablation_flags, edge):
            raise SystemExit(f"unknown ablation edge '{edge}'")
        setattr(bb.ablation_flags, edge, value)
    return bb


async def _run(args: argparse.Namespace) -> None:
    llm_config = LLMConfig.load(args.llm_config) if args.llm_config else LLMConfig.load()
    disabled = set(args.disable_agent)
    run_dir = make_run_dir(args.run_dir, args.experiment, args.mechanism)
    print(f"run dir: {run_dir}")

    with CoreClient(args.grpc) as client:
        catalog = CheckerCatalog(client)
        mcp = MCPClient(args.mcp)
        generator = GeneratorAgent(args.mechanism, mcp, llm_config)
        feedback = (
            None if "feedback" in disabled else FeedbackAgent(args.mechanism, mcp, llm_config)
        )
        minimizer = None if "minimizer" in disabled else MinimizerAgent(args.mechanism, mcp)
        graph = build_graph(
            generator=generator,
            catalog=catalog,
            client=client,
            max_rounds=args.max_rounds,
            feedback=feedback,
            minimizer=minimizer,
        )

        initial = _initial_blackboard(args)
        write_manifest(
            run_dir,
            build_manifest(
                experiment=args.experiment,
                mechanism=args.mechanism,
                max_rounds=args.max_rounds,
                grpc_addr=args.grpc,
                mcp_addr=args.mcp,
                llm_config=llm_config,
                blackboard=initial,
                disabled_agents=sorted(disabled),
                checker_ids=catalog.all_ids,
            ),
        )

        async with async_checkpointer(run_dir) as saver:
            app = graph.compile(checkpointer=saver)
            config = {
                "configurable": {"thread_id": thread_id(args.experiment, args.mechanism)},
                "recursion_limit": 8 * args.max_rounds + 10,
            }
            final = await app.ainvoke(initial, config=config)

    bb = Blackboard.model_validate(final)
    print(f"rounds run: {bb.round + 1}")
    print(f"corpus size: {len(bb.corpus)}")
    print(f"verdicts: {[v.aggregate for v in bb.verdict_history]}")
    if bb.guidance is not None:
        print(f"guidance: {bb.guidance.summary}")
    if bb.pending_bug is not None:
        print(
            f"BUG: seed={bb.pending_bug.seed_id} checker={bb.pending_bug.failing_checker} "
            f"isa={bb.pending_bug.isa}"
        )
        if bb.minimized_poc is not None:
            print(
                f"  minimized: still_triggers={bb.minimized_poc.still_triggers} "
                f"({len(bb.minimized_poc.reduced_source)} chars)"
            )
    else:
        print("no violation found")


def _inspect(args: argparse.Namespace) -> None:
    """List the checkpoint chain for a run dir (FR-009)."""
    thread = thread_from_run_dir(args.run_dir)
    config = {"configurable": {"thread_id": thread}}
    with checkpointer(args.run_dir) as saver:
        states = list(saver.list(config))
    if not states:
        print(f"no checkpoints for thread '{thread}'")
        return
    for st in reversed(states):
        cid = st.config["configurable"].get("checkpoint_id", "?")
        step = st.metadata.get("step", "?") if st.metadata else "?"
        writes = st.metadata.get("writes") if st.metadata else None
        node = next(iter(writes), "?") if isinstance(writes, dict) and writes else "-"
        print(f"step={step} checkpoint_id={cid} node={node}")


def _replay(args: argparse.Namespace) -> None:
    """Pin a checkpoint and print its (deterministic) input state (SC-003).

    Replay locks a checkpoint_id and reports the recorded blackboard plus the
    tool_call_log; downstream re-execution reuses those records rather than
    re-running the LLM token-by-token (R7).
    """
    thread = thread_from_run_dir(args.run_dir)
    config = {
        "configurable": {"thread_id": thread, "checkpoint_id": args.checkpoint}
    }
    with checkpointer(args.run_dir) as saver:
        tup = saver.get_tuple(config)
    if tup is None:
        print(f"no checkpoint {args.checkpoint} on thread '{thread}'")
        return
    bb = Blackboard.model_validate(tup.checkpoint["channel_values"])
    print(f"thread={thread} checkpoint={args.checkpoint}")
    print(f"round={bb.round} corpus={len(bb.corpus)} verdicts={len(bb.verdict_history)}")
    print(f"tool_call_log ({len(bb.tool_call_log)} calls):")
    for tc in bb.tool_call_log:
        print(f"  r{tc.round} {tc.agent}:{tc.tool} digest={tc.result_digest}")


def _trace_bug(args: argparse.Namespace) -> None:
    """Walk the checkpoint chain back to the deterministic bug evidence (SC-005)."""
    thread = thread_from_run_dir(args.run_dir)
    config = {"configurable": {"thread_id": thread}}
    with checkpointer(args.run_dir) as saver:
        states = list(saver.list(config))
    for st in states:
        bb = Blackboard.model_validate(st.checkpoint["channel_values"])
        if bb.pending_bug is not None and bb.pending_bug.seed_id == args.bug:
            cid = st.config["configurable"].get("checkpoint_id", "?")
            bug = bb.pending_bug
            print(f"bug seed={bug.seed_id} found at checkpoint={cid}")
            print(f"  failing_checker={bug.failing_checker} isa={bug.isa}")
            print(f"  evidence={bug.evidence}")
            seed = next((s for s in bb.corpus if s.id == bug.seed_id), None)
            if seed is not None:
                print(f"  source ({len(seed.source)} chars):\n{seed.source}")
            return
    print(f"no pending_bug with seed_id '{args.bug}' on thread '{thread}'")


def main() -> None:
    parser = argparse.ArgumentParser(prog="defuzz-loop")
    sub = parser.add_subparsers(dest="command", required=True)

    run = sub.add_parser("run", help="run the agentic loop")
    run.add_argument(
        "--mechanism", default="canary", help="single defense mechanism (canary|ibt|fortify)"
    )
    run.add_argument("--experiment", default="exp", help="experiment label for the thread_id")
    run.add_argument("--max-rounds", type=int, default=1, help="max loop iterations")
    run.add_argument("--grpc", default="localhost:50051", help="Go core gRPC address")
    run.add_argument("--mcp", default="http://127.0.0.1:50052/mcp", help="Go core MCP url")
    run.add_argument(
        "--llm-config", default=None, help="path to llm.yaml (default: configs/llm.yaml)"
    )
    run.add_argument(
        "--run-dir",
        default=None,
        help="base dir for the per-run audit directory (default: orchestrator/runs)",
    )
    run.add_argument(
        "--disable-agent",
        action="append",
        default=[],
        choices=["feedback", "minimizer"],
        help="disable an optional agent (MVP disables both)",
    )
    run.add_argument(
        "--ablation",
        action="append",
        default=[],
        metavar="EDGE=on|off",
        help="toggle a linkage edge, e.g. --ablation checker_routing=off (SC-004)",
    )

    insp = sub.add_parser("inspect", help="list a run's checkpoint chain")
    insp.add_argument("--run-dir", required=True, help="the run's audit directory")

    rep = sub.add_parser("replay", help="lock a checkpoint and show its input state")
    rep.add_argument("--run-dir", required=True, help="the run's audit directory")
    rep.add_argument("--checkpoint", required=True, help="checkpoint_id to pin")

    tb = sub.add_parser("trace-bug", help="trace a bug back to deterministic evidence")
    tb.add_argument("--run-dir", required=True, help="the run's audit directory")
    tb.add_argument("--bug", required=True, help="failing seed_id")

    args = parser.parse_args()
    if args.command == "run":
        asyncio.run(_run(args))
    elif args.command == "inspect":
        _inspect(args)
    elif args.command == "replay":
        _replay(args)
    elif args.command == "trace-bug":
        _trace_bug(args)


if __name__ == "__main__":
    main()
