"""Rebuild the bundled callgraph SQLite database from the bundled real CSVs.

The CSVs under ``callgraph_mcp/data/csv`` are the real, CodeQL-extracted GCC and
LLVM pass call graphs. This script re-ingests them into
``callgraph_mcp/data/callgraph.sqlite`` so the database can be regenerated
deterministically from the committed CSVs without any external dependency.
"""

from __future__ import annotations

import json
from pathlib import Path

# target name -> bundled CSV file name
TARGETS = {
    "gcc-16.2.0-x86_64": "gcc-16.2.0-x86_64.csv",
    "gcc-16.2.0-aarch64": "gcc-16.2.0-aarch64.csv",
    "llvm-23.1.2": "llvm-23.1.2.csv",
}


def main() -> None:
    root = Path(__file__).resolve().parent / "data"
    csv_dir = root / "csv"
    db_path = root / "callgraph.sqlite"

    from defuzz_callgraph.db import connect, ingest_target, initialise, list_targets

    con = connect(db_path)
    manifest = json.loads((root / "provenance.json").read_text())
    initialise(con)
    for target, csv_name in TARGETS.items():
        csv_path = csv_dir / csv_name
        if not csv_path.is_file():
            raise FileNotFoundError(f"missing bundled CSV for {target}: {csv_path}")
        count = ingest_target(
            con, target, csv_path, provenance=manifest["targets"][target],
        )
        print(f"Ingested {target}: {count} edges from {csv_path.name}")

    targets = list_targets(con)
    print("Targets in SQLite DB:", targets["targets"])
    print("Total edges:", targets["counters"]["edges"])
    con.close()


if __name__ == "__main__":
    main()
