#!/usr/bin/env python3
"""Stream `docker save` as a Docker archive for Podman, preserving all layers.

Docker's containerd exporter can include an OCI index whose manifest mixes
Docker layer media types with an OCI manifest. Podman can run that import but
Buildah rejects it as a base. Retaining manifest.json and removing only the OCI
entry points makes Podman use the valid Docker archive representation instead.
Usage: docker save IMAGE | python3 scripts/podman_import.py | podman load
"""

import sys
import tarfile

with tarfile.open(fileobj=sys.stdin.buffer, mode="r|") as source:
    with tarfile.open(fileobj=sys.stdout.buffer, mode="w|") as destination:
        for member in source:
            if member.name.removeprefix("./") in {"index.json", "oci-layout"}:
                continue
            content = source.extractfile(member) if member.isfile() else None
            destination.addfile(member, content)
