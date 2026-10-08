"""Reproducible invariant -> source mapping -> directed fuzz experiment runner.

Offline mode is a deterministic CI smoke. Live mode invokes OpenCode for the first two
stages; the Go core MCP fuzz bridge remains explicit TODO and is recorded as a failure.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import random
import subprocess
import sys
import time
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Literal

from pydantic import BaseModel, Field

STAGES = (
    ("invariants", "01-invariants"),
    ("source-mapping", "02-source-mapping"),
    ("fuzz", "03-fuzz"),
)
STACK = [
    {
        "phi_id": "SP-001",
        "phi_text": (
            "Functions with vulnerable stack objects must receive a stack protector canary."
        ),
        "mechanism": "stack_canary",
        "compiler": "gcc",
        "isa": "x86_64",
    }
]
EXTRA = [
    {
        "phi_id": "FORT-001",
        "phi_text": (
            "Known-size overflowing libc writes must route through a checked FORTIFY wrapper."
        ),
        "mechanism": "fortify_source",
        "compiler": "gcc",
        "isa": "x86_64",
    }
]


def utc() -> str:
    return datetime.now(UTC).isoformat()


def dump(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def text(path: Path, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(value)


def run(command: list[str], cwd: Path) -> str:
    return subprocess.run(
        command, cwd=cwd, text=True, capture_output=True, check=True
    ).stdout.strip()


def redact(value: str) -> str:
    for name, secret in os.environ.items():
        if ("KEY" in name or "TOKEN" in name or "SECRET" in name) and secret:
            value = value.replace(secret, f"<redacted:{name}>")
    return value


class InvariantSet(BaseModel):
    invariants: list[dict[str, Any]] = Field(min_length=1)


class MappingSet(BaseModel):
    mappings: list[dict[str, Any]]


class FuzzSummary(BaseModel):
    mode: Literal["offline", "live"]
    rounds: int
    candidates: int
    findings: int
    note: str


def _git(repo: Path) -> dict[str, Any]:
    return {
        "commit": run(["git", "rev-parse", "HEAD"], repo),
        "dirty": bool(run(["git", "status", "--porcelain"], repo)),
    }


def _version(command: list[str], repo: Path) -> str | None:
    try:
        return run(command, repo)
    except (OSError, subprocess.CalledProcessError):
        return None


def _invoke_opencode(
    repo: Path, agent: str, prompt: str, model: str | None, attempts: Path
) -> dict[str, Any]:
    from defuzz_loop.opencode.client import extract_json_block

    command = ["opencode", "run", "--format", "json", "--agent", agent, "--dir", str(repo)]
    if model:
        command += ["--model", model]
    command.append(prompt)
    last = ""
    for index in range(3):
        try:
            process = subprocess.run(command, cwd=repo, text=True, capture_output=True, timeout=600)
            last = redact(process.stdout + "\n" + process.stderr)
            text(attempts / f"attempt-{index + 1}.log", last)
            if process.returncode:
                raise RuntimeError(f"OpenCode exited {process.returncode}: {last[-1000:]}")
            return json.loads(extract_json_block(last))
        except Exception as exc:
            text(
                attempts / f"repair-{index + 1}.txt",
                f"Validation/invocation error: {exc}\nRetry with exactly one fenced JSON block.\n",
            )
            if index == 2:
                raise
    raise AssertionError("unreachable")


def execute(args: argparse.Namespace, repo: Path) -> tuple[int, Path]:
    run_id = args.run_id or datetime.now(UTC).strftime("full-%Y%m%dT%H%M%SZ")
    root = (args.output_root / run_id).resolve()
    root.mkdir(parents=True, exist_ok=True)
    (root / "logs").mkdir(exist_ok=True)
    for _, directory in STAGES:
        (root / directory).mkdir(exist_ok=True)
    argv = vars(args).copy()
    argv["output_root"] = str(argv["output_root"])
    argv["force_stage"] = sorted(argv["force_stage"])
    config_hash = hashlib.sha256(json.dumps(argv, sort_keys=True).encode()).hexdigest()
    manifest_path = root / "manifest.json"
    if manifest_path.exists():
        manifest = json.loads(manifest_path.read_text())
        if not args.resume:
            raise RuntimeError(f"run exists: {root}; pass --resume")
    else:
        manifest = {
            "run_id": run_id,
            "started_at": utc(),
            "finished_at": None,
            "status": "running",
            "failure_reason": None,
            "git": _git(repo),
            "host": platform.node(),
            "platform": platform.platform(),
            "python": sys.version,
            "opencode": _version(["opencode", "--version"], repo),
            "model": args.model,
            "mode": args.mode,
            "cli_args": argv,
            "config_hash": config_hash,
            "seed": args.seed,
            "stages": {},
        }
    dump(manifest_path, manifest)
    random.seed(args.seed)
    try:
        for stage, directory in STAGES:
            previous = manifest["stages"].get(stage, {})
            if previous.get("status") == "success" and stage not in args.force_stage:
                continue
            started = time.monotonic()
            info = {
                "status": "running",
                "started_at": utc(),
                "finished_at": None,
                "duration_seconds": None,
                "failure_reason": None,
            }
            manifest["stages"][stage] = info
            dump(manifest_path, manifest)
            out = root / directory
            try:
                if stage == "invariants":
                    prompt = "Generate strict JSON {invariants:[...]} for stack protector" + (
                        " and all supported defenses" if args.all_invariants else ""
                    )
                    text(out / "prompt.txt", prompt)
                    raw = (
                        {"invariants": STACK + (EXTRA if args.all_invariants else [])}
                        if args.mode == "offline"
                        else _invoke_opencode(
                            repo, "source-mapper", prompt, args.model, out / "repair-attempts"
                        )
                    )
                    validated = InvariantSet.model_validate(raw)
                    dump(out / "response.json", raw)
                    dump(out / "validated.json", validated.model_dump())
                elif stage == "source-mapping":
                    invs = InvariantSet.model_validate_json(
                        (root / "01-invariants" / "validated.json").read_text()
                    ).invariants
                    prompt = "Map these invariants using defuzz-callgraph tools: " + json.dumps(
                        invs
                    )
                    text(out / "prompt.txt", prompt)
                    if args.mode == "offline":
                        raw = {
                            "mappings": [
                                {
                                    "phi_id": i["phi_id"],
                                    "target": "gcc-x86_64",
                                    "entry": "pass_stack_protect",
                                    "confidence": "smoke",
                                }
                                for i in invs
                            ]
                        }
                    else:
                        raw = {
                            "mappings": [
                                _invoke_opencode(
                                    repo,
                                    "source-mapper",
                                    json.dumps({**i, "src_root": args.src_root}),
                                    args.model,
                                    out / "repair-attempts" / i["phi_id"],
                                )
                                for i in invs
                            ]
                        }
                    validated = MappingSet.model_validate(raw)
                    dump(out / "response.json", raw)
                    dump(out / "validated.json", validated.model_dump())
                    dump(
                        out / "mcp-query-summaries.json",
                        {
                            "server": "defuzz-callgraph",
                            "mode": args.mode,
                            "note": (
                                "OpenCode owns agent-internal query selection; "
                                "offline has no remote calls."
                            ),
                        },
                    )
                    text(
                        root / "logs" / "callgraph-tools.log",
                        "offline: no MCP queries\n"
                        if args.mode == "offline"
                        else "see OpenCode attempt logs\n",
                    )
                else:
                    if args.mode == "live":
                        raise RuntimeError(
                            "TODO(core-mcp): Go core MCP compile/execute/coverage integration "
                            "is not implemented; refusing to fabricate live fuzz results"
                        )
                    summary = FuzzSummary(
                        mode="offline",
                        rounds=args.rounds,
                        candidates=args.rounds,
                        findings=0,
                        note="Deterministic fake core smoke; no live claim.",
                    )
                    dump(out / "validated.json", summary.model_dump())
                    dump(out / "final-summary.json", summary.model_dump())
                    for round_no in range(1, args.rounds + 1):
                        dump(
                            out / "blackboard" / f"round-{round_no:03}.json",
                            {
                                "round": round_no,
                                "seed": args.seed,
                                "candidate": f"offline-{round_no}",
                                "coverage_delta": round_no,
                            },
                        )
                info.update(
                    status="success",
                    finished_at=utc(),
                    duration_seconds=round(time.monotonic() - started, 6),
                )
            except Exception as exc:
                info.update(
                    status="failed",
                    finished_at=utc(),
                    duration_seconds=round(time.monotonic() - started, 6),
                    failure_reason=f"{type(exc).__name__}: {exc}",
                )
                text(root / "logs" / f"{stage}.error.log", redact(info["failure_reason"] + "\n"))
                raise
            finally:
                dump(manifest_path, manifest)
        manifest.update(status="success", finished_at=utc())
        dump(manifest_path, manifest)
        dump(
            root / "summary.json",
            {"run_id": run_id, "status": "success", "stages": manifest["stages"]},
        )
        return 0, root
    except Exception as exc:
        manifest.update(
            status="failed", finished_at=utc(), failure_reason=f"{type(exc).__name__}: {exc}"
        )
        dump(manifest_path, manifest)
        dump(
            root / "summary.json",
            {"run_id": run_id, "status": "failed", "failure_reason": manifest["failure_reason"]},
        )
        return 1, root


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Run the full reproducible DeFuzz experiment")
    p.add_argument("--mode", choices=("offline", "live"), default="offline")
    p.add_argument("--run-id")
    p.add_argument("--output-root", type=Path, default=Path("runs"))
    p.add_argument("--resume", action="store_true")
    p.add_argument("--force-stage", action="append", choices=[s[0] for s in STAGES], default=[])
    p.add_argument("--all-invariants", action="store_true")
    p.add_argument("--rounds", type=int, default=2)
    p.add_argument("--seed", type=int, default=20260928)
    p.add_argument("--model")
    p.add_argument("--src-root", default="/opt/gcc-src")
    return p


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    repo = Path(__file__).resolve().parents[2]
    code, root = execute(args, repo)
    print(root)
    return code


if __name__ == "__main__":
    raise SystemExit(main())
