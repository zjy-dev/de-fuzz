"""LangGraph skeleton: empty StateGraph + SQLite checkpointer + thread_id convention.

This is the deterministic-skeleton entry point (FR-006/009). Nodes and edges are
wired in later phases (US1+); here we only set up the graph shell, the checkpointer
that versions the blackboard, and the thread_id convention used for replay.
"""

from __future__ import annotations

from pathlib import Path

from langgraph.checkpoint.sqlite import SqliteSaver
from langgraph.graph import StateGraph

from .state import Blackboard

_DEFAULT_CHECKPOINT_DB = Path(__file__).resolve().parents[1] / "checkpoints.sqlite"


def thread_id(experiment: str, mechanism: str) -> str:
    """Stable thread_id convention: <experiment>:<mechanism>.

    A run is pinned by (thread_id, checkpoint_id); replay locks a checkpoint_id.
    """
    return f"{experiment}:{mechanism}"


def build_graph_skeleton() -> StateGraph:
    """Return the bare StateGraph over the Blackboard state.

    Nodes/edges are added in Phase 3+ (US1). Kept node-free so the skeleton can be
    imported and inspected before the loop is wired.
    """
    return StateGraph(Blackboard)


def checkpointer(db_path: str | Path | None = None) -> SqliteSaver:
    """SQLite checkpointer that versions the blackboard across rounds."""
    path = str(db_path) if db_path else str(_DEFAULT_CHECKPOINT_DB)
    return SqliteSaver.from_conn_string(path)


def main() -> None:
    """CLI placeholder; `run`/`inspect`/`replay` subcommands land in later phases."""
    raise SystemExit("defuzz-loop CLI not yet wired — see Phase 3 (US1) tasks.")
