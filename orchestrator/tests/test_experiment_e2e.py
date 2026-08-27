from __future__ import annotations

import hashlib
import json
import shutil
import subprocess
from pathlib import Path
from typing import Any, Literal

import pytest
import yaml

from defuzz_loop import agent_audit
from defuzz_loop.audit_schema import AuditCandidate
from defuzz_loop.candidate_verification import candidate_fingerprint
from defuzz_loop.checker_bundle import load_checker_bundle
from defuzz_loop.experiment_engine import AgentResult, BudgetEnvelope, ExperimentPlan
from defuzz_loop.experiment_engine.checker_authoring import CheckerAuthoringRunner
from defuzz_loop.online_oracle import checker_bundle_dispatcher_argv

REPO_ROOT = Path(__file__).resolve().parents[2]
CHECKER_ID = "INV-IBT-B01"
TRIGGER_SOURCE = """
unsigned long long gadget(void) {
    return 0x1fa1e0ff3abULL;
}
""".lstrip()
TRIGGER_FLAGS = (
    "--target=x86_64-linux-gnu",
    "-O2",
    "-fcf-protection=branch",
    "-c",
)
TARGET_EVIDENCE = """
bool valid_ibt_immediate(unsigned long long value) {
    for (unsigned shift = 0; shift != 8; ++shift) {
        const auto candidate = value >> (shift * 8);
        if ((candidate & 0xffffffffULL) == 0xfa1e0ff3ULL)
            return false;
    }
    return true;
}
""".lstrip()


class _NoOpAuthoringBackend:
    provider = "fake"
    model = "no-model"

    def __init__(self) -> None:
        self.requests: list[Any] = []

    async def run(self, request: Any) -> AgentResult:
        self.requests.append(request)
        return AgentResult(
            success=True,
            final={"summary": "use the existing INV-IBT-B01 checker"},
            usage={"input_tokens": 2, "output_tokens": 1, "total_tokens": 3},
        )


class _IBTAuditBackend:
    def __init__(self, *, clang_version: str) -> None:
        self.clang_version = clang_version
        self.requests: list[Any] = []

    async def run(self, request: Any) -> AgentResult:
        self.requests.append(request)
        evidence = (request.cwd / "compiler" / "ibt_lowering.cc").read_text(encoding="utf-8")
        return AgentResult(
            success=True,
            final={
                "schema_version": 1,
                "family": request.metadata["family"],
                "variant": request.metadata["variant"],
                "toolchain_version": self.clang_version,
                "discovered": "2026-08-27",
                "scope": "x86_64 CET IBT immediate encoding",
                "audited_components": ["compiler/ibt_lowering.cc"],
                "cross_isa_matrix": {"x86_64": "audited"},
                "cross_mechanism_matrix": {"ibt": "audited"},
                "candidates": [_candidate_payload(evidence, self.clang_version)],
                "coverage_gaps": [],
                "next_steps": ["Retain the trigger as a regression test."],
                "worker_bundle_sha256": request.metadata["worker_bundle_sha256"],
            },
        )


def _candidate_payload(evidence: str, clang_version: str) -> dict[str, Any]:
    return {
        "schema_version": 1,
        "id": "IBT-E2E-CANDIDATE",
        "title": "ENDBR64 bytes appear inside a function body",
        "toolchain": "clang",
        "toolchain_version": clang_version,
        "mechanism": "ibt",
        "isa": ["x86_64"],
        "checker_ids": [CHECKER_ID],
        "invariant_violated": "ENDBR64 may appear only at intentional branch targets.",
        "root_cause": "An immediate embeds the ENDBR64 byte sequence.",
        "layer": "x86 backend immediate lowering",
        "evidence_file_line": ["compiler/ibt_lowering.cc:1"],
        "evidence_code": evidence,
        "evidence": [
            {
                "file": "compiler/ibt_lowering.cc",
                "line": 1,
                "symbol": "valid_ibt_immediate",
                "excerpt": evidence,
            }
        ],
        "minimal_trigger": {
            "source": TRIGGER_SOURCE,
            "flags": list(TRIGGER_FLAGS),
            "target": "x86_64-linux-gnu",
            "isa": "x86_64",
            "language": "c",
        },
        "impact": "An unintended ENDBR64 creates an extra valid indirect branch target.",
        "why_not_rescued": "The assembler and loader preserve the emitted bytes.",
        "poc_verified": False,
        "poc_verification_plan": ("Compile the trigger and run the trusted INV-IBT-B01 checker."),
        "suggested_regression_test": "Reject embedded ENDBR64 immediates.",
        "related_historical": [],
        "related_invariants": [CHECKER_ID],
        "severity": "high",
        "severity_justification": "The bytes weaken hardware-enforced control flow.",
        "discovered": "2026-08-27",
    }


def _clang_or_skip(tmp_path: Path) -> tuple[str, str]:
    clang = shutil.which("clang")
    if clang is None:
        pytest.skip("clang is unavailable")
    probe = subprocess.run(
        (clang, *TRIGGER_FLAGS, "-x", "c", "-", "-o", str(tmp_path / "probe.o")),
        input=TRIGGER_SOURCE,
        text=True,
        capture_output=True,
        check=False,
    )
    if probe.returncode != 0:
        pytest.skip(
            "clang does not support the x86_64 ELF/-fcf-protection fixture: " + probe.stderr.strip()
        )
    version = subprocess.run(
        (clang, "--version"), text=True, capture_output=True, check=True
    ).stdout.splitlines()[0]
    return clang, version


def _write_reference_docs(root: Path) -> None:
    (root / ".claude" / "agents").mkdir(parents=True)
    (root / "docs" / "prompts").mkdir(parents=True)
    (root / "docs" / "invariants").mkdir(parents=True)
    (root / ".claude" / "agents" / "defend-reviewer.md").write_text(
        "Audit compiler defenses from source and report reproducible evidence.\n",
        encoding="utf-8",
    )
    (root / "docs" / "prompts" / "full-review.md").write_text(
        "### SUBAGENT C — control-flow integrity\n"
        "Inspect IBT lowering and emitted landing pads.\n\n"
        "## Uniform subagent instructions\n"
        "Return a complete, evidence-grounded candidate.\n\n"
        "## Phase 2: stop\n",
        encoding="utf-8",
    )
    (root / "docs" / "invariants" / "README.md").write_text(
        "Compiler defense invariants.\n", encoding="utf-8"
    )
    (root / "docs" / "invariants" / "endbr-ibt.md").write_text(
        "INV-IBT-B01 forbids unintended ENDBR opcodes inside function bodies.\n",
        encoding="utf-8",
    )


def _run_dispatcher(
    *,
    mode: Literal["online", "verify"],
    bundle: Any,
    toolchains: Path,
    candidate_path: Path,
    fingerprint: str,
    compiler: str = "clang",
) -> tuple[subprocess.CompletedProcess[str], dict[str, Any]]:
    command = checker_bundle_dispatcher_argv(bundle, toolchains, mode=mode, compiler=compiler)
    argv = tuple(
        argument.replace("{candidate_json}", str(candidate_path)).replace(
            "{candidate_fingerprint}", fingerprint
        )
        for argument in command
    )
    completed = subprocess.run(
        argv, cwd=candidate_path.parent, text=True, capture_output=True, check=False, timeout=60
    )
    return completed, json.loads(completed.stdout)


@pytest.mark.asyncio
async def test_real_checker_bundle_crosses_python_go_and_clang_without_a_model(
    tmp_path: Path,
) -> None:
    clang, clang_version = _clang_or_skip(tmp_path)
    source_status_before = subprocess.run(
        ("git", "status", "--porcelain=v1", "--untracked-files=all"),
        cwd=REPO_ROOT,
        text=True,
        capture_output=True,
        check=True,
    ).stdout

    accepted = tmp_path / "accepted-invariants.jsonl"
    accepted.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "invariant_id": CHECKER_ID,
                "statement": "ENDBR opcodes must not occur unintentionally in function bodies.",
                "observation": "An immediate can encode ENDBR64 at a non-entry offset.",
                "generation_path": "combined",
                "provenance": [{"source_id": "ibt-e2e-reference"}],
                "compiler": "clang",
                "version": clang_version,
                "target": ["x86_64"],
                "mechanism": "ibt",
                "source_kind": "reference",
                "source_url_or_path": "docs/invariants/endbr-ibt.md",
                "evidence_snippet": "scan executable sections byte by byte",
                "falsifiability": "compile the trigger and inspect every ENDBR64 offset",
                "grounding": "accepted",
                "novelty": "cross-language protocol fixture",
            },
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )
    authoring_output = tmp_path / "part-ii"
    authoring_backend = _NoOpAuthoringBackend()
    authoring_result = await CheckerAuthoringRunner(backend=authoring_backend).run(
        ExperimentPlan(
            run_id="ibt-cross-language-e2e-authoring",
            experiment="checker-authoring",
            source_root=REPO_ROOT,
            budget=BudgetEnvelope(token_budget=100, time_budget_minutes=5),
            parameters={
                "accepted_invariants": str(accepted),
                "max_attempts": 1,
                "validation_timeout_seconds": 180,
            },
        ),
        1,
        authoring_output,
    )

    assert authoring_result.success
    assert authoring_result.metrics["bundle_ready"] is True
    assert len(authoring_backend.requests) == 1
    authoring_row = json.loads((authoring_output / "results.jsonl").read_text(encoding="utf-8"))
    assert authoring_row["invariant_id"] == CHECKER_ID
    assert authoring_row["final_status"] == "passed"
    assert authoring_row["files"] == []
    assert len(authoring_row["attempts"][0]["validation"]) == 3
    assert all(item["status"] == "passed" for item in authoring_row["attempts"][0]["validation"])
    token_rows = [
        json.loads(line)
        for line in (authoring_output / "token_usage.jsonl").read_text().splitlines()
    ]
    assert len(token_rows) == 1
    assert token_rows[0]["total_tokens"] == 3

    bundle = load_checker_bundle(authoring_output)
    assert bundle.manifest.status == "ready"
    assert bundle.manifest.included_invariant_ids == [CHECKER_ID]
    assert bundle.manifest.validation.status == "passed"
    assert bundle.manifest.validation.build is not None
    assert bundle.manifest.validation.build["status"] == "passed"

    toolchains = tmp_path / "toolchains.yaml"
    toolchains.write_text(
        yaml.safe_dump(
            {"toolchains": {"x86_64": {"clang_path": clang, "cflags": []}}},
            sort_keys=False,
        ),
        encoding="utf-8",
    )
    target_root = tmp_path / "target"
    evidence_path = target_root / "compiler" / "ibt_lowering.cc"
    evidence_path.parent.mkdir(parents=True)
    evidence_path.write_text(TARGET_EVIDENCE, encoding="utf-8")
    reference_root = tmp_path / "reference"
    _write_reference_docs(reference_root)

    audit_backend = _IBTAuditBackend(clang_version=clang_version)
    candidate = AuditCandidate.model_validate(_candidate_payload(TARGET_EVIDENCE, clang_version))
    candidate_path = tmp_path / "candidate.json"
    candidate_path.write_text(
        json.dumps(
            candidate.model_dump(mode="json"),
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
        ),
        encoding="utf-8",
    )
    fingerprint = candidate_fingerprint(candidate)
    assert hashlib.sha256(candidate_path.read_bytes()).hexdigest() == fingerprint

    gcc_only_toolchains = tmp_path / "gcc-only-toolchains.yaml"
    gcc_only_toolchains.write_text(
        yaml.safe_dump(
            {"toolchains": {"x86_64": {"gcc_path": clang, "cflags": []}}},
            sort_keys=False,
        ),
        encoding="utf-8",
    )
    no_fallback, no_fallback_payload = _run_dispatcher(
        mode="online",
        bundle=bundle,
        toolchains=gcc_only_toolchains,
        candidate_path=candidate_path,
        fingerprint=fingerprint,
    )
    assert no_fallback.returncode == 2
    assert no_fallback_payload["verdict"] == "ERROR"
    assert "clang_path is not configured" in no_fallback_payload["feedback"]
    assert no_fallback_payload["builds"] == []

    mismatch, mismatch_payload = _run_dispatcher(
        mode="online",
        bundle=bundle,
        toolchains=toolchains,
        candidate_path=candidate_path,
        fingerprint=fingerprint,
        compiler="gcc",
    )
    assert mismatch.returncode == 2
    assert mismatch_payload["verdict"] == "ERROR"
    assert "does not match trusted compiler" in mismatch_payload["feedback"]
    assert mismatch_payload["builds"] == []

    online, online_payload = _run_dispatcher(
        mode="online",
        bundle=bundle,
        toolchains=toolchains,
        candidate_path=candidate_path,
        fingerprint=fingerprint,
    )
    verify, verify_payload = _run_dispatcher(
        mode="verify",
        bundle=bundle,
        toolchains=toolchains,
        candidate_path=candidate_path,
        fingerprint=fingerprint,
    )
    for completed, payload in ((online, online_payload), (verify, verify_payload)):
        assert completed.returncode == 0, completed.stderr
        assert payload["candidate_fingerprint"] == fingerprint
        assert payload["echoed_candidate_fingerprint"] == fingerprint
        assert payload["verdict"] == "FAIL"
        assert [item["id"] for item in payload["results"]] == [CHECKER_ID]
        assert payload["builds"][0]["success"] is True
        assert payload["builds"][0]["compiler"] == clang
        assert set(TRIGGER_FLAGS) <= set(payload["builds"][0]["effective_flags"])

    audit_output = tmp_path / "part-iii"
    audit_result = await agent_audit.run(
        ExperimentPlan(
            run_id="ibt-cross-language-e2e-audit",
            experiment="agent-audit",
            variant="full",
            source_root=target_root,
            budget=BudgetEnvelope(token_budget=100, time_budget_minutes=5),
            parameters={
                "reference_root": str(reference_root),
                "compiler": "clang",
                "toolchain_versions": {"clang": clang_version},
                "families": ["C"],
                "mechanisms": ["ibt"],
                "isas": ["x86_64"],
                "checker_bundle_manifest": str(bundle.manifest_path),
                "toolchains_config": str(toolchains),
                "require_verified_candidates": True,
                "require_host_read_isolation": False,
                "oracle_rounds": 1,
                "online_oracle_timeout_seconds": 60,
                "verification_timeout_seconds": 60,
            },
        ),
        1,
        audit_output,
        audit_backend,
    )

    assert audit_result.success
    assert audit_result.result_valid is True
    assert audit_result.outcome == "verified-findings"
    assert [request.output_dir.name for request in audit_backend.requests] == [
        "worker-c-initial",
        "worker-c-oracle-001",
    ]
    summary = json.loads((audit_output / "agent-audit-summary.json").read_text(encoding="utf-8"))
    assert all(
        request.metadata["worker_bundle_sha256"]
        == summary["worker_validity"][0]["expected_worker_bundle_sha256"]
        for request in audit_backend.requests
    )
    assert (
        summary["worker_validity"][0]["worker_bundle_sha256"]
        == summary["worker_validity"][0]["expected_worker_bundle_sha256"]
    )
    assert summary["online_oracle"]["records"][0]["verdict"] == "FAIL"
    assert summary["candidate_verification"][0]["status"] == "verified"
    assert summary["candidate_verification"][0]["original_poc_verified_claim"] is False
    assert summary["candidate_verification"][0]["execution_records"][0]["verdict"] == ("FAIL")
    assert summary["candidate_verification"][0]["execution_records"][0]["result_valid"] is True
    assert summary["result_valid"] is True
    assert summary["outcome"] == "verified-findings"
    assert summary["checker_bundle"]["bundle_id"] == bundle.manifest.bundle_id
    assert (
        summary["checker_bundle"]["manifest_sha256"]
        == hashlib.sha256(bundle.manifest_path.read_bytes()).hexdigest()
    )
    assert summary["checker_bundle"]["compiler"] == "llvm"

    source_status_after = subprocess.run(
        ("git", "status", "--porcelain=v1", "--untracked-files=all"),
        cwd=REPO_ROOT,
        text=True,
        capture_output=True,
        check=True,
    ).stdout
    assert source_status_after == source_status_before
