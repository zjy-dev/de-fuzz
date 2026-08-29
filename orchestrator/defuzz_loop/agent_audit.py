"""Runnable Part III audit stage with deterministic worker fanout."""

from __future__ import annotations

import asyncio
import hashlib
import hmac
import json
import os
import shutil
import stat
import tempfile
import time
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Literal, cast

from .admission import admit_report, candidate_leaks_findings
from .audit_schema import (
    AuditCandidate,
    AuditFamily,
    AuditReport,
    AuditVariant,
    audit_report_json_schema,
    families_for_mechanisms,
    normalize_isa,
    normalize_mechanism,
    parse_audit_report,
)
from .candidate_verification import VerificationCommand, verify_candidate
from .checker_bundle import ValidatedCheckerBundle, load_checker_bundle
from .experiment_engine import (
    AgentBackend,
    AgentRequest,
    ArtifactRef,
    ExecAgentBackend,
    ExperimentPlan,
    StageResult,
    WorkspaceBuilder,
)
from .experiment_engine.workspace import (
    validate_agent_path_isolation,
    validate_disjoint_input_roots,
)
from .online_oracle import (
    CommandOnlineOracle,
    CompilerName,
    checker_bundle_dispatcher_argv,
    normalize_compiler,
    render_oracle_feedback,
)
from .parity import (
    ParityProfile,
    ParityScope,
    ThresholdMetric,
    evaluate_demo_parity,
    finding_identity,
    parity_metrics,
)
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
_DEFAULT_TOOLCHAINS_CONFIG = Path(__file__).resolve().parents[2] / "configs/toolchains.yaml"


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


@dataclass(frozen=True)
class _CheckerBundleRuntime:
    bundle: ValidatedCheckerBundle
    checker_ids: frozenset[str]
    toolchains_config: Path
    compiler: CompilerName
    online_oracle: CommandOnlineOracle
    verification_command: VerificationCommand

    def provenance(self) -> dict[str, Any]:
        manifest = self.bundle.manifest
        catalog_artifact = manifest.artifacts.catalog
        dispatcher_artifact = manifest.artifacts.dispatcher
        patch_artifact = manifest.artifacts.cumulative_patch
        scoped_invariants_artifact = manifest.artifacts.scoped_invariants
        input_scope_artifact = manifest.artifacts.input_scope
        assert catalog_artifact is not None
        assert dispatcher_artifact is not None
        assert patch_artifact is not None
        assert scoped_invariants_artifact is not None
        assert input_scope_artifact is not None
        assert self.bundle.catalog is not None
        assert self.bundle.dispatcher is not None
        assert self.bundle.cumulative_patch is not None
        assert self.bundle.scoped_invariants is not None
        assert self.bundle.input_scope is not None
        return {
            "enabled": True,
            "bundle_id": manifest.bundle_id,
            "manifest_path": str(self.bundle.manifest_path),
            "manifest_sha256": _sha256_file(self.bundle.manifest_path),
            "status": manifest.status,
            "coverage_complete": manifest.coverage_complete,
            "included_invariant_ids": list(manifest.included_invariant_ids),
            "failed_invariant_ids": list(manifest.failed_invariant_ids),
            "source_invariants_sha256": manifest.source_invariants_sha256,
            "requested_mechanisms": list(manifest.requested_mechanisms),
            "requested_isas": list(manifest.requested_isas),
            "catalog_path": str(self.bundle.catalog),
            "catalog_sha256": catalog_artifact.sha256,
            "dispatcher_path": str(self.bundle.dispatcher),
            "dispatcher_sha256": dispatcher_artifact.sha256,
            "cumulative_patch_path": str(self.bundle.cumulative_patch),
            "cumulative_patch_sha256": patch_artifact.sha256,
            "scoped_invariants_path": str(self.bundle.scoped_invariants),
            "scoped_invariants_sha256": scoped_invariants_artifact.sha256,
            "input_scope_path": str(self.bundle.input_scope),
            "input_scope_sha256": input_scope_artifact.sha256,
            "toolchains_config": str(self.toolchains_config),
            "toolchains_config_sha256": _sha256_file(self.toolchains_config),
            "compiler": self.compiler,
            "checker_ids": sorted(self.checker_ids),
        }


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _required_verified_candidates(parameters: Mapping[str, Any]) -> bool:
    keys = (
        "require_verified_candidates",
        "require_formal_verification",
        "require_verified_results",
    )
    supplied = [(key, parameters[key]) for key in keys if key in parameters]
    for key, value in supplied:
        if not isinstance(value, bool):
            raise ValueError(f"{key} must be a boolean")
    values = {value for _, value in supplied}
    if len(values) > 1:
        raise ValueError("require_verified_candidates and its compatibility aliases disagree")
    return bool(supplied and supplied[0][1])


def _catalog_checker_ids(bundle: ValidatedCheckerBundle) -> frozenset[str]:
    if bundle.catalog is None:
        raise ValueError("ready checker bundle has no catalog artifact")
    try:
        payload = json.loads(bundle.catalog.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"trusted checker catalog cannot be parsed: {exc}") from exc
    if not isinstance(payload, Mapping):
        raise ValueError("trusted checker catalog must be a JSON object")
    if payload.get("schema_version") != 1:
        raise ValueError("trusted checker catalog schema_version must be 1")
    if payload.get("kind") != "defuzz-checker-catalog":
        raise ValueError("trusted checker catalog kind must be 'defuzz-checker-catalog'")
    if payload.get("source_tree_sha256") != bundle.manifest.source_tree_sha256:
        raise ValueError("trusted checker catalog source_tree_sha256 mismatches manifest")
    if payload.get("result_tree_sha256") != bundle.manifest.final_tree_sha256:
        raise ValueError("trusted checker catalog result_tree_sha256 mismatches manifest")
    entries = payload.get("checkers")
    if not isinstance(entries, list):
        raise ValueError("trusted checker catalog checkers must be an array")
    checker_ids: list[str] = []
    invariant_ids: list[str] = []
    for index, entry in enumerate(entries):
        if not isinstance(entry, Mapping):
            raise ValueError(f"trusted checker catalog entry {index} must be an object")
        checker_id = entry.get("checker_id")
        invariant_id = entry.get("invariant_id")
        if not isinstance(checker_id, str) or not checker_id.strip():
            raise ValueError(f"trusted checker catalog entry {index} has no checker_id")
        if not isinstance(invariant_id, str) or not invariant_id.strip():
            raise ValueError(f"trusted checker catalog entry {index} has no invariant_id")
        if checker_id != invariant_id:
            raise ValueError(
                f"trusted checker catalog entry {index} checker_id must equal invariant_id"
            )
        checker_ids.append(checker_id)
        invariant_ids.append(invariant_id)
    if len(set(checker_ids)) != len(checker_ids):
        raise ValueError("trusted checker catalog checker_id values must be unique")
    if set(invariant_ids) != set(bundle.manifest.included_invariant_ids):
        raise ValueError(
            "trusted checker catalog entries do not match manifest included_invariant_ids"
        )
    if not checker_ids:
        raise ValueError("trusted checker catalog contains no executable checkers")
    return frozenset(checker_ids)


def _configured_compiler(parameters: Mapping[str, Any], *, required: bool) -> CompilerName | None:
    raw = parameters.get("compiler")
    if raw is None or (isinstance(raw, str) and not raw.strip()):
        if required:
            raise ValueError("checker bundle runtime requires plan.parameters.compiler")
        return None
    if not isinstance(raw, str):
        raise ValueError("compiler must be a string")
    return normalize_compiler(raw)


def _load_checker_bundle_runtime(
    parameters: Mapping[str, Any],
    *,
    required: bool,
    compiler: CompilerName | None,
) -> _CheckerBundleRuntime | None:
    configured = parameters.get("checker_bundle_manifest")
    if not configured:
        if required:
            raise ValueError("require_verified_candidates requires checker_bundle_manifest")
        return None
    if not isinstance(configured, (str, Path)):
        raise ValueError("checker_bundle_manifest must be a filesystem path")
    if compiler is None:
        raise ValueError("checker bundle runtime requires plan.parameters.compiler")
    bundle = load_checker_bundle(configured, require_ready=True)
    raw_config = parameters.get("toolchains_config")
    if required and not raw_config:
        raise ValueError("require_verified_candidates requires an explicit toolchains_config")
    if not raw_config:
        raw_config = _DEFAULT_TOOLCHAINS_CONFIG
    if not isinstance(raw_config, (str, Path)):
        raise ValueError("toolchains_config must be a filesystem path")
    try:
        toolchains_config = Path(raw_config).expanduser().resolve(strict=True)
    except OSError as exc:
        raise ValueError(f"toolchains_config does not resolve: {raw_config}") from exc
    if not toolchains_config.is_file():
        raise ValueError(f"toolchains_config is not a regular file: {toolchains_config}")
    checker_ids = _catalog_checker_ids(bundle)
    if bundle.dispatcher is None:
        raise ValueError("ready checker bundle has no dispatcher artifact")
    if not os.access(bundle.dispatcher, os.X_OK):
        raise ValueError(f"checker-bundle dispatcher is not executable: {bundle.dispatcher}")
    online_argv = checker_bundle_dispatcher_argv(
        bundle, toolchains_config, mode="online", compiler=compiler
    )
    verify_argv = checker_bundle_dispatcher_argv(
        bundle, toolchains_config, mode="verify", compiler=compiler
    )
    try:
        online_timeout = float(parameters.get("online_oracle_timeout_seconds", 300.0))
        verification_timeout = float(parameters.get("verification_timeout_seconds", 300.0))
    except (TypeError, ValueError) as exc:
        raise ValueError("checker dispatcher timeouts must be numeric") from exc
    if online_timeout <= 0 or verification_timeout <= 0:
        raise ValueError("checker dispatcher timeouts must be positive")
    return _CheckerBundleRuntime(
        bundle=bundle,
        checker_ids=checker_ids,
        toolchains_config=toolchains_config,
        compiler=compiler,
        online_oracle=CommandOnlineOracle(
            online_argv,
            timeout_seconds=online_timeout,
            allowed_checker_ids=checker_ids,
            require_dispatcher_echo=True,
        ),
        verification_command=VerificationCommand(
            verify_argv,
            timeout_seconds=verification_timeout,
            protocol="dispatcher-verify",
        ),
    )


def _materialize_workspace(source_roots: Sequence[Path], *, repetition: int) -> _WorkspaceLease:
    root = Path(tempfile.mkdtemp(prefix=f"defuzz-audit-r{repetition:03d}-")).resolve()
    labels = (
        (".",)
        if len(source_roots) == 1
        else tuple(f"source-{index}" for index in range(1, len(source_roots) + 1))
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

    return _WorkspaceLease(root=root, sha256=aggregate.hexdigest(), labels=labels, cleanup=cleanup)


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
        "bare worker emitted a non-object issue" for item in issues if not isinstance(item, Mapping)
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


def _agent_result_leaks_findings(result: Any) -> bool:
    """Inspect every provider-returned channel before publishing artifacts."""

    values = (
        getattr(result, "final", None),
        getattr(result, "events", None),
        getattr(result, "raw_stdout", ""),
        getattr(result, "raw_stderr", ""),
        getattr(result, "error", ""),
    )
    for value in values:
        if value in (None, "", []):
            continue
        text = (
            value
            if isinstance(value, str)
            else json.dumps(value, ensure_ascii=False, default=str)
        )
        try:
            assert_no_findings_leak(text)
        except ValueError:
            return True
    return False


def _worker_invalid_reasons(
    bundle: WorkerPromptBundle,
    report: AuditReport,
    *,
    expected_compiler: CompilerName | None = None,
    expected_mechanisms: Sequence[str] = (),
    expected_isas: Sequence[str] = (),
) -> list[str]:
    reasons = [f"parse issue: {issue}" for issue in report.parse_issues]
    if report.tainted:
        reasons.append("worker result is tainted by findings corpus content")
    if not report.worker_bundle_sha256:
        reasons.append("worker_bundle_sha256 echo is missing")
    elif report.worker_bundle_sha256 != bundle.sha256:
        reasons.append("worker_bundle_sha256 does not match the delivered bundle")
    if report.family != bundle.family.key:
        reasons.append(f"worker family echo {report.family!r} does not match {bundle.family.key!r}")
    if report.variant != bundle.variant.value:
        reasons.append(
            f"worker variant echo {report.variant!r} does not match {bundle.variant.value!r}"
        )
    plan_mechanisms = {normalize_mechanism(value) for value in expected_mechanisms}
    family_mechanisms = (
        {
            normalize_mechanism(value)
            for family in families_for_mechanisms(None)
            for value in family.mechanisms
        }
        if bundle.variant is AuditVariant.BARE_AGENT
        else {normalize_mechanism(value) for value in bundle.family.mechanisms}
    )
    allowed_mechanisms: set[str] | None
    if plan_mechanisms and family_mechanisms:
        allowed_mechanisms = plan_mechanisms.intersection(family_mechanisms)
    elif plan_mechanisms:
        allowed_mechanisms = plan_mechanisms
    elif family_mechanisms:
        allowed_mechanisms = family_mechanisms
    else:
        allowed_mechanisms = None
    allowed_isas = {normalize_isa(value) for value in expected_isas if value.strip()}

    for index, candidate in enumerate(report.candidates):
        label = candidate.id.strip() or f"at index {index}"
        if not candidate.toolchain.strip():
            reasons.append(f"candidate {label} toolchain is missing")
            continue
        try:
            candidate_compiler = normalize_compiler(candidate.toolchain)
        except (TypeError, ValueError):
            reasons.append(f"candidate {label} has unknown toolchain {candidate.toolchain!r}")
            continue
        if expected_compiler is not None and candidate_compiler != expected_compiler:
            reasons.append(
                f"candidate {label} compiler {candidate_compiler!r} does not match "
                f"plan compiler {expected_compiler!r}"
            )
        mechanism = normalize_mechanism(candidate.mechanism)
        if not mechanism:
            reasons.append(f"candidate {label} mechanism is missing")
        elif allowed_mechanisms is not None and mechanism not in allowed_mechanisms:
            reasons.append(
                f"candidate {label} mechanism {mechanism!r} is outside the worker lane "
                f"scope {sorted(allowed_mechanisms)!r}"
            )

        declared_isas = [str(value) for value in candidate.isa]
        trigger_isas = [str(value) for value in _as_list(candidate.minimal_trigger.isa)]
        if trigger_isas:
            declared_isas.extend(trigger_isas)
        elif not declared_isas and candidate.minimal_trigger.target.strip():
            # Match the trusted Go dispatcher's final compatibility fallback.
            declared_isas.append(candidate.minimal_trigger.target)
        normalized_isas = {normalize_isa(value) for value in declared_isas if value.strip()}
        if not normalized_isas:
            reasons.append(f"candidate {label} ISA is missing")
        elif allowed_isas:
            outside_scope = normalized_isas - allowed_isas
            if outside_scope:
                reasons.append(
                    f"candidate {label} ISA values {sorted(outside_scope)!r} are outside "
                    f"the lane scope {sorted(allowed_isas)!r}"
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


def _load_generated_invariants(
    parameters: Mapping[str, Any],
    checker_runtime: _CheckerBundleRuntime | None = None,
) -> tuple[list[dict[str, Any]], str | None, Path | None, str]:
    configured = parameters.get("accepted_invariants")
    source = "explicit"
    if checker_runtime is not None:
        scoped_path = checker_runtime.bundle.scoped_invariants
        scoped_artifact = checker_runtime.bundle.manifest.artifacts.scoped_invariants
        if scoped_path is None or scoped_artifact is None:
            raise ValueError("ready checker bundle has no scoped_invariants artifact")
        if configured is not None:
            if not isinstance(configured, (str, Path)):
                raise ValueError("accepted_invariants must be a JSONL filesystem path")
            explicit_path = Path(configured).expanduser().resolve(strict=True)
            if not explicit_path.is_file() or explicit_path.is_symlink():
                raise ValueError(
                    f"accepted_invariants is not a regular file: {explicit_path}"
                )
            explicit_hash = _sha256_file(explicit_path)
            if not hmac.compare_digest(explicit_hash, scoped_artifact.sha256):
                raise ValueError(
                    "accepted_invariants SHA-256 does not match checker-bundle "
                    "scoped_invariants: "
                    f"expected {scoped_artifact.sha256}, got {explicit_hash}"
                )
        path = scoped_path
        source = "checker-bundle-scoped"
    elif configured is None:
        return [], None, None, "none"
    else:
        if not isinstance(configured, (str, Path)):
            raise ValueError("accepted_invariants must be a JSONL filesystem path")
        path = Path(configured).expanduser().resolve(strict=True)
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"accepted_invariants is not a regular file: {path}")
    actual_hash = _sha256_file(path)
    expected_hash = parameters.get("accepted_invariants_sha256")
    if expected_hash is not None and expected_hash != actual_hash:
        raise ValueError(
            "accepted_invariants SHA-256 mismatch: "
            f"expected {expected_hash}, got {actual_hash}"
        )
    records: list[dict[str, Any]] = []
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        if not line.strip():
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(
                f"accepted_invariants contains invalid JSON at line {line_number}: {exc.msg}"
            ) from exc
        if not isinstance(value, dict):
            raise ValueError(
                f"accepted_invariants line {line_number} must be a JSON object"
            )
        records.append(value)
    if not records:
        raise ValueError("accepted_invariants contains no records")
    return records, actual_hash, path, source


def _coerce_plan(plan: ExperimentPlan | Mapping[str, Any]) -> ExperimentPlan:
    return plan if isinstance(plan, ExperimentPlan) else ExperimentPlan.from_dict(plan)


def _toolchains(parameters: Mapping[str, Any], *, compiler: CompilerName | None) -> list[str]:
    raw = parameters.get("toolchains")
    if raw is None:
        raw = [compiler] if compiler is not None else ["gcc", "llvm"]
    return [str(item) for item in _as_list(raw)]


def _toolchain_versions(parameters: Mapping[str, Any]) -> dict[str, str]:
    raw = parameters.get("toolchain_versions", {})
    versions = (
        {str(key): str(value) for key, value in raw.items()} if isinstance(raw, Mapping) else {}
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
    expected_compiler: CompilerName | None = None,
    expected_mechanisms: Sequence[str] = (),
    expected_isas: Sequence[str] = (),
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
    with tempfile.TemporaryDirectory(prefix="defuzz-audit-quarantine-") as temporary:
        quarantine_dir = Path(temporary).resolve() / worker_dir.name
        resolved_denials = validate_agent_path_isolation(
            cwd=cwd,
            output_dir=quarantine_dir,
            schema_path=schema_path,
            deny_read_paths=deny_read_paths,
        )
        request = AgentRequest(
            prompt=delivered_prompt,
            cwd=cwd,
            output_dir=quarantine_dir,
            schema_path=schema_path,
            timeout_seconds=timeout_seconds,
            writable=False,
            token_sink=token_sink,
            deny_read_paths=resolved_denials,
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
                message = f"backend raised {type(exc).__name__}"
                report = AuditReport(
                    family=bundle.family.key,
                    variant=bundle.variant.value,
                    parse_issues=[message],
                )
                return bundle, report, message
        leaked_output = _agent_result_leaks_findings(result)
        if leaked_output:
            report = AuditReport(
                family=bundle.family.key,
                variant=bundle.variant.value,
                worker_bundle_sha256=bundle.sha256,
                parse_issues=["worker output rejected by findings leak guard"],
                tainted=True,
            )
            _write_json(worker_dir / "audit-report.json", report)
            admission_payload = {
                "results": [],
                "admitted": [],
                "rejected": [],
                "candidate_admission": {
                    "scope": _CANDIDATE_ADMISSION_SCOPE,
                    "deterministic_poc_validator_executed": False,
                },
                "worker_valid": False,
                "worker_invalid_reasons": [
                    "worker result is tainted by findings corpus content"
                ],
            }
            _write_json(worker_dir / "admission.json", admission_payload)
            return bundle, report, "worker output rejected by findings leak guard"
    if not result.success:
        message = result.error or f"backend exited with status {result.exit_code}"
        report = AuditReport(
            family=bundle.family.key,
            variant=bundle.variant.value,
            parse_issues=[message],
        )
        return bundle, report, message
    report = _parse_worker_report(bundle, result.final)
    if any(candidate_leaks_findings(candidate) for candidate in report.candidates):
        report = AuditReport(
            family=bundle.family.key,
            variant=bundle.variant.value,
            worker_bundle_sha256=bundle.sha256,
            parse_issues=["worker candidate rejected by findings leak guard"],
            tainted=True,
        )
    _write_json(worker_dir / "audit-report.json", report)
    admission = admit_report(report)
    admission_payload = admission.model_dump(mode="json")
    admission_payload["candidate_admission"] = {
        "scope": _CANDIDATE_ADMISSION_SCOPE,
        "deterministic_poc_validator_executed": False,
    }
    invalid_reasons = _worker_invalid_reasons(
        bundle,
        report,
        expected_compiler=expected_compiler,
        expected_mechanisms=expected_mechanisms,
        expected_isas=expected_isas,
    )
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

    started = time.monotonic()
    resolved = _coerce_plan(plan)
    destination = Path(output_dir).expanduser().resolve(strict=False)
    try:
        variant = AuditVariant(resolved.variant)
        policy_for_variant(variant)
    except ValueError as exc:
        return StageResult(stage="agent-audit", status="failed", error=str(exc))

    parameters = resolved.parameters
    try:
        require_verified_candidates = _required_verified_candidates(parameters)
        configured_compiler = _configured_compiler(
            parameters,
            required=bool(parameters.get("checker_bundle_manifest")),
        )
        checker_runtime = _load_checker_bundle_runtime(
            parameters,
            required=require_verified_candidates,
            compiler=configured_compiler,
        )
        (
            generated_invariants,
            generated_invariants_sha256,
            generated_invariants_path,
            generated_invariants_source,
        ) = _load_generated_invariants(parameters, checker_runtime)
    except (OSError, TypeError, ValueError, json.JSONDecodeError) as exc:
        # The trust boundary is validated before any worker prompt or backend
        # invocation. A tampered bundle can therefore never influence a worker.
        return StageResult(
            stage="agent-audit",
            status="failed",
            error=f"invalid checker bundle configuration: {exc}",
            metadata={
                "execution_status": "failed",
                "execution_completed": False,
                "result_valid": False,
                "continuation_ready": False,
                "require_verified_candidates": require_verified_candidates
                if "require_verified_candidates" in locals()
                else False,
            },
            execution_status="failed",
            result_valid=False,
            continuation_ready=False,
            outcome="invalid",
        )
    online_oracles: list[CommandOnlineOracle] = []
    oracle_rounds = 0
    if resolved.policy.use_online_oracle:
        raw_commands = parameters.get("online_oracle_command")
        if checker_runtime is not None:
            # One candidate, one trusted dispatcher. The dispatcher performs all
            # checker selection internally from candidate.checker_ids.
            online_oracles = [checker_runtime.online_oracle]
        elif not raw_commands:
            return StageResult(
                stage="agent-audit",
                status="failed",
                error=(
                    "full agent-audit requires at least one online_oracle_command; "
                    "use the without-oracle ablation to disable online feedback"
                ),
            )
        elif isinstance(raw_commands, (str, bytes)):
            return StageResult(
                stage="agent-audit",
                status="failed",
                error="online_oracle_command must be an argv sequence or list of argv sequences",
            )
        elif raw_commands:
            command_templates = list(raw_commands)
            if command_templates and all(isinstance(item, str) for item in command_templates):
                command_templates = [command_templates]
            try:
                online_oracles = [CommandOnlineOracle(template) for template in command_templates]
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
    reference_root = (
        Path(
            parameters.get(
                "reference_root", parameters.get("reviewer_root", DEFAULT_REFERENCE_ROOT)
            )
        )
        .expanduser()
        .resolve(strict=False)
    )
    mechanisms = [
        normalize_mechanism(str(item))
        for item in _as_list(parameters.get("mechanisms", parameters.get("mechanism")))
    ]
    try:
        scoped_families = families_for_mechanisms(mechanisms)
    except ValueError as exc:
        return StageResult(stage="agent-audit", status="failed", error=str(exc))
    families: tuple[AuditFamily, ...]
    if variant is AuditVariant.BARE_AGENT:
        # The baseline is intentionally one neutral worker.  Mechanism and A--E
        # sharding are treatment information and must not reach this arm.
        families = (families_for_mechanisms(None)[0],)
    else:
        families = scoped_families
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
    try:
        validate_disjoint_input_roots(
            [
                *(
                    (f"source_roots[{index}]", source)
                    for index, source in enumerate(source_roots)
                ),
                ("reference_root", reference_root),
            ]
        )
    except ValueError as exc:
        return StageResult(stage="agent-audit", status="failed", error=str(exc))
    trusted_runtime_paths: tuple[Path, ...] = (
        (
            checker_runtime.bundle.root,
            checker_runtime.toolchains_config,
        )
        if checker_runtime is not None
        else ()
    )
    agent_deny_paths = tuple(
        dict.fromkeys(
            (
                *source_roots,
                reference_root,
                *((generated_invariants_path,) if generated_invariants_path is not None else ()),
                *trusted_runtime_paths,
            )
        )
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
    toolchains = _toolchains(parameters, compiler=configured_compiler)
    oracle_documents = _as_list(
        parameters.get("oracle_documents", parameters.get("checker_artifacts"))
    )
    if checker_runtime is not None and resolved.policy.use_online_oracle:
        # The catalog is trusted only after bundle validation above. Full workers
        # may see checker capabilities; the two ablations remain blind to them.
        oracle_documents = [checker_runtime.bundle.catalog]
    checker_routing_prompt = (
        "\n\n# Checker routing contract\n\n"
        "For every candidate, set checker_ids to one or more checker_id values "
        "from the permitted checker catalog. Select only checkers relevant to the "
        "candidate; these bundle-scoped IDs drive online feedback. Also put each "
        "canonical invariant actually claimed by the candidate in related_invariants. "
        "The final verify pass resolves those IDs only against trusted checkers compiled "
        "into the same manifest-bound dispatcher, then expands dependencies and ISA routes."
        if checker_runtime is not None and resolved.policy.use_online_oracle
        else ""
    )
    isas = [str(item) for item in _as_list(parameters.get("isas", parameters.get("isa")))]
    lane_scope = {
        "compiler": configured_compiler,
        "mechanisms": sorted(set(mechanisms)),
        "isas": sorted({normalize_isa(value) for value in isas if value.strip()}),
        "empty_mechanisms_or_isas_mean_all_configured": True,
    }
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
        validate_agent_path_isolation(
            cwd=lease.root,
            output_dir=destination,
            schema_path=schema_path,
            deny_read_paths=agent_deny_paths,
        )
    except ValueError as exc:
        if not keep_workspace:
            lease.cleanup()
        return StageResult(stage="agent-audit", status="failed", error=str(exc))
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
                    generated_invariants=(
                        () if variant is AuditVariant.BARE_AGENT else generated_invariants
                    ),
                )
                for family in families
            ]
        except (OSError, ValueError) as exc:
            return StageResult(stage="agent-audit", status="failed", error=str(exc))

        active_backend = backend or ExecAgentBackend(
            binary=str(parameters.get("agent_binary", parameters.get("backend", "traex"))),
            model=cast(str | None, parameters.get("model")),
            provider=cast(
                Literal["traex", "codex"],
                parameters.get("backend", "traex"),
            ),
        )
        host_isolation = bool(getattr(active_backend, "supports_host_read_isolation", False))
        require_host_isolation = bool(parameters.get("require_host_read_isolation", True))
        if require_host_isolation and not host_isolation:
            return StageResult(
                stage="agent-audit",
                status="failed",
                error=(
                    "host read isolation is unavailable; use a backend with an enforced "
                    "workspace boundary or run inside an equivalent OS sandbox"
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
                        deny_read_paths=agent_deny_paths,
                        require_host_read_isolation=require_host_isolation,
                        expected_compiler=configured_compiler,
                        expected_mechanisms=mechanisms,
                        expected_isas=isas,
                        prompt_suffix=checker_routing_prompt,
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
                        deny_read_paths=agent_deny_paths,
                        require_host_read_isolation=require_host_isolation,
                        expected_compiler=configured_compiler,
                        expected_mechanisms=mechanisms,
                        expected_isas=isas,
                        prompt_suffix=checker_routing_prompt,
                    )
                )

        reports: list[AuditReport] = []
        worker_validity: list[dict[str, Any]] = []
        admitted: list[AuditCandidate] = []
        rejected: list[AuditCandidate] = []
        errors: list[str] = []
        for bundle, report, error in worker_results:
            reports.append(report)
            invalid_reasons = _worker_invalid_reasons(
                bundle,
                report,
                expected_compiler=configured_compiler,
                expected_mechanisms=mechanisms,
                expected_isas=isas,
            )
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
                    f"worker {bundle.family.key} invalid: {reason}" for reason in invalid_reasons
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
                for bundle, report in zip(bundles, reports, strict=True):
                    if _worker_invalid_reasons(
                        bundle,
                        report,
                        expected_compiler=configured_compiler,
                        expected_mechanisms=mechanisms,
                        expected_isas=isas,
                    ):
                        continue
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
                            feedback_by_family.setdefault(report.family, []).append(result)
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
                        deny_read_paths=agent_deny_paths,
                        require_host_read_isolation=require_host_isolation,
                        expected_compiler=configured_compiler,
                        expected_mechanisms=mechanisms,
                        expected_isas=isas,
                        prompt_suffix=(
                            checker_routing_prompt
                            + "\n\n# Deterministic online checker feedback\n\n"
                            + feedback
                            + "\n\nRevise the report using this feedback. Preserve the "
                            "worker_bundle_sha256 echo."
                        ),
                        invocation_label=f"oracle-{round_index:03d}",
                    )
                    revised[family_key] = report
                    if error:
                        errors.append(f"worker {family_key} oracle round {round_index}: {error}")
                if not revised:
                    break
                reports = [revised.get(report.family, report) for report in reports]

            # Recompute structural results from the reports that actually leave
            # the online checker-feedback loop.
            worker_validity = []
            admitted = []
            rejected = []
            for bundle, report in zip(bundles, reports, strict=True):
                invalid_reasons = _worker_invalid_reasons(
                    bundle,
                    report,
                    expected_compiler=configured_compiler,
                    expected_mechanisms=mechanisms,
                    expected_isas=isas,
                )
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
        if checker_runtime is not None:
            verification_commands = [checker_runtime.verification_command]
            # Online checker_ids are bundle-scoped. The manifest-bound dispatcher
            # is also the trusted final verifier and may select canonical
            # related_invariants compiled into that same executable, so the
            # verifier validates those IDs itself rather than applying the
            # online allowlist a second time.
            allowed_checker_ids: frozenset[str] | None = checker_runtime.checker_ids
        else:
            raw_commands = parameters.get("verification_command", [])
            if isinstance(raw_commands, (str, bytes)):
                return StageResult(
                    stage="agent-audit",
                    status="failed",
                    error="verification_command must be an argv sequence or list of argv sequences",
                )
            command_templates = list(raw_commands)
            if command_templates and all(isinstance(item, str) for item in command_templates):
                command_templates = [command_templates]
            try:
                verification_commands = [
                    VerificationCommand(tuple(str(part) for part in command))
                    for command in command_templates
                ]
            except (TypeError, ValueError) as exc:
                return StageResult(
                    stage="agent-audit",
                    status="failed",
                    error=f"invalid verification_command: {exc}",
                )
            allowed_checker_ids = None
        verification_results = []
        time_to_first_verified_ms: float | None = None
        for candidate in admitted:
            verification = await verify_candidate(
                candidate,
                [lease.root],
                commands=verification_commands,
                allowed_checker_ids=(
                    None
                    if checker_runtime is not None and candidate.related_invariants
                    else allowed_checker_ids
                ),
                require_checker_ids=(
                    checker_runtime is not None
                    and variant is AuditVariant.FULL
                    and not candidate.related_invariants
                ),
            )
            verification_results.append(verification)
            if verification.confirmed and time_to_first_verified_ms is None:
                time_to_first_verified_ms = (time.monotonic() - started) * 1000.0
        verified_candidates = [
            candidate
            for candidate, verification in zip(admitted, verification_results, strict=True)
            if verification.confirmed
        ]
        candidate_unverified = sum(
            verification.status == "unverified" for verification in verification_results
        )
        candidate_invalid = sum(
            verification.status == "invalid" for verification in verification_results
        )
        candidate_rejected = sum(
            verification.status == "rejected" for verification in verification_results
        )
        candidate_outcomes_terminal = candidate_unverified == 0
        candidate_outcomes_valid = candidate_invalid == 0 and candidate_unverified == 0
        execution_completed = True
        deterministic_validator_executed = any(
            verification.execution_records for verification in verification_results
        )
        result_valid = (
            invalid_workers == 0
            and not online_oracle_errors
            and (candidate_outcomes_valid or not require_verified_candidates)
        )
        formal_verification_error = None
        if require_verified_candidates and not candidate_outcomes_valid:
            formal_verification_error = (
                "formal candidate verification requires every admitted candidate "
                "to end verified or rejected; found "
                f"{candidate_invalid} invalid and {candidate_unverified} unverified"
            )
        if formal_verification_error is not None:
            errors.append(formal_verification_error)
        final_outcome = "verified-findings" if verified_candidates else "no-verified-findings"

        summary = {
            "schema_version": 1,
            "run_id": resolved.run_id,
            "repetition": repetition,
            "variant": variant.value,
            "campaign_variant": str(parameters.get("campaign_variant", variant.value)),
            "execution_status": "completed",
            "execution_completed": execution_completed,
            "result_valid": result_valid,
            "continuation_ready": result_valid,
            "outcome": final_outcome,
            "time_to_first_verified_ms": time_to_first_verified_ms,
            "family_order": [bundle.family.key for bundle in bundles],
            "candidate_lane_scope": lane_scope,
            "generated_invariants": {
                "visible_to_worker": variant is not AuditVariant.BARE_AGENT,
                "records": (
                    len(generated_invariants) if variant is not AuditVariant.BARE_AGENT else 0
                ),
                "sha256": generated_invariants_sha256,
                "path": str(generated_invariants_path)
                if generated_invariants_path is not None
                else None,
                "source": generated_invariants_source,
                "bundle_id": (
                    checker_runtime.bundle.manifest.bundle_id
                    if checker_runtime is not None
                    else None
                ),
            },
            "reports": [report.model_dump(mode="json") for report in reports],
            "worker_validity": worker_validity,
            "admitted_candidates": [candidate.model_dump(mode="json") for candidate in admitted],
            "rejected_candidates": [candidate.model_dump(mode="json") for candidate in rejected],
            "candidate_verification": [
                verification.model_dump(mode="json") for verification in verification_results
            ],
            "candidate_terminal_outcomes": {
                "verified": len(verified_candidates),
                "invalid": candidate_invalid,
                "rejected": candidate_rejected,
                "unverified": candidate_unverified,
                "all_admitted_terminal": candidate_outcomes_terminal,
                "all_admitted_valid": candidate_outcomes_valid,
            },
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
            "checker_bundle": (
                checker_runtime.provenance() if checker_runtime is not None else {"enabled": False}
            ),
            "require_verified_candidates": require_verified_candidates,
            "candidate_admission": {
                "scope": _CANDIDATE_ADMISSION_SCOPE,
                "deterministic_poc_validator_executed": deterministic_validator_executed,
            },
        }
        parity_report = None
        parity_path: Path | None = None
        if parameters.get("demo_parity") or parameters.get("parity_threshold") is not None:
            threshold_value = parameters.get("parity_threshold")
            threshold = float(threshold_value) if threshold_value is not None else None
            raw_parity_scope = parameters.get("parity_scope")
            try:
                parity_scope = (
                    ParityScope.model_validate(raw_parity_scope)
                    if raw_parity_scope is not None
                    else None
                )
            except (TypeError, ValueError) as exc:
                return StageResult(
                    stage="agent-audit",
                    status="failed",
                    error=f"invalid parity_scope: {exc}",
                )
            # Keep the evaluator boundary strict: admitted-but-unverified worker
            # claims never contribute to parity or superset-coverage metrics.
            parity_report = evaluate_demo_parity(
                verified_candidates,
                reference_root / "findings",
                threshold=threshold,
                threshold_metric=cast(
                    ThresholdMetric,
                    parameters.get("parity_threshold_metric", "recall"),
                ),
                profile=cast(
                    ParityProfile,
                    parameters.get("parity_profile", "demo-workset"),
                ),
                scope=parity_scope,
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
                ArtifactRef.from_path(parity_path, base_dir=destination, kind="demo-parity")
            )
        metadata = {
            "variant": variant.value,
            "campaign_variant": str(parameters.get("campaign_variant", variant.value)),
            "family_order": [bundle.family.key for bundle in bundles],
            "candidate_lane_scope": lane_scope,
            "generated_invariants": {
                "visible_to_worker": variant is not AuditVariant.BARE_AGENT,
                "records": (
                    len(generated_invariants) if variant is not AuditVariant.BARE_AGENT else 0
                ),
                "sha256": generated_invariants_sha256,
                "path": str(generated_invariants_path)
                if generated_invariants_path is not None
                else None,
                "source": generated_invariants_source,
                "bundle_id": (
                    checker_runtime.bundle.manifest.bundle_id
                    if checker_runtime is not None
                    else None
                ),
            },
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
            "execution_status": "completed",
            "execution_completed": execution_completed,
            "result_valid": result_valid,
            "continuation_ready": result_valid,
            "outcome": final_outcome,
            "checker_bundle": (
                checker_runtime.provenance() if checker_runtime is not None else {"enabled": False}
            ),
            "require_verified_candidates": require_verified_candidates,
            "candidate_admission": {
                "scope": _CANDIDATE_ADMISSION_SCOPE,
                "deterministic_poc_validator_executed": deterministic_validator_executed,
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
            "time_to_first_verified_ms": time_to_first_verified_ms,
            "execution_completed": execution_completed,
            "result_valid": result_valid,
            "continuation_ready": result_valid,
            "online_oracle_calls": len(online_oracle_records),
            "online_oracle_errors": len(online_oracle_errors),
            "candidate_unverified": candidate_unverified,
            "candidate_invalid": candidate_invalid,
            "candidate_invalid_evidence": candidate_invalid,
            "candidate_rejected_by_verification": candidate_rejected,
        }
        if parity_report is not None:
            parity_summary = parity_metrics(parity_report)
            metrics.update({f"demo_parity_{key}": value for key, value in parity_summary.items()})
        return StageResult(
            stage="agent-audit",
            status=(
                "failed"
                if online_oracle_errors or formal_verification_error is not None
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
            execution_status="completed",
            result_valid=result_valid,
            continuation_ready=result_valid,
            outcome=final_outcome,
        )
    finally:
        if not keep_workspace:
            lease.cleanup()


run_agent_audit = run

__all__ = ["DEFAULT_REFERENCE_ROOT", "run", "run_agent_audit"]
