from __future__ import annotations

import os
from pathlib import Path

import pytest
from pydantic import ValidationError

from defuzz_loop.audit_schema import AuditCandidate
from defuzz_loop.parity import (
    aggregate_findings,
    evaluate_demo_parity,
    normalize_mechanism,
    parity_metrics,
    parse_demo_findings,
)

_REAL_DEMO_ROOT = Path(
    os.environ.get(
        "DEFUZZ_REFERENCE_ROOT",
        "/Users/bytedance/projects/research/defend-reviewer/main",
    )
)


def _write_finding(root: Path, name: str, frontmatter: str) -> Path:
    path = root / "findings" / name / "README.md"
    path.parent.mkdir(parents=True)
    path.write_text(f"---\n{frontmatter}\n---\n# body\n", encoding="utf-8")
    return path


def _candidate(**updates: object) -> AuditCandidate:
    payload: dict[str, object] = {
        "toolchain": "clang",
        "toolchain_version": "llvmorg-22.1.4",
        "mechanism": "stack-canary",
        "isa": ["x86_64"],
        "invariant_violated": "The canary check must remain before every return!",
        "root_cause": "The canary check must remain before every return!",
        "evidence_file_line": ["llvm/lib/X.cpp:9"],
        "evidence_code": "1\n2\n3\n4\n5",
        "minimal_trigger": {"source": "x", "flags": "-O2", "isa": "x86_64"},
        "impact": "A check is silently skipped.",
        "why_not_rescued": "No later pass inserts it.",
        "poc_verified": True,
        "discovered": "2026-08-26",
    }
    payload.update(updates)
    return AuditCandidate.model_validate(payload)


def test_parser_recovers_malformed_yaml_and_emits_explicit_issues(tmp_path: Path) -> None:
    malformed = _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: llvm
toolchain_version: llvmorg-22.1.4
mechanism: stack-protector
invariant_violated: >
  The canary check must remain before every return.
status: draft
poc_verified: false
flags: -O2 note: this intentionally breaks YAML""",
    )
    before = malformed.read_bytes()

    parsed = parse_demo_findings(tmp_path)

    assert len(parsed.findings) == 1
    assert parsed.findings[0].id == "DREV-2026-001"
    assert any("malformed YAML" in issue.message for issue in parsed.issues)
    assert malformed.read_bytes() == before


def test_aggregate_and_parity_use_normalized_toolchain_mechanism_and_root(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: llvm
toolchain_version: llvmorg-22.1.4
mechanism: stack-protector
invariant_violated: The canary check must remain before every return.
status: draft
poc_verified: true""",
    )
    _write_finding(
        tmp_path,
        "DREV-2026-002",
        """id: DREV-2026-002
toolchain: gcc
toolchain_version: gcc-17
mechanism: cet-ibt
invariant_violated: Every indirect target has an ENDBR landing pad.
poc_verified: false""",
    )
    parsed = parse_demo_findings(tmp_path)
    aggregates = aggregate_findings(parsed)
    parity = evaluate_demo_parity([_candidate()], parsed)

    assert aggregates.total == 2
    assert aggregates.by_toolchain == {"gcc": 1, "llvm": 1}
    assert aggregates.by_mechanism == {"ibt": 1, "stack-protector": 1}
    assert aggregates.by_status == {"draft": 1, "missing": 1}
    assert any("missing required fields: status" in issue.message for issue in parsed.issues)
    assert normalize_mechanism("CET / IBT") == "ibt"
    assert parity.matched_count == 1
    assert parity.matches[0].demo_id == "DREV-2026-001"
    assert parity.missing_demo_ids == ["DREV-2026-002"]
    assert parity.precision == 1.0
    assert parity.recall == 0.5


def test_parity_report_serializes_metrics_and_non_blocking_threshold(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: llvm
mechanism: stack-protector
invariant_violated: The canary check must remain before every return.
status: confirmed
poc_verified: true""",
    )
    _write_finding(
        tmp_path,
        "DREV-2026-002",
        """id: DREV-2026-002
toolchain: gcc
mechanism: ibt
invariant_violated: Every indirect target has an ENDBR landing pad.
poc_verified: true""",
    )

    parity = evaluate_demo_parity([_candidate()], tmp_path, threshold=0.8)
    payload = parity.model_dump(mode="json")

    assert payload["match_count"] == 1
    assert payload["precision"] == 1.0
    assert payload["recall"] == 0.5
    assert payload["f1"] == pytest.approx(2 / 3)
    assert payload["threshold"] == 0.8
    assert payload["threshold_pass"] is False
    assert len(payload["parse_issues"]) == 1
    assert "missing required fields: status" in payload["parse_issues"][0]["message"]
    assert parity_metrics(parity) == {
        "precision": 1.0,
        "recall": 0.5,
        "f1": pytest.approx(2 / 3),
        "match_count": 1,
        "threshold": 0.8,
        "threshold_pass": False,
    }

    without_threshold = evaluate_demo_parity([_candidate()], tmp_path)
    assert without_threshold.threshold is None
    assert without_threshold.threshold_pass is None


def test_parity_threshold_must_be_a_ratio(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="less than or equal to 1"):
        evaluate_demo_parity([], tmp_path, threshold=1.01)


@pytest.mark.skipif(
    not (_REAL_DEMO_ROOT / "findings").is_dir(),
    reason="real defend-reviewer demo corpus is not available",
)
def test_real_demo_corpus_parser_smoke_is_read_only() -> None:
    readmes = sorted((_REAL_DEMO_ROOT / "findings").glob("DREV-*/README.md"))
    before = {path: path.stat().st_mtime_ns for path in readmes}

    parsed = parse_demo_findings(_REAL_DEMO_ROOT)

    assert len(parsed.findings) == 30
    assert len(parsed.issues) == 2
    assert sum("malformed YAML" in issue.message for issue in parsed.issues) == 1
    assert sum("missing required fields" in issue.message for issue in parsed.issues) == 1
    assert {path: path.stat().st_mtime_ns for path in readmes} == before
