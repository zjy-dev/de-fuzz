"""Checker→ISA routing: deterministic BuildMatrix expansion (not an agent).

Consumes the SSOT CheckerMetadata pulled over gRPC (same table the agent's
query_invariants reads). Rules (data-model §rules, blackboard-schema §routing):

1. selected = current_seed.selected_checkers ∪ all cheap checkers (always-on, FR-017).
2. For each selected checker, cartesian-expand its applicable_isas into cells.
3. mode=differential checkers go into forced_full; their ISAs are never pruned (FR-016).
4. ablation_flags.checker_routing=False → ignore agent selection, run ALL checkers ×
   ALL ISAs (the control arm, FR-010 / SC-004).

Superset principle (FR-018): missing an expensive checker only changes whether it
runs; cheap checkers stay on and their own NotApplicable verdict guards correctness.
"""

from __future__ import annotations

from .clients.grpc_client import CoreClient
from .state import Blackboard, BuildCell, BuildMatrix


class CheckerCatalog:
    """Read-only in-memory copy of the SSOT metadata, fetched once at startup."""

    def __init__(self, client: CoreClient) -> None:
        self._by_id: dict[str, dict] = {}
        for m in client.list_checker_metadata():
            self._by_id[m.id] = {
                "applicable_isas": list(m.applicable_isas),
                "mode": m.mode,
                "cost": m.cost,
                "category": m.category,
            }

    @property
    def all_ids(self) -> list[str]:
        return sorted(self._by_id)

    def cheap_ids(self) -> list[str]:
        return sorted(cid for cid, m in self._by_id.items() if m["cost"] == "cheap")

    def get(self, checker_id: str) -> dict | None:
        return self._by_id.get(checker_id)


def expand_matrix(catalog: CheckerCatalog, bb: Blackboard) -> BuildMatrix:
    if not bb.ablation_flags.checker_routing:
        selected = catalog.all_ids  # control arm: full checker × ISA product
    else:
        seed_checkers = bb.current_seed.selected_checkers if bb.current_seed else []
        selected = sorted(set(seed_checkers) | set(catalog.cheap_ids()))

    cells: list[BuildCell] = []
    forced_full: set[str] = set()
    seen: set[tuple[str, str]] = set()

    for checker_id in selected:
        meta = catalog.get(checker_id)
        if meta is None:
            continue
        if meta["mode"] == "differential":
            forced_full.add(checker_id)
        for isa in meta["applicable_isas"]:
            key = (checker_id, isa)
            if key in seen:
                continue
            seen.add(key)
            cells.append(BuildCell(checker_id=checker_id, isa=isa))

    cells.sort(key=lambda c: (c.checker_id, c.isa))
    return BuildMatrix(cells=cells, forced_full=forced_full)


def make_routing_node(catalog: CheckerCatalog):
    def routing_node(bb: Blackboard) -> dict:
        return {"build_matrix": expand_matrix(catalog, bb)}

    return routing_node
