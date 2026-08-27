from __future__ import annotations

import os
import subprocess
import sys
from contextlib import asynccontextmanager
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest

from defuzz_loop.clients import mcp_client


@asynccontextmanager
async def _streamable_client(url: str) -> Any:
    del url
    yield object(), object(), object()


@pytest.mark.parametrize("alias", ["legacy", "modern"])
def test_mcp_client_import_supports_both_streamable_http_aliases(
    alias: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    fake_module = (
        SimpleNamespace(streamable_http_client=_streamable_client)
        if alias == "modern"
        else SimpleNamespace(streamablehttp_client=_streamable_client)
    )

    def _fake_import_module(name: str) -> Any:
        assert name == "mcp.client.streamable_http"
        return fake_module

    monkeypatch.setattr(mcp_client.importlib, "import_module", _fake_import_module)

    assert mcp_client._streamable_http_client() is _streamable_client
    assert mcp_client.ClientSession is not None
    assert mcp_client.MCPClient("http://127.0.0.1:50052/mcp")._url.endswith("/mcp")


def _write_fake_mcp(root: Path, *, alias: str) -> Path:
    pkg_root = root / "fake_mcp"
    mcp_dir = pkg_root / "mcp"
    client_dir = mcp_dir / "client"
    client_dir.mkdir(parents=True)

    (mcp_dir / "__init__.py").write_text(
        """
class ClientSession:
    async def __aenter__(self):
        return self

    async def __aexit__(self, exc_type, exc, tb):
        return None

    async def initialize(self):
        return None
""".lstrip(),
        encoding="utf-8",
    )
    (client_dir / "__init__.py").write_text("", encoding="utf-8")

    attr_name = (
        "streamable_http_client" if alias == "modern" else "streamablehttp_client"
    )
    (client_dir / "streamable_http.py").write_text(
        f"""
from contextlib import asynccontextmanager


@asynccontextmanager
async def {attr_name}(url):
    del url
    yield object(), object(), lambda: None
""".lstrip(),
        encoding="utf-8",
    )
    return pkg_root


@pytest.mark.parametrize("alias", ["legacy", "modern"])
@pytest.mark.parametrize(
    ("module_name", "argv0", "main_call"),
    [
        ("defuzz_loop.cli", "defuzz-loop", "module.main()"),
        ("defuzz_loop.experiments_cli", "defuzz-experiment", "module.main(['--help'])"),
    ],
)
def test_console_help_survives_fake_mcp_import_surface(
    alias: str,
    module_name: str,
    argv0: str,
    main_call: str,
    tmp_path: Path,
) -> None:
    fake_mcp_root = _write_fake_mcp(tmp_path, alias=alias)
    orchestrator_root = Path(__file__).resolve().parents[1]
    env = os.environ.copy()
    env["PYTHONPATH"] = os.pathsep.join(
        [str(fake_mcp_root), str(orchestrator_root), env.get("PYTHONPATH", "")]
    ).rstrip(os.pathsep)

    completed = subprocess.run(
        [
            sys.executable,
            "-c",
            (
                "import importlib, sys; "
                f"sys.argv = ['{argv0}', '--help']; "
                f"module = importlib.import_module('{module_name}'); "
                f"{main_call}"
            ),
        ],
        text=True,
        capture_output=True,
        check=False,
        env=env,
        cwd=orchestrator_root,
    )

    assert completed.returncode == 0, completed.stderr
    assert "usage:" in completed.stdout
