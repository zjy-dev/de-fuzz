"""Mechanical implementation of the five-item Finding Admission Gate."""

from __future__ import annotations

import re
from collections.abc import Iterable

from .audit_schema import (
    AdmissionCheck,
    AdmissionResult,
    AuditCandidate,
    AuditReport,
    ReportAdmissionResult,
)

_SITE = re.compile(r"^.+:[1-9]\d*(?:[-:]\d+)?$")
_HEDGED_IMPACT = {
    "risky",
    "fragile",
    "suspicious",
    "weakens the mitigation",
    "could be exploitable",
}
_DREV = re.compile(r"\bDREV-\d{4}-\d{3,}\b", re.IGNORECASE)
_FINDINGS_REFERENCE = re.compile(r"(?i)(?:^|[\s'(\[])findings/")


def _present(value: object) -> bool:
    if isinstance(value, str):
        return bool(value.strip())
    if isinstance(value, Iterable):
        return bool(list(value))
    return value is not None


def _excerpt_line_count(excerpt: str) -> int:
    lines = excerpt.strip().splitlines()
    if lines and lines[0].strip().startswith("```"):
        lines = lines[1:]
    if lines and lines[-1].strip().startswith("```"):
        lines = lines[:-1]
    return sum(bool(line.strip()) for line in lines)


def _evidence(candidate: AuditCandidate) -> tuple[bool, str]:
    sites = list(candidate.evidence_file_line)
    excerpts = [candidate.evidence_code] if candidate.evidence_code.strip() else []
    for item in candidate.evidence:
        if item.file and item.line not in (None, ""):
            sites.append(f"{item.file}:{item.line}")
        if item.excerpt.strip():
            excerpts.append(item.excerpt)
    exact_site = any(_SITE.match(site.strip()) for site in sites)
    five_lines = any(_excerpt_line_count(excerpt) >= 5 for excerpt in excerpts)
    plan_ok = candidate.poc_verified or bool(candidate.poc_verification_plan.strip())
    passed = exact_site and five_lines and plan_ok
    missing = []
    if not exact_site:
        missing.append("an exact file:line source site")
    if not five_lines:
        missing.append("a verbatim source excerpt of at least 5 non-empty lines")
    if not plan_ok:
        missing.append("one explicit PoC verification plan when poc_verified is false")
    return passed, "missing " + ", ".join(missing) if missing else ""


def candidate_leaks_findings(candidate: AuditCandidate) -> bool:
    """Return true when a worker echoes concrete private-corpus material."""

    payload = candidate.model_dump_json()
    return bool(_DREV.search(payload) or _FINDINGS_REFERENCE.search(payload))


def evaluate_candidate(candidate: AuditCandidate | dict[str, object]) -> AdmissionResult:
    """Evaluate exactly five doctrine gates, without subjective promotion."""

    item = (
        candidate
        if isinstance(candidate, AuditCandidate)
        else AuditCandidate.model_validate(candidate)
    )
    tainted = candidate_leaks_findings(item)
    invariant_ok = (
        bool(item.invariant_violated.strip())
        and bool(item.toolchain.strip())
        and bool(item.toolchain_version.strip())
        and bool(item.mechanism.strip())
        and bool(item.discovered.strip())
        and not tainted
    )
    evidence_ok, evidence_reason = _evidence(item)
    trigger = item.minimal_trigger
    trigger_ok = (
        bool(trigger.source.strip())
        and _present(trigger.flags)
        and (_present(trigger.isa) or bool(item.isa))
    )
    impact_text = item.impact.strip()
    impact_ok = bool(impact_text) and impact_text.lower().rstrip(".") not in _HEDGED_IMPACT
    rescued_text = item.why_not_rescued.strip()
    rescued_ok = bool(rescued_text) and rescued_text.lower() not in {"unknown", "unclear", "n/a"}

    checks = [
        AdmissionCheck(
            gate="violated-invariant",
            passed=invariant_ok,
            reason=(
                ""
                if invariant_ok
                else (
                    "candidate is tainted by concrete DREV/findings content"
                    if tainted
                    else (
                        "missing finding identity/provenance fields: toolchain, "
                        "toolchain_version, mechanism, discovered, and named invariant"
                    )
                )
            ),
        ),
        AdmissionCheck(
            gate="concrete-code-site",
            passed=evidence_ok,
            reason=evidence_reason,
        ),
        AdmissionCheck(
            gate="minimal-trigger",
            passed=trigger_ok,
            reason=(
                ""
                if trigger_ok
                else "minimal trigger must include source, compile flags, and target ISA"
            ),
        ),
        AdmissionCheck(
            gate="concrete-impact",
            passed=impact_ok,
            reason="" if impact_ok else "missing a concrete, non-hedged consequence",
        ),
        AdmissionCheck(
            gate="why-not-rescued",
            passed=rescued_ok,
            reason=(
                ""
                if rescued_ok
                else (
                    "missing why later compiler, linker, loader, runtime, "
                    "or CPU layers do not rescue it"
                )
            ),
        ),
    ]
    return AdmissionResult(candidate=item, checks=checks)


def admit_report(report: AuditReport) -> ReportAdmissionResult:
    results = [evaluate_candidate(candidate) for candidate in report.candidates]
    if report.tainted:
        for result in results:
            first = result.checks[0]
            first.passed = False
            first.reason = "worker result is tainted by concrete DREV/findings content"
    return ReportAdmissionResult(
        results=results,
        admitted=[result.candidate for result in results if result.admitted],
        rejected=[result.candidate for result in results if not result.admitted],
    )


# Compatibility aliases for callers using noun-oriented names.
check_admission = evaluate_candidate
apply_admission_gate = admit_report
