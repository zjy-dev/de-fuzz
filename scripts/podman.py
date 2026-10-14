#!/usr/bin/env python3
"""Build and operate the local rootless DeFuzz Pod; requires Python 3.10+."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
STATE = ROOT / ".work" / "podman"
RUNTIME = "local/defuzz-gcc17-four-isa:20260531-qemu10.2"
RUNTIME_DIGESTS = {
    # Same config/rootfs; OCI exporter representation and normalized Docker archive.
    "sha256:3030327836aa7f15978a67b634912bd5aea38f35104fbbc1de0631033d2bd4d3",
    "sha256:ecf5edcde1853a85b4bf1d34115900f02fe275daeaa17fb6e665d08e25072e58",
}
GCC_REVISION = "f20bc4c2fe00928013c533e241b89ae3a6724ca1"
IMAGES = {
    "runtime": RUNTIME,
    "go": "public.ecr.aws/docker/library/golang:1.26.3-bookworm",
    "python": "public.ecr.aws/docker/library/python:3.12.14-slim-bookworm",
    "uv": "ghcr.io/astral-sh/uv:0.11.14",
    "node": "public.ecr.aws/docker/library/node:24.15.0-bookworm-slim",
}
CORE = "localhost/defuzz-core:dev"
ORCHESTRATOR = "localhost/defuzz-orchestrator:dev"
MATCHED_TOOLCHAINS = "localhost/defuzz-toolchains-matched:16.2.0-23.1.2"


def run(*args: str, capture: bool = False, **kwargs):
    return subprocess.run(args, check=True, text=True, capture_output=capture, **kwargs)


def inspect(engine: str, image: str) -> dict:
    return json.loads(run(engine, "image", "inspect", image, capture=True).stdout)[0]


def require_rootless() -> None:
    value = run("podman", "info", "--format", "{{.Host.Security.Rootless}}", capture=True)
    if value.stdout.strip() != "true":
        raise SystemExit(
            "Use a rootless Podman connection (macOS: podman machine set --rootful=false)."
        )


def stage_context(destination: Path, pypi_mirror: str = "") -> dict[str, str]:
    # Allowlist: never send .git, local credentials, data, findings or run outputs.
    patterns = [
        "core/**/*.go", "core/go.mod", "core/go.sum",
        "orchestrator/defuzz_loop/**/*.py", "orchestrator/configs/llm.yaml",
        "orchestrator/configs/embedding.yaml",
        "orchestrator/pyproject.toml", "orchestrator/uv.lock",
        "callgraph_mcp/src/**/*.py", "callgraph_mcp/pyproject.toml",
        "callgraph_mcp/uv.lock", "callgraph_mcp/README.md",
        "agents/definitions/*.md", "agents/policies/*.md",
        "agent-runtime/package.json", "agent-runtime/package-lock.json",
        "agent-runtime/tsconfig.json", "agent-runtime/src/*.ts",
        "configs/agents/*.example.json",
        "configs/toolchains.container.yaml", "configs/toolchains.matched.container.yaml",
        "configs/toolchains.matched.lock.json", "configs/experiments/*.example.yaml",
        "configs/experiments/example.yaml", "scripts/run_callgraph_mcp.sh",
        "docker/Containerfile.*", "docker/smoke.py", "docker/pi-smoke.py",
        "docker/replay.py", "docker/toolchain-smoke.py",
    ]
    hashes = {}
    for pattern in patterns:
        for source in sorted(ROOT.glob(pattern)):
            if source.is_symlink():
                raise SystemExit(f"Refusing symlink in build context: {source}")
            if not source.is_file():
                continue
            relative = source.relative_to(ROOT)
            target = destination / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, target)
            if source.name == "uv.lock" and pypi_mirror:
                # Preserve every pinned version and hash; change only the wheel/sdist CDN.
                target.write_text(target.read_text().replace(
                    "https://files.pythonhosted.org/", pypi_mirror.rstrip("/") + "/",
                ))
            hashes[str(relative)] = hashlib.sha256(target.read_bytes()).hexdigest()
    return hashes


def build(args) -> None:
    engine = args.engine
    if engine == "podman":
        require_rootless()
    resolved = {}
    for name, tag in {**IMAGES, "runtime": args.runtime_image}.items():
        if name != "runtime":
            run(engine, "pull", "--platform=linux/amd64", tag)
        info = inspect(engine, tag)
        if info.get("Architecture") != "amd64" or info.get("Os") != "linux":
            raise SystemExit(f"{tag}: requires linux/amd64")
        resolved[name] = {
            "tag": tag, "id": info["Id"], "repo_digests": info.get("RepoDigests", []),
            "digest": info.get("Digest") or info.get("Descriptor", {}).get("digest"),
        }
    runtime = inspect(engine, args.runtime_image)
    matched = inspect(engine, args.matched_toolchains_image)
    matched_labels = matched.get("Config", {}).get("Labels", {}) or {}
    if any(
        matched_labels.get(key) != value for key, value in {
            "org.defuzz.gcc.version": "16.2.0",
            "org.defuzz.gcc.revision": "78d4ac73dd391005b895a6148cd9831e28e1208b",
            "org.defuzz.llvm.version": "23.1.2",
            "org.defuzz.llvm.revision": "85ac560262434c9ccfc0c183ec22d4138ed647fb",
        }.items()
    ):
        raise SystemExit("Build the pinned matched toolchains image before application images")
    labels = runtime.get("Config", {}).get("Labels", {}) or {}
    if labels.get("org.opencontainers.image.revision") != GCC_REVISION:
        raise SystemExit("Runtime must use the frozen GCC17 revision; see container runbook.")
    accepted_digests = {args.runtime_digest} if args.runtime_digest else RUNTIME_DIGESTS
    if resolved["runtime"]["digest"] not in accepted_digests:
        raise SystemExit("Runtime differs from --runtime-digest; verify its provenance first.")

    STATE.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="defuzz-context-") as temporary:
        context = Path(temporary)
        files = stage_context(context, args.pypi_mirror)
        git = run("git", "-C", str(ROOT), "rev-parse", "HEAD", capture=True).stdout.strip()
        provenance = {
            "git_commit": git, "files_sha256": files, "base_images": resolved,
            "platform": "linux/amd64", "gcc_revision": GCC_REVISION,
            "matched_toolchains": {
                "image": args.matched_toolchains_image, "id": matched["Id"],
                "labels": matched_labels,
            },
            "dependency_mirrors": {
                "go_proxy": args.go_proxy, "debian": args.debian_mirror,
                "pypi_files": args.pypi_mirror,
            },
        }
        (context / "docker" / "build-provenance.json").write_text(json.dumps(provenance, indent=2))
        # Podman/Buildah accept local image IDs in FROM. Docker also resolves
        # the IDs locally, avoiding an implicit pull of a mutable runtime tag.
        run(engine, "build", "--platform=linux/amd64", "--pull=false",
            "--build-arg", f"GOPROXY={args.go_proxy}",
            "--build-arg", f"DEBIAN_MIRROR={args.debian_mirror}",
            "--build-arg", f"RUNTIME_IMAGE={resolved['runtime']['id']}",
            "--build-arg", f"MATCHED_TOOLCHAINS_IMAGE={matched['Id']}",
            "--build-arg", f"GO_IMAGE={resolved['go']['id']}",
            "-f", str(context / "docker/Containerfile.core"), "-t", CORE, str(context))
        core_id = inspect(engine, CORE)["Id"]
        run(engine, "build", "--platform=linux/amd64", "--pull=false",
            "--build-arg", f"GOPROXY={args.go_proxy}",
            "--build-arg", f"CORE_IMAGE={core_id}",
            "--build-arg", f"GO_IMAGE={resolved['go']['id']}",
            "--build-arg", f"PYTHON_IMAGE={resolved['python']['id']}",
            "--build-arg", f"UV_IMAGE={resolved['uv']['id']}",
            "--build-arg", f"NODE_IMAGE={resolved['node']['id']}",
            "-f", str(context / "docker/Containerfile.orchestrator"),
            "-t", ORCHESTRATOR, str(context))
        provenance["images"] = {
            "core": core_id, "orchestrator": inspect(engine, ORCHESTRATOR)["Id"],
        }
        (STATE / "images.json").write_text(json.dumps(provenance, indent=2) + "\n")
    print(f"Image provenance: {STATE / 'images.json'}")


def up(args) -> None:
    require_rootless()
    if args.mechanism != "canary" and args.toolchains is None:
        raise SystemExit(
            "The default flags target canary; select --toolchains for other mechanisms."
        )
    toolchains = Path(args.toolchains or ROOT / "configs/toolchains.matched.container.yaml").resolve()
    if not toolchains.is_file():
        raise SystemExit(f"Missing toolchain config: {toolchains}")
    STATE.mkdir(parents=True, exist_ok=True)
    manifest = STATE / "pod.yaml"
    if run("podman", "pod", "ps", "--filter", "name=^defuzz$", "--format", "{{.Name}}",
           capture=True).stdout.strip():
        raise SystemExit("The defuzz Pod already exists; use down before replacing it.")
    if run("podman", "volume", "ls", "--filter", "name=^defuzz-work$",
           "--format", "{{.Name}}", capture=True).stdout.strip():
        raise SystemExit("The defuzz-work volume already exists; inspect its owner before reuse.")
    paths = {}
    for key, value in [("RUNS_DIR", args.runs), ("CALLGRAPH_DIR", args.callgraph),
                       ("INPUTS_DIR", args.inputs)]:
        path = Path(value).expanduser().resolve()
        if key == "CALLGRAPH_DIR" and not (path / "callgraph.sqlite").is_file():
            raise SystemExit(f"Missing {path / 'callgraph.sqlite'}; see callgraph_mcp/README.md")
        path.mkdir(parents=True, exist_ok=True)
        paths[key] = str(path)
    core_id = inspect("podman", CORE)["Id"]
    orchestrator_id = inspect("podman", ORCHESTRATOR)["Id"]
    text = (ROOT / "docker/pod.yaml.in").read_text()
    values = {**paths, "TOOLCHAINS_FILE": str(toolchains),
              "CORE_IMAGE": core_id, "ORCHESTRATOR_IMAGE": orchestrator_id,
              "MECHANISM_ARG": f"--mechanism={args.mechanism}"}
    for key, value in values.items():
        text = text.replace("{{" + key + "}}", json.dumps(value))
    manifest.write_text(text)
    # The macOS remote client does not expose --build; local IDs plus
    # imagePullPolicy: Never make the prebuilt-image requirement explicit.
    run("podman", "kube", "play", str(manifest))
    if (STATE / "images.json").exists():
        shutil.copy2(STATE / "images.json", Path(paths["RUNS_DIR"]) / "container-images.json")
    print("Ready: python3 scripts/podman.py smoke")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    command = commands.add_parser("build", help="build Linux images from an allowlisted context")
    command.add_argument("--engine", choices=["podman", "docker"], default="podman")
    command.add_argument("--runtime-image", default=RUNTIME)
    command.add_argument("--matched-toolchains-image", default=MATCHED_TOOLCHAINS)
    command.add_argument("--runtime-digest", help="override the accepted frozen runtime digests")
    command.add_argument("--go-proxy", default="https://goproxy.cn,https://proxy.golang.org,direct")
    command.add_argument("--debian-mirror", default="https://mirrors.tuna.tsinghua.edu.cn")
    command.add_argument("--pypi-mirror", default="https://pypi.tuna.tsinghua.edu.cn")
    command = commands.add_parser("up", help="start the rootless native Pod")
    command.add_argument("--runs", default=str(ROOT / "runs"))
    command.add_argument("--callgraph", default=str(ROOT / "callgraph_mcp/data"))
    command.add_argument("--inputs", default=str(STATE / "inputs"))
    command.add_argument("--toolchains", help="host path to a container-native toolchain YAML")
    command.add_argument("--mechanism", choices=["canary", "ibt", "fortify"], default="canary")
    commands.add_parser("down", help="remove this Pod and its temporary volume; keep runs")
    commands.add_parser("status")
    commands.add_parser("smoke", help="configured-ISA gRPC/QEMU/storage/MCP check, without model calls")
    command = commands.add_parser("exec", help="run a command in the orchestrator")
    command.add_argument("--env", action="append", default=[], help="forward a named host variable")
    command.add_argument("argv", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.command == "build":
        build(args)
    elif args.command == "up":
        up(args)
    elif args.command == "down":
        require_rootless()
        # Podman 6.1 remote --force can report the shared PVC twice and fail
        # decoding its teardown response. Remove the Pod, then its one volume.
        run("podman", "kube", "down", str(STATE / "pod.yaml"))
        if run("podman", "volume", "ls", "--filter", "name=^defuzz-work$",
               "--format", "{{.Name}}", capture=True).stdout.strip():
            run("podman", "volume", "rm", "defuzz-work")
    elif args.command == "status":
        run("podman", "pod", "ps", "--filter", "name=^defuzz$")
        run("podman", "ps", "-a", "--filter", "pod=defuzz")
    elif args.command == "smoke":
        run("podman", "exec", "defuzz-orchestrator", "python", "/workspace/docker/smoke.py")
    elif args.command == "exec":
        argv = args.argv[1:] if args.argv[:1] == ["--"] else args.argv
        if not argv:
            parser.error("exec requires a command after --")
        environment = []
        for name in args.env:
            if not name.isidentifier() or name not in os.environ:
                parser.error(f"--env requires an existing host environment variable name: {name}")
            environment += ["--env", name]
        run("podman", "exec", *environment, "defuzz-orchestrator", *argv)


if __name__ == "__main__":
    main()
