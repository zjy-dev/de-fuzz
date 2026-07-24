"""Stage 0 — parse known defects into mechanism-tagged Seeds.

Three sources feed the seed pool:

- DREV findings   (``defend-reviewer-invariants/findings/DREV-*/README.md``)
- historical bugs (``defend-reviewer-invariants/docs/bugs/**/*.md``)
- survey invariants (``defend-reviewer-invariants/docs/invariants/*.md``)

The first two share YAML front-matter (``id`` / ``mechanism`` /
``invariant_violated`` / ``impact`` / ``why_not_rescued`` / ``evidence`` /
``tags``) so one parser (``_seed_from_data``) covers them. The survey invariants
use a different layout — ``### INV-...`` markdown blocks whose fields are
``- **statement**:`` / ``- **observation**:`` bullets, no front-matter — so they
get a dedicated parser (``parse_invariant_file``). Using an already-catalogued
invariant as a probe lets a *rule* (not just a bug) search sister mechanisms for
the same root-cause shape. The ``source_kind`` label records which pool a seed
came from.

Anchors — mechanism-specific symbols, attributes and invariant IDs harvested
from the seed — are the exit-filter fuel (a retrieval hit containing any of
them means the seed rediscovered itself). They are collected here, never fed to
retrieval. For invariant seeds this matters most: a rule's ``statement`` /
``source_url_or_path`` typically names the very GCC function it describes, and
that function is in the corpus, so the anchors are what stop the rule from
"finding itself".
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

# --- survey-invariant parsing ---------------------------------------------
# A survey invariant block header: ``### INV-SCP-B02 — AArch64 上的 probe 序列``.
_INV_HEADER = re.compile(r"^###\s+(INV-[A-Z0-9]+-[A-Z]+\d+)\s*[—-]\s*(.*)$")
# A ``- **field**: value`` bullet inside an invariant block.
_INV_FIELD = re.compile(r"^\s*-\s*\*\*([\w ]+?)\*\*\s*[:：]\s*(.*)$")
# An inline-code span (backticks delimit real code symbols/paths in the survey,
# never prose), used to harvest exit-filter anchors surgically.
_BACKTICK = re.compile(r"`([^`]+)`")
# A lone C identifier: keep only ones specific enough to be a safe anchor
# (contains ``_`` or is long) so a hit substring-match never nukes on a common
# word like "stack" / "probe" / "guard".
_IDENT = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")

# Map a survey filename stem to a mechanism label. Where the subject is also a
# corpus mechanism tag (fortify-source, stack-clash-protection, cet, ...) the
# label is aligned to that tag so the exit filter's "same mechanism = rediscover
# self" test fires; the rest keep a readable stem and rely on anchors.
_INV_FILE_MECHANISM: dict[str, str] = {
    "fortify-source": "fortify-source",
    "stack-clash-protection": "stack-clash-protection",
    "stack-canary": "stack-protector",
    "stack-check": "stack-protector",
    "shadow-stack": "shstk",
    "shadow-call-stack": "shadowcallstack",
    "pointer-authentication": "return-address-signing",
    "endbr-ibt": "cet",
    "strub": "strub",
    "bti": "bti",
}


def _mechanism_for_invariant_file(path: Path) -> str:
    """Mechanism label for a survey invariant file (aligned to corpus tags)."""
    stem = path.stem
    return _INV_FILE_MECHANISM.get(stem, stem)


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

    Surgical on purpose (same rule as ``_harvest_invariant_anchors``): a symbol
    token is kept only if it carries a ``_`` (so ``stack_protect_epilogue`` /
    ``FRAME_GROWS_DOWNWARD`` survive) and dropped otherwise. Evidence symbols in
    the DREV/bug docs are snake_case C identifiers, but their parentheticals
    ("(reference: fixed backend)") and minimal-trigger prose leak bare English
    words (``const``/``generic``/``fixed``/``reference``...); anchoring those
    would substring-nuke unrelated hits. INV ids are kept unconditionally.
    """
    anchors: set[str] = set()

    def _keep_symbol_tokens(text: str) -> None:
        for tok in _SYMBOL.findall(text):
            if "_" in tok:
                anchors.add(tok)

    ev_symbols, _ = _flatten_evidence(data.get("evidence"))
    for sym in ev_symbols:
        # An evidence "symbol" can be a phrase; keep identifier-shaped tokens.
        _keep_symbol_tokens(sym)

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
            _keep_symbol_tokens(src)

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
        origin_isas=[i.lower() for i in _as_list(data.get("isa"))],
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


def _harvest_invariant_anchors(inv_id: str, fields: dict[str, str]) -> list[str]:
    """Exit-filter fuel for an invariant seed (stop it rediscovering its own site).

    A survey invariant's ``statement`` / ``source_url_or_path`` / ``evidence`` /
    ``observation`` almost always name the exact GCC function or macro they
    describe, and that function is in the corpus. We harvest those names so the
    exit filter drops the seed's own site and only *sister* mechanisms survive.

    Surgical on purpose: only backtick-delimited spans are inspected (backticks
    mark real code in the survey, never prose) and only ``_``-bearing identifier
    tokens are kept. A bare English word like "stack" / "guard" / "probe" — which
    would substring-match half the corpus and nuke every hit — has no underscore
    and is never anchored, while ``aarch64_allocate_and_probe_stack_space`` is.
    """
    anchors: set[str] = {inv_id}
    hay = " ".join(fields.values())
    for span in _BACKTICK.findall(hay):
        for tok in _SYMBOL.findall(span):
            if "_" in tok:
                anchors.add(tok)
    for m in _INV_ID.findall(hay):
        anchors.add(m)
    return sorted(anchors)


def _iter_invariant_blocks(text: str):
    """Yield ``(inv_id, title, fields)`` for every ``### INV-...`` block in a file."""
    cur_id = ""
    cur_title = ""
    fields: dict[str, str] = {}
    for line in text.split("\n"):
        h = _INV_HEADER.match(line)
        if h:
            if cur_id:
                yield cur_id, cur_title, fields
            cur_id, cur_title = h.group(1), h.group(2).strip()
            fields = {}
            continue
        if cur_id:
            f = _INV_FIELD.match(line)
            if f:
                key = f.group(1).strip().lower().replace(" ", "_")
                fields[key] = f.group(2).strip()
    if cur_id:
        yield cur_id, cur_title, fields


def parse_invariant_file(path: Path) -> list[Seed]:
    """Parse one survey invariant markdown file into a list of Seeds.

    The rule's ``statement`` becomes ``violated_invariant`` (the property whose
    negation the distiller generalizes into a mechanism-agnostic root cause) and
    its ``observation`` becomes ``impact``. A block without a ``statement`` is
    skipped — there is nothing to distill.
    """
    try:
        text = path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return []
    mechanism = _mechanism_for_invariant_file(path)
    seeds: list[Seed] = []
    for inv_id, title, fields in _iter_invariant_blocks(text):
        statement = fields.get("statement", "").strip()
        if not statement:
            continue
        seeds.append(
            Seed(
                seed_id=inv_id,
                origin_mechanism=mechanism,
                source_kind="invariant",
                violated_invariant=statement,
                impact=fields.get("observation", "").strip(),
                why_not_rescued="",
                notes=title,
                anchors=_harvest_invariant_anchors(inv_id, fields),
                tags=[],
                source_path=f"{path}#{inv_id}",
            )
        )
    return seeds


def load_invariants(invariants_root: Path) -> list[Seed]:
    """Load survey invariants (``<root>/*.md`` ``### INV-...`` blocks) as seeds."""
    seeds: list[Seed] = []
    for md in sorted(invariants_root.glob("*.md")):
        if md.name in {"README.md", "gcc-llvm-defense-invariant-source-survey.md"}:
            continue
        if md.stem.startswith("cross-mechanism"):
            # Never seed on the pipeline's own generated output.
            continue
        seeds.extend(parse_invariant_file(md))
    return seeds


def load_seeds(
    sources: list[str],
    *,
    findings_root: Path | None = None,
    bugs_root: Path | None = None,
    invariants_root: Path | None = None,
) -> list[Seed]:
    """Load seeds from the requested pools (``findings`` / ``bugs`` / ``invariants``)."""
    seeds: list[Seed] = []
    if "findings" in sources and findings_root is not None:
        seeds.extend(load_findings(findings_root))
    if "bugs" in sources and bugs_root is not None:
        seeds.extend(load_bugs(bugs_root))
    if "invariants" in sources and invariants_root is not None:
        seeds.extend(load_invariants(invariants_root))
    return seeds
