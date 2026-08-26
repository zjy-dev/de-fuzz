"""Pluggable judgment backend for the specgen pipeline.

The pipeline has two kinds of stages:

- **deterministic** (seed parse, corpus build, BM25 retrieval, exit filter,
  falsifiability gate, dedup) — always run for real, no model needed.
- **judgment** (query distillation, analogy alignment, specialize, evidence
  entailment) — need a reasoning model.

``Judge`` abstracts the judgment stages behind one structured-completion call so
the pipeline never branches on "do we have an API key". Two implementations:

- ``LLMJudge`` — routes to the shared langchain chat model via ``build_chat_model``
  and ``with_structured_output`` (identical to the three runtime agents).
- ``TranscriptJudge`` — replays a JSON transcript keyed by ``(task, key)``. A
  missing key is recorded (with the fully rendered prompt) into ``pending`` and
  raised as ``PendingJudgment`` so the pipeline can skip that item and dump the
  authoring worklist. This is the offline path: the transcript is authored from
  the *real* deterministic artifacts (the distilled query, the actual BM25 hit
  text), never invented from thin air.

Both paths produce the same pydantic outputs, so the staging results are
identical in shape regardless of backend.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Protocol, TypeVar

from pydantic import BaseModel

from ..llm import LLMConfig, ainvoke_structured, build_chat_model

T = TypeVar("T", bound=BaseModel)

# The four judgment tasks. Keys are deterministic so a transcript is stable
# across runs: distill is keyed by seed_id, the per-hit tasks by seed_id:chunk_id.
TASK_DISTILL = "distill_query"
TASK_ANALOGY = "analogy"
TASK_SPECIALIZE = "specialize"
TASK_ENTAILMENT = "entailment"


class PendingJudgment(Exception):
    """Raised by TranscriptJudge when a (task, key) has no authored entry."""

    def __init__(self, task: str, key: str) -> None:
        super().__init__(f"no transcript entry for task={task} key={key}")
        self.task = task
        self.key = key


class Judge(Protocol):
    async def complete(
        self, *, task: str, key: str, system: str, user: str, output_model: type[T]
    ) -> T: ...


class LLMJudge:
    """Live judgment via the shared chat model + structured output."""

    def __init__(self, llm_config: LLMConfig | None = None) -> None:
        self._model = build_chat_model(llm_config)

    async def complete(
        self, *, task: str, key: str, system: str, user: str, output_model: type[T]
    ) -> T:
        return await ainvoke_structured(
            self._model,
            output_model,
            [("system", system), ("user", user)],
            stage=task,
            agent=key,
        )


class TranscriptJudge:
    """Replay authored judgments from a JSON transcript; record misses.

    Transcript layout::

        {"<task>": {"<key>": {<fields of the output model>}}}

    A miss appends a self-contained worklist item to ``pending`` (task, key, and
    the rendered system/user prompt + the target schema) and raises
    ``PendingJudgment`` so the caller can skip and later author the entry.
    """

    def __init__(self, path: Path) -> None:
        self._path = path
        self._data: dict[str, dict[str, dict]] = {}
        if path.exists():
            self._data = json.loads(path.read_text(encoding="utf-8"))
        self.pending: list[dict] = []

    async def complete(
        self, *, task: str, key: str, system: str, user: str, output_model: type[T]
    ) -> T:
        entry = self._data.get(task, {}).get(key)
        if entry is None:
            self.pending.append(
                {
                    "task": task,
                    "key": key,
                    "output_schema": output_model.model_json_schema(),
                    "system": system,
                    "user": user,
                }
            )
            raise PendingJudgment(task, key)
        return output_model.model_validate(entry)

    def dump_pending(self, out_path: Path) -> int:
        """Write the pending worklist for authoring; return its length."""
        out_path.write_text(
            json.dumps(self.pending, indent=2, ensure_ascii=False), encoding="utf-8"
        )
        return len(self.pending)


def build_judge(
    *, transcript: Path | None, llm_config: LLMConfig | None
) -> tuple[Judge, TranscriptJudge | None]:
    """Pick a backend.

    When ``transcript`` is given, use TranscriptJudge (offline replay/record).
    Otherwise use the live LLMJudge. Returns the judge plus the TranscriptJudge
    (or None) so the caller can dump the pending worklist afterwards.
    """
    if transcript is not None:
        tj = TranscriptJudge(transcript)
        return tj, tj
    return LLMJudge(llm_config), None
