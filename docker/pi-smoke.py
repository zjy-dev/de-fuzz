"""Real SDK and local model fixture in a clean container; no paid model calls."""

from __future__ import annotations

import asyncio
import json
import os
import shutil
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from defuzz_loop.agents.contracts.output_schemas import GeneratorOutput
from defuzz_loop.agents.runner import SubagentRunner
from defuzz_loop.agents.trace import inspect_attempt
from defuzz_loop.experiment_engine.pi_backend import PiAgentBackend
from defuzz_loop.experiment_engine.pi_config import load_pi_config, resolve_agent


async def main() -> None:
    assert shutil.which("opencode") is None
    assert not Path("/workspace/.opencode").exists()
    calls = []
    os.environ["DEFUZZ_PI_CONTAINER_FIXTURE_KEY"] = "local-fixture"

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            calls.append(
                json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            )
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            for chunk in [
                {
                    "choices": [
                        {
                            "index": 0,
                            "delta": {
                                "role": "assistant",
                                "content": '{"source":"int main(void){return 0;}"}',
                            },
                            "finish_reason": None,
                        }
                    ]
                },
                {"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]},
                {
                    "choices": [],
                    "usage": {
                        "prompt_tokens": 40,
                        "completion_tokens": 20,
                        "total_tokens": 60,
                    },
                },
            ]:
                chunk.update(
                    id="chatcmpl-fixture",
                    object="chat.completion.chunk",
                    model="fixture",
                )
                self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
            self.wfile.write(b"data: [DONE]\n\n")

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix="defuzz-pi-container-") as directory:
            root = Path(directory)
            models = root / "models.json"
            models.write_text(
                json.dumps(
                    {
                        "providers": {
                            "fixture": {
                                "baseUrl": f"http://127.0.0.1:{server.server_port}/v1",
                                "api": "openai-completions",
                                "apiKey": "${DEFUZZ_PI_CONTAINER_FIXTURE_KEY}",
                                "models": [
                                    {
                                        "id": "fixture",
                                        "name": "Fixture",
                                        "reasoning": False,
                                        "input": ["text"],
                                        "contextWindow": 200000,
                                        "maxTokens": 8192,
                                    }
                                ],
                            }
                        }
                    }
                )
            )
            config = root / "runtime.json"
            config.write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "model_provider": "fixture",
                        "model_id": "fixture",
                        "models_path": str(models),
                        "definitions_root": "/workspace/agents/definitions",
                        "worker_path": "/workspace/agent-runtime/dist/worker.js",
                        "thinking_level": "off",
                        "max_model_calls": 3,
                        "credential_env": ["DEFUZZ_PI_CONTAINER_FIXTURE_KEY"],
                    }
                )
            )
            workspace = root / "source"
            workspace.mkdir()
            runner = SubagentRunner(
                PiAgentBackend(config),
                cwd=workspace,
                output_dir=root / "attempts",
                timeout_secs=30,
                require_host_read_isolation=True,
            )
            result = await runner.run(
                "seed-generator", "Return one standalone C input.", GeneratorOutput
            )
            assert result.source.startswith("int main")
            attempt = next((root / "attempts").glob("*/*/request.json")).parent
            traced = inspect_attempt(attempt)
            assert not traced["integrity_errors"], traced
            assert traced["admission"]["success"]
            assert len(calls) == 1 and traced["usage"][0]["total_tokens"] == 60
            parsed, _ = load_pi_config(config)
            from defuzz_loop.experiment_engine.agent_backend import AgentRequest

            for definition in Path("/workspace/agents/definitions").glob("*.md"):
                resolve_agent(
                    parsed,
                    AgentRequest(
                        prompt="fixture",
                        cwd=workspace,
                        output_dir=root / "unused",
                        metadata={"agent_id": definition.stem},
                    ),
                )
            print(
                json.dumps(
                    {
                        "kind": "pi-sdk-container-smoke",
                        "success": True,
                        "model_calls": len(calls),
                        "reported_tokens": 60,
                        "definitions": 10,
                        "isolation": "bubblewrap",
                        "offline_trace": True,
                    }
                )
            )
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


if __name__ == "__main__":
    asyncio.run(main())
