from pathlib import Path


def test_callgraph_mcp_config_and_minimal_permissions() -> None:
    root = Path(__file__).resolve().parents[3]
    config = (root / ".opencode/opencode.jsonc").read_text()
    assert '"command": ["./scripts/run_callgraph_mcp_ssh.sh"]' in config
    for path in (
        root / ".opencode/agent/source-mapper.md",
        root / ".opencode/agents/seed-generator.md",
    ):
        body = path.read_text()
        assert "defuzz-callgraph_*: allow" in body
        for tool in (
            "list_targets",
            "find_function",
            "get_callees",
            "get_callers",
            "slice_from",
            "paths_between",
        ):
            assert tool in body
    for name in ("feedback-analyzer.md", "llm-oracle.md", "minimizer.md"):
        assert "defuzz-callgraph" not in (root / ".opencode/agents" / name).read_text()
