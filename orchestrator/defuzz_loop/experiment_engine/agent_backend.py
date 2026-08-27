"""Subprocess-backed adapters for non-interactive TraeCode and Codex agents."""

from __future__ import annotations

import asyncio
import json
import os
import shutil
import signal
import sys
import tempfile
import time
import uuid
from collections.abc import Callable, Iterator, Mapping, Sequence
from contextlib import contextmanager
from pathlib import Path
from typing import Any, Literal, Protocol, runtime_checkable

from pydantic import BaseModel, ConfigDict, Field, PositiveFloat, model_validator

from defuzz_loop.token_usage import (
    TokenUsageContext,
    TokenUsageRecord,
    TokenUsageSink,
    normalize_external_agent_usage,
)


class AgentRequest(BaseModel):
    model_config = ConfigDict(arbitrary_types_allowed=True, extra="allow")

    prompt: str
    cwd: Path
    output_dir: Path
    schema_path: Path | None = None
    timeout_seconds: PositiveFloat | None = None
    writable: bool = False
    token_sink: TokenUsageSink | Callable[[TokenUsageRecord], None] | Any | None = None
    deny_read_paths: list[Path] = Field(default_factory=list)
    require_host_read_isolation: bool = False
    metadata: dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode="before")
    @classmethod
    def _compat_names(cls, value: Any) -> Any:
        if not isinstance(value, Mapping):
            return value
        data = dict(value)
        if "schema_path" not in data and "output_schema" in data:
            data["schema_path"] = data.pop("output_schema")
        if "timeout_seconds" not in data and "timeout" in data:
            data["timeout_seconds"] = data.pop("timeout")
        return data


class AgentResult(BaseModel):
    model_config = ConfigDict(extra="allow")

    success: bool
    final: Any = None
    events: list[Any] = Field(default_factory=list)
    usage: dict[str, Any] | None = None
    exit_code: int | None = None
    timed_out: bool = False
    error: str | None = None
    raw_stdout: str = ""
    raw_stderr: str = ""
    events_path: Path | None = None
    final_path: Path | None = None

    @property
    def output(self) -> Any:
        return self.final

    @property
    def stdout(self) -> str:
        return self.raw_stdout

    @property
    def stderr(self) -> str:
        return self.raw_stderr

    @property
    def content(self) -> Any:
        return self.final


@runtime_checkable
class AgentBackend(Protocol):
    async def run(self, request: AgentRequest) -> AgentResult: ...


_CAPABILITY_FLAGS = {
    "-c",
    "--config",
    "-C",
    "--cd",
    "--add-dir",
    "-s",
    "--sandbox",
    "-y",
    "--dangerously-bypass-approvals-and-sandbox",
    "--dangerously-bypass-hook-trust",
    "--permission-mode",
    "-a",
    "--ask-for-approval",
    "--output-schema",
    "-o",
    "--output-last-message",
    "--search",
    "--allowed-tool",
    "--disallowed-tool",
    "--enable",
    "--disable",
    "-m",
    "--model",
    "--ignore-user-config",
    "--ignore-rules",
    "--skip-git-repo-check",
    "--profile",
    "-p",
}

_AGENT_ISOLATION_CONFIG = (
    "memories.use_memories=false",
    "memories.generate_memories=false",
    "features.memories=false",
    "project_doc_max_bytes=0",
    "resource_dirs=[]",
    "skills.bundled.enabled=false",
    "skills.include_instructions=false",
    "features.plugins=false",
    "features.hooks=false",
    "features.apps=false",
)
_TRAEX_ISOLATION_CONFIG = ("features.plugin_hooks=false",)
_CREDENTIAL_FILES = ("auth.json", "models_cache.json")
_SUBPROCESS_ENVIRONMENT = {
    "PATH",
    "HOME",
    "SHELL",
    "TMPDIR",
    "TMP",
    "TEMP",
    "LANG",
    "SSL_CERT_FILE",
    "SSL_CERT_DIR",
    "SYSTEMROOT",
    "WINDIR",
    "COMSPEC",
    "PATHEXT",
}

AgentProvider = Literal["traex", "codex"]


def _atomic_write(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    try:
        temporary.write_bytes(content)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


class ExecAgentBackend:
    """Run an agent CLI with fixed capability arguments and JSONL capture."""

    def __init__(
        self,
        binary: str | os.PathLike[str] = "traex",
        model: str | None = None,
        *,
        provider: AgentProvider | None = None,
        extra_args: Sequence[str] = (),
        terminate_grace_seconds: float = 2.0,
    ) -> None:
        self.binary = os.fspath(binary)
        self.model = model
        if provider not in {None, "traex", "codex"}:
            raise ValueError(f"unsupported agent provider: {provider}")
        self._provider = provider or self._infer_provider()
        self.extra_args = tuple(str(value) for value in extra_args)
        self.terminate_grace_seconds = terminate_grace_seconds
        self._validate_extra_args()

    def _validate_extra_args(self) -> None:
        for index, argument in enumerate(self.extra_args):
            name = argument.split("=", 1)[0]
            if name in {"-c", "--config"}:
                raise ValueError(f"config-affecting argument is managed by the backend: {name}")
            if index > 0 and self.extra_args[index - 1] in {"-c", "--config"}:
                raise ValueError(
                    "config-affecting argument is managed by the backend: "
                    f"{self.extra_args[index - 1]}"
                )
            if name in _CAPABILITY_FLAGS:
                raise ValueError(f"capability-affecting argument is managed by the backend: {name}")
            if "\x00" in argument:
                raise ValueError("agent argument contains a NUL byte")

    @property
    def provider(self) -> AgentProvider:
        return self._provider

    def _infer_provider(self) -> AgentProvider:
        name = Path(self.binary).name.casefold()
        if "codex" in name:
            return "codex"
        if "traex" in name or "traecli" in name:
            return "traex"
        raise ValueError(
            "agent provider cannot be inferred from binary name; "
            "pass provider='traex' or provider='codex'"
        )

    @property
    def supports_host_read_isolation(self) -> bool:
        return sys.platform == "darwin" and Path("/usr/bin/sandbox-exec").is_file()

    @staticmethod
    def _traex_cli_home() -> Path:
        if value := os.environ.get("TRAECLI_HOME"):
            return Path(value).expanduser()
        if value := os.environ.get("TRAE_HOME"):
            return Path(value).expanduser() / "cli"
        return Path.home() / ".trae" / "cli"

    @staticmethod
    def _traex_home() -> Path:
        if value := os.environ.get("TRAE_HOME"):
            return Path(value).expanduser()
        return Path.home() / ".trae"

    @staticmethod
    def _codex_home() -> Path:
        if value := os.environ.get("CODEX_HOME"):
            return Path(value).expanduser()
        return Path.home() / ".codex"

    @staticmethod
    def _minimal_environment() -> dict[str, str]:
        return {
            key: value
            for key, value in os.environ.items()
            if (key.upper() in _SUBPROCESS_ENVIRONMENT or key.upper().startswith("LC_"))
            and not any(
                marker in key.upper()
                for marker in ("TOKEN", "SECRET", "PASSWORD", "API_KEY", "CREDENTIAL")
            )
        }

    def _resource_paths(self) -> list[Path]:
        home = Path(os.environ.get("HOME", os.fspath(Path.home()))).expanduser()
        if self.provider == "traex":
            credential_home = self._traex_cli_home()
            candidates = [
                credential_home,
                self._traex_home(),
                home / ".trae",
                home / ".trae-cn",
            ]
        else:
            candidates = [self._codex_home(), home / ".codex"]
        candidates.extend(
            (
                home / ".agents" / "skills",
                home / ".codex" / "skills",
                home / ".trae" / "skills",
                home / ".trae-cn" / "skills",
            )
        )
        paths: list[Path] = []
        for candidate in candidates:
            if not candidate.exists():
                continue
            resolved = candidate.resolve(strict=False)
            if resolved == Path("/"):
                raise RuntimeError(
                    f"refusing to deny filesystem root as a {self.provider} resource path"
                )
            if resolved not in paths:
                paths.append(resolved)
        return paths

    @staticmethod
    def _contains(parent: Path, child: Path) -> bool:
        try:
            child.relative_to(parent)
        except ValueError:
            return False
        return True

    def _protected_paths(self, request: AgentRequest, final_path: Path | None) -> dict[str, Path]:
        paths = {
            "workspace": request.cwd.expanduser().resolve(strict=True),
            "output": request.output_dir.expanduser().resolve(strict=False),
        }
        resolved_binary = shutil.which(self.binary)
        if resolved_binary is not None:
            paths["agent binary"] = Path(resolved_binary).resolve(strict=False)
        if final_path is not None:
            paths["final output"] = final_path.expanduser().resolve(strict=False)
        return paths

    @contextmanager
    def _subprocess_environment(self) -> Iterator[dict[str, str]]:
        temporaries: list[tempfile.TemporaryDirectory[str]] = []
        try:
            if self.provider == "traex":
                temporaries = [
                    tempfile.TemporaryDirectory(
                        prefix="defuzz-traex-home-", ignore_cleanup_errors=True
                    ),
                    tempfile.TemporaryDirectory(
                        prefix="defuzz-traex-cli-home-", ignore_cleanup_errors=True
                    ),
                ]
                isolated_homes = {
                    "TRAE_HOME": Path(temporaries[0].name),
                    "TRAECLI_HOME": Path(temporaries[1].name),
                }
                credential_home = self._traex_cli_home()
                credential_label = "TraeX"
            else:
                temporaries = [
                    tempfile.TemporaryDirectory(
                        prefix="defuzz-codex-home-", ignore_cleanup_errors=True
                    )
                ]
                isolated_homes = {"CODEX_HOME": Path(temporaries[0].name)}
                credential_home = self._codex_home()
                credential_label = "Codex"
            auth_source = credential_home / "auth.json"
            if not auth_source.is_file():
                raise RuntimeError(
                    f"{credential_label} credentials are unavailable: {auth_source} is not a file"
                )
            destination_home = next(reversed(isolated_homes.values()))
            for name in _CREDENTIAL_FILES:
                source = credential_home / name
                if not source.is_file():
                    continue
                destination = destination_home / name
                shutil.copyfile(source, destination)
                destination.chmod(0o600)
        except (OSError, RuntimeError) as exc:
            for temporary in reversed(temporaries):
                temporary.cleanup()
            raise RuntimeError(f"failed to prepare isolated {self.provider} home: {exc}") from exc

        environment = self._minimal_environment()
        environment.update({name: os.fspath(path) for name, path in isolated_homes.items()})
        try:
            yield environment
        finally:
            for temporary in reversed(temporaries):
                temporary.cleanup()

    @staticmethod
    def _deny_profile(paths: Sequence[Path]) -> str:
        rules = ["(version 1)", "(allow default)"]
        for path in paths:
            resolved = path.expanduser().resolve(strict=False)
            if resolved == Path("/"):
                raise ValueError("refusing to deny the filesystem root")
            # JSON string quoting is compatible with the SBPL string grammar.
            rules.append(f"(deny file-read* (subpath {json.dumps(str(resolved))}))")
        return "\n".join(rules)

    def launch_argv_for(self, request: AgentRequest, final_path: Path | None = None) -> list[str]:
        argv = self.argv_for(request, final_path)
        automatic = self._resource_paths()
        protected = self._protected_paths(request, final_path)
        explicit = [path.expanduser().resolve(strict=False) for path in request.deny_read_paths]
        for denied_path in (*explicit, *automatic):
            for label, protected_path in protected.items():
                if self._contains(denied_path, protected_path):
                    raise RuntimeError(
                        f"deny path {denied_path} contains the {label} {protected_path}"
                    )
        denied = list(
            dict.fromkeys(
                [
                    *explicit,
                    *automatic,
                ]
            )
        )
        if not denied:
            if request.require_host_read_isolation:
                raise RuntimeError(
                    "host read isolation was required but no deny paths were provided"
                )
            return argv
        if self.supports_host_read_isolation:
            return ["/usr/bin/sandbox-exec", "-p", self._deny_profile(denied), *argv]
        if request.require_host_read_isolation:
            raise RuntimeError(
                "host read isolation is unavailable on this platform; run in a container "
                "or provide a supported OS sandbox"
            )
        return argv

    def argv_for(self, request: AgentRequest, final_path: Path | None = None) -> list[str]:
        cwd = request.cwd.expanduser().resolve(strict=True)
        if not cwd.is_dir():
            raise ValueError(f"agent cwd is not a directory: {cwd}")
        output = request.output_dir.expanduser().resolve(strict=False)
        final = final_path or output / "final.json"
        if self.provider == "codex":
            isolation_config: Sequence[str] = _AGENT_ISOLATION_CONFIG
        else:
            isolation_config = (*_AGENT_ISOLATION_CONFIG, *_TRAEX_ISOLATION_CONFIG)
        argv = [
            self.binary,
            "--sandbox",
            "workspace-write" if request.writable else "read-only",
            "--ask-for-approval",
            "never",
            "-C",
            os.fspath(cwd),
        ]
        if self.model:
            argv.extend(("--model", self.model))
        for config in isolation_config:
            argv.extend(("-c", config))
        argv.extend(
            [
                "exec",
                "--json",
                "--color",
                "never",
                "--ephemeral",
                "--ignore-user-config",
                "--ignore-rules",
                "--skip-git-repo-check",
                "--output-last-message",
                os.fspath(final),
            ]
        )
        if request.schema_path is not None:
            schema = request.schema_path.expanduser().resolve(strict=True)
            if not schema.is_file():
                raise ValueError(f"output schema is not a file: {schema}")
            argv.extend(("--output-schema", os.fspath(schema)))
        argv.extend(self.extra_args)
        argv.append("-")
        return argv

    async def run(self, request: AgentRequest) -> AgentResult:
        budget_check = getattr(request.token_sink, "check_budget", None)
        if callable(budget_check):
            budget_check()
        request.output_dir.mkdir(parents=True, exist_ok=True)
        events_path = request.output_dir / "events.jsonl"
        final_path = request.output_dir / "final.json"
        started = time.monotonic()
        try:
            with self._subprocess_environment() as environment:
                try:
                    argv = self.launch_argv_for(request, final_path)
                except (OSError, RuntimeError, ValueError) as exc:
                    return AgentResult(
                        success=False,
                        error=f"cannot establish agent isolation: {exc}",
                        events_path=events_path,
                    )
                final_path.unlink(missing_ok=True)
                try:
                    process = await asyncio.create_subprocess_exec(
                        *argv,
                        stdin=asyncio.subprocess.PIPE,
                        stdout=asyncio.subprocess.PIPE,
                        stderr=asyncio.subprocess.PIPE,
                        cwd=request.cwd,
                        env=environment,
                        start_new_session=True,
                    )
                except OSError as exc:
                    return AgentResult(
                        success=False,
                        error=f"failed to start {self.binary}: {exc}",
                        events_path=events_path,
                    )

                assert process.stdin is not None
                assert process.stdout is not None
                assert process.stderr is not None
                stdout_task = asyncio.create_task(process.stdout.read())
                stderr_task = asyncio.create_task(process.stderr.read())
                process.stdin.write(request.prompt.encode("utf-8"))
                try:
                    await process.stdin.drain()
                except (BrokenPipeError, ConnectionResetError):
                    pass
                finally:
                    process.stdin.close()

                timed_out = False
                try:
                    await asyncio.wait_for(process.wait(), timeout=request.timeout_seconds)
                except TimeoutError:
                    timed_out = True
                    await self._terminate_process_group(process)
                except asyncio.CancelledError:
                    await self._terminate_process_group(process)
                    raise
                stdout, stderr = await asyncio.gather(stdout_task, stderr_task)
        except RuntimeError as exc:
            return AgentResult(
                success=False,
                error=f"cannot establish agent isolation: {exc}",
                events_path=events_path,
            )
        _atomic_write(events_path, stdout)

        raw_stdout = stdout.decode("utf-8", errors="replace")
        raw_stderr = stderr.decode("utf-8", errors="replace")
        events = self._parse_events(raw_stdout)
        usage = self._capture_usage(
            events, request, latency_ms=(time.monotonic() - started) * 1000.0
        )
        final, final_error = self._read_final(final_path, events, request.schema_path)
        completed = any(
            isinstance(event, Mapping) and event.get("type") == "turn.completed" for event in events
        )
        success = (
            process.returncode == 0
            and not timed_out
            and final is not None
            and final_error is None
            and completed
        )
        error = None
        if timed_out:
            error = f"agent timed out after {request.timeout_seconds} seconds"
        elif process.returncode != 0:
            error = raw_stderr.strip() or f"agent exited with status {process.returncode}"
        elif final_error is not None:
            error = final_error
        elif final is None:
            error = "agent did not produce a final output"
        elif not completed:
            error = "agent event stream ended without turn.completed"
        return AgentResult(
            success=success,
            final=final,
            events=events,
            usage=usage,
            exit_code=process.returncode,
            timed_out=timed_out,
            error=error,
            raw_stdout=raw_stdout,
            raw_stderr=raw_stderr,
            events_path=events_path,
            final_path=final_path if final_path.exists() else None,
        )

    async def complete(
        self,
        prompt: str,
        schema: Any = None,
        *,
        cwd: str | os.PathLike[str] = ".",
        output_dir: str | os.PathLike[str] = ".agent-output",
        schema_path: str | os.PathLike[str] | None = None,
        timeout_seconds: float | None = None,
        writable: bool = False,
        token_sink: TokenUsageSink | Callable[[TokenUsageRecord], None] | Any | None = None,
        metadata: Mapping[str, Any] | None = None,
        **compat: Any,
    ) -> AgentResult:
        destination = Path(output_dir)
        resolved_schema: Path | None = Path(schema_path) if schema_path is not None else None
        if isinstance(schema, type) and issubclass(schema, BaseModel):
            schema = schema.model_json_schema()
        elif isinstance(schema, BaseModel):
            schema = type(schema).model_json_schema()
        if isinstance(schema, Mapping):
            resolved_schema = destination / "output-schema.json"
            _atomic_write(
                resolved_schema,
                (json.dumps(dict(schema), ensure_ascii=False, indent=2) + "\n").encode(),
            )
        elif schema is not None:
            resolved_schema = Path(schema)
        context_keys = {
            "run_id",
            "experiment",
            "variant",
            "part",
            "stage",
            "agent",
            "provider",
            "model",
        }
        context = dict(metadata or {})
        for key in tuple(compat):
            if key in context_keys:
                context[key] = compat.pop(key)
        request = AgentRequest(
            prompt=prompt,
            cwd=Path(cwd),
            output_dir=destination,
            schema_path=resolved_schema,
            timeout_seconds=timeout_seconds,
            writable=writable,
            token_sink=token_sink,
            metadata=context,
            **compat,
        )
        return await self.run(request)

    async def _terminate_process_group(self, process: asyncio.subprocess.Process) -> None:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            return
        try:
            await asyncio.wait_for(process.wait(), timeout=self.terminate_grace_seconds)
        except TimeoutError:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                return
            await process.wait()

    @staticmethod
    def _parse_events(stdout: str) -> list[Any]:
        events: list[Any] = []
        for line in stdout.splitlines():
            if not line.strip():
                continue
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                events.append(line)
        return events

    @staticmethod
    def _read_final(
        path: Path, events: Sequence[Any], schema_path: Path | None
    ) -> tuple[Any, str | None]:
        if path.is_file():
            text = path.read_text(encoding="utf-8")
            try:
                value = json.loads(text)
            except json.JSONDecodeError:
                if schema_path is not None:
                    return None, "agent final output is not valid JSON"
                return text, None
            if schema_path is not None:
                error = ExecAgentBackend._validate_schema(value, schema_path)
                if error is not None:
                    return None, error
            return value, None
        for event in reversed(events):
            if not isinstance(event, Mapping):
                continue
            if event.get("type") in {"item.completed", "message.completed"}:
                item = event.get("item", event)
                if isinstance(item, Mapping):
                    for key in ("output_text", "text", "content"):
                        if key in item:
                            return item[key], None
        return None, None

    @staticmethod
    def _validate_schema(value: Any, schema_path: Path) -> str | None:
        try:
            from jsonschema import ValidationError, validate

            schema = json.loads(schema_path.read_text(encoding="utf-8"))
            validate(instance=value, schema=schema)
        except (OSError, json.JSONDecodeError) as exc:
            return f"invalid output schema: {exc}"
        except ValidationError as exc:
            return f"agent final output failed schema validation: {exc.message}"
        except ImportError:
            # The agent CLI performs schema validation itself. Locally retain the
            # strongest validation possible without making jsonschema mandatory.
            if not isinstance(value, (dict, list)):
                return "agent final output is not structured JSON"
        return None

    def _capture_usage(
        self, events: Sequence[Any], request: AgentRequest, *, latency_ms: float
    ) -> dict[str, Any] | None:
        last_usage: dict[str, Any] | None = None
        for event in events:
            if not isinstance(event, Mapping) or event.get("type") != "turn.completed":
                continue
            raw_usage = event.get("usage")
            last_usage = dict(normalize_external_agent_usage(event))
            if request.token_sink is None:
                continue
            context = {
                "run_id": "unknown",
                "experiment": "unknown",
                "variant": "full",
                "part": "unknown",
                "stage": "agent",
                "agent": self.provider,
                "provider": self.provider,
                "model": self.model,
                "latency_ms": latency_ms,
                **request.metadata,
            }
            sink = request.token_sink
            external_recorder = getattr(sink, "record_external_usage", None)
            if callable(external_recorder):
                selected = TokenUsageContext(
                    **{key: context[key] for key in TokenUsageRecord.REQUIRED_CONTEXT},
                    agent=context["agent"],
                    provider=context["provider"],
                    model=context["model"],
                )
                try:
                    external_recorder(event, context=selected, latency_ms=latency_ms)
                except TypeError:
                    external_recorder(event)
            else:
                record_context = {
                    key: context[key]
                    for key in (
                        *TokenUsageRecord.REQUIRED_CONTEXT,
                        "agent",
                        "provider",
                        "model",
                        "latency_ms",
                    )
                }
                record = TokenUsageRecord.from_response(raw_usage, **record_context)
                recorder = getattr(sink, "record", None)
                if callable(recorder):
                    recorder(record)
                elif callable(sink):
                    sink(record)
        return last_usage


__all__ = [
    "AgentBackend",
    "AgentProvider",
    "AgentRequest",
    "AgentResult",
    "ExecAgentBackend",
]
