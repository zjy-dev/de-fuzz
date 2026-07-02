"""Stage 0 — parse known defects into mechanism-tagged Seeds.

Three sources, all with YAML front-matter delimited by ``---``:

- DREV findings   (``defend-reviewer-invariants/findings/DREV-*/README.md``)
- historical bugs (``defend-reviewer-invariants/docs/bugs/**/*.md``)

Both share enough keys (``id`` / ``mechanism`` / ``invariant_violated`` /
``impact`` / ``why_not_rescued`` / ``evidence`` / ``tags``) that one parser
covers them; the ``source_kind`` label records which pool a seed came from.

Anchors — mechanism-specific symbols, attributes and invariant IDs harvested
from the seed — are the exit-filter fuel (a retrieval hit containing any of
them means the seed rediscovered itself). They are collected here, never fed to
retrieval.
"""

from __future__ import annotations

import re
from pathlib import Path
from typing import Any

import yaml

from .schema import Seed

# An INV-<MECH>-<CAT><NN> identifier, e.g. INV-FORT-O02 / INV-SP-L01.
_INV_ID = re.compile(r"\bINV-[A-Z0-9]+-[A-Z]+\d+\b")
# A C identifier long enough to be a real symbol (drops `n`, `p`, `if`, ...).
_SYMBOL = re.compile(r"\b[A-Za-z_][A-Za-z0-9_]{4,}\b")


def _front_matter(text: str) -> dict[str, Any] | None:
    """Extract and parse the leading ``---``-delimited YAML block, if any."""
    if not text.startswith("---"):
        return None
    end = text.find("\n---", 3)
    if end == -1:
        return None
    block = text[3:end]
    # A malformed front matter in one seed must not abort the whole batch;
    # skip it (returns None) rather than propagate the YAML parse error.
    try:
        data = yaml.safe_load(block)
    except yaml.YAMLError:
        return None
    return data if isinstance(data, dict) else None


def _flatten_evidence(evidence: Any) -> tuple[list[str], list[str]]:
    """Return (symbols, files) harvested from an ``evidence`` list."""
    symbols: list[str] = []
    files: list[str] = []
    if not isinstance(evidence, list):
        return symbols, files
    for item in evidence:
        if not isinstance(item, dict):
            continue
        sym = item.get("symbol")
        if isinstance(sym, str) and sym.strip():
            symbols.append(sym.strip())
        fpath = item.get("file")
        if isinstance(fpath, str) and fpath.strip():
            files.append(fpath.strip())
    return symbols, files


def _as_list(value: Any) -> list[str]:
    if isinstance(value, list):
        return [str(v).strip() for v in value if str(v).strip()]
    if isinstance(value, str) and value.strip():
        return [value.strip()]
    return []


def _harvest_anchors(data: dict[str, Any]) -> list[str]:
    """Collect mechanism-specific symbols / attrs / INV IDs (exit-filter fuel).

    These are the tokens whose appearance in a retrieval hit means "you found
    yourself again". We pull evidence symbols, every INV-* id referenced, and
    the more specific identifier-shaped symbols named in the free-text fields.
    """
    anchors: set[str] = set()

    ev_symbols, _ = _flatten_evidence(data.get("evidence"))
    for sym in ev_symbols:
        # An evidence "symbol" can be a phrase; keep identifier-shaped tokens.
        for tok in _SYMBOL.findall(sym):
            anchors.add(tok)

    # INV ids from the violated invariant, related invariants, and free text.
    hay = " ".join(
        str(data.get(k, ""))
        for k in ("invariant_violated", "impact", "why_not_rescued", "notes")
    )
    for m in _INV_ID.findall(hay):
        anchors.add(m)
    for inv in _as_list(data.get("related_invariants")):
        for m in _INV_ID.findall(inv):
            anchors.add(m)

    # A minimal_trigger source block carries the smoking-gun symbols/attrs.
    trigger = data.get("minimal_trigger")
    if isinstance(trigger, dict):
        src = trigger.get("source")
        if isinstance(src, str):
            for tok in _SYMBOL.findall(src):
                anchors.add(tok)

    return sorted(anchors)


def _seed_from_data(data: dict[str, Any], source_kind: str, source_path: Path) -> Seed | None:
    seed_id = data.get("id")
    mechanism = data.get("mechanism")
    if not isinstance(seed_id, str) or not isinstance(mechanism, str):
        return None

    def _text(key: str) -> str:
        val = data.get(key, "")
        return val.strip() if isinstance(val, str) else ""

    return Seed(
        seed_id=seed_id.strip(),
        origin_mechanism=mechanism.strip(),
        source_kind=source_kind,
        violated_invariant=_text("invariant_violated"),
        impact=_text("impact"),
        why_not_rescued=_text("why_not_rescued"),
        notes=_text("notes") or _text("falsification_pattern"),
        anchors=_harvest_anchors(data),
        tags=_as_list(data.get("tags")),
        source_path=str(source_path),
    )


def parse_seed_file(path: Path, source_kind: str) -> Seed | None:
    """Parse one YAML-front-matter markdown file into a Seed (or None)."""
    try:
        text = path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return None
    data = _front_matter(text)
    if data is None:
        return None
    return _seed_from_data(data, source_kind, path)


def load_findings(findings_root: Path) -> list[Seed]:
    """Load DREV findings (``<root>/DREV-*/README.md``)."""
    seeds: list[Seed] = []
    for readme in sorted(findings_root.glob("DREV-*/README.md")):
        seed = parse_seed_file(readme, source_kind="finding")
        if seed is not None:
            seeds.append(seed)
    return seeds


def load_bugs(bugs_root: Path) -> list[Seed]:
    """Load historical bug disclosures (``<root>/**/*.md``)."""
    seeds: list[Seed] = []
    for md in sorted(bugs_root.rglob("*.md")):
        seed = parse_seed_file(md, source_kind="historical-bug")
        if seed is not None:
            seeds.append(seed)
    return seeds


def load_seeds(
    sources: list[str],
    *,
    findings_root: Path | None = None,
    bugs_root: Path | None = None,
) -> list[Seed]:
    """Load seeds from the requested pools (``findings`` / ``bugs``)."""
    seeds: list[Seed] = []
    if "findings" in sources and findings_root is not None:
        seeds.extend(load_findings(findings_root))
    if "bugs" in sources and bugs_root is not None:
        seeds.extend(load_bugs(bugs_root))
    return seeds
