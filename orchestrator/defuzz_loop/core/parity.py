"""Orchestrator-only parsing and parity evaluation for the demo corpus.

Nothing in this module is imported by prompt_bundle. This keeps the read side
of the private findings corpus structurally separate from worker prompt
construction. Parsing is deliberately tolerant: malformed YAML is reported
and useful top-level identity fields are recovered when possible.
"""

from __future__ import annotations

import hashlib
import json
import re
import unicodedata
from collections import Counter, defaultdict
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any, Literal

import yaml
from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    ValidationError,
    field_validator,
    model_validator,
)

from .audit_schema import (
    AuditCandidate,
    AuditReport,
    normalize_isa,
    normalize_mechanism,
)

_TOP_LEVEL = re.compile(r"^([A-Za-z_][A-Za-z0-9_-]*):(?:[ \t]*(.*))?$")
_MARKDOWN = re.compile(r"[*_#>\[\]{}()]+")
_WORDS = re.compile(r"[a-z0-9]+")

_TOOLCHAIN_ALIASES = {
    "clang": "llvm",
    "llvm-clang": "llvm",
    "gnu-gcc": "gcc",
    "gnu-ld": "binutils",
}
_REQUIRED_DEMO_FIELDS = (
    "id",
    "toolchain",
    "mechanism",
    "invariant_violated",
    "status",
)
_DEFAULT_TOKEN_SIMILARITY_THRESHOLD = 0.65
ThresholdMetric = Literal["f1", "recall"]
ParityProfile = Literal["demo-workset", "poc-verified"]
ResolvedParityProfile = Literal["demo-workset", "poc-verified", "custom"]
ExclusionReason = Literal["schema_invalid", "retracted", "poc_not_verified"]
ScopeStatus = Literal["not_requested", "applicable", "not_applicable"]
_PROFILE_DESCRIPTIONS: dict[ResolvedParityProfile, str] = {
    "demo-workset": (
        "Engineering parity/superset workset: schema-valid, non-retracted demo "
        "findings, including drafts."
    ),
    "poc-verified": (
        "Stronger-evidence demo subset: schema-valid, non-retracted findings with "
        "poc_verified=true; this is not a formal paper result."
    ),
    "custom": (
        "Custom library inclusion policy; it is not one of the named demo-parity "
        "profiles or a formal paper result."
    ),
}


class ParseIssue(BaseModel):
    path: str
    message: str
    line: int | None = None
    recoverable: bool = True
    finding_id: str = ""


class DemoFinding(BaseModel):
    model_config = ConfigDict(extra="allow")

    id: str = ""
    toolchain: str = ""
    toolchain_version: str = ""
    mechanism: str = ""
    isa: list[str] = Field(default_factory=list)
    invariant_violated: str = ""
    root_cause: str = ""
    status: str = ""
    poc_verified: bool | None = None
    source_path: str = ""
    finding_key: str = ""
    checker_ids: list[str] = Field(default_factory=list)
    evidence_signature: str = ""
    verified_evidence_signature: str = ""
    schema_valid: bool = True
    schema_issues: list[str] = Field(default_factory=list)

    @field_validator("poc_verified", mode="before")
    @classmethod
    def _bool_or_none(cls, value: Any) -> bool | None:
        if value is None or value == "":
            return None
        if isinstance(value, bool):
            return value
        if isinstance(value, str) and value.strip().casefold() in {"true", "false"}:
            return value.strip().casefold() == "true"
        return None

    @field_validator("isa", "checker_ids", "schema_issues", mode="before")
    @classmethod
    def _list_or_empty(cls, value: Any) -> list[Any]:
        if value in (None, ""):
            return []
        if isinstance(value, str):
            return [value]
        return list(value)

    @model_validator(mode="after")
    def _derive_schema_validity(self) -> DemoFinding:
        missing = [field for field in _REQUIRED_DEMO_FIELDS if not getattr(self, field).strip()]
        missing_message = f"missing required fields: {', '.join(missing)}" if missing else ""
        if missing_message and missing_message not in self.schema_issues:
            self.schema_issues.append(missing_message)
        self.schema_valid = self.schema_valid and not self.schema_issues
        return self

    @property
    def root(self) -> str:
        return self.root_cause or self.invariant_violated


class DemoFindingsParseResult(BaseModel):
    findings: list[DemoFinding] = Field(default_factory=list)
    issues: list[ParseIssue] = Field(default_factory=list)

    def __iter__(self):  # type: ignore[no-untyped-def]
        return iter(self.findings)

    def __len__(self) -> int:
        return len(self.findings)


class FindingAggregates(BaseModel):
    total: int = 0
    by_toolchain: dict[str, int] = Field(default_factory=dict)
    by_mechanism: dict[str, int] = Field(default_factory=dict)
    by_status: dict[str, int] = Field(default_factory=dict)
    by_poc_verified: dict[str, int] = Field(default_factory=dict)


class BenchmarkProfileReport(BaseModel):
    """Selected corpus profile and its explicit scientific boundary."""

    name: ResolvedParityProfile
    description: str
    aggregates: FindingAggregates = Field(default_factory=FindingAggregates)


class ParityScope(BaseModel):
    """Evaluator-only benchmark scope.

    Values within one dimension are ORed; populated dimensions are ANDed.
    Exact ``demo_ids`` cannot be combined with dimension filters.  Absence of
    this model preserves the historical unscoped benchmark behavior.
    """

    model_config = ConfigDict(extra="forbid", frozen=True)

    toolchains: tuple[str, ...] = ()
    version: tuple[str, ...] = ()
    mechanisms: tuple[str, ...] = ()
    isas: tuple[str, ...] = ()
    demo_ids: tuple[str, ...] = ()

    @model_validator(mode="before")
    @classmethod
    def _accept_compatibility_names(cls, value: Any) -> Any:
        if not isinstance(value, Mapping):
            return value
        payload = dict(value)
        aliases = {
            "compiler": "toolchains",
            "toolchain": "toolchains",
            "versions": "version",
            "mechanism": "mechanisms",
            "isa": "isas",
        }
        for alias, canonical in aliases.items():
            if alias not in payload:
                continue
            if canonical in payload:
                raise ValueError(f"{alias} cannot be combined with {canonical}")
            payload[canonical] = payload.pop(alias)
        return payload

    @field_validator("toolchains", "version", "mechanisms", "isas", "demo_ids", mode="before")
    @classmethod
    def _tuple_values(cls, value: Any) -> tuple[str, ...]:
        if value in (None, ""):
            return ()
        values = (value,) if isinstance(value, str) else tuple(value)
        normalized = tuple(str(item).strip() for item in values)
        if any(not item for item in normalized):
            raise ValueError("parity scope values must be non-empty strings")
        if len(normalized) != len(set(normalized)):
            raise ValueError("parity scope values must be unique")
        return normalized

    @field_validator("toolchains")
    @classmethod
    def _normalize_toolchains(cls, value: tuple[str, ...]) -> tuple[str, ...]:
        return tuple(dict.fromkeys(normalize_toolchain(item) for item in value))

    @field_validator("mechanisms")
    @classmethod
    def _normalize_mechanisms(cls, value: tuple[str, ...]) -> tuple[str, ...]:
        return tuple(dict.fromkeys(normalize_mechanism(item) for item in value))

    @field_validator("isas")
    @classmethod
    def _normalize_isas(cls, value: tuple[str, ...]) -> tuple[str, ...]:
        return tuple(dict.fromkeys(normalize_isa(item) for item in value))

    @model_validator(mode="after")
    def _validate_selector_mode(self) -> ParityScope:
        dimensions = (self.toolchains, self.version, self.mechanisms, self.isas)
        if self.demo_ids and any(dimensions):
            raise ValueError("demo_ids cannot be combined with dimension filters")
        if not self.demo_ids and not any(dimensions):
            raise ValueError("parity scope requires demo_ids or at least one dimension")
        normalized_versions: list[str] = []
        for version in self.version:
            if self.toolchains:
                normalized_versions.extend(
                    normalize_toolchain_version(version, toolchain=toolchain)
                    for toolchain in self.toolchains
                )
            else:
                normalized_versions.append(normalize_toolchain_version(version))
        object.__setattr__(
            self, "version", tuple(dict.fromkeys(normalized_versions))
        )
        return self

    @property
    def versions(self) -> tuple[str, ...]:
        """Compatibility spelling for callers using a plural dimension name."""

        return self.version


class ParityScopeReport(BaseModel):
    requested: ParityScope | None = None
    status: ScopeStatus = "not_requested"
    empty_scope: bool = False
    not_applicable: bool = False
    selected_demo_ids: list[str] = Field(default_factory=list)
    excluded_demo_ids: list[str] = Field(default_factory=list)
    unresolved_demo_ids: list[str] = Field(default_factory=list)
    aggregates: FindingAggregates = Field(default_factory=FindingAggregates)


class FindingIdentity(BaseModel):
    model_config = ConfigDict(frozen=True)

    toolchain: str
    mechanism: str
    root_cause: str

    def key(self) -> tuple[str, str, str]:
        return self.toolchain, self.mechanism, self.root_cause


class ParityMatch(BaseModel):
    candidate_index: int
    demo_id: str
    identity: FindingIdentity
    match_method: str = "exact"
    score: float = Field(default=1.0, ge=0.0, le=1.0)
    ambiguous: bool = False


class AmbiguousParityEdge(BaseModel):
    candidate_index: int
    demo_id: str
    score: float = Field(ge=0.0, le=1.0)


class AmbiguousParityMatch(BaseModel):
    """A connected one-to-many/many-to-one component left unmatched."""

    demo_ids: list[str] = Field(default_factory=list)
    candidate_indices: list[int] = Field(default_factory=list)
    match_method: str
    score: float = Field(ge=0.0, le=1.0)
    edges: list[AmbiguousParityEdge] = Field(default_factory=list)
    ambiguous: bool = True


class BenchmarkPolicy(BaseModel):
    """Controls which parsed demo records contribute to parity recall.

    The inclusion flags remain available to existing library callers. CLI and
    typed-pipeline users select one of the named profiles instead.
    """

    model_config = ConfigDict(frozen=True)

    profile: ResolvedParityProfile = "demo-workset"
    include_retracted: bool = False
    include_schema_invalid: bool = False

    @model_validator(mode="after")
    def _label_nonstandard_inclusion(self) -> BenchmarkPolicy:
        if self.profile == "poc-verified" and (
            self.include_retracted or self.include_schema_invalid
        ):
            raise ValueError(
                "poc-verified cannot include retracted or schema-invalid records"
            )
        if self.profile == "demo-workset" and (
            self.include_retracted or self.include_schema_invalid
        ):
            object.__setattr__(self, "profile", "custom")
        return self


class BenchmarkExclusion(BaseModel):
    """One raw-corpus record excluded from the selected profile."""

    finding_id: str
    source_path: str
    reasons: list[ExclusionReason]
    schema_issues: list[str] = Field(default_factory=list)


class ParityResult(BaseModel):
    schema_version: int = 4
    matches: list[ParityMatch] = Field(default_factory=list)
    ambiguous_matches: list[AmbiguousParityMatch] = Field(default_factory=list)
    missing_demo_ids: list[str] = Field(default_factory=list)
    unmatched_candidate_indices: list[int] = Field(default_factory=list)
    raw_corpus_aggregates: FindingAggregates = Field(default_factory=FindingAggregates)
    profile_aggregates: FindingAggregates = Field(default_factory=FindingAggregates)
    scoped_aggregates: FindingAggregates = Field(default_factory=FindingAggregates)
    # Compatibility name used by early reports. It is the selected-profile
    # aggregate, not the unfiltered raw corpus aggregate.
    demo_aggregates: FindingAggregates = Field(default_factory=FindingAggregates)
    candidate_aggregates: FindingAggregates = Field(default_factory=FindingAggregates)
    parse_issues: list[ParseIssue] = Field(default_factory=list)
    precision: float = 0.0
    recall: float = 0.0
    f1: float = 0.0
    match_count: int = Field(default=0, ge=0)
    raw_total: int = Field(default=0, ge=0)
    profile_total: int = Field(default=0, ge=0)
    scoped_total: int = Field(default=0, ge=0)
    benchmark_total: int = Field(default=0, ge=0)
    profile: ResolvedParityProfile = "demo-workset"
    profile_description: str = _PROFILE_DESCRIPTIONS["demo-workset"]
    profile_report: BenchmarkProfileReport = Field(
        default_factory=lambda: BenchmarkProfileReport(
            name="demo-workset",
            description=_PROFILE_DESCRIPTIONS["demo-workset"],
        )
    )
    exclusions: list[BenchmarkExclusion] = Field(default_factory=list)
    exclusion_reasons: dict[str, list[str]] = Field(default_factory=dict)
    excluded_ids: list[str] = Field(default_factory=list)
    benchmark_policy: BenchmarkPolicy = Field(default_factory=BenchmarkPolicy)
    scope: ParityScope | None = None
    scope_report: ParityScopeReport = Field(default_factory=ParityScopeReport)
    scope_status: ScopeStatus = "not_requested"
    empty_scope: bool = False
    not_applicable: bool = False
    threshold: float | None = Field(default=None, ge=0.0, le=1.0)
    threshold_metric: ThresholdMetric = "recall"
    threshold_blocking: bool = False
    threshold_pass: bool | None = None
    threshold_blocked: bool = False
    superset_coverage: float = Field(default=0.0, ge=0.0, le=1.0)
    token_similarity_threshold: float = Field(
        default=_DEFAULT_TOKEN_SIMILARITY_THRESHOLD, ge=0.0, le=1.0
    )

    @model_validator(mode="after")
    def _derive_report_fields(self) -> ParityResult:
        """Keep serialized report fields consistent with the detailed matches."""

        self.match_count = len(self.matches)
        self.profile = self.benchmark_policy.profile
        self.profile_description = _PROFILE_DESCRIPTIONS[self.profile]
        self.profile_report = BenchmarkProfileReport(
            name=self.profile,
            description=self.profile_description,
            aggregates=self.profile_aggregates,
        )
        self.profile_total = self.profile_aggregates.total
        self.scoped_total = self.scoped_aggregates.total
        self.benchmark_total = self.scoped_total
        self.scope = self.scope_report.requested
        self.scope_status = self.scope_report.status
        self.empty_scope = self.scope_report.empty_scope
        self.not_applicable = self.scope_report.not_applicable
        self.superset_coverage = 0.0 if self.not_applicable else self.recall
        metric = self.recall if self.threshold_metric == "recall" else self.f1
        self.threshold_pass = (
            None
            if self.not_applicable or self.threshold is None
            else metric >= self.threshold
        )
        self.threshold_blocked = (
            not self.not_applicable
            and self.threshold_blocking
            and self.threshold_pass is False
        )
        return self

    @property
    def matched_count(self) -> int:
        """Compatibility spelling retained for early experiment scripts."""

        return self.match_count


def parity_metrics(result: ParityResult) -> dict[str, int | float | bool | str | None]:
    """Return the stable summary fields copied into ``StageResult.metrics``.

    A failed threshold is an experiment result, not an execution error.  The
    caller should report ``threshold_pass`` without changing stage status.
    """

    return {
        "precision": result.precision,
        "recall": result.recall,
        "f1": result.f1,
        "superset_coverage": result.superset_coverage,
        "match_count": result.match_count,
        "raw_total": result.raw_total,
        "profile_total": result.profile_total,
        "scoped_total": result.scoped_total,
        "benchmark_total": result.benchmark_total,
        "profile": result.profile,
        "scope_status": result.scope_status,
        "empty_scope": result.empty_scope,
        "not_applicable": result.not_applicable,
        "threshold": result.threshold,
        "threshold_metric": result.threshold_metric,
        "threshold_blocking": result.threshold_blocking,
        "threshold_pass": result.threshold_pass,
        "threshold_blocked": result.threshold_blocked,
    }


def normalize_toolchain(value: str) -> str:
    normalized = "-".join(_WORDS.findall(unicodedata.normalize("NFKC", value).casefold()))
    return _TOOLCHAIN_ALIASES.get(normalized, normalized)


def normalize_toolchain_version(value: str, *, toolchain: str = "") -> str:
    """Normalize stable compiler release identities without using snapshot dates.

    GCC spellings such as ``gcc-17-20260531``,
    ``gcc-17 (20260531 snapshot)``, and ``gcc-17.0.0-20260531`` all
    normalize to ``gcc-17``.  Other toolchains use the same major-release rule
    only when their name is explicit or supplied separately.
    """

    folded = unicodedata.normalize("NFKC", value).casefold().strip()
    normalized_toolchain = normalize_toolchain(toolchain)
    known_toolchains = ("gcc", "llvm", "clang", "lld", "binutils", "compiler-rt")
    for name in known_toolchains:
        match = re.search(rf"(?<![a-z0-9]){re.escape(name)}(?:org)?[-\s_]*v?(\d{{1,3}})", folded)
        if match:
            canonical = normalize_toolchain(name)
            return f"{canonical}-{int(match.group(1))}"
    if normalized_toolchain:
        match = re.match(r"^\s*v?(\d{1,3})(?:\.\d+)*", folded)
        if match:
            return f"{normalized_toolchain}-{int(match.group(1))}"
    return "-".join(_WORDS.findall(folded))


def normalize_root_cause(value: str) -> str:
    """Normalize presentation differences while retaining semantic words."""

    value = unicodedata.normalize("NFKC", value).casefold()
    value = _MARKDOWN.sub(" ", value)
    return " ".join(_WORDS.findall(value))


def finding_identity(value: DemoFinding | AuditCandidate | Mapping[str, Any]) -> FindingIdentity:
    if isinstance(value, Mapping):
        root = str(value.get("root_cause") or value.get("invariant_violated") or "")
        toolchain = str(value.get("toolchain") or "")
        mechanism = str(value.get("mechanism") or "")
    else:
        root = value.root_cause or value.invariant_violated
        toolchain = value.toolchain
        mechanism = value.mechanism
    return FindingIdentity(
        toolchain=normalize_toolchain(toolchain),
        mechanism=normalize_mechanism(mechanism),
        root_cause=normalize_root_cause(root),
    )


def _value(value: object, field: str, default: Any = None) -> Any:
    if isinstance(value, Mapping):
        return value.get(field, default)
    return getattr(value, field, default)


def _stable_text(value: Any) -> str:
    if value is None:
        return ""
    return " ".join(str(value).split()).strip().casefold()


def _finding_key(value: object) -> str:
    return _stable_text(_value(value, "finding_key", ""))


def _checker_ids(value: object) -> frozenset[str]:
    raw = _value(value, "checker_ids", ())
    if raw in (None, ""):
        return frozenset()
    values = (raw,) if isinstance(raw, str) else raw
    return frozenset(
        item
        for item in (_stable_text(entry).upper() for entry in values)
        if item
    )


def _normalized_evidence(value: object) -> tuple[tuple[str, ...], tuple[str, ...]]:
    citations: set[str] = set()
    excerpts: set[str] = set()
    raw_citations = _value(value, "evidence_file_line", ())
    if isinstance(raw_citations, str):
        raw_citations = (raw_citations,)
    for citation in raw_citations or ():
        normalized = _stable_text(citation)
        if normalized:
            citations.add(normalized)

    evidence = _value(value, "evidence", ())
    if isinstance(evidence, Mapping):
        evidence = (evidence,)
    for site in evidence or ():
        path = _stable_text(_value(site, "file", ""))
        line = _stable_text(_value(site, "line", ""))
        symbol = _stable_text(_value(site, "symbol", ""))
        citation = ":".join(part for part in (path, line, symbol) if part)
        if citation:
            citations.add(citation)
        excerpt = _stable_text(
            _value(site, "excerpt", "") or _value(site, "excerpt_lines", "")
        )
        if excerpt:
            excerpts.add(excerpt)

    code = _stable_text(_value(value, "evidence_code", ""))
    if code:
        excerpts.add(code)
    return tuple(sorted(citations)), tuple(sorted(excerpts))


def _verified_evidence_signature(value: object) -> str:
    """Return explicit or reproducible evidence IDs only for verified records."""

    explicit = _stable_text(_value(value, "verified_evidence_signature", ""))
    if explicit:
        return explicit
    if _value(value, "poc_verified", None) is not True:
        return ""
    declared = _stable_text(_value(value, "evidence_signature", ""))
    if declared:
        return declared
    citations, excerpts = _normalized_evidence(value)
    if not citations or not excerpts:
        return ""
    payload = json.dumps(
        {"citations": citations, "excerpts": excerpts},
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    )
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def _scope_compatible(
    left: DemoFinding | AuditCandidate | Mapping[str, Any],
    right: DemoFinding | AuditCandidate | Mapping[str, Any],
) -> bool:
    left_identity = finding_identity(left)
    right_identity = finding_identity(right)
    if not (
        bool(left_identity.toolchain)
        and left_identity.toolchain == right_identity.toolchain
        and bool(left_identity.mechanism)
        and left_identity.mechanism == right_identity.mechanism
    ):
        return False

    left_version = normalize_toolchain_version(
        str(_value(left, "toolchain_version", "")),
        toolchain=left_identity.toolchain,
    )
    right_version = normalize_toolchain_version(
        str(_value(right, "toolchain_version", "")),
        toolchain=right_identity.toolchain,
    )
    # A declared release is a lane constraint.  Missing legacy metadata remains
    # compatible, but two declared and different releases must never match.
    if left_version and right_version and left_version != right_version:
        return False

    def normalized_isas(value: object) -> set[str]:
        raw = _value(value, "isa", ())
        if raw in (None, ""):
            return set()
        values = (raw,) if isinstance(raw, str) else raw
        return {normalize_isa(str(item)) for item in values if str(item).strip()}

    left_isas = normalized_isas(left)
    right_isas = normalized_isas(right)
    # `generic` is an explicit wildcard.  As with version, absent metadata is
    # retained for backwards compatibility; conflicting declared ISAs are not.
    if left_isas and right_isas and not _isa_sets_compatible(left_isas, right_isas):
        return False
    return True


def _isa_pair_compatible(left: str, right: str) -> bool:
    if left == right or "generic" in {left, right}:
        return True
    for family, members in (
        ("x86", frozenset({"i386", "x86_64"})),
        ("riscv", frozenset({"riscv32", "riscv64"})),
    ):
        if left == family and right in members:
            return True
        if right == family and left in members:
            return True
    return False


def _isa_sets_compatible(left: set[str] | frozenset[str], right: set[str] | frozenset[str]) -> bool:
    return any(_isa_pair_compatible(lhs, rhs) for lhs in left for rhs in right)


def _token_set(value: object) -> frozenset[str]:
    root = str(
        _value(value, "root_cause", "")
        or _value(value, "invariant_violated", "")
    )
    return frozenset(_WORDS.findall(unicodedata.normalize("NFKC", root).casefold()))


def _token_set_score(
    left: DemoFinding | AuditCandidate | Mapping[str, Any],
    right: DemoFinding | AuditCandidate | Mapping[str, Any],
) -> float:
    if not _scope_compatible(left, right):
        return 0.0
    left_tokens = _token_set(left)
    right_tokens = _token_set(right)
    if not left_tokens or not right_tokens:
        return 0.0
    return len(left_tokens & right_tokens) / len(left_tokens | right_tokens)


def _frontmatter(text: str) -> tuple[str | None, str | None]:
    lines = text.lstrip("\ufeff").splitlines()
    if not lines or lines[0].strip() != "---":
        return None, "missing opening YAML front-matter delimiter"
    for index in range(1, len(lines)):
        if lines[index].strip() == "---":
            return "\n".join(lines[1:index]), None
    return None, "missing closing YAML front-matter delimiter"


def _parse_scalar(raw: str) -> Any:
    try:
        return yaml.safe_load(raw)
    except yaml.YAMLError:
        return raw.strip().strip("\"'")


def _recover_top_level(frontmatter: str) -> dict[str, Any]:
    """Recover top-level scalar/block fields without accepting malformed YAML."""

    lines = frontmatter.splitlines()
    payload: dict[str, Any] = {}
    index = 0
    while index < len(lines):
        match = _TOP_LEVEL.match(lines[index])
        if match is None:
            index += 1
            continue
        key, raw = match.groups()
        raw = (raw or "").strip()
        if raw in {">", "|-", "|", ">-"}:
            block: list[str] = []
            index += 1
            while index < len(lines) and not _TOP_LEVEL.match(lines[index]):
                block.append(lines[index].strip())
                index += 1
            payload[key] = " ".join(part for part in block if part)
            continue
        payload[key] = _parse_scalar(raw)
        index += 1
    return payload


def _finding_paths(root: Path) -> list[Path]:
    if root.is_file():
        return [root]
    findings_root = root / "findings" if (root / "findings").is_dir() else root
    direct = sorted(findings_root.glob("DREV-*/README.md"))
    return direct or sorted(findings_root.rglob("README.md"))


def _recover_invalid_payload(payload: Mapping[str, Any], message: str) -> dict[str, Any]:
    recovered = dict(payload)
    for field in (
        "id",
        "toolchain",
        "toolchain_version",
        "mechanism",
        "invariant_violated",
        "root_cause",
        "status",
        "source_path",
        "finding_key",
        "evidence_signature",
        "verified_evidence_signature",
    ):
        value = recovered.get(field, "")
        recovered[field] = value if isinstance(value, str) else str(value or "")
    raw_checker_ids = recovered.get("checker_ids", ())
    if isinstance(raw_checker_ids, str):
        raw_checker_ids = (raw_checker_ids,)
    elif not isinstance(raw_checker_ids, Sequence):
        raw_checker_ids = (raw_checker_ids,)
    recovered["checker_ids"] = [str(item) for item in raw_checker_ids if item is not None]
    schema_issues = recovered.get("schema_issues", ())
    if isinstance(schema_issues, str):
        schema_issues = (schema_issues,)
    elif not isinstance(schema_issues, Sequence):
        schema_issues = (schema_issues,)
    recovered["schema_issues"] = [str(item) for item in schema_issues if item]
    recovered["schema_issues"].append(message)
    recovered["schema_valid"] = False
    return recovered


def parse_demo_findings(root: str | Path) -> DemoFindingsParseResult:
    """Read demo front matter without modifying or archiving the corpus."""

    source = Path(root).expanduser().resolve()
    findings: list[DemoFinding] = []
    issues: list[ParseIssue] = []
    if not source.exists():
        return DemoFindingsParseResult(
            issues=[
                ParseIssue(
                    path=str(source),
                    message="demo findings path does not exist",
                    recoverable=False,
                )
            ]
        )
    for path in _finding_paths(source):
        try:
            text = path.read_text(encoding="utf-8")
        except OSError as exc:
            issues.append(
                ParseIssue(
                    path=str(path),
                    message=f"could not read finding: {exc}",
                    recoverable=False,
                )
            )
            continue
        frontmatter, delimiter_issue = _frontmatter(text)
        if delimiter_issue is not None or frontmatter is None:
            issues.append(
                ParseIssue(path=str(path), message=delimiter_issue or "invalid front matter")
            )
            continue
        try:
            loaded = yaml.safe_load(frontmatter)
            if not isinstance(loaded, Mapping):
                raise TypeError("YAML front matter is not a mapping")
            payload = dict(loaded)
        except (yaml.YAMLError, TypeError) as exc:
            mark = getattr(exc, "problem_mark", None)
            parse_message = f"malformed YAML front matter; recovered top-level fields: {exc}"
            payload = _recover_top_level(frontmatter)
            finding_id = str(payload.get("id") or "")
            issues.append(
                ParseIssue(
                    path=str(path),
                    line=getattr(mark, "line", -1) + 1 if mark is not None else None,
                    message=parse_message,
                    finding_id=finding_id,
                )
            )
            payload["schema_valid"] = False
            payload["schema_issues"] = [parse_message]
        payload["source_path"] = str(path)
        try:
            finding = DemoFinding.model_validate(payload)
        except (TypeError, ValidationError) as exc:
            schema_message = f"schema validation failed; recovered fields: {exc}"
            issues.append(
                ParseIssue(
                    path=str(path),
                    message=schema_message,
                    finding_id=str(payload.get("id") or ""),
                )
            )
            finding = DemoFinding.model_validate(
                _recover_invalid_payload(payload, schema_message)
            )
        missing_message = next(
            (
                message
                for message in finding.schema_issues
                if message.startswith("missing required")
            ),
            "",
        )
        if missing_message:
            issues.append(
                ParseIssue(
                    path=str(path),
                    message=missing_message,
                    finding_id=finding.id,
                )
            )
        findings.append(finding)
    return DemoFindingsParseResult(findings=findings, issues=issues)


def _as_findings(
    values: DemoFindingsParseResult | AuditReport | Sequence[DemoFinding | AuditCandidate],
) -> Sequence[DemoFinding | AuditCandidate]:
    if isinstance(values, DemoFindingsParseResult):
        return values.findings
    if isinstance(values, AuditReport):
        return values.candidates
    return values


def aggregate_findings(
    values: DemoFindingsParseResult | AuditReport | Sequence[DemoFinding | AuditCandidate],
) -> FindingAggregates:
    findings = _as_findings(values)
    toolchains: Counter[str] = Counter()
    mechanisms: Counter[str] = Counter()
    statuses: Counter[str] = Counter()
    poc: Counter[str] = Counter()
    for finding in findings:
        toolchains[normalize_toolchain(finding.toolchain) or "missing"] += 1
        mechanisms[normalize_mechanism(finding.mechanism) or "missing"] += 1
        status = getattr(finding, "status", "") or "missing"
        statuses[str(status)] += 1
        verified = finding.poc_verified
        poc["missing" if verified is None else str(verified).lower()] += 1
    return FindingAggregates(
        total=len(findings),
        by_toolchain=dict(sorted(toolchains.items())),
        by_mechanism=dict(sorted(mechanisms.items())),
        by_status=dict(sorted(statuses.items())),
        by_poc_verified=dict(sorted(poc.items())),
    )


def _policy_findings(
    findings: Sequence[DemoFinding], policy: BenchmarkPolicy
) -> tuple[list[DemoFinding], list[BenchmarkExclusion]]:
    included: list[DemoFinding] = []
    excluded: list[BenchmarkExclusion] = []
    for finding in findings:
        reasons: list[ExclusionReason] = []
        if not policy.include_retracted and finding.status.strip().casefold() == "retracted":
            reasons.append("retracted")
        if not policy.include_schema_invalid and not finding.schema_valid:
            reasons.append("schema_invalid")
        if policy.profile == "poc-verified" and finding.poc_verified is not True:
            reasons.append("poc_not_verified")
        if not reasons:
            included.append(finding)
        else:
            excluded.append(
                BenchmarkExclusion(
                    finding_id=finding.id or finding.source_path,
                    source_path=finding.source_path,
                    reasons=reasons,
                    schema_issues=list(finding.schema_issues),
                )
            )
    return included, excluded


def _scope_versions(scope: ParityScope) -> frozenset[str]:
    return frozenset(scope.version)


def _finding_isas(finding: DemoFinding) -> frozenset[str]:
    return frozenset(normalize_isa(value) for value in finding.isa if value.strip())


def _scope_matches(finding: DemoFinding, scope: ParityScope) -> bool:
    if scope.demo_ids:
        return finding.id in scope.demo_ids
    if scope.toolchains and normalize_toolchain(finding.toolchain) not in scope.toolchains:
        return False
    if scope.version:
        finding_version = normalize_toolchain_version(
            finding.toolchain_version, toolchain=finding.toolchain
        )
        if finding_version not in _scope_versions(scope):
            return False
    if scope.mechanisms and normalize_mechanism(finding.mechanism) not in scope.mechanisms:
        return False
    if scope.isas:
        finding_isas = _finding_isas(finding)
        if not finding_isas or not _isa_sets_compatible(finding_isas, frozenset(scope.isas)):
            return False
    return True


def _scope_findings(
    findings: Sequence[DemoFinding], scope: ParityScope | None
) -> tuple[list[DemoFinding], ParityScopeReport]:
    if scope is None:
        unscoped_findings = list(findings)
        return unscoped_findings, ParityScopeReport(
            status="not_requested",
            selected_demo_ids=[
                finding.id or finding.source_path for finding in unscoped_findings
            ],
            aggregates=aggregate_findings(unscoped_findings),
        )

    selected: list[DemoFinding] = []
    excluded_ids: list[str] = []
    for finding in findings:
        if _scope_matches(finding, scope):
            selected.append(finding)
        else:
            excluded_ids.append(finding.id or finding.source_path)
    selected_ids = [finding.id or finding.source_path for finding in selected]
    selected_id_set = set(selected_ids)
    unresolved = (
        [demo_id for demo_id in scope.demo_ids if demo_id not in selected_id_set]
        if scope.demo_ids
        else []
    )
    empty = not selected
    return selected, ParityScopeReport(
        requested=scope,
        status="not_applicable" if empty else "applicable",
        empty_scope=empty,
        not_applicable=empty,
        selected_demo_ids=selected_ids,
        excluded_demo_ids=excluded_ids,
        unresolved_demo_ids=unresolved,
        aggregates=aggregate_findings(selected),
    )


def _resolve_unique_edges(
    candidates: Sequence[AuditCandidate],
    demos: Sequence[DemoFinding],
    candidate_indices: set[int],
    demo_indices: set[int],
    edges: Mapping[tuple[int, int], float],
    *,
    match_method: str,
) -> tuple[
    list[ParityMatch],
    list[AmbiguousParityMatch],
    set[int],
    set[int],
    set[int],
]:
    """Resolve isolated one-to-one edges; quarantine all ambiguous components."""

    candidate_edges: defaultdict[int, set[int]] = defaultdict(set)
    demo_edges: defaultdict[int, set[int]] = defaultdict(set)
    for (candidate_index, demo_index), _score in edges.items():
        if candidate_index in candidate_indices and demo_index in demo_indices:
            candidate_edges[candidate_index].add(demo_index)
            demo_edges[demo_index].add(candidate_index)

    matches: list[ParityMatch] = []
    matched_candidates: set[int] = set()
    matched_demos: set[int] = set()
    for candidate_index in sorted(candidate_edges):
        targets = candidate_edges[candidate_index]
        if len(targets) != 1:
            continue
        demo_index = next(iter(targets))
        if len(demo_edges[demo_index]) != 1:
            continue
        matched_candidates.add(candidate_index)
        matched_demos.add(demo_index)
        matches.append(
            ParityMatch(
                candidate_index=candidate_index,
                demo_id=demos[demo_index].id or demos[demo_index].source_path,
                identity=finding_identity(demos[demo_index]),
                match_method=match_method,
                score=edges[(candidate_index, demo_index)],
            )
        )

    remaining_edges = {
        pair: score
        for pair, score in edges.items()
        if pair[0] not in matched_candidates and pair[1] not in matched_demos
    }
    ambiguous: list[AmbiguousParityMatch] = []
    visited_candidates: set[int] = set()
    visited_demos: set[int] = set()
    for start_candidate, _ in sorted(remaining_edges):
        if start_candidate in visited_candidates:
            continue
        component_candidates: set[int] = set()
        component_demos: set[int] = set()
        pending_candidates = [start_candidate]
        while pending_candidates:
            candidate_index = pending_candidates.pop()
            if candidate_index in component_candidates:
                continue
            component_candidates.add(candidate_index)
            for demo_index in candidate_edges[candidate_index] - matched_demos:
                if demo_index not in demo_indices:
                    continue
                component_demos.add(demo_index)
                for neighbor in demo_edges[demo_index] - matched_candidates:
                    if neighbor not in component_candidates:
                        pending_candidates.append(neighbor)
        if not component_demos:
            continue
        visited_candidates.update(component_candidates)
        visited_demos.update(component_demos)
        component_scores = [
            score
            for (candidate_index, demo_index), score in remaining_edges.items()
            if candidate_index in component_candidates and demo_index in component_demos
        ]
        ambiguous.append(
            AmbiguousParityMatch(
                demo_ids=[
                    demos[index].id or demos[index].source_path
                    for index in sorted(component_demos)
                ],
                candidate_indices=sorted(component_candidates),
                match_method=match_method,
                score=max(component_scores),
                edges=[
                    AmbiguousParityEdge(
                        candidate_index=candidate_index,
                        demo_id=demos[demo_index].id or demos[demo_index].source_path,
                        score=score,
                    )
                    for (candidate_index, demo_index), score in sorted(
                        remaining_edges.items()
                    )
                    if candidate_index in component_candidates
                    and demo_index in component_demos
                ],
            )
        )

    handled_candidates = matched_candidates | visited_candidates
    handled_demos = matched_demos | visited_demos
    return matches, ambiguous, handled_candidates, handled_demos, matched_demos


def _tier_edges(
    candidates: Sequence[AuditCandidate],
    demos: Sequence[DemoFinding],
    candidate_indices: set[int],
    demo_indices: set[int],
    *,
    tier: str,
    token_similarity_threshold: float,
) -> dict[tuple[int, int], float]:
    edges: dict[tuple[int, int], float] = {}
    for candidate_index in sorted(candidate_indices):
        candidate = candidates[candidate_index]
        for demo_index in sorted(demo_indices):
            demo = demos[demo_index]
            if not _scope_compatible(candidate, demo):
                continue
            score = 0.0
            if tier == "finding_key":
                text_key = _finding_key(candidate)
                score = 1.0 if text_key and text_key == _finding_key(demo) else 0.0
            elif tier == "checker_ids":
                candidate_ids = _checker_ids(candidate)
                demo_ids = _checker_ids(demo)
                score = 1.0 if candidate_ids and candidate_ids == demo_ids else 0.0
            elif tier == "verified_evidence_signature":
                signature = _verified_evidence_signature(candidate)
                score = (
                    1.0
                    if signature and signature == _verified_evidence_signature(demo)
                    else 0.0
                )
            elif tier == "exact":
                score = (
                    1.0
                    if finding_identity(candidate).key() == finding_identity(demo).key()
                    else 0.0
                )
            elif tier == "token_set":
                score = _token_set_score(candidate, demo)
                if score < token_similarity_threshold:
                    score = 0.0
            else:  # pragma: no cover - internal programming error
                raise ValueError(f"unknown parity tier: {tier}")
            if score:
                edges[(candidate_index, demo_index)] = score
    return edges


def evaluate_demo_parity(
    candidates: AuditReport | Sequence[AuditCandidate],
    demo: DemoFindingsParseResult | Sequence[DemoFinding] | str | Path,
    *,
    threshold: float | None = None,
    threshold_metric: ThresholdMetric = "recall",
    threshold_blocking: bool = False,
    profile: ParityProfile | None = None,
    benchmark_policy: BenchmarkPolicy | Mapping[str, Any] | None = None,
    scope: ParityScope | Mapping[str, Any] | None = None,
    token_similarity_threshold: float = _DEFAULT_TOKEN_SIMILARITY_THRESHOLD,
) -> ParityResult:
    """Layered, deterministic one-to-one parity matching.

    Stable finding keys, checker IDs, and verified evidence signatures are
    evaluated first.  The legacy exact normalized identity remains supported,
    followed by an auditable token-set score.  Any tier that creates a
    one-to-many or many-to-one component is reported as ambiguous and is not
    counted as a match.  No embeddings, LLMs, or private finding text are sent
    to workers; this function runs only on the orchestrator after audit output.

    ``demo-workset`` is the engineering parity/superset regression set.
    ``poc-verified`` is its stronger-evidence subset and is not labelled as a
    formal paper result. ``recall`` is therefore the default threshold metric:
    it directly measures coverage of the selected demo superset.

    ``threshold_blocking`` records caller-consumable policy but never changes
    stage status or raises here. Explicit ``threshold_metric="f1"`` remains
    supported for compatibility.
    """

    candidate_items = list(
        candidates.candidates if isinstance(candidates, AuditReport) else candidates
    )
    if isinstance(demo, (str, Path)):
        parsed = parse_demo_findings(demo)
    elif isinstance(demo, DemoFindingsParseResult):
        parsed = demo
    else:
        parsed = DemoFindingsParseResult(findings=list(demo))
    policy = BenchmarkPolicy.model_validate(benchmark_policy or {})
    if profile is not None:
        if benchmark_policy is not None and policy.profile != profile:
            raise ValueError(
                "profile and benchmark_policy.profile must select the same profile"
            )
        policy = BenchmarkPolicy.model_validate(
            {**policy.model_dump(mode="python"), "profile": profile}
        )
    profile_findings, exclusions = _policy_findings(parsed.findings, policy)
    resolved_scope = (
        scope if isinstance(scope, ParityScope) else ParityScope.model_validate(scope)
        if scope is not None
        else None
    )
    benchmark_findings, scope_report = _scope_findings(
        profile_findings, resolved_scope
    )
    excluded_ids = [exclusion.finding_id for exclusion in exclusions]
    exclusions_by_reason = {
        reason: [
            exclusion.finding_id
            for exclusion in exclusions
            if reason in exclusion.reasons
        ]
        for reason in ("schema_invalid", "retracted", "poc_not_verified")
    }
    available_candidates = set(range(len(candidate_items)))
    available_demos = set(range(len(benchmark_findings)))
    matched_candidate_indices: set[int] = set()
    matched_demo_indices: set[int] = set()
    matches: list[ParityMatch] = []
    ambiguous: list[AmbiguousParityMatch] = []
    for tier in (
        "finding_key",
        "checker_ids",
        "verified_evidence_signature",
        "exact",
        "token_set",
    ):
        edges = _tier_edges(
            candidate_items,
            benchmark_findings,
            available_candidates,
            available_demos,
            tier=tier,
            token_similarity_threshold=token_similarity_threshold,
        )
        if not edges:
            continue
        (
            tier_matches,
            tier_ambiguous,
            handled_candidates,
            handled_demos,
            tier_matched_demos,
        ) = (
            _resolve_unique_edges(
                candidate_items,
                benchmark_findings,
                available_candidates,
                available_demos,
                edges,
                match_method=tier,
            )
        )
        matches.extend(tier_matches)
        ambiguous.extend(tier_ambiguous)
        available_candidates -= handled_candidates
        available_demos -= handled_demos
        matched_candidate_indices.update(
            match.candidate_index for match in tier_matches
        )
        matched_demo_indices.update(tier_matched_demos)

    matches.sort(key=lambda match: (match.candidate_index, match.demo_id))
    missing = [
        benchmark_findings[index].id or benchmark_findings[index].source_path
        for index in sorted(set(range(len(benchmark_findings))) - matched_demo_indices)
    ]
    unmatched = sorted(set(range(len(candidate_items))) - matched_candidate_indices)
    precision = len(matches) / len(candidate_items) if candidate_items else 0.0
    recall = len(matches) / len(benchmark_findings) if benchmark_findings else 0.0
    f1 = 2 * precision * recall / (precision + recall) if precision + recall else 0.0
    raw_aggregates = aggregate_findings(parsed)
    profile_aggregates = aggregate_findings(profile_findings)
    scoped_aggregates = aggregate_findings(benchmark_findings)
    return ParityResult(
        matches=matches,
        ambiguous_matches=ambiguous,
        missing_demo_ids=missing,
        unmatched_candidate_indices=unmatched,
        raw_corpus_aggregates=raw_aggregates,
        profile_aggregates=profile_aggregates,
        scoped_aggregates=scoped_aggregates,
        demo_aggregates=profile_aggregates,
        candidate_aggregates=aggregate_findings(candidate_items),
        parse_issues=parsed.issues,
        precision=precision,
        recall=recall,
        f1=f1,
        raw_total=len(parsed.findings),
        profile_total=len(profile_findings),
        scoped_total=len(benchmark_findings),
        benchmark_total=len(benchmark_findings),
        profile=policy.profile,
        exclusions=exclusions,
        exclusion_reasons=exclusions_by_reason,
        excluded_ids=excluded_ids,
        benchmark_policy=policy,
        scope=resolved_scope,
        scope_report=scope_report,
        threshold=threshold,
        threshold_metric=threshold_metric,
        threshold_blocking=threshold_blocking,
        token_similarity_threshold=token_similarity_threshold,
    )


# Compatibility aliases used by early experiment scripts.
parse_findings = parse_demo_findings
compute_aggregates = aggregate_findings
compare_demo_parity = evaluate_demo_parity
