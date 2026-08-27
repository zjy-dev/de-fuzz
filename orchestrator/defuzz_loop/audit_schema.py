"""Schemas and canonical scope for Part III compiler-defense audits.

The models in this module intentionally accept incomplete candidates.  A model
describes what a worker said; :mod:`defuzz_loop.admission` decides whether that
statement clears the mechanical five-item finding gate.
"""

from __future__ import annotations

import json
import re
import unicodedata
from collections.abc import Mapping, Sequence
from enum import StrEnum
from typing import Any

import yaml
from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


class AuditVariant(StrEnum):
    """Part III variants.  ``without-rag`` belongs to Part I, not here."""

    FULL = "full"
    WITHOUT_ORACLE = "without-oracle"
    BARE_AGENT = "bare-agent"


class AuditFamily(BaseModel):
    """One canonical worker shard from the reference full-review prompt."""

    model_config = ConfigDict(frozen=True)

    key: str
    name: str
    mechanisms: tuple[str, ...]


CANONICAL_AUDIT_FAMILIES: tuple[AuditFamily, ...] = (
    AuditFamily(
        key="A",
        name="stack-protection",
        mechanisms=("stack-protector", "stack-clash-protection"),
    ),
    AuditFamily(
        key="B",
        name="memory-bounds",
        mechanisms=(
            "fortify-source",
            "strict-flex-arrays",
            "zero-init-padding",
            "source-annotations",
            "codegen",
        ),
    ),
    AuditFamily(
        key="C",
        name="control-flow-integrity",
        mechanisms=(
            "cet",
            "ibt",
            "shstk",
            "bti",
            "pac",
            "return-address-signing",
            "cmse",
            "riscv-cfi",
            "cet-ibt",
            "ret-hardening",
        ),
    ),
    AuditFamily(
        key="D",
        name="address-space-elf",
        mechanisms=("pie", "pic", "relro", "nx", "noexecstack", "nodlopen", "as-needed"),
    ),
    AuditFamily(
        key="E",
        name="sanitizer-umbrella",
        mechanisms=(
            "asan",
            "ubsan",
            "tsan",
            "lsan",
            "auto-var-init",
            "glibcxx-assertions",
            "fhardened",
            "zero-call-used-regs",
        ),
    ),
)

# Short aliases make the constant discoverable without weakening its immutability.
AUDIT_FAMILIES = CANONICAL_AUDIT_FAMILIES
FAMILY_BY_KEY = {family.key: family for family in CANONICAL_AUDIT_FAMILIES}
FAMILY_BY_NAME = {family.name: family for family in CANONICAL_AUDIT_FAMILIES}


def canonical_family(value: str | AuditFamily) -> AuditFamily:
    """Resolve a family key/name without allowing an ad-hoc sixth shard."""

    if isinstance(value, AuditFamily):
        return value
    normalized = value.strip().lower().replace("_", "-")
    for family in CANONICAL_AUDIT_FAMILIES:
        if normalized in {family.key.lower(), family.name}:
            return family
    raise ValueError(f"unknown audit family: {value!r}")


_MECHANISM_ALIASES = {
    "canary": "stack-protector",
    "stack-canary": "stack-protector",
    "ssp": "stack-protector",
    "stack-clash": "stack-clash-protection",
    "fortify": "fortify-source",
    "fortify-source-3": "fortify-source",
    "backend-codegen": "codegen",
    "code-generation": "codegen",
    "fzero-call-used-regs": "zero-call-used-regs",
    "zero-call-used-registers": "zero-call-used-regs",
    "zcur": "zero-call-used-regs",
    "risc-v-cfi": "riscv-cfi",
    "zicfilp": "riscv-cfi",
    "zicfiss": "riscv-cfi",
    "cet-ibt": "ibt",
    "intel-cet-ibt": "ibt",
    "endbr-ibt": "ibt",
    "pointer-authentication": "pac",
    "return-address-signing": "pac",
    "ftrivial-auto-var-init": "auto-var-init",
    "return-hardening": "ret-hardening",
    "return-thunk": "ret-hardening",
    "return-thunks": "ret-hardening",
    "lvi-ret-hardening": "ret-hardening",
    "ret-hardening-return-thunks-lvi": "ret-hardening",
}

_ISA_ALIASES = {
    "amd64": "x86_64",
    "x64": "x86_64",
    "x86-64": "x86_64",
    "x8664": "x86_64",
    "arm64": "aarch64",
    "aarch-64": "aarch64",
    "risc-v64": "riscv64",
    "risc-v-64": "riscv64",
}


def normalize_mechanism(value: str) -> str:
    """Normalize corpus, CLI, and prompt spellings to one canonical label.

    Tokenization deliberately removes punctuation before alias lookup so raw
    corpus labels such as ``ret-hardening (return thunks / LVI)`` resolve the
    same way as their CLI-safe spelling.
    """

    normalized = "-".join(
        re.findall(r"[a-z0-9]+", unicodedata.normalize("NFKC", value).casefold())
    )
    return _MECHANISM_ALIASES.get(normalized, normalized)


def normalize_isa(value: str) -> str:
    """Normalize common ISA aliases used by plans and candidate reports."""

    normalized = "-".join(
        re.findall(r"[a-z0-9]+", unicodedata.normalize("NFKC", value).casefold())
    ).strip("-")
    return _ISA_ALIASES.get(normalized, normalized)


def families_for_mechanisms(mechanisms: Sequence[str] | None) -> tuple[AuditFamily, ...]:
    """Return intersecting canonical families in A--E order.

    An empty mechanism list means the complete canonical review. Unknown
    mechanisms are rejected instead of being silently left without an owner.
    """

    if not mechanisms:
        return CANONICAL_AUDIT_FAMILIES
    wanted = {normalize_mechanism(item) for item in mechanisms}
    known = {
        normalize_mechanism(mechanism)
        for family in CANONICAL_AUDIT_FAMILIES
        for mechanism in family.mechanisms
    }
    unknown = wanted - known
    if unknown:
        raise ValueError(f"mechanisms have no canonical audit family: {sorted(unknown)!r}")
    return tuple(
        family
        for family in CANONICAL_AUDIT_FAMILIES
        if wanted.intersection(
            normalize_mechanism(mechanism) for mechanism in family.mechanisms
        )
    )


class EvidenceSite(BaseModel):
    model_config = ConfigDict(extra="allow")

    file: str = ""
    line: int | str | None = None
    symbol: str = ""
    excerpt: str = ""
    excerpt_lines: str = ""


class MinimalTrigger(BaseModel):
    model_config = ConfigDict(extra="allow")

    source: str = ""
    flags: str | list[str] = ""
    target: str = ""
    isa: str | list[str] = ""
    notes: str = ""

    @field_validator("isa", mode="after")
    @classmethod
    def _normalize_isas(cls, value: str | list[str]) -> str | list[str]:
        if isinstance(value, str):
            return normalize_isa(value)
        return list(dict.fromkeys(normalize_isa(item) for item in value))


class AuditCandidate(BaseModel):
    """A worker candidate before deterministic admission."""

    model_config = ConfigDict(extra="allow", populate_by_name=True)

    schema_version: int = 1
    id: str = "DREV-YYYY-NNN"
    title: str = ""
    toolchain: str = ""
    toolchain_version: str = ""
    mechanism: str = ""
    isa: list[str] = Field(default_factory=list)
    checker_ids: list[str] = Field(default_factory=list)
    invariant_violated: str = ""
    root_cause: str = ""
    layer: str = ""
    evidence_file_line: list[str] = Field(default_factory=list)
    evidence_code: str = ""
    evidence: list[EvidenceSite] = Field(default_factory=list)
    minimal_trigger: MinimalTrigger = Field(default_factory=MinimalTrigger)
    impact: str = ""
    why_not_rescued: str = ""
    poc_verified: bool = False
    poc_verification_plan: str = ""
    suggested_regression_test: str = ""
    related_historical: list[str] = Field(default_factory=list)
    related_invariants: list[str] = Field(default_factory=list)
    severity: str = ""
    severity_justification: str = ""
    discovered: str = ""

    @field_validator(
        "isa",
        "checker_ids",
        "evidence_file_line",
        "related_historical",
        "related_invariants",
        mode="before",
    )
    @classmethod
    def _coerce_list(cls, value: Any) -> list[Any]:
        if value is None:
            return []
        if isinstance(value, str):
            return [value]
        return list(value)

    @field_validator("checker_ids")
    @classmethod
    def _normalize_checker_ids(cls, value: list[str]) -> list[str]:
        normalized = [item.strip() for item in value if item.strip()]
        return list(dict.fromkeys(normalized))

    @field_validator("mechanism")
    @classmethod
    def _normalize_mechanism(cls, value: str) -> str:
        return normalize_mechanism(value)

    @field_validator("isa")
    @classmethod
    def _normalize_candidate_isas(cls, value: list[str]) -> list[str]:
        return list(dict.fromkeys(normalize_isa(item) for item in value if item.strip()))

    @field_validator("minimal_trigger", mode="before")
    @classmethod
    def _coerce_trigger(cls, value: Any) -> Any:
        if value is None:
            return {}
        if isinstance(value, str):
            return {"source": value}
        return value

    @model_validator(mode="after")
    def _derive_legacy_evidence_fields(self) -> AuditCandidate:
        if not self.evidence_file_line and self.evidence:
            sites: list[str] = []
            for item in self.evidence:
                if item.file and item.line not in (None, ""):
                    sites.append(f"{item.file}:{item.line}")
            self.evidence_file_line = sites
        if not self.evidence_code and self.evidence:
            excerpts = [item.excerpt for item in self.evidence if item.excerpt.strip()]
            self.evidence_code = "\n\n".join(excerpts)
        return self


class CoverageGap(BaseModel):
    model_config = ConfigDict(extra="allow")

    summary: str = ""
    mechanism: str = ""
    isa: list[str] = Field(default_factory=list)
    reason: str = ""

    @model_validator(mode="before")
    @classmethod
    def _from_text(cls, value: Any) -> Any:
        if isinstance(value, str):
            return {"summary": value}
        return value


class AuditReport(BaseModel):
    """Structured output from one audit worker."""

    model_config = ConfigDict(extra="allow", populate_by_name=True)

    schema_version: int = 1
    family: str = ""
    variant: str = AuditVariant.FULL.value
    toolchain_version: str = ""
    discovered: str = ""
    scope: str | dict[str, Any] = ""
    audited_components: list[str] = Field(default_factory=list)
    cross_isa_matrix: dict[str, Any] | list[Any] = Field(default_factory=dict)
    cross_mechanism_matrix: dict[str, Any] | list[Any] = Field(default_factory=dict)
    candidates: list[AuditCandidate] = Field(default_factory=list)
    coverage_gaps: list[CoverageGap] = Field(default_factory=list)
    next_steps: list[str] = Field(default_factory=list)
    worker_bundle_sha256: str = ""
    parse_issues: list[str] = Field(default_factory=list)
    tainted: bool = False

    @model_validator(mode="before")
    @classmethod
    def _accept_findings_key(cls, value: Any) -> Any:
        if not isinstance(value, Mapping):
            return value
        payload = dict(value)
        if "candidates" not in payload and "findings" in payload:
            findings = payload.get("findings")
            payload["candidates"] = [] if findings in (None, "No bug found") else findings
        if "next_steps" not in payload and "highest_priority_next_steps" in payload:
            payload["next_steps"] = payload["highest_priority_next_steps"]
        return payload


class AdmissionCheck(BaseModel):
    gate: str
    passed: bool
    reason: str = ""


class AdmissionResult(BaseModel):
    candidate: AuditCandidate
    checks: list[AdmissionCheck]

    @property
    def admitted(self) -> bool:
        return len(self.checks) == 5 and all(check.passed for check in self.checks)

    @property
    def issues(self) -> list[str]:
        return [check.reason for check in self.checks if not check.passed]


class ReportAdmissionResult(BaseModel):
    results: list[AdmissionResult] = Field(default_factory=list)
    admitted: list[AuditCandidate] = Field(default_factory=list)
    rejected: list[AuditCandidate] = Field(default_factory=list)


def _extract_payload(text: str) -> Any:
    stripped = text.strip()
    if stripped.startswith("```"):
        lines = stripped.splitlines()
        if len(lines) >= 2 and lines[-1].strip().startswith("```"):
            stripped = "\n".join(lines[1:-1])
    try:
        return json.loads(stripped)
    except json.JSONDecodeError:
        return yaml.safe_load(stripped)


def parse_audit_report(value: AuditReport | Mapping[str, Any] | str) -> AuditReport:
    """Parse JSON/YAML worker output without turning syntax failures into findings."""

    if isinstance(value, AuditReport):
        return value
    try:
        payload = _extract_payload(value) if isinstance(value, str) else dict(value)
        if not isinstance(payload, Mapping):
            raise TypeError("audit report must be a mapping")
        return AuditReport.model_validate(payload)
    except (TypeError, ValueError, yaml.YAMLError) as exc:
        return AuditReport(parse_issues=[f"worker output could not be parsed: {exc}"])


def audit_report_json_schema() -> dict[str, Any]:
    """Return the worker response schema for structured-output backends."""

    schema = AuditReport.model_json_schema()
    candidate = schema.get("$defs", {}).get("AuditCandidate")
    if isinstance(candidate, dict):
        required = set(candidate.get("required", []))
        required.update(
            {
                "schema_version",
                "toolchain",
                "toolchain_version",
                "mechanism",
                "isa",
                "invariant_violated",
                "evidence_file_line",
                "evidence_code",
                "minimal_trigger",
                "impact",
                "why_not_rescued",
                "poc_verified",
                "discovered",
            }
        )
        candidate["required"] = sorted(required)
    required_report = set(schema.get("required", []))
    required_report.update({"schema_version", "family", "variant", "candidates"})
    schema["required"] = sorted(required_report)
    return schema
