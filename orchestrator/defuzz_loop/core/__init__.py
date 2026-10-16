"""Shared models, token accounting, workspace isolation, and evaluation gates."""

from defuzz_loop.core.models import (
    ArtifactRef,
    BudgetEnvelope,
    ExperimentPlan,
    StageResult,
    VariantName,
)
from defuzz_loop.core.token_accounting import (
    TokenUsageContext,
    TokenUsageRecord,
    TokenUsageSink,
    use_token_usage,
)
from defuzz_loop.core.store import RunStore
from defuzz_loop.core.workspace import (
    WorkspaceBuilder,
    WorkspaceManifest,
    WorkspaceSecurityError,
    validate_agent_path_isolation,
    validate_disjoint_input_roots,
)
from defuzz_loop.core.admission import (
    admit_report,
    candidate_leaks_findings,
    evaluate_candidate,
)
from defuzz_loop.core.parity import (
    ParityScope,
    evaluate_demo_parity,
    parse_demo_findings,
    parity_metrics,
)
from defuzz_loop.core.checker_bundle import (
    ValidatedCheckerBundle,
    load_checker_bundle,
    validate_checker_bundle,
)
from defuzz_loop.core.prompt_bundle import (
    WorkerPromptBundle,
    assert_worker_safe_path,
    build_per_invariant_prompt_bundle,
    build_worker_prompt_bundle,
    build_worker_prompt_bundles,
)
from defuzz_loop.core.audit_schema import (
    AuditCandidate,
    AuditReport,
    normalize_isa,
    normalize_mechanism,
)
from defuzz_loop.core.candidate_verification import (
    VerificationCommand,
    verify_candidate,
)

__all__ = [
    "ArtifactRef",
    "BudgetEnvelope",
    "ExperimentPlan",
    "StageResult",
    "VariantName",
    "TokenUsageContext",
    "TokenUsageRecord",
    "TokenUsageSink",
    "use_token_usage",
    "RunStore",
    "WorkspaceBuilder",
    "WorkspaceManifest",
    "WorkspaceSecurityError",
    "validate_agent_path_isolation",
    "validate_disjoint_input_roots",
    "admit_report",
    "candidate_leaks_findings",
    "evaluate_candidate",
    "ParityScope",
    "evaluate_demo_parity",
    "parse_demo_findings",
    "parity_metrics",
    "ValidatedCheckerBundle",
    "load_checker_bundle",
    "validate_checker_bundle",
    "WorkerPromptBundle",
    "assert_worker_safe_path",
    "build_per_invariant_prompt_bundle",
    "build_worker_prompt_bundle",
    "build_worker_prompt_bundles",
    "AuditCandidate",
    "AuditReport",
    "normalize_isa",
    "normalize_mechanism",
    "VerificationCommand",
    "verify_candidate",
]
