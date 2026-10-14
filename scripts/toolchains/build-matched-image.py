#!/usr/bin/env python3
"""Build the CallGraph-aligned image from verified local compiler artifacts."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import tarfile
import tempfile
from datetime import UTC, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def sha256(path: Path) -> str:
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifacts", type=Path, default=ROOT / ".work/toolchains-matched")
    parser.add_argument("--base-image", default="localhost/defuzz-core:toolchain-bootstrap-20261012")
    parser.add_argument("--llvm-source-archive", type=Path, default=Path("/tmp/llvm-project-23.1.2.src.tar.xz"))
    parser.add_argument("--tag", default="localhost/defuzz-toolchains-matched:16.2.0-23.1.2")
    args = parser.parse_args()
    lock = json.loads((ROOT / "configs/toolchains.matched.lock.json").read_text())
    artifacts = args.artifacts.resolve()
    base = json.loads(subprocess.check_output(
        ["podman", "image", "inspect", args.base_image], text=True,
    ))[0]
    manifest = {"schema_version": 1, "lock": lock, "base_image_id": base["Id"], "files": {}}
    manifest["built_at"] = datetime.now(UTC).isoformat()
    # Only explicit build artifacts are staged; no experiment inputs/findings.
    inputs = {
        "gcc-x86_64": artifacts / "gcc-x86_64-install",
        "gcc-aarch64": artifacts / "gcc-aarch64-install",
        "llvm": artifacts / "llvm-install",
    }
    for isa in ("x86_64", "aarch64"):
        driver = inputs[f"gcc-{isa}"] / f"opt/gcc16/bin/{isa}-linux-gnu-gcc"
        if not driver.is_file():
            raise SystemExit(f"Missing completed GCC16 build: {driver}")
    anchors = json.loads((ROOT / "callgraph_mcp/data/provenance.json").read_text())["targets"]
    for target, data in anchors.items():
        source = artifacts / (data["compiler"] + "-source")
        for relative, expected in data["source_files_sha256"].items():
            if sha256(source / relative) != expected:
                raise SystemExit(f"Source identity mismatch: {target}/{relative}")
    for target in ("x86_64", "aarch64"):
        manifest[f"gcc_{target}_configure"] = (
            artifacts / f"gcc-{target}-build/config.status"
        ).read_text().splitlines()[0:20]
    manifest["native_build_image"] = subprocess.check_output([
        "podman", "image", "inspect", "localhost/defuzz-gcc-build-arm64:20261012",
        "--format", "{{.Id}}",
    ], text=True).strip()
    with tempfile.TemporaryDirectory(prefix="defuzz-matched-image-") as temporary:
        context = Path(temporary)
        for compiler, archive in (
            ("gcc", artifacts / "gcc-16.2.0.tar.xz"),
            ("llvm", args.llvm_source_archive),
        ):
            if sha256(archive) != lock[compiler]["source_sha256"]:
                raise SystemExit(f"{compiler} source archive checksum mismatch")
            destination = context / f"sources-{compiler}.tar.xz"
            shutil.copyfile(archive, destination)
            manifest["files"][destination.name] = sha256(destination)
        for relative, source in inputs.items():
            if not source.is_dir():
                raise SystemExit(f"Missing artifact directory: {source}")
            archive = context / (relative.replace("/", "-") + ".tar")
            print(f"Archiving {relative}", flush=True)
            subprocess.run(
                ["tar", "-cf", str(archive), "-C", str(source), "."], check=True,
                env={**os.environ, "COPYFILE_DISABLE": "1"},
            )
            with tarfile.open(archive) as bundle:
                for member in bundle:
                    if member.isfile():
                        with bundle.extractfile(member) as stream:
                            manifest["files"][relative + "/" + member.name.removeprefix("./")] = (
                                hashlib.file_digest(stream, "sha256").hexdigest()
                            )
            manifest["files"][archive.name] = sha256(archive)
        shutil.copy2(ROOT / "scripts/toolchains/clang-aarch64.sh", context)
        manifest["files"]["clang-aarch64.sh"] = sha256(context / "clang-aarch64.sh")
        (context / "versions.json").write_text(json.dumps(manifest, indent=2) + "\n")
        subprocess.run([
            "podman", "build", "--platform=linux/amd64", "--pull=false",
            "--build-arg", f"BASE_IMAGE={base['Id']}",
            "-f", str(ROOT / "docker/Containerfile.toolchains-matched"),
            "-t", args.tag, str(context),
        ], check=True)
    image = json.loads(subprocess.check_output(
        ["podman", "image", "inspect", args.tag], text=True,
    ))[0]
    manifest["image_id"] = image["Id"]
    (artifacts / "image-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps({"tag": args.tag, "image_id": image["Id"]}))


if __name__ == "__main__":
    main()
