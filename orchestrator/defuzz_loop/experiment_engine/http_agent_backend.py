"""Direct OpenAI Responses API backend with workspace-scoped local tools.

The backend deliberately uses the standard library HTTP client instead of an
agent CLI or provider SDK.  Credentials are resolved from the environment at
call time and are never included in persisted events or errors.
"""

from __future__ import annotations

import asyncio
import fnmatch
import hashlib
import http.client
import ipaddress
import json
import os
import re
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path
from typing import Any, Literal, Protocol, runtime_checkable
from urllib.parse import urlsplit

import yaml
from jsonschema import SchemaError, validators
from pydantic import (
    BaseModel,
    ConfigDict,
    NonNegativeFloat,
    NonNegativeInt,
    PositiveFloat,
    PositiveInt,
    field_validator,
    model_validator,
)

from defuzz_loop.token_usage import (
    BudgetExceeded,
    TokenUsageContext,
    TokenUsageRecord,
    normalize_external_agent_usage,
)

from .agent_backend import AgentRequest, AgentResult, ExecAgentBackend

ReasoningEffort = Literal["none", "minimal", "low", "medium", "high", "xhigh", "max"]
ContinuationMode = Literal["full_input"]

_RETRYABLE_STATUS_CODES = frozenset({408, 409, 429, 500, 502, 503, 504})
_USAGE_FIELDS = (
    "input_tokens",
    "output_tokens",
    "total_tokens",
    "cached_input_tokens",
    "cache_creation_input_tokens",
    "reasoning_tokens",
)
_ENVIRONMENT_NAME = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
_SCHEMA_NAME_CHARACTER = re.compile(r"[^A-Za-z0-9_-]+")
_MAX_ERROR_BODY_BYTES = 64 * 1024
_MAX_RESPONSE_BODY_BYTES = 16 * 1024 * 1024
_MAX_RUN_RAW_RESPONSE_BYTES = 32 * 1024 * 1024
_MAX_SEARCH_FILE_BYTES = 2 * 1024 * 1024
_MAX_WRITE_BYTES = 1024 * 1024
_MAX_SCHEMA_REPAIR_ERROR_CHARS = 2000
_MAX_LIST_ENTRIES = 500
_MAX_SEARCH_MATCH_TEXT_CHARS = 400
_DEFAULT_MAX_TOOL_OUTPUT_CHARS = 24 * 1024
_DEFAULT_READ_CONTENT_CHARS = 24 * 1024
_DEFAULT_SEARCH_MAX_MATCHES = 50
_FINISH_TOOL: Mapping[str, Any] = {
    "type": "function",
    "name": "finish",
    "description": (
        "Finish the task after all requested edits are complete. Call this exactly once, "
        "by itself, with a concise non-empty summary."
    ),
    "parameters": {
        "type": "object",
        "properties": {"summary": {"type": "string", "minLength": 1}},
        "required": ["summary"],
        "additionalProperties": False,
    },
    "strict": True,
}


class HTTPAgentConfig(BaseModel):
    """Validated, secret-free configuration for a Responses API backend."""

    model_config = ConfigDict(
        extra="forbid",
        frozen=True,
        str_strip_whitespace=True,
        hide_input_in_errors=True,
    )

    base_url: str
    model: str
    api_key_env: str
    reasoning_effort: ReasoningEffort = "medium"
    timeout: PositiveFloat = 600.0
    max_output_tokens: PositiveInt | None = None
    max_retries: NonNegativeInt = 3
    retry_backoff_seconds: NonNegativeFloat = 0.5
    max_tool_rounds: PositiveInt = 64
    max_schema_retries: NonNegativeInt = 2
    max_tool_output_chars: PositiveInt = _DEFAULT_MAX_TOOL_OUTPUT_CHARS
    search_max_matches: PositiveInt = _DEFAULT_SEARCH_MAX_MATCHES
    read_content_chars: PositiveInt = _DEFAULT_READ_CONTENT_CHARS
    continuation_mode: ContinuationMode = "full_input"
    user_agent: str = "defuzz-loop-http-agent/1.0"

    @model_validator(mode="before")
    @classmethod
    def _reject_inline_credentials(cls, value: Any) -> Any:
        if isinstance(value, Mapping):
            forbidden = {
                str(key)
                for key in value
                if str(key).casefold()
                in {"api_key", "apikey", "authorization", "access_token", "token"}
            }
            if forbidden:
                names = ", ".join(sorted(forbidden))
                raise ValueError(
                    f"inline credentials are forbidden ({names}); configure api_key_env instead"
                )
        return value

    @field_validator("base_url")
    @classmethod
    def _validate_base_url(cls, value: str) -> str:
        parsed = urlsplit(value)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise ValueError("base_url must be an absolute HTTP(S) URL")
        if parsed.username is not None or parsed.password is not None:
            raise ValueError("base_url must not contain credentials")
        if parsed.query or parsed.fragment:
            raise ValueError("base_url must not contain a query or fragment")
        hostname = parsed.hostname or ""
        try:
            loopback = ipaddress.ip_address(hostname).is_loopback
        except ValueError:
            loopback = hostname.casefold() == "localhost"
        if parsed.scheme == "http" and not loopback:
            raise ValueError("plain HTTP is permitted only for loopback endpoints")
        normalized_path = parsed.path.rstrip("/")
        if not (
            normalized_path.endswith("/v1")
            or normalized_path.endswith("/v1/responses")
        ):
            raise ValueError("base_url must end with /v1 or /v1/responses")
        return value.rstrip("/")

    @field_validator("model", "user_agent")
    @classmethod
    def _require_nonempty(cls, value: str) -> str:
        if not value:
            raise ValueError("value must not be empty")
        if "\r" in value or "\n" in value:
            raise ValueError("value must not contain line breaks")
        return value

    @field_validator("api_key_env")
    @classmethod
    def _validate_api_key_env(cls, value: str) -> str:
        if not _ENVIRONMENT_NAME.fullmatch(value):
            raise ValueError("api_key_env must be an environment variable name")
        return value

    @property
    def responses_url(self) -> str:
        if self.base_url.endswith("/responses"):
            return self.base_url
        return f"{self.base_url}/responses"

    @classmethod
    def load(cls, path: str | os.PathLike[str]) -> HTTPAgentConfig:
        return load_http_agent_config(path)


def load_http_agent_config(path: str | os.PathLike[str]) -> HTTPAgentConfig:
    """Load a direct mapping or ``http_agent`` section from YAML/JSON."""

    config, _, _ = load_http_agent_config_snapshot(path)
    return config


def load_http_agent_config_snapshot(
    path: str | os.PathLike[str],
) -> tuple[HTTPAgentConfig, str, int]:
    """Parse and hash one stable byte snapshot of an HTTP config."""

    source = Path(path)
    if not source.is_file():
        raise FileNotFoundError(f"HTTP agent config not found: {source}")
    try:
        raw = source.read_bytes()
        text = raw.decode("utf-8")
        if source.suffix.casefold() == ".json":
            loaded = json.loads(text)
        elif source.suffix.casefold() in {".yaml", ".yml"}:
            loaded = yaml.safe_load(text)
        else:
            raise ValueError("HTTP agent config must use .json, .yaml, or .yml")
    except (OSError, UnicodeError, json.JSONDecodeError, yaml.YAMLError) as exc:
        raise ValueError(f"failed to read HTTP agent config {source}: {exc}") from exc
    if not isinstance(loaded, Mapping):
        raise ValueError("HTTP agent config must contain an object")
    selected: Any = loaded.get("http_agent", loaded)
    if not isinstance(selected, Mapping):
        raise ValueError("http_agent config section must contain an object")
    config = HTTPAgentConfig.model_validate(dict(selected))
    return config, hashlib.sha256(raw).hexdigest(), len(raw)


@runtime_checkable
class ResponsesToolExecutor(Protocol):
    """Extensible executor contract for Responses function calls."""

    @property
    def tools(self) -> Sequence[Mapping[str, Any]]: ...

    async def execute(self, name: str, arguments: Mapping[str, Any]) -> Mapping[str, Any]: ...


class LocalWorkspaceToolExecutor:
    """Safe file tools constrained to one request workspace.

    Symlinks are resolved before access, recursive traversal never follows
    directory symlinks, and denied paths are removed from enumeration as well
    as rejected on direct access.
    """

    _READ_TOOLS = (
        {
            "type": "function",
            "name": "list_files",
            "description": (
                "List files below the workspace. Paths are relative to the workspace. "
                "Recursive listings are capped."
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "recursive": {"type": "boolean"},
                },
                "required": ["path", "recursive"],
                "additionalProperties": False,
            },
            "strict": True,
        },
        {
            "type": "function",
            "name": "search_text",
            "description": (
                "Search UTF-8 text files below a workspace path. The query is a literal "
                "string; file_glob such as '*.py' limits files. Start with offset=0 and "
                "continue at next_offset when truncated=true."
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "query": {"type": "string"},
                    "file_glob": {"type": "string"},
                    "case_sensitive": {"type": "boolean"},
                    "offset": {"type": "integer", "minimum": 0},
                },
                "required": [
                    "path",
                    "query",
                    "file_glob",
                    "case_sensitive",
                    "offset",
                ],
                "additionalProperties": False,
            },
            "strict": True,
        },
        {
            "type": "function",
            "name": "read_file",
            "description": (
                "Read a UTF-8 workspace file by inclusive 1-based line range. "
                "Use end_line=0 to read through EOF."
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "start_line": {"type": "integer", "minimum": 1},
                    "end_line": {"type": "integer", "minimum": 0},
                },
                "required": ["path", "start_line", "end_line"],
                "additionalProperties": False,
            },
            "strict": True,
        },
    )
    _WRITE_TOOLS = (
        {
            "type": "function",
            "name": "write_file",
            "description": (
                "Create or replace a UTF-8 file inside the writable workspace. "
                "Parent directories are created as needed."
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "content": {"type": "string"},
                },
                "required": ["path", "content"],
                "additionalProperties": False,
            },
            "strict": True,
        },
        {
            "type": "function",
            "name": "replace_text",
            "description": (
                "Apply an exact text replacement to one UTF-8 workspace file. "
                "With replace_all=false, old_text must occur exactly once."
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "old_text": {"type": "string"},
                    "new_text": {"type": "string"},
                    "replace_all": {"type": "boolean"},
                },
                "required": ["path", "old_text", "new_text", "replace_all"],
                "additionalProperties": False,
            },
            "strict": True,
        },
    )

    def __init__(
        self,
        request: AgentRequest,
        *,
        search_max_matches: int = _DEFAULT_SEARCH_MAX_MATCHES,
        read_content_chars: int = _DEFAULT_READ_CONTENT_CHARS,
        max_tool_output_chars: int = _DEFAULT_MAX_TOOL_OUTPUT_CHARS,
    ) -> None:
        if search_max_matches <= 0:
            raise ValueError("search_max_matches must be positive")
        if read_content_chars <= 0:
            raise ValueError("read_content_chars must be positive")
        if max_tool_output_chars <= 0:
            raise ValueError("max_tool_output_chars must be positive")
        self._root = request.cwd.expanduser().resolve(strict=True)
        if not self._root.is_dir():
            raise ValueError(f"agent cwd is not a directory: {self._root}")
        self._writable = request.writable
        self._search_max_matches = search_max_matches
        self._read_content_chars = read_content_chars
        self._max_tool_output_chars = max_tool_output_chars
        self._denied = tuple(self._resolve_denied(path) for path in request.deny_read_paths)
        if any(self._contains(path, self._root) for path in self._denied):
            raise ValueError("a denied read path contains the entire agent workspace")

    @property
    def tools(self) -> Sequence[Mapping[str, Any]]:
        if self._writable:
            return (*self._READ_TOOLS, *self._WRITE_TOOLS)
        return self._READ_TOOLS

    async def execute(self, name: str, arguments: Mapping[str, Any]) -> Mapping[str, Any]:
        return await asyncio.to_thread(self._execute_sync, name, arguments)

    def _execute_sync(self, name: str, arguments: Mapping[str, Any]) -> Mapping[str, Any]:
        try:
            if name == "list_files":
                result = self._list_files(arguments)
            elif name == "search_text":
                result = self._search_text(arguments)
            elif name == "read_file":
                result = self._read_file(arguments)
            elif name == "write_file" and self._writable:
                result = self._write_file(arguments)
            elif name in {"replace_text", "apply_patch"} and self._writable:
                result = self._apply_patch(arguments)
            elif name in {"write_file", "replace_text", "apply_patch"}:
                raise PermissionError("write tools are disabled for this read-only request")
            else:
                raise ValueError(f"unknown tool: {name}")
            return {"ok": True, **result}
        except (OSError, UnicodeError, TypeError, ValueError) as exc:
            return {"ok": False, "error": str(exc)}

    def _resolve_denied(self, path: Path) -> Path:
        expanded = path.expanduser()
        if not expanded.is_absolute():
            expanded = self._root / expanded
        return expanded.resolve(strict=False)

    @staticmethod
    def _contains(parent: Path, child: Path) -> bool:
        try:
            child.relative_to(parent)
        except ValueError:
            return False
        return True

    def _is_denied(self, path: Path) -> bool:
        return any(self._contains(denied, path) for denied in self._denied)

    def _resolve(self, raw_path: Any, *, must_exist: bool) -> Path:
        if not isinstance(raw_path, str) or not raw_path:
            raise ValueError("path must be a non-empty string")
        candidate = Path(raw_path).expanduser()
        if not candidate.is_absolute():
            candidate = self._root / candidate
        resolved = candidate.resolve(strict=must_exist)
        if not self._contains(self._root, resolved):
            raise PermissionError("path escapes the agent workspace")
        if self._is_denied(resolved):
            raise PermissionError("path is denied by the agent request")
        return resolved

    def _relative(self, path: Path) -> str:
        value = path.relative_to(self._root).as_posix()
        return value or "."

    def _walk_files(self, directory: Path) -> list[Path]:
        files: list[Path] = []
        for current, dirnames, filenames in os.walk(directory, followlinks=False):
            current_path = Path(current)
            allowed_dirs: list[str] = []
            for dirname in sorted(dirnames):
                candidate = current_path / dirname
                if candidate.is_symlink():
                    continue
                resolved = candidate.resolve(strict=False)
                if self._contains(self._root, resolved) and not self._is_denied(resolved):
                    allowed_dirs.append(dirname)
            dirnames[:] = allowed_dirs
            for filename in sorted(filenames):
                candidate = current_path / filename
                try:
                    resolved = candidate.resolve(strict=True)
                except OSError:
                    continue
                if (
                    candidate.is_symlink()
                    or not resolved.is_file()
                    or not self._contains(self._root, resolved)
                    or self._is_denied(resolved)
                ):
                    continue
                files.append(resolved)
        return files

    def _list_files(self, arguments: Mapping[str, Any]) -> Mapping[str, Any]:
        directory = self._resolve(arguments.get("path"), must_exist=True)
        recursive = arguments.get("recursive")
        if not isinstance(recursive, bool):
            raise TypeError("recursive must be a boolean")
        if not directory.is_dir():
            raise ValueError("list_files path must be a directory")
        entries: list[dict[str, str]] = []
        truncated = False
        if recursive:
            candidates: Sequence[Path] = self._walk_files(directory)
        else:
            candidates = sorted(directory.iterdir(), key=lambda item: item.name)
        for candidate in candidates:
            try:
                resolved = candidate.resolve(strict=True)
            except OSError:
                continue
            if not self._contains(self._root, resolved) or self._is_denied(resolved):
                continue
            if candidate.is_symlink():
                kind = "symlink"
            elif candidate.is_dir():
                kind = "directory"
            elif candidate.is_file():
                kind = "file"
            else:
                kind = "other"
            entries.append({"path": self._relative(candidate), "type": kind})
            if len(entries) >= _MAX_LIST_ENTRIES:
                truncated = True
                break
        return {"entries": entries, "truncated": truncated}

    def _search_text(self, arguments: Mapping[str, Any]) -> Mapping[str, Any]:
        directory = self._resolve(arguments.get("path"), must_exist=True)
        if not directory.is_dir():
            raise ValueError("search_text path must be a directory")
        query = arguments.get("query")
        file_glob = arguments.get("file_glob")
        case_sensitive = arguments.get("case_sensitive")
        offset = arguments.get("offset")
        if not isinstance(query, str) or not query:
            raise ValueError("query must be a non-empty string")
        if not isinstance(file_glob, str):
            raise TypeError("file_glob must be a string")
        if not isinstance(case_sensitive, bool):
            raise TypeError("case_sensitive must be a boolean")
        if isinstance(offset, bool) or not isinstance(offset, int) or offset < 0:
            raise ValueError("offset must be a non-negative integer")
        needle = query if case_sensitive else query.casefold()
        matches: list[dict[str, Any]] = []
        matched_count = 0
        truncated = False
        for path in self._walk_files(directory):
            relative = self._relative(path)
            if file_glob and not fnmatch.fnmatch(relative, file_glob) and not fnmatch.fnmatch(
                path.name, file_glob
            ):
                continue
            try:
                if path.stat().st_size > _MAX_SEARCH_FILE_BYTES:
                    continue
                lines = path.read_text(encoding="utf-8").splitlines()
            except (OSError, UnicodeError):
                continue
            for line_number, line in enumerate(lines, start=1):
                haystack = line if case_sensitive else line.casefold()
                if needle not in haystack:
                    continue
                if matched_count < offset:
                    matched_count += 1
                    continue
                if len(matches) >= self._search_max_matches:
                    truncated = True
                    break
                match = {
                    "path": relative,
                    "line": line_number,
                    "text": line[:_MAX_SEARCH_MATCH_TEXT_CHARS],
                }
                candidate = [*matches, match]
                if not self._fits_tool_output(
                    {
                        "matches": candidate,
                        "offset": offset,
                        "truncated": True,
                        "next_offset": offset + len(candidate),
                    }
                ):
                    truncated = True
                    break
                matches.append(match)
                matched_count += 1
            if truncated:
                break
        return {
            "matches": matches,
            "offset": offset,
            "truncated": truncated,
            "next_offset": offset + len(matches) if truncated else None,
        }

    def _fits_tool_output(self, result: Mapping[str, Any]) -> bool:
        encoded = json.dumps(
            {"ok": True, **result}, ensure_ascii=False, separators=(",", ":")
        )
        return len(encoded) <= self._max_tool_output_chars

    def _read_file(self, arguments: Mapping[str, Any]) -> Mapping[str, Any]:
        path = self._resolve(arguments.get("path"), must_exist=True)
        start_line = arguments.get("start_line")
        end_line = arguments.get("end_line")
        if isinstance(start_line, bool) or not isinstance(start_line, int) or start_line < 1:
            raise ValueError("start_line must be a positive integer")
        if isinstance(end_line, bool) or not isinstance(end_line, int) or end_line < 0:
            raise ValueError("end_line must be a non-negative integer")
        if end_line and end_line < start_line:
            raise ValueError("end_line must be zero or no smaller than start_line")
        if not path.is_file():
            raise ValueError("read_file path must be a file")

        parts: list[str] = []
        content_chars = 0
        returned_end_line: int | None = None
        last_scanned_line = 0
        truncated = False
        line_truncated = False
        eof = False
        next_start_line: int | None = None
        with path.open("r", encoding="utf-8") as stream:
            for line_number, line in enumerate(stream, start=1):
                last_scanned_line = line_number
                if line_number < start_line:
                    continue
                if end_line and line_number > end_line:
                    next_start_line = line_number
                    break

                remaining = self._read_content_chars - content_chars
                if len(line) > remaining:
                    truncated = True
                    if content_chars == 0 and remaining > 0:
                        parts.append(line[:remaining])
                        returned_end_line = line_number
                        line_truncated = True
                        next_start_line = line_number + 1
                    else:
                        next_start_line = line_number
                    break

                parts.append(line)
                content_chars += len(line)
                returned_end_line = line_number
                if end_line and line_number == end_line:
                    following = next(stream, None)
                    eof = following is None
                    if not eof:
                        next_start_line = line_number + 1
                    break
                if content_chars == self._read_content_chars:
                    following = next(stream, None)
                    eof = following is None
                    truncated = not eof
                    if truncated:
                        next_start_line = line_number + 1
                    break
            else:
                eof = True

        return {
            "path": self._relative(path),
            "start_line": start_line,
            "end_line": returned_end_line,
            "content": "".join(parts),
            "truncated": truncated,
            "line_truncated": line_truncated,
            "eof": eof,
            "next_start_line": next_start_line,
            "total_lines": last_scanned_line if eof else None,
        }

    def _write_file(self, arguments: Mapping[str, Any]) -> Mapping[str, Any]:
        path = self._resolve(arguments.get("path"), must_exist=False)
        content = arguments.get("content")
        if not isinstance(content, str):
            raise TypeError("content must be a string")
        encoded_size = len(content.encode())
        if encoded_size > _MAX_WRITE_BYTES:
            raise ValueError(f"content exceeds the {_MAX_WRITE_BYTES}-byte write limit")
        if path.exists() and not path.is_file():
            raise ValueError("write_file path must be a file or not yet exist")
        self._atomic_write_text(path, content)
        return {"path": self._relative(path), "bytes_written": encoded_size}

    def _apply_patch(self, arguments: Mapping[str, Any]) -> Mapping[str, Any]:
        path = self._resolve(arguments.get("path"), must_exist=True)
        old_text = arguments.get("old_text")
        new_text = arguments.get("new_text")
        replace_all = arguments.get("replace_all")
        if not isinstance(old_text, str) or not old_text:
            raise ValueError("old_text must be a non-empty string")
        if not isinstance(new_text, str):
            raise TypeError("new_text must be a string")
        if not isinstance(replace_all, bool):
            raise TypeError("replace_all must be a boolean")
        if not path.is_file():
            raise ValueError("apply_patch path must be a file")
        content = path.read_text(encoding="utf-8")
        occurrences = content.count(old_text)
        if occurrences == 0:
            raise ValueError("old_text was not found")
        if not replace_all and occurrences != 1:
            raise ValueError("old_text is ambiguous; set replace_all=true or provide more context")
        updated = content.replace(old_text, new_text, -1 if replace_all else 1)
        self._atomic_write_text(path, updated)
        return {"path": self._relative(path), "replacements": occurrences if replace_all else 1}

    @staticmethod
    def _atomic_write_text(path: Path, content: str) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        descriptor, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
        temporary = Path(temporary_name)
        try:
            with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
                stream.write(content)
                stream.flush()
                os.fsync(stream.fileno())
            if path.exists():
                temporary.chmod(path.stat().st_mode & 0o777)
            os.replace(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)


class _HTTPAgentError(RuntimeError):
    def __init__(self, message: str, *, retryable: bool = False, timed_out: bool = False) -> None:
        super().__init__(message)
        self.retryable = retryable
        self.timed_out = timed_out


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Reject redirects so Authorization never crosses an origin boundary."""

    def redirect_request(
        self,
        req: urllib.request.Request,
        fp: Any,
        code: int,
        msg: str,
        headers: Any,
        newurl: str,
    ) -> None:
        del req, fp, code, msg, headers, newurl
        return None


class HTTPResponsesAgentBackend:
    """Run an agent through OpenAI's Responses API without a subprocess."""

    # The remote model has no process or unrestricted host-filesystem primitive.
    # Every local read/write goes through LocalWorkspaceToolExecutor, which
    # resolves containment, denies configured roots, and rejects symlinks.
    provider = "http-responses"

    def __init__(
        self,
        config: HTTPAgentConfig,
        *,
        tool_executor_factory: Callable[[AgentRequest], ResponsesToolExecutor] | None = None,
    ) -> None:
        self.config = config
        self._tool_executor_factory = tool_executor_factory

    @property
    def model(self) -> str:
        return self.config.model

    @property
    def supports_host_read_isolation(self) -> bool:
        """Return true only for the built-in workspace-confined executor."""

        return self._tool_executor_factory is None

    @classmethod
    def from_config(
        cls,
        path: str | os.PathLike[str],
        *,
        tool_executor_factory: Callable[[AgentRequest], ResponsesToolExecutor] | None = None,
    ) -> HTTPResponsesAgentBackend:
        return cls(
            load_http_agent_config(path),
            tool_executor_factory=tool_executor_factory,
        )

    def _tool_executor(self, request: AgentRequest) -> ResponsesToolExecutor:
        if self._tool_executor_factory is not None:
            return self._tool_executor_factory(request)
        read_content_chars = min(
            int(self.config.read_content_chars),
            max(1, int(self.config.max_tool_output_chars) - 2048),
        )
        return LocalWorkspaceToolExecutor(
            request,
            search_max_matches=int(self.config.search_max_matches),
            read_content_chars=read_content_chars,
            max_tool_output_chars=int(self.config.max_tool_output_chars),
        )

    @staticmethod
    def _tools_for_request(
        tools: Sequence[Mapping[str, Any]], *, schema_path: Path | None
    ) -> tuple[Mapping[str, Any], ...]:
        selected = tuple(tools)
        if schema_path is None:
            return (*selected, _FINISH_TOOL)
        return selected

    async def run(self, request: AgentRequest) -> AgentResult:
        budget_check = getattr(request.token_sink, "check_budget", None)
        request.output_dir.mkdir(parents=True, exist_ok=True)
        events_path = request.output_dir / "events.jsonl"
        final_path = request.output_dir / "final.json"
        final_path.unlink(missing_ok=True)
        api_key = os.environ.get(self.config.api_key_env)
        if not api_key:
            return self._failure(
                f"HTTP agent API key is unavailable: export ${self.config.api_key_env}",
                events_path=events_path,
            )
        try:
            executor = self._tool_executor(request)
        except (OSError, TypeError, ValueError) as exc:
            return self._failure(
                f"cannot establish HTTP agent workspace isolation: {exc}",
                events_path=events_path,
            )

        timeout = float(request.timeout_seconds or self.config.timeout)
        deadline = time.monotonic() + timeout
        events: list[Any] = []
        raw_responses: list[str] = []
        raw_response_bytes = 0
        usage_parts: list[Mapping[str, Any]] = []
        schema_retries = 0
        schema, schema_error = self._load_schema(request.schema_path)
        if schema_error is not None:
            return self._failure(schema_error, events_path=events_path)
        conversation: list[Any] = [{"role": "user", "content": request.prompt}]
        # Part I embeds the complete source segment or RAG evidence in the
        # prompt and intentionally runs from an empty workspace. Exposing file
        # tools there only encourages redundant empty-directory browsing and
        # multiplies token cost without adding evidence.
        embedded_evidence_only = (
            request.metadata.get("part") == "part-i"
            and request.schema_path is not None
            and not request.writable
        )
        tools = self._tools_for_request(
            () if embedded_evidence_only else executor.tools,
            schema_path=request.schema_path,
        )
        payload = self._initial_payload(
            conversation, tools, schema, request.schema_path
        )
        response_id: str | None = None

        try:
            for round_index in range(1, int(self.config.max_tool_rounds) + 1):
                if callable(budget_check):
                    budget_check()
                started = time.monotonic()
                response, raw_response = await self._request_with_retries(
                    payload, api_key=api_key, deadline=deadline
                )
                latency_ms = (time.monotonic() - started) * 1000.0
                usage = self._normalize_response_usage(response)
                usage_parts.append(usage)
                raw_response_bytes += len(raw_response.encode("utf-8"))
                if raw_response_bytes > _MAX_RUN_RAW_RESPONSE_BYTES:
                    self._record_usage(
                        usage,
                        request,
                        latency_ms=latency_ms,
                        success=False,
                        error_type="ResponseLimitExceeded",
                    )
                    raise _HTTPAgentError(
                        "Responses API cumulative response size exceeded the safety limit"
                    )
                raw_responses.append(self._redact(raw_response, api_key))
                response_id_value = response.get("id")
                response_id = response_id_value if isinstance(response_id_value, str) else None
                events.append(
                    {
                        "type": "response.received",
                        "round": round_index,
                        "response_id": response_id,
                        "status": response.get("status"),
                        "usage": usage,
                    }
                )
                response_error = self._response_error(response)
                if response_error is not None:
                    self._record_usage(
                        usage,
                        request,
                        latency_ms=latency_ms,
                        success=False,
                        error_type="ResponseError",
                    )
                    raise _HTTPAgentError(response_error)
                calls = self._function_calls(response)
                raw_calls = self._raw_function_calls(response)
                if any(item.get("name") == "finish" for item in raw_calls):
                    finish_call, finish_error = self._validate_finish_call(
                        calls, raw_calls, schema_path=request.schema_path
                    )
                    if finish_error is not None:
                        self._record_usage(
                            usage,
                            request,
                            latency_ms=latency_ms,
                            success=False,
                            error_type="TerminalToolError",
                        )
                        raise _HTTPAgentError(finish_error)
                    assert finish_call is not None
                    arguments, argument_error = self._parse_tool_arguments(
                        finish_call["arguments"]
                    )
                    assert argument_error is None
                    summary = str(arguments["summary"]).strip()
                    events.append(
                        {
                            "type": "tool.call",
                            "round": round_index,
                            "response_id": response_id,
                            "call_id": finish_call["call_id"],
                            "name": "finish",
                            "arguments": {"summary": summary},
                        }
                    )
                    self._record_usage(usage, request, latency_ms=latency_ms)
                    aggregate_usage = self._aggregate_usage(usage_parts)
                    events.append(
                        {
                            "type": "turn.completed",
                            "response_id": response_id,
                            "usage": aggregate_usage,
                        }
                    )
                    final = {"summary": summary}
                    safe_events = self._redact_value(events, api_key)
                    safe_final = self._redact_value(final, api_key)
                    assert isinstance(safe_events, list)
                    self._write_events(events_path, safe_events, api_key=api_key)
                    self._write_final(final_path, safe_final)
                    return AgentResult(
                        success=True,
                        final=safe_final,
                        events=safe_events,
                        usage=aggregate_usage,
                        exit_code=None,
                        raw_stdout="\n".join(raw_responses),
                        events_path=events_path,
                        final_path=final_path,
                    )
                if calls:
                    self._record_usage(usage, request, latency_ms=latency_ms)
                    response_output = response.get("output")
                    if not isinstance(response_output, Sequence) or isinstance(
                        response_output, (str, bytes)
                    ):
                        raise _HTTPAgentError("Responses API function response omitted output")
                    conversation.extend(
                        dict(item) for item in response_output if isinstance(item, Mapping)
                    )
                    outputs: list[dict[str, Any]] = []
                    for call in calls:
                        call_id = call["call_id"]
                        name = call["name"]
                        raw_arguments = call["arguments"]
                        arguments, argument_error = self._parse_tool_arguments(raw_arguments)
                        events.append(
                            {
                                "type": "tool.call",
                                "round": round_index,
                                "response_id": response_id,
                                "call_id": call_id,
                                "name": name,
                                "arguments": arguments
                                if argument_error is None
                                else raw_arguments,
                            }
                        )
                        if argument_error is None:
                            remaining = deadline - time.monotonic()
                            if remaining <= 0:
                                raise _HTTPAgentError(
                                    f"HTTP agent timed out before executing tool {name}",
                                    timed_out=True,
                                )
                            try:
                                result = await asyncio.wait_for(
                                    executor.execute(name, arguments), timeout=remaining
                                )
                            except TimeoutError as exc:
                                raise _HTTPAgentError(
                                    f"HTTP agent timed out while executing tool {name}",
                                    timed_out=True,
                                ) from exc
                        else:
                            result = {"ok": False, "error": argument_error}
                        safe_result = self._bounded_tool_result(result)
                        events.append(
                            {
                                "type": "tool.result",
                                "round": round_index,
                                "response_id": response_id,
                                "call_id": call_id,
                                "name": name,
                                "result": safe_result,
                            }
                        )
                        outputs.append(
                            {
                                "type": "function_call_output",
                                "call_id": call_id,
                                "output": json.dumps(
                                    safe_result, ensure_ascii=False, separators=(",", ":")
                                ),
                            }
                        )
                    conversation.extend(outputs)
                    payload = self._continuation_payload(
                        conversation=conversation,
                        delta=outputs,
                        response_id=response_id,
                        tools=tools,
                        schema=schema,
                        schema_path=request.schema_path,
                    )
                    continue

                if request.schema_path is None:
                    self._record_usage(
                        usage,
                        request,
                        latency_ms=latency_ms,
                        success=False,
                        error_type="TerminalToolError",
                    )
                    raise _HTTPAgentError(
                        "schema-free HTTP agent request must terminate with exactly one "
                        "finish function call"
                    )

                final, final_error = self._extract_final(
                    response, schema_path=request.schema_path
                )
                if final_error is not None:
                    self._record_usage(
                        usage,
                        request,
                        latency_ms=latency_ms,
                        success=False,
                        error_type="SchemaValidationError",
                    )
                    if (
                        schema is not None
                        and self._is_repairable_schema_error(final_error, response)
                        and schema_retries < int(self.config.max_schema_retries)
                        and round_index < int(self.config.max_tool_rounds)
                    ):
                        schema_retries += 1
                        response_output = response.get("output")
                        if isinstance(response_output, Sequence) and not isinstance(
                            response_output, (str, bytes)
                        ):
                            conversation.extend(
                                dict(item)
                                for item in response_output
                                if isinstance(item, Mapping)
                            )
                        else:
                            previous_text, _ = self._extract_output_text(response)
                            if previous_text is not None:
                                conversation.append(
                                    {"role": "assistant", "content": previous_text}
                                )
                        repair_error = self._redact(final_error, api_key)[
                            :_MAX_SCHEMA_REPAIR_ERROR_CHARS
                        ]
                        conversation.append(
                            {
                                "role": "user",
                                "content": self._schema_repair_prompt(repair_error),
                            }
                        )
                        events.append(
                            {
                                "type": "schema.repair",
                                "round": round_index,
                                "response_id": response_id,
                                "attempt": schema_retries,
                                "max_attempts": int(self.config.max_schema_retries),
                                "error": repair_error,
                            }
                        )
                        payload = self._continuation_payload(
                            conversation=conversation,
                            delta=[conversation[-1]],
                            response_id=response_id,
                            tools=tools,
                            schema=schema,
                            schema_path=request.schema_path,
                        )
                        continue
                    raise _HTTPAgentError(final_error)
                if final is None:
                    self._record_usage(
                        usage,
                        request,
                        latency_ms=latency_ms,
                        success=False,
                        error_type="MissingFinalOutput",
                    )
                    status = response.get("status")
                    raise _HTTPAgentError(
                        f"Responses API returned no final output (status={status!r})"
                    )
                self._record_usage(usage, request, latency_ms=latency_ms)
                aggregate_usage = self._aggregate_usage(usage_parts)
                events.append(
                    {
                        "type": "turn.completed",
                        "response_id": response_id,
                        "usage": aggregate_usage,
                    }
                )
                safe_events = self._redact_value(events, api_key)
                safe_final = self._redact_value(final, api_key)
                assert isinstance(safe_events, list)
                self._write_events(events_path, safe_events, api_key=api_key)
                self._write_final(final_path, safe_final)
                return AgentResult(
                    success=True,
                    final=safe_final,
                    events=safe_events,
                    usage=aggregate_usage,
                    exit_code=None,
                    raw_stdout="\n".join(raw_responses),
                    events_path=events_path,
                    final_path=final_path,
                )
            raise _HTTPAgentError(
                f"HTTP agent exceeded {self.config.max_tool_rounds} response rounds"
            )
        except asyncio.CancelledError:
            raise
        except BudgetExceeded as exc:
            return self._failed_result(
                error=self._redact(str(exc), api_key),
                error_type=type(exc).__name__,
                events=events,
                usage_parts=usage_parts,
                raw_responses=raw_responses,
                events_path=events_path,
                api_key=api_key,
            )
        except _HTTPAgentError as exc:
            return self._failed_result(
                error=self._redact(str(exc), api_key),
                error_type=type(exc).__name__,
                events=events,
                usage_parts=usage_parts,
                raw_responses=raw_responses,
                events_path=events_path,
                api_key=api_key,
                timed_out=exc.timed_out,
            )
        except Exception as exc:
            # A response may already have consumed tokens when an observer or
            # custom executor fails. Persist the audit trail before propagating.
            if events or usage_parts:
                error = self._redact(f"{type(exc).__name__}: {exc}", api_key)
                self._persist_failure_event(
                    events,
                    error=error,
                    error_type=type(exc).__name__,
                    events_path=events_path,
                    api_key=api_key,
                )
            raise

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
        token_sink: Any = None,
        metadata: Mapping[str, Any] | None = None,
        **compat: Any,
    ) -> AgentResult:
        destination = Path(output_dir)
        resolved_schema = Path(schema_path) if schema_path is not None else None
        if isinstance(schema, type) and issubclass(schema, BaseModel):
            schema = schema.model_json_schema()
        elif isinstance(schema, BaseModel):
            schema = type(schema).model_json_schema()
        if isinstance(schema, Mapping):
            resolved_schema = destination / "output-schema.json"
            self._atomic_write_bytes(
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
        return await self.run(
            AgentRequest(
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
        )

    def _initial_payload(
        self,
        conversation: Sequence[Any],
        tools: Sequence[Mapping[str, Any]],
        schema: Mapping[str, Any] | None,
        schema_path: Path | None,
    ) -> dict[str, Any]:
        payload = self._common_payload(tools, schema, schema_path)
        payload["input"] = list(conversation)
        return payload

    def _continuation_payload(
        self,
        *,
        conversation: Sequence[Any],
        delta: Sequence[Any],
        response_id: str | None,
        tools: Sequence[Mapping[str, Any]],
        schema: Mapping[str, Any] | None,
        schema_path: Path | None,
    ) -> dict[str, Any]:
        payload = self._common_payload(tools, schema, schema_path)
        del delta, response_id
        payload["input"] = list(conversation)
        return payload

    def _common_payload(
        self,
        tools: Sequence[Mapping[str, Any]],
        schema: Mapping[str, Any] | None,
        schema_path: Path | None,
    ) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "model": self.config.model,
            "reasoning": {"effort": self.config.reasoning_effort},
            "store": False,
            "include": ["reasoning.encrypted_content"],
            "instructions": (
                "Work only through the provided local workspace tools. Inspect files before "
                "answering. When write tools are available and the task requests edits, make "
                "the edits before finishing. Never claim an edit that a tool did not "
                "successfully apply. No shell or command-execution tool is available; the "
                "orchestrator runs fixed validation commands after your edits and will provide "
                "any failures in a later repair request. Do not keep browsing to self-validate "
                "after completing the requested edits. If a finish tool is available, call it "
                "exactly once and by itself with a concise summary."
            ),
        }
        if tools:
            payload.update(
                {
                    "tools": [dict(tool) for tool in tools],
                    "tool_choice": "auto",
                    "parallel_tool_calls": False,
                }
            )
        if self.config.max_output_tokens is not None:
            payload["max_output_tokens"] = self.config.max_output_tokens
        if schema is not None:
            payload["text"] = {
                "format": {
                    "type": "json_schema",
                    "name": self._schema_name(schema_path),
                    "strict": True,
                    "schema": dict(schema),
                }
            }
        return payload

    async def _request_with_retries(
        self, payload: Mapping[str, Any], *, api_key: str, deadline: float
    ) -> tuple[Mapping[str, Any], str]:
        last_error: _HTTPAgentError | None = None
        attempts = int(self.config.max_retries) + 1
        idempotency_key = f"defuzz-{uuid.uuid4().hex}"
        for attempt in range(attempts):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise _HTTPAgentError("HTTP agent timed out", timed_out=True)
            try:
                response, raw = await asyncio.wait_for(
                    asyncio.to_thread(
                        self._post_once, payload, api_key, remaining, idempotency_key
                    ),
                    timeout=remaining,
                )
                return response, raw
            except TimeoutError as exc:
                raise _HTTPAgentError(
                    "HTTP agent timed out while waiting for the Responses API",
                    timed_out=True,
                ) from exc
            except _HTTPAgentError as exc:
                last_error = exc
                if not exc.retryable or attempt + 1 >= attempts:
                    raise
                delay = float(self.config.retry_backoff_seconds) * (2**attempt)
                remaining = deadline - time.monotonic()
                if remaining <= delay:
                    raise _HTTPAgentError(
                        "HTTP agent timed out during retry", timed_out=True
                    ) from exc
                await asyncio.sleep(delay)
        assert last_error is not None
        raise last_error

    def _post_once(
        self,
        payload: Mapping[str, Any],
        api_key: str,
        timeout: float,
        idempotency_key: str,
    ) -> tuple[Mapping[str, Any], str]:
        body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode()
        request = urllib.request.Request(
            self.config.responses_url,
            data=body,
            method="POST",
            headers={
                "Authorization": f"Bearer {api_key}",
                "Content-Type": "application/json",
                "Accept": "application/json",
                "User-Agent": self.config.user_agent,
                "Idempotency-Key": idempotency_key,
            },
        )
        try:
            opener = urllib.request.build_opener(
                urllib.request.ProxyHandler({}), _NoRedirectHandler()
            )
            with opener.open(request, timeout=timeout) as response:  # noqa: S310
                raw_bytes = response.read(_MAX_RESPONSE_BODY_BYTES + 1)
                if len(raw_bytes) > _MAX_RESPONSE_BODY_BYTES:
                    raise _HTTPAgentError(
                        "Responses API response exceeded the configured safety limit"
                    )
        except urllib.error.HTTPError as exc:
            try:
                raw_bytes = exc.read(_MAX_ERROR_BODY_BYTES)
            except OSError:
                raw_bytes = b""
            detail = self._http_error_detail(raw_bytes)
            raise _HTTPAgentError(
                f"Responses API HTTP {exc.code}: {detail}",
                retryable=exc.code in _RETRYABLE_STATUS_CODES,
            ) from exc
        except TimeoutError as exc:
            raise _HTTPAgentError(
                "Responses API request timed out", retryable=True, timed_out=True
            ) from exc
        except (urllib.error.URLError, http.client.HTTPException, OSError) as exc:
            reason = getattr(exc, "reason", exc)
            timed_out = isinstance(reason, TimeoutError)
            message = (
                "Responses API request timed out"
                if timed_out
                else "Responses API request failed"
            )
            raise _HTTPAgentError(message, retryable=True, timed_out=timed_out) from exc
        raw = raw_bytes.decode("utf-8", errors="replace")
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise _HTTPAgentError("Responses API returned invalid JSON", retryable=True) from exc
        if not isinstance(parsed, Mapping):
            raise _HTTPAgentError("Responses API returned a non-object JSON value")
        return parsed, raw

    @staticmethod
    def _http_error_detail(body: bytes) -> str:
        text = body.decode("utf-8", errors="replace").strip()
        if not text:
            return "empty response body"
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError:
            return text[:1000]
        if isinstance(parsed, Mapping):
            error = parsed.get("error")
            if isinstance(error, Mapping) and isinstance(error.get("message"), str):
                return str(error["message"])[:1000]
            if isinstance(error, str):
                return error[:1000]
        return text[:1000]

    @staticmethod
    def _response_error(response: Mapping[str, Any]) -> str | None:
        error = response.get("error")
        if error:
            if isinstance(error, Mapping):
                message = error.get("message")
                if isinstance(message, str):
                    return f"Responses API error: {message}"
            return f"Responses API error: {error}"
        if response.get("status") == "failed":
            return "Responses API reported a failed response"
        if response.get("status") == "incomplete":
            details = response.get("incomplete_details")
            return f"Responses API returned an incomplete response: {details}"
        return None

    @staticmethod
    def _function_calls(response: Mapping[str, Any]) -> list[dict[str, str]]:
        calls: list[dict[str, str]] = []
        for item in HTTPResponsesAgentBackend._raw_function_calls(response):
            call_id = item.get("call_id")
            name = item.get("name")
            arguments = item.get("arguments")
            if not isinstance(call_id, str) or not call_id:
                continue
            if not isinstance(name, str) or not name:
                continue
            if not isinstance(arguments, str) or not arguments:
                continue
            calls.append({"call_id": call_id, "name": name, "arguments": arguments})
        return calls

    @staticmethod
    def _raw_function_calls(response: Mapping[str, Any]) -> list[Mapping[str, Any]]:
        output = response.get("output")
        if not isinstance(output, Sequence) or isinstance(output, (str, bytes)):
            return []
        return [
            item
            for item in output
            if isinstance(item, Mapping) and item.get("type") == "function_call"
        ]

    @classmethod
    def _validate_finish_call(
        cls,
        calls: Sequence[Mapping[str, str]],
        raw_calls: Sequence[Mapping[str, Any]],
        *,
        schema_path: Path | None,
    ) -> tuple[Mapping[str, str] | None, str | None]:
        if schema_path is not None:
            return None, "finish is not available for schema-constrained requests"
        if len(raw_calls) != 1:
            return None, "finish must be the only function call in its response"
        if len(calls) != 1 or calls[0].get("name") != "finish":
            return None, "finish function call is malformed"
        call = calls[0]
        arguments, error = cls._parse_tool_arguments(call["arguments"])
        if error is not None:
            return None, f"invalid finish arguments: {error}"
        if set(arguments) != {"summary"}:
            return None, "finish arguments must contain only summary"
        summary = arguments.get("summary")
        if not isinstance(summary, str) or not summary.strip():
            return None, "finish summary must be a non-empty string"
        return call, None

    @staticmethod
    def _parse_tool_arguments(raw: str) -> tuple[Mapping[str, Any], str | None]:
        try:
            arguments = json.loads(raw)
        except json.JSONDecodeError as exc:
            return {}, f"function arguments are invalid JSON: {exc.msg}"
        if not isinstance(arguments, Mapping):
            return {}, "function arguments must be a JSON object"
        return arguments, None

    def _bounded_tool_result(self, result: Mapping[str, Any]) -> Mapping[str, Any]:
        encoded = json.dumps(result, ensure_ascii=False, separators=(",", ":"))
        limit = int(self.config.max_tool_output_chars)
        if len(encoded) <= limit:
            return dict(result)
        return {
            "ok": False,
            "error": f"tool output exceeded {limit} characters",
            "truncated": True,
        }

    @staticmethod
    def _load_schema(path: Path | None) -> tuple[Mapping[str, Any] | None, str | None]:
        if path is None:
            return None, None
        try:
            parsed = json.loads(path.expanduser().resolve(strict=True).read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            return None, f"invalid output schema: {exc}"
        if not isinstance(parsed, Mapping):
            return None, "invalid output schema: root must be an object"
        try:
            validators.validator_for(parsed).check_schema(parsed)
        except SchemaError as exc:
            return None, f"invalid output schema: {exc.message}"
        return parsed, None

    @staticmethod
    def _schema_name(path: Path | None) -> str:
        source = path.stem if path is not None else "defuzz_output"
        sanitized = _SCHEMA_NAME_CHARACTER.sub("_", source).strip("_")
        return (sanitized or "defuzz_output")[:64]

    @staticmethod
    def _extract_output_text(response: Mapping[str, Any]) -> tuple[str | None, str | None]:
        direct = response.get("output_text")
        if isinstance(direct, str):
            return direct, None
        output = response.get("output")
        if not isinstance(output, Sequence) or isinstance(output, (str, bytes)):
            return None, None
        text_parts: list[str] = []
        refusals: list[str] = []
        for item in output:
            if not isinstance(item, Mapping) or item.get("type") != "message":
                continue
            content = item.get("content")
            if not isinstance(content, Sequence) or isinstance(content, (str, bytes)):
                continue
            for part in content:
                if not isinstance(part, Mapping):
                    continue
                if part.get("type") == "output_text" and isinstance(part.get("text"), str):
                    text_parts.append(str(part["text"]))
                elif part.get("type") == "refusal" and isinstance(
                    part.get("refusal"), str
                ):
                    refusals.append(str(part["refusal"]))
        if refusals and not text_parts:
            return None, f"model refused the request: {' '.join(refusals)}"
        return "".join(text_parts) if text_parts else None, None

    @classmethod
    def _extract_final(
        cls, response: Mapping[str, Any], *, schema_path: Path | None
    ) -> tuple[Any, str | None]:
        text, error = cls._extract_output_text(response)
        if error is not None or text is None:
            return None, error
        if schema_path is None:
            return text, None
        try:
            value = json.loads(text)
        except json.JSONDecodeError:
            return None, "agent final output is not valid JSON"
        schema_error = ExecAgentBackend._validate_schema(value, schema_path)
        if schema_error is not None:
            return None, schema_error
        return value, None

    @classmethod
    def _is_repairable_schema_error(
        cls, error: str, response: Mapping[str, Any]
    ) -> bool:
        text, output_error = cls._extract_output_text(response)
        if output_error is not None or text is None:
            return False
        return error == "agent final output is not valid JSON" or error.startswith(
            "agent final output failed schema validation:"
        )

    @staticmethod
    def _schema_repair_prompt(error: str) -> str:
        return (
            "Your previous final output did not satisfy the required JSON Schema. "
            f"Validation error: {error}\n"
            "Return only a corrected JSON value that matches the supplied text.format "
            "schema exactly. Preserve the intended answer, include every required field, "
            "and do not wrap the JSON in Markdown."
        )

    @staticmethod
    def _aggregate_usage(parts: Sequence[Mapping[str, Any]]) -> dict[str, Any] | None:
        if not parts:
            return None
        aggregate: dict[str, Any] = {}
        for field in _USAGE_FIELDS:
            values = [part.get(field) for part in parts if part.get(field) is not None]
            aggregate[field] = sum(values) if values else None
        aggregate["usage_missing"] = all(aggregate[field] is None for field in _USAGE_FIELDS)
        return aggregate

    @staticmethod
    def _normalize_response_usage(response: Mapping[str, Any]) -> dict[str, Any]:
        """Normalize both canonical and Responses-specific usage detail keys."""

        normalized = dict(normalize_external_agent_usage(response))
        raw_usage = response.get("usage")
        if not isinstance(raw_usage, Mapping):
            return normalized
        input_details = raw_usage.get("input_tokens_details")
        if not isinstance(input_details, Mapping):
            input_details = raw_usage.get("input_token_details")
        output_details = raw_usage.get("output_tokens_details")
        if not isinstance(output_details, Mapping):
            output_details = raw_usage.get("output_token_details")
        if normalized.get("cached_input_tokens") is None and isinstance(
            input_details, Mapping
        ):
            cached = input_details.get("cached_tokens")
            if isinstance(cached, int) and not isinstance(cached, bool) and cached >= 0:
                normalized["cached_input_tokens"] = cached
        if normalized.get("reasoning_tokens") is None and isinstance(
            output_details, Mapping
        ):
            reasoning = output_details.get("reasoning_tokens")
            if isinstance(reasoning, int) and not isinstance(reasoning, bool) and reasoning >= 0:
                normalized["reasoning_tokens"] = reasoning
        normalized["usage_missing"] = all(
            normalized.get(field) is None for field in _USAGE_FIELDS
        )
        return normalized

    def _record_usage(
        self,
        response: Mapping[str, Any],
        request: AgentRequest,
        *,
        latency_ms: float,
        success: bool = True,
        error_type: str | None = None,
    ) -> None:
        sink = request.token_sink
        if sink is None:
            return
        context: dict[str, Any] = {
            "run_id": "unknown",
            "experiment": "unknown",
            "variant": "full",
            "part": "unknown",
            "stage": "agent",
            "agent": "http-responses",
            "provider": "openai-responses",
            "model": self.config.model,
            **request.metadata,
        }
        external_recorder = getattr(sink, "record_external_usage", None)
        if callable(external_recorder):
            selected = TokenUsageContext(
                **{key: context[key] for key in TokenUsageRecord.REQUIRED_CONTEXT},
                agent=context["agent"],
                provider=context["provider"],
                model=context["model"],
            )
            try:
                external_recorder(
                    response,
                    context=selected,
                    latency_ms=latency_ms,
                    success=success,
                    error_type=error_type,
                )
            except TypeError:
                external_recorder(response)
            return
        record = TokenUsageRecord.from_response(
            response,
            **{key: context[key] for key in TokenUsageRecord.REQUIRED_CONTEXT},
            agent=context["agent"],
            provider=context["provider"],
            model=context["model"],
            latency_ms=latency_ms,
            success=success,
            error_type=error_type,
        )
        recorder = getattr(sink, "record", None)
        if callable(recorder):
            recorder(record)
        elif callable(sink):
            sink(record)

    @staticmethod
    def _redact(text: str, api_key: str) -> str:
        if not api_key:
            return text
        return text.replace(api_key, "[REDACTED]")

    @classmethod
    def _redact_value(cls, value: Any, api_key: str) -> Any:
        if isinstance(value, str):
            return cls._redact(value, api_key)
        if isinstance(value, list):
            return [cls._redact_value(item, api_key) for item in value]
        if isinstance(value, tuple):
            return tuple(cls._redact_value(item, api_key) for item in value)
        if isinstance(value, Mapping):
            return {
                cls._redact(str(key), api_key): cls._redact_value(item, api_key)
                for key, item in value.items()
            }
        return value

    @classmethod
    def _persist_failure_event(
        cls,
        events: list[Any],
        *,
        error: str,
        error_type: str,
        events_path: Path,
        api_key: str,
    ) -> list[Any]:
        events.append(
            {"type": "turn.failed", "error": error, "error_type": error_type}
        )
        safe_events = cls._redact_value(events, api_key)
        assert isinstance(safe_events, list)
        cls._write_events(events_path, safe_events, api_key=api_key)
        return safe_events

    @classmethod
    def _failed_result(
        cls,
        *,
        error: str,
        error_type: str,
        events: list[Any],
        usage_parts: Sequence[Mapping[str, Any]],
        raw_responses: Sequence[str],
        events_path: Path,
        api_key: str,
        timed_out: bool = False,
    ) -> AgentResult:
        safe_events = cls._persist_failure_event(
            events,
            error=error,
            error_type=error_type,
            events_path=events_path,
            api_key=api_key,
        )
        return AgentResult(
            success=False,
            events=safe_events,
            usage=cls._aggregate_usage(usage_parts),
            timed_out=timed_out,
            error=error,
            raw_stdout="\n".join(raw_responses),
            raw_stderr=error,
            events_path=events_path,
        )

    @classmethod
    def _write_events(cls, path: Path, events: Sequence[Any], *, api_key: str) -> None:
        content = "".join(
            cls._redact(json.dumps(event, ensure_ascii=False, separators=(",", ":")), api_key)
            + "\n"
            for event in events
        )
        cls._atomic_write_bytes(path, content.encode())

    @classmethod
    def _write_final(cls, path: Path, final: Any) -> None:
        if isinstance(final, str):
            content = final
        else:
            content = json.dumps(final, ensure_ascii=False, indent=2) + "\n"
        cls._atomic_write_bytes(path, content.encode())

    @staticmethod
    def _atomic_write_bytes(path: Path, content: bytes) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
        try:
            temporary.write_bytes(content)
            os.replace(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)

    @staticmethod
    def _failure(message: str, *, events_path: Path) -> AgentResult:
        return AgentResult(success=False, error=message, events_path=events_path)


# Short alias for callers that do not need the transport name in configuration.
HTTPAgentBackend = HTTPResponsesAgentBackend

__all__ = [
    "ContinuationMode",
    "HTTPAgentBackend",
    "HTTPAgentConfig",
    "HTTPResponsesAgentBackend",
    "LocalWorkspaceToolExecutor",
    "ReasoningEffort",
    "ResponsesToolExecutor",
    "load_http_agent_config",
    "load_http_agent_config_snapshot",
]
