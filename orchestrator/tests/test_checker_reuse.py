from __future__ import annotations

from types import SimpleNamespace

import defuzz_loop.experiment_engine.pipeline as pipeline
from defuzz_loop.experiment_engine.checker_reuse import (
    P01_FINGERPRINT,
    PILOT_CHECKER_ID,
    canonical_concept_fingerprint,
    reusable_checker_ids,
    reuse_report,
)


def test_pilot_reuses_only_complete_indirect_callable_entry_contract() -> None:
    statement = "Every indirect-callable function entry must begin with ENDBR."

    assert canonical_concept_fingerprint(statement) == P01_FINGERPRINT
    assert reusable_checker_ids(statement) == [PILOT_CHECKER_ID]
    real_pilot_statement = (
        "ELF assembly marks binaries with the IBT feature property and emits ENDBR at "
        "address-taken function entries when Intel CET indirect-branch tracking is enabled."
    )
    assert reusable_checker_ids(real_pilot_statement) == [PILOT_CHECKER_ID]


def test_nearby_ibt_properties_do_not_false_map_to_p01() -> None:
    # Property-only ENDBR wording lacks the indirect-callable scope.
    assert reusable_checker_ids("Function entries must begin with ENDBR.") == []
    # P02 is a return-edge contract, not an entry-point contract.
    assert reusable_checker_ids("The instruction after a setjmp call must begin with ENDBR.") == []
    # B01 is a whole-function-body byte-pattern scan, not P01.
    assert reusable_checker_ids("No unintended ENDBR may occur inside a function body.") == []
    assert reusable_checker_ids("Address-taken function entries must not begin with ENDBR.") == []


def test_reuse_report_is_audit_only_and_deterministic() -> None:
    records = [
        {
            "invariant_id": "INVGEN-P01",
            "statement": "Indirect-callable function entries must begin with ENDBR.",
        },
        {"invariant_id": "INVGEN-P02", "statement": "A setjmp return site needs ENDBR."},
    ]

    report = reuse_report(records)

    assert report["finding_claims_exposed"] is False
    assert report["decisions"] == [
        {
            "invariant_id": "INVGEN-P01",
            "concept_fingerprint": P01_FINGERPRINT,
            "reused_checker_ids": [PILOT_CHECKER_ID],
            "decision": "reused",
            "reason": "exact pilot semantic contract for indirect-callable function entry ENDBR",
        },
        {
            "invariant_id": "INVGEN-P02",
            "concept_fingerprint": None,
            "reused_checker_ids": [],
            "decision": "author",
            "reason": "no trusted exact semantic fingerprint match",
        },
    ]


def test_formal_usage_allows_only_explicit_deterministic_zero_call_stage() -> None:
    formal = SimpleNamespace(mode="formal")
    deterministic = {
        "records": 0,
        "deterministic_only": True,
        "token_comparable": True,
        "token_budget_overshot": False,
    }

    assert pipeline._formal_usage_error(formal, deterministic) is None
    assert "provider-reported token usage" in (
        pipeline._formal_usage_error(
            formal,
            {**deterministic, "deterministic_only": False, "token_comparable": False},
        )
        or ""
    )
