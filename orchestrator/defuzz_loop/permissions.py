"""Write-permission matrix enforcement at node exits (FR-008/022).

The blackboard is the only linkage channel between agents; to keep that channel
auditable each node may write only the fields it owns (blackboard-schema.md
§write-permission matrix). A node returning any key outside its allowance is a
contract violation and raises immediately — the test_blackboard.py guard.

Wrapping is transparent to sync and async node callables.
"""

from __future__ import annotations

import inspect
from collections.abc import Awaitable, Callable

# field → sole writer (blackboard-schema.md). Transient companions a writer must
# also set are grouped with their owner.
NODE_WRITE_PERMISSIONS: dict[str, frozenset[str]] = {
    "generator": frozenset({"corpus", "current_seed"}),
    "routing": frozenset({"build_matrix"}),
    "build": frozenset({"build_artifacts"}),
    "coverage": frozenset({"coverage"}),
    "oracle": frozenset({"verdict_history", "last_verdict", "pending_bug"}),
    "bump": frozenset({"round"}),
    "feedback": frozenset({"guidance"}),
    "minimizer": frozenset({"minimized_poc"}),
}


class WritePermissionError(RuntimeError):
    """Raised when a node writes a blackboard field it does not own."""


def _check(node: str, update: object) -> object:
    allowed = NODE_WRITE_PERMISSIONS.get(node)
    if allowed is None:
        raise WritePermissionError(f"unknown node '{node}' has no write permissions")
    if update is None:
        return update
    if not isinstance(update, dict):
        raise WritePermissionError(f"node '{node}' must return a dict update, got {type(update)}")
    violations = set(update) - allowed
    if violations:
        raise WritePermissionError(
            f"node '{node}' wrote unauthorized field(s) {sorted(violations)}; "
            f"allowed: {sorted(allowed)}"
        )
    return update


def guard(node: str, fn: Callable) -> Callable:
    """Wrap a node so its return is validated against the write matrix."""
    if inspect.iscoroutinefunction(fn):

        async def async_wrapped(bb) -> object:
            result: Awaitable = fn(bb)
            return _check(node, await result)

        return async_wrapped

    def wrapped(bb) -> object:
        return _check(node, fn(bb))

    return wrapped
