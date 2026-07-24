"""Embedding provider for the Phase-2 dense retriever (plan §"Retriever 接口化").

``doubao-embedding-vision`` is a fixed vector model — it cannot be reached
through Ark's "Auto" router or swapped in the console — so we call its
OpenAI-compatible ``{base_url}/embeddings`` endpoint directly. The endpoint takes
a batch of at most 10 string inputs per request and returns one 2048-dim vector
each. The API key is read from the env var named by ``api_key_env`` and never
stored in the repo.

This module is intentionally dependency-light (stdlib ``urllib`` only, like the
Bugzilla fetch in ``corpus.py``); vector math + disk caching live in
``EmbeddingRetriever``.
"""

from __future__ import annotations

import http.client
import json
import time
import urllib.error
import urllib.request
from collections.abc import Callable
from pathlib import Path
from typing import Any

import yaml
from pydantic import BaseModel

_DEFAULT_CONFIG_PATH = Path(__file__).resolve().parents[2] / "configs" / "embedding.yaml"

# The Ark embeddings endpoint rejects > 10 inputs per request.
_MAX_BATCH = 10
# Keep a single input bounded; the API tolerates long text but a runaway chunk
# wastes tokens. Matches generate.py's _MAX_CHUNK_CHARS budget order of magnitude.
_MAX_INPUT_CHARS = 24000


class EmbeddingConfig(BaseModel):
    model: str = "doubao-embedding-vision"
    base_url: str = "https://ark.cn-beijing.volces.com/api/coding/v3"
    api_key_env: str = "ARK_API_KEY"
    dim: int = 2048
    batch_size: int = 5
    timeout: int = 120

    @classmethod
    def load(cls, path: str | Path | None = None) -> EmbeddingConfig:
        cfg_path = Path(path) if path else _DEFAULT_CONFIG_PATH
        if not cfg_path.exists():
            raise FileNotFoundError(f"embedding config not found: {cfg_path}")
        data: dict[str, Any] = yaml.safe_load(cfg_path.read_text()) or {}
        return cls.model_validate(data.get("embedding", data))


class EmbeddingClient:
    """Batched calls to the Ark OpenAI-compatible embeddings endpoint."""

    def __init__(self, config: EmbeddingConfig, *, api_key: str) -> None:
        if not api_key:
            raise ValueError(
                f"no embedding API key: export ${config.api_key_env} before a live run"
            )
        self._cfg = config
        self._key = api_key
        self._url = config.base_url.rstrip("/") + "/embeddings"

    def _post(self, inputs: list[str]) -> list[list[float]]:
        payload = json.dumps({"model": self._cfg.model, "input": inputs}).encode()
        req = urllib.request.Request(
            self._url,
            data=payload,
            headers={
                "Authorization": f"Bearer {self._key}",
                "Content-Type": "application/json",
            },
        )
        last: Exception | None = None
        for attempt in range(4):
            try:
                with urllib.request.urlopen(req, timeout=self._cfg.timeout) as resp:  # noqa: S310
                    data = json.loads(resp.read().decode("utf-8"))
                rows = sorted(data["data"], key=lambda r: r.get("index", 0))
                return [r["embedding"] for r in rows]
            except (
                urllib.error.URLError,
                http.client.HTTPException,
                OSError,
                KeyError,
                json.JSONDecodeError,
            ) as exc:
                last = exc
                time.sleep(1.5 * (attempt + 1))
        raise RuntimeError(f"embedding request failed ({self._url}): {last}")

    def _embed_batch(self, batch: list[str]) -> list[list[float]]:
        """Embed one batch; on repeated failure, bisect down to single inputs.

        The Ark endpoint occasionally truncates a large response body
        (IncompleteRead). Splitting the batch shrinks the response until it
        arrives intact, so a whole run never dies on one flaky large batch.
        """
        try:
            return self._post(batch)
        except RuntimeError:
            if len(batch) == 1:
                raise
            mid = len(batch) // 2
            return self._embed_batch(batch[:mid]) + self._embed_batch(batch[mid:])

    def embed(
        self,
        texts: list[str],
        *,
        on_progress: Callable[[int, list[list[float]]], None] | None = None,
    ) -> list[list[float]]:
        """Embed a list of texts in <=batch_size requests.

        ``on_progress(start_index, vectors)`` fires after each batch so a caller
        can checkpoint partial results and resume after an interruption.
        """
        out: list[list[float]] = []
        bs = max(1, min(self._cfg.batch_size, _MAX_BATCH))
        for i in range(0, len(texts), bs):
            batch = [t[:_MAX_INPUT_CHARS] if t else " " for t in texts[i : i + bs]]
            vecs = self._embed_batch(batch)
            if on_progress is not None:
                on_progress(i, vecs)
            out.extend(vecs)
        return out
