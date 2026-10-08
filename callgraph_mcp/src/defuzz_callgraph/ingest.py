"""CSV to SQLite command line entry point."""

from __future__ import annotations

import argparse
import json
from collections.abc import Sequence
from pathlib import Path

from .db import connect, ingest_target, initialise, list_targets


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Ingest callgraph CSV files into SQLite")
    parser.add_argument("--out", type=Path, required=True, help="output SQLite database")
    parser.add_argument(
        "--add",
        nargs=2,
        action="append",
        default=[],
        metavar=("TARGET", "CSV"),
        required=True,
        help="atomically replace TARGET from CSV; may be repeated",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    con = connect(args.out)
    try:
        initialise(con)
        for target, csv_path in args.add:
            count = ingest_target(con, target, csv_path)
            print(json.dumps({"target": target, "csv": str(csv_path), "edge_count": count}))
        print(json.dumps({"database": str(args.out), "metadata": list_targets(con)}))
    finally:
        con.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
