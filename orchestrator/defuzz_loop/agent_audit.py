"""Runnable Part III audit stage with deterministic worker fanout."""

from __future__ import annotations

import asyncio
import hashlib
import json
import shutil
import stat
import tempfile
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .admission import admit_report, candidate_leaks_findings
from .audit_schema import (
    AuditCandidate,
    AuditFamily,
    AuditReport,
    AuditVariant,
    audit_report_json_schema,
    families_for_mechanisms,
    parse_audit_report,
)
from .candidate_verification import VerificationCommand, verify_candidate
from .experiment_engine import (
    AgentBackend,
    AgentRequest,
    ArtifactRef,
    ExecAgentBackend,
    ExperimentPlan,
    StageResult,
    WorkspaceBuilder,
)
from .online_oracle import CommandOnlineOracle, render_oracle_feedback
from .parity import evaluate_demo_parity, finding_identity, parity_metrics
from .prompt_bundle import (
    WorkerPromptBundle,
    assert_no_findings_leak,
    assert_worker_safe_path,
    build_worker_prompt_bundle,
    policy_for_variant,
)
from .token_usage import TokenUsageSink, current_token_usage_sink

DEFAULT_REFERENCE_ROOT = Path("/Users/bytedance/projects/research/defend-reviewer/main")
_WORKSPACE_ISOLATION_LEVEL = "workspace-copy"
_CANDIDATE_ADMISSION_SCOPE = "structural-completeness-only"


class _AuditWorkspaceBuilder(WorkspaceBuilder):
    """Part III workspace policy: no result, VCS, or run corpus is copied."""

    @staticmethod
    def _denied(relative: Any) -> bool:
        folded = tuple(part.casefold() for part in relative.parts)
        return WorkspaceBuilder._denied(relative) or "reports" in folded


@dataclass(frozen=True)
class _WorkspaceLease:
    root: Path
    sha256: str
    labels: tuple[str, ...]
    cleanup: Any


def _materialize_workspace(
    source_roots: Sequence[Path], *, repetition: int
) -> _WorkspaceLease:
    root = Path(tempfile.mkdtemp(prefix=f"defuzz-audit-r{repetition:03d}-")).resolve()
    labels = (".",) if len(source_roots) == 1 else tuple(
        f"source-{index}" for index in range(1, len(source_roots) + 1)
    )
    aggregate = hashlib.sha256()
    try:
        for source, label in zip(source_roots, labels, strict=True):
            target = root if label == "." else root / label
            manifest = _AuditWorkspaceBuilder(source).materialize(target)
            aggregate.update(label.encode("utf-8"))
            aggregate.update(b"\0")
            aggregate.update(manifest.sha256.encode("ascii"))
            aggregate.update(b"\n")
        for path in sorted(root.rglob("*"), reverse=True):
            path.chmod(path.stat().st_mode & ~0o222)
        root.chmod(root.stat().st_mode & ~0o222)
    except Exception:
        shutil.rmtree(root, ignore_errors=True)
        raise

    def cleanup() -> None:
        if root.exists():
            root.chmod(root.stat().st_mode | stat.S_IRWXU)
            for path in root.rglob("*"):
                if path.is_dir():
                    path.chmod(path.stat().st_mode | stat.S_IRWXU)
        shutil.rmtree(root, ignore_errors=True)

    return _WorkspaceLease(
        root=root, sha256=aggregate.hexdigest(), labels=labels, cleanup=cleanup
    )


def _bare_report_json_schema() -> dict[str, Any]:
    """A neutral transport shape, intentionally not the DeFuzz audit schema."""

    return {
        "type": "object",
        "properties": {
            "worker_bundle_sha256": {"type": "string", "minLength": 64},
            "issues": {
                "type": "array",
                "items": {"type": "object", "additionalProperties": True},
            },
            "coverage_gaps": {"type": "array", "items": {}},
        },
        "required": ["issues", "worker_bundle_sha256"],
        "additionalProperties": True,
    }


def _structured_report_json_schema() -> dict[str, Any]:
    """Require every structured worker to echo its delivered bundle identity."""

    schema = audit_report_json_schema()
    required = set(schema.get("required", []))
    required.add("worker_bundle_sha256")
    schema["required"] = sorted(required)
    properties = schema.setdefault("properties", {})
    worker_hash = properties.setdefault("worker_bundle_sha256", {"type": "string"})
    worker_hash["minLength"] = 64
    worker_hash["maxLength"] = 64
    return schema


def _parse_worker_report(bundle: WorkerPromptBundle, value: Any) -> AuditReport:
    if bundle.variant is not AuditVariant.BARE_AGENT:
        if isinstance(value, Mapping):
            payload = dict(value)
            # AuditReport has compatibility defaults, but worker identity must be
            # an explicit echo rather than silently repaired by the orchestrator.
            payload.setdefault("family", "")
            payload.setdefault("variant", "")
            return parse_audit_report(payload)
        return parse_audit_report(value)
    if not isinstance(value, Mapping):
        return AuditReport(
            family=bundle.family.key,
            variant=bundle.variant.value,
            parse_issues=["bare worker output is not a JSON object"],
        )
    issues = value.get("issues", [])
    gaps = value.get("coverage_gaps", [])
    if not isinstance(issues, list):
        return AuditReport(
            family=bundle.family.key,
            variant=bundle.variant.value,
            parse_issues=["bare worker issues is not an array"],
        )
    candidates: list[AuditCandidate] = []
    parse_issues = [
        "bare worker emitted a non-object issue"
        for item in issues
        if not isinstance(item, Mapping)
    ]
    for index, item in enumerate(issues):
        if not isinstance(item, Mapping):
            continue
        try:
            candidates.append(AuditCandidate.model_validate(item))
        except (TypeError, ValueError) as exc:
            parse_issues.append(f"bare worker issue {index} could not be parsed: {exc}")
    return AuditReport(
        family=str(value.get("family", bundle.family.key)),
        variant=str(value.get("variant", bundle.variant.value)),
        worker_bundle_sha256=str(value.get("worker_bundle_sha256", "")),
        candidates=candidates,
        coverage_gaps=gaps if isinstance(gaps, list) else [],
        parse_issues=parse_issues,
    )


def _worker_invalid_reasons(
    bundle: WorkerPromptBundle, report: AuditReport
) -> list[str]:
    reasons = [f"parse issue: {issue}" for issue in report.parse_issues]
    if report.tainted:
        reasons.append("worker result is tainted by findings corpus content")
    if not report.worker_bundle_sha256:
        reasons.append("worker_bundle_sha256 echo is missing")
    elif report.worker_bundle_sha256 != bundle.sha256:
        reasons.append("worker_bundle_sha256 does not match the delivered bundle")
    if report.family != bundle.family.key:
        reasons.append(
            f"worker family echo {report.family!r} does not match {bundle.family.key!r}"
        )
    if report.variant != bundle.variant.value:
        reasons.append(
            f"worker variant echo {report.variant!r} does not match "
            f"{bundle.variant.value!r}"
        )
    return list(dict.fromkeys(reasons))


def _as_list(value: Any) -> list[Any]:
    if value is None:
        return []
    if isinstance(value, (str, Path)):
        return [value]
    return list(value)


def _write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if hasattr(value, "model_dump"):
        value = value.model_dump(mode="json")
    path.write_text(
        json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True, default=str) + "\n",
        encoding="utf-8",
    )


def _coerce_plan(plan: ExperimentPlan | Mapping[str, Any]) -> ExperimentPlan:
    return plan if isinstance(plan, ExperimentPlan) else ExperimentPlan.from_dict(plan)


def _toolchains(parameters: Mapping[str, Any]) -> list[str]:
    raw = parameters.get("toolchains", parameters.get("compiler", ["gcc", "llvm"]))
    return [str(item) for item in _as_list(raw)]


def _toolchain_versions(parameters: Mapping[str, Any]) -> dict[str, str]:
    raw = parameters.get("toolchain_versions", {})
    versions = (
        {str(key): str(value) for key, value in raw.items()}
        if isinstance(raw, Mapping)
        else {}
    )
    for suffix in ("a", "b"):
        value = parameters.get(f"toolchain_version_{suffix}")
        if value:
            versions[suffix] = str(value)
    return versions


def _source_roots(plan: ExperimentPlan) -> list[Path]:
    values = _as_list(plan.parameters.get("source_roots"))
    for key in ("toolchain_tree_a", "toolchain_tree_b"):
        if plan.parameters.get(key):
            values.append(plan.parameters[key])
    if plan.source_root is not None:
        values.insert(0, plan.source_root)
    roots = [Path(value).expanduser().resolve(strict=False) for value in values]
    for root in roots:
        assert_worker_safe_path(root)
    return list(dict.fromkeys(roots))


def _deduplicate(candidates: Sequence[AuditCandidate]) -> list[AuditCandidate]:
    """Collapse exact normalized identities while retaining first-worker order."""

    seen: set[tuple[str, str, str]] = set()
    result: list[AuditCandidate] = []
    for candidate in candidates:
        identity = finding_identity(candidate).key()
        if identity in seen:
            continue
        seen.add(identity)
        result.append(candidate)
    return result


async def _run_worker(
    backend: AgentBackend,
    bundle: WorkerPromptBundle,
    *,
    cwd: Path,
    output_dir: Path,
    schema_path: Path,
    timeout_seconds: float,
    semaphore: asyncio.Semaphore,
    token_sink: TokenUsageSink | None = None,
    deny_read_paths: Sequence[Path] = (),
    require_host_read_isolation: bool = True,
    prompt_suffix: str = "",
    invocation_label: str = "initial",
) -> tuple[WorkerPromptBundle, AuditReport, str | None]:
    worker_dir = output_dir / f"worker-{bundle.family.key.lower()}-{invocation_label}"
    worker_dir.mkdir(parents=True, exist_ok=True)
    delivered_prompt = (
        bundle.prompt
        + "\n\n# Worker bundle identity\n\n"
        + "Echo this exact value as worker_bundle_sha256 in the JSON response: "
        + bundle.sha256
        + "."
        + prompt_suffix
    )
    (worker_dir / "prompt.txt").write_text(delivered_prompt, encoding="utf-8")
    assert_no_findings_leak(delivered_prompt)
    request = AgentRequest(
        prompt=delivered_prompt,
        cwd=cwd,
        output_dir=worker_dir,
        schema_path=schema_path,
        timeout_seconds=timeout_seconds,
        writable=False,
        token_sink=token_sink,
        deny_read_paths=list(deny_read_paths),
        require_host_read_isolation=require_host_read_isolation,
        metadata={
            "stage": "agent-audit",
            "family": bundle.family.key,
            "variant": bundle.variant.value,
            "agent": f"audit-family-{bundle.family.key}",
            "worker_bundle_sha256": bundle.sha256,
            "findings_access": "denied",
            "isolation_level": _WORKSPACE_ISOLATION_LEVEL,
        },
    )
    async with semaphore:
        try:
            result = await backend.run(request)
        except Exception as exc:
            report = AuditReport(
                family=bundle.family.key,
                variant=bundle.variant.value,
                parse_issues=[f"backend raised {type(exc).__name__}: {exc}"],
            )
            return bundle, report, str(exc)
    if not result.success:
        message = result.error or f"backend exited with status {result.exit_code}"
        report = AuditReport(
            family=bundle.family.key,
            variant=bundle.variant.value,
            parse_issues=[message],
        )
        return bundle, report, message
    raw_output = (
        result.final
        if isinstance(result.final, str)
        else json.dumps(result.final, ensure_ascii=False, default=str)
    )
    leaked_output = False
    try:
        assert_no_findings_leak(raw_output)
    except ValueError:
        leaked_output = True
    report = _parse_worker_report(bundle, result.final)
    if leaked_output or any(
        candidate_leaks_findings(candidate) for candidate in report.candidates
    ):
        report.tainted = True
    _write_json(worker_dir / "audit-report.json", report)
    admission = admit_report(report)
    admission_payload = admission.model_dump(mode="json")
    admission_payload["candidate_admission"] = {
        "scope": _CANDIDATE_ADMISSION_SCOPE,
        "deterministic_poc_validator_executed": False,
    }
    invalid_reasons = _worker_invalid_reasons(bundle, report)
    admission_payload["worker_valid"] = not invalid_reasons
    admission_payload["worker_invalid_reasons"] = invalid_reasons
    if invalid_reasons:
        admission_payload["admitted"] = []
        admission_payload["rejected"] = [
            candidate.model_dump(mode="json") for candidate in report.candidates
        ]
    _write_json(worker_dir / "admission.json", admission_payload)
    return bundle, report, None


async def run(
    plan: ExperimentPlan | Mapping[str, Any],
    repetition: int,
    output_dir: str | Path,
    backend: AgentBackend | None = None,
) -> StageResult:
    """Run Part III once without allocating IDs or archiving findings."""

    resolved = _coerce_plan(plan)
    destination = Path(output_dir).expanduser().resolve(strict=False)
    try:
        variant = AuditVariant(resolved.variant)
        policy_for_variant(variant)
    except ValueError as exc:
        return StageResult(stage="agent-audit", status="failed", error=str(exc))

    parameters = resolved.parameters
    online_oracles: list[CommandOnlineOracle] = []
    oracle_rounds = 0
    if resolved.policy.use_online_oracle:
        raw_commands = parameters.get("online_oracle_command")
        if not raw_commands:
            return StageResult(
                stage="agent-audit",
                status="failed",
                error=(
                    "full agent-audit requires at least one online_oracle_command; "
                    "use the without-oracle ablation to disable online feedback"
                ),
            )
        if isinstance(raw_commands, (str, bytes)):
            return StageResult(
                stage="agent-audit",
                status="failed",
                error="online_oracle_command must be an argv sequence or list of argv sequences",
            )
        command_templates = list(raw_commands)
        if command_templates and all(
            isinstance(item, str) for item in command_templates
        ):
            command_templates = [command_templates]
        try:
            online_oracles = [
                CommandOnlineOracle(template) for template in command_templates
            ]
        except (TypeError, ValueError) as exc:
            return StageResult(
                stage="agent-audit",
                status="failed",
                error=f"invalid online_oracle_command: {exc}",
            )
        if not online_oracles:
            return StageResult(
                stage="agent-audit",
                status="failed",
                error="full agent-audit requires at least one online_oracle_command",
            )
        try:
            oracle_rounds = int(parameters.get("oracle_rounds", 1))
        except (TypeError, ValueError):
            oracle_rounds = 0
        if oracle_rounds <= 0:
            return StageResult(
                stage="agent-audit",
                status="failed",
                error="oracle_rounds must be a positive integer",
            )
    reference_root = Path(
        parameters.get("reference_root", parameters.get("reviewer_root", DEFAULT_REFERENCE_ROOT))
    ).expanduser().resolve(strict=False)
    mechanisms = [
        str(item)
        for item in _as_list(parameters.get("mechanisms", parameters.get("mechanism")))
    ]
    families: tuple[AuditFamily, ...]
    if variant is AuditVariant.BARE_AGENT:
        # The baseline is intentionally one neutral worker.  Mechanism and A--E
        # sharding are treatment information and must not reach this arm.
        families = (families_for_mechanisms(None)[0],)
    else:
        try:
            families = families_for_mechanisms(mechanisms)
        except ValueError as exc:
            return StageResult(stage="agent-audit", status="failed", error=str(exc))
        requested = {str(item).upper() for item in _as_list(parameters.get("families"))}
        if requested:
            families = tuple(
                family
                for family in families
                if family.key in requested or family.name.upper() in requested
            )
    if not families:
        return StageResult(
            stage="agent-audit", status="failed", error="audit scope selected no families"
        )

    source_roots = _source_roots(resolved)
    if not source_roots:
        return StageResult(
            stage="agent-audit",
            status="failed",
            error="agent audit requires at least one explicit source root",
        )
    missing_roots = [root for root in source_roots if not root.is_dir()]
    if missing_roots:
        return StageResult(
            stage="agent-audit",
            status="failed",
            error=f"audit working tree does not exist: {missing_roots[0]}",
        )
    destination.mkdir(parents=True, exist_ok=True)
    schema_path = destination / (
        "worker-output.schema.json"
        if variant is AuditVariant.BARE_AGENT
        else "audit-report.schema.json"
    )
    _write_json(
        schema_path,
        _bare_report_json_schema()
        if variant is AuditVariant.BARE_AGENT
        else _structured_report_json_schema(),
    )

    toolchains = _toolchains(parameters)
    oracle_documents = _as_list(
        parameters.get("oracle_documents", parameters.get("checker_artifacts"))
    )
    isas = [str(item) for item in _as_list(parameters.get("isas", parameters.get("isa")))]
    hypotheses = parameters.get("hypotheses", "")
    concurrency = max(1, int(parameters.get("max_concurrency", len(families))))
    semaphore = asyncio.Semaphore(concurrency)
    try:
        lease = _materialize_workspace(source_roots, repetition=repetition)
    except (OSError, ValueError) as exc:
        return StageResult(stage="agent-audit", status="failed", error=str(exc))
    keep_workspace = bool(
        parameters.get("keep_workspace", parameters.get("keep_workspaces", False))
    )
    try:
        try:
            bundles = [
                build_worker_prompt_bundle(
                    reference_root,
                    family,
                    variant,
                    source_roots=source_roots,
                    toolchains=toolchains,
                    toolchain_versions=_toolchain_versions(parameters),
                    mechanisms=() if variant is AuditVariant.BARE_AGENT else mechanisms,
                    isas=isas,
                    discovered=str(parameters.get("discovered", "")),
                    hypotheses=(
                        ""
                        if variant is AuditVariant.BARE_AGENT
                        else str(
                            hypotheses.get(family.key, hypotheses.get(family.name, ""))
                            if isinstance(hypotheses, Mapping)
                            else hypotheses
                        )
                    ),
                    oracle_documents=oracle_documents,
                )
                for family in families
            ]
        except (OSError, ValueError) as exc:
            return StageResult(stage="agent-audit", status="failed", error=str(exc))

        active_backend = backend or ExecAgentBackend()
        host_isolation = bool(
            getattr(active_backend, "supports_host_read_isolation", False)
        )
        require_host_isolation = bool(
            parameters.get("require_host_read_isolation", True)
        )
        if (
            require_host_isolation
            and hasattr(active_backend, "supports_host_read_isolation")
            and not host_isolation
        ):
            return StageResult(
                stage="agent-audit",
                status="failed",
                error=(
                    "host read isolation is unavailable; run on macOS with sandbox-exec "
                    "or inside an equivalent container"
                ),
            )
        # The CLI installs the repetition sink in ambient context and its backend
        # wrapper reuses this same object.  Passing it through avoids a second sink.
        token_sink = current_token_usage_sink()
        if token_sink is None:
            worker_results = await asyncio.gather(
                *(
                    _run_worker(
                        active_backend,
                        bundle,
                        cwd=lease.root,
                        output_dir=destination,
                        schema_path=schema_path,
                        timeout_seconds=resolved.budget.timeout_seconds,
                        semaphore=semaphore,
                        token_sink=None,
                        deny_read_paths=(reference_root,),
                        require_host_read_isolation=require_host_isolation,
                    )
                    for bundle in bundles
                )
            )
        else:
            # A shared provider-token budget cannot be reserved atomically across
            # concurrent processes. Dispatch serially so every next worker sees
            # usage recorded by the preceding worker.
            worker_results = []
            for bundle in bundles:
                try:
                    token_sink.check_budget()
                except Exception as exc:
                    report = AuditReport(
                        family=bundle.family.key,
                        variant=bundle.variant.value,
                        parse_issues=[f"budget stopped worker: {exc}"],
                    )
                    worker_results.append((bundle, report, str(exc)))
                    continue
                worker_results.append(
                    await _run_worker(
                        active_backend,
                        bundle,
                        cwd=lease.root,
                        output_dir=destination,
                        schema_path=schema_path,
                        timeout_seconds=resolved.budget.timeout_seconds,
                        semaphore=semaphore,
                        token_sink=token_sink,
                        deny_read_paths=(reference_root,),
                        require_host_read_isolation=require_host_isolation,
                    )
                )

        reports: list[AuditReport] = []
        worker_validity: list[dict[str, Any]] = []
        admitted: list[AuditCandidate] = []
        rejected: list[AuditCandidate] = []
        errors: list[str] = []
        for bundle, report, error in worker_results:
            reports.append(report)
            invalid_reasons = _worker_invalid_reasons(bundle, report)
            worker_validity.append(
                {
                    "family": bundle.family.key,
                    "variant": bundle.variant.value,
                    "valid": not invalid_reasons,
                    "invalid_reasons": invalid_reasons,
                    "expected_worker_bundle_sha256": bundle.sha256,
                    "worker_bundle_sha256": report.worker_bundle_sha256,
                }
            )
            if not invalid_reasons:
                outcome = admit_report(report)
                admitted.extend(outcome.admitted)
                rejected.extend(outcome.rejected)
            else:
                errors.extend(
                    f"worker {bundle.family.key} invalid: {reason}"
                    for reason in invalid_reasons
                )
            if error:
                backend_error = f"worker {bundle.family.key}: {error}"
                if backend_error not in errors:
                    errors.append(backend_error)

        online_oracle_records: list[dict[str, Any]] = []
        online_oracle_errors: list[str] = []
        if resolved.policy.use_online_oracle:
            by_family = {bundle.family.key: bundle for bundle in bundles}
            for round_index in range(1, oracle_rounds + 1):
                feedback_by_family: dict[str, list[Any]] = {}
                for report in reports:
                    results = [
                        await oracle.evaluate(candidate, lease.root)
                        for candidate in report.candidates
                        for oracle in online_oracles
                    ]
                    if results:
                        online_oracle_records.extend(
                            {
                                "round": round_index,
                                "family": report.family,
                                **result.model_dump(mode="json"),
                            }
                            for result in results
                        )
                        for result in results:
                            if result.verdict == "ERROR":
                                detail = result.error or result.feedback or "unknown error"
                                message = (
                                    f"online oracle round {round_index} family "
                                    f"{report.family} candidate "
                                    f"{result.candidate_fingerprint}: {detail}"
                                )
                                if message not in online_oracle_errors:
                                    online_oracle_errors.append(message)
                                if message not in errors:
                                    errors.append(message)
                                continue
                            feedback_by_family.setdefault(report.family, []).append(
                                result
                            )
                if not feedback_by_family:
                    break
                revised: dict[str, AuditReport] = {}
                for family_key, oracle_results in feedback_by_family.items():
                    selected_bundle = by_family.get(family_key)
                    if selected_bundle is None:
                        continue
                    feedback = render_oracle_feedback(oracle_results)
                    _, report, error = await _run_worker(
                        active_backend,
                        selected_bundle,
                        cwd=lease.root,
                        output_dir=destination,
                        schema_path=schema_path,
                        timeout_seconds=resolved.budget.timeout_seconds,
                        semaphore=semaphore,
                        token_sink=token_sink,
                        deny_read_paths=(reference_root,),
                        require_host_read_isolation=require_host_isolation,
                        prompt_suffix=(
                            "\n\n# Deterministic online checker feedback\n\n"
                            + feedback
                            + "\n\nRevise the report using this feedback. Preserve the "
                            "worker_bundle_sha256 echo."
                        ),
                        invocation_label=f"oracle-{round_index:03d}",
                    )
                    revised[family_key] = report
                    if error:
                        errors.append(
                            f"worker {family_key} oracle round {round_index}: {error}"
                        )
                if not revised:
                    break
                reports = [revised.get(report.family, report) for report in reports]

            # Recompute structural results from the reports that actually leave
            # the online checker-feedback loop.
            worker_validity = []
            admitted = []
            rejected = []
            for bundle, report in zip(bundles, reports, strict=True):
                invalid_reasons = _worker_invalid_reasons(bundle, report)
                worker_validity.append(
                    {
                        "family": bundle.family.key,
                        "variant": bundle.variant.value,
                        "valid": not invalid_reasons,
                        "invalid_reasons": invalid_reasons,
                        "expected_worker_bundle_sha256": bundle.sha256,
                        "worker_bundle_sha256": report.worker_bundle_sha256,
                    }
                )
                if not invalid_reasons:
                    outcome = admit_report(report)
                    admitted.extend(outcome.admitted)
                    rejected.extend(outcome.rejected)
        admitted = _deduplicate(admitted)
        valid_workers = sum(item["valid"] for item in worker_validity)
        invalid_workers = len(worker_validity) - valid_workers
        raw_commands = parameters.get("verification_command", [])
        verification_commands = [
            VerificationCommand(tuple(str(part) for part in command))
            for command in raw_commands
        ]
        verification_results = [
            await verify_candidate(
                candidate,
                [lease.root],
                commands=verification_commands,
            )
            for candidate in admitted
        ]
        verified_candidates = [
            candidate
            for candidate, verification in zip(
                admitted, verification_results, strict=True
            )
            if verification.confirmed
        ]

        summary = {
            "schema_version": 1,
            "run_id": resolved.run_id,
            "repetition": repetition,
            "variant": variant.value,
            "family_order": [bundle.family.key for bundle in bundles],
            "reports": [report.model_dump(mode="json") for report in reports],
            "worker_validity": worker_validity,
            "admitted_candidates": [
                candidate.model_dump(mode="json") for candidate in admitted
            ],
            "rejected_candidates": [
                candidate.model_dump(mode="json") for candidate in rejected
            ],
            "candidate_verification": [
                verification.model_dump(mode="json")
                for verification in verification_results
            ],
            "online_oracle": {
                "enabled": resolved.policy.use_online_oracle,
                "rounds_configured": int(parameters.get("oracle_rounds", 0)),
                "records": online_oracle_records,
                "error_count": len(online_oracle_errors),
                "operational": not online_oracle_errors,
            },
            "verified_candidates": [
                candidate.model_dump(mode="json") for candidate in verified_candidates
            ],
            "errors": errors,
            "archive_performed": False,
            "isolation_level": _WORKSPACE_ISOLATION_LEVEL,
            "host_absolute_path_read_isolation": host_isolation,
            "workspace_sha256": lease.sha256,
            "candidate_admission": {
                "scope": _CANDIDATE_ADMISSION_SCOPE,
                "deterministic_poc_validator_executed": bool(verification_commands),
            },
        }
        parity_report = None
        parity_path: Path | None = None
        if parameters.get("demo_parity") or parameters.get("parity_threshold") is not None:
            threshold_value = parameters.get("parity_threshold")
            threshold = float(threshold_value) if threshold_value is not None else None
            parity_report = evaluate_demo_parity(
                verified_candidates,
                reference_root / "findings",
                threshold=threshold,
            )
            parity_path = destination / "demo-parity.json"
            _write_json(parity_path, parity_report)
            summary["demo_parity"] = parity_report.model_dump(mode="json")
        summary_path = destination / "agent-audit-summary.json"
        _write_json(summary_path, summary)
        artifacts = [
            ArtifactRef.from_path(schema_path, base_dir=destination, kind="schema"),
            ArtifactRef.from_path(summary_path, base_dir=destination, kind="audit-summary"),
        ]
        if parity_path is not None:
            artifacts.append(
                ArtifactRef.from_path(
                    parity_path, base_dir=destination, kind="demo-parity"
                )
            )
        metadata = {
            "variant": variant.value,
            "family_order": [bundle.family.key for bundle in bundles],
            "archive_performed": False,
            "findings_access": "excluded-from-workspace",
            "isolation_level": _WORKSPACE_ISOLATION_LEVEL,
            "host_absolute_path_read_isolation": host_isolation,
            "isolation_limit": (
                "injected test backends do not exercise OS-level path denial"
                if not hasattr(active_backend, "supports_host_read_isolation")
                else None
            ),
            "workspace_sha256": lease.sha256,
            "workspace_kept": keep_workspace,
            "candidate_admission": {
                "scope": _CANDIDATE_ADMISSION_SCOPE,
                "deterministic_poc_validator_executed": bool(verification_commands),
            },
        }
        if keep_workspace:
            metadata["workspace_path"] = str(lease.root)
        metrics: dict[str, Any] = {
            "workers": len(bundles),
            "valid_workers": valid_workers,
            "invalid_workers": invalid_workers,
            "candidates": sum(len(report.candidates) for report in reports),
            "candidate_admitted": len(admitted),
            "candidate_rejected": len(rejected),
            "candidate_verified": len(verified_candidates),
            "online_oracle_calls": len(online_oracle_records),
            "online_oracle_errors": len(online_oracle_errors),
            "candidate_unverified": sum(
                verification.status == "unverified"
                for verification in verification_results
            ),
            "candidate_invalid_evidence": sum(
                verification.status == "invalid" for verification in verification_results
            ),
        }
        if parity_report is not None:
            parity_summary = parity_metrics(parity_report)
            metrics.update(
                {f"demo_parity_{key}": value for key, value in parity_summary.items()}
            )
        return StageResult(
            stage="agent-audit",
            status=(
                "failed"
                if online_oracle_errors
                else "completed"
                if invalid_workers == 0
                else "partial"
                if valid_workers
                else "failed"
            ),
            artifacts=artifacts,
            metrics=metrics,
            errors=errors,
            metadata=metadata,
        )
    finally:
        if not keep_workspace:
            lease.cleanup()


run_agent_audit = run

__all__ = ["DEFAULT_REFERENCE_ROOT", "run", "run_agent_audit"]
