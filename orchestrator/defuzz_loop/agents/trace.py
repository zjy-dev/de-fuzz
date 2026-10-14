"""Inspect persisted attempt evidence without contacting models or running tools."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
from typing import Any


def inspect_attempt(root: Path) -> dict[str, Any]:
    root = root.expanduser().resolve(strict=True)
    request = json.loads((root / "request.json").read_text())
    identity = {key: request[key] for key in ("request_id", "run_id", "stage_id", "attempt_id")}
    errors: list[str] = []
    timeline: list[dict[str, Any]] = []
    terminal = None
    sequence = 0
    protocol = root / "protocol.jsonl"
    for line in protocol.read_text().splitlines() if protocol.exists() else []:
        try:
            frame = json.loads(line)
            if frame.get("seq") != sequence + 1 or any(
                frame.get(k) != v for k, v in identity.items()
            ):
                raise ValueError("frame identity or sequence mismatch")
            sequence = frame["seq"]
            data = frame.get("data", {})
            if "artifact_ref" in frame:
                ref = frame["artifact_ref"]
                relative = Path(ref["path"])
                target = (root / relative).resolve(strict=True)
                if relative.is_absolute() or not target.is_relative_to(root):
                    raise ValueError("artifact reference escapes attempt")
                body = target.read_bytes()
                if len(body) != ref["bytes"] or hashlib.sha256(body).hexdigest() != ref["sha256"]:
                    raise ValueError("artifact integrity mismatch")
                data = json.loads(body)
            timeline.append(
                {
                    "seq": sequence,
                    "timestamp": frame["timestamp"],
                    "type": frame["type"],
                    "event": data.get("type"),
                    "model_call_id": data.get("model_call_id"),
                    "tool_call_id": data.get("tool_call_id", data.get("toolCallId")),
                    "error_category": data.get("error_category"),
                }
            )
            if frame["type"] in {"result", "error"}:
                if terminal is not None:
                    raise ValueError("multiple terminal frames")
                terminal = data
        except (ValueError, KeyError, OSError) as exc:
            errors.append(str(exc))
            break
    if terminal is None:
        errors.append("terminal frame missing")
    manifest_path = root / "artifacts/manifest.json"
    if manifest_path.is_file():
        manifest = json.loads(manifest_path.read_text())
        for ref in manifest["files"]:
            target = (root / ref["path"]).resolve(strict=True)
            if not target.is_relative_to(root):
                errors.append("manifest path escapes attempt")
                continue
            body = target.read_bytes()
            if len(body) != ref["bytes"] or hashlib.sha256(body).hexdigest() != ref["sha256"]:
                errors.append(f"artifact integrity mismatch: {ref['path']}")
    else:
        errors.append("artifact manifest missing")
    admission_path = root / "admission.json"
    admission = json.loads(admission_path.read_text()) if admission_path.exists() else None
    usage_path = root / "usage.jsonl"
    usage = (
        [json.loads(line) for line in usage_path.read_text().splitlines()]
        if usage_path.exists()
        else []
    )
    return {
        "mode": "offline-evidence",
        **identity,
        "agent_id": request["agent"]["agent_id"],
        "parent_attempt_id": request.get("metadata", {}).get("parent_attempt_id"),
        "integrity_errors": errors,
        "timeline": timeline,
        "usage": usage,
        "admission": admission,
        "terminal": terminal,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("attempt", type=Path)
    args = parser.parse_args(argv)
    result = inspect_attempt(args.attempt)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 1 if result["integrity_errors"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
