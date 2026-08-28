from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import stat
import sys
from pathlib import Path
from typing import Any, Literal

import pytest

from defuzz_loop.agent_audit import run
from defuzz_loop.audit_schema import CANONICAL_AUDIT_FAMILIES, normalize_mechanism
from defuzz_loop.checker_bundle import compute_bundle_id
from defuzz_loop.experiment_engine import AgentRequest, AgentResult, ExperimentPlan
from defuzz_loop.prompt_bundle import audit_doctrine_parity, build_worker_prompt_bundle

_UNUSED_ONLINE_ORACLE_COMMAND = (
    "unused-online-oracle",
    "{candidate_fingerprint}",
)
_ERROR_ONLINE_ORACLE_COMMAND = (
    sys.executable,
    "-c",
    (
        "import json, sys; "
        "print(json.dumps({'candidate_fingerprint': sys.argv[1], "
        "'verdict': 'ERROR', 'feedback': 'checker unavailable', "
        "'evidence': ['fixture checker error']}))"
    ),
    "{candidate_fingerprint}",
)
_REAL_REFERENCE_ROOT = Path(
    os.environ.get(
        "DEFUZZ_REFERENCE_ROOT",
        "/Users/bytedance/projects/research/defend-reviewer/main",
    )
)


def _reference(tmp_path: Path) -> Path:
    root = tmp_path / "reviewer"
    (root / ".claude" / "agents").mkdir(parents=True)
    (root / "docs" / "prompts").mkdir(parents=True)
    (root / "docs" / "bugs" / "gcc" / "stack-protector").mkdir(parents=True)
    (root / "docs" / "invariants").mkdir(parents=True)
    (root / "findings" / "DREV-2099-999").mkdir(parents=True)
    (root / ".claude" / "agents" / "defend-reviewer.md").write_text(
        "CANONICAL_DOCTRINE\nDo not read findings/.\n", encoding="utf-8"
    )
    family_blocks = "\n\n".join(
        f"### SUBAGENT {key} — family {key}\nFAMILY_{key}_ONLY" for key in "ABCDE"
    )
    (root / "docs" / "prompts" / "full-review.md").write_text(
        family_blocks + "\n\n## Uniform subagent instructions\nUNIFORM_RULE\n\n## Phase 2: stop\n",
        encoding="utf-8",
    )
    (root / "docs" / "invariants" / "README.md").write_text("INVARIANT_INDEX", encoding="utf-8")
    (root / "docs" / "invariants" / "stack-canary.md").write_text(
        "INVARIANT_SENTINEL", encoding="utf-8"
    )
    (root / "docs" / "invariants" / "stack-clash-protection.md").write_text(
        "STACK_CLASH_INVARIANT", encoding="utf-8"
    )
    (root / "docs" / "bugs" / "gcc" / "stack-protector" / "CVE-1.md").write_text(
        "HISTORICAL_SENTINEL\nfindings/DREV-2099-998", encoding="utf-8"
    )
    (root / "findings" / "DREV-2099-999" / "README.md").write_text(
        "PRIVATE_FINDING_SENTINEL", encoding="utf-8"
    )
    (root / "oracle.txt").write_text("ORACLE_SENTINEL", encoding="utf-8")
    return root


def _source(tmp_path: Path) -> Path:
    root = tmp_path / "source"
    root.mkdir()
    (root / "compiler.c").write_text("int compiler(void) { return 0; }\n")
    (root / "findings").mkdir()
    (root / "findings" / "secret.txt").write_text("SOURCE_FINDING_SENTINEL")
    (root / "reports").mkdir()
    (root / "reports" / "private.txt").write_text("SOURCE_REPORT_SENTINEL")
    (root / "runs").mkdir()
    (root / "runs" / "prior.json").write_text("SOURCE_RUN_SENTINEL")
    (root / ".git").mkdir()
    (root / ".git" / "config").write_text("SOURCE_GIT_SENTINEL")
    return root


def _sha256_bytes(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def _checker_bundle(
    tmp_path: Path,
    *,
    tamper_catalog: bool = False,
    sleep_seconds: float = 0.0,
    verdict: str = "FAIL",
) -> tuple[Path, Path]:
    root = tmp_path / "checker-bundle"
    root.mkdir()
    dispatcher = root / "dispatcher.py"
    dispatcher.write_text(
        """#!/usr/bin/env python3
import argparse
import hashlib
import json
from pathlib import Path
import sys
import time

parser = argparse.ArgumentParser()
parser.add_argument('--mode', choices=('online', 'verify'), required=True)
parser.add_argument('--compiler', choices=('gcc', 'llvm'), required=True)
parser.add_argument('--bundle-manifest', required=True)
parser.add_argument('--catalog', required=True)
parser.add_argument('--toolchains', required=True)
parser.add_argument('--candidate-json', required=True)
parser.add_argument('--candidate-fingerprint', required=True)
args = parser.parse_args()
"""
        + f"time.sleep({sleep_seconds!r})\n"
        + f"verdict = {verdict!r}\n"
        + """
candidate = Path(args.candidate_json).read_bytes()
fingerprint = hashlib.sha256(candidate).hexdigest()
with Path(__file__).with_name('invocations.jsonl').open('a') as stream:
    stream.write(json.dumps({
        'mode': args.mode,
        'compiler': args.compiler,
        'fingerprint': fingerprint,
    }) + '\\n')
payload = {
    'candidate_fingerprint': fingerprint,
    'echoed_candidate_fingerprint': args.candidate_fingerprint,
    'verdict': verdict,
    'feedback': 'reproduced',
    'evidence': ['fixture checker'],
    'results': [{'checker_id': 'INV-ONE', 'verdict': verdict}],
    'builds': [{'isa': 'x86_64'}],
}
print(json.dumps(payload))
if args.mode == 'verify':
    sys.exit(0 if verdict == 'FAIL' else 1 if verdict in ('PASS', 'NOT_APPLICABLE') else 2)
""",
        encoding="utf-8",
    )
    dispatcher.chmod(0o755)
    patch = root / "checker-bundle.patch"
    patch.write_text("diff --git a/x b/x\n", encoding="utf-8")
    scoped_invariants = root / "scoped-accepted-invariants.jsonl"
    scoped_invariants.write_text(
        json.dumps(
            {
                "invariant_id": "INV-ONE",
                "statement": "BUNDLE_SCOPED_INVARIANT_SENTINEL",
                "mechanism": "stack-protector",
                "target": "x86_64",
            },
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )
    source_invariants_sha = "7" * 64
    input_scope = root / "checker-input-scope.json"
    input_scope.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "kind": "defuzz-checker-input-scope",
                "source_artifact": {"sha256": source_invariants_sha},
                "requested": {"mechanisms": [], "isas": []},
                "scope_requested": False,
                "selected_invariant_ids": ["INV-ONE"],
            },
            sort_keys=True,
        ),
        encoding="utf-8",
    )
    source_tree_sha = "2" * 64
    final_tree_sha = "3" * 64
    catalog = root / "checker-catalog.json"
    catalog_payload = {
        "schema_version": 1,
        "kind": "defuzz-checker-catalog",
        "source_tree_sha256": source_tree_sha,
        "result_tree_sha256": final_tree_sha,
        "checkers": [
            {
                "invariant_id": "INV-ONE",
                "checker_id": "INV-ONE",
                "statement": "A deterministic fixture checker",
                "mechanism": "stack-protector",
                "target": "x86_64",
                "lineage": {},
                "parent_tree_sha256": "1" * 64,
                "result_tree_sha256": final_tree_sha,
                "files": [],
            }
        ],
    }
    catalog.write_text(json.dumps(catalog_payload), encoding="utf-8")

    def artifact(path: Path, kind: str) -> dict[str, object]:
        content = path.read_bytes()
        return {
            "path": path.name,
            "sha256": _sha256_bytes(content),
            "size_bytes": len(content),
            "kind": kind,
        }

    manifest: dict[str, Any] = {
        "schema_version": 1,
        "kind": "defuzz-checker-bundle",
        "status": "ready",
        "bundle_id": "0" * 64,
        "source_root": "/fixture/source",
        "source_root_sha256": "1" * 64,
        "source_tree_sha256": source_tree_sha,
        "final_tree_sha256": final_tree_sha,
        "source_invariants_sha256": source_invariants_sha,
        "requested_mechanisms": [],
        "requested_isas": [],
        "coverage_complete": True,
        "budget_exhausted": False,
        "included_invariant_ids": ["INV-ONE"],
        "failed_invariant_ids": [],
        "invariants": [
            {
                "invariant_id": "INV-ONE",
                "final_status": "passed",
                "parent_tree_sha256": "1" * 64,
                "result_tree_sha256": final_tree_sha,
                "files": [],
            }
        ],
        "artifacts": {
            "cumulative_patch": artifact(patch, "cumulative-patch"),
            "catalog": artifact(catalog, "checker-catalog"),
            "dispatcher": artifact(dispatcher, "checker-dispatcher"),
            "scoped_invariants": artifact(
                scoped_invariants, "scoped-accepted-invariants"
            ),
            "input_scope": artifact(input_scope, "checker-input-scope"),
        },
        "validation": {
            "status": "passed",
            "commands": [],
            "build": {"status": "passed"},
        },
    }
    manifest["bundle_id"] = compute_bundle_id(manifest)
    manifest_path = root / "checker-bundle-manifest.json"
    manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
    if tamper_catalog:
        catalog.write_text("tampered\n", encoding="utf-8")
    toolchains = tmp_path / "toolchains.yaml"
    toolchains.write_text("toolchains: {}\n", encoding="utf-8")
    return manifest_path, toolchains


def test_canonical_five_family_mapping_is_frozen() -> None:
    assert [family.key for family in CANONICAL_AUDIT_FAMILIES] == list("ABCDE")
    assert CANONICAL_AUDIT_FAMILIES[0].mechanisms == (
        "stack-protector",
        "stack-clash-protection",
    )
    assert "codegen" in CANONICAL_AUDIT_FAMILIES[1].mechanisms
    assert {"riscv-cfi", "cet-ibt", "ret-hardening"} <= set(CANONICAL_AUDIT_FAMILIES[2].mechanisms)
    assert "zero-call-used-regs" in CANONICAL_AUDIT_FAMILIES[4].mechanisms


def test_doctrine_parity_reports_stale_wrapper_without_using_it(tmp_path: Path) -> None:
    root = _reference(tmp_path)
    trae = root / ".trae/agents/defend-reviewer.md"
    trae.parent.mkdir(parents=True)
    trae.write_text("---\nname: reviewer\n---\nSTALE\n", encoding="utf-8")

    report = audit_doctrine_parity(root)

    assert report["canonical"] == ".claude/agents/defend-reviewer.md"
    assert report["all_match"] is False
    assert report["mismatches"] == [".trae/agents/defend-reviewer.md"]
    assert CANONICAL_AUDIT_FAMILIES[-1].mechanisms == (
        "asan",
        "ubsan",
        "tsan",
        "lsan",
        "auto-var-init",
        "glibcxx-assertions",
        "fhardened",
        "zero-call-used-regs",
    )


def test_prompt_variants_have_exact_visibility_and_never_read_findings(tmp_path: Path) -> None:
    root = _reference(tmp_path)
    common: dict[str, Any] = {
        "source_roots": [tmp_path / "source"],
        "toolchains": ["gcc"],
        "oracle_documents": [root / "oracle.txt"],
        "generated_invariants": [
            {
                "invariant_id": "INV-GENERATED-ONE",
                "statement": "GENERATED_INVARIANT_SENTINEL",
                "mechanism": "stack-protector",
                "source_url_or_path": "/private/host/path.c",
                "evidence_snippet": "PRIVATE_SOURCE_SENTINEL",
            }
        ],
    }
    full = build_worker_prompt_bundle(root, "A", "full", **common)
    without_oracle = build_worker_prompt_bundle(root, "A", "without-oracle", **common)
    bare = build_worker_prompt_bundle(root, "A", "bare-agent", **common)

    assert "CANONICAL_DOCTRINE" in full.prompt
    assert "FAMILY_A_ONLY" in full.prompt and "FAMILY_B_ONLY" not in full.prompt
    assert "INVARIANT_SENTINEL" in full.prompt
    assert "HISTORICAL_SENTINEL" in full.prompt
    assert "ORACLE_SENTINEL" in full.prompt
    assert "GENERATED_INVARIANT_SENTINEL" in full.prompt
    assert "PRIVATE_SOURCE_SENTINEL" not in full.prompt
    assert "PRIVATE_FINDING_SENTINEL" not in full.prompt
    assert "DREV-2099-998" not in full.prompt

    assert "CANONICAL_DOCTRINE" in without_oracle.prompt
    assert "ORACLE_SENTINEL" not in without_oracle.prompt
    assert "GENERATED_INVARIANT_SENTINEL" in without_oracle.prompt
    assert "no dedicated checker or online oracle feedback" in without_oracle.prompt

    assert "CANONICAL_DOCTRINE" not in bare.prompt
    assert "INVARIANT_SENTINEL" not in bare.prompt
    assert "HISTORICAL_SENTINEL" not in bare.prompt
    assert "ORACLE_SENTINEL" not in bare.prompt
    assert "GENERATED_INVARIANT_SENTINEL" not in bare.prompt
    assert "independent general code-audit worker" in bare.prompt
    assert bare.family.key == "neutral"
    assert "Assigned family" not in bare.prompt
    assert "Mechanisms:" not in bare.prompt
    assert "AuditReport" not in bare.prompt
    assert str(root) not in full.prompt
    assert str(root) not in bare.prompt
    assert str(tmp_path / "source") not in full.prompt

    with pytest.raises(ValueError, match="Part III supports only"):
        build_worker_prompt_bundle(root, "A", "without-rag")
    with pytest.raises(ValueError, match="forbidden"):
        build_worker_prompt_bundle(
            root,
            "A",
            "full",
            extra_documents=[root / "findings" / "DREV-2099-999" / "README.md"],
        )


def test_generated_invariants_are_filtered_by_mechanism_and_isa_scope(
    tmp_path: Path,
) -> None:
    root = _reference(tmp_path)
    records: list[dict[str, Any]] = [
        {
            "invariant_id": "INV-GENERIC",
            "statement": "INCLUDE_GENERIC",
            "mechanism": "stack-canary",
        },
        {
            "invariant_id": "INV-PLATFORM",
            "statement": "INCLUDE_PLATFORM",
            "mechanism": "stack-protector",
            "target": ["Android", "config-smoke"],
        },
        {
            "invariant_id": "INV-MULTI",
            "statement": "INCLUDE_MULTI_ISA",
            "mechanism": "SSP",
            "target": "riscv64 / x86_64",
        },
        {
            "invariant_id": "INV-FAMILY",
            "statement": "INCLUDE_ISA_FAMILY",
            "mechanism": "stack-protector",
            "target": "x86",
        },
        {
            "invariant_id": "INV-LEGACY-ISA",
            "statement": "INCLUDE_LEGACY_ISA",
            "mechanism": "stack-protector",
            "isa": "amd64",
        },
        {
            "invariant_id": "INV-WIDTH",
            "statement": "EXCLUDE_ISA_WIDTH",
            "mechanism": "stack-protector",
            "target": "i386",
        },
        {
            "invariant_id": "INV-UNKNOWN",
            "statement": "EXCLUDE_UNKNOWN_ISA",
            "mechanism": "stack-protector",
            "target": "unknown-architecture",
        },
        {
            "invariant_id": "INV-MIXED-UNKNOWN",
            "statement": "EXCLUDE_MIXED_UNKNOWN_ISA",
            "mechanism": "stack-protector",
            "target": ["generic", "unknown-architecture"],
        },
        {
            "invariant_id": "INV-OTHER-MECHANISM",
            "statement": "EXCLUDE_OTHER_MECHANISM",
            "mechanism": "stack-clash-protection",
            "target": "x86_64",
        },
    ]

    bundle = build_worker_prompt_bundle(
        root,
        "A",
        "without-oracle",
        mechanisms=["SSP"],
        isas=["x86-64"],
        generated_invariants=records,
    )

    document = next(
        document for document in bundle.documents if document.kind == "generated-invariants"
    )
    payload = json.loads(document.content)
    assert [
        item["invariant_id"] for item in payload["accepted_invariants"]
    ] == [
        "INV-GENERIC",
        "INV-PLATFORM",
        "INV-MULTI",
        "INV-FAMILY",
        "INV-LEGACY-ISA",
    ]
    assert payload["accepted_invariants"][-1]["target"] == "amd64"
    for sentinel in (
        "EXCLUDE_ISA_WIDTH",
        "EXCLUDE_UNKNOWN_ISA",
        "EXCLUDE_MIXED_UNKNOWN_ISA",
        "EXCLUDE_OTHER_MECHANISM",
    ):
        assert sentinel not in bundle.prompt


@pytest.mark.parametrize(
    ("target", "requested_isa", "expected"),
    [
        ("RISC-V", "riscv32", True),
        ("riscv64", "riscv32", False),
        ("arm", "aarch64", False),
    ],
)
def test_generated_invariant_isa_family_compatibility(
    tmp_path: Path, target: str, requested_isa: str, expected: bool
) -> None:
    bundle = build_worker_prompt_bundle(
        _reference(tmp_path),
        "A",
        "without-oracle",
        mechanisms=["stack-protector"],
        isas=[requested_isa],
        generated_invariants=[
            {
                "invariant_id": "INV-TARGET",
                "statement": "TARGET_SENTINEL",
                "mechanism": "stack-protector",
                "target": target,
            }
        ],
    )

    generated = [
        document for document in bundle.documents if document.kind == "generated-invariants"
    ]
    assert bool(generated) is expected
    assert ("TARGET_SENTINEL" in bundle.prompt) is expected


def test_new_mechanism_prompt_overlay_is_scoped_and_leak_resistant(
    tmp_path: Path,
) -> None:
    root = _reference(tmp_path)
    invariant_root = root / "docs" / "invariants"
    (invariant_root / "gcc-llvm-defense-invariant-source-survey.md").write_text(
        "CODEGEN_SURVEY", encoding="utf-8"
    )
    (invariant_root / "riscv-cfi.md").write_text("RISCV_CFI_SURVEY", encoding="utf-8")
    (invariant_root / "zero-call-used-regs.md").write_text("ZCUR_SURVEY", encoding="utf-8")
    codegen_bug = root / "docs" / "bugs" / "gcc" / "codegen" / "GCC-1.md"
    codegen_bug.parent.mkdir(parents=True)
    codegen_bug.write_text("RETURN_THUNK_HISTORY findings/DREV-2099-777", encoding="utf-8")

    codegen = build_worker_prompt_bundle(
        root, "B", "without-oracle", mechanisms=["backend_codegen"]
    )
    cfi = build_worker_prompt_bundle(root, "C", "without-oracle", mechanisms=["zicfilp"])
    ret = build_worker_prompt_bundle(
        root,
        "C",
        "without-oracle",
        mechanisms=["ret-hardening (return thunks / LVI)"],
    )
    zcur = build_worker_prompt_bundle(
        root, "E", "without-oracle", mechanisms=["fzero-call-used-regs"]
    )

    assert codegen.metadata["mechanisms"] == ["codegen"]
    assert "CODEGEN_SURVEY" in codegen.prompt
    assert "security-relevant backend lowering" in codegen.prompt
    assert cfi.metadata["mechanisms"] == ["riscv-cfi"]
    assert "RISCV_CFI_SURVEY" in cfi.prompt
    assert "Zicfilp/Zicfiss" in cfi.prompt
    assert ret.metadata["mechanisms"] == ["ret-hardening"]
    assert "RETURN_THUNK_HISTORY" in ret.prompt
    assert "return-thunk" in ret.prompt
    assert "DREV-2099-777" not in ret.prompt
    assert zcur.metadata["mechanisms"] == ["zero-call-used-regs"]
    assert "ZCUR_SURVEY" in zcur.prompt
    assert "applicable exit forms" in zcur.prompt
    for bundle in (codegen, cfi, ret, zcur):
        assert "PRIVATE_FINDING_SENTINEL" not in bundle.prompt
        assert str(root) not in bundle.prompt


@pytest.mark.skipif(
    not (_REAL_REFERENCE_ROOT / "findings").is_dir(),
    reason="real defend-reviewer demo corpus is not available",
)
def test_real_new_mechanism_bundles_cover_docs_without_reading_findings() -> None:
    readmes = sorted((_REAL_REFERENCE_ROOT / "findings").glob("DREV-*/README.md"))
    before = {path: path.stat().st_mtime_ns for path in readmes}
    raw_ret_hardening = next(
        line.partition(":")[2].strip()
        for line in (_REAL_REFERENCE_ROOT / "findings/DREV-2026-029/README.md")
        .read_text(encoding="utf-8")
        .splitlines()
        if line.startswith("mechanism:")
    )
    scopes = (
        ("B", "codegen", "gcc-llvm-defense-invariant-source-survey.md"),
        ("C", "riscv-cfi", "riscv-cfi.md"),
        ("C", "cet-ibt", "endbr-ibt.md"),
        (
            "C",
            raw_ret_hardening,
            "gcc-llvm-defense-invariant-source-survey.md",
        ),
        ("E", "zero-call-used-regs", "zero-call-used-regs.md"),
    )

    for family, mechanism, expected_document in scopes:
        bundle = build_worker_prompt_bundle(
            _REAL_REFERENCE_ROOT,
            family,
            "without-oracle",
            mechanisms=[mechanism],
        )
        assert any(document.path.endswith(expected_document) for document in bundle.documents)
        assert bundle.metadata["mechanisms"] == [normalize_mechanism(mechanism)]
        assert re.search(r"\bDREV-\d{4}-\d{3,}\b", bundle.prompt) is None
        assert "findings/DREV-" not in bundle.prompt
        assert str(_REAL_REFERENCE_ROOT) not in bundle.prompt

    assert {path: path.stat().st_mtime_ns for path in readmes} == before


class _FakeBackend:
    supports_host_read_isolation = True

    def __init__(self) -> None:
        self.requests: list[AgentRequest] = []
        self.workspace_files: list[set[str]] = []
        self.workspace_text: list[str] = []
        self.workspace_read_only: list[bool] = []

    async def run(self, request: AgentRequest) -> AgentResult:
        self.requests.append(request)
        self.workspace_files.append(
            {path.relative_to(request.cwd).as_posix() for path in request.cwd.rglob("*")}
        )
        self.workspace_text.append(
            "\n".join(
                path.read_text(encoding="utf-8")
                for path in request.cwd.rglob("*")
                if path.is_file()
            )
        )
        self.workspace_read_only.append(
            not bool(request.cwd.stat().st_mode & stat.S_IWUSR)
            and not bool((request.cwd / "compiler.c").stat().st_mode & stat.S_IWUSR)
        )
        family = request.metadata["family"]
        if request.metadata["variant"] == "bare-agent":
            return AgentResult(
                success=True,
                final={
                    "worker_bundle_sha256": request.metadata["worker_bundle_sha256"],
                    "issues": [],
                    "coverage_gaps": ["generic gap"],
                },
            )
        return AgentResult(
            success=True,
            final={
                "family": family,
                "variant": request.metadata["variant"],
                "worker_bundle_sha256": request.metadata["worker_bundle_sha256"],
                "candidates": [],
                "coverage_gaps": [f"gap-{family}"],
            },
        )


class _TaintedBackend:
    supports_host_read_isolation = True

    async def run(self, request: AgentRequest) -> AgentResult:
        request.output_dir.mkdir(parents=True, exist_ok=True)
        (request.output_dir / "events.jsonl").write_text(
            '{"type":"message","text":"DREV-2026-001"}\n',
            encoding="utf-8",
        )
        (request.output_dir / "final.json").write_text(
            '{"id":"DREV-2026-001"}\n', encoding="utf-8"
        )
        return AgentResult(
            success=True,
            final={
                "family": request.metadata["family"],
                "variant": request.metadata["variant"],
                "worker_bundle_sha256": request.metadata["worker_bundle_sha256"],
                "candidates": [
                    {
                        "id": "DREV-2026-001",
                        "toolchain": "gcc",
                        "toolchain_version": "gcc-17",
                        "mechanism": "stack-protector",
                        "isa": ["x86_64"],
                        "checker_ids": ["INV-ONE"],
                        "invariant_violated": "A concrete invariant.",
                        "evidence_file_line": ["gcc/x.cc:10"],
                        "evidence_code": "1\n2\n3\n4\n5",
                        "minimal_trigger": {
                            "source": "int main(void) { return 0; }",
                            "flags": "-O2",
                            "isa": "x86_64",
                        },
                        "impact": "A required check is skipped.",
                        "why_not_rescued": "No later layer restores the check.",
                        "poc_verified": True,
                        "discovered": "2026-08-26",
                    }
                ],
            },
        )


class _InvalidWorkerBackend:
    supports_host_read_isolation = True

    def __init__(self, invalidity: str) -> None:
        self.invalidity = invalidity

    async def run(self, request: AgentRequest) -> AgentResult:
        if self.invalidity == "parse-issue":
            return AgentResult(success=True, final="not: [valid")
        payload = {
            "family": request.metadata["family"],
            "variant": request.metadata["variant"],
            "worker_bundle_sha256": request.metadata["worker_bundle_sha256"],
            "candidates": [],
        }
        if self.invalidity == "missing-bundle-hash":
            payload.pop("worker_bundle_sha256")
        elif self.invalidity == "bundle-hash-mismatch":
            payload["worker_bundle_sha256"] = "0" * 64
        elif self.invalidity == "family-mismatch":
            payload["family"] = "B"
        elif self.invalidity == "variant-mismatch":
            payload["variant"] = "without-oracle"
        return AgentResult(success=True, final=payload)


class _CandidateBackend:
    supports_host_read_isolation = True

    def __init__(
        self,
        *,
        evidence_code: str | None = None,
        checker_ids: tuple[str, ...] = ("INV-ONE",),
        toolchain: str = "gcc",
        mechanism: str = "stack-protector",
        isas: tuple[str, ...] = ("x86_64",),
    ) -> None:
        self.requests: list[AgentRequest] = []
        self.evidence_code = evidence_code
        self.checker_ids = checker_ids
        self.toolchain = toolchain
        self.mechanism = mechanism
        self.isas = isas

    async def run(self, request: AgentRequest) -> AgentResult:
        self.requests.append(request)
        source = self.evidence_code or (request.cwd / "compiler.c").read_text(encoding="utf-8")
        candidate = {
            "toolchain": self.toolchain,
            "toolchain_version": "fixture",
            "mechanism": self.mechanism,
            "isa": list(self.isas),
            "checker_ids": list(self.checker_ids),
            "invariant_violated": "A concrete invariant.",
            "evidence_file_line": ["compiler.c:1"],
            "evidence_code": source,
            "minimal_trigger": {
                "source": "int main(void) { return 0; }",
                "flags": "-O2",
                "isa": list(self.isas),
            },
            "impact": "A required check is skipped.",
            "why_not_rescued": "No later layer restores it.",
            "poc_verified": True,
            "discovered": "2026-08-26",
        }
        candidate_key = "issues" if request.metadata["variant"] == "bare-agent" else "candidates"
        return AgentResult(
            success=True,
            final={
                "family": request.metadata["family"],
                "variant": request.metadata["variant"],
                "worker_bundle_sha256": request.metadata["worker_bundle_sha256"],
                candidate_key: [candidate],
            },
        )


@pytest.mark.asyncio
async def test_full_runner_fails_without_online_oracle_command(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    backend = _FakeBackend()

    result = await run(
        ExperimentPlan(
            run_id="full-without-oracle-command",
            experiment="agent-audit",
            variant="full",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
            },
        ),
        1,
        tmp_path / "missing-oracle-run",
        backend,
    )

    assert not result.success
    assert result.status == "failed"
    assert "requires at least one online_oracle_command" in (result.error or "")
    assert backend.requests == []


@pytest.mark.asyncio
async def test_full_runner_fails_closed_on_online_oracle_error(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    backend = _CandidateBackend()
    output = tmp_path / "oracle-error-run"

    result = await run(
        ExperimentPlan(
            run_id="full-oracle-error",
            experiment="agent-audit",
            variant="full",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "online_oracle_command": _ERROR_ONLINE_ORACLE_COMMAND,
            },
        ),
        1,
        output,
        backend,
    )

    assert not result.success
    assert result.status == "failed"
    assert len(backend.requests) == 1
    assert result.metrics["candidate_admitted"] == 1
    assert result.metrics["online_oracle_calls"] == 1
    assert result.metrics["online_oracle_errors"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["online_oracle"]["operational"] is False
    assert summary["online_oracle"]["error_count"] == 1
    assert summary["online_oracle"]["records"][0]["verdict"] == "ERROR"
    assert "checker unavailable" in summary["errors"][0]


@pytest.mark.asyncio
async def test_without_oracle_succeeds_without_command_and_runs_only_initial_worker(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    backend = _CandidateBackend()
    output = tmp_path / "without-oracle-run"

    result = await run(
        ExperimentPlan(
            run_id="without-oracle",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
            },
        ),
        1,
        output,
        backend,
    )

    assert result.success
    assert len(backend.requests) == 1
    assert backend.requests[0].metadata["variant"] == "without-oracle"
    assert result.metrics["online_oracle_calls"] == 0
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["online_oracle"] == {
        "enabled": False,
        "rounds_configured": 0,
        "records": [],
        "error_count": 0,
        "operational": True,
    }


@pytest.mark.asyncio
async def test_runner_uses_fake_backend_in_canonical_order_without_archive(tmp_path: Path) -> None:
    root = _reference(tmp_path)
    source = _source(tmp_path)
    backend = _FakeBackend()
    output = tmp_path / "run"
    plan = ExperimentPlan(
        run_id="audit-1",
        experiment="agent-audit",
        variant="full",
        source_root=source,
        parameters={
            "reference_root": str(root),
            "compiler": "gcc",
            "online_oracle_command": _UNUSED_ONLINE_ORACLE_COMMAND,
        },
    )

    result = await run(plan, 1, output, backend)

    assert result.success
    assert [request.metadata["family"] for request in backend.requests] == list("ABCDE")
    assert all(request.writable is False for request in backend.requests)
    assert all(
        request.deny_read_paths == [source.resolve(), root.resolve()]
        for request in backend.requests
    )
    assert all(request.require_host_read_isolation for request in backend.requests)
    assert len({request.cwd for request in backend.requests}) == 1
    workspace = backend.requests[0].cwd
    assert workspace != source
    assert not workspace.exists()
    assert all(files == {"compiler.c"} for files in backend.workspace_files)
    assert all(backend.workspace_read_only)
    assert all(
        request.metadata["isolation_level"] == "workspace-copy" for request in backend.requests
    )
    assert all("PRIVATE_FINDING_SENTINEL" not in request.prompt for request in backend.requests)
    assert all(str(root) not in request.prompt for request in backend.requests)
    assert all(str(source) not in request.prompt for request in backend.requests)
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["family_order"] == list("ABCDE")
    assert summary["archive_performed"] is False
    assert summary["isolation_level"] == "workspace-copy"
    assert not (root / "findings" / "DREV-2099-999" / "timeline.md").exists()


@pytest.mark.asyncio
async def test_parity_scope_is_evaluator_only_and_never_reaches_worker(
    tmp_path: Path,
) -> None:
    root = _reference(tmp_path)
    source = _source(tmp_path)
    sentinel = "__EVALUATOR_ONLY_PARITY_SCOPE__"
    finding = root / "findings" / "DREV-2099-997" / "README.md"
    finding.parent.mkdir(parents=True)
    finding.write_text(
        """---
id: __EVALUATOR_ONLY_PARITY_SCOPE__
toolchain: gcc
toolchain_version: gcc-17-20260531
mechanism: stack-protector
isa: [x86_64]
invariant_violated: Every protected return checks the saved guard.
status: draft
poc_verified: true
---
# evaluator fixture
""",
        encoding="utf-8",
    )
    backend = _FakeBackend()
    output = tmp_path / "scope-evaluator-only"

    result = await run(
        ExperimentPlan(
            run_id="scope-evaluator-only",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(root),
                "compiler": "gcc",
                "families": ["A"],
                "demo_parity": True,
                "parity_scope": {"demo_ids": [sentinel]},
            },
        ),
        1,
        output,
        backend,
    )

    assert result.success
    assert len(backend.requests) == 1
    request = backend.requests[0]
    assert sentinel not in request.prompt
    assert "parity_scope" not in request.prompt
    assert "parity_scope" not in request.metadata
    assert sentinel not in json.dumps(request.metadata, sort_keys=True)
    assert all(sentinel not in path for path in backend.workspace_files[0])
    assert sentinel not in backend.workspace_text[0]
    report = json.loads((output / "demo-parity.json").read_text(encoding="utf-8"))
    assert report["scope"]["demo_ids"] == [sentinel]
    assert report["scope_report"]["selected_demo_ids"] == [sentinel]
    assert report["scope_status"] == "applicable"


@pytest.mark.asyncio
async def test_runner_rejects_overlapping_original_source_and_reference_roots(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    backend = _FakeBackend()

    result = await run(
        ExperimentPlan(
            run_id="overlap",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=reference,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
            },
        ),
        1,
        tmp_path / "overlap-run",
        backend,
    )

    assert not result.success
    assert "original input roots overlap" in (result.error or "")
    assert backend.requests == []


@pytest.mark.asyncio
async def test_runner_rejects_tainted_backend_candidate(tmp_path: Path) -> None:
    root = _reference(tmp_path)
    source = _source(tmp_path)
    output = tmp_path / "run"
    plan = ExperimentPlan(
        run_id="tainted",
        experiment="agent-audit",
        variant="without-oracle",
        source_root=source,
        parameters={
            "reference_root": str(root),
            "compiler": "gcc",
            "families": ["A"],
        },
    )

    result = await run(plan, 1, output, _TaintedBackend())

    assert not result.success
    assert result.status == "failed"
    assert result.metrics["valid_workers"] == 0
    assert result.metrics["invalid_workers"] == 1
    assert result.metrics["candidate_admitted"] == 0
    assert result.metrics["candidate_rejected"] == 0
    report = json.loads((output / "worker-a-initial" / "audit-report.json").read_text())
    assert report["tainted"] is True
    assert report["candidates"] == []
    admission = json.loads((output / "worker-a-initial" / "admission.json").read_text())
    assert admission["admitted"] == []
    assert admission["candidate_admission"] == {
        "scope": "structural-completeness-only",
        "deterministic_poc_validator_executed": False,
    }
    assert "DREV-2026-001" not in "".join(
        path.read_text(encoding="utf-8", errors="replace")
        for path in output.rglob("*")
        if path.is_file()
    )


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "invalidity",
    [
        "parse-issue",
        "missing-bundle-hash",
        "bundle-hash-mismatch",
        "family-mismatch",
        "variant-mismatch",
    ],
)
async def test_any_invalid_worker_output_fails_stage(tmp_path: Path, invalidity: str) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    output = tmp_path / "invalid-run"
    result = await run(
        ExperimentPlan(
            run_id=f"invalid-{invalidity}",
            experiment="agent-audit",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "online_oracle_command": _UNUSED_ONLINE_ORACLE_COMMAND,
            },
        ),
        1,
        output,
        _InvalidWorkerBackend(invalidity),
    )

    assert not result.success
    assert result.status == "failed"
    assert result.metrics["valid_workers"] == 0
    assert result.metrics["invalid_workers"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["worker_validity"][0]["valid"] is False
    assert summary["worker_validity"][0]["invalid_reasons"]
    admission = json.loads((output / "worker-a-initial" / "admission.json").read_text())
    assert admission["worker_valid"] is False


@pytest.mark.asyncio
async def test_valid_worker_requires_bundle_hash_echo_in_schema_and_metadata(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    output = tmp_path / "valid-run"
    result = await run(
        ExperimentPlan(
            run_id="valid-worker",
            experiment="agent-audit",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "online_oracle_command": _UNUSED_ONLINE_ORACLE_COMMAND,
            },
        ),
        1,
        output,
        _FakeBackend(),
    )

    assert result.success
    schema = json.loads((output / "audit-report.schema.json").read_text())
    assert "worker_bundle_sha256" in schema["required"]
    assert result.metadata["candidate_admission"] == {
        "scope": "structural-completeness-only",
        "deterministic_poc_validator_executed": False,
    }
    assert "confirmed" not in result.metrics


@pytest.mark.asyncio
async def test_structural_candidate_stays_unverified_without_verification_command(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    output = tmp_path / "candidate-run"

    result = await run(
        ExperimentPlan(
            run_id="candidate",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
            },
        ),
        1,
        output,
        _CandidateBackend(),
    )

    assert result.success
    assert result.metrics["candidate_admitted"] == 1
    assert result.metrics["candidate_verified"] == 0
    assert result.metrics["time_to_first_verified_ms"] is None
    assert result.metrics["candidate_unverified"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["verified_candidates"] == []
    assert summary["candidate_verification"][0]["status"] == "unverified"
    assert summary["time_to_first_verified_ms"] is None


@pytest.mark.asyncio
async def test_formal_bundle_routes_one_dispatcher_in_both_modes(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path)
    output = tmp_path / "formal-bundle-run"
    backend = _CandidateBackend()

    result = await run(
        ExperimentPlan(
            run_id="formal-bundle",
            experiment="agent-audit",
            variant="full",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
                "oracle_rounds": 1,
            },
        ),
        1,
        output,
        backend,
    )

    assert result.success
    assert result.execution_status == "completed"
    assert result.result_valid is True
    assert result.continuation_ready is True
    assert result.outcome == "verified-findings"
    assert isinstance(result.metrics["time_to_first_verified_ms"], float)
    assert result.metrics["time_to_first_verified_ms"] >= 0.0
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["execution_completed"] is True
    assert summary["result_valid"] is True
    assert summary["outcome"] == "verified-findings"
    assert summary["time_to_first_verified_ms"] == pytest.approx(
        result.metrics["time_to_first_verified_ms"]
    )
    assert summary["candidate_terminal_outcomes"] == {
        "verified": 1,
        "invalid": 0,
        "rejected": 0,
        "unverified": 0,
        "all_admitted_terminal": True,
        "all_admitted_valid": True,
    }
    invocation_log = manifest.parent / "invocations.jsonl"
    invocations = [json.loads(line) for line in invocation_log.read_text().splitlines()]
    assert [item["mode"] for item in invocations] == ["online", "verify"]
    assert [item["compiler"] for item in invocations] == ["gcc", "gcc"]
    assert summary["checker_bundle"]["compiler"] == "gcc"
    scoped = manifest.parent / "scoped-accepted-invariants.jsonl"
    input_scope = manifest.parent / "checker-input-scope.json"
    scoped_hash = _sha256_bytes(scoped.read_bytes())
    assert "BUNDLE_SCOPED_INVARIANT_SENTINEL" in backend.requests[0].prompt
    assert summary["generated_invariants"] == {
        "visible_to_worker": True,
        "records": 1,
        "sha256": scoped_hash,
        "path": str(scoped.resolve()),
        "source": "checker-bundle-scoped",
        "bundle_id": summary["checker_bundle"]["bundle_id"],
    }
    assert summary["checker_bundle"]["scoped_invariants_path"] == str(
        scoped.resolve()
    )
    assert summary["checker_bundle"]["scoped_invariants_sha256"] == scoped_hash
    assert summary["checker_bundle"]["input_scope_path"] == str(
        input_scope.resolve()
    )
    assert summary["checker_bundle"]["input_scope_sha256"] == _sha256_bytes(
        input_scope.read_bytes()
    )
    assert result.metadata["generated_invariants"] == summary["generated_invariants"]


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("plan_compiler", "candidate_toolchain", "expected_compiler"),
    [("gnu-gcc", "gcc", "gcc"), ("clang", "llvm", "llvm")],
)
async def test_formal_compiler_aliases_dispatch_canonical_value(
    tmp_path: Path,
    plan_compiler: str,
    candidate_toolchain: str,
    expected_compiler: str,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path)

    result = await run(
        ExperimentPlan(
            run_id=f"formal-alias-{expected_compiler}",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": plan_compiler,
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        tmp_path / f"formal-alias-{expected_compiler}",
        _CandidateBackend(toolchain=candidate_toolchain),
    )

    assert result.success
    invocations = [
        json.loads(line)
        for line in (manifest.parent / "invocations.jsonl").read_text().splitlines()
    ]
    assert [item["compiler"] for item in invocations] == [expected_compiler]


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("candidate_toolchain", "reason"),
    [("", "toolchain is missing"), ("msvc", "unknown toolchain"), ("clang", "does not match")],
)
async def test_formal_candidate_compiler_is_rejected_before_dispatch(
    tmp_path: Path, candidate_toolchain: str, reason: str
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path)
    output = tmp_path / "formal-compiler-rejection"

    result = await run(
        ExperimentPlan(
            run_id="formal-compiler-rejection",
            experiment="agent-audit",
            variant="full",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
                "oracle_rounds": 1,
            },
        ),
        1,
        output,
        _CandidateBackend(toolchain=candidate_toolchain),
    )

    assert result.status == "failed"
    assert result.metrics["invalid_workers"] == 1
    assert result.metrics["online_oracle_calls"] == 0
    assert result.metrics["candidate_admitted"] == 0
    assert not (manifest.parent / "invocations.jsonl").exists()
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert any(reason in item for item in summary["worker_validity"][0]["invalid_reasons"])


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("candidate_mechanism", "candidate_isas", "reason"),
    [
        ("ibt", ("x86_64",), "mechanism 'ibt' is outside"),
        ("stack-protector", ("aarch64",), "ISA values ['aarch64'] are outside"),
    ],
)
async def test_formal_candidate_outside_lane_scope_is_rejected_before_dispatch(
    tmp_path: Path,
    candidate_mechanism: str,
    candidate_isas: tuple[str, ...],
    reason: str,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path)
    output = tmp_path / "formal-scope-rejection"

    result = await run(
        ExperimentPlan(
            run_id="formal-scope-rejection",
            experiment="agent-audit",
            variant="full",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "mechanisms": ["stack-protector"],
                "isas": ["x86_64"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
                "oracle_rounds": 1,
            },
        ),
        1,
        output,
        _CandidateBackend(mechanism=candidate_mechanism, isas=candidate_isas),
    )

    assert result.status == "failed"
    assert result.metrics["invalid_workers"] == 1
    assert result.metrics["online_oracle_calls"] == 0
    assert result.metrics["candidate_admitted"] == 0
    assert not (manifest.parent / "invocations.jsonl").exists()
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert any(reason in item for item in summary["worker_validity"][0]["invalid_reasons"])


@pytest.mark.asyncio
async def test_candidate_lane_scope_accepts_mechanism_and_isa_aliases(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path)

    result = await run(
        ExperimentPlan(
            run_id="formal-scope-aliases",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "mechanisms": ["canary"],
                "isas": ["x86-64"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        tmp_path / "formal-scope-aliases",
        _CandidateBackend(mechanism="stack-canary", isas=("amd64",)),
    )

    assert result.success
    assert result.metrics["invalid_workers"] == 0
    invocations = [
        json.loads(line)
        for line in (manifest.parent / "invocations.jsonl").read_text().splitlines()
    ]
    assert [item["mode"] for item in invocations] == ["verify"]


@pytest.mark.asyncio
async def test_bare_agent_output_must_stay_in_hidden_campaign_scope(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    output = tmp_path / "bare-scope-rejection"

    result = await run(
        ExperimentPlan(
            run_id="bare-scope-rejection",
            experiment="agent-audit",
            variant="bare-agent",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "mechanisms": ["stack-protector"],
                "isas": ["x86_64"],
            },
        ),
        1,
        output,
        _CandidateBackend(mechanism="ibt"),
    )

    assert result.status == "failed"
    assert result.metrics["invalid_workers"] == 1
    assert result.metrics["candidate_admitted"] == 0
    assert "stack-protector" not in (output / "worker-neutral-initial" / "prompt.txt").read_text()
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert any(
        "mechanism 'ibt' is outside" in item
        for item in summary["worker_validity"][0]["invalid_reasons"]
    )


@pytest.mark.asyncio
@pytest.mark.parametrize("compiler", [None, "cc"])
async def test_formal_bundle_rejects_missing_or_unknown_plan_compiler_before_worker(
    tmp_path: Path, compiler: str | None
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    manifest, toolchains = _checker_bundle(tmp_path)
    backend = _FakeBackend()
    parameters: dict[str, Any] = {
        "reference_root": str(reference),
        "checker_bundle_manifest": str(manifest),
        "toolchains_config": str(toolchains),
        "require_verified_candidates": True,
    }
    if compiler is not None:
        parameters["compiler"] = compiler

    result = await run(
        ExperimentPlan(
            run_id="invalid-plan-compiler",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters=parameters,
        ),
        1,
        tmp_path / "invalid-plan-compiler",
        backend,
    )

    assert result.status == "failed"
    assert "compiler" in (result.error or "")
    assert backend.requests == []
    assert not (manifest.parent / "invocations.jsonl").exists()


@pytest.mark.asyncio
async def test_formal_full_missing_checker_ids_is_invalid(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path)
    output = tmp_path / "formal-full-missing-checker-ids"

    result = await run(
        ExperimentPlan(
            run_id="formal-full-missing-checker-ids",
            experiment="agent-audit",
            variant="full",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        output,
        _CandidateBackend(checker_ids=()),
    )

    assert result.status == "failed"
    assert result.result_valid is False
    assert result.metrics["candidate_invalid"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    verification = summary["candidate_verification"][0]
    assert verification["status"] == "invalid"
    assert any("requires at least one checker_id" in issue for issue in verification["issues"])


@pytest.mark.asyncio
@pytest.mark.parametrize("variant", ["without-oracle", "bare-agent"])
async def test_formal_blind_variants_allow_dispatcher_routing_without_checker_ids(
    tmp_path: Path, variant: Literal["without-oracle", "bare-agent"]
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path)
    output = tmp_path / f"formal-{variant}"

    result = await run(
        ExperimentPlan(
            run_id=f"formal-{variant}",
            experiment="agent-audit",
            variant=variant,
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        output,
        _CandidateBackend(checker_ids=()),
    )

    assert result.status == "completed"
    assert result.result_valid is True
    assert result.metrics["candidate_verified"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["candidate_verification"][0]["status"] == "verified"
    invocations = [
        json.loads(line)
        for line in (manifest.parent / "invocations.jsonl").read_text().splitlines()
    ]
    assert [item["mode"] for item in invocations] == ["verify"]


@pytest.mark.asyncio
async def test_formal_unknown_explicit_checker_id_is_invalid_before_dispatch(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path)
    output = tmp_path / "formal-unknown-checker"

    result = await run(
        ExperimentPlan(
            run_id="formal-unknown-checker",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        output,
        _CandidateBackend(checker_ids=("UNKNOWN",)),
    )

    assert result.status == "failed"
    assert result.result_valid is False
    assert result.metrics["candidate_invalid"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    verification = summary["candidate_verification"][0]
    assert verification["status"] == "invalid"
    assert any("absent from trusted catalog" in issue for issue in verification["issues"])
    assert not (manifest.parent / "invocations.jsonl").exists()


@pytest.mark.asyncio
async def test_formal_bundle_tamper_fails_before_worker(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    manifest, toolchains = _checker_bundle(tmp_path, tamper_catalog=True)
    backend = _FakeBackend()

    result = await run(
        ExperimentPlan(
            run_id="tampered-bundle",
            experiment="agent-audit",
            variant="full",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        tmp_path / "tampered-output",
        backend,
    )

    assert result.status == "failed"
    assert result.metadata["execution_completed"] is False
    assert result.metadata["result_valid"] is False
    assert "SHA-256 mismatch" in (result.error or "")
    assert backend.requests == []


@pytest.mark.asyncio
async def test_formal_bundle_rejects_spliced_invariants_before_worker(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    manifest, toolchains = _checker_bundle(tmp_path)
    spliced = tmp_path / "spliced-invariants.jsonl"
    spliced.write_text(
        '{"invariant_id":"INV-SPLICED","statement":"DO_NOT_SHOW"}\n',
        encoding="utf-8",
    )
    backend = _FakeBackend()

    result = await run(
        ExperimentPlan(
            run_id="spliced-invariants",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "accepted_invariants": str(spliced),
                "require_verified_candidates": True,
            },
        ),
        1,
        tmp_path / "spliced-output",
        backend,
    )

    assert result.status == "failed"
    assert "does not match checker-bundle scoped_invariants" in (result.error or "")
    assert backend.requests == []
    assert not (tmp_path / "spliced-output").exists()


@pytest.mark.asyncio
async def test_formal_zero_candidate_run_is_a_valid_negative_outcome(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    manifest, toolchains = _checker_bundle(tmp_path)

    result = await run(
        ExperimentPlan(
            run_id="formal-negative",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        tmp_path / "formal-negative-output",
        _FakeBackend(),
    )

    assert result.success
    assert result.result_valid is True
    assert result.outcome == "no-verified-findings"
    assert result.metrics["candidate_admitted"] == 0


@pytest.mark.asyncio
async def test_formal_rejected_candidate_is_a_valid_no_findings_result(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path, verdict="PASS")
    output = tmp_path / "formal-rejected-output"

    result = await run(
        ExperimentPlan(
            run_id="formal-rejected",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        output,
        _CandidateBackend(),
    )

    assert result.status == "completed"
    assert result.success
    assert result.result_valid is True
    assert result.outcome == "no-verified-findings"
    assert result.metrics["candidate_rejected_by_verification"] == 1
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["candidate_verification"][0]["status"] == "rejected"
    assert summary["candidate_terminal_outcomes"]["all_admitted_terminal"] is True
    assert summary["candidate_terminal_outcomes"]["all_admitted_valid"] is True


@pytest.mark.asyncio
async def test_formal_invalid_candidate_evidence_fails_result(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path)
    output = tmp_path / "formal-invalid-output"

    result = await run(
        ExperimentPlan(
            run_id="formal-invalid",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        output,
        _CandidateBackend(evidence_code="bad\nevidence\nwith\nfive\nlines\n"),
    )

    assert result.status == "failed"
    assert not result.success
    assert result.execution_status == "completed"
    assert result.result_valid is False
    assert result.metrics["candidate_invalid"] == 1
    assert (result.error or "").endswith("found 1 invalid and 0 unverified")
    summary = json.loads((output / "agent-audit-summary.json").read_text())
    assert summary["candidate_verification"][0]["status"] == "invalid"
    assert summary["candidate_terminal_outcomes"]["all_admitted_terminal"] is True
    assert summary["candidate_terminal_outcomes"]["all_admitted_valid"] is False
    assert summary["result_valid"] is False


@pytest.mark.asyncio
async def test_formal_unverified_candidate_fails_stage(tmp_path: Path) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    (source / "compiler.c").write_text("one\ntwo\nthree\nfour\nfive\n", encoding="utf-8")
    manifest, toolchains = _checker_bundle(tmp_path, sleep_seconds=0.2)
    output = tmp_path / "formal-timeout-output"

    result = await run(
        ExperimentPlan(
            run_id="formal-timeout",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "families": ["A"],
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_results": True,
                "verification_timeout_seconds": 0.01,
            },
        ),
        1,
        output,
        _CandidateBackend(),
    )

    assert result.status == "failed"
    assert result.execution_status == "completed"
    assert result.result_valid is False
    assert result.metrics["candidate_unverified"] == 1
    assert (result.error or "").endswith("found 0 invalid and 1 unverified")


@pytest.mark.asyncio
async def test_formal_mode_requires_bundle_and_explicit_toolchains_before_worker(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    backend = _FakeBackend()

    missing_bundle = await run(
        ExperimentPlan(
            run_id="missing-bundle",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "require_verified_candidates": True,
            },
        ),
        1,
        tmp_path / "missing-bundle-output",
        backend,
    )
    manifest, _ = _checker_bundle(tmp_path)
    missing_toolchains = await run(
        ExperimentPlan(
            run_id="missing-toolchains",
            experiment="agent-audit",
            variant="without-oracle",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "checker_bundle_manifest": str(manifest),
                "require_formal_verification": True,
            },
        ),
        1,
        tmp_path / "missing-toolchains-output",
        backend,
    )

    assert "requires checker_bundle_manifest" in (missing_bundle.error or "")
    assert "requires an explicit toolchains_config" in (missing_toolchains.error or "")
    assert backend.requests == []


@pytest.mark.asyncio
async def test_bare_runner_is_one_neutral_worker_and_can_keep_workspace(
    tmp_path: Path,
) -> None:
    reference = _reference(tmp_path)
    source = _source(tmp_path)
    full_backend = _FakeBackend()
    full_result = await run(
        ExperimentPlan(
            run_id="full-for-parity",
            experiment="agent-audit",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "online_oracle_command": _UNUSED_ONLINE_ORACLE_COMMAND,
            },
        ),
        1,
        tmp_path / "full-run",
        full_backend,
    )
    backend = _FakeBackend()
    manifest, toolchains = _checker_bundle(tmp_path)
    result = await run(
        ExperimentPlan(
            run_id="bare",
            experiment="agent-audit",
            variant="bare-agent",
            source_root=source,
            parameters={
                "reference_root": str(reference),
                "compiler": "gcc",
                "mechanisms": ["stack-protector"],
                "families": ["A"],
                "keep_workspace": True,
                "checker_bundle_manifest": str(manifest),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
            },
        ),
        1,
        tmp_path / "bare-run",
        backend,
    )

    assert result.success
    assert len(backend.requests) == 1
    request = backend.requests[0]
    assert request.metadata["family"] == "neutral"
    assert "FAMILY_A_ONLY" not in request.prompt
    assert "stack-protector" not in request.prompt
    assert "BUNDLE_SCOPED_INVARIANT_SENTINEL" not in request.prompt
    assert "BUNDLE_SCOPED_INVARIANT_SENTINEL" not in json.dumps(
        request.metadata, sort_keys=True
    )
    assert "BUNDLE_SCOPED_INVARIANT_SENTINEL" not in backend.workspace_text[0]
    assert "AuditReport" not in request.prompt
    assert request.cwd.is_dir()
    assert result.metadata["workspace_path"] == str(request.cwd)
    assert result.metadata["workspace_kept"] is True
    assert result.metadata["host_absolute_path_read_isolation"] is True
    assert result.metadata["generated_invariants"]["visible_to_worker"] is False
    assert result.metadata["generated_invariants"]["records"] == 0
    assert result.metadata["generated_invariants"]["source"] == (
        "checker-bundle-scoped"
    )
    assert result.metadata["generated_invariants"]["sha256"] == _sha256_bytes(
        (manifest.parent / "scoped-accepted-invariants.jsonl").read_bytes()
    )
    assert full_result.metadata["workspace_sha256"] == result.metadata["workspace_sha256"]
    request.cwd.chmod(request.cwd.stat().st_mode | stat.S_IRWXU)
    for path in request.cwd.rglob("*"):
        path.chmod(path.stat().st_mode | stat.S_IRWXU)
    shutil.rmtree(request.cwd)
