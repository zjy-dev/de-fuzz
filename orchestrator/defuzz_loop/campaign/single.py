"""Single-invariant campaign — Rev 3 replacement for the LangGraph flavour.

One invariant → one campaign → one final :class:`Blackboard`. The
:class:`IterationLoop` is built with ``target_queue = [phi_id]`` so the
scheduler always targets ``bb.target_queue[0]``. The campaign terminates
when:

- the oracle promotes the aggregate to ``violated``, OR
- ``budget_rounds`` OR ``budget_secs`` is exhausted (whichever first).

Dependencies are injected as a **loop factory** — a zero-arg callable
returning a fully-wired :class:`IterationLoop`. This keeps the public
entry point testable with fakes and lets the CLI and the parallel
launcher share code.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:  # pragma: no cover — typing only
    from ..loop import IterationLoop
    from ..state import Blackboard


@dataclass(frozen=True)
class CampaignConfig:
    """Everything :func:`run_single_invariant_campaign` needs.

    ``blackboard_extra`` is an optional mapping of extra kwargs to feed
    into the :class:`Blackboard` constructor (e.g. custom ablation flags).
    """

    phi_id: str
    mechanism: str
    budget_rounds: int
    budget_secs: float = 0.0
    thread_id: str | None = None
    recursion_limit: int | None = None
    blackboard_extra: dict[str, Any] = field(default_factory=dict)

    def resolved_thread_id(self) -> str:
        return self.thread_id or f"campaign::{self.mechanism}::{self.phi_id}"

    def resolved_recursion_limit(self) -> int:
        """Historical field kept for backwards compatibility with the CLI.

        Rev 3 no longer uses LangGraph so the recursion limit has no
        runtime effect, but the CLI still logs it for parity with older
        run manifests.
        """

        if self.recursion_limit is not None:
            return self.recursion_limit
        return 8 * max(1, self.budget_rounds) + 10


@dataclass(frozen=True)
class CampaignResult:
    """Outcome of a single-invariant campaign."""

    phi_id: str
    final_blackboard: Blackboard
    ok: bool = True
    error: BaseException | None = None
    duration_secs: float = 0.0


LoopFactory = Callable[[], "IterationLoop"]
"""Zero-arg callable returning a wired :class:`IterationLoop`."""


BlackboardFactory = Callable[..., Any]
"""Callable producing an initial :class:`Blackboard` for a given
``target_queue`` and other run parameters. Matches the ``Blackboard``
pydantic constructor."""


async def run_single_invariant_campaign(
    config: CampaignConfig,
    *,
    loop_builder: LoopFactory,
    blackboard_factory: BlackboardFactory,
) -> CampaignResult:
    """Build one :class:`IterationLoop` and drive it to termination.

    The returned :class:`CampaignResult` carries the final blackboard
    (already a :class:`Blackboard` instance — no LangGraph channel-dict
    reconstruction step needed).
    """

    import time

    loop = loop_builder()
    initial = blackboard_factory(
        target_queue=[config.phi_id],
        **config.blackboard_extra,
    )

    started = time.monotonic()
    try:
        final = await loop.run(
            initial,
            budget_rounds=config.budget_rounds,
            budget_secs=config.budget_secs,
        )
    except BaseException as e:  # noqa: BLE001 — record and re-raise via CampaignResult
        return CampaignResult(
            phi_id=config.phi_id,
            final_blackboard=initial,
            ok=False,
            error=e,
            duration_secs=time.monotonic() - started,
        )
    return CampaignResult(
        phi_id=config.phi_id,
        final_blackboard=final,
        ok=True,
        duration_secs=time.monotonic() - started,
    )


__all__ = [
    "CampaignConfig",
    "CampaignResult",
    "run_single_invariant_campaign",
    "LoopFactory",
    "BlackboardFactory",
]
