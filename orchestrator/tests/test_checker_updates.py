"""Compile and exercise updated Go checkers without contacting a model provider."""

from __future__ import annotations

import json
import shutil
import subprocess

import pytest
from test_audit_prompt_bundle import _CandidateBackend, _reference
from test_mapping_handoff import Mapper

from defuzz_loop.agent_audit import run as audit
from defuzz_loop.checker_bundle import load_checker_bundle
from defuzz_loop.experiment_engine import AgentResult, ExperimentPlan
from defuzz_loop.experiment_engine.checker_authoring import run as author
from defuzz_loop.experiment_engine.checker_updates import update_checkers
from defuzz_loop.experiment_engine.source_mapping_stage import run as map_sources

pytestmark = pytest.mark.skipif(not shutil.which("go"), reason="requires Go compiler")


class CheckerBackend:
    provider = "fixture"
    supports_host_read_isolation = True

    def __init__(self, *, needed=True, invalid=False):
        self.needed = needed
        self.invalid = invalid
        self.requests = []
        self.candidates = _CandidateBackend(checker_ids=("INV-TEST-001",))

    async def run(self, request):
        self.requests.append(request)
        if request.metadata.get("agent_id") == "checker-update-planner":
            return AgentResult(success=True, final={
                "needed": self.needed,
                "invariant_ids": ["INV-TEST-001"] if self.needed else [],
                "reason": "Fixture identifies an observation gap" if self.needed else "No gap",
            })
        if not request.writable:
            return await self.candidates.run(request)
        number = request.metadata["invariant_id"][-3:]
        updating = "Validated parent checker update" in request.prompt
        value = 10 if updating else int(number)
        text = (
            f"package oracle\n\nfunc init() {{ Values[\"INV-TEST-{number}\"] = {value} }}\n"
        )
        if updating and self.invalid:
            text = "package oracle\ninvalid Go syntax\n"
        (request.cwd / f"core/internal/oracle/checker_{number}.go").write_text(text)
        return AgentResult(success=True, final={"summary": "changed checker"},
                           usage={"input_tokens": 10, "output_tokens": 5, "total_tokens": 15})


@pytest.fixture
async def compiled_bundle(tmp_path):
    # An enclosing checkout catches git-apply accidentally resolving its parent repo.
    subprocess.run(["git", "init", "--quiet", str(tmp_path)], check=True)
    source = tmp_path / "checker-source"
    oracle = source / "core/internal/oracle"
    oracle.mkdir(parents=True)
    (source / "core/go.mod").write_text("module fixture.local/core\n\ngo 1.23\n")
    (oracle / "base.go").write_text("package oracle\n\nvar Values = map[string]int{}\n")
    command = source / "core/cmd/defuzz-candidate-dispatcher"
    command.mkdir(parents=True)
    (command / "main.go").write_text('''package main
import (
 "encoding/json"
 "flag"
 "fmt"
 "os"
 "fixture.local/core/internal/oracle"
)
func main() {
 mode := flag.String("mode", "", "")
 fingerprint := flag.String("candidate-fingerprint", "", "")
 for _, name := range []string{"compiler", "bundle-manifest", "catalog",
                              "toolchains", "candidate-json"} { flag.String(name, "", "") }
 flag.Parse()
 if *mode == "catalog" {
  checkers := []map[string]interface{}{}
  for id := range oracle.Values {
   checkers = append(checkers, map[string]interface{}{
    "id":id, "oracle":"canary", "mechanism":"canary", "requires":[]string{},
    "applicable_isas":[]string{"x86_64"}, "mode":"single", "cost":"cheap",
    "category":"static",
   })
  }
  json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
   "schema_version":1, "kind":"defuzz-checker-catalog", "checkers":checkers,
  })
  return
 }
 value := oracle.Values["INV-TEST-001"] + oracle.Values["INV-TEST-002"]
 if *mode == "value" { fmt.Println(value); return }
 verdict := "FAIL"
 if value > 3 { verdict = "PASS" }
 json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
  "candidate_fingerprint":*fingerprint, "echoed_candidate_fingerprint":*fingerprint,
  "verdict":verdict, "feedback":fmt.Sprintf("checker value=%d",value),
  "evidence":[]string{"compiled fixture checker"},
 })
}
''')
    invariants = tmp_path / "accepted.jsonl"
    invariants.write_text("".join(json.dumps({
        "invariant_id": f"INV-TEST-{i:03d}", "statement": f"An observation guard {i}.",
        "mechanism": "canary", "compiler": "gcc", "target": "x86_64",
    }) + "\n" for i in (1, 2)))
    plan = ExperimentPlan(
        run_id="parent", experiment="checker-authoring", source_root=source,
        parameters={"accepted_invariants": str(invariants), "require_host_read_isolation": False},
    )
    # Each attempt and the final bundle expose only registered generated checkers.
    result = await author(plan, 1, tmp_path / "parent", CheckerBackend())
    assert result.success, result.model_dump()
    parent = load_checker_bundle(tmp_path / "parent/checker-bundle-manifest.json")
    return plan, parent


def executable_value(bundle):
    result = subprocess.run(
        [str(bundle.dispatcher), "--mode", "value"], capture_output=True, text=True, check=True,
    )
    return int(result.stdout)


@pytest.mark.parametrize(("needed", "invalid"), [(False, False), (True, False), (True, True)])
async def test_checker_update_validates_and_preserves_parent(
    compiled_bundle, tmp_path, needed, invalid,
):
    plan, parent = compiled_bundle
    frozen = parent.manifest_path.read_bytes()
    workspace = tmp_path / "compiler"
    workspace.mkdir()
    backend = CheckerBackend(needed=needed, invalid=invalid)
    kwargs = dict(
        plan=plan, repetition=1, parent_manifest=parent.manifest_path,
        feedback=[{"verdict": "FAIL", "feedback": "observation gap"}], reports=[],
        workspace=workspace, deny_read_paths=[], output_dir=tmp_path / "update", backend=backend,
    )
    if invalid:
        with pytest.raises(ValueError, match="failed authoring"):
            await update_checkers(**kwargs)
    else:
        path, record = await update_checkers(**kwargs)
        assert record["status"] == ("adopted" if needed else "not-needed")
        if needed:
            updated = load_checker_bundle(path)
            assert executable_value(updated) == 12
            assert updated.manifest.included_invariant_ids == parent.manifest.included_invariant_ids
            assert len([r for r in backend.requests if r.writable]) == 1
        else:
            assert path is None
            assert not any(r.writable for r in backend.requests)
    assert parent.manifest_path.read_bytes() == frozen
    assert executable_value(load_checker_bundle(parent.manifest_path)) == 3


@pytest.mark.parametrize("invalid", [False, True])
async def test_audit_uses_updated_online_checker_and_original_offline_checker(
    compiled_bundle, tmp_path, invalid,
):
    author_plan, parent = compiled_bundle
    reference = _reference(tmp_path)
    source = tmp_path / "compiler"
    source.mkdir()
    (source / "compiler.c").write_text(
        "void guard() {\n int a = 1;\n int b = 2;\n (void)(a + b);\n}\n"
    )
    mapping_plan = ExperimentPlan(
        run_id="mapping", experiment="source-mapping", source_root=source,
        parameters={"accepted_invariants": str(parent.scoped_invariants),
                    "require_host_read_isolation": False},
    )
    mapped = await map_sources(mapping_plan, 1, tmp_path / "mapping", Mapper())
    assert mapped.success
    toolchains = tmp_path / "toolchains.yaml"
    toolchains.write_text("toolchains: {}\n")
    backend = CheckerBackend(invalid=invalid)
    plan = ExperimentPlan(
        run_id="audit", experiment="agent-audit", source_root=source,
        parameters={
            "compiler": "gcc", "mechanisms": ["canary"], "isas": ["x86_64"],
            "reference_root": str(reference), "toolchains_config": str(toolchains),
            "checker_bundle_manifest": str(parent.manifest_path),
            "checker_source_root": str(author_plan.source_root), "checker_update_rounds": 1,
            "require_verified_candidates": True, "require_host_read_isolation": False,
            "source_mappings": str(tmp_path / "mapping/source-mappings.jsonl"),
        },
    )
    result = await audit(plan, 1, tmp_path / "audit", backend)
    summary = json.loads((tmp_path / "audit/agent-audit-summary.json").read_text())
    assert result.success is (not invalid), result.errors
    assert summary["checker_bundle"]["bundle_id"] == parent.manifest.bundle_id
    if invalid:
        assert result.metrics["checker_update_errors"] == 1
        assert not result.result_valid
    else:
        assert result.metrics["checker_updates_adopted"] == 1
        assert result.metrics["candidate_verified"] == 1
        assert [r["feedback"] for r in summary["online_oracle"]["records"]] == [
            "checker value=3", "checker value=12",
        ]
        assert "checker value=12" in backend.candidates.requests[-1].prompt
