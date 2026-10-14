"""SubagentRunner 3-layer defense unit tests.

We stub the :class:`AgentBackend` so we can drive
``SubagentRunner.run`` deterministically:

- valid JSON on the first call → returns parsed model in one shot;
- invalid JSON on the first call, valid on retry → returns parsed model;
- invalid JSON on all attempts → raises :class:`SubagentRunError`.

No model subprocess is spawned.
"""

from __future__ import annotations

from pathlib import Path

import pytest
from pydantic import BaseModel, ConfigDict, Field

from defuzz_loop.agents.runner import SubagentRunError, SubagentRunner
from defuzz_loop.experiment_engine.agent_backend import AgentRequest, AgentResult


class _Payload(BaseModel):
    model_config = ConfigDict(extra="ignore")

    value: int = Field(..., ge=0)
    label: str


class _FakeBackend:
    """Deterministic stand-in for the real subprocess wrapper."""

    def __init__(self, replies: list[str | Exception]) -> None:
        self._replies = list(replies)
        self.calls: list[AgentRequest] = []

    async def run(self, invocation: AgentRequest) -> AgentResult:  # type: ignore[override]
        self.calls.append(invocation)
        if not self._replies:
            raise AssertionError("FakeBackend ran out of scripted replies")
        reply = self._replies.pop(0)
        if isinstance(reply, Exception):
            raise reply
        if isinstance(reply, AgentResult):
            return reply
        return AgentResult(success=True, final=reply)


def _fenced(body: str) -> str:
    return f"```json\n{body}\n```"


@pytest.mark.asyncio
async def test_valid_json_first_attempt_returns_parsed_model(tmp_path: Path) -> None:
    client = _FakeBackend([_fenced('{"value": 42, "label": "ok"}')])
    runner = SubagentRunner(
        client, cwd=tmp_path, output_dir=tmp_path / "evidence", max_retries=2, timeout_secs=1
    )
    result = await runner.run("some-agent", "hello", _Payload)
    assert result == _Payload(value=42, label="ok")
    assert len(client.calls) == 1
    assert client.calls[0].metadata["agent_id"] == "some-agent"


@pytest.mark.asyncio
async def test_retry_after_validation_error_then_succeeds(tmp_path: Path) -> None:
    client = _FakeBackend(
        [
            _fenced('{"value": -1, "label": 5}'),  # bad: negative + wrong type
            _fenced('{"value": 7, "label": "good"}'),
        ]
    )
    runner = SubagentRunner(
        client, cwd=tmp_path, output_dir=tmp_path / "evidence", max_retries=2, timeout_secs=1
    )
    result = await runner.run("some-agent", "hello", _Payload)
    assert result == _Payload(value=7, label="good")
    # Two calls total: first invalid, then retry with repair prompt.
    assert len(client.calls) == 2
    # The retry prompt embeds the pydantic error and the original task.
    retry_prompt = client.calls[1].prompt
    assert "validation errors" in retry_prompt
    assert "hello" in retry_prompt
    assert '"value": -1' in retry_prompt
    assert client.calls[1].metadata["parent_attempt_id"] == client.calls[0].metadata["attempt_id"]
    assert client.calls[0].output_dir != client.calls[1].output_dir


@pytest.mark.asyncio
async def test_all_retries_fail_raises_subagent_run_error(tmp_path: Path) -> None:
    client = _FakeBackend(
        [
            _fenced('{"value": -1, "label": 1}'),
            _fenced('{"value": -2, "label": 2}'),
            _fenced('{"value": -3, "label": 3}'),
        ]
    )
    runner = SubagentRunner(
        client, cwd=tmp_path, output_dir=tmp_path / "evidence", max_retries=2, timeout_secs=1
    )
    with pytest.raises(SubagentRunError):
        await runner.run("some-agent", "hello", _Payload)
    assert len(client.calls) == 3


@pytest.mark.asyncio
async def test_infrastructure_error_is_not_format_retried(tmp_path: Path) -> None:
    client = _FakeBackend(
        [
            AgentResult(success=False, error="timeout", error_category="timeout"),
            _fenced('{"value": 0, "label": "recovered"}'),
        ]
    )
    runner = SubagentRunner(
        client, cwd=tmp_path, output_dir=tmp_path / "evidence", max_retries=2, timeout_secs=1
    )
    with pytest.raises(SubagentRunError, match="timeout"):
        await runner.run("some-agent", "hello", _Payload)
    assert len(client.calls) == 1


@pytest.mark.asyncio
async def test_missing_json_is_repairable_output_error(
    tmp_path: Path,
) -> None:
    """Malformed JSON is repaired with the original task."""

    client = _FakeBackend(
        [
            "not JSON at all, just prose",
            _fenced('{"value": 1, "label": "ok"}'),
        ]
    )
    runner = SubagentRunner(
        client, cwd=tmp_path, output_dir=tmp_path / "evidence", max_retries=2, timeout_secs=1
    )
    result = await runner.run("some-agent", "hello", _Payload)
    assert result == _Payload(value=1, label="ok")
