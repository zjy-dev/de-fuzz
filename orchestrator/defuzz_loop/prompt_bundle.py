"""Leak-resistant prompt bundles for Part III audit workers.

Only the orchestrator may inspect the reference project's ``findings`` corpus.
This module deliberately has no API for doing so: worker bundles are assembled
from the canonical Claude doctrine, one family block from the full-review
template, scoped historical bugs/invariant surveys, and (for the full variant)
explicitly supplied checker/oracle artifacts.
"""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Iterable, Mapping, Sequence
from pathlib import Path
from typing import Any

from pydantic import BaseModel, ConfigDict, Field

from .audit_schema import (
    AuditFamily,
    AuditVariant,
    audit_report_json_schema,
    canonical_family,
    normalize_mechanism,
)

_DOCTRINE_PATH = Path(".claude/agents/defend-reviewer.md")
_DOCTRINE_MIRRORS = (
    Path(".claude/agents/defend-reviewer.md"),
    Path(".trae/agents/defend-reviewer.md"),
    Path(".opencode/agents/defend-reviewer.md"),
)
_FULL_REVIEW_PATH = Path("docs/prompts/full-review.md")
_DREV_ID = re.compile(r"\bDREV-\d{4}-\d{3,}\b", re.IGNORECASE)
_FINDINGS_PATH = re.compile(r"(?i)(?:^|[\s`'(\[])findings/(?:DREV-[^\s`)'\]]+)?")
_NEUTRAL_FAMILY = AuditFamily(
    key="neutral", name="general-source-audit", mechanisms=()
)

_SURVEY_FILES: dict[str, tuple[str, ...]] = {
    "stack-protector": ("stack-canary.md",),
    "stack-clash-protection": ("stack-clash-protection.md",),
    "fortify-source": ("fortify-source.md",),
    "strict-flex-arrays": ("bounds-safety.md",),
    "zero-init-padding": ("auto-var-init.md",),
    "source-annotations": ("bounds-safety.md",),
    "codegen": ("gcc-llvm-defense-invariant-source-survey.md",),
    "cet": ("endbr-ibt.md", "shadow-stack.md"),
    "ibt": ("endbr-ibt.md",),
    "shstk": ("shadow-stack.md",),
    "bti": ("bti.md",),
    "pac": ("pointer-authentication.md",),
    "cmse": (),
    "riscv-cfi": ("riscv-cfi.md",),
    "ret-hardening": ("gcc-llvm-defense-invariant-source-survey.md",),
    "asan": ("sanitizers.md",),
    "ubsan": ("sanitizers.md",),
    "tsan": ("sanitizers.md",),
    "lsan": ("sanitizers.md",),
    "auto-var-init": ("auto-var-init.md",),
    "fhardened": ("hardened.md",),
    "zero-call-used-regs": ("zero-call-used-regs.md",),
}

_HISTORICAL_MECHANISM_DIRS: dict[str, tuple[str, ...]] = {
    "ibt": ("cet",),
    "pac": ("return-address-signing",),
    "ret-hardening": ("codegen",),
    # The corpus currently keeps these bugs under codegen until dedicated
    # mechanism directories exist.
    "zero-call-used-regs": ("codegen",),
}

_MECHANISM_PROMPT_GUIDANCE: dict[str, str] = {
    "codegen": (
        "Audit security-relevant backend lowering and late optimization. Check that "
        "control-flow, unwind, and hardening metadata remain consistent with emitted code."
    ),
    "zero-call-used-regs": (
        "Audit the documented -fzero-call-used-regs contract across applicable exit forms, "
        "target backends, function attributes, and interactions with other epilogue passes."
    ),
    "riscv-cfi": (
        "Audit RISC-V Zicfilp/Zicfiss landing-pad and shadow-stack semantics across compiler, "
        "assembler, linker metadata, and mixed-mode code."
    ),
    "ibt": (
        "Audit Intel CET/IBT target marking and property propagation across all indirect "
        "control-flow entry forms and link stages."
    ),
    "ret-hardening": (
        "Audit return-thunk, speculation-barrier, and LVI return-hardening semantics across "
        "late lowering, target-specific epilogues, and linker-visible code paths."
    ),
}

# Compatibility name retained for callers of the first prompt-overlay API; the
# implementation and alias table now live exclusively in audit_schema.
normalize_prompt_mechanism = normalize_mechanism


class AuditVisibilityPolicy(BaseModel):
    """Worker-visible capabilities for a Part III comparison arm."""

    model_config = ConfigDict(frozen=True)

    variant: AuditVariant
    include_doctrine: bool
    include_structured_workflow: bool
    include_historical_bugs: bool
    include_invariants: bool
    include_online_oracle: bool
    findings_access: str = "denied"


VARIANT_POLICIES: dict[AuditVariant, AuditVisibilityPolicy] = {
    AuditVariant.FULL: AuditVisibilityPolicy(
        variant=AuditVariant.FULL,
        include_doctrine=True,
        include_structured_workflow=True,
        include_historical_bugs=True,
        include_invariants=True,
        include_online_oracle=True,
    ),
    AuditVariant.WITHOUT_ORACLE: AuditVisibilityPolicy(
        variant=AuditVariant.WITHOUT_ORACLE,
        include_doctrine=True,
        include_structured_workflow=True,
        include_historical_bugs=True,
        include_invariants=True,
        include_online_oracle=False,
    ),
    AuditVariant.BARE_AGENT: AuditVisibilityPolicy(
        variant=AuditVariant.BARE_AGENT,
        include_doctrine=False,
        include_structured_workflow=False,
        include_historical_bugs=False,
        include_invariants=False,
        include_online_oracle=False,
    ),
}


def policy_for_variant(value: str | AuditVariant) -> AuditVisibilityPolicy:
    try:
        variant = AuditVariant(value)
    except ValueError as exc:
        raise ValueError(
            "Part III supports only full, without-oracle, and bare-agent; "
            "without-rag is a Part I generation policy"
        ) from exc
    return VARIANT_POLICIES[variant]


class PromptDocument(BaseModel):
    model_config = ConfigDict(frozen=True)

    path: str
    content: str
    sha256: str
    kind: str


class WorkerPromptBundle(BaseModel):
    model_config = ConfigDict(frozen=True)

    family: AuditFamily
    variant: AuditVariant
    prompt: str
    documents: tuple[PromptDocument, ...] = ()
    sha256: str
    findings_access: str = "denied"
    metadata: dict[str, Any] = Field(default_factory=dict)

    @property
    def worker_bundle_sha256(self) -> str:
        return self.sha256


def _sha256(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def _normalized_doctrine_body(path: Path) -> str:
    text = path.read_text(encoding="utf-8")
    if path.suffix == ".toml":
        match = re.search(r'(?ms)^developer_instructions\s*=\s*"""(.*)"""\s*$', text)
        if match is None:
            raise ValueError(f"cannot extract doctrine body from {path}")
        return match.group(1).strip() + "\n"
    if text.startswith("---\n"):
        marker = text.find("\n---\n", 4)
        if marker < 0:
            raise ValueError(f"unterminated doctrine front matter: {path}")
        text = text[marker + 5 :]
    return text.strip() + "\n"


def audit_doctrine_parity(reference_root: str | Path) -> dict[str, Any]:
    """Report wrapper-normalized hashes without mutating the demo checkout."""

    root = Path(reference_root).expanduser().resolve()
    paths = [root / relative for relative in _DOCTRINE_MIRRORS]
    codex = root / ".codex/agents/defend-reviewer.toml"
    if codex.is_file():
        paths.append(codex)
    hashes = {
        path.relative_to(root).as_posix(): _sha256(_normalized_doctrine_body(path))
        for path in paths
        if path.is_file()
    }
    canonical_key = _DOCTRINE_PATH.as_posix()
    canonical_hash = hashes.get(canonical_key)
    mismatches = [key for key, value in hashes.items() if value != canonical_hash]
    return {
        "canonical": canonical_key,
        "canonical_sha256": canonical_hash,
        "body_sha256": hashes,
        "mismatches": mismatches,
        "all_match": bool(hashes) and not mismatches,
    }


def _is_forbidden_path(path: Path) -> bool:
    lowered = tuple(part.lower() for part in path.parts)
    if "findings" in lowered or path.name.lower() == "findings.md":
        return True
    if "reports" in lowered and any(part.lower().startswith("drev-") for part in path.parts):
        return True
    return False


def assert_worker_safe_path(path: str | Path) -> None:
    """Reject all output-corpus/report paths before any file is read."""

    candidate = Path(path)
    if _is_forbidden_path(candidate):
        raise ValueError(f"worker input path is forbidden: {candidate}")


def _redact_findings_references(content: str) -> str:
    # Graduated historical entries occasionally retain internal DREV links. They
    # are permitted inputs, but those output-corpus breadcrumbs are not.
    content = _DREV_ID.sub("[REDACTED-OUTPUT-ID]", content)
    return _FINDINGS_PATH.sub(" [REDACTED-OUTPUT-PATH]", content)


def assert_no_findings_leak(text: str) -> None:
    """Fail closed if concrete output-corpus material reached a worker."""

    match = _DREV_ID.search(text)
    if match:
        raise ValueError(f"worker prompt leaks a concrete finding id: {match.group(0)}")
    # A generic doctrine mention of the output corpus is acceptable. Concrete
    # paths (with an actual DREV suffix) have already been redacted above.
    if re.search(r"(?i)findings/DREV-\d", text):
        raise ValueError("worker prompt leaks a concrete findings path")


def _redact_root(content: str, root: Path) -> str:
    """Keep an embedded document from disclosing its host checkout path."""

    return content.replace(str(root.resolve()), "[REFERENCE_ROOT]")


def _document(root: Path, path: Path, kind: str) -> PromptDocument:
    resolved_root = root.resolve()
    resolved = path.resolve()
    assert_worker_safe_path(resolved)
    try:
        relative = resolved.relative_to(resolved_root).as_posix()
    except ValueError as exc:
        raise ValueError(f"worker document is outside reference root: {resolved}") from exc
    content = _redact_root(
        _redact_findings_references(resolved.read_text(encoding="utf-8")),
        resolved_root,
    )
    assert_no_findings_leak(content)
    return PromptDocument(path=relative, content=content, sha256=_sha256(content), kind=kind)


def _external_document(
    path: Path, kind: str, *, display_path: str, redacted_roots: Sequence[Path] = ()
) -> PromptDocument:
    """Load a caller-authorized artifact while retaining output-corpus denial."""

    resolved = path.expanduser().resolve()
    assert_worker_safe_path(resolved)
    content = _redact_findings_references(resolved.read_text(encoding="utf-8"))
    for root in redacted_roots:
        content = _redact_root(content, root)
    assert_no_findings_leak(content)
    return PromptDocument(
        path=display_path, content=content, sha256=_sha256(content), kind=kind
    )

def _family_section(template: str, family: AuditFamily) -> str:
    pattern = re.compile(
        rf"(?ms)^### SUBAGENT {re.escape(family.key)}\b.*?"
        r"(?=^### SUBAGENT [A-E]\b|^## Uniform subagent instructions)"
    )
    match = pattern.search(template)
    if match is None:
        raise ValueError(f"full-review template has no SUBAGENT {family.key} block")
    uniform = re.search(
        r"(?ms)^## Uniform subagent instructions.*?(?=^## Phase 2:|\Z)", template
    )
    suffix = uniform.group(0) if uniform else ""
    return _redact_findings_references(match.group(0).strip() + "\n\n" + suffix.strip())


def _scoped_documents(
    root: Path,
    family: AuditFamily,
    toolchains: Sequence[str],
    mechanisms: Sequence[str] | None = None,
) -> list[tuple[Path, str]]:
    paths: list[tuple[Path, str]] = []
    invariant_root = root / "docs" / "invariants"
    index = invariant_root / "README.md"
    if index.is_file():
        paths.append((index, "invariant-index"))
    scoped_mechanisms = tuple(mechanisms or family.mechanisms)
    survey_names = {
        name for mechanism in scoped_mechanisms for name in _SURVEY_FILES.get(mechanism, ())
    }
    for name in sorted(survey_names):
        path = invariant_root / name
        if path.is_file():
            paths.append((path, "invariant"))

    bug_root = root / "docs" / "bugs"
    for toolchain in sorted(set(toolchains)):
        for mechanism in scoped_mechanisms:
            directory_names = (mechanism, *_HISTORICAL_MECHANISM_DIRS.get(mechanism, ()))
            for directory_name in dict.fromkeys(directory_names):
                directory = bug_root / toolchain / directory_name
                if directory.is_dir():
                    paths.extend(
                        (path, "historical-bug")
                        for path in sorted(directory.rglob("*.md"))
                    )
    cross = bug_root / "cross"
    if cross.is_dir():
        paths.extend((path, "historical-bug") for path in sorted(cross.rglob("*.md")))
    return list(dict.fromkeys(paths))


def _render_documents(documents: Iterable[PromptDocument]) -> str:
    chunks = []
    for document in documents:
        chunks.append(
            f"<document kind={document.kind!r} path={document.path!r} "
            f"sha256={document.sha256!r}>\n{document.content.rstrip()}\n</document>"
        )
    return "\n\n".join(chunks)


def _mechanism_guidance_document(mechanisms: Sequence[str]) -> PromptDocument | None:
    lines = [
        f"- {mechanism}: {_MECHANISM_PROMPT_GUIDANCE[mechanism]}"
        for mechanism in mechanisms
        if mechanism in _MECHANISM_PROMPT_GUIDANCE
    ]
    if not lines:
        return None
    content = "Mechanism scope overlay for this worker:\n" + "\n".join(lines)
    return PromptDocument(
        path="builtin/mechanism-scope-overlay",
        content=content,
        sha256=_sha256(content),
        kind="mechanism-guidance",
    )


def _generated_invariant_document(
    records: Sequence[Mapping[str, Any]], mechanisms: Sequence[str]
) -> PromptDocument | None:
    """Render the Part I handoff without leaking its host path or raw provenance."""

    wanted = {normalize_mechanism(value) for value in mechanisms}
    safe_fields = (
        "invariant_id",
        "statement",
        "observation",
        "compiler",
        "version",
        "target",
        "mechanism",
        "protected_asset",
        "activation_condition",
        "version_sensitivity",
        "falsifiability",
        "grounding",
        "novelty",
    )
    selected = []
    for record in records:
        mechanism = normalize_mechanism(str(record.get("mechanism", "")))
        if wanted and mechanism not in wanted:
            continue
        item = {key: record[key] for key in safe_fields if key in record}
        item["mechanism"] = mechanism
        selected.append(item)
    if not selected:
        return None
    content = json.dumps(
        {"schema_version": 1, "accepted_invariants": selected},
        ensure_ascii=False,
        indent=2,
        sort_keys=True,
    )
    assert_no_findings_leak(content)
    return PromptDocument(
        path="pipeline/accepted-invariants.json",
        content=content,
        sha256=_sha256(content),
        kind="generated-invariants",
    )


def build_worker_prompt_bundle(
    reference_root: str | Path,
    family: str | AuditFamily,
    variant: str | AuditVariant = AuditVariant.FULL,
    *,
    source_roots: Sequence[str | Path] = (),
    toolchains: Sequence[str] = ("gcc", "llvm"),
    toolchain_versions: Mapping[str, str] | None = None,
    mechanisms: Sequence[str] | None = None,
    isas: Sequence[str] = (),
    discovered: str = "",
    hypotheses: str = "",
    oracle_documents: Sequence[str | Path] = (),
    extra_documents: Sequence[str | Path] = (),
    generated_invariants: Sequence[Mapping[str, Any]] = (),
) -> WorkerPromptBundle:
    """Build one self-contained worker prompt under a strict input allowlist."""

    policy = policy_for_variant(variant)
    root = Path(reference_root).expanduser().resolve()
    selected_family = (
        _NEUTRAL_FAMILY
        if policy.variant is AuditVariant.BARE_AGENT
        else canonical_family(family)
    )
    requested_mechanisms = {
        normalize_mechanism(mechanism) for mechanism in mechanisms or ()
    }
    family_mechanisms = tuple(
        dict.fromkeys(normalize_mechanism(item) for item in selected_family.mechanisms)
    )
    selected_mechanisms = tuple(
        mechanism
        for mechanism in family_mechanisms
        if not requested_mechanisms or mechanism in requested_mechanisms
    )
    if policy.variant is not AuditVariant.BARE_AGENT and not selected_mechanisms:
        raise ValueError(f"family {selected_family.key} has no mechanisms in the requested scope")

    documents: list[PromptDocument] = []
    source_labels = ["."] if len(source_roots) <= 1 else [
        f"source-{index}" for index, _ in enumerate(source_roots, start=1)
    ]
    if policy.variant is AuditVariant.BARE_AGENT:
        instructions = [
            "You are an independent general code-audit worker.",
            "Inspect the source tree in the current working directory for security-relevant "
            "compiler implementation defects. Choose scope and inspection strategy using "
            "ordinary source-analysis judgment.",
            f"Source locations inside the working directory: {', '.join(source_labels)}.",
            f"Toolchains under review: {', '.join(toolchains) or 'infer from source'}.",
            f"Target ISAs: {', '.join(isas) or 'all applicable ISAs'}.",
            "Use general source-inspection tools and your own audit judgment.",
            "Do not enumerate, read, search, or quote findings/, FINDINGS.md, reports/, or "
            "prior audit records. Treat accidental exposure as a tainted run.",
            "Return one JSON object with an issues array and an optional coverage_gaps array. "
            "For each issue, include enough source locations, evidence, reproduction details, "
            "impact, and validation state for independent evaluation. Do not write or archive "
            "reports.",
        ]
    else:
        instructions = [
            "You are an independent compiler-defense audit worker.",
            f"Assigned family: {selected_family.key} ({selected_family.name}).",
            f"Mechanisms: {', '.join(selected_mechanisms)}.",
            "Target source trees inside the current working directory: "
            f"{', '.join(source_labels)}.",
            f"Toolchain versions: {dict(toolchain_versions or {})}.",
            f"Target ISAs: {', '.join(isas) or 'all applicable ISAs'}.",
            f"Discovery date: {discovered or 'record the current run date'}.",
            "Do not enumerate, read, search, or quote findings/, FINDINGS.md, reports/DREV-*, "
            "or any prior DREV record. Treat accidental exposure as a tainted run.",
            "Return one JSON object conforming to the supplied AuditReport schema. Do not "
            "write or archive findings.",
        ]
    if hypotheses.strip():
        instructions.append(f"Per-review hypotheses:\n{hypotheses.strip()}")

    if policy.include_doctrine:
        doctrine = _document(root, root / _DOCTRINE_PATH, "reviewer-doctrine")
        documents.append(doctrine)
    if policy.include_structured_workflow:
        full_review_path = root / _FULL_REVIEW_PATH
        template = full_review_path.read_text(encoding="utf-8")
        family_text = _redact_root(_family_section(template, selected_family), root)
        assert_no_findings_leak(family_text)
        documents.append(
            PromptDocument(
                path=f"{_FULL_REVIEW_PATH.as_posix()}#subagent-{selected_family.key.lower()}",
                content=family_text,
                sha256=_sha256(family_text),
                kind="family-instructions",
            )
        )
        guidance = _mechanism_guidance_document(selected_mechanisms)
        if guidance is not None:
            documents.append(guidance)
    if policy.include_invariants or policy.include_historical_bugs:
        for path, kind in _scoped_documents(
            root, selected_family, toolchains, selected_mechanisms
        ):
            if kind.startswith("invariant") and not policy.include_invariants:
                continue
            if kind == "historical-bug" and not policy.include_historical_bugs:
                continue
            documents.append(_document(root, path, kind))
    if policy.include_invariants:
        generated = _generated_invariant_document(generated_invariants, selected_mechanisms)
        if generated is not None:
            documents.append(generated)
    if policy.include_online_oracle:
        for index, value in enumerate(oracle_documents, start=1):
            documents.append(
                _external_document(
                    Path(value),
                    "oracle",
                    display_path=f"oracle-{index}",
                    redacted_roots=(root,),
                )
            )
    for value in extra_documents:
        path = Path(value)
        # Extra worker inputs remain constrained to the two documented input corpora.
        resolved = path.resolve()
        assert_worker_safe_path(resolved)
        allowed_roots = (
            (root / "docs" / "bugs").resolve(),
            (root / "docs" / "invariants").resolve(),
        )
        if not any(resolved.is_relative_to(allowed) for allowed in allowed_roots):
            raise ValueError(f"extra worker document is outside permitted corpora: {path}")
        documents.append(_document(root, path, "permitted-extra"))

    if policy.variant is AuditVariant.WITHOUT_ORACLE:
        instructions.append(
            "Ablation: no dedicated checker or online oracle feedback is visible during review. "
            "Produce candidates for the same isolated post-run admission process."
        )
    prompt = "\n\n".join(instructions)
    if documents:
        prompt += "\n\n# Permitted reference material\n\n" + _render_documents(documents)
    if policy.variant is not AuditVariant.BARE_AGENT:
        prompt += "\n\n# Output JSON Schema\n\n" + str(audit_report_json_schema())
    host_paths = [
        (root, "[REFERENCE_ROOT]"),
        *(
            (Path(value).expanduser().resolve(strict=False), f"[SOURCE_ROOT_{index}]")
            for index, value in enumerate(source_roots, start=1)
        ),
    ]
    for host_path, replacement in host_paths:
        if str(host_path) != "/":
            prompt = prompt.replace(str(host_path), replacement)
    assert_no_findings_leak(prompt)
    if str(root) != "/" and str(root) in prompt:
        raise ValueError("worker prompt discloses the reference checkout path")
    digest = _sha256(prompt)
    return WorkerPromptBundle(
        family=selected_family,
        variant=policy.variant,
        prompt=prompt,
        documents=tuple(documents),
        sha256=digest,
        metadata={
            "mechanisms": list(selected_mechanisms),
            "findings_access": "denied",
            "online_oracle": policy.include_online_oracle,
            "generated_invariant_count": sum(
                document.kind == "generated-invariants" for document in documents
            ),
        },
    )


def build_worker_prompt_bundles(
    reference_root: str | Path,
    families: Sequence[str | AuditFamily],
    **kwargs: Any,
) -> tuple[WorkerPromptBundle, ...]:
    """Build bundles in canonical A--E order regardless of caller order."""

    selected = {canonical_family(family).key for family in families}
    from .audit_schema import CANONICAL_AUDIT_FAMILIES

    return tuple(
        build_worker_prompt_bundle(reference_root, family, **kwargs)
        for family in CANONICAL_AUDIT_FAMILIES
        if family.key in selected
    )
