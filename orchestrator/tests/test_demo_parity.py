from __future__ import annotations

import os
from pathlib import Path

import pytest
from pydantic import ValidationError

from defuzz_loop.audit_schema import AuditCandidate, families_for_mechanisms
from defuzz_loop.parity import (
    BenchmarkPolicy,
    DemoFinding,
    ParityScope,
    aggregate_findings,
    evaluate_demo_parity,
    normalize_mechanism,
    normalize_toolchain_version,
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


def test_parser_marks_schema_type_errors_without_aborting_corpus(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: 1
toolchain: [gcc]
mechanism: stack-protector
invariant_violated: A concrete invariant.
status: draft
checker_ids: 7
poc_verified: false""",
    )

    parsed = parse_demo_findings(tmp_path)
    parity = evaluate_demo_parity([], parsed)

    assert len(parsed.findings) == 1
    assert parsed.findings[0].schema_valid is False
    assert any("schema validation failed" in issue.message for issue in parsed.issues)
    assert parity.raw_total == 1
    assert parity.benchmark_total == 0
    assert parity.excluded_ids == ["1"]


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
    parity = evaluate_demo_parity(
        [_candidate()],
        parsed,
        benchmark_policy=BenchmarkPolicy(include_schema_invalid=True),
    )

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

    parity = evaluate_demo_parity(
        [_candidate()],
        tmp_path,
        threshold=0.8,
        threshold_metric="f1",
        benchmark_policy=BenchmarkPolicy(include_schema_invalid=True),
    )
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
        "superset_coverage": 0.5,
        "match_count": 1,
        "raw_total": 2,
        "profile_total": 2,
        "scoped_total": 2,
        "benchmark_total": 2,
        "profile": "custom",
        "scope_status": "not_requested",
        "empty_scope": False,
        "not_applicable": False,
        "threshold": 0.8,
        "threshold_metric": "f1",
        "threshold_blocking": False,
        "threshold_pass": False,
        "threshold_blocked": False,
    }

    without_threshold = evaluate_demo_parity([_candidate()], tmp_path)
    assert without_threshold.threshold is None
    assert without_threshold.threshold_pass is None
    assert without_threshold.threshold_metric == "recall"
    assert without_threshold.superset_coverage == without_threshold.recall


def test_parity_threshold_must_be_a_ratio(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="less than or equal to 1"):
        evaluate_demo_parity([], tmp_path, threshold=1.01)


def test_default_benchmark_policy_excludes_retracted_and_schema_invalid(
    tmp_path: Path,
) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: llvm
mechanism: stack-protector
invariant_violated: The canary remains before every return.
status: confirmed
poc_verified: true""",
    )
    _write_finding(
        tmp_path,
        "DREV-2026-002",
        """id: DREV-2026-002
toolchain: llvm
mechanism: auto-var-init
invariant_violated: Padding is initialized.
status: retracted
poc_verified: false""",
    )
    _write_finding(
        tmp_path,
        "DREV-2026-003",
        """id: DREV-2026-003
toolchain: gcc
mechanism: stack-clash-protection
invariant_violated: Every guard page is probed.
poc_verified: false""",
    )

    default = evaluate_demo_parity([], tmp_path)
    all_records = evaluate_demo_parity(
        [],
        tmp_path,
        benchmark_policy=BenchmarkPolicy(
            include_retracted=True, include_schema_invalid=True
        ),
    )

    assert default.raw_total == 3
    assert default.benchmark_total == 1
    assert default.excluded_ids == ["DREV-2026-002", "DREV-2026-003"]
    assert default.missing_demo_ids == ["DREV-2026-001"]
    assert all_records.benchmark_total == 3
    assert all_records.excluded_ids == []


def test_parity_scope_dimensions_are_or_within_and_across(tmp_path: Path) -> None:
    records = (
        ("001", "gcc", "gcc-17-20260531", "stack-protector", "x86_64"),
        ("002", "gcc", "gcc-17 (20260531 snapshot)", "ibt", "aarch64"),
        ("003", "llvm", "llvmorg-22.1.4", "stack-protector", "x86_64"),
        ("004", "gcc", "gcc-16.2.0", "stack-protector", "x86_64"),
    )
    for suffix, toolchain, version, mechanism, isa in records:
        _write_finding(
            tmp_path,
            f"DREV-2026-{suffix}",
            f"""id: DREV-2026-{suffix}
toolchain: {toolchain}
toolchain_version: {version}
mechanism: {mechanism}
isa: [{isa}]
invariant_violated: Scope fixture {suffix}.
status: draft
poc_verified: true""",
        )

    result = evaluate_demo_parity(
        [],
        tmp_path,
        scope=ParityScope(
            toolchains=("gnu-gcc", "llvm"),
            version=("gcc-17.0.0-20260531", "gcc-16"),
            mechanisms=("stack-canary", "cet-ibt"),
            isas=("x86-64", "arm64"),
        ),
    )

    assert result.raw_total == 4
    assert result.profile_total == 4
    assert result.scoped_total == 3
    assert result.benchmark_total == 3
    assert result.scope_status == "applicable"
    assert result.scope_report.selected_demo_ids == [
        "DREV-2026-001",
        "DREV-2026-002",
        "DREV-2026-004",
    ]
    assert result.missing_demo_ids == [
        "DREV-2026-001",
        "DREV-2026-002",
        "DREV-2026-004",
    ]
    assert result.demo_aggregates.total == 4  # Compatibility: profile aggregate.
    assert result.scoped_aggregates.total == 3


def test_parity_scope_demo_ids_are_exact_and_exclusive(tmp_path: Path) -> None:
    for suffix in ("001", "002"):
        _write_finding(
            tmp_path,
            f"DREV-2026-{suffix}",
            f"""id: DREV-2026-{suffix}
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: [x86_64]
invariant_violated: Exact ID fixture {suffix}.
status: draft
poc_verified: true""",
        )

    result = evaluate_demo_parity(
        [], tmp_path, scope={"demo_ids": ["DREV-2026-002", "DREV-4040-999"]}
    )

    assert result.scoped_total == 1
    assert result.scope_report.selected_demo_ids == ["DREV-2026-002"]
    assert result.scope_report.unresolved_demo_ids == ["DREV-4040-999"]
    with pytest.raises(ValidationError, match="demo_ids cannot be combined"):
        ParityScope(demo_ids=("DREV-2026-001",), toolchains=("gcc",))
    with pytest.raises(ValidationError, match="requires demo_ids"):
        ParityScope()


def test_profile_precedes_scope_and_empty_scope_is_not_applicable(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: [x86_64]
invariant_violated: Retracted fixture.
status: retracted
poc_verified: true""",
    )

    result = evaluate_demo_parity(
        [],
        tmp_path,
        scope={"demo_ids": ["DREV-2026-001"]},
        threshold=0.5,
        threshold_blocking=True,
    )

    assert result.raw_total == 1
    assert result.profile_total == 0
    assert result.scoped_total == 0
    assert result.scope_status == "not_applicable"
    assert result.empty_scope is True
    assert result.not_applicable is True
    assert result.threshold_pass is None
    assert result.threshold_blocked is False
    assert result.superset_coverage == 0.0
    assert result.missing_demo_ids == []
    metrics = parity_metrics(result)
    assert metrics["not_applicable"] is True
    assert metrics["threshold_pass"] is None


def test_empty_dimension_scope_is_not_applicable_without_threshold_failure(
    tmp_path: Path,
) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: [x86_64]
invariant_violated: Concrete scoped finding.
status: draft
poc_verified: true""",
    )

    result = evaluate_demo_parity(
        [],
        tmp_path,
        scope={"toolchains": ["llvm"]},
        threshold=1.0,
        threshold_blocking=True,
    )

    assert result.profile_total == 1
    assert result.scoped_total == 0
    assert result.scope_status == "not_applicable"
    assert result.threshold_pass is None
    assert result.threshold_blocked is False


def test_no_scope_preserves_profile_denominator(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: [x86_64]
invariant_violated: Unscoped compatibility fixture.
status: draft
poc_verified: true""",
    )

    result = evaluate_demo_parity([], tmp_path)

    assert result.scope is None
    assert result.scope_status == "not_requested"
    assert result.not_applicable is False
    assert result.profile_total == result.scoped_total == result.benchmark_total == 1


def test_generic_isa_matches_any_concrete_lane_but_missing_isa_does_not(
    tmp_path: Path,
) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: [generic]
invariant_violated: Generic fixture.
status: draft
poc_verified: true""",
    )
    _write_finding(
        tmp_path,
        "DREV-2026-002",
        """id: DREV-2026-002
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
invariant_violated: Missing ISA fixture.
status: draft
poc_verified: true""",
    )

    result = evaluate_demo_parity([], tmp_path, scope={"isas": ["aarch64"]})

    assert result.scope_report.selected_demo_ids == ["DREV-2026-001"]


def test_scalar_demo_isa_is_normalized_for_scope(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: amd64
invariant_violated: Scalar ISA fixture.
status: draft
poc_verified: true""",
    )

    result = evaluate_demo_parity([], tmp_path, scope={"isas": ["x86_64"]})

    assert result.scope_report.selected_demo_ids == ["DREV-2026-001"]


@pytest.mark.parametrize(
    "value",
    (
        "gcc-17-20260531",
        "gcc-17 (20260531 snapshot)",
        "gcc-17.0.0-20260531",
    ),
)
def test_gcc17_version_spellings_share_one_scope_key(value: str) -> None:
    assert normalize_toolchain_version(value, toolchain="gcc") == "gcc-17"


def test_profiles_report_raw_statuses_and_exclusion_reasons(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
toolchain: llvm
mechanism: stack-protector
invariant_violated: Verified draft.
status: draft
poc_verified: true""",
    )
    _write_finding(
        tmp_path,
        "DREV-2026-002",
        """id: DREV-2026-002
toolchain: gcc
mechanism: ibt
invariant_violated: Unverified draft.
status: draft
poc_verified: false""",
    )
    _write_finding(
        tmp_path,
        "DREV-2026-003",
        """id: DREV-2026-003
toolchain: gcc
mechanism: ibt
invariant_violated: Retracted record.
status: retracted
poc_verified: false""",
    )

    workset = evaluate_demo_parity([], tmp_path)
    verified = evaluate_demo_parity([], tmp_path, profile="poc-verified")

    assert workset.profile == "demo-workset"
    assert workset.raw_corpus_aggregates.by_status == {"draft": 2, "retracted": 1}
    assert workset.profile_aggregates.by_status == {"draft": 2}
    assert workset.exclusion_reasons == {
        "schema_invalid": [],
        "retracted": ["DREV-2026-003"],
        "poc_not_verified": [],
    }
    assert verified.profile == "poc-verified"
    assert "not a formal paper result" in verified.profile_description
    assert verified.benchmark_total == 1
    assert verified.profile_aggregates.by_status == {"draft": 1}
    assert verified.exclusion_reasons["poc_not_verified"] == [
        "DREV-2026-002",
        "DREV-2026-003",
    ]
    retracted = next(
        item for item in verified.exclusions if item.finding_id == "DREV-2026-003"
    )
    assert retracted.reasons == ["retracted", "poc_not_verified"]
    payload = verified.model_dump(mode="json")
    assert payload["raw_corpus_aggregates"]["by_status"] == {
        "draft": 2,
        "retracted": 1,
    }
    assert payload["exclusions"][0]["finding_id"] == "DREV-2026-002"
    assert payload["exclusions"][0]["reasons"] == ["poc_not_verified"]
    assert payload["profile_report"] == {
        "name": "poc-verified",
        "description": verified.profile_description,
        "aggregates": verified.profile_aggregates.model_dump(mode="json"),
    }


def test_layered_keys_precede_exact_and_token_set_matching(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
finding_key: stable-stack-exit
checker_ids: [INV-SP-G01]
toolchain: llvm
mechanism: stack-protector
invariant_violated: Every protected exit executes a canary comparison.
status: confirmed
poc_verified: true""",
    )
    _write_finding(
        tmp_path,
        "DREV-2026-002",
        """id: DREV-2026-002
toolchain: llvm
mechanism: stack-protector
invariant_violated: Every protected function exit executes the canary comparison.
status: confirmed
poc_verified: true""",
    )
    candidates = [
        _candidate(
            finding_key="stable-stack-exit",
            checker_ids=["INV-OTHER"],
            invariant_violated="Completely different phrasing.",
            root_cause="Completely different phrasing.",
        ),
        _candidate(
            invariant_violated="Every protected exit executes the canary comparison.",
            root_cause="Every protected exit executes the canary comparison.",
        ),
    ]

    result = evaluate_demo_parity(candidates, tmp_path, token_similarity_threshold=0.6)

    assert [(match.demo_id, match.match_method) for match in result.matches] == [
        ("DREV-2026-001", "finding_key"),
        ("DREV-2026-002", "token_set"),
    ]
    assert result.matches[0].score == 1.0
    assert 0.6 <= result.matches[1].score < 1.0


def test_stable_key_cannot_match_a_wrong_toolchain_or_mechanism(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
finding_key: globally-stable-key
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: [x86_64]
invariant_violated: GCC stack-protector invariant.
status: draft
poc_verified: true""",
    )
    wrong_lane = _candidate(
        finding_key="globally-stable-key",
        toolchain="llvm",
        mechanism="ibt",
        invariant_violated="Unrelated LLVM IBT invariant.",
        root_cause="Unrelated LLVM IBT invariant.",
    )

    result = evaluate_demo_parity([wrong_lane], tmp_path)

    assert result.matches == []
    assert result.missing_demo_ids == ["DREV-2026-001"]
    assert result.unmatched_candidate_indices == [0]


@pytest.mark.parametrize(
    ("toolchain_version", "isa"),
    [
        ("gcc-16", ["x86_64"]),
        ("gcc-17", ["aarch64"]),
    ],
)
def test_stable_key_cannot_cross_version_or_isa_lanes(
    tmp_path: Path, toolchain_version: str, isa: list[str]
) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
finding_key: globally-stable-key
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: [x86_64]
invariant_violated: GCC stack-protector invariant.
status: draft
poc_verified: true""",
    )
    wrong_lane = _candidate(
        finding_key="globally-stable-key",
        toolchain="gcc",
        toolchain_version=toolchain_version,
        mechanism="stack-protector",
        isa=isa,
        invariant_violated="GCC stack-protector invariant.",
        root_cause="GCC stack-protector invariant.",
    )

    result = evaluate_demo_parity([wrong_lane], tmp_path)

    assert result.matches == []
    assert result.missing_demo_ids == ["DREV-2026-001"]
    assert result.unmatched_candidate_indices == [0]


def test_generic_isa_is_compatible_across_concrete_isa_lane(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
finding_key: globally-stable-key
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: [generic]
invariant_violated: GCC stack-protector invariant.
status: draft
poc_verified: true""",
    )
    candidate = _candidate(
        finding_key="globally-stable-key",
        toolchain="gcc",
        toolchain_version="gcc-17.0.0-20260531",
        mechanism="stack-protector",
        isa=["x86_64"],
        invariant_violated="GCC stack-protector invariant.",
        root_cause="GCC stack-protector invariant.",
    )

    result = evaluate_demo_parity([candidate], tmp_path)

    assert [(match.demo_id, match.match_method) for match in result.matches] == [
        ("DREV-2026-001", "finding_key")
    ]


def test_x86_family_scope_and_candidate_match_x86_64_demo(tmp_path: Path) -> None:
    _write_finding(
        tmp_path,
        "DREV-2026-001",
        """id: DREV-2026-001
finding_key: globally-stable-key
toolchain: gcc
toolchain_version: gcc-17
mechanism: stack-protector
isa: [x86_64]
invariant_violated: GCC stack-protector invariant.
status: draft
poc_verified: true""",
    )
    candidate = _candidate(
        finding_key="globally-stable-key",
        toolchain="gcc",
        toolchain_version="gcc-17",
        mechanism="stack-protector",
        isa=["x86"],
        invariant_violated="GCC stack-protector invariant.",
        root_cause="GCC stack-protector invariant.",
    )

    result = evaluate_demo_parity(
        [candidate],
        tmp_path,
        scope={
            "toolchains": ["gcc"],
            "version": ["gcc-17"],
            "mechanisms": ["stack-protector"],
            "isas": ["x86"],
        },
    )

    assert result.scoped_total == 1
    assert [(match.demo_id, match.match_method) for match in result.matches] == [
        ("DREV-2026-001", "finding_key")
    ]


def test_duplicate_demo_ids_do_not_mark_unmatched_records_as_matched() -> None:
    demos = [
        DemoFinding(
            id="DREV-DUPLICATE",
            toolchain="llvm",
            toolchain_version="llvm-22",
            mechanism="stack-protector",
            isa=["x86_64"],
            invariant_violated="The first unique invariant.",
            status="draft",
            poc_verified=True,
        ),
        DemoFinding(
            id="DREV-DUPLICATE",
            toolchain="llvm",
            toolchain_version="llvm-22",
            mechanism="stack-protector",
            isa=["x86_64"],
            invariant_violated="The second distinct invariant.",
            status="draft",
            poc_verified=True,
        ),
    ]
    candidate = _candidate(
        invariant_violated="The first unique invariant.",
        root_cause="The first unique invariant.",
    )

    result = evaluate_demo_parity([candidate], demos)

    assert result.match_count == 1
    assert result.recall == 0.5
    assert result.missing_demo_ids == ["DREV-DUPLICATE"]


def test_ambiguous_token_matches_are_reported_and_never_counted(tmp_path: Path) -> None:
    for suffix in ("001", "002"):
        _write_finding(
            tmp_path,
            f"DREV-2026-{suffix}",
            f"""id: DREV-2026-{suffix}
toolchain: llvm
mechanism: stack-protector
invariant_violated: Every protected exit checks the canary before returning path {suffix}.
status: confirmed
poc_verified: true""",
        )

    candidate = _candidate(
        invariant_violated="Every protected exit checks the canary before returning.",
        root_cause="Every protected exit checks the canary before returning.",
    )
    result = evaluate_demo_parity(
        [candidate], tmp_path, token_similarity_threshold=0.5
    )

    assert result.match_count == 0
    assert result.precision == 0.0
    assert result.recall == 0.0
    assert result.missing_demo_ids == ["DREV-2026-001", "DREV-2026-002"]
    assert result.unmatched_candidate_indices == [0]
    assert len(result.ambiguous_matches) == 1
    assert result.ambiguous_matches[0].match_method == "token_set"
    assert result.ambiguous_matches[0].candidate_indices == [0]
    assert result.ambiguous_matches[0].demo_ids == [
        "DREV-2026-001",
        "DREV-2026-002",
    ]
    assert [edge.demo_id for edge in result.ambiguous_matches[0].edges] == [
        "DREV-2026-001",
        "DREV-2026-002",
    ]
    assert all(edge.score >= 0.5 for edge in result.ambiguous_matches[0].edges)


def test_recall_threshold_can_be_marked_blocking_for_caller_consumption(
    tmp_path: Path,
) -> None:
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
invariant_violated: Every target has ENDBR.
status: confirmed
poc_verified: true""",
    )

    result = evaluate_demo_parity(
        [_candidate()],
        tmp_path,
        threshold=0.75,
        threshold_metric="recall",
        threshold_blocking=True,
    )

    assert result.recall == 0.5
    assert result.threshold_pass is False
    assert result.threshold_blocked is True
    assert parity_metrics(result)["threshold_blocked"] is True


@pytest.mark.skipif(
    not (_REAL_DEMO_ROOT / "findings").is_dir(),
    reason="real defend-reviewer demo corpus is not available",
)
def test_real_demo_corpus_parser_smoke_is_read_only() -> None:
    readmes = sorted((_REAL_DEMO_ROOT / "findings").glob("DREV-*/README.md"))
    before = {path: path.stat().st_mtime_ns for path in readmes}

    parsed = parse_demo_findings(_REAL_DEMO_ROOT)
    parity = evaluate_demo_parity([], parsed)
    verified_parity = evaluate_demo_parity([], parsed, profile="poc-verified")

    assert len(parsed.findings) == 30
    assert len(parsed.issues) == 2
    assert {finding.id for finding in parsed.findings if not finding.schema_valid} == {
        "DREV-2026-021",
        "DREV-2026-030",
    }
    assert parity.raw_total == 30
    assert parity.benchmark_total == 27
    assert parity.profile == "demo-workset"
    assert parity.raw_corpus_aggregates.by_status == {
        "draft": 27,
        "missing": 1,
        "reported-upstream": 1,
        "retracted": 1,
    }
    assert parity.profile_aggregates.by_status == {
        "draft": 26,
        "reported-upstream": 1,
    }
    assert parity.excluded_ids == [
        "DREV-2026-015",
        "DREV-2026-021",
        "DREV-2026-030",
    ]
    assert verified_parity.raw_total == 30
    assert verified_parity.benchmark_total == 20
    assert verified_parity.profile == "poc-verified"
    assert verified_parity.profile_aggregates.by_status == {
        "draft": 19,
        "reported-upstream": 1,
    }
    assert verified_parity.exclusion_reasons["schema_invalid"] == [
        "DREV-2026-021",
        "DREV-2026-030",
    ]
    assert verified_parity.exclusion_reasons["retracted"] == ["DREV-2026-015"]
    assert verified_parity.exclusion_reasons["poc_not_verified"] == [
        "DREV-2026-015",
        "DREV-2026-017",
        "DREV-2026-018",
        "DREV-2026-019",
        "DREV-2026-020",
        "DREV-2026-021",
        "DREV-2026-022",
        "DREV-2026-023",
        "DREV-2026-024",
        "DREV-2026-030",
    ]
    assert {
        "codegen",
        "zero-call-used-regs",
        "riscv-cfi",
        "ibt",
        "ret-hardening",
    } <= set(parity.demo_aggregates.by_mechanism)
    raw_ret_hardening = next(
        finding.mechanism for finding in parsed.findings if finding.id == "DREV-2026-029"
    )
    assert raw_ret_hardening == "ret-hardening (return thunks / LVI)"
    assert normalize_mechanism(raw_ret_hardening) == "ret-hardening"
    assert [
        family.key for family in families_for_mechanisms([raw_ret_hardening])
    ] == ["C"]
    assert sum("malformed YAML" in issue.message for issue in parsed.issues) == 1
    assert sum("missing required fields" in issue.message for issue in parsed.issues) == 1
    assert {path: path.stat().st_mtime_ns for path in readmes} == before
