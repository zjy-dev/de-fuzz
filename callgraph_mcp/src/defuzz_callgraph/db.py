"""SQLite ingest and bounded graph queries."""

from __future__ import annotations

import csv
import sqlite3
from collections import deque
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

SCHEMA = """
CREATE TABLE IF NOT EXISTS edges (
 target TEXT NOT NULL, caller TEXT NOT NULL, callee TEXT NOT NULL,
 edge_kind TEXT NOT NULL, file TEXT NOT NULL, line INTEGER
);
CREATE TABLE IF NOT EXISTS metadata (
 target TEXT PRIMARY KEY, source_csv TEXT NOT NULL, edge_count INTEGER NOT NULL,
 ingested_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_edges_target_caller ON edges(target, caller);
CREATE INDEX IF NOT EXISTS idx_edges_target_callee ON edges(target, callee);
CREATE INDEX IF NOT EXISTS idx_edges_target ON edges(target);
"""


def connect(db: str | Path, *, read_only: bool = False) -> sqlite3.Connection:
    path = Path(db).expanduser().resolve()
    if read_only:
        con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    else:
        path.parent.mkdir(parents=True, exist_ok=True)
        con = sqlite3.connect(path)
    con.row_factory = sqlite3.Row
    return con


def initialise(con: sqlite3.Connection) -> None:
    con.executescript(SCHEMA)


def _value(row: dict[str, str], *names: str) -> str:
    for name in names:
        if row.get(name) is not None:
            return row[name]
    raise ValueError(f"CSV missing required column; expected one of {names}")


def ingest_target(con: sqlite3.Connection, target: str, csv_path: str | Path) -> int:
    path = Path(csv_path).expanduser().resolve()
    if not target.strip():
        raise ValueError("target must not be empty")
    initialise(con)
    count = 0
    batch = []
    try:
        con.execute("BEGIN IMMEDIATE")
        con.execute("DELETE FROM edges WHERE target = ?", (target,))
        with path.open(newline="", encoding="utf-8-sig") as handle:
            reader = csv.DictReader(handle)
            if reader.fieldnames is None:
                raise ValueError(f"CSV has no header: {path}")
            for row in reader:
                raw_line = (row.get("line") or "").strip()
                try:
                    line = int(raw_line) if raw_line else None
                except ValueError as exc:
                    raise ValueError(
                        f"invalid line {raw_line!r} at CSV row {reader.line_num}"
                    ) from exc
                batch.append(
                    (
                        target,
                        _value(row, "caller", "caller_name"),
                        _value(row, "callee", "callee_name"),
                        (row.get("edge_kind") or "unknown").strip() or "unknown",
                        (row.get("file") or "").strip(),
                        line,
                    )
                )
                if len(batch) >= 10000:
                    con.executemany("INSERT INTO edges VALUES (?, ?, ?, ?, ?, ?)", batch)
                    count += len(batch)
                    batch.clear()
            if batch:
                con.executemany("INSERT INTO edges VALUES (?, ?, ?, ?, ?, ?)", batch)
                count += len(batch)
        con.execute(
            "INSERT OR REPLACE INTO metadata VALUES (?, ?, ?, ?)",
            (target, str(path), count, datetime.now(timezone.utc).isoformat()),
        )
        con.commit()
    except Exception:
        con.rollback()
        raise
    return count


def _check(value: int, name: str, low: int, high: int) -> None:
    if not low <= value <= high:
        raise ValueError(f"{name} must be between {low} and {high}")


def list_targets(con: sqlite3.Connection) -> dict[str, Any]:
    rows = con.execute(
        "SELECT target, source_csv, edge_count, ingested_at FROM metadata ORDER BY target"
    ).fetchall()
    return {
        "query": {},
        "targets": [dict(r) for r in rows],
        "counters": {"targets": len(rows), "edges": sum(r["edge_count"] for r in rows)},
        "truncated": False,
    }


def find_function(
    con: sqlite3.Connection, target: str, pattern: str, limit: int = 100
) -> dict[str, Any]:
    _check(limit, "limit", 1, 500)
    sql = (
        "SELECT name FROM (SELECT caller AS name FROM edges WHERE target=? "
        "UNION SELECT callee AS name FROM edges WHERE target=?) "
        "WHERE instr(name, ?) > 0 ORDER BY name LIMIT ?"
    )
    rows = con.execute(sql, (target, target, pattern, limit + 1)).fetchall()
    names = [r["name"] for r in rows[:limit]]
    return {
        "query": {"target": target, "pattern": pattern, "limit": limit},
        "functions": names,
        "counters": {"matches_returned": len(names)},
        "truncated": len(rows) > limit,
    }


def _edge(row: sqlite3.Row) -> dict[str, Any]:
    return {k: row[k] for k in ("caller", "callee", "edge_kind", "file", "line")}


def traverse(
    con: sqlite3.Connection, target: str, function: str, *, direction: str, depth: int, limit: int
) -> dict[str, Any]:
    _check(depth, "depth", 1, 5)
    _check(limit, "limit", 1, 5000)
    source, dest = ("caller", "callee") if direction == "callees" else ("callee", "caller")
    queue = deque([(function, 0)])
    visited = {function}
    edges = []
    truncated = False
    expanded = 0
    while queue:
        node, level = queue.popleft()
        if level >= depth:
            continue
        expanded += 1
        rows = con.execute(
            f"SELECT caller,callee,edge_kind,file,line FROM edges WHERE target=? "
            f"AND {source}=? ORDER BY {dest},file,line",
            (target, node),
        )
        for row in rows:
            if len(edges) >= limit:
                truncated = True
                queue.clear()
                break
            item = _edge(row)
            item["depth"] = level + 1
            edges.append(item)
            nxt = row[dest]
            if nxt not in visited:
                visited.add(nxt)
                queue.append((nxt, level + 1))
    return {
        "query": {"target": target, "function": function, "depth": depth, "limit": limit},
        "direction": direction,
        "nodes": sorted(visited),
        "edges": edges,
        "counters": {"nodes": len(visited), "edges": len(edges), "expanded": expanded},
        "truncated": truncated,
    }


def slice_from(
    con: sqlite3.Connection, target: str, entry: str, depth: int = 3, max_nodes: int = 2000
) -> dict[str, Any]:
    _check(depth, "depth", 1, 5)
    _check(max_nodes, "max_nodes", 1, 2000)
    queue = deque([(entry, 0)])
    visited = {entry}
    edges = []
    truncated = False
    while queue:
        node, level = queue.popleft()
        if level >= depth:
            continue
        for row in con.execute(
            "SELECT caller,callee,edge_kind,file,line FROM edges WHERE target=? "
            "AND caller=? ORDER BY callee,file,line",
            (target, node),
        ):
            nxt = row["callee"]
            if nxt not in visited and len(visited) >= max_nodes:
                truncated = True
                continue
            item = _edge(row)
            item["depth"] = level + 1
            edges.append(item)
            if nxt not in visited:
                visited.add(nxt)
                queue.append((nxt, level + 1))
    return {
        "query": {"target": target, "entry": entry, "depth": depth, "max_nodes": max_nodes},
        "nodes": sorted(visited),
        "edges": edges,
        "counters": {"nodes": len(visited), "edges": len(edges)},
        "truncated": truncated,
    }


def _reverse_distances(
    con: sqlite3.Connection, target: str, dst: str, max_depth: int
) -> dict[str, int]:
    distances = {dst: 0}
    frontier = [dst]
    for level in range(1, max_depth + 1):
        new = []
        for node in frontier:
            for row in con.execute(
                "SELECT DISTINCT caller FROM edges WHERE target=? AND callee=?", (target, node)
            ):
                caller = row["caller"]
                if caller not in distances:
                    distances[caller] = level
                    new.append(caller)
        frontier = new
        if not frontier:
            break
    return distances


def paths_between(
    con: sqlite3.Connection,
    target: str,
    src: str,
    dst: str,
    max_depth: int = 6,
    max_paths: int = 50,
) -> dict[str, Any]:
    _check(max_depth, "max_depth", 1, 8)
    _check(max_paths, "max_paths", 1, 50)
    distances = _reverse_distances(con, target, dst, max_depth)
    queue = deque([(src, [src], [])])
    paths = []
    expanded = 0
    while queue and len(paths) < max_paths:
        node, nodes, path_edges = queue.popleft()
        used = len(path_edges)
        if node == dst:
            paths.append({"nodes": nodes, "edges": path_edges})
            continue
        if used >= max_depth:
            continue
        expanded += 1
        for row in con.execute(
            "SELECT caller,callee,edge_kind,file,line FROM edges WHERE target=? "
            "AND caller=? ORDER BY callee,file,line",
            (target, node),
        ):
            nxt = row["callee"]
            remaining = max_depth - used - 1
            if nxt in nodes or distances.get(nxt, max_depth + 1) > remaining:
                continue
            queue.append((nxt, [*nodes, nxt], [*path_edges, _edge(row)]))
    return {
        "query": {
            "target": target,
            "src": src,
            "dst": dst,
            "max_depth": max_depth,
            "max_paths": max_paths,
        },
        "algorithm": "bounded BFS pruned by reverse shortest-distance-to-dst",
        "paths": paths,
        "counters": {
            "paths": len(paths),
            "expanded": expanded,
            "reverse_reachable_nodes": len(distances),
        },
        "truncated": len(paths) >= max_paths and bool(queue),
    }
