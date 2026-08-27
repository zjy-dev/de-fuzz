from __future__ import annotations

import asyncio
import json
import os
import threading
from collections.abc import Iterator, Mapping
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest
from pydantic import ValidationError

from defuzz_loop.experiment_engine.agent_backend import AgentRequest
from defuzz_loop.experiment_engine.http_agent_backend import (
    HTTPAgentConfig,
    HTTPResponsesAgentBackend,
    LocalWorkspaceToolExecutor,
    load_http_agent_config,
)
from defuzz_loop.token_usage import TokenUsageContext, TokenUsageSink, read_jsonl


class _ResponseServer(ThreadingHTTPServer):
    responses: list[tuple[int, Mapping[str, Any]]]
    requests: list[dict[str, Any]]
    authorizations: list[str | None]
    idempotency_keys: list[str | None]
    delay_seconds: float


class _ResponseHandler(BaseHTTPRequestHandler):
    server: _ResponseServer

    def do_POST(self) -> None:  # noqa: N802
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        self.server.requests.append(json.loads(body))
        self.server.authorizations.append(self.headers.get("Authorization"))
        self.server.idempotency_keys.append(self.headers.get("Idempotency-Key"))
        if self.server.delay_seconds:
            import time

            time.sleep(self.server.delay_seconds)
        status, payload = self.server.responses.pop(0)
        encoded = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        try:
            self.wfile.write(encoded)
        except BrokenPipeError:
            pass

    def log_message(self, format: str, *args: Any) -> None:
        del format, args


class _RedirectHandler(BaseHTTPRequestHandler):
    server: Any

    def do_POST(self) -> None:  # noqa: N802
        self.server.authorizations.append(self.headers.get("Authorization"))
        self.send_response(307)
        self.send_header("Location", self.server.redirect_location)
        self.end_headers()

    def log_message(self, format: str, *args: Any) -> None:
        del format, args


@contextmanager
def _response_server(
    responses: list[tuple[int, Mapping[str, Any]]], *, delay_seconds: float = 0
) -> Iterator[_ResponseServer]:
    server = _ResponseServer(("127.0.0.1", 0), _ResponseHandler)
    server.responses = list(responses)
    server.requests = []
    server.authorizations = []
    server.idempotency_keys = []
    server.delay_seconds = delay_seconds
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


@contextmanager
def _redirect_server(location: str) -> Iterator[Any]:
    server = ThreadingHTTPServer(("127.0.0.1", 0), _RedirectHandler)
    server.redirect_location = location
    server.authorizations = []
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


def _config(server: _ResponseServer, **updates: Any) -> HTTPAgentConfig:
    values: dict[str, Any] = {
        "base_url": f"http://127.0.0.1:{server.server_port}/v1",
        "model": "test-model",
        "api_key_env": "DEFUZZ_TEST_API_KEY",
        "reasoning_effort": "medium",
        "timeout": 2,
        "max_retries": 0,
        "retry_backoff_seconds": 0,
    }
    values.update(updates)
    return HTTPAgentConfig.model_validate(values)


def _usage(
    input_tokens: int,
    output_tokens: int,
    *,
    cached_tokens: int = 0,
    reasoning_tokens: int = 0,
) -> dict[str, Any]:
    return {
        "input_tokens": input_tokens,
        "output_tokens": output_tokens,
        "total_tokens": input_tokens + output_tokens,
        "input_tokens_details": {"cached_tokens": cached_tokens},
        "output_tokens_details": {"reasoning_tokens": reasoning_tokens},
    }


def _final_response(value: str, *, usage: Mapping[str, Any] | None = None) -> dict[str, Any]:
    return {
        "id": "resp-final",
        "status": "completed",
        "output": [
            {
                "type": "message",
                "role": "assistant",
                "content": [{"type": "output_text", "text": value}],
            }
        ],
        "usage": usage or _usage(2, 1),
    }


def _finish_response(
    summary: str,
    *,
    usage: Mapping[str, Any] | None = None,
    response_id: str = "resp-finish",
) -> dict[str, Any]:
    return {
        "id": response_id,
        "status": "completed",
        "output": [
            {
                "type": "function_call",
                "call_id": "call-finish",
                "name": "finish",
                "arguments": json.dumps({"summary": summary}),
            }
        ],
        "usage": usage or _usage(2, 1),
    }


def test_load_http_agent_config_accepts_yaml_and_json_without_inline_secret(
    tmp_path: Path,
) -> None:
    yaml_path = tmp_path / "agent.yaml"
    yaml_path.write_text(
        """
http_agent:
  base_url: http://127.0.0.1:8787/v1/
  model: coconut-model
  api_key_env: COCONUT_API_KEY
  max_output_tokens: 4096
  max_tool_output_chars: 20000
  search_max_matches: 25
  read_content_chars: 16000
  continuation_mode: full_input
""".lstrip(),
        encoding="utf-8",
    )
    loaded = load_http_agent_config(yaml_path)
    assert loaded.base_url == "http://127.0.0.1:8787/v1"
    assert loaded.responses_url == "http://127.0.0.1:8787/v1/responses"
    assert loaded.reasoning_effort == "medium"
    assert loaded.max_output_tokens == 4096
    assert loaded.max_tool_output_chars == 20_000
    assert loaded.search_max_matches == 25
    assert loaded.read_content_chars == 16_000
    assert loaded.continuation_mode == "full_input"
    with pytest.raises(ValidationError):
        HTTPAgentConfig(
            base_url="https://api.openai.com/v1",
            model="gpt-test",
            api_key_env="OPENAI_API_KEY",
            continuation_mode="previous_response_id",  # type: ignore[arg-type]
        )

    json_path = tmp_path / "agent.json"
    json_path.write_text(
        json.dumps(
            {
                "base_url": "https://api.openai.com/v1",
                "model": "gpt-test",
                "api_key_env": "OPENAI_API_KEY",
                "timeout": 30,
            }
        ),
        encoding="utf-8",
    )
    assert HTTPAgentConfig.load(json_path).timeout == 30
    with pytest.raises(ValidationError, match="plain HTTP is permitted only for loopback"):
        HTTPAgentConfig(
            base_url="http://api.example.test/v1",
            model="gpt-test",
            api_key_env="OPENAI_API_KEY",
        )

    inline_secret = "must-not-appear-in-validation-error"
    with pytest.raises(ValidationError, match="inline credentials are forbidden") as caught:
        HTTPAgentConfig.model_validate(
            {
                "base_url": "https://api.openai.com/v1",
                "model": "gpt-test",
                "api_key_env": "OPENAI_API_KEY",
                "api_key": inline_secret,
            }
        )
    assert inline_secret not in str(caught.value)
    with pytest.raises(ValidationError, match="must not contain credentials"):
        HTTPAgentConfig(
            base_url="https://secret@example.test/v1",
            model="gpt-test",
            api_key_env="OPENAI_API_KEY",
        )
    with pytest.raises(ValidationError):
        HTTPAgentConfig(
            base_url="https://api.openai.com/v1",
            model="gpt-test",
            api_key_env="OPENAI_API_KEY",
            max_output_tokens=0,
        )
    for field in ("max_tool_output_chars", "search_max_matches", "read_content_chars"):
        with pytest.raises(ValidationError):
            HTTPAgentConfig.model_validate(
                {
                    "base_url": "https://api.openai.com/v1",
                    "model": "gpt-test",
                    "api_key_env": "OPENAI_API_KEY",
                    field: 0,
                }
            )


@pytest.mark.asyncio
async def test_http_backend_never_forwards_authorization_across_redirects(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "redirect-secret")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    with _response_server([(200, _finish_response("must not arrive"))]) as target:
        location = f"http://127.0.0.1:{target.server_port}/v1/responses"
        with _redirect_server(location) as redirect:
            config = HTTPAgentConfig(
                base_url=f"http://127.0.0.1:{redirect.server_port}/v1",
                model="test-model",
                api_key_env="DEFUZZ_TEST_API_KEY",
                max_retries=0,
            )
            result = await HTTPResponsesAgentBackend(config).run(
                AgentRequest(
                    prompt="do not follow",
                    cwd=workspace,
                    output_dir=tmp_path / "out",
                )
            )

    assert not result.success
    assert "HTTP 307" in (result.error or "")
    assert redirect.authorizations == ["Bearer redirect-secret"]
    assert target.requests == []
    assert target.authorizations == []


def test_custom_tool_executor_does_not_claim_formal_host_isolation(
    tmp_path: Path,
) -> None:
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    request = AgentRequest(prompt="test", cwd=workspace, output_dir=tmp_path / "out")
    executor = LocalWorkspaceToolExecutor(request)
    config = HTTPAgentConfig(
        base_url="http://127.0.0.1:1/v1",
        model="test",
        api_key_env="DEFUZZ_TEST_API_KEY",
    )

    backend = HTTPResponsesAgentBackend(config, tool_executor_factory=lambda _: executor)

    assert backend.supports_host_read_isolation is False


@pytest.mark.asyncio
async def test_http_response_body_is_bounded(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.http_agent_backend._MAX_RESPONSE_BODY_BYTES", 64
    )
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    with _response_server([(200, _final_response("x" * 256))]) as server:
        result = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(
                prompt="bounded", cwd=workspace, output_dir=tmp_path / "out"
            )
        )

    assert not result.success
    assert "safety limit" in (result.error or "")


@pytest.mark.asyncio
async def test_http_run_cumulative_response_size_is_bounded(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    monkeypatch.setattr(
        "defuzz_loop.experiment_engine.http_agent_backend._MAX_RUN_RAW_RESPONSE_BYTES",
        350,
    )
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    (workspace / "source.c").write_text("int main(void) {}\n", encoding="utf-8")
    tool_response = {
        "id": "resp-tool",
        "status": "completed",
        "output": [
            {
                "type": "function_call",
                "call_id": "call-read",
                "name": "read_file",
                "arguments": json.dumps(
                    {"path": "source.c", "start_line": 1, "end_line": 0}
                ),
            }
        ],
        "usage": _usage(3, 1),
    }
    with _response_server(
        [(200, tool_response), (200, _finish_response("x" * 256))]
    ) as server:
        result = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(
                prompt="bounded run", cwd=workspace, output_dir=tmp_path / "out"
            )
        )

    assert not result.success
    assert "cumulative response size" in (result.error or "")


@pytest.mark.asyncio
async def test_invalid_json_schema_fails_as_agent_result_before_http(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    schema = tmp_path / "invalid.schema.json"
    schema.write_text(json.dumps({"type": "not-a-json-schema-type"}), encoding="utf-8")
    with _response_server([(200, _final_response("unused"))]) as server:
        result = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(
                prompt="never sent",
                cwd=workspace,
                output_dir=tmp_path / "out",
                schema_path=schema,
            )
        )

    assert not result.success
    assert result.error and result.error.startswith("invalid output schema:")
    assert server.requests == []


@pytest.mark.asyncio
async def test_part_i_embedded_evidence_request_omits_workspace_tools(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    schema = tmp_path / "segment.schema.json"
    schema.write_text(
        json.dumps(
            {
                "type": "object",
                "properties": {"answer": {"type": "string"}},
                "required": ["answer"],
                "additionalProperties": False,
            }
        ),
        encoding="utf-8",
    )
    with _response_server(
        [(200, _final_response(json.dumps({"answer": "grounded"})))]
    ) as server:
        result = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(
                prompt="The complete evidence is embedded here.",
                cwd=workspace,
                output_dir=tmp_path / "out",
                schema_path=schema,
                metadata={"part": "part-i"},
            )
        )

    assert result.success
    assert "tools" not in server.requests[0]
    assert "tool_choice" not in server.requests[0]
    assert "parallel_tool_calls" not in server.requests[0]


@pytest.mark.asyncio
async def test_responses_function_loop_reads_then_returns_structured_output_and_usage(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    key = "super-secret-test-key"
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", key)
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    (workspace / "source.c").write_text("int vulnerable(void) { return 7; }\n")
    schema = tmp_path / "audit-report.schema.json"
    schema.write_text(
        json.dumps(
            {
                "type": "object",
                "properties": {"answer": {"type": "string"}},
                "required": ["answer"],
                "additionalProperties": False,
            }
        ),
        encoding="utf-8",
    )
    tool_response = {
        "id": "resp-tool",
        "status": "completed",
        "output": [
            {
                "type": "function_call",
                "call_id": "call-read",
                "name": "read_file",
                "arguments": json.dumps(
                    {"path": "source.c", "start_line": 1, "end_line": 0}
                ),
            }
        ],
        "usage": _usage(10, 4, cached_tokens=3, reasoning_tokens=2),
    }
    final = _final_response(
        json.dumps({"answer": "found"}),
        usage=_usage(6, 5, cached_tokens=1, reasoning_tokens=3),
    )
    with _response_server([(200, tool_response), (200, final)]) as server:
        sink = TokenUsageSink(
            tmp_path / "usage.jsonl",
            context=TokenUsageContext(
                run_id="run",
                experiment="audit",
                variant="full",
                part="part-iii",
                stage="audit",
            ),
        )
        result = await HTTPResponsesAgentBackend(
            _config(server, max_output_tokens=2048)
        ).run(
            AgentRequest(
                prompt="Inspect the source",
                cwd=workspace,
                output_dir=tmp_path / "out",
                schema_path=schema,
                token_sink=sink,
                require_host_read_isolation=True,
            )
        )

    assert result.success
    assert result.final == {"answer": "found"}
    assert result.usage == {
        "input_tokens": 16,
        "output_tokens": 9,
        "total_tokens": 25,
        "cached_input_tokens": 4,
        "cache_creation_input_tokens": None,
        "reasoning_tokens": 5,
        "usage_missing": False,
    }
    backend = HTTPResponsesAgentBackend(_config(server))
    assert backend.supports_host_read_isolation
    assert backend.model == "test-model"
    assert HTTPResponsesAgentBackend.provider == "http-responses"
    assert server.authorizations == [f"Bearer {key}", f"Bearer {key}"]
    first, second = server.requests
    assert first["reasoning"] == {"effort": "medium"}
    assert first["max_output_tokens"] == 2048
    assert first["store"] is False
    assert "previous_response_id" not in first
    assert first["input"] == [{"role": "user", "content": "Inspect the source"}]
    assert first["text"]["format"]["type"] == "json_schema"
    assert first["text"]["format"]["strict"] is True
    assert {tool["name"] for tool in first["tools"]} == {
        "list_files",
        "search_text",
        "read_file",
    }
    assert "finish" not in {tool["name"] for tool in first["tools"]}
    assert "previous_response_id" not in second
    assert second["max_output_tokens"] == 2048
    assert second["input"][-1]["type"] == "function_call_output"
    tool_output = json.loads(second["input"][-1]["output"])
    assert "int vulnerable" in tool_output["content"]
    assert [event["type"] for event in result.events] == [
        "response.received",
        "tool.call",
        "tool.result",
        "response.received",
        "turn.completed",
    ]
    assert len(read_jsonl(sink.path)) == 2
    persisted = (tmp_path / "out" / "events.jsonl").read_text()
    assert key not in persisted
    assert key not in result.raw_stdout


@pytest.mark.asyncio
async def test_writable_loop_applies_patch_without_starting_subprocess(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    target = workspace / "main.go"
    target.write_text("package old\n", encoding="utf-8")
    tool_response = {
        "id": "resp-patch",
        "status": "completed",
        "output": [
            {
                "type": "function_call",
                "call_id": "call-patch",
                "name": "replace_text",
                "arguments": json.dumps(
                    {
                        "path": "main.go",
                        "old_text": "package old",
                        "new_text": "package oracle",
                        "replace_all": False,
                    }
                ),
            }
        ],
        "usage": _usage(3, 2),
    }
    with _response_server(
        [(200, tool_response), (200, _finish_response("updated package name"))]
    ) as server:
        def forbidden_subprocess(*args: Any, **kwargs: Any) -> None:
            raise AssertionError("HTTP backend must not start a subprocess")

        monkeypatch.setattr(asyncio, "create_subprocess_exec", forbidden_subprocess)
        result = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(
                prompt="Fix the package",
                cwd=workspace,
                output_dir=tmp_path / "out",
                writable=True,
            )
        )

    assert result.success
    assert result.final == {"summary": "updated package name"}
    assert json.loads((tmp_path / "out" / "final.json").read_text()) == result.final
    assert target.read_text(encoding="utf-8") == "package oracle\n"
    assert "max_output_tokens" not in server.requests[0]
    assert "max_output_tokens" not in server.requests[1]
    assert {tool["name"] for tool in server.requests[0]["tools"]} == {
        "list_files",
        "search_text",
        "read_file",
        "write_file",
        "replace_text",
        "finish",
    }
    assert [event["type"] for event in result.events] == [
        "response.received",
        "tool.call",
        "tool.result",
        "response.received",
        "tool.call",
        "turn.completed",
    ]
    assert result.events[-2]["name"] == "finish"


@pytest.mark.asyncio
async def test_invalid_finish_fails_closed_without_final(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    invalid_finish = {
        "id": "resp-finish",
        "status": "completed",
        "output": [
            {
                "type": "function_call",
                "call_id": "call-finish",
                "name": "finish",
                "arguments": json.dumps({"summary": "   "}),
            }
        ],
        "usage": _usage(2, 1),
    }
    with _response_server([(200, invalid_finish)]) as server:
        result = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(prompt="finish", cwd=workspace, output_dir=tmp_path / "out")
        )

    assert not result.success
    assert result.error == "finish summary must be a non-empty string"
    assert result.usage and result.usage["total_tokens"] == 3
    assert result.events[-1]["error_type"] == "_HTTPAgentError"
    assert not (tmp_path / "out" / "final.json").exists()


@pytest.mark.asyncio
async def test_mixed_finish_and_write_call_fails_without_executing_edit(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    target = workspace / "main.go"
    target.write_text("package old\n", encoding="utf-8")
    mixed = {
        "id": "resp-mixed",
        "status": "completed",
        "output": [
            {
                "type": "function_call",
                "call_id": "call-write",
                "name": "write_file",
                "arguments": json.dumps(
                    {"path": "main.go", "content": "package changed\n"}
                ),
            },
            {
                "type": "function_call",
                "call_id": "call-finish",
                "name": "finish",
                "arguments": json.dumps({"summary": "changed package"}),
            },
        ],
        "usage": _usage(4, 2),
    }
    with _response_server([(200, mixed)]) as server:
        result = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(
                prompt="edit",
                cwd=workspace,
                output_dir=tmp_path / "out",
                writable=True,
            )
        )

    assert not result.success
    assert result.error == "finish must be the only function call in its response"
    assert target.read_text(encoding="utf-8") == "package old\n"
    assert [event["type"] for event in result.events] == [
        "response.received",
        "turn.failed",
    ]
    assert not (tmp_path / "out" / "final.json").exists()


@pytest.mark.asyncio
async def test_schema_failure_after_tool_use_gets_bounded_repair_turn(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    (workspace / "sentinel.txt").write_text("41\n", encoding="utf-8")
    schema = tmp_path / "answer.schema.json"
    schema.write_text(
        json.dumps(
            {
                "type": "object",
                "properties": {"answer": {"type": "integer"}},
                "required": ["answer"],
                "additionalProperties": False,
            }
        ),
        encoding="utf-8",
    )
    tool_response = {
        "id": "resp-read",
        "status": "completed",
        "output": [
            {
                "type": "function_call",
                "call_id": "call-read",
                "name": "read_file",
                "arguments": json.dumps(
                    {"path": "sentinel.txt", "start_line": 1, "end_line": 0}
                ),
            }
        ],
        "usage": _usage(5, 2),
    }
    invalid = _final_response("42", usage=_usage(7, 3))
    repaired = _final_response(json.dumps({"answer": 42}), usage=_usage(8, 4))
    with _response_server(
        [(200, tool_response), (200, invalid), (200, repaired)]
    ) as server:
        sink = TokenUsageSink(
            tmp_path / "usage.jsonl",
            context=TokenUsageContext(
                run_id="run",
                experiment="audit",
                variant="full",
                part="part-iii",
                stage="audit",
            ),
        )
        result = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(
                prompt="Read sentinel.txt and return the value plus one",
                cwd=workspace,
                output_dir=tmp_path / "out",
                schema_path=schema,
                token_sink=sink,
            )
        )

    assert result.success
    assert result.final == {"answer": 42}
    assert result.usage and result.usage["total_tokens"] == 29
    assert [event["type"] for event in result.events].count("schema.repair") == 1
    repair_event = next(event for event in result.events if event["type"] == "schema.repair")
    assert repair_event["attempt"] == 1
    assert "failed schema validation" in repair_event["error"]
    repair_input = server.requests[2]["input"]
    assert repair_input[-2]["content"][0]["text"] == "42"
    assert repair_input[-1]["role"] == "user"
    assert "required JSON Schema" in repair_input[-1]["content"]
    assert all(request["text"]["format"]["strict"] for request in server.requests)
    usage_records = read_jsonl(sink.path)
    assert [record.success for record in usage_records] == [True, False, True]
    assert [record.error_type for record in usage_records] == [
        None,
        "SchemaValidationError",
        None,
    ]
    assert [record.total_tokens for record in usage_records] == [7, 10, 12]


@pytest.mark.asyncio
async def test_schema_repair_exhaustion_fails_without_accepting_invalid_output(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    schema = tmp_path / "answer.schema.json"
    schema.write_text(
        json.dumps(
            {
                "type": "object",
                "properties": {"answer": {"type": "integer"}},
                "required": ["answer"],
                "additionalProperties": False,
            }
        ),
        encoding="utf-8",
    )
    with _response_server(
        [(200, _final_response("not-json")), (200, _final_response("42"))]
    ) as server:
        sink = TokenUsageSink(
            tmp_path / "usage.jsonl",
            context=TokenUsageContext(
                run_id="run",
                experiment="audit",
                variant="full",
                part="part-iii",
                stage="audit",
            ),
        )
        result = await HTTPResponsesAgentBackend(
            _config(server, max_schema_retries=1)
        ).run(
            AgentRequest(
                prompt="Return an object",
                cwd=workspace,
                output_dir=tmp_path / "out",
                schema_path=schema,
                token_sink=sink,
            )
        )

    assert not result.success
    assert result.final is None
    assert result.error and "schema validation" in result.error
    assert [event["type"] for event in result.events] == [
        "response.received",
        "schema.repair",
        "response.received",
        "turn.failed",
    ]
    assert result.usage and result.usage["total_tokens"] == 6
    assert [record.success for record in read_jsonl(sink.path)] == [False, False]
    assert {record.error_type for record in read_jsonl(sink.path)} == {
        "SchemaValidationError"
    }
    assert not (tmp_path / "out" / "final.json").exists()


@pytest.mark.asyncio
async def test_local_tools_enforce_cwd_denials_symlink_escape_and_read_only(
    tmp_path: Path,
) -> None:
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    allowed = workspace / "allowed.txt"
    allowed.write_text("needle allowed\n")
    denied = workspace / "private"
    denied.mkdir()
    (denied / "secret.txt").write_text("needle secret\n")
    outside = tmp_path / "outside.txt"
    outside.write_text("outside\n")
    (workspace / "escape.txt").symlink_to(outside)
    executor = LocalWorkspaceToolExecutor(
        AgentRequest(
            prompt="inspect",
            cwd=workspace,
            output_dir=tmp_path / "out",
            deny_read_paths=[denied],
            writable=False,
        )
    )

    listing = await executor.execute(
        "list_files", {"path": ".", "recursive": True}
    )
    assert listing["ok"] is True
    assert listing["entries"] == [{"path": "allowed.txt", "type": "file"}]
    search = await executor.execute(
        "search_text",
        {
            "path": ".",
            "query": "needle",
            "file_glob": "*.txt",
            "case_sensitive": True,
            "offset": 0,
        },
    )
    assert search["matches"] == [
        {"path": "allowed.txt", "line": 1, "text": "needle allowed"}
    ]
    assert search["next_offset"] is None
    denied_read = await executor.execute(
        "read_file", {"path": "private/secret.txt", "start_line": 1, "end_line": 0}
    )
    escaped_read = await executor.execute(
        "read_file", {"path": "escape.txt", "start_line": 1, "end_line": 0}
    )
    outside_read = await executor.execute(
        "read_file", {"path": str(outside), "start_line": 1, "end_line": 0}
    )
    write = await executor.execute(
        "write_file", {"path": "new.txt", "content": "nope"}
    )
    assert denied_read["ok"] is False and "denied" in denied_read["error"]
    assert escaped_read["ok"] is False and "escapes" in escaped_read["error"]
    assert outside_read["ok"] is False and "escapes" in outside_read["error"]
    assert write == {"ok": False, "error": "write tools are disabled for this read-only request"}
    assert not (workspace / "new.txt").exists()


@pytest.mark.asyncio
async def test_read_file_streams_narrow_range_from_large_source_and_caps_whole_read(
    tmp_path: Path,
) -> None:
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    large_source = workspace / "aarch64.cc"
    lines = [f"line {index:05d}: {'x' * 48}\n" for index in range(1, 20_001)]
    large_source.write_text("".join(lines), encoding="utf-8")
    assert large_source.stat().st_size > 1024 * 1024
    executor = LocalWorkspaceToolExecutor(
        AgentRequest(prompt="inspect", cwd=workspace, output_dir=tmp_path / "out")
    )

    narrow = await executor.execute(
        "read_file",
        {"path": "aarch64.cc", "start_line": 15_000, "end_line": 15_002},
    )
    assert narrow["ok"] is True
    assert narrow["content"] == "".join(lines[14_999:15_002])
    assert narrow["end_line"] == 15_002
    assert narrow["eof"] is False
    assert narrow["truncated"] is False
    assert narrow["next_start_line"] == 15_003
    assert narrow["total_lines"] is None

    whole = await executor.execute(
        "read_file", {"path": "aarch64.cc", "start_line": 1, "end_line": 0}
    )
    assert whole["ok"] is True
    assert len(whole["content"]) <= 24 * 1024
    assert whole["truncated"] is True
    assert whole["eof"] is False
    assert isinstance(whole["next_start_line"], int)


@pytest.mark.asyncio
async def test_tool_limits_truncate_search_with_pagination_and_bound_read_output(
    tmp_path: Path,
) -> None:
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    (workspace / "many.txt").write_text(
        "".join(f"needle {index} {'x' * 100}\n" for index in range(20)),
        encoding="utf-8",
    )
    request = AgentRequest(prompt="inspect", cwd=workspace, output_dir=tmp_path / "out")
    executor = LocalWorkspaceToolExecutor(
        request,
        search_max_matches=3,
        read_content_chars=256,
        max_tool_output_chars=1024,
    )
    arguments = {
        "path": ".",
        "query": "needle",
        "file_glob": "*.txt",
        "case_sensitive": True,
        "offset": 0,
    }

    first = await executor.execute("search_text", arguments)
    assert first["ok"] is True
    assert len(first["matches"]) == 3
    assert first["truncated"] is True
    assert first["next_offset"] == 3
    second = await executor.execute(
        "search_text", {**arguments, "offset": first["next_offset"]}
    )
    assert [match["line"] for match in first["matches"]] == [1, 2, 3]
    assert [match["line"] for match in second["matches"]] == [4, 5, 6]
    assert len(json.dumps(first)) <= 1024

    read = await executor.execute(
        "read_file", {"path": "many.txt", "start_line": 1, "end_line": 0}
    )
    assert read["ok"] is True
    assert len(read["content"]) <= 256
    assert read["truncated"] is True
    assert read["next_start_line"] is not None


@pytest.mark.asyncio
async def test_retry_redacts_key_and_honors_request_timeout(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    key = "error-secret-key"
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", key)
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    with _response_server(
        [
            (503, {"error": {"message": f"temporary failure {key}"}}),
            (200, _finish_response("ok")),
        ]
    ) as server:
        result = await HTTPResponsesAgentBackend(_config(server, max_retries=1)).run(
            AgentRequest(prompt="retry", cwd=workspace, output_dir=tmp_path / "retry")
        )
    assert result.success
    assert len(server.requests) == 2
    assert server.idempotency_keys[0]
    assert server.idempotency_keys[0] == server.idempotency_keys[1]
    assert key not in result.raw_stdout

    with _response_server([(200, _finish_response(f"leaked {key}"))]) as server:
        redacted = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(prompt="redact", cwd=workspace, output_dir=tmp_path / "redact")
        )
    assert redacted.final == {"summary": "leaked [REDACTED]"}
    assert key not in json.dumps(redacted.events)
    assert key not in (tmp_path / "redact" / "final.json").read_text()

    with _response_server(
        [(200, _finish_response("too late")), (200, _finish_response("too late"))],
        delay_seconds=0.2,
    ) as server:
        timed_out = await HTTPResponsesAgentBackend(
            _config(server, max_retries=1, retry_backoff_seconds=0)
        ).run(
            AgentRequest(
                prompt="timeout",
                cwd=workspace,
                output_dir=tmp_path / "timeout",
                timeout_seconds=0.03,
            )
        )
    assert not timed_out.success
    assert timed_out.timed_out
    assert "timed out" in (timed_out.error or "")


@pytest.mark.asyncio
async def test_request_deadline_covers_tool_execution(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    tool_response = {
        "id": "resp-tool",
        "status": "completed",
        "output": [
            {
                "type": "function_call",
                "call_id": "call-slow",
                "name": "slow_tool",
                "arguments": "{}",
            }
        ],
        "usage": _usage(2, 1),
    }

    class SlowExecutor:
        @property
        def tools(self) -> list[Mapping[str, Any]]:
            return [
                {
                    "type": "function",
                    "name": "slow_tool",
                    "description": "test tool",
                    "parameters": {
                        "type": "object",
                        "properties": {},
                        "additionalProperties": False,
                    },
                    "strict": True,
                }
            ]

        async def execute(
            self, name: str, arguments: Mapping[str, Any]
        ) -> Mapping[str, Any]:
            del name, arguments
            await asyncio.sleep(1)
            return {"ok": True}

    with _response_server([(200, tool_response)]) as server:
        result = await HTTPResponsesAgentBackend(
            _config(server), tool_executor_factory=lambda _request: SlowExecutor()
        ).run(
            AgentRequest(
                prompt="call tool",
                cwd=workspace,
                output_dir=tmp_path / "out",
                timeout_seconds=0.03,
            )
        )

    assert not result.success
    assert result.timed_out
    assert result.error == "HTTP agent timed out while executing tool slow_tool"
    assert len(server.requests) == 1


@pytest.mark.asyncio
async def test_budget_exceeded_mid_tool_loop_persists_events_and_usage(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    key = "budget-secret-key"
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", key)
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    (workspace / "source.c").write_text(f"/* {key} */\nint main(void) {{}}\n")
    tool_response = {
        "id": "resp-tool",
        "status": "completed",
        "output": [
            {
                "type": "function_call",
                "call_id": "call-read",
                "name": "read_file",
                "arguments": json.dumps(
                    {"path": "source.c", "start_line": 1, "end_line": 0}
                ),
            }
        ],
        "usage": _usage(7, 3),
    }
    with _response_server([(200, tool_response)]) as server:
        sink = TokenUsageSink(
            tmp_path / "usage.jsonl",
            context=TokenUsageContext(
                run_id="run",
                experiment="audit",
                variant="full",
                part="part-iii",
                stage="audit",
            ),
            token_budget=10,
        )
        result = await HTTPResponsesAgentBackend(_config(server)).run(
            AgentRequest(
                prompt=f"inspect without exposing {key}",
                cwd=workspace,
                output_dir=tmp_path / "out",
                token_sink=sink,
            )
        )

    assert not result.success
    assert result.error == "token budget exceeded: consumed 10 of 10 tokens"
    assert result.usage and result.usage["total_tokens"] == 10
    assert [event["type"] for event in result.events] == [
        "response.received",
        "tool.call",
        "tool.result",
        "turn.failed",
    ]
    assert result.events[-1]["error_type"] == "BudgetExceeded"
    persisted = (tmp_path / "out" / "events.jsonl").read_text(encoding="utf-8")
    assert "BudgetExceeded" in persisted
    assert key not in persisted
    assert "[REDACTED]" in persisted
    assert not (tmp_path / "out" / "final.json").exists()
    assert len(server.requests) == 1


@pytest.mark.asyncio
async def test_token_sink_record_error_persists_events_before_propagating(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()

    class BrokenSink:
        def check_budget(self) -> None:
            return

        def record_external_usage(self, *args: Any, **kwargs: Any) -> None:
            del args, kwargs
            raise RuntimeError("sink failed")

    with _response_server([(200, _finish_response("done"))]) as server:
        with pytest.raises(RuntimeError, match="sink failed"):
            await HTTPResponsesAgentBackend(_config(server)).run(
                AgentRequest(
                    prompt="inspect",
                    cwd=workspace,
                    output_dir=tmp_path / "out",
                    token_sink=BrokenSink(),
                )
            )

    events = [
        json.loads(line)
        for line in (tmp_path / "out" / "events.jsonl").read_text().splitlines()
    ]
    assert [event["type"] for event in events] == [
        "response.received",
        "tool.call",
        "turn.failed",
    ]
    assert events[-1]["error_type"] == "RuntimeError"
    assert events[-1]["error"] == "RuntimeError: sink failed"
    assert not (tmp_path / "out" / "final.json").exists()


@pytest.mark.asyncio
async def test_missing_key_fails_before_http_call(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv("DEFUZZ_TEST_API_KEY", raising=False)
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    config = HTTPAgentConfig(
        base_url="http://127.0.0.1:1/v1",
        model="test",
        api_key_env="DEFUZZ_TEST_API_KEY",
    )
    result = await HTTPResponsesAgentBackend(config).run(
        AgentRequest(prompt="never", cwd=workspace, output_dir=tmp_path / "out")
    )
    assert not result.success
    assert result.error == "HTTP agent API key is unavailable: export $DEFUZZ_TEST_API_KEY"


@pytest.mark.asyncio
async def test_cancelled_request_propagates(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DEFUZZ_TEST_API_KEY", "test-key")
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    with _response_server(
        [(200, _finish_response("too late"))], delay_seconds=0.2
    ) as server:
        task = asyncio.create_task(
            HTTPResponsesAgentBackend(_config(server)).run(
                AgentRequest(prompt="cancel", cwd=workspace, output_dir=tmp_path / "out")
            )
        )
        await asyncio.sleep(0.01)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task

    assert os.environ["DEFUZZ_TEST_API_KEY"] == "test-key"
