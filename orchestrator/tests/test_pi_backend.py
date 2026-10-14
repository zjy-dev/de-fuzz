"""Real Pi SDK against a local HTTP fixture; no agent CLI or paid model."""

from __future__ import annotations

import asyncio
import json
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from defuzz_loop.experiment_engine.agent_backend import AgentRequest
from defuzz_loop.experiment_engine.pi_backend import PiAgentBackend, verified_artifact
from defuzz_loop.experiment_engine.pi_config import load_pi_config, resolve_agent
from defuzz_loop.token_usage import TokenUsageContext, TokenUsageSink

ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture
def endpoint():
    state = {"requests": [], "reply": '{"ok":true}', "usage": True, "tool": None, "delay": 0}

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            state["requests"].append(body)
            time.sleep(state["delay"])
            if state.get("status", 200) != 200:
                self.send_response(state["status"])
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b'{"error":{"message":"fixture rate limit","type":"rate_limit"}}')
                return
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            index = len(state["requests"])
            tool = state["tool"] if index == 1 else None
            reply = state["replies"][index - 1] if state.get("replies") else state["reply"]
            if callable(reply):
                reply = json.dumps(reply(body))
            delta = {"role": "assistant", "content": reply}
            if tool:
                delta = {
                    "role": "assistant",
                    "tool_calls": [
                        {
                            "index": 0,
                            "id": "call_probe",
                            "type": "function",
                            "function": {"name": tool[0], "arguments": json.dumps(tool[1])},
                        }
                    ],
                }
            chunks = [
                {
                    "id": f"chatcmpl-{index}",
                    "object": "chat.completion.chunk",
                    "model": "fixture",
                    "choices": [{"index": 0, "delta": delta, "finish_reason": None}],
                },
                {
                    "id": f"chatcmpl-{index}",
                    "object": "chat.completion.chunk",
                    "model": "fixture",
                    "choices": [
                        {"index": 0, "delta": {}, "finish_reason": "tool_calls" if tool else "stop"}
                    ],
                },
            ]
            if state["usage"]:
                chunks.append(
                    {
                        "id": f"chatcmpl-{index}",
                        "choices": [],
                        "usage": {
                            "prompt_tokens": state.get("tokens", 60) - 20,
                            "completion_tokens": 20,
                            "total_tokens": state.get("tokens", 60),
                            "completion_tokens_details": {"reasoning_tokens": 5},
                            "prompt_tokens_details": {"cached_tokens": 10},
                        },
                    }
                )
            try:
                for chunk in chunks:
                    self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
                    self.wfile.flush()
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield state, server.server_port
    server.shutdown()
    server.server_close()
    thread.join()


@pytest.fixture
def runtime(tmp_path, endpoint, monkeypatch):
    state, port = endpoint
    monkeypatch.setenv("DEFUZZ_FIXTURE_KEY", "fixture-key")
    models = tmp_path / "models.json"
    models.write_text(
        json.dumps(
            {
                "providers": {
                    "fixture": {
                        "baseUrl": f"http://127.0.0.1:{port}/v1",
                        "api": "openai-completions",
                        "apiKey": "${DEFUZZ_FIXTURE_KEY}",
                        "models": [
                            {
                                "id": "fixture",
                                "name": "Fixture",
                                "reasoning": False,
                                "input": ["text"],
                                "contextWindow": 2000000,
                                "maxTokens": 10000,
                            }
                        ],
                    }
                }
            }
        )
    )
    config = tmp_path / "runtime.json"
    config.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "model_provider": "fixture",
                "model_id": "fixture",
                "models_path": str(models),
                "definitions_root": str(ROOT / "agents/definitions"),
                "worker_path": str(ROOT / "agent-runtime/dist/worker.js"),
                "thinking_level": "off",
                "credential_env": ["DEFUZZ_FIXTURE_KEY"],
                "max_model_calls": 3,
                "max_output_tokens": 1024,
                "request_timeout_ms": 10000,
                "mcp_startup_timeout_ms": 300,
                "cleanup_timeout_ms": 1000,
            }
        )
    )
    cwd = tmp_path / "source"
    cwd.mkdir()
    schema = tmp_path / "schema.json"
    schema.write_text(
        json.dumps({"type": "object", "required": ["ok"], "properties": {"ok": {"const": True}}})
    )
    sink = TokenUsageSink(
        tmp_path / "tokens.jsonl",
        context=TokenUsageContext(
            run_id="fixture",
            experiment="sdk",
            variant="full",
            part="test",
            stage="probe",
        ),
    )
    request = AgentRequest(
        prompt='Return {"ok":true}.',
        cwd=cwd,
        output_dir=tmp_path / "attempt",
        schema_path=schema,
        timeout_seconds=30,
        token_sink=sink,
        metadata={"agent_id": "invariant-reviewer"},
    )
    return config, request, state, sink


@pytest.mark.asyncio
async def test_real_sdk_text_usage_and_permanent_evidence(runtime):
    config, request, state, sink = runtime
    result = await PiAgentBackend(config).run(request)
    assert result.success, (result.error, result.raw_stderr)
    assert result.final == {"ok": True}
    assert len(state["requests"]) == len(sink.records) == 1
    assert sink.records[0].total_tokens == 60  # reasoning is already in output
    assert sink.records[0].reasoning_tokens == 5
    assert sink.records[0].cached_input_tokens == 10
    assert sink.records[0].call_id.endswith(":model:1")
    manifest = json.loads((request.output_dir / "artifacts/manifest.json").read_text())
    for item in manifest["files"]:
        verified_artifact(request.output_dir, item)
    for name in (
        "session.jsonl",
        "events.jsonl",
        "provider-events.jsonl",
        "resolved-agent.json",
        "resolved-config.json",
        "system-prompt.txt",
        "tools.json",
        "admission.json",
    ):
        assert (request.output_dir / name).is_file()


@pytest.mark.asyncio
async def test_large_unicode_output_is_not_pipe_truncated(runtime):
    config, request, state, _ = runtime
    state["reply"] = json.dumps({"ok": True, "large": "界" * 400000}, ensure_ascii=False)
    result = await PiAgentBackend(config).run(request)
    assert result.success, (result.error, result.raw_stderr)
    assert len(result.final["large"]) == 400000
    assert (request.output_dir / "provider-events.jsonl").stat().st_size > 1024 * 1024
    assert list((request.output_dir / "artifacts").glob("*.json"))


@pytest.mark.asyncio
async def test_missing_usage_stops_internal_followup(runtime):
    config, request, state, sink = runtime
    (request.cwd / "input.txt").write_text("fixture")
    state["tool"] = ("read", {"path": "input.txt"})
    state["usage"] = False
    result = await PiAgentBackend(config).run(request)
    assert not result.success
    assert not result.evidence_ok
    assert len(state["requests"]) == 1
    assert len(sink.records) == 1 and sink.records[0].usage_missing


@pytest.mark.asyncio
async def test_checker_write_tool_and_followup_are_measured(runtime):
    config, request, state, sink = runtime
    request.writable = True
    request.metadata["agent_id"] = "checker-author"
    state["tool"] = ("write", {"path": "checker.c", "content": "int checker(void) { return 0; }\n"})
    result = await PiAgentBackend(config).run(request)
    assert result.success, (result.error, result.raw_stderr)
    assert (request.cwd / "checker.c").exists()
    assert len(state["requests"]) == len(sink.records) == 2


@pytest.mark.asyncio
async def test_path_escape_denied_before_write(runtime, tmp_path):
    config, request, state, _ = runtime
    request.writable = True
    request.metadata["agent_id"] = "checker-author"
    target = tmp_path / "outside.c"
    state["tool"] = ("write", {"path": str(target), "content": "bad"})
    result = await PiAgentBackend(config).run(request)
    assert result.success, (result.error, result.raw_stderr)
    assert not target.exists()
    assert "tool_denied" in (request.output_dir / "protocol.jsonl").read_text()


@pytest.mark.asyncio
async def test_timeout_preserves_missing_call_accounting(runtime):
    config, request, state, sink = runtime
    state["delay"] = 4
    request.timeout_seconds = 2.5
    result = await PiAgentBackend(config).run(request)
    assert not result.success and result.timed_out
    assert len(state["requests"]) == 1
    assert len(sink.records) == 1 and sink.records[0].usage_missing


def test_all_ten_definitions_resolve_and_unknown_fields_fail(runtime, tmp_path):
    path, request, _, _ = runtime
    config, _ = load_pi_config(path)
    definitions = list((ROOT / "agents/definitions").glob("*.md"))
    assert len(definitions) == 10
    for definition in definitions:
        request.metadata["agent_id"] = definition.stem
        resolved = resolve_agent(config, request)
        assert resolved["source_sha256"]
        assert "mode:" not in resolved["system_prompt"]
    copied = tmp_path / "definitions"
    copied.mkdir()
    text = (
        definitions[0]
        .read_text()
        .replace("schema_version: 1", "schema_version: 1\npermission: allow")
    )
    (copied / definitions[0].name).write_text(text)
    config["definitions_root"] = str(copied)
    request.metadata["agent_id"] = definitions[0].stem
    with pytest.raises(ValueError, match="permission"):
        resolve_agent(config, request)


def configure_mcp(config_path: Path, *extra: str) -> None:
    mcp = config_path.parent / "mcp.json"
    mcp.write_text(
        json.dumps(
            {
                "mcpServers": {
                    "defuzz_callgraph": {
                        "command": sys.executable,
                        "args": [
                            str(ROOT / "orchestrator/tests/fixtures/pi_mcp_server.py"),
                            *extra,
                        ],
                    }
                }
            }
        )
    )
    config = json.loads(config_path.read_text())
    config["mcp_path"] = str(mcp)
    config["mcp_startup_timeout_ms"] = 1500
    config_path.write_text(json.dumps(config))


@pytest.mark.asyncio
async def test_mcp_direct_query_archives_full_original_and_stderr(runtime):
    config, request, state, _ = runtime
    configure_mcp(config)
    request.metadata.update(agent_id="source-mapper", use_callgraph=True)
    state["tool"] = ("mcp__defuzz_callgraph__list_targets", {})
    result = await PiAgentBackend(config).run(request)
    assert result.success, (result.error, result.raw_stderr)
    assert result.callgraph_queries == 1
    assert result.callgraph_data_queries == 0
    originals = list((request.output_dir / "artifacts").glob("*.txt"))
    assert len(originals) == 1
    assert originals[0].read_text() == "mcp-original-" + "界" * 30000
    assert "mcp-fixture-stderr" in (request.output_dir / "mcp.log").read_text()
    tools = json.loads((request.output_dir / "tools.json").read_text())
    assert all(
        t["name"].startswith("mcp__defuzz_callgraph__")
        or t["name"] in {"read", "grep", "find", "ls"}
        for t in tools
    )


@pytest.mark.asyncio
async def test_missing_mcp_tools_fail_before_model_request(runtime):
    config, request, state, sink = runtime
    configure_mcp(config, "--empty")
    request.metadata.update(agent_id="source-mapper", use_callgraph=True)
    result = await PiAgentBackend(config).run(request)
    assert not result.success and result.error_category == "mcp"
    assert not state["requests"] and not sink.records


@pytest.mark.asyncio
async def test_model_call_cap_stops_tool_followup(runtime):
    config, request, state, sink = runtime
    settings = json.loads(config.read_text())
    settings["max_model_calls"] = 1
    config.write_text(json.dumps(settings))
    (request.cwd / "input.txt").write_text("fixture")
    state["tool"] = ("read", {"path": "input.txt"})
    result = await PiAgentBackend(config).run(request)
    assert not result.success and result.error_category == "budget"
    assert len(state["requests"]) == len(sink.records) == 1


@pytest.mark.asyncio
async def test_mcp_call_wall_deadline_cancels_and_reaps(runtime):
    config, request, state, _ = runtime
    configure_mcp(config, "--slow")
    settings = json.loads(config.read_text())
    settings["mcp_call_timeout_ms"] = 500
    config.write_text(json.dumps(settings))
    request.metadata.update(agent_id="source-mapper", use_callgraph=True)
    state["tool"] = ("mcp__defuzz_callgraph__list_targets", {})
    started = time.monotonic()
    result = await PiAgentBackend(config).run(request)
    assert not result.success and result.error_category == "timeout"
    assert time.monotonic() - started < 8
    assert len(state["requests"]) == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("mode", ["missing", "malformed", "sequence", "large"])
async def test_parent_rejects_bad_protocol_and_drains_stderr(runtime, tmp_path, mode, monkeypatch):
    config, request, state, _ = runtime
    worker = tmp_path / "bad_worker.py"
    worker.write_text(
        "import json,sys\n"
        "r=json.loads(sys.stdin.readline())\n"
        "sys.stderr.write('diagnostic'*200000); sys.stderr.flush()\n"
        "v={k:r[k] for k in ['request_id','run_id','stage_id','attempt_id']}\n"
        "v.update(protocol_version=1,seq=1,type='ready',timestamp=r['timestamp'],data={})\n"
        + ("v['data']={'large':'界'*400000}\n" if mode == "large" else "")
        + ("v['seq']=3\n" if mode == "sequence" else "")
        + (
            "print('{bad frame}',flush=True)\n"
            if mode == "malformed"
            else "print(json.dumps(v,ensure_ascii=False),flush=True)\n"
        )
    )
    backend = PiAgentBackend(config)
    monkeypatch.setattr(backend, "argv_for", lambda *_: [sys.executable, str(worker)])
    result = await backend.run(request)
    assert not result.success and result.error_category == "protocol"
    assert result.stderr_truncated
    assert (request.output_dir / "stderr.log").stat().st_size == 2000000
    assert not state["requests"]
    if mode == "large":
        assert result.stdout_truncated and result.events[0]["data"]["large"] == "界" * 400000


@pytest.mark.asyncio
async def test_shared_runner_schema_repair_keeps_inputs_and_usage(runtime):
    from defuzz_loop.agents.contracts.output_schemas import GeneratorOutput
    from defuzz_loop.agents.runner import SubagentRunner

    config, request, state, sink = runtime
    state["replies"] = ['{"wrong":"candidate-one"}', '{"source":"int main(void){return 0;}"}']
    runner = SubagentRunner.from_config(
        config,
        cwd=request.cwd,
        output_dir=request.output_dir,
        timeout_secs=30,
        token_sink=sink,
    )
    result = await runner.run(
        "seed-generator", "original-task-with-complete-inputs", GeneratorOutput
    )
    assert result.source.startswith("int main")
    assert len(state["requests"]) == len(sink.records) == 2
    requests = [json.loads(p.read_text()) for p in request.output_dir.glob("*/*/request.json")]
    first = next(r for r in requests if not r["metadata"].get("parent_attempt_id"))
    repair = next(r for r in requests if r["metadata"].get("parent_attempt_id"))
    assert repair["metadata"]["parent_attempt_id"] == first["metadata"]["attempt_id"]
    assert "original-task-with-complete-inputs" in repair["prompt"]
    assert "candidate-one" in repair["prompt"]
    assert "source" in repair["prompt"]


@pytest.mark.asyncio
async def test_budget_exhaustion_blocks_internal_followup(runtime):
    config, request, state, sink = runtime
    state["tokens"] = 20000
    state["tool"] = ("read", {"path": "input.txt"})
    (request.cwd / "input.txt").write_text("fixture")
    # The provider may exceed its requested maxTokens; its reported total is
    # authoritative. The next request must still be blocked.
    settings = json.loads(config.read_text())
    settings["token_budget"] = 15000
    config.write_text(json.dumps(settings))
    result = await PiAgentBackend(config).run(request)
    assert not result.success and result.error_category == "budget"
    assert len(state["requests"]) == len(sink.records) == 1
    assert sink.records[0].total_tokens == 20000


@pytest.mark.asyncio
async def test_provider_rate_limit_is_not_automatically_retried(runtime):
    config, request, state, sink = runtime
    state["status"] = 429
    result = await PiAgentBackend(config).run(request)
    assert not result.success and result.error_category == "provider"
    assert len(state["requests"]) == 1
    assert len(sink.records) == 1 and sink.records[0].usage_missing
    assert "429" in (request.output_dir / "provider-events.jsonl").read_text()


@pytest.mark.asyncio
async def test_parent_cancellation_reaps_worker_and_stdio_mcp(runtime):
    config, request, state, sink = runtime
    configure_mcp(config, "--slow")
    request.metadata.update(agent_id="source-mapper", use_callgraph=True)
    state["tool"] = ("mcp__defuzz_callgraph__list_targets", {})
    backend = PiAgentBackend(config)
    task = asyncio.create_task(backend.run(request))
    protocol = request.output_dir / "protocol.jsonl"
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        if protocol.exists() and "tool_execution_start" in protocol.read_text():
            break
        await asyncio.sleep(0.025)
    else:
        task.cancel()
        await asyncio.gather(task, return_exceptions=True)
        pytest.fail("MCP call did not start")
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert len(state["requests"]) == 1 and len(sink.records) == 1
    events = [json.loads(line) for line in protocol.read_text().splitlines()]
    pids = [
        e["data"]["pid"] for e in events if e.get("data", {}).get("type") == "mcp_process_started"
    ]
    assert pids
    for pid in pids:
        with pytest.raises(ProcessLookupError):
            os.kill(pid, 0)
    assert (request.output_dir / "session.jsonl").is_file()


@pytest.mark.asyncio
async def test_offline_inspection_verifies_large_artifacts_without_new_calls(runtime):
    from defuzz_loop.agents.trace import inspect_attempt

    config, request, state, _ = runtime
    state["reply"] = json.dumps({"ok": True, "large": "x" * 70000})
    assert (await PiAgentBackend(config).run(request)).success
    trace = inspect_attempt(request.output_dir)
    assert not trace["integrity_errors"], trace["integrity_errors"]
    assert trace["admission"]["success"]
    assert len(state["requests"]) == 1


@pytest.mark.asyncio
async def test_source_mapping_cli_uses_pi_and_preserves_scope(runtime, tmp_path):
    from defuzz_loop import experiments_cli
    from defuzz_loop.experiment_engine.source_mapping_stage import load_source_mappings

    config, request, state, sink = runtime
    configure_mcp(config)
    (request.cwd / "pass.cc").write_text("// guard\nvoid guard() {}\n")
    # This CLI fixture satisfies the same version/source contract as production.
    import hashlib
    import sqlite3

    (request.cwd / "gcc").mkdir()
    (request.cwd / "gcc/BASE-VER").write_text("16.2.0\n")
    database = tmp_path / "fixture-graph.sqlite"
    with sqlite3.connect(database) as con:
        con.execute("CREATE TABLE provenance(target TEXT, payload TEXT)")
        con.execute("INSERT INTO provenance VALUES (?,?)", (
            "gcc-fixture-aarch64", json.dumps({
                "compiler": "gcc", "version": "16.2.0", "isas": ["aarch64"],
                "source_revision": "fixture", "csv_sha256": "fixture",
                "source_files_sha256": {
                    "pass.cc": hashlib.sha256((request.cwd / "pass.cc").read_bytes()).hexdigest()
                },
            }),
        ))
    mcp_path = tmp_path / "mcp.json"
    mcp_config = json.loads(mcp_path.read_text())
    mcp_config["mcpServers"]["defuzz_callgraph"]["database_path"] = str(database)
    mcp_path.write_text(json.dumps(mcp_config))
    accepted = tmp_path / "accepted.jsonl"
    accepted.write_text(
        json.dumps(
            {
                "invariant_id": "MAP-001",
                "statement": "Protected functions check a guard.",
                "compiler": "gcc",
                "mechanism": "canary",
                "target": "aarch64",
            }
        )
        + "\n"
    )
    state["tool"] = ("mcp__defuzz_callgraph__list_targets", {})
    state["reply"] = json.dumps(
        {
            "phi_id": "MAP-001",
            "compiler": "gcc",
            "isa": "aarch64",
            "mapping_confidence": "low",
            "level_1_pass": {
                "name": "guard",
                "file": "pass.cc",
                "line_start": 1,
                "line_end": 2,
                "evidence": "Fixture mapping anchor",
            },
        }
    )
    argv = [
        "source-mapping",
        "--pi-config",
        str(config),
        "--source-root",
        str(request.cwd),
        "--inputs",
        str(accepted),
        "--mechanism",
        "canary",
        "--isa",
        "aarch64",
        "--output-root",
        str(tmp_path / "cli-runs"),
        "--token-budget",
        "50000",
        "--time-budget-minutes",
        "1",
    ]
    assert await asyncio.to_thread(experiments_cli.main, [*argv, "--show-plan"]) == 0
    assert not state["requests"]
    assert await asyncio.to_thread(experiments_cli.main, argv) == 0
    artifacts = tmp_path / "cli-runs/source-mapping/rep-001/artifacts"
    mappings, summary = load_source_mappings(
        artifacts / "source-mappings.jsonl",
        source_root=request.cwd,
    )
    assert [(m.phi_id, m.isa) for m in mappings] == [("MAP-001", "aarch64")]
    assert summary["callgraph_queries"] == 1
    assert len(state["requests"]) == 2
    assert await asyncio.to_thread(experiments_cli.main, [*argv, "--resume"]) == 0
    assert len(state["requests"]) == 2


@pytest.mark.asyncio
async def test_pi_mapping_handoff_to_audit_archives_full_evidence(runtime, tmp_path):
    from test_audit_prompt_bundle import _reference

    from defuzz_loop.agent_audit import run as audit
    from defuzz_loop.experiment_engine.models import ExperimentPlan
    from defuzz_loop.experiment_engine.source_mapping_stage import run as map_sources
    from defuzz_loop.token_usage import use_token_usage

    config, request, state, sink = runtime
    configure_mcp(config)
    (request.cwd / "pass.cc").write_text("// guard\nvoid guard() {}\n")
    accepted = tmp_path / "accepted.jsonl"
    accepted.write_text(
        json.dumps(
            {
                "invariant_id": "MAP-001",
                "statement": "Protected functions check a guard.",
                "compiler": "gcc",
                "mechanism": "canary",
                "target": "aarch64",
            }
        )
        + "\n"
    )
    state["tool"] = ("mcp__defuzz_callgraph__list_targets", {})
    state["reply"] = json.dumps(
        {
            "phi_id": "MAP-001",
            "compiler": "gcc",
            "isa": "aarch64",
            "mapping_confidence": "low",
            "level_1_pass": {
                "name": "guard",
                "file": "pass.cc",
                "line_start": 1,
                "line_end": 2,
                "evidence": "Fixture source anchor",
            },
        }
    )
    backend = PiAgentBackend(config)
    with use_token_usage(sink):
        mapped = await map_sources(
            ExperimentPlan(
                run_id="mapping",
                experiment="source-mapping",
                source_root=request.cwd,
                parameters={"accepted_invariants": str(accepted)},
            ),
            1,
            tmp_path / "mapping",
            backend,
        )
    assert mapped.success, mapped.errors
    state["requests"].clear()
    import re

    def reply(body):
        text = json.dumps(body["messages"])
        identity = re.findall(
            r"Echo this exact value as worker_bundle_sha256 in the JSON response: "
            r"([a-f0-9]{64})",
            text,
        )[-1]
        return {
            "schema_version": 1,
            "family": "A",
            "variant": "without-oracle",
            "worker_bundle_sha256": identity,
            "candidates": [],
            "coverage_gaps": [],
        }

    state["reply"] = reply
    with use_token_usage(sink):
        result = await audit(
            ExperimentPlan(
                run_id="audit",
                experiment="agent-audit",
                variant="without-oracle",
                source_root=request.cwd,
                parameters={
                    "reference_root": str(_reference(tmp_path)),
                    "compiler": "gcc",
                    "mechanisms": ["canary"],
                    "isas": ["aarch64"],
                    "source_mappings": str(tmp_path / "mapping/source-mappings.jsonl"),
                    "accepted_invariants": str(accepted),
                },
            ),
            1,
            tmp_path / "audit",
            backend,
        )
    assert result.success, result.errors
    execution = json.loads((tmp_path / "audit/worker-a-initial/execution.json").read_text())
    assert execution["provider"] == "pi" and execution["callgraph_queries"] == 1
    archive = json.loads((tmp_path / "audit/worker-a-initial/runtime-archive.json").read_text())
    directory = (tmp_path / "audit/worker-a-initial" / archive["path"]).resolve()
    assert (directory / "attempt-001/provider-events.jsonl").is_file()
    assert (directory / "attempt-001/session.jsonl").is_file()
    assert len(sink.records) == 4


@pytest.mark.asyncio
async def test_sdk_find_uses_installed_search_tool(runtime):
    config, request, state, _ = runtime
    (request.cwd / "fixture.cc").write_text("void guard() {}\n")
    state["tool"] = ("find", {"path": ".", "pattern": "*.cc"})
    result = await PiAgentBackend(config).run(request)
    assert result.success, result.error
    events = [
        json.loads(line) for line in (request.output_dir / "events.jsonl").read_text().splitlines()
    ]
    found = next(e for e in events if e["type"] == "tool_execution_end")
    assert not found["isError"], found["result"]
    assert "fixture.cc" in json.dumps(found["result"])
