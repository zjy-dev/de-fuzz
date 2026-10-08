"""End-to-end source-mapper glue test using a fake OpenCodeClient."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest

from defuzz_loop.opencode.client import OpenCodeClient, OpenCodeInvocationError
from defuzz_loop.opencode.schemas import Invariant
from defuzz_loop.source_mapping.mapper import (
    SourceMappingError,
    SourceMappingRunner,
    map_invariant_to_source,
)


class _FakeClient(OpenCodeClient):
    """A doubles OpenCodeClient that replays scripted responses without
    launching a subprocess."""

    def __init__(self, replies: list[str | Exception]) -> None:
        super().__init__(executable="/nonexistent/opencode")
        self._replies = list(replies)
        self.calls: list[Any] = []

    async def run_agent(self, invocation: Any) -> str:  # type: ignore[override]
        self.calls.append(invocation)
        if not self._replies:
            raise AssertionError("no more scripted replies")
        reply = self._replies.pop(0)
        if isinstance(reply, BaseException):
            raise reply
        return reply


_SAMPLE_INV = Invariant(
    phi_id="SP-001",
    phi_text="For any function with a VLA...",
    mechanism="stack_canary",
    compiler="gcc",
    isa="x86_64",
    src_root="/opt/gcc-src",
)


_GOOD_REPLY = """
Here's the mapping.

```json
{
  "phi_id": "SP-001",
  "compiler": "gcc",
  "mapping_confidence": "high",
  "level_1_pass": {
    "name": "pass_stack_protect",
    "file": "gcc/cfgexpand.cc",
    "line_start": 6521,
    "line_end": 6688,
    "evidence": "pass_data + gate"
  },
  "level_2_functions": [],
  "level_3_critical_structs": [],
  "level_3_critical_conditions": [],
  "evidence_paths": ["gcc/cfgexpand.cc"],
  "unresolved_notes": []
}
```
"""


@pytest.mark.asyncio
async def test_mapper_happy_path(tmp_path: Path) -> None:
    client = _FakeClient([_GOOD_REPLY])
    result = await map_invariant_to_source(
        _SAMPLE_INV,
        project_root=tmp_path,
        client=client,
    )
    assert result.phi_id == "SP-001"
    assert result.mapping_confidence == "high"
    assert result.level_1_pass.name == "pass_stack_protect"

    # And the invocation carried the correct payload.
    assert len(client.calls) == 1
    assert client.calls[0].agent == "source-mapper"
    assert "<invariant>" in client.calls[0].message


@pytest.mark.asyncio
async def test_mapper_retries_then_succeeds(tmp_path: Path) -> None:
    """First reply is malformed; runner should retry and succeed on the second."""

    client = _FakeClient(["This is not JSON at all.", _GOOD_REPLY])
    runner = SourceMappingRunner(client, project_root=tmp_path, max_retries=2)
    result = await runner.run(_SAMPLE_INV)
    assert result.phi_id == "SP-001"
    assert len(client.calls) == 2
    assert "failed validation" in client.calls[1].message
    assert "Original task" in client.calls[1].message


@pytest.mark.asyncio
async def test_mapper_gives_up_after_retries(tmp_path: Path) -> None:
    client = _FakeClient(["nope", "still nope", "even more nope"])
    runner = SourceMappingRunner(client, project_root=tmp_path, max_retries=2)
    with pytest.raises(SourceMappingError):
        await runner.run(_SAMPLE_INV)
    assert len(client.calls) == 3  # 1 initial + 2 retries


@pytest.mark.asyncio
async def test_mapper_retries_on_invocation_error(tmp_path: Path) -> None:
    client = _FakeClient([OpenCodeInvocationError("timeout"), _GOOD_REPLY])
    runner = SourceMappingRunner(client, project_root=tmp_path, max_retries=2)
    result = await runner.run(_SAMPLE_INV)
    assert result.phi_id == "SP-001"


@pytest.mark.asyncio
async def test_mapper_phi_id_mismatch_is_retried(tmp_path: Path) -> None:
    """If the agent returns a valid schema but wrong phi_id, we retry."""

    wrong_id_reply = _GOOD_REPLY.replace('"SP-001"', '"SP-999"', 1)
    client = _FakeClient([wrong_id_reply, _GOOD_REPLY])
    runner = SourceMappingRunner(client, project_root=tmp_path, max_retries=2)
    result = await runner.run(_SAMPLE_INV)
    assert result.phi_id == "SP-001"
    assert len(client.calls) == 2
