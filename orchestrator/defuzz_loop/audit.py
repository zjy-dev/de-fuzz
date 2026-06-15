"""Per-run audit directory: isolation + self-describing manifest (FR-009 / SC-003).

Each run gets its own directory under orchestrator/runs/ holding its own
checkpoints.sqlite (full blackboard history) and a manifest.json capturing the
environment a checkpoint alone cannot (git sha, toolchains snapshot, checker
catalog, LLM + ablation config). Two runs never share a directory or sqlite
file, so replay/inspect of one can never be contaminated by another.
"""

from __future__ import annotations

import json
import subprocess
from datetime import UTC, datetime
from pathlib import Path

from langgraph.checkpoint.sqlite import SqliteSaver
from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver

from .llm import LLMConfig
from .state import Blackboard

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


def thread_from_run_dir(run_dir: str | Path) -> str:
    """Resolve a run dir's thread_id from its manifest (the dir is self-describing)."""
    manifest = read_manifest(Path(run_dir))
    thread = manifest.get("thread_id")
    if not thread:
        raise SystemExit(f"no manifest.json (or thread_id) in run dir '{run_dir}'")
    return thread


def _db_path(run_dir: str | Path) -> str:
    return str(Path(run_dir) / _CHECKPOINT_DB_NAME)


def checkpointer(run_dir: str | Path):
    """Sync SQLite checkpointer over a run dir's own db (inspect/replay CLI paths)."""
    return SqliteSaver.from_conn_string(_db_path(run_dir))


def async_checkpointer(run_dir: str | Path):
    """Async SQLite checkpointer over a run dir's own db (the async loop `run`)."""
    return AsyncSqliteSaver.from_conn_string(_db_path(run_dir))
