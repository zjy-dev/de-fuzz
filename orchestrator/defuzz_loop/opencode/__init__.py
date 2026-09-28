"""OpenCode headless CLI integration for DeFuzz.

This subpackage provides:

- :mod:`~defuzz_loop.opencode.client` — an async subprocess wrapper around
  ``opencode run --format json``. It knows how to spawn a headless OpenCode
  session in a specific working directory, feed it a message, harvest the
  final assistant text, and time out cleanly.
- :mod:`~defuzz_loop.opencode.schemas` — pydantic models for the JSON payloads
  we exchange with OpenCode subagents (currently only ``SourceMappingResult``).

Design contract:

- **No hidden state.** Each :class:`OpenCodeClient.run_agent` call spawns a
  fresh process; no server sharing, no session reuse. That makes each call
  independently replayable and safe to parallelise with
  :func:`asyncio.gather`.
- **JSON-only interface.** Subagents are told (via their markdown prompt) to
  emit exactly one fenced JSON block; the client extracts and returns just
  that string. Higher-level modules (:mod:`~defuzz_loop.source_mapping`)
  parse it into pydantic objects.
"""

from .client import OpenCodeClient, OpenCodeInvocation, OpenCodeInvocationError
from .schemas import (
    CriticalCondition,
    CriticalStruct,
    Invariant,
    PassAnchor,
    SourceMappingResult,
    TargetFunction,
)

__all__ = [
    "OpenCodeClient",
    "OpenCodeInvocation",
    "OpenCodeInvocationError",
    "Invariant",
    "PassAnchor",
    "TargetFunction",
    "CriticalStruct",
    "CriticalCondition",
    "SourceMappingResult",
]
