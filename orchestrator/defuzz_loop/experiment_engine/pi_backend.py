"""One Pi SDK worker per attempt, with streaming evidence and explicit isolation."""

from __future__ import annotations

import asyncio
import hashlib
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import uuid
from collections import deque
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from ..token_usage import TokenUsageRecord, current_token_usage_sink
from .agent_backend import AgentRequest, AgentResult, ExecAgentBackend, _atomic_write
from .pi_config import agent_id_for, load_pi_config, resolve_agent, validate_credentials

PROTOCOL_VERSION = 1
MAX_FRAME_BYTES = 16 * 1024 * 1024
MAX_OUTPUT_BYTES = 2048 * 1024 * 1024
DIAGNOSTIC_TAIL_BYTES = 64 * 1024


class ProtocolError(RuntimeError):
    pass


def extract_json(text: str) -> Any:
    """Accept one JSON value, optionally wrapped in a single Markdown code fence or with commentary."""
    text = text.strip()
    # 1. Standard markdown code fence at start
    if text.startswith("```"):
        first, _, body = text.partition("\n")
        if first in {"```", "```json"} and body.rstrip().endswith("```"):
            candidate = body.rstrip()[:-3].strip()
            try:
                return json.loads(candidate)
            except Exception:
                pass
    # 2. Direct JSON
    if (text.startswith("{") and text.endswith("}")) or (text.startswith("[") and text.endswith("]")):
        try:
            return json.loads(text)
        except Exception:
            pass
    # 3. Markdown code block embedded in text (e.g. after introductory reasoning)
    if "```json" in text:
        start = text.index("```json") + len("```json")
        end = text.rindex("```")
        if end > start:
            inner = text[start:end].strip()
            try:
                return json.loads(inner)
            except Exception:
                pass
    if "```" in text:
        start = text.index("```") + 3
        _, _, rest = text[start:].partition("\n")
        if "```" in rest:
            end = rest.rindex("```")
            inner = rest[:end].strip()
            try:
                return json.loads(inner)
            except Exception:
                pass
    # 4. Outermost JSON object / array
    first_brace = text.find("{")
    last_brace = text.rfind("}")
    if first_brace != -1 and last_brace > first_brace:
        candidate = text[first_brace : last_brace + 1].strip()
        try:
            return json.loads(candidate)
        except Exception:
            pass
    return json.loads(text)


def verified_artifact(root: Path, reference: dict[str, Any]) -> bytes:
    relative = Path(reference["path"])
    if relative.is_absolute() or ".." in relative.parts:
        raise ProtocolError("artifact reference escapes attempt directory")
    path = (root / relative).resolve(strict=True)
    if not path.is_relative_to(root.resolve()):
        raise ProtocolError("artifact symlink escapes attempt directory")
    if path.stat().st_size > MAX_OUTPUT_BYTES:
        raise ProtocolError("artifact exceeds output limit")
    data = path.read_bytes()
    if len(data) != reference["bytes"] or hashlib.sha256(data).hexdigest() != reference["sha256"]:
        raise ProtocolError(f"artifact integrity mismatch: {relative}")
    return data


class PiAgentBackend:
    provider = "pi"
    backend_identity = "pi-sdk"

    def __init__(
        self, config_path: Path, *, binary: str = "node", model: str | None = None
    ) -> None:
        self.config_path = config_path.expanduser().resolve(strict=True)
        self.config, self.config_sha256 = load_pi_config(self.config_path)
        self.binary = binary
        self.model = self.config["model"]
        if model is not None and model != self.model:
            raise ValueError("--model must match the pinned Pi configuration")

    @property
    def supports_host_read_isolation(self) -> bool:
        return (sys.platform == "darwin" and Path("/usr/bin/sandbox-exec").is_file()) or (
            sys.platform == "linux" and shutil.which("bwrap") is not None
        )

    def validate_credentials(self) -> None:
        validate_credentials(self.config)

    def preflight(self, *, require_isolation: bool = True) -> None:
        """Exercise the actual OS sandbox before opening a model session."""
        self.validate_credentials()
        node = subprocess.run(
            [self.binary, "--version"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        if node.returncode:
            raise ValueError(f"Node preflight failed: {node.stderr.strip()}")
        try:
            version = tuple(map(int, node.stdout.strip().lstrip("v").split(".")))
        except ValueError as exc:
            raise ValueError("Node version output is invalid") from exc
        if version < (22, 19, 0):
            raise ValueError("Pi requires Node >=22.19.0")
        if not require_isolation:
            return
        if not self.supports_host_read_isolation:
            raise ValueError("Pi formal stages require sandbox-exec or bubblewrap")
        if sys.platform == "linux":
            argv = [
                "bwrap",
                "--die-with-parent",
                "--unshare-user",
                "--unshare-pid",
                "--ro-bind",
                "/",
                "/",
                "--dev-bind",
                "/dev",
                "/dev",
                "--tmpfs",
                "/proc",
                "true",
            ]
        else:
            argv = ["/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)", "/usr/bin/true"]
        probe = subprocess.run(argv, capture_output=True, text=True, timeout=10)
        if probe.returncode:
            raise ValueError(f"Pi OS isolation preflight failed: {probe.stderr.strip()}")

    @staticmethod
    def agent_name(request: AgentRequest) -> str:
        return agent_id_for(request)

    @staticmethod
    def uses_callgraph(request: AgentRequest) -> bool:
        return request.metadata.get("use_callgraph") is True

    def runtime_config(self, request: AgentRequest) -> dict[str, Any]:
        return resolve_agent(self.config, request)

    def _denied_paths(self, request: AgentRequest) -> list[Path]:
        automatic = [
            Path.home() / p
            for p in (
                ".pi",
                ".agents/skills",
                ".codex",
                ".trae",
                ".trae-cn",
            )
        ]
        paths = list(
            dict.fromkeys(
                p.expanduser().resolve()
                for p in [*request.deny_read_paths, *(p for p in automatic if p.exists())]
            )
        )
        protected = [
            request.cwd.resolve(),
            request.output_dir.resolve(),
            Path(self.config["worker_path"]).resolve(),
        ]
        for denied in paths:
            if any(p.is_relative_to(denied) for p in protected):
                raise ValueError(f"deny path contains a required runtime path: {denied}")
        return paths

    def argv_for(self, request: AgentRequest, final_path: Path | None = None) -> list[str]:
        return [self.binary, self.config["worker_path"]]

    def launch_argv_for(self, request: AgentRequest, final_path: Path | None = None) -> list[str]:
        argv = self.argv_for(request, final_path)
        denied = self._denied_paths(request)
        root = request.output_dir.resolve()
        writable = [root]
        if request.writable:
            writable.append(request.cwd.resolve())
        if sys.platform == "darwin" and self.supports_host_read_isolation:
            exceptions = " ".join(f"(subpath {json.dumps(str(p))})" for p in writable)
            profile = [
                "(version 1)",
                "(allow default)",
                f"(deny file-write* (require-not (require-any {exceptions})))",
                '(allow file-write* (literal "/dev/null"))',
            ]
            profile += [f"(deny file-read* (subpath {json.dumps(str(p))}))" for p in denied]
            return ["/usr/bin/sandbox-exec", "-p", "\n".join(profile), *argv]
        if sys.platform == "linux" and shutil.which("bwrap"):
            args = [
                "bwrap",
                "--die-with-parent",
                "--unshare-user",
                "--unshare-pid",
                "--ro-bind",
                "/",
                "/",
                "--dev-bind",
                "/dev",
                "/dev",
                "--tmpfs",
                "/proc",
            ]
            for p in writable:
                args += ["--bind", str(p), str(p)]
            for p in denied:
                if not p.exists():
                    continue
                args += ["--tmpfs", str(p)] if p.is_dir() else ["--ro-bind", "/dev/null", str(p)]
            return [*args, *argv]
        if request.require_host_read_isolation:
            raise RuntimeError(
                "Pi requires sandbox-exec on macOS or bubblewrap on Linux for formal isolation"
            )
        return argv

    def _environment(self, output: Path) -> dict[str, str]:
        env = ExecAgentBackend._minimal_environment()
        runtime = output / ".runtime"
        for subdir in ("home", "tmp", "cache", "config", "data", "state"):
            (runtime / subdir).mkdir(parents=True, exist_ok=True)
        env.update(
            {
                "HOME": str(runtime / "home"),
                "TMPDIR": str(runtime / "tmp"),
                "TMP": str(runtime / "tmp"),
                "TEMP": str(runtime / "tmp"),
                "XDG_CACHE_HOME": str(runtime / "cache"),
                "UV_CACHE_DIR": str(runtime / "cache/uv"),
                "XDG_CONFIG_HOME": str(runtime / "config"),
                "XDG_DATA_HOME": str(runtime / "data"),
                "XDG_STATE_HOME": str(runtime / "state"),
                "PI_CODING_AGENT_DIR": str(runtime),
                "PI_OFFLINE": "1",
            }
        )
        # Some editor launchers omit Homebrew from PATH even though the SDK's
        # find tool needs fd. Resolve the installed tools in the parent; no SDK
        # auto-download or mutable global tool cache is permitted.
        for name in ("fd", "fdfind", "rg"):
            candidates = [
                shutil.which(name),
                str(Path("/opt/homebrew/bin") / name),
                str(Path("/usr/local/bin") / name),
            ]
            for candidate in candidates:
                if candidate and Path(candidate).is_file() and os.access(candidate, os.X_OK):
                    directory = str(Path(candidate).parent)
                    if directory not in env.get("PATH", "").split(os.pathsep):
                        env["PATH"] = directory + os.pathsep + env.get("PATH", "")
                    if name in {"fd", "fdfind", "rg"}:
                        tool = "fd" if name == "fdfind" else name
                        installed = runtime / "bin" / tool
                        installed.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copyfile(Path(candidate).resolve(), installed)
                        installed.chmod(0o500)
                        digest = hashlib.sha256(installed.read_bytes()).hexdigest()
                        with (output / "tool-binaries.jsonl").open("a", encoding="utf-8") as log:
                            log.write(
                                json.dumps(
                                    {
                                        "tool": tool,
                                        "source": str(Path(candidate).resolve()),
                                        "path": str(installed),
                                        "sha256": digest,
                                    }
                                )
                                + "\n"
                            )
                    break
        for name in self.config["credential_env"]:
            env[name] = os.environ[name]
        return env

    @staticmethod
    async def _reap(
        process: asyncio.subprocess.Process,
        grace: float = 2,
        groups: set[int] | None = None,
    ) -> None:
        """Reap SDK stdio groups as well as the worker, including detached wrappers."""
        groups = set(groups or ()) | {process.pid}
        # Inspect only numeric process relationships, never command arguments/environment.
        try:
            ps = await asyncio.create_subprocess_exec(
                "ps",
                "-axo",
                "pid=,ppid=,pgid=",
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.DEVNULL,
            )
            listing, _ = await asyncio.wait_for(ps.communicate(), 2)
            rows = [tuple(map(int, line.split())) for line in listing.splitlines()]
            descendants = {process.pid}
            changed = True
            while changed:
                previous = len(descendants)
                for pid, parent, group in rows:
                    if parent in descendants:
                        descendants.add(pid)
                        groups.add(group)
                changed = len(descendants) != previous
        except (OSError, ValueError, TimeoutError):
            pass
        groups.discard(os.getpgrp())
        for group in groups:
            try:
                os.killpg(group, signal.SIGTERM)
            except ProcessLookupError:
                pass
        deadline = time.monotonic() + grace
        while time.monotonic() < deadline:
            await asyncio.sleep(0.025)
            alive = False
            for group in groups:
                try:
                    os.killpg(group, 0)
                    alive = True
                except ProcessLookupError:
                    pass
            if not alive:
                break
        for group in groups:
            try:
                os.killpg(group, signal.SIGKILL)
            except ProcessLookupError:
                pass
        await process.wait()

    async def run(self, request: AgentRequest) -> AgentResult:
        sink = request.token_sink or current_token_usage_sink()
        metadata = dict(request.metadata)
        if context := getattr(sink, "context", None):
            metadata = {
                **{
                    k: context[k] if isinstance(context, dict) else getattr(context, k)
                    for k in TokenUsageRecord.REQUIRED_CONTEXT
                },
                **metadata,
            }
        output = request.output_dir.expanduser().resolve()
        request = request.model_copy(
            update={
                "output_dir": output,
                "cwd": request.cwd.expanduser().resolve(),
                "token_sink": sink,
                "metadata": metadata,
            }
        )
        output.mkdir(parents=True, exist_ok=True)
        identity = {
            "request_id": str(metadata.get("request_id") or uuid.uuid4().hex),
            "run_id": str(metadata.get("run_id") or uuid.uuid4().hex),
            "stage_id": str(metadata.get("stage") or "agent"),
            "attempt_id": str(metadata.get("attempt_id") or uuid.uuid4().hex),
        }
        events_path = output / "protocol.jsonl"
        base = dict(
            backend_identity=self.backend_identity,
            provider=self.provider,
            model_provider=self.config["model_provider"],
            protocol_version=1,
            agent_id=agent_id_for(request),
            events_path=events_path,
            **identity,
        )
        try:
            if events_path.exists():
                raise ValueError(
                    "attempt directory already contains protocol evidence; choose a fresh directory"
                )
            if load_pi_config(self.config_path)[1] != self.config_sha256:
                raise ValueError("Pi inputs changed after plan resolution")
            self.validate_credentials()
            if callable(check := getattr(sink, "check_budget", None)):
                check()
            if self.config["strict_usage"] and any(
                r.usage_missing or r.error_type == "missing_usage"
                for r in getattr(sink, "records", ())
            ):
                raise ValueError(
                    "earlier model usage is missing; strict accounting blocks further calls"
                )
            agent = resolve_agent(self.config, request)
            config = dict(self.config)
            budgets = [
                b
                for b in (config["token_budget"], getattr(sink, "remaining_tokens", None))
                if b is not None
            ]
            config["token_budget"] = min(budgets) if budgets else None
            if config["token_budget"] is not None and config["token_budget"] <= 0:
                raise ValueError("token budget exhausted")
            config["mcp"] = {
                name: server
                for name, server in config["mcp"].items()
                if any(t.startswith(f"mcp__{name}__") for t in agent["tools"])
            }
            if graph := config["mcp"].get("defuzz_callgraph"):
                graph = dict(graph)
                config["mcp"]["defuzz_callgraph"] = graph
                if graph.get("database_path"):
                    from .callgraph_identity import bind_callgraph, database_digest

                    binding = metadata.get("callgraph_binding")
                    if binding is None:
                        compiler = metadata.get("compiler")
                        isas = metadata.get("isas") or (
                            [metadata["isa"]] if metadata.get("isa") else []
                        )
                        if not compiler or not isas:
                            raise ValueError(
                                "CallGraph requires explicit compiler/ISA source binding"
                            )
                        binding = bind_callgraph(
                            Path(graph["database_path"]),
                            request.cwd,
                            compiler=compiler,
                            isas=isas,
                        )
                    if binding.get("database") != graph["database_path"]:
                        raise ValueError("CallGraph binding refers to another database")
                    if binding.get("database_sha256") != database_digest(
                        Path(graph["database_path"])
                    ):
                        raise ValueError("CallGraph database changed after source binding")
                    graph["env"] = {
                        **graph.get("env", {}),
                        "DEFUZZ_CALLGRAPH_DB": graph["database_path"],
                    }
                    graph["args"] = [
                        *graph.get("args", []),
                        *[
                            part
                            for target in binding["targets"]
                            for part in ("--allow-target", target)
                        ],
                    ]
                    agent["system_prompt"] += (
                        "\n\nSource-bound CallGraph targets (other versions are unavailable):\n"
                        + json.dumps(binding["bindings"], sort_keys=True)
                    )
                    _atomic_write(output / "callgraph-binding.json", json.dumps(binding).encode())
            env = self._environment(output)
            config["models_path"] = str(output / ".runtime/models.json")
            _atomic_write(Path(config["models_path"]), json.dumps(config["models"]).encode())
            prompt = request.prompt
            if request.schema_path:
                from jsonschema.validators import validator_for

                schema_bytes = request.schema_path.read_bytes()
                schema = json.loads(schema_bytes)
                validator_for(schema).check_schema(schema)
                _atomic_write(output / "output-schema.json", schema_bytes)
                prompt += (
                    "\n\nReturn one JSON value matching this schema:\n" + schema_bytes.decode()
                )
            control = {
                "protocol_version": 1,
                "type": "run",
                "seq": 1,
                "timestamp": datetime.now(UTC).isoformat(),
                **identity,
                "prompt": prompt,
                "cwd": str(request.cwd),
                "output_dir": str(output),
                "writable": request.writable,
                "deny_read_paths": [str(p) for p in self._denied_paths(request)],
                "agent": agent,
                "config": config,
            }
            for name, value in (
                ("request.json", {**control, "metadata": metadata}),
                ("resolved-agent.json", agent),
                ("resolved-config.json", config),
            ):
                _atomic_write(output / name, (json.dumps(value, indent=2) + "\n").encode())
            argv = self.launch_argv_for(request)
        except (OSError, ValueError, RuntimeError) as exc:
            return AgentResult(
                success=False,
                error=str(exc),
                error_category="config",
                runtime_ok=False,
                schema_ok=False,
                artifact_ok=False,
                evidence_ok=False,
                **base,
            )

        events: deque[Any] = deque(maxlen=512)
        usage_records: dict[str, dict[str, Any]] = {}
        initiated: set[str] = set()
        mcp_groups: set[int] = set()
        terminal: dict[str, Any] | None = None
        terminal_kind: str | None = None
        session_started = False
        session_id: str | None = None
        last_seq = 0
        stdout_tail = b""
        stderr_tail = b""
        stdout_bytes = stderr_bytes = 0
        timed_out = False
        protocol_error: str | None = None
        started = time.monotonic()

        def capture_usage(data: dict[str, Any]) -> None:
            call_id = data["model_call_id"]
            if call_id in usage_records:
                if usage_records[call_id] != data:
                    raise ProtocolError("conflicting model usage events")
                return
            usage_records[call_id] = data
            measured = {
                key: data.get(key)
                for key in (
                    "input_tokens",
                    "output_tokens",
                    "total_tokens",
                    "cached_input_tokens",
                    "cache_creation_input_tokens",
                    "reasoning_tokens",
                )
            }
            record = TokenUsageRecord.from_dict(
                {
                    **{
                        key: str(
                            metadata.get(key, identity["run_id"] if key == "run_id" else "unknown")
                        )
                        for key in TokenUsageRecord.REQUIRED_CONTEXT
                    },
                    "call_id": call_id,
                    "agent": agent["agent_id"],
                    "provider": "pi",
                    "model": self.model,
                    **measured,
                    "latency_ms": data.get("latency_ms"),
                    "success": data.get("status") == "reported"
                    and data.get("stop_reason") not in {"error", "aborted"},
                    "error_type": None if data.get("status") == "reported" else "missing_usage",
                }
            )
            if callable(recorder := getattr(sink, "record", None)):
                recorder(record)
            elif callable(sink):
                sink(record)

        def frame(line: bytes) -> None:
            nonlocal last_seq, terminal, session_id, terminal_kind, session_started
            value = json.loads(line)
            if not isinstance(value, dict) or value.get("protocol_version") != 1:
                raise ProtocolError("unsupported protocol frame")
            if (
                any(value.get(k) != v for k, v in identity.items())
                or value.get("seq") != last_seq + 1
            ):
                raise ProtocolError("frame identity or sequence mismatch")
            if terminal is not None:
                raise ProtocolError("frame received after terminal")
            if session_id and value.get("session_id") != session_id:
                raise ProtocolError("session identity changed")
            session_id = value.get("session_id") or session_id
            last_seq += 1
            if "artifact_ref" in value:
                data = json.loads(verified_artifact(output, value["artifact_ref"]))
            else:
                data = value.get("data", {})
            if not isinstance(data, dict):
                raise ProtocolError("frame payload must be an object")
            kind = value["type"]
            if kind not in {
                "ready",
                "session_started",
                "event",
                "usage",
                "heartbeat",
                "result",
                "error",
            }:
                raise ProtocolError("unknown frame type")
            datetime.fromisoformat(value["timestamp"].replace("Z", "+00:00"))
            if last_seq == 1 and kind != "ready":
                raise ProtocolError("first frame must be ready")
            if kind == "ready" and last_seq != 1:
                raise ProtocolError("duplicate ready frame")
            if kind == "session_started":
                if session_started or not session_id:
                    raise ProtocolError("invalid session_started frame")
                if data.get("model_id") != self.config["model_id"]:
                    raise ProtocolError("session model identity mismatch")
                session_started = True
            if kind == "event" and data.get("type") == "model_request":
                if not session_started or data["model_call_id"] in initiated:
                    raise ProtocolError("invalid model request identity")
                initiated.add(data["model_call_id"])
            if kind == "event" and data.get("type") == "mcp_process_started":
                pid = data.get("pid")
                if not isinstance(pid, int) or pid <= 1 or pid in {os.getpid(), os.getppid()}:
                    raise ProtocolError("invalid MCP process identity")
                # bubblewrap reports PIDs inside its namespace; ps supplies host IDs instead.
                if "bwrap" not in Path(argv[0]).name:
                    mcp_groups.add(pid)
            if kind == "usage":
                if data["model_call_id"] not in initiated:
                    raise ProtocolError("usage without a model request")
                capture_usage(data)
            if kind in {"result", "error"}:
                terminal = data
                terminal_kind = kind
                if data.get("model_calls") != len(initiated) or initiated != usage_records.keys():
                    raise ProtocolError("terminal model request accounting mismatch")
                if kind == "result" and (not session_started or not initiated):
                    raise ProtocolError("result without a measured model session")
                manifest = json.loads(verified_artifact(output, data["manifest"]))
                for item in manifest["files"]:
                    verified_artifact(output, item)
            events.append({"type": kind, "data": data, "seq": last_seq, "session_id": session_id})

        try:
            process = await asyncio.create_subprocess_exec(
                *argv,
                cwd=request.cwd,
                env=env,
                start_new_session=True,
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
        except OSError as exc:
            return AgentResult(
                success=False,
                error=f"cannot start Pi worker: {exc}",
                error_category="config",
                runtime_ok=False,
                schema_ok=False,
                artifact_ok=False,
                evidence_ok=False,
                **base,
            )
        assert process.stdin and process.stdout and process.stderr
        stdin, stdout, stderr = process.stdin, process.stdout, process.stderr

        async def read_stdout() -> None:
            nonlocal stdout_tail, stdout_bytes
            pending = bytearray()
            with events_path.open("wb") as archive:
                while chunk := await stdout.read(32768):
                    archive.write(chunk)
                    archive.flush()
                    stdout_bytes += len(chunk)
                    stdout_tail = (stdout_tail + chunk)[-DIAGNOSTIC_TAIL_BYTES:]
                    if stdout_bytes > MAX_OUTPUT_BYTES:
                        raise ProtocolError("stdout total output limit exceeded")
                    pending.extend(chunk)
                    while (index := pending.find(b"\n")) >= 0:
                        if index > MAX_FRAME_BYTES:
                            raise ProtocolError("protocol frame too large")
                        line = bytes(pending[:index])
                        del pending[: index + 1]
                        frame(line)
                    if len(pending) > MAX_FRAME_BYTES:
                        raise ProtocolError("protocol frame too large")
                if pending:
                    raise ProtocolError("unterminated protocol frame")

        async def read_stderr() -> None:
            nonlocal stderr_tail, stderr_bytes
            with (output / "stderr.log").open("wb") as archive:
                while chunk := await stderr.read(32768):
                    stderr_bytes += len(chunk)
                    archive.write(chunk)
                    archive.flush()
                    stderr_tail = (stderr_tail + chunk)[-DIAGNOSTIC_TAIL_BYTES:]
                    if stderr_bytes > MAX_OUTPUT_BYTES:
                        raise ProtocolError("stderr total output limit exceeded")

        async def cancel(reason: str) -> None:
            try:
                stdin.write(
                    (
                        json.dumps(
                            {
                                "protocol_version": 1,
                                "type": "cancel",
                                **identity,
                                "seq": 2,
                                "timestamp": datetime.now(UTC).isoformat(),
                                "reason": reason,
                            }
                        )
                        + "\n"
                    ).encode()
                )
                await asyncio.wait_for(stdin.drain(), 1)
            except (BrokenPipeError, ConnectionResetError, TimeoutError):
                pass
            try:
                await asyncio.wait_for(process.wait(), self.config["cleanup_timeout_ms"] / 1000 + 1)
            except TimeoutError:
                pass
            await self._reap(process, groups=mcp_groups)

        readers = [asyncio.create_task(read_stdout()), asyncio.create_task(read_stderr())]
        waiter = asyncio.create_task(process.wait())
        combined = asyncio.gather(*readers, waiter)
        try:
            process.stdin.write((json.dumps(control) + "\n").encode())
            await process.stdin.drain()
            await asyncio.wait_for(
                asyncio.shield(combined),
                request.timeout_seconds or self.config["attempt_timeout_seconds"],
            )
        except TimeoutError:
            timed_out = True
            await cancel("attempt timeout")
        except asyncio.CancelledError:
            await cancel("Python task cancelled")
            raise
        except (OSError, ValueError, KeyError, TypeError, ProtocolError) as exc:
            protocol_error = str(exc)
            await cancel("protocol/evidence failure")
        finally:
            process.stdin.close()
            await self._reap(process, grace=0.1, groups=mcp_groups)
            for task in readers:
                if not task.done():
                    task.cancel()
            results = await asyncio.gather(*readers, return_exceptions=True)
            for reader_result in results:
                if isinstance(reader_result, Exception):
                    protocol_error = protocol_error or str(reader_result)
            if combined.done() and not combined.cancelled():
                combined.exception()  # retrieve any parser exception
            # Interrupted requests remain explicit even when the worker was killed.
            for missing in initiated - usage_records.keys():
                capture_usage(
                    {"model_call_id": missing, "status": "missing", "stop_reason": "aborted"}
                )

        error = protocol_error
        category = "protocol" if error else None
        runtime_ok = bool(
            terminal_kind == "result"
            and terminal
            and terminal.get("runtime_ok")
            and process.returncode == 0
            and not timed_out
        )
        evidence_ok = bool(terminal and terminal.get("evidence_ok") and not protocol_error)
        artifact_ok = bool(terminal and terminal.get("artifact_ok") and not protocol_error)
        schema_ok = False
        final: Any = None
        if timed_out:
            error, category = "Pi attempt timed out", "timeout"
        elif not terminal and not error:
            detail = stderr_tail.decode(errors="replace").strip()
            error = "Pi worker exited without a terminal frame" + (f": {detail}" if detail else "")
            category = "config" if detail.startswith("bwrap:") else "protocol"
        elif terminal and not runtime_ok and not error:
            error = terminal.get("error") or f"Pi worker exited with {process.returncode}"
            category = terminal.get("error_category") or "provider"
        if terminal:
            text = terminal.get("final_text", "")
            try:
                final = extract_json(text)
            except ValueError as exc:
                if request.schema_path is None and text:
                    final = text
                elif not error:
                    error, category = f"Pi final output is not valid JSON: {exc}", "schema"
            if final is not None:
                schema_error = None
                if request.schema_path:
                    from jsonschema import ValidationError, validate

                    try:
                        validate(final, json.loads((output / "output-schema.json").read_bytes()))
                    except ValidationError as exc:
                        schema_error = exc.message
                schema_ok = schema_error is None
                if schema_error and not error:
                    error, category = schema_error, "schema"
                _atomic_write(
                    output / "final.json", (json.dumps(final, ensure_ascii=False) + "\n").encode()
                )
        graph_calls = int((terminal or {}).get("callgraph_queries", 0))
        if runtime_ok and self.uses_callgraph(request) and not graph_calls:
            error, category = (
                "Pi returned without a successful required CallGraph query",
                "artifact",
            )
            artifact_ok = False
        complete_usage = all(v.get("status") == "reported" for v in usage_records.values())
        if self.config["strict_usage"] and not complete_usage:
            evidence_ok = False
            error, category = error or "provider usage is incomplete", category or "evidence"
        known = [
            v["total_tokens"] for v in usage_records.values() if v.get("total_tokens") is not None
        ]
        usage = {
            "total_tokens": sum(known) if known else None,
            "call_count": len(usage_records),
            "usage_missing_count": sum(
                v.get("status") != "reported" for v in usage_records.values()
            ),
            "usage_complete": complete_usage,
        }
        result = AgentResult(
            success=runtime_ok and schema_ok and artifact_ok and evidence_ok and not error,
            final=final,
            events=list(events),
            usage=usage,
            exit_code=process.returncode,
            timed_out=timed_out,
            error=error,
            error_category=category,
            session_id=session_id,
            runtime_ok=runtime_ok,
            schema_ok=schema_ok,
            artifact_ok=artifact_ok,
            evidence_ok=evidence_ok,
            raw_stdout=stdout_tail.decode(errors="replace"),
            raw_stderr=stderr_tail.decode(errors="replace"),
            stdout_truncated=stdout_bytes > DIAGNOSTIC_TAIL_BYTES,
            stderr_truncated=stderr_bytes > DIAGNOSTIC_TAIL_BYTES,
            events_truncated=last_seq > len(events),
            final_path=output / "final.json",
            callgraph_queries=graph_calls,
            callgraph_data_queries=(terminal or {}).get("callgraph_data_queries", 0),
            latency_ms=(time.monotonic() - started) * 1000,
            **base,
        )
        _atomic_write(
            output / "admission.json",
            result.model_dump_json(
                exclude={
                    "raw_stdout",
                    "raw_stderr",
                    "events",
                    "final",
                },
                indent=2,
            ).encode(),
        )
        return result


__all__ = ["PiAgentBackend", "load_pi_config"]
