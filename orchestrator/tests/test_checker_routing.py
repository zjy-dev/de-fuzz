"""T024 (US2): deterministic checker→ISA routing (SC-002/006/008).

routing.py expands the orchestrator-assigned enumeration target (current_target)
into a (checker, ISA) BuildMatrix using the SSOT metadata. Asserts:
- the Generator's contract output never carries a checker- or ISA-selection field
  (the agent no longer chooses what to attack; SC-002),
- all cheap checkers are always-on regardless of the assigned target (FR-017),
- a differential target runs ALL its applicable_isas with no pruning, and lands
  in forced_full (FR-016),
- not assigning an expensive checker only removes its cells; cheap coverage and the
  rest of the matrix are unaffected (superset principle, FR-018),
- ablation checker_routing=off falls back to the full checker × ISA product.
"""

from __future__ import annotations

from types import SimpleNamespace

from defuzz_loop.agents.generator import GeneratorOutput
from defuzz_loop.routing import CheckerCatalog, expand_matrix
from defuzz_loop.state import AblationFlags, Blackboard


def _meta(cid, isas, mode, cost, category="static"):
    return SimpleNamespace(
        id=cid, applicable_isas=isas, mode=mode, cost=cost, category=category
    )


class _StubClient:
    def __init__(self, metas) -> None:
        self._metas = metas

    def list_checker_metadata(self):
        return self._metas


# A small representative catalog: 2 cheap single, 1 expensive differential.
_METAS = [
    _meta("INV-SP-G01", ["x86_64", "aarch64"], "single", "cheap"),
    _meta("INV-SP-A01", ["x86_64"], "single", "cheap"),
    _meta("INV-SP-L01", ["x86_64", "aarch64", "riscv64"], "differential", "expensive"),
]


def _catalog() -> CheckerCatalog:
    return CheckerCatalog(_StubClient(_METAS))


def _bb(target: str | None, *, checker_routing: bool = True) -> Blackboard:
    # The orchestrator assigns one target via the enumeration cursor.
    queue = [target] if target is not None else []
    return Blackboard(
        target_queue=queue,
        target_idx=0,
        ablation_flags=AblationFlags(checker_routing=checker_routing),
    )


def test_generator_output_carries_no_target_or_isa() -> None:
    # SC-002: the Generator's structured contract chooses neither checker nor ISA.
    assert "isa" not in GeneratorOutput.model_fields
    assert "applicable_isas" not in GeneratorOutput.model_fields
    assert "selected_checkers" not in GeneratorOutput.model_fields
    out = GeneratorOutput(source="int main(void){return 0;}")
    assert out.source.startswith("int main")


def test_cheap_checkers_always_on() -> None:
    # FR-017: even with no expensive target assigned, both cheap checkers expand.
    matrix = expand_matrix(_catalog(), _bb(None))
    checkers = {c.checker_id for c in matrix.cells}
    assert "INV-SP-G01" in checkers
    assert "INV-SP-A01" in checkers
    # The expensive checker is NOT pulled in unless it is the assigned target.
    assert "INV-SP-L01" not in checkers


def test_differential_runs_all_isas_no_pruning() -> None:
    # FR-016: assign the differential checker → all 3 ISAs, marked forced_full.
    matrix = expand_matrix(_catalog(), _bb("INV-SP-L01"))
    l01_isas = {c.isa for c in matrix.cells if c.checker_id == "INV-SP-L01"}
    assert l01_isas == {"x86_64", "aarch64", "riscv64"}
    assert "INV-SP-L01" in matrix.forced_full


def test_missing_expensive_checker_preserves_cheap_coverage() -> None:
    # FR-018 superset principle: not assigning the expensive checker only removes
    # its cells; the cheap matrix is byte-identical.
    full = expand_matrix(_catalog(), _bb("INV-SP-L01"))
    dropped = expand_matrix(_catalog(), _bb(None))
    cheap_full = [(c.checker_id, c.isa) for c in full.cells if c.checker_id != "INV-SP-L01"]
    cheap_dropped = [(c.checker_id, c.isa) for c in dropped.cells]
    assert cheap_full == cheap_dropped
    assert "INV-SP-L01" not in dropped.forced_full


def test_ablation_off_runs_full_product() -> None:
    # checker_routing=off → control arm: every checker × every ISA.
    matrix = expand_matrix(_catalog(), _bb(None, checker_routing=False))
    pairs = {(c.checker_id, c.isa) for c in matrix.cells}
    assert pairs == {
        ("INV-SP-G01", "x86_64"),
        ("INV-SP-G01", "aarch64"),
        ("INV-SP-A01", "x86_64"),
        ("INV-SP-L01", "x86_64"),
        ("INV-SP-L01", "aarch64"),
        ("INV-SP-L01", "riscv64"),
    }


def test_cells_are_deterministically_sorted() -> None:
    # Reproducibility: same input → identical ordered cells.
    a = expand_matrix(_catalog(), _bb("INV-SP-L01"))
    b = expand_matrix(_catalog(), _bb("INV-SP-L01"))
    assert [(c.checker_id, c.isa) for c in a.cells] == [(c.checker_id, c.isa) for c in b.cells]
    assert a.cells == sorted(a.cells, key=lambda c: (c.checker_id, c.isa))
