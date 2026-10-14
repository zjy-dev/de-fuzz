"""Persist online-round evidence before reclaiming shared temporary builds."""

from __future__ import annotations

import base64
import hashlib
import json
import os
import shutil
from pathlib import Path

from .state import Blackboard


def _json_default(value):
    if isinstance(value, bytes):
        return {"encoding": "base64", "data": base64.b64encode(value).decode("ascii")}
    if isinstance(value, (set, frozenset)):
        return sorted(value)
    raise TypeError(f"cannot serialize {type(value).__name__}")


def _write_json(path: Path, data: dict) -> None:
    temporary = path.with_suffix(".tmp")
    with temporary.open("w") as stream:
        json.dump(data, stream, indent=2, default=_json_default)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    temporary.replace(path)


class RoundArtifacts:
    """A completed round owns only the build directories named by its artifacts.

    Cleanup is opt-in via an explicit shared work root. Nothing is removed on
    archive failure or outside that root. Other processes' temporary files and
    compiler outputs on ordinary single-host runs are left alone.
    """

    def __init__(self, run_dir: Path, work_root: str | Path | None = None) -> None:
        self.run_dir = run_dir
        self.work_root = Path(work_root).resolve(strict=True) if work_root else None
        self.completed_rounds = 0

    def _owned_directory(self, binary: Path) -> Path | None:
        if self.work_root is None or binary.is_symlink() or binary.parent.is_symlink():
            return None
        parent = binary.parent.resolve(strict=True)
        if parent.parent == self.work_root and parent.name.startswith("defuzz-build-"):
            return parent
        return None

    def complete(self, bb: Blackboard) -> None:
        destination = self.run_dir / "rounds" / f"{bb.round:06d}"
        destination.mkdir(parents=True, exist_ok=False)
        state = bb.model_dump()
        archived: dict[str, dict[str, str]] = {}
        cleanup: set[Path] = set()
        if bb.current_seed is not None:
            (destination / "seed.c").write_text(bb.current_seed.source)
        if bb.minimized_poc is not None:
            (destination / "minimized.c").write_text(bb.minimized_poc.reduced_source)
        for artifact in bb.build_artifacts:
            if not artifact.success or not artifact.binary_path or artifact.binary_path in archived:
                continue
            source = Path(artifact.binary_path)
            # Separate-host clients may not have this filesystem mounted.
            # In the Pod, a missing successful artifact is an infrastructure error.
            if not source.is_file() and self.work_root is None:
                continue
            name = f"binary-{len(archived):02d}"
            target = destination / name
            shutil.copy2(source, target)
            with target.open("rb") as stream:
                os.fsync(stream.fileno())
            archived[artifact.binary_path] = {
                "path": str(target.relative_to(self.run_dir)),
                "sha256": hashlib.sha256(target.read_bytes()).hexdigest(),
            }
            owned = self._owned_directory(source)
            if owned is not None:
                cleanup.add(owned)
        state["archived_binaries"] = archived
        _write_json(destination / "state.json", state)
        with (self.run_dir / "events.jsonl").open("a") as stream:
            stream.write(json.dumps({
                "event": "round_completed", "round": bb.round,
                "state": str((destination / "state.json").relative_to(self.run_dir)),
            }) + "\n")
            stream.flush()
            os.fsync(stream.fileno())
        # All downstream users (oracle, minimizer and feedback) have returned.
        for directory in sorted(cleanup):
            shutil.rmtree(directory)
        self.completed_rounds += 1

    def finish(self, bb: Blackboard, *, error: str | None = None) -> None:
        _write_json(self.run_dir / "summary.json", {
            "status": "error" if error else "completed",
            "completed_rounds": self.completed_rounds,
            "error": error,
            "pending_bug": bb.pending_bug.model_dump() if bb.pending_bug else None,
            "minimized_poc": bb.minimized_poc.model_dump() if bb.minimized_poc else None,
        })
        if error:
            # Preserve partial state and leave its temporary binaries for diagnosis.
            _write_json(self.run_dir / "interrupted-state.json", bb.model_dump())
