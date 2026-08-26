from __future__ import annotations

from copy import deepcopy

import pytest

from defuzz_loop.admission import admit_report, evaluate_candidate
from defuzz_loop.audit_schema import AuditCandidate, AuditReport, audit_report_json_schema


def _valid_candidate() -> dict[str, object]:
    return {
        "toolchain": "gcc",
        "toolchain_version": "gcc-17-20260531",
        "mechanism": "stack-protector",
        "isa": ["x86_64"],
        "invariant_violated": "Every protected return checks the saved guard.",
        "layer": "backend-frame",
        "evidence_file_line": ["gcc/config/i386/i386.cc:123"],
        "evidence_code": "\n".join(f"line {index}" for index in range(1, 6)),
        "minimal_trigger": {
            "source": "void f(char *p) { char b[32]; __builtin_strcpy(b, p); }",
            "flags": "-O2 -fstack-protector-all",
            "target": "x86_64-linux-gnu",
            "isa": "x86_64",
        },
        "impact": "The generated return silently skips the canary check.",
        "why_not_rescued": "The linker and runtime cannot restore an omitted epilogue check.",
        "poc_verified": True,
        "suggested_regression_test": "Scan the epilogue and execute a corrupting input.",
        "severity": "high",
        "severity_justification": "A stack overwrite can bypass the promised guard.",
        "discovered": "2026-08-26",
    }


def test_admission_has_exactly_five_passing_gates() -> None:
    result = evaluate_candidate(_valid_candidate())
    assert result.admitted
    assert [check.gate for check in result.checks] == [
        "violated-invariant",
        "concrete-code-site",
        "minimal-trigger",
        "concrete-impact",
        "why-not-rescued",
    ]


def test_worker_schema_marks_identity_and_provenance_fields_required() -> None:
    schema = audit_report_json_schema()
    candidate_required = set(schema["$defs"]["AuditCandidate"]["required"])
    assert {"schema_version", "toolchain_version", "discovered"} <= candidate_required
    assert {"schema_version", "family", "variant", "candidates"} <= set(
        schema["required"]
    )


def test_static_candidate_requires_five_lines_and_one_verification_plan() -> None:
    payload = _valid_candidate()
    payload["poc_verified"] = False
    payload["evidence_code"] = "one\ntwo\nthree\nfour"
    result = evaluate_candidate(payload)
    assert not result.admitted
    assert any("at least 5" in issue for issue in result.issues)
    assert any("PoC verification plan" in issue for issue in result.issues)

    payload["evidence_code"] = "one\ntwo\nthree\nfour\nfive"
    payload["poc_verification_plan"] = (
        "Confirm the single runtime fact that the generated return has no guard branch."
    )
    assert evaluate_candidate(payload).admitted


@pytest.mark.parametrize("field", ["toolchain_version", "discovered"])
def test_required_provenance_is_rejected_by_admission(field: str) -> None:
    payload = _valid_candidate()
    payload[field] = ""
    result = evaluate_candidate(payload)
    assert not result.admitted
    assert "identity/provenance" in result.issues[0]


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("id", "DREV-2026-001"),
        ("related_historical", ["findings/DREV-2026-001/README.md"]),
    ],
)
def test_concrete_finding_leak_taints_and_rejects_candidate(field: str, value: object) -> None:
    payload = deepcopy(_valid_candidate())
    payload[field] = value
    result = evaluate_candidate(payload)
    assert not result.admitted
    assert any("tainted" in issue for issue in result.issues)


def test_report_admission_separates_candidates_without_archiving() -> None:
    valid = AuditCandidate.model_validate(_valid_candidate())
    invalid_data = _valid_candidate()
    invalid_data["impact"] = "risky"
    report = AuditReport(candidates=[valid, AuditCandidate.model_validate(invalid_data)])
    result = admit_report(report)
    assert len(result.results) == 2
    assert result.admitted == [valid]
    assert len(result.rejected) == 1


def test_tainted_report_rejects_every_candidate() -> None:
    candidate = AuditCandidate.model_validate(_valid_candidate())
    result = admit_report(AuditReport(candidates=[candidate], tainted=True))
    assert result.admitted == []
    assert result.rejected == [candidate]
    assert "tainted" in result.results[0].issues[0]
