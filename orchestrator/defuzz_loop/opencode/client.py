"""Async wrapper for ``opencode run`` (headless mode).

Design goals:

- **One process per invocation.** No shared server, no session reuse. Each
  call is a fresh ``opencode run --format json`` subprocess that terminates
  as soon as the assistant finishes. This makes concurrent invocations
  (:func:`asyncio.gather`) trivially safe.
- **Timeout + kill hygiene.** If OpenCode hangs (LLM stall, network blip),
  the child process is force-killed after ``timeout_secs``.
- **Structured errors.** Non-zero exit, stdout parse failure, or empty
  assistant reply all raise :class:`OpenCodeInvocationError` with the
  full stderr tail attached for triage.

The ``--format json`` flag makes OpenCode emit line-delimited JSON events
to stdout. We only need the *final assistant text*, which we recover with
a small, forgiving parser (see :func:`_extract_final_assistant_text`).
"""

from __future__ import annotations

import asyncio
import json
import re
import shutil
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


class OpenCodeInvocationError(RuntimeError):
    """Raised when an ``opencode run`` invocation fails or returns garbage."""


@dataclass(frozen=True)
class OpenCodeInvocation:
    """Everything a single headless call needs to know."""

    agent: str
    """SubAgent name, e.g. ``source-mapper`` (must match a markdown file)."""

    message: str
    """The user message to feed OpenCode."""

    cwd: Path
    """Working directory for the subprocess (``--dir``). Determines which
    ``.opencode/agent/*.md`` files are visible."""

    model: str | None = None
    """Optional model override, e.g. ``coconut/coconut-gpt-5-6-terra-max``.
    If ``None``, OpenCode uses its configured default."""

    timeout_secs: float = 600.0

    env: dict[str, str] = field(default_factory=dict)


class OpenCodeClient:
    """Async subprocess client for ``opencode run --format json``.

    Instances are cheap; you typically construct one per orchestrator run
    and share it across all subagent invocations. Concurrency is naturally
    supported by the OS: N :meth:`run_agent` awaitables in ``asyncio.gather``
    spawn N independent ``opencode`` processes.
    """

    def __init__(self, *, executable: str | None = None) -> None:
        exe = executable or shutil.which("opencode") or "opencode"
        self._exe = exe

    @property
    def executable(self) -> str:
        return self._exe

    async def run_agent(self, invocation: OpenCodeInvocation) -> str:
        """Spawn ``opencode run``, feed a message, return the assistant's
        final text.

        Raises :class:`OpenCodeInvocationError` on non-zero exit, timeout,
        or an unparseable stdout.
        """

        cmd: list[str] = [
            self._exe,
            "run",
            "--agent",
            invocation.agent,
            "--format",
            "json",
            "--dir",
            str(invocation.cwd),
        ]
        if invocation.model is not None:
            cmd += ["--model", invocation.model]
        cmd.append(invocation.message)

        proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            env={**invocation.env} if invocation.env else None,
        )

        try:
            stdout_b, stderr_b = await asyncio.wait_for(
                proc.communicate(), timeout=invocation.timeout_secs
            )
        except TimeoutError as e:
            with _suppress():
                proc.kill()
                await proc.wait()
            raise OpenCodeInvocationError(
                f"opencode run timed out after {invocation.timeout_secs:.0f}s "
                f"(agent={invocation.agent!r}, cwd={invocation.cwd})"
            ) from e

        stdout = stdout_b.decode("utf-8", errors="replace")
        stderr = stderr_b.decode("utf-8", errors="replace")

        if proc.returncode != 0:
            raise OpenCodeInvocationError(
                f"opencode run exited with code {proc.returncode} "
                f"(agent={invocation.agent!r})\n"
                f"--- stderr tail ---\n{_tail(stderr, 2000)}"
            )

        text = _extract_final_assistant_text(stdout)
        if not text.strip():
            raise OpenCodeInvocationError(
                f"opencode run returned empty assistant text "
                f"(agent={invocation.agent!r})\n"
                f"--- stdout tail ---\n{_tail(stdout, 2000)}"
            )
        return text


# ------------------------------- helpers -------------------------------------


class _suppress:
    """Tiny sync context manager that swallows all exceptions."""

    def __enter__(self) -> None:
        return None

    def __exit__(self, exc_type: Any, exc: Any, tb: Any) -> bool:  # noqa: D401
        return True


def _tail(s: str, n: int) -> str:
    return s if len(s) <= n else "...(truncated)...\n" + s[-n:]


_JSON_FENCE_RE = re.compile(r"```json\s*\n(.+?)\n```", re.DOTALL)


def _extract_final_assistant_text(stdout: str) -> str:
    """Recover the final assistant message from ``opencode run --format json``
    stdout.

    OpenCode's ``--format json`` mode emits line-delimited JSON events on
    stdout. Each line is a JSON object; the events we care about have some
    variant of ``{"type": "message.delta", "role": "assistant", ...}`` or a
    final ``{"type": "message", "role": "assistant", "text": "..."}``. The
    exact event schema has changed between OpenCode versions.

    Rather than couple to a specific schema, we use a forgiving strategy:

    1. Scan all JSON lines and collect any string field named ``text`` /
       ``content`` from events tagged ``role == "assistant"``.
    2. If we found nothing, fall back to *any* fenced ```json ...``` block
       in the raw stdout.
    3. Return the *last* candidate — the subagent's final answer.

    That final text is expected (by our subagent markdown prompts) to
    itself be a fenced ```json ...``` block; higher-level parsers extract
    that block via :func:`extract_json_block`.
    """

    candidates: list[str] = []

    for line in stdout.splitlines():
        line = line.strip()
        if not line or not (line.startswith("{") or line.startswith("[")):
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        text = _harvest_assistant_text(obj)
        if text:
            candidates.append(text)

    if candidates:
        return candidates[-1]

    # Fallback: maybe stdout is plain text with a JSON block inline.
    blocks = _JSON_FENCE_RE.findall(stdout)
    if blocks:
        return "```json\n" + blocks[-1] + "\n```"

    return stdout


def _harvest_assistant_text(obj: Any) -> str | None:
    """Best-effort extraction of assistant-role text from an arbitrary
    OpenCode event object."""

    if not isinstance(obj, dict):
        return None
    role = obj.get("role") or obj.get("author") or ""
    if isinstance(role, str) and role.lower() != "assistant":
        # Nested? Some schemas wrap the message under "message".
        msg = obj.get("message")
        if isinstance(msg, dict):
            return _harvest_assistant_text(msg)
        return None

    for key in ("text", "content", "output"):
        val = obj.get(key)
        if isinstance(val, str) and val.strip():
            return val
        if isinstance(val, list):
            # e.g. [{"type": "text", "text": "..."}]
            parts: list[str] = []
            for item in val:
                if isinstance(item, dict):
                    for k in ("text", "content"):
                        v = item.get(k)
                        if isinstance(v, str):
                            parts.append(v)
            joined = "".join(parts).strip()
            if joined:
                return joined
    # Nested "message" wrapper.
    msg = obj.get("message")
    if isinstance(msg, dict):
        return _harvest_assistant_text(msg)
    return None


def extract_json_block(assistant_text: str) -> str:
    """Extract the last ```json ...``` fenced block from an assistant reply.

    Raises :class:`OpenCodeInvocationError` if no fenced JSON block is found;
    that indicates the subagent violated its output contract.
    """

    matches = _JSON_FENCE_RE.findall(assistant_text)
    if not matches:
        # Also allow a bare JSON object as a fallback — some models drop the
        # fence when the message is just JSON.
        stripped = assistant_text.strip()
        if stripped.startswith("{") and stripped.endswith("}"):
            return stripped
        raise OpenCodeInvocationError(
            "Assistant reply did not contain a ```json ...``` fenced block.\n"
            f"--- reply tail ---\n{_tail(assistant_text, 2000)}"
        )
    return matches[-1].strip()
