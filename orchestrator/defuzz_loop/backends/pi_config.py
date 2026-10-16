"""Strict Pi configuration/agent resolution, without global runtime defaults."""

from __future__ import annotations

import hashlib
import json
import os
import re
from pathlib import Path
from typing import Any, Literal

import yaml
from pydantic import BaseModel, ConfigDict, Field, PositiveInt

from defuzz_loop.backends.base import AgentRequest

ENV_REFERENCE = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}")
GRAPH_TOOLS = tuple(
    f"mcp__defuzz_callgraph__{name}"
    for name in (
        "list_targets",
        "find_function",
        "get_callees",
        "get_callers",
        "slice_from",
        "paths_between",
    )
)
BUILTIN_TOOLS = {"read", "grep", "find", "ls", "write", "edit", "bash"}


def project_root() -> Path:
    return Path(__file__).resolve().parents[3]


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


class PiRuntimeConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")
    schema_version: Literal[1]
    model_provider: str = Field(min_length=1)
    model_id: str = Field(min_length=1)
    models_path: str
    mcp_path: str | None = None
    definitions_root: str = "../../agents/definitions"
    worker_path: str = "../../agent-runtime/dist/worker.js"
    thinking_level: Literal["off", "minimal", "low", "medium", "high", "xhigh"] = "minimal"
    temperature_policy: Literal["apply", "omit"] = "apply"
    temperature_note: str | None = None
    max_model_calls: PositiveInt = 32
    max_output_tokens: PositiveInt = 8192
    token_budget: PositiveInt | None = None
    strict_usage: bool = True
    attempt_timeout_seconds: PositiveInt = 600
    request_timeout_ms: PositiveInt = 120000
    mcp_startup_timeout_ms: PositiveInt = 30000
    mcp_call_timeout_ms: PositiveInt = 30000
    cleanup_timeout_ms: PositiveInt = 5000
    credential_env: list[str] = Field(default_factory=list)


class AgentDefinition(BaseModel):
    model_config = ConfigDict(extra="forbid")
    schema_version: Literal[1]
    agent_id: str = Field(pattern=r"^[a-z][a-z0-9-]+$")
    description: str = Field(min_length=1)
    temperature: float | None = Field(default=None, ge=0, le=2)
    tools: list[str]
    deny_tools: list[str] = Field(default_factory=list)
    required_tools: list[str] = Field(default_factory=list)


def resolve_path(value: str, base: Path) -> Path:
    value = value.replace("${PROJECT_ROOT}", str(project_root()))
    if "${" in value:
        raise ValueError("paths cannot use environment-dependent expansion")
    return (base / value).expanduser().resolve(strict=True)


def load_pi_config(path: Path) -> tuple[dict[str, Any], str]:
    """Hash the config and every referenced implementation/configuration input."""
    path = path.expanduser().resolve(strict=True)
    config = PiRuntimeConfig.model_validate_json(path.read_bytes()).model_dump()
    if config["temperature_policy"] == "omit" and not config["temperature_note"]:
        raise ValueError("omitting definition temperatures requires temperature_note")
    for key in ("models_path", "mcp_path", "definitions_root", "worker_path"):
        if config[key] is not None:
            config[key] = str(resolve_path(config[key], path.parent))
    models = json.loads(Path(config["models_path"]).read_bytes())
    if set(models) != {"providers"}:
        raise ValueError("models configuration must contain only providers")
    provider = models["providers"].get(config["model_provider"])
    if not isinstance(provider, dict):
        raise ValueError("pinned model provider is absent")
    if not any(m.get("id") == config["model_id"] for m in provider.get("models", [])):
        raise ValueError("pinned model is absent")
    for p in models["providers"].values():
        key = p.get("apiKey")
        if key is not None and (not isinstance(key, str) or not ENV_REFERENCE.fullmatch(key)):
            raise ValueError("model apiKey must be an explicit ${ENV_NAME} reference")
        for value in p.get("headers", {}).values():
            if not isinstance(value, str) or not ENV_REFERENCE.search(value):
                raise ValueError("model headers must reference explicit environment variables")
    config["models"] = models
    mcp = (
        json.loads(Path(config["mcp_path"]).read_bytes())
        if config["mcp_path"]
        else {"mcpServers": {}}
    )
    if set(mcp) != {"mcpServers"} or not isinstance(mcp["mcpServers"], dict):
        raise ValueError("MCP configuration must contain only mcpServers")
    for name, server in mcp["mcpServers"].items():
        if not re.fullmatch(r"[a-z][a-z0-9_]*", name):
            raise ValueError("MCP server names must be normalized (lowercase/underscore)")
        if not isinstance(server, dict) or set(server) - {
            "command",
            "args",
            "cwd",
            "env",
            "url",
            "headers",
            "type",
            "database_path",
            "provenance_path",
        }:
            raise ValueError(f"unsupported MCP server configuration: {name}")
        if bool(server.get("command")) == bool(server.get("url")):
            raise ValueError("MCP requires exactly one of command or url")
        if server.get("type") not in {None, "stdio", "http"}:
            raise ValueError("only stdio and streamable HTTP MCP are supported")
        for value in (*server.get("env", {}).values(), *server.get("headers", {}).values()):
            if not isinstance(value, str) or value.startswith("!"):
                raise ValueError("MCP environment/header values cannot execute commands")
        if server.get("command"):
            if "/" in server["command"]:
                server["command"] = str(resolve_path(server["command"], path.parent))
            if not isinstance(server.get("args", []), list):
                raise ValueError("MCP args must be an array")
            server["cwd"] = str(resolve_path(server.get("cwd", "${PROJECT_ROOT}"), path.parent))
        for key in ("database_path", "provenance_path"):
            if server.get(key):
                server[key] = str(resolve_path(server[key], path.parent))
        for key in ("args", "env"):
            if key in server:
                server[key] = json.loads(
                    json.dumps(server[key]).replace("${PROJECT_ROOT}", str(project_root()))
                )
    config["mcp"] = mcp["mcpServers"]
    refs = set(ENV_REFERENCE.findall(json.dumps({"models": models, "mcp": config["mcp"]})))
    if not refs <= set(config["credential_env"]):
        raise ValueError("credential_env must list every referenced environment variable")
    if not all(re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name)
               for name in config["credential_env"]):
        raise ValueError("credential_env entries must be environment variable names")
    files = [path, Path(config["models_path"]), Path(config["worker_path"])]
    if config["mcp_path"]:
        files.append(Path(config["mcp_path"]))
    definitions = Path(config["definitions_root"])
    files.extend(sorted(definitions.glob("*.md")))
    runtime = Path(config["worker_path"]).parent.parent
    files.extend(sorted((runtime / "src").glob("*.ts")))
    files.extend(sorted((runtime / "dist").glob("*.js")))
    for name in ("package.json", "package-lock.json"):
        files.append(runtime / name)
    for server in config["mcp"].values():
        if server.get("command") and Path(server["command"]).is_file():
            files.append(Path(server["command"]))
        for key in ("provenance_path",):
            if server.get(key):
                files.append(Path(server[key]))
    config["input_hashes"] = {str(p): digest(p) for p in dict.fromkeys(files)}
    from defuzz_loop.stages.s3_source_mapping.callgraph import database_digest

    for server in config["mcp"].values():
        if server.get("database_path"):
            config["input_hashes"][server["database_path"]] = database_digest(
                Path(server["database_path"])
            )
    # The launcher script alone does not freeze the Python server it starts.
    if config["mcp"].get("defuzz_callgraph", {}).get("database_path"):
        for implementation in sorted((project_root() / "callgraph_mcp/src").rglob("*.py")):
            config["input_hashes"][str(implementation)] = digest(implementation)
    config["model"] = f"{config['model_provider']}/{config['model_id']}"
    return config, hashlib.sha256(json.dumps(config, sort_keys=True).encode()).hexdigest()


def agent_id_for(request: AgentRequest) -> str:
    if value := request.metadata.get("agent_id"):
        return str(value)
    if request.metadata.get("stage") == "agent-audit":
        return (
            "audit-neutral"
            if request.metadata.get("variant") == "bare-agent"
            else (f"audit-family-{str(request.metadata['family']).lower()}")
        )
    return "checker-author" if request.writable else "invariant-reviewer"


def resolve_agent(config: dict[str, Any], request: AgentRequest) -> dict[str, Any]:
    identity = agent_id_for(request)
    family = re.fullmatch(r"audit-family-([a-z0-9_-]+)", identity)
    definition_id = "audit-family" if family else identity
    if not re.fullmatch(r"[a-z][a-z0-9-]+", definition_id):
        raise ValueError("invalid agent_id")
    path = Path(config["definitions_root"]) / f"{definition_id}.md"
    text = path.read_text(encoding="utf-8")
    parts = text.split("---", 2)
    if len(parts) != 3 or parts[0].strip():
        raise ValueError(f"agent definition requires YAML frontmatter: {path}")
    definition = AgentDefinition.model_validate(yaml.safe_load(parts[1]))
    if definition.agent_id != definition_id:
        raise ValueError("agent definition identity does not match filename")
    names = definition.tools + definition.deny_tools + definition.required_tools
    if set(names) - BUILTIN_TOOLS - set(GRAPH_TOOLS):
        raise ValueError("unknown or unapproved agent tool")
    if len(set(definition.tools)) != len(definition.tools):
        raise ValueError("duplicate tools in definition")
    if not set(definition.required_tools) <= set(definition.tools) - set(definition.deny_tools):
        raise ValueError("required_tools must be allowed")
    tools = set(definition.tools) - set(definition.deny_tools)
    if not request.writable:
        tools -= {"write", "edit", "bash"}
    if request.metadata.get("use_callgraph") is not True:
        tools -= set(GRAPH_TOOLS)
    required = set(definition.required_tools) & tools
    if request.metadata.get("use_callgraph") is True:
        if not set(GRAPH_TOOLS) <= tools or "defuzz_callgraph" not in config["mcp"]:
            raise ValueError("CallGraph requested but not authorized/configured")
        required |= set(GRAPH_TOOLS)
    body = parts[2].strip()
    if family:
        body += f"\n\nThis instance is assigned to audit task: {family[1].upper()}."
    return {
        **definition.model_dump(exclude_none=True),
        "agent_id": identity,
        "tools": sorted(tools),
        "required_tools": sorted(required),
        "source_path": str(path),
        "source_sha256": digest(path),
        "body_sha256": hashlib.sha256(body.encode()).hexdigest(),
        "system_prompt": body,
        "model": config["model"],
        "thinking_level": config["thinking_level"],
        "temperature_policy": config["temperature_policy"],
        "temperature_note": config["temperature_note"],
        "instance_parameters": {"family": family[1].upper()} if family else {},
    }


def validate_credentials(config: dict[str, Any]) -> None:
    for name in config["credential_env"]:
        if not os.environ.get(name):
            raise ValueError(f"Pi credential environment variable missing: {name}")
