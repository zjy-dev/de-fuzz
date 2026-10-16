"""The five formal stages of the DeFuzz compiler-defense verification pipeline.

Stage 1: Invariant Generation (s1_invariants)
Stage 2: Deterministic Checker Authoring (s2_checkers)
Stage 3: CallGraph Source Mapping (s3_source_mapping)
Stage 4: Gated Log Instrumentation & Compiler Rebuild (s4_log_instrumentation)
Stage 5: Autonomous Invariant Agent Audit (s5_agent_audit)
"""

from defuzz_loop.stages.s1_invariants import stage as invariant_generation
from defuzz_loop.stages.s2_checkers import stage as checker_authoring
from defuzz_loop.stages.s3_source_mapping import stage as source_mapping
from defuzz_loop.stages.s4_log_instrumentation import stage as log_instrumentation
from defuzz_loop.stages.s5_agent_audit import stage as agent_audit

__all__ = [
    "invariant_generation",
    "checker_authoring",
    "source_mapping",
    "log_instrumentation",
    "agent_audit",
]
