from __future__ import annotations

import csv
from pathlib import Path

import pytest

from defuzz_callgraph import db
from defuzz_callgraph.server import create_server

ROWS = [
    ("root", "a", "direct", "a.c", 1),
    ("root", "b", "indirect", "b.c", 2),
    ("a", "c", "direct", "a.c", 3),
    ("b", "c", "direct", "b.c", 4),
    ("c", "root", "direct", "c.c", 5),
    ("c", "leaf", "direct", "c.c", 6),
]


def make_csv(path: Path, rows=ROWS) -> None:
    with path.open("w", newline="") as handle:
        writer = csv.writer(handle)
        writer.writerow(["caller_name", "callee_name", "edge_kind", "file", "line"])
        writer.writerows(rows)


def test_ingest_queries_and_reingest(tmp_path: Path) -> None:
    source = tmp_path / "tiny.csv"
    database = tmp_path / "graph.sqlite"
    make_csv(source)
    con = db.connect(database)
    assert db.ingest_target(con, "gcc-x86_64", source) == 6
    targets = db.list_targets(con)
    assert targets["counters"] == {"targets": 1, "edges": 6}
    assert db.find_function(con, "gcc-x86_64", "oo")["functions"] == ["root"]
    assert db.find_function(con, "missing", "root")["functions"] == []
    callees = db.traverse(con, "gcc-x86_64", "root", direction="callees", depth=2, limit=20)
    assert callees["counters"]["nodes"] == 4
    assert all({"file", "line", "edge_kind"} <= edge.keys() for edge in callees["edges"])
    callers = db.traverse(con, "gcc-x86_64", "c", direction="callers", depth=1, limit=20)
    assert {edge["caller"] for edge in callers["edges"]} == {"a", "b"}
    sliced = db.slice_from(con, "gcc-x86_64", "root", depth=3, max_nodes=4)
    assert sliced["truncated"] is True
    paths = db.paths_between(con, "gcc-x86_64", "root", "leaf", max_depth=4, max_paths=10)
    assert {tuple(path["nodes"]) for path in paths["paths"]} == {
        ("root", "a", "c", "leaf"),
        ("root", "b", "c", "leaf"),
    }
    make_csv(source, [("new", "only", "direct", "new.c", 9)])
    assert db.ingest_target(con, "gcc-x86_64", source) == 1
    assert db.list_targets(con)["counters"]["edges"] == 1
    assert db.find_function(con, "gcc-x86_64", "root")["functions"] == []
    con.close()


def test_limits_and_exact_tool_set(tmp_path: Path) -> None:
    source = tmp_path / "tiny.csv"
    database = tmp_path / "graph.sqlite"
    make_csv(source)
    con = db.connect(database)
    db.ingest_target(con, "t", source)
    with pytest.raises(ValueError, match="depth"):
        db.traverse(con, "t", "root", direction="callees", depth=6, limit=1)
    with pytest.raises(ValueError, match="limit"):
        db.find_function(con, "t", "root", 501)
    with pytest.raises(ValueError, match="max_depth"):
        db.paths_between(con, "t", "root", "leaf", max_depth=9)
    con.close()
    server = create_server(database)
    names = {tool.name for tool in server._tool_manager.list_tools()}
    assert names == {
        "list_targets",
        "find_function",
        "get_callees",
        "get_callers",
        "slice_from",
        "paths_between",
    }
