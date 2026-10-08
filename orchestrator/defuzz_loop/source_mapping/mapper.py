"""Drive the ``source-mapper`` OpenCode SubAgent to map an invariant onto
compiler source code.

This is the glue between:

- input: a single :class:`Invariant` (phi_id, phi_text, mechanism, src_root)
- output: a validated :class:`SourceMappingResult`

The heavy lifting (grep/read/list on the compiler source tree) is delegated
to the OpenCode subagent defined in ``.opencode/agent/source-mapper.md``;
this module is a thin retry+validation harness.
"""

from __future__ import annotations

import logging
from pathlib import Path

from pydantic import ValidationError

from ..opencode.client import (
    OpenCodeClient,
    OpenCodeInvocation,
    OpenCodeInvocationError,
    extract_json_block,
)
from ..opencode.schemas import Invariant, SourceMappingResult

log = logging.getLogger(__name__)


class SourceMappingError(RuntimeError):
    """Raised when the source-mapper subagent fails after retries."""


class SourceMappingRunner:
    """Retries + JSON validation around a single :class:`OpenCodeClient`.

    Parameters
    ----------
    client:
        The OpenCode subprocess client. Share one across the whole orchestrator
        run.
    project_root:
        Directory containing ``.opencode/agent/source-mapper.md``. This is
        NOT the compiler source tree — it's the DeFuzz project root that
        holds the subagent definition. Passed to ``opencode run --dir``.
    model:
        Optional model override forwarded to ``opencode run --model``. If
        ``None`` (default), OpenCode uses its configured default from
        ``~/.config/opencode/opencode.jsonc``.
    max_retries:
        How many additional attempts to make if the subagent returns
        malformed JSON or fails to invoke. Default: 2 (total 3 attempts).
    timeout_secs:
        Per-attempt wall-clock timeout.
    """

    _AGENT_NAME = "source-mapper"

    def __init__(
        self,
        client: OpenCodeClient,
        *,
        project_root: Path,
        model: str | None = None,
        max_retries: int = 2,
        timeout_secs: float = 600.0,
    ) -> None:
        self._client = client
        self._project_root = Path(project_root)
        self._model = model
        self._max_retries = max_retries
        self._timeout_secs = timeout_secs

    async def run(self, invariant: Invariant) -> SourceMappingResult:
        """Execute the source-mapper subagent for a single invariant.

        Retries on:

        - ``OpenCodeInvocationError`` (subprocess failure, timeout, empty reply)
        - ``ValueError`` / ``pydantic.ValidationError`` (malformed JSON)

        Does *not* retry on programmer errors (missing subagent markdown,
        misconfigured client) — those bubble up.
        """

        last_err: Exception | None = None
        prompt = invariant.as_prompt_payload()
        for attempt in range(1, self._max_retries + 2):
            try:
                assistant_text = await self._invoke_agent(invariant, prompt)
                json_str = extract_json_block(assistant_text)
                result = SourceMappingResult.model_validate_json(json_str)
                if result.phi_id != invariant.phi_id:
                    raise ValueError(
                        f"phi_id mismatch: agent returned {result.phi_id!r}, "
                        f"expected {invariant.phi_id!r}"
                    )
                return result
            except (OpenCodeInvocationError, ValidationError, ValueError) as e:
                last_err = e
                prompt = (
                    "The previous reply failed validation. Repair it using the error below, "
                    "then return exactly one fenced JSON block.\n"
                    f"Error: {type(e).__name__}: {e}\n"
                    f"Original task:\n{invariant.as_prompt_payload()}"
                )
                log.warning(
                    "source-mapper attempt %d/%d failed for %s: %s",
                    attempt,
                    self._max_retries + 1,
                    invariant.phi_id,
                    e,
                )
        assert last_err is not None
        raise SourceMappingError(
            f"source-mapper failed after {self._max_retries + 1} attempts "
            f"for {invariant.phi_id!r}: {last_err}"
        ) from last_err

    async def _invoke_agent(self, invariant: Invariant, prompt: str | None = None) -> str:
        invocation = OpenCodeInvocation(
            agent=self._AGENT_NAME,
            message=prompt or invariant.as_prompt_payload(),
            cwd=self._project_root,
            model=self._model,
            timeout_secs=self._timeout_secs,
        )
        return await self._client.run_agent(invocation)


async def map_invariant_to_source(
    invariant: Invariant,
    *,
    project_root: Path,
    client: OpenCodeClient | None = None,
    model: str | None = None,
    timeout_secs: float = 600.0,
) -> SourceMappingResult:
    """One-shot convenience wrapper.

    Usage::

        result = await map_invariant_to_source(
            Invariant(
                phi_id="SP-001",
                phi_text="For any function with a VLA...",
                mechanism="stack_canary",
                compiler="gcc",
                isa="x86_64",
                src_root="/opt/gcc-src",
            ),
            project_root=Path("/repo/de-fuzz"),
        )
        print(result.level_1_pass.name)
    """

    client = client or OpenCodeClient()
    runner = SourceMappingRunner(
        client,
        project_root=project_root,
        model=model,
        timeout_secs=timeout_secs,
    )
    return await runner.run(invariant)
