"""Smoke test that actually launches ``opencode run --agent source-mapper``.

**Auto-skipped** when:

- the ``opencode`` binary is not on PATH, OR
- the ``DEFUZZ_ENABLE_OPENCODE_SMOKE`` env var is not set to ``"1"``.

We keep this test opt-in because it depends on network access to the LLM
provider configured in ``~/.config/opencode/opencode.jsonc`` and can take
tens of seconds. The offline tests in :mod:`test_client_parsing` cover the
parser logic without any subprocess.
"""

from __future__ import annotations

import json
import os
import shutil
from pathlib import Path

import pytest

from defuzz_loop.opencode import (
    Invariant,
    OpenCodeClient,
    OpenCodeInvocation,
)
from defuzz_loop.opencode.client import extract_json_block
from defuzz_loop.opencode.schemas import SourceMappingResult

pytestmark = [
    pytest.mark.skipif(
        shutil.which("opencode") is None,
        reason="opencode binary not on PATH",
    ),
    pytest.mark.skipif(
        os.environ.get("DEFUZZ_ENABLE_OPENCODE_SMOKE") != "1",
        reason="DEFUZZ_ENABLE_OPENCODE_SMOKE=1 not set",
    ),
]


def _repo_root() -> Path:
    """Locate the project root that owns .opencode/agent/source-mapper.md."""

    here = Path(__file__).resolve()
    for parent in [here, *here.parents]:
        if (parent / ".opencode" / "agent" / "source-mapper.md").exists():
            return parent
    raise RuntimeError("could not locate .opencode/agent/source-mapper.md")


@pytest.mark.asyncio
async def test_source_mapper_returns_parseable_shape(tmp_path: Path) -> None:
    """The subagent must return JSON that our pydantic schema accepts.

    We don't assert on the semantic *contents* (that depends on the model
    and would be flaky), only on the schema.
    """

    root = _repo_root()
    inv = Invariant(
        phi_id="SMOKE-001",
        phi_text=(
            "For any function that declares a variable-length array (VLA), "
            "GCC must allocate a stack canary slot."
        ),
        mechanism="stack_canary",
        compiler="gcc",
        isa="x86_64",
        # We don't need a real GCC source tree for a schema-shape smoke:
        # the subagent may return low-confidence + unresolved_notes.
        src_root=str(tmp_path),
    )

    client = OpenCodeClient()
    text = await client.run_agent(
        OpenCodeInvocation(
            agent="source-mapper",
            message=inv.as_prompt_payload(),
            cwd=root,
            timeout_secs=180.0,
        )
    )
    block = extract_json_block(text)
    parsed = json.loads(block)  # sanity: it must be valid JSON at minimum
    # If the agent respected the schema, this should not raise.
    SourceMappingResult.model_validate(parsed)
