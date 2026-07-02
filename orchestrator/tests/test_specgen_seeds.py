"""specgen stage 0 — seed parsing from YAML-front-matter markdown.

Asserts the parser turns a DREV finding / historical bug doc into a Seed with
mechanism + free-text fields + harvested anchors, handles the missing-key edge
(no id / no mechanism → None), and that anchors capture INV ids + evidence
symbols (the exit-filter fuel) without inventing tokens.
"""

from __future__ import annotations

from pathlib import Path

from defuzz_loop.specgen.seeds import (
    load_seeds,
    parse_seed_file,
)

_DREV = """---
id: DREV-2026-999
toolchain: gcc
mechanism: fortify-source
invariant_violated: >
  INV-FORT-O02 — a size-carrying field must not be narrowed to int.
impact: >
  A signed count with bit 31 set truncates to int and over-reports the bound.
why_not_rescued: >
  The wrong size is folded at compile time and baked into the _chk bound.
evidence:
  - file: gcc/tree-object-size.cc
    symbol: access_with_size_object_size
    line: 886-897
related_invariants:
  - INV-FORT-O02
minimal_trigger:
  source: |
    struct S { long n; char arr[] __attribute__((counted_by(n))); };
tags: [counted_by, bdos, signed-truncation]
---
# body ignored
"""

_NO_MECH = """---
id: DREV-2026-000
toolchain: gcc
---
# no mechanism → not a seed
"""

_NOT_FRONT_MATTER = "# just a heading, no front matter\n"

# A real DREV had a flags scalar with an embedded quote+`or`, which is invalid
# YAML. One malformed seed must not abort the whole batch (parser returns None).
_MALFORMED_YAML = """---
id: DREV-2026-021
mechanism: bounds-safety
minimal_trigger:
  source: poc/trigger.cpp
  flags: "-std=gnu++11 -O2 -D_GLIBCXX_ASSERTIONS" or "-std=gnu++11 -O2 -fhardened"
---
# body
"""



def _write(tmp_path: Path, rel: str, text: str) -> Path:
    p = tmp_path / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(text, encoding="utf-8")
    return p


def test_parse_finding_populates_core_fields(tmp_path: Path) -> None:
    path = _write(tmp_path, "DREV-2026-999/README.md", _DREV)
    seed = parse_seed_file(path, source_kind="finding")
    assert seed is not None
    assert seed.seed_id == "DREV-2026-999"
    assert seed.origin_mechanism == "fortify-source"
    assert seed.source_kind == "finding"
    assert "narrowed to int" in seed.violated_invariant
    assert "over-reports" in seed.impact
    assert "counted_by" in seed.tags


def test_anchors_harvest_inv_ids_and_evidence_symbols(tmp_path: Path) -> None:
    path = _write(tmp_path, "DREV-2026-999/README.md", _DREV)
    seed = parse_seed_file(path, source_kind="finding")
    assert seed is not None
    # INV id from violated_invariant + related_invariants.
    assert "INV-FORT-O02" in seed.anchors
    # Evidence symbol becomes an anchor (exit-filter fuel).
    assert "access_with_size_object_size" in seed.anchors
    # minimal_trigger source contributes the smoking-gun attr symbol.
    assert any("counted_by" in a for a in seed.anchors)


def test_missing_mechanism_is_not_a_seed(tmp_path: Path) -> None:
    path = _write(tmp_path, "DREV-2026-000/README.md", _NO_MECH)
    assert parse_seed_file(path, source_kind="finding") is None


def test_no_front_matter_is_not_a_seed(tmp_path: Path) -> None:
    path = _write(tmp_path, "plain.md", _NOT_FRONT_MATTER)
    assert parse_seed_file(path, source_kind="historical-bug") is None


def test_malformed_yaml_is_skipped_not_raised(tmp_path: Path) -> None:
    # A malformed front matter returns None instead of raising, and a batch
    # load containing it still yields the well-formed seeds.
    bad = _write(tmp_path, "findings/DREV-2026-021/README.md", _MALFORMED_YAML)
    assert parse_seed_file(bad, source_kind="finding") is None

    _write(tmp_path, "findings/DREV-2026-999/README.md", _DREV)
    findings = tmp_path / "findings"
    seeds = load_seeds(["findings"], findings_root=findings, bugs_root=None)
    assert [s.seed_id for s in seeds] == ["DREV-2026-999"]


def test_load_seeds_selects_pools(tmp_path: Path) -> None:
    findings = tmp_path / "findings"
    _write(findings, "DREV-2026-999/README.md", _DREV)
    # A bug pool doc with valid front matter.
    bugs = tmp_path / "bugs"
    _write(bugs, "gcc/x/PR-1.md", _DREV.replace("DREV-2026-999", "PR-1"))

    only_findings = load_seeds(["findings"], findings_root=findings, bugs_root=bugs)
    assert [s.seed_id for s in only_findings] == ["DREV-2026-999"]

    both = load_seeds(["findings", "bugs"], findings_root=findings, bugs_root=bugs)
    assert {s.seed_id for s in both} == {"DREV-2026-999", "PR-1"}

    # An unrequested pool is not loaded even when the root exists.
    none = load_seeds([], findings_root=findings, bugs_root=bugs)
    assert none == []
