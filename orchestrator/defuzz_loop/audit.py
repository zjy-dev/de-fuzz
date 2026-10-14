"""Per-run audit directory: isolation + self-describing manifest (FR-009 / SC-003).

Rev 3: the LangGraph SQLite checkpointer path is gone. The run directory
and manifest.json capture environment metadata. RoundArtifacts persists
per-round evidence; the retired checkpointer factories still fail loudly.
"""

from __future__ import annotations

import json
import subprocess
from datetime import UTC, datetime
from pathlib import Path

from .llm import LLMConfig
from .state import Blackboard

_REPO_ROOT = Path(__file__).resolve().parents[2]
_DEFAULT_RUNS_DIR = Path(__file__).resolve().parents[1] / "runs"
_CHECKPOINT_DB_NAME = "checkpoints.sqlite"
_MANIFEST_NAME = "manifest.json"


def thread_id(experiment: str, mechanism: str) -> str:
    """Stable thread_id convention: ``<experiment>:<mechanism>``.

    Kept as a stable identifier used in run manifests; no longer paired
    with a LangGraph thread.
    """

    return f"{experiment}:{mechanism}"


def _run_slug(experiment: str, mechanism: str) -> str:
    stamp = datetime.now(UTC).strftime("%Y%m%d-%H%M%S-%f")
    return f"{experiment}_{mechanism}_{stamp}"


def make_run_dir(base: str | Path | None, experiment: str, mechanism: str) -> Path:
    """Create a fresh, self-contained audit directory for one run."""

    root = Path(base) if base else _DEFAULT_RUNS_DIR
    run_dir = root / _run_slug(experiment, mechanism)
    run_dir.mkdir(parents=True, exist_ok=False)
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
        return _container_provenance().get("git_commit", "")


def _container_provenance() -> dict:
    path = Path("/usr/local/share/defuzz/build-provenance.json")
    return json.loads(path.read_text()) if path.exists() else {}


def _toolchains_snapshot() -> str:
    path = _REPO_ROOT / "configs" / "toolchains.yaml"
    return path.read_text() if path.exists() else ""


def build_manifest(
    *,
    experiment: str,
    mechanism: str,
    budget_rounds: int,
    budget_secs: float,
    grpc_addr: str,
    mcp_addr: str,
    llm_config: LLMConfig,
    blackboard: Blackboard,
    disabled_agents: list[str],
    checker_ids: list[str],
    runtime_metadata: dict | None = None,
) -> dict:
    """Assemble the environment manifest that makes a run dir self-auditable."""

    return {
        "created_at": datetime.now(UTC).isoformat(),
        "experiment": experiment,
        "mechanism": mechanism,
        "thread_id": thread_id(experiment, mechanism),
        "budget_rounds": budget_rounds,
        "budget_secs": budget_secs,
        "grpc_addr": grpc_addr,
        "mcp_addr": mcp_addr,
        "git_sha": _git_sha(),
        "container_build": _container_provenance(),
        "llm": runtime_metadata or {
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
    """Resolve a run dir's thread_id from its manifest."""

    manifest = read_manifest(Path(run_dir))
    thread = manifest.get("thread_id")
    if not thread:
        raise SystemExit(f"no manifest.json (or thread_id) in run dir '{run_dir}'")
    return thread


def _db_path(run_dir: str | Path) -> str:
    return str(Path(run_dir) / _CHECKPOINT_DB_NAME)


def checkpointer(run_dir: str | Path):
    """Rev 3 stub — LangGraph SqliteSaver was removed.

    A future replacement will emit per-round manifests as the replay
    substrate; until then, this factory raises immediately so callers
    fail loudly rather than silently continuing with a broken chain.
    """

    raise NotImplementedError(
        "LangGraph SqliteSaver was removed in Rev 3; see "
        "docs/tech-docs/architecture/pi-sdk-migration.md."
    )


def async_checkpointer(run_dir: str | Path):
    """Rev 3 stub — LangGraph AsyncSqliteSaver was removed. See above."""

    raise NotImplementedError(
        "LangGraph AsyncSqliteSaver was removed in Rev 3; see "
        "docs/tech-docs/architecture/pi-sdk-migration.md."
    )
