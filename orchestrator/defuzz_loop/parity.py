"""Orchestrator-only parsing and parity evaluation for the demo corpus.

Nothing in this module is imported by prompt_bundle. This keeps the read side
of the private findings corpus structurally separate from worker prompt
construction. Parsing is deliberately tolerant: malformed YAML is reported
and useful top-level identity fields are recovered when possible.
"""

from __future__ import annotations

import re
import unicodedata
from collections import Counter, defaultdict
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

import yaml
from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from .audit_schema import AuditCandidate, AuditReport

_TOP_LEVEL = re.compile(r"^([A-Za-z_][A-Za-z0-9_-]*):(?:[ \t]*(.*))?$")
_MARKDOWN = re.compile(r"[*_#>\[\]{}()]+")
_WORDS = re.compile(r"[a-z0-9]+")

_TOOLCHAIN_ALIASES = {
    "clang": "llvm",
    "llvm-clang": "llvm",
    "gnu-gcc": "gcc",
    "gnu-ld": "binutils",
}
_MECHANISM_ALIASES = {
    "canary": "stack-protector",
    "stack-canary": "stack-protector",
    "ssp": "stack-protector",
    "stack-clash": "stack-clash-protection",
    "fortify": "fortify-source",
    "fortify-source-3": "fortify-source",
    "cet-ibt": "ibt",
    "endbr-ibt": "ibt",
    "pointer-authentication": "pac",
    "return-address-signing": "pac",
    "ftrivial-auto-var-init": "auto-var-init",
}


class ParseIssue(BaseModel):
    path: str
    message: str
    line: int | None = None
    recoverable: bool = True


class DemoFinding(BaseModel):
    model_config = ConfigDict(extra="allow")

    id: str = ""
    toolchain: str = ""
    toolchain_version: str = ""
    mechanism: str = ""
    invariant_violated: str = ""
    root_cause: str = ""
    status: str = ""
    poc_verified: bool | None = None
    source_path: str = ""

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


class ParityResult(BaseModel):
    schema_version: int = 1
    matches: list[ParityMatch] = Field(default_factory=list)
    missing_demo_ids: list[str] = Field(default_factory=list)
    unmatched_candidate_indices: list[int] = Field(default_factory=list)
    demo_aggregates: FindingAggregates = Field(default_factory=FindingAggregates)
    candidate_aggregates: FindingAggregates = Field(default_factory=FindingAggregates)
    parse_issues: list[ParseIssue] = Field(default_factory=list)
    precision: float = 0.0
    recall: float = 0.0
    f1: float = 0.0
    match_count: int = Field(default=0, ge=0)
    threshold: float | None = Field(default=None, ge=0.0, le=1.0)
    threshold_pass: bool | None = None

    @model_validator(mode="after")
    def _derive_report_fields(self) -> ParityResult:
        """Keep serialized report fields consistent with the detailed matches."""

        self.match_count = len(self.matches)
        self.threshold_pass = self.f1 >= self.threshold if self.threshold is not None else None
        return self

    @property
    def matched_count(self) -> int:
        """Compatibility spelling retained for early experiment scripts."""

        return self.match_count


def parity_metrics(result: ParityResult) -> dict[str, int | float | bool | None]:
    """Return the stable summary fields copied into ``StageResult.metrics``.

    A failed threshold is an experiment result, not an execution error.  The
    caller should report ``threshold_pass`` without changing stage status.
    """

    return {
        "precision": result.precision,
        "recall": result.recall,
        "f1": result.f1,
        "match_count": result.match_count,
        "threshold": result.threshold,
        "threshold_pass": result.threshold_pass,
    }


def normalize_toolchain(value: str) -> str:
    normalized = "-".join(_WORDS.findall(unicodedata.normalize("NFKC", value).casefold()))
    return _TOOLCHAIN_ALIASES.get(normalized, normalized)


def normalize_mechanism(value: str) -> str:
    normalized = "-".join(_WORDS.findall(unicodedata.normalize("NFKC", value).casefold()))
    return _MECHANISM_ALIASES.get(normalized, normalized)


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
            issues.append(
                ParseIssue(
                    path=str(path),
                    line=getattr(mark, "line", -1) + 1 if mark is not None else None,
                    message=f"malformed YAML front matter; recovered top-level fields: {exc}",
                )
            )
            payload = _recover_top_level(frontmatter)
        payload["source_path"] = str(path)
        finding = DemoFinding.model_validate(payload)
        missing = [
            field
            for field in ("id", "toolchain", "mechanism", "invariant_violated", "status")
            if not getattr(finding, field).strip()
        ]
        if missing:
            issues.append(
                ParseIssue(path=str(path), message=f"missing required fields: {', '.join(missing)}")
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


def evaluate_demo_parity(
    candidates: AuditReport | Sequence[AuditCandidate],
    demo: DemoFindingsParseResult | Sequence[DemoFinding] | str | Path,
    *,
    threshold: float | None = None,
) -> ParityResult:
    """One-to-one parity match by normalized root cause, toolchain, mechanism.

    When supplied, ``threshold`` is evaluated against F1 and recorded in the
    report.  It intentionally does not raise when parity falls short.
    """

    candidate_items = list(_as_findings(candidates))
    if isinstance(demo, (str, Path)):
        parsed = parse_demo_findings(demo)
    elif isinstance(demo, DemoFindingsParseResult):
        parsed = demo
    else:
        parsed = DemoFindingsParseResult(findings=list(demo))

    available: dict[tuple[str, str, str], list[int]] = defaultdict(list)
    for candidate_index, candidate in enumerate(candidate_items):
        available[finding_identity(candidate).key()].append(candidate_index)

    matches: list[ParityMatch] = []
    missing: list[str] = []
    consumed: set[int] = set()
    for finding in parsed.findings:
        identity = finding_identity(finding)
        bucket = available.get(identity.key(), [])
        index = next((item for item in bucket if item not in consumed), None)
        if index is None:
            missing.append(finding.id or finding.source_path)
            continue
        consumed.add(index)
        matches.append(
            ParityMatch(candidate_index=index, demo_id=finding.id, identity=identity)
        )

    unmatched = [index for index in range(len(candidate_items)) if index not in consumed]
    precision = len(matches) / len(candidate_items) if candidate_items else 0.0
    recall = len(matches) / len(parsed.findings) if parsed.findings else 0.0
    f1 = 2 * precision * recall / (precision + recall) if precision + recall else 0.0
    return ParityResult(
        matches=matches,
        missing_demo_ids=missing,
        unmatched_candidate_indices=unmatched,
        demo_aggregates=aggregate_findings(parsed),
        candidate_aggregates=aggregate_findings(candidate_items),
        parse_issues=parsed.issues,
        precision=precision,
        recall=recall,
        f1=f1,
        threshold=threshold,
    )


# Compatibility aliases used by early experiment scripts.
parse_findings = parse_demo_findings
compute_aggregates = aggregate_findings
compare_demo_parity = evaluate_demo_parity
