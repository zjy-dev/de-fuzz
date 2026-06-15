"""Per-run audit directory: isolation + self-describing manifest (FR-009 / SC-003).

Each run gets its own directory holding its own checkpoints.sqlite and a
manifest.json capturing the environment a checkpoint alone cannot (git sha,
toolchains snapshot, checker catalog, LLM + ablation config). Two runs never
share a directory or sqlite file, so replay/inspect of one can never be
contaminated by another.
"""

from __future__ import annotations

from defuzz_loop.audit import (
    build_manifest,
    make_run_dir,
    read_manifest,
    thread_id,
    write_manifest,
)
from defuzz_loop.llm import LLMConfig
from defuzz_loop.state import AblationFlags, Blackboard


def test_each_run_gets_an_isolated_directory(tmp_path) -> None:
    a = make_run_dir(tmp_path, "exp", "canary")
    b = make_run_dir(tmp_path, "exp", "canary")
    # Same experiment+mechanism, yet distinct directories (timestamp-suffixed).
    assert a.exists() and b.exists()
    assert a.parent == tmp_path and b.parent == tmp_path
    assert a.name.startswith("exp_canary_") and b.name.startswith("exp_canary_")


def test_manifest_roundtrips_and_captures_environment(tmp_path) -> None:
    run_dir = make_run_dir(tmp_path, "ablate", "canary")
    bb = Blackboard(ablation_flags=AblationFlags(coverage_feedback=False))
    cfg = LLMConfig(provider="openai", model="gpt-5.4", base_url="https://x/v1")

    manifest = build_manifest(
        experiment="ablate",
        mechanism="canary",
        max_rounds=3,
        grpc_addr="127.0.0.1:50051",
        mcp_addr="http://127.0.0.1:50052/mcp",
        llm_config=cfg,
        blackboard=bb,
        disabled_agents=["minimizer"],
        checker_ids=["INV-SP-G01", "INV-SP-L01"],
    )
    write_manifest(run_dir, manifest)
    restored = read_manifest(run_dir)

    assert restored["thread_id"] == thread_id("ablate", "canary")
    assert restored["max_rounds"] == 3
    assert restored["llm"]["model"] == "gpt-5.4"
    assert restored["ablation_flags"]["coverage_feedback"] is False
    assert restored["disabled_agents"] == ["minimizer"]
    assert restored["checker_catalog"] == ["INV-SP-G01", "INV-SP-L01"]
    # Environment that a checkpoint alone cannot carry is present (keys exist).
    assert "git_sha" in restored
    assert "toolchains_yaml" in restored


def test_read_manifest_missing_is_empty(tmp_path) -> None:
    assert read_manifest(tmp_path) == {}
