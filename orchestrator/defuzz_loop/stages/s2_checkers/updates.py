"""Bounded online checker evolution; offline evaluation keeps its frozen bundle."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
from typing import Any

from pydantic import BaseModel, ConfigDict, Field

from defuzz_loop.agents.runner import SubagentRunner
from defuzz_loop.core.checker_bundle import load_checker_bundle
from defuzz_loop.backends.base import AgentBackend, AgentRequest
from defuzz_loop.stages.s2_checkers.stage import load_accepted_invariants
from defuzz_loop.stages.s2_checkers.stage import run as author_checkers
from defuzz_loop.core.models import ExperimentPlan


class CheckerUpdateDecision(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    needed: bool
    invariant_ids: list[str] = Field(default_factory=list)
    reason: str = Field(min_length=1)


async def update_checkers(
    *, plan: ExperimentPlan, repetition: int, parent_manifest: Path,
    feedback: list[dict[str, Any]], reports: list[dict[str, Any]], workspace: Path,
    deny_read_paths: list[Path], output_dir: Path, backend: AgentBackend,
) -> tuple[Path | None, dict[str, Any]]:
    """Return a freshly compiled bundle or an explicit no-change decision.

    Every failure propagates to the audit runner. Nothing mutates the parent.
    All provider calls share the audit stage's ambient token budget.
    """
    output_dir.mkdir(parents=True, exist_ok=True)
    parent = load_checker_bundle(parent_manifest)
    parent_hash = hashlib.sha256(parent_manifest.read_bytes()).hexdigest()
    assert parent.scoped_invariants is not None
    assert parent.catalog is not None
    records = load_accepted_invariants(parent.scoped_invariants)
    source_root = Path(plan.parameters.get("checker_source_root") or parent.manifest.source_root)
    if not source_root.is_dir():
        raise ValueError("checker update requires the original checker source root")
    schema = output_dir / "decision-schema.json"
    schema.write_text(json.dumps(CheckerUpdateDecision.model_json_schema()))
    prompt = (
        "Assess whether the online evidence identifies a checker implementation defect "
        "or missing observation. Request a bounded update only for such a defect. Compiler "
        "bugs and malformed candidates alone do not justify changing the checker. Preserve "
        "the invariant semantics and checker IDs; never weaken a check to force a finding. "
        "Return JSON needed, invariant_ids (generated IDs), reason. If no update is needed, "
        "return needed=false and invariant_ids=[].\n"
        + json.dumps({
            "invariants": [r.value for r in records],
            "catalog": json.loads(parent.catalog.read_text()),
            "online_feedback": feedback, "reports": reports,
        }, sort_keys=True)
    )
    request = AgentRequest(
        prompt=prompt, cwd=workspace, output_dir=output_dir / "planner",
        schema_path=schema, timeout_seconds=plan.budget.timeout_seconds,
        deny_read_paths=list(dict.fromkeys([*deny_read_paths, source_root, parent.root])),
        require_host_read_isolation=bool(plan.parameters.get("require_host_read_isolation", True)),
        metadata={"agent_id": "checker-update-planner", "stage": "checker-update-plan"},
    )

    def validate_decision(decision: CheckerUpdateDecision) -> CheckerUpdateDecision:
        if len(decision.invariant_ids) != len(set(decision.invariant_ids)):
            raise ValueError("checker update decision contains duplicate IDs")
        if not set(decision.invariant_ids) <= {r.invariant_id for r in records}:
            raise ValueError("checker update decision references unknown invariant IDs")
        if decision.needed != bool(decision.invariant_ids):
            raise ValueError("checker update decision needed/IDs disagree")
        return decision

    if getattr(backend, "provider", None) == "pi":
        runner = SubagentRunner(
            backend, cwd=request.cwd, output_dir=request.output_dir,
            timeout_secs=request.timeout_seconds or 600,
            deny_read_paths=request.deny_read_paths,
            require_host_read_isolation=request.require_host_read_isolation,
            metadata=request.metadata,
        )
        decision = await runner.run(
            "checker-update-planner", prompt, CheckerUpdateDecision, validator=validate_decision,
        )
    else:
        result = await backend.run(request)
        if not result.success:
            raise ValueError(f"checker update planner failed: {result.error}")
        decision = validate_decision(CheckerUpdateDecision.model_validate(result.final))
    record: dict[str, Any] = {
        "parent_bundle_id": parent.manifest.bundle_id,
        "parent_manifest_sha256": parent_hash,
        "decision": decision.model_dump(), "status": "not-needed",
        "offline_bundle_unchanged": True,
    }
    (output_dir / "decision.json").write_text(json.dumps(record, indent=2) + "\n")
    if not decision.needed:
        return None, record
    author_plan = plan.model_copy(update={
        "experiment": "checker-authoring", "variant": "full", "source_root": source_root,
        "parameters": {
            "accepted_invariants": str(parent.scoped_invariants),
            "parent_checker_bundle": str(parent_manifest),
            "checker_update_ids": decision.invariant_ids,
            "checker_update_context": {"decision": decision.model_dump(), "feedback": feedback},
            "mechanisms": parent.manifest.requested_mechanisms,
            "isas": parent.manifest.requested_isas,
            "max_attempts": int(plan.parameters.get("checker_update_attempts", 3)),
            "require_host_read_isolation": bool(
                plan.parameters.get("require_host_read_isolation", True)
            ),
            "deny_read_paths": list(dict.fromkeys([*deny_read_paths, parent.root])),
        },
    })
    authored = await author_checkers(author_plan, repetition, output_dir / "bundle", backend)
    (output_dir / "authoring-result.json").write_text(authored.model_dump_json(indent=2) + "\n")
    if authored.status != "completed" or not authored.metrics.get("coverage_complete"):
        raise ValueError("checker update failed authoring, validation or coverage checks")
    new_path = output_dir / "bundle/checker-bundle-manifest.json"
    updated = load_checker_bundle(new_path)
    if updated.manifest.final_tree_sha256 == parent.manifest.final_tree_sha256:
        raise ValueError("checker update requested but produced no implementation change")
    if hashlib.sha256(parent_manifest.read_bytes()).hexdigest() != parent_hash:
        raise ValueError("checker update mutated the frozen parent manifest")
    load_checker_bundle(parent_manifest)
    if set(updated.manifest.included_invariant_ids) != set(parent.manifest.included_invariant_ids):
        raise ValueError("checker update changed the executable checker IDs")
    record.update({
        "status": "adopted", "bundle_id": updated.manifest.bundle_id,
        "manifest": str(new_path),
        "manifest_sha256": hashlib.sha256(new_path.read_bytes()).hexdigest(),
        "final_tree_sha256": updated.manifest.final_tree_sha256,
    })
    (output_dir / "update.json").write_text(json.dumps(record, indent=2) + "\n")
    return new_path, record
