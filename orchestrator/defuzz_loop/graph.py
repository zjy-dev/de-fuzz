"""LangGraph wiring: the deterministic agentic loop (FR-005/006/009).

Fixed edge order (never reordered, so a run is reproducible):

    START → generator → routing → build → coverage → oracle → ⟨route⟩
                ▲                                                 │
                └────────────── bump (round+1) ←── not_violated ──┘
                                                violated → END

Only the Generator step is an LLM agent; routing/build/coverage/oracle are
deterministic gRPC nodes. The conditional router reads the oracle's aggregate
verdict: violated → END, not_violated → loop back after incrementing the round
(bounded by --max-rounds).
"""

from __future__ import annotations

import argparse
import asyncio
import json
import subprocess
from datetime import UTC, datetime
from pathlib import Path

from langgraph.checkpoint.sqlite import SqliteSaver
from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
from langgraph.graph import END, START, StateGraph

from .agents.feedback import FeedbackAgent
from .agents.generator import GeneratorAgent
from .agents.minimizer import MinimizerAgent
from .clients.grpc_client import CoreClient
from .clients.mcp_client import MCPClient
from .llm.provider import LLMConfig
from .nodes.build import make_build_node
from .nodes.coverage import make_coverage_node
from .nodes.oracle import make_oracle_node
from .permissions import guard
from .routing import CheckerCatalog, make_routing_node
from .state import Aggregate, Blackboard

_REPO_ROOT = Path(__file__).resolve().parents[2]
_DEFAULT_RUNS_DIR = Path(__file__).resolve().parents[1] / "runs"
_CHECKPOINT_DB_NAME = "checkpoints.sqlite"
_MANIFEST_NAME = "manifest.json"


def thread_id(experiment: str, mechanism: str) -> str:
    """Stable thread_id convention: <experiment>:<mechanism>.

    A run is pinned by (thread_id, checkpoint_id); replay locks a checkpoint_id.
    Cross-run isolation comes from the per-run audit directory (its own sqlite),
    so two runs of the same mechanism never share a checkpoint chain.
    """
    return f"{experiment}:{mechanism}"


def _run_slug(experiment: str, mechanism: str) -> str:
    stamp = datetime.now(UTC).strftime("%Y%m%d-%H%M%S")
    return f"{experiment}_{mechanism}_{stamp}"


def make_run_dir(base: str | Path | None, experiment: str, mechanism: str) -> Path:
    """Create a fresh, self-contained audit directory for one run.

    Layout: <base>/<experiment>_<mechanism>_<UTC-timestamp>/ holding the run's own
    checkpoints.sqlite (full blackboard history) and manifest.json (environment).
    Each run is isolated: a distinct directory and sqlite file, so replay/inspect
    of one run can never be contaminated by another.
    """
    root = Path(base) if base else _DEFAULT_RUNS_DIR
    run_dir = root / _run_slug(experiment, mechanism)
    run_dir.mkdir(parents=True, exist_ok=True)
    return run_dir


def _git_sha() -> str:
    try:
        out = subprocess.run(
            ["git", "-C", str(_REPO_ROOT), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )
        return out.stdout.strip()
    except (subprocess.SubprocessError, OSError):
        return ""


def _toolchains_snapshot() -> str:
    path = _REPO_ROOT / "configs" / "toolchains.yaml"
    return path.read_text() if path.exists() else ""


def build_manifest(
    *,
    experiment: str,
    mechanism: str,
    max_rounds: int,
    grpc_addr: str,
    mcp_addr: str,
    llm_config: LLMConfig,
    blackboard: Blackboard,
    disabled_agents: list[str],
    checker_ids: list[str],
) -> dict:
    """Assemble the environment manifest that makes a run dir self-auditable.

    Captures everything a checkpoint alone cannot: the code/toolchain version under
    test (git sha + toolchains.yaml), the SSOT checker catalog actually pulled, the
    LLM provider/model, and the ablation configuration. Combined with the corpus
    seeds stored in the checkpoint, this lets the run be re-derived deterministically.
    """
    return {
        "created_at": datetime.now(UTC).isoformat(),
        "experiment": experiment,
        "mechanism": mechanism,
        "thread_id": thread_id(experiment, mechanism),
        "max_rounds": max_rounds,
        "grpc_addr": grpc_addr,
        "mcp_addr": mcp_addr,
        "git_sha": _git_sha(),
        "llm": {
            "provider": llm_config.provider,
            "model": llm_config.model,
            "base_url": llm_config.base_url,
        },
        "ablation_flags": blackboard.ablation_flags.model_dump(),
        "disabled_agents": sorted(disabled_agents),
        "checker_catalog": sorted(checker_ids),
        "toolchains_yaml": _toolchains_snapshot(),
    }


def write_manifest(run_dir: Path, manifest: dict) -> None:
    (run_dir / _MANIFEST_NAME).write_text(json.dumps(manifest, indent=2, default=str))


def read_manifest(run_dir: Path) -> dict:
    path = run_dir / _MANIFEST_NAME
    return json.loads(path.read_text()) if path.exists() else {}


def _db_path(run_dir: str | Path) -> str:
    return str(Path(run_dir) / _CHECKPOINT_DB_NAME)


def make_generator_node(agent: GeneratorAgent):
    """Wrap the async Generator agent as a graph node.

    Sole writer of corpus / current_seed (write-permission matrix, FR-007).
    """

    async def generator_node(bb: Blackboard) -> dict:
        seed = await agent.generate(bb)
        return {"current_seed": seed, "corpus": [*bb.corpus, seed]}

    return generator_node


def make_feedback_node(agent):
    """Wrap the feedback agent: sole writer of guidance (FR-008)."""

    async def feedback_node(bb: Blackboard) -> dict:
        guidance = await agent.summarize(bb)
        return {"guidance": guidance}

    return feedback_node


def make_minimizer_node(agent):
    """Wrap the minimizer agent: sole writer of minimized_poc (FR-025/026)."""

    async def minimizer_node(bb: Blackboard) -> dict:
        poc = await agent.minimize(bb)
        return {"minimized_poc": poc}

    return minimizer_node


def bump_node(bb: Blackboard) -> dict:
    """Loop-back step: advance the round counter for the next iteration."""
    return {"round": bb.round + 1}


def make_router(max_rounds: int):
    """Conditional edge after oracle (FR-005).

    violated → bug branch; otherwise loop back unless the round budget is
    exhausted. Error/NotApplicable aggregate to NOT_VIOLATED, so they take the
    non-bug branch — zero false positives (R8).
    """

    def route_after_oracle(bb: Blackboard) -> str:
        if bb.last_verdict is not None and bb.last_verdict.aggregate is Aggregate.VIOLATED:
            return "violated"
        if bb.round + 1 >= max_rounds:
            return "stop"
        return "loop"

    return route_after_oracle


def build_graph(
    *,
    generator: GeneratorAgent,
    catalog: CheckerCatalog,
    client: CoreClient,
    max_rounds: int,
    feedback=None,
    minimizer=None,
) -> StateGraph:
    """Assemble the fixed-order pipeline over the Blackboard state.

    Optional agents (feedback on the not_violated loop, minimizer on the violated
    terminus) are wired only when supplied (the `--disable-agent` flags drop them).
    Every node is guarded by the write-permission matrix (FR-008/022).
    """
    g = StateGraph(Blackboard)

    g.add_node("generator", guard("generator", make_generator_node(generator)))
    g.add_node("routing", guard("routing", make_routing_node(catalog)))
    g.add_node("build", guard("build", make_build_node(client)))
    g.add_node("coverage", guard("coverage", make_coverage_node(client)))
    g.add_node("oracle", guard("oracle", make_oracle_node(client)))
    g.add_node("bump", guard("bump", bump_node))

    g.add_edge(START, "generator")
    g.add_edge("generator", "routing")
    g.add_edge("routing", "build")
    g.add_edge("build", "coverage")
    g.add_edge("coverage", "oracle")

    # not_violated loop: optional feedback (writes guidance) → bump → generator.
    loop_target = "bump"
    if feedback is not None:
        g.add_node("feedback", guard("feedback", make_feedback_node(feedback)))
        g.add_edge("feedback", "bump")
        loop_target = "feedback"

    # violated terminus: optional minimizer (writes MinimizedPoC) → END.
    violated_target = END
    if minimizer is not None:
        g.add_node("minimizer", guard("minimizer", make_minimizer_node(minimizer)))
        g.add_edge("minimizer", END)
        violated_target = "minimizer"

    g.add_conditional_edges(
        "oracle",
        make_router(max_rounds),
        {"loop": loop_target, "stop": END, "violated": violated_target},
    )
    g.add_edge("bump", "generator")

    return g


def build_graph_skeleton() -> StateGraph:
    """Bare node-free StateGraph (kept for skeleton inspection / tests)."""
    return StateGraph(Blackboard)


def checkpointer(run_dir: str | Path):
    """Sync SQLite checkpointer over a run dir's own db (inspect/replay CLI paths)."""
    return SqliteSaver.from_conn_string(_db_path(run_dir))


def async_checkpointer(run_dir: str | Path):
    """Async SQLite checkpointer over a run dir's own db (the async loop `run`)."""
    return AsyncSqliteSaver.from_conn_string(_db_path(run_dir))


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


def _thread_from_run_dir(run_dir: str | Path) -> str:
    """Resolve a run dir's thread_id from its manifest (the dir is self-describing)."""
    manifest = read_manifest(Path(run_dir))
    thread = manifest.get("thread_id")
    if not thread:
        raise SystemExit(f"no manifest.json (or thread_id) in run dir '{run_dir}'")
    return thread


def _inspect(args: argparse.Namespace) -> None:
    """List the checkpoint chain for a run dir (FR-009)."""
    thread = _thread_from_run_dir(args.run_dir)
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
    thread = _thread_from_run_dir(args.run_dir)
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
    thread = _thread_from_run_dir(args.run_dir)
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
