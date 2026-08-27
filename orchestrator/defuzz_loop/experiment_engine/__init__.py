"""Shared runtime contracts for reproducible DeFuzz experiments."""

from .agent_backend import AgentBackend, AgentRequest, AgentResult, ExecAgentBackend
from .http_agent_backend import (
    HTTPAgentConfig,
    HTTPResponsesAgentBackend,
    load_http_agent_config_snapshot,
)
from .models import ArtifactRef, BudgetEnvelope, ExperimentPlan, StageResult, VariantPolicy
from .pipeline import (
    PipelineAuditConfig,
    PipelineBackendConfig,
    PipelineBudgets,
    PipelineCheckerConfig,
    PipelineConfig,
    PipelineGenerationConfig,
    PipelineLaneResult,
    PipelineRunners,
    PipelineRunResult,
    PipelineStageRecord,
    PipelineTarget,
    build_pipeline_plan,
    load_pipeline_config,
    run_pipeline,
)
from .store import PlanMismatchError, RunStore, RunTokenSink, TokenUsageSink
from .workspace import WorkspaceBuilder, WorkspaceManifest, WorkspaceSecurityError

__all__ = [
    "ArtifactRef",
    "AgentBackend",
    "AgentRequest",
    "AgentResult",
    "BudgetEnvelope",
    "ExecAgentBackend",
    "HTTPAgentConfig",
    "HTTPResponsesAgentBackend",
    "load_http_agent_config_snapshot",
    "ExperimentPlan",
    "PlanMismatchError",
    "PipelineAuditConfig",
    "PipelineBackendConfig",
    "PipelineBudgets",
    "PipelineCheckerConfig",
    "PipelineConfig",
    "PipelineGenerationConfig",
    "PipelineLaneResult",
    "PipelineRunResult",
    "PipelineRunners",
    "PipelineStageRecord",
    "PipelineTarget",
    "RunStore",
    "RunTokenSink",
    "StageResult",
    "TokenUsageSink",
    "VariantPolicy",
    "WorkspaceBuilder",
    "WorkspaceManifest",
    "WorkspaceSecurityError",
    "build_pipeline_plan",
    "load_pipeline_config",
    "run_pipeline",
]
