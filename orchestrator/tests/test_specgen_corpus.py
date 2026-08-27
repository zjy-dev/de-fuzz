from __future__ import annotations

import hashlib
import json
from pathlib import Path
from typing import Any, cast

import pytest

from defuzz_loop.specgen import corpus as corpus_mod
from defuzz_loop.specgen.pipeline import PipelineConfig, render_candidate_md, run_pipeline
from defuzz_loop.specgen.retriever import EmbeddingRetriever
from defuzz_loop.specgen.schema import Candidate, Falsifiability


def _write(root: Path, relative: str, content: str) -> Path:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    return path


def _llvm_fixture(root: Path, body: str = "return Enabled;") -> None:
    _write(
        root,
        "llvm/lib/CodeGen/StackProtector.cpp",
        "bool StackProtector::requiresGuard() const {\n"
        f"  {body}\n"
        "}\n",
    )


def test_gcc_default_adapter_preserves_legacy_chunk_identity(tmp_path: Path) -> None:
    _write(
        tmp_path,
        "tree-object-size.cc",
        "int compute_object_size()\n{\n  return 1;\n}\n",
    )

    legacy = corpus_mod.build_corpus(
        tmp_path, cache_root=tmp_path / "cache", include_bugzilla=False
    )
    explicit = corpus_mod.build_corpus(
        tmp_path,
        cache_root=tmp_path / "cache",
        include_bugzilla=False,
        compiler="gcc",
    )

    assert [chunk.model_dump() for chunk in explicit] == [
        chunk.model_dump() for chunk in legacy
    ]
    assert explicit[0].chunk_id == "tree-object-size.cc:1:compute_object_size"
    assert explicit[0].metadata.compiler == "GCC"
    assert explicit[0].metadata.version == corpus_mod.GCC_VERSION
    assert PipelineConfig(
        seed_sources=[], gcc_root=tmp_path, include_bugzilla=False
    ).corpus_root == tmp_path


async def test_legacy_gcc_corpus_cache_is_validated_and_migrated(tmp_path: Path) -> None:
    _write(
        tmp_path,
        "tree-object-size.cc",
        "int compute_object_size()\n{\n  return 1;\n}\n",
    )
    cache = tmp_path / "cache"
    chunks = corpus_mod.build_corpus(
        tmp_path, cache_root=cache, include_bugzilla=False
    )
    corpus_mod.write_corpus(chunks, cache / "corpus.jsonl")
    config = PipelineConfig(
        seed_sources=[],
        gcc_root=tmp_path,
        out_dir=tmp_path / "out",
        cache_root=cache,
        include_bugzilla=False,
        reuse_corpus=True,
    )

    result = await run_pipeline(config, judge_override=cast(Any, object()))

    assert result.corpus_size == 1
    assert (cache / "corpus.meta.json").is_file()


def test_llvm_adapter_uses_explicit_whitelist_and_exact_metadata(tmp_path: Path) -> None:
    _llvm_fixture(tmp_path)
    _write(tmp_path, "unrelated.cpp", "int must_not_be_indexed() { return 0; }\n")

    chunks = corpus_mod.build_corpus(
        tmp_path,
        cache_root=tmp_path / "cache",
        compiler="llvm",
        version="llvm-main-test",
    )

    assert len(chunks) == 1
    chunk = chunks[0]
    assert chunk.chunk_id == "llvm/lib/CodeGen/StackProtector.cpp:1:lines-1-3"
    assert chunk.metadata.compiler == "LLVM"
    assert chunk.metadata.version == "llvm-main-test"
    assert chunk.metadata.path == "llvm/lib/CodeGen/StackProtector.cpp"
    assert chunk.metadata.line == 1
    assert chunk.metadata.symbol == ""
    assert "must_not_be_indexed" not in chunk.text


def test_llvm_adapter_never_fetches_gcc_bugzilla(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _llvm_fixture(tmp_path)

    def fail_fetch(*args: Any, **kwargs: Any) -> Any:
        raise AssertionError("LLVM corpus must not fetch GCC Bugzilla")

    monkeypatch.setattr(corpus_mod, "fetch_bugzilla", fail_fetch)
    assert corpus_mod.build_corpus(
        tmp_path, cache_root=tmp_path / "cache", compiler="llvm"
    )


async def test_pipeline_namespaces_cache_and_rejects_stale_reuse(tmp_path: Path) -> None:
    source = tmp_path / "llvm"
    _llvm_fixture(source)
    bugs = tmp_path / "bugs"
    bugs.mkdir()
    config = PipelineConfig(
        seed_sources=["bugs"],
        corpus_root=source,
        compiler="llvm",
        version="v1",
        bugs_root=bugs,
        out_dir=tmp_path / "out",
        cache_root=tmp_path / "cache",
        include_bugzilla=False,
        require_non_empty_corpus=True,
    )

    result = await run_pipeline(
        config, judge_override=cast(Any, object())
    )  # no seeds => no calls
    assert result.corpus_size == 1
    corpus = config.cache_root / "corpus-llvm.jsonl"
    metadata = config.cache_root / "corpus-llvm.meta.json"
    assert corpus.is_file() and metadata.is_file()
    assert not (config.cache_root / "corpus.jsonl").exists()
    assert json.loads(metadata.read_text(encoding="utf-8"))["identity"]["compiler"] == "llvm"

    reused = PipelineConfig(**{**config.__dict__, "reuse_corpus": True})
    assert (
        await run_pipeline(reused, judge_override=cast(Any, object()))
    ).corpus_size == 1

    _llvm_fixture(source, "return Disabled;")
    with pytest.raises(ValueError, match="cache identity mismatch"):
        await run_pipeline(reused, judge_override=cast(Any, object()))


async def test_pipeline_rejects_reused_corpus_with_foreign_metadata(
    tmp_path: Path,
) -> None:
    source = tmp_path / "llvm"
    _llvm_fixture(source)
    bugs = tmp_path / "bugs"
    bugs.mkdir()
    config = PipelineConfig(
        seed_sources=[],
        corpus_root=source,
        compiler="llvm",
        version="v1",
        bugs_root=bugs,
        out_dir=tmp_path / "out",
        cache_root=tmp_path / "cache",
        include_bugzilla=False,
        require_non_empty_corpus=True,
    )
    await run_pipeline(config, judge_override=cast(Any, object()))
    corpus_path = config.cache_root / "corpus-llvm.jsonl"
    payload = json.loads(corpus_path.read_text(encoding="utf-8").strip())
    payload["metadata"]["compiler"] = "GCC"
    corpus_path.write_text(json.dumps(payload) + "\n", encoding="utf-8")
    meta_path = config.cache_root / "corpus-llvm.meta.json"
    metadata = json.loads(meta_path.read_text(encoding="utf-8"))
    metadata["corpus_sha256"] = hashlib.sha256(corpus_path.read_bytes()).hexdigest()
    meta_path.write_text(json.dumps(metadata), encoding="utf-8")

    reused = PipelineConfig(**{**config.__dict__, "reuse_corpus": True})
    with pytest.raises(ValueError, match="foreign compiler metadata"):
        await run_pipeline(reused, judge_override=cast(Any, object()))


async def test_pipeline_fails_closed_before_judgment_on_empty_corpus(tmp_path: Path) -> None:
    config = PipelineConfig(
        seed_sources=[],
        corpus_root=tmp_path / "empty",
        compiler="llvm",
        out_dir=tmp_path / "out",
        cache_root=tmp_path / "cache",
        include_bugzilla=False,
        require_non_empty_corpus=True,
    )

    with pytest.raises(ValueError, match=r"empty llvm retrieval corpus.*empty"):
        await run_pipeline(config, judge_override=cast(Any, object()))


def test_renderer_does_not_apply_gcc_defaults_to_llvm_candidate() -> None:
    candidate = Candidate(
        seed_id="LLVM-1",
        origin_mechanism="bti",
        hit_mechanism="stack-protector",
        statement="A guard is checked.",
        observation="The guard check is visible.",
        falsifiability=Falsifiability(observability="inspect IR"),
    )

    rendered = render_candidate_md(
        candidate, 1, default_compiler="LLVM", default_version="llvm-main"
    )

    assert "- **compiler**: LLVM" in rendered
    assert "- **version**: llvm-main" in rendered
    assert "gcc-16.1.0" not in rendered


def test_embedding_cache_names_and_identity_are_compiler_scoped(tmp_path: Path) -> None:
    class Client:
        class Config:
            model = "fixture"
            dim = 1

        _cfg = Config()

    gcc = EmbeddingRetriever(Client(), cache_path=tmp_path / "embeddings.json")  # type: ignore[arg-type]
    llvm = EmbeddingRetriever(
        Client(),  # type: ignore[arg-type]
        cache_path=tmp_path / "embeddings-llvm.json",
        cache_identity="llvm-corpus-v1",
    )

    assert gcc._query_cache_path == tmp_path / "query_vectors.json"
    assert llvm._query_cache_path == tmp_path / "query_vectors-llvm.json"
    llvm._save_cache("same-fingerprint", [[1.0]])
    assert llvm._load_cache("same-fingerprint") == [[1.0]]
    mismatch = EmbeddingRetriever(
        Client(),  # type: ignore[arg-type]
        cache_path=tmp_path / "embeddings-llvm.json",
        cache_identity="llvm-corpus-v2",
    )
    assert mismatch._load_cache("same-fingerprint") is None
