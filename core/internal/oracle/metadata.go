package oracle

import "sort"

// CheckerMetadata is the single source of truth (SSOT) for per-checker
// routing attributes (research R4, data-model §CheckerMetadata).
//
// It is read by two faces of the Go core:
//   - gRPC CheckerMetadataService.ListCheckerMetadata (deterministic nodes)
//   - the MCP query_invariants tool (agent ReAct)
//
// Both read THIS table; the Python side holds a read-only copy only.
//
// Routing contract (data-model §rules):
//   - Cost == CostCheap     → always-on, not part of Generator's decision (FR-017).
//   - Cost == CostExpensive → Generator routes it under the superset principle (FR-018).
//   - Mode == ModeDifferential → once selected, ALL ApplicableISAs run, no pruning;
//     the cross-ISA comparison IS the bug signal (FR-016).
//   - Mode == ModeSingle → the checker is a per-backend codegen invariant; each
//     ApplicableISA is an independent target (no cross-ISA diff).
//
// ApplicableISAs are grounded in docs/tech-docs/invariants/*.md "target" lines.
// Unbuildable toolchains do not need pruning here: a missing cross-gcc yields an
// error cell at build time (R8), never a crash.
type CheckerMetadata struct {
	ID             string
	ApplicableISAs []string
	Mode           CheckerMode
	Cost           CheckerCost
	Category       InvariantCategory
}

// CheckerMode is the single vs. cross-ISA differential dimension (FR-014/016).
type CheckerMode string

const (
	ModeSingle       CheckerMode = "single"
	ModeDifferential CheckerMode = "differential"
)

// CheckerCost decides whether a checker is always-on or agent-routed (FR-017/018).
type CheckerCost string

const (
	CostCheap     CheckerCost = "cheap"
	CostExpensive CheckerCost = "expensive"
)

// Canonical ISA identifiers, aligned with internal/compiler target arch names
// (e.g. "x86_64", "aarch64", "riscv64").
const (
	ISAx8664       = "x86_64"
	ISAi386        = "i386"
	ISAaarch64     = "aarch64"
	ISAarm         = "arm" // aarch32 / thumb
	ISAriscv64     = "riscv64"
	ISAloongarch64 = "loongarch64"
	ISAmips64      = "mips64"
	ISAmips        = "mips"
	ISAxtensa      = "xtensa"
	ISAcsky        = "csky"
)

// stackDownISAs is the generic "stack grows downward" family used by the
// architecture-agnostic stack-canary invariants (stack-canary.md INV-SP-L01:
// "generic (x86_64, aarch64 已验证; 其他栈下行 ISA 同理)").
var stackDownISAs = []string{ISAx8664, ISAaarch64, ISAriscv64, ISAloongarch64}

// x86ISAs is the i386/x86_64 family used by Intel CET-IBT invariants
// (endbr-ibt.md target lines: "i386, x86_64").
var x86ISAs = []string{ISAi386, ISAx8664}

// fortifyISAs: _FORTIFY_SOURCE object-size checking is libc-level and
// architecture-generic; assigned the broad target family.
var fortifyISAs = []string{ISAx8664, ISAaarch64, ISAriscv64}

// checkerMetadata is the authoritative table, keyed by checker ID. Every ID
// here MUST be registered by a mechanism() in this package; conversely every
// registered checker MUST appear here (enforced by metadata_test.go).
var checkerMetadata = map[string]CheckerMetadata{
	// ── Stack canary (docs/tech-docs/invariants/stack-canary.md) ──────────

	// INV-SP-G01: __stack_chk_* symbol presence. Cheap symbol scan, generic.
	"INV-SP-G01": {ID: "INV-SP-G01", ApplicableISAs: stackDownISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	// INV-SP-A01: main must not carry a canary. Cheap symbol/disasm, generic.
	"INV-SP-A01": {ID: "INV-SP-A01", ApplicableISAs: stackDownISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	// INV-SP-H01: VLA/alloca → binary must import __stack_chk_fail. Source-vs-binary, generic.
	"INV-SP-H01": {ID: "INV-SP-H01", ApplicableISAs: stackDownISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	// INV-SP-V01: epilogue compares guard value not address (GCC PR85434, ARM/Thumb, Cortex-M GCC 9.3).
	"INV-SP-V01": {ID: "INV-SP-V01", ApplicableISAs: []string{ISAarm, ISAaarch64}, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	// INV-SP-S01: guard must not spill to attacker-rewritable slot (GCC PR85434: arm/aarch32 PIC, aarch64, x86_64).
	"INV-SP-S01": {ID: "INV-SP-S01", ApplicableISAs: []string{ISAarm, ISAaarch64, ISAx8664}, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	// INV-SP-L01: dynamic buffer-search bypass. Binary search via QEMU — the one truly expensive checker.
	// Differential: CVE-2023-4039 manifests as a cross-ISA layout divergence (aarch64 vs others).
	"INV-SP-L01": {ID: "INV-SP-L01", ApplicableISAs: stackDownISAs, Mode: ModeDifferential, Cost: CostExpensive, Category: CategoryDynamic},
	// INV-SP-S02: epilogue must scrub registers holding the guard (GCC PR96191 generic-fallback backends).
	// Differential across the long-tail fallback backends.
	"INV-SP-S02": {ID: "INV-SP-S02", ApplicableISAs: []string{ISAloongarch64, ISAriscv64, ISAmips64, ISAmips, ISAxtensa, ISAcsky}, Mode: ModeDifferential, Cost: CostExpensive, Category: CategoryDynamic},
	// INV-SP-V02: __stack_chk_fail must be noreturn. Reads L01's cache — no extra exec, cheap.
	"INV-SP-V02": {ID: "INV-SP-V02", ApplicableISAs: stackDownISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryDynamic},
	// INV-SP-L02: VLA/alloca below canary (CVE-2023-4039, aarch64). Cache reader, cheap.
	"INV-SP-L02": {ID: "INV-SP-L02", ApplicableISAs: []string{ISAaarch64, ISAx8664}, Mode: ModeDifferential, Cost: CostCheap, Category: CategoryDynamic},
	// INV-SP-L03: mixed vulnerable objects share one protection plane. Cache reader, generic, cheap.
	"INV-SP-L03": {ID: "INV-SP-L03", ApplicableISAs: stackDownISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryDynamic},
	// INV-SP-L04: protector slot must not relocate above locals (CERT VU#129209, LLVM Arm). Cache reader, cheap.
	"INV-SP-L04": {ID: "INV-SP-L04", ApplicableISAs: []string{ISAarm, ISAaarch64}, Mode: ModeSingle, Cost: CostCheap, Category: CategoryDynamic},

	// ── Intel CET-IBT (docs/tech-docs/invariants/endbr-ibt.md) ────────────
	// All IBT invariants are x86-only (i386, x86_64), static ENDBR scans, cheap, single.

	"INV-IBT-B01": {ID: "INV-IBT-B01", ApplicableISAs: x86ISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-IBT-P01": {ID: "INV-IBT-P01", ApplicableISAs: x86ISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-IBT-P02": {ID: "INV-IBT-P02", ApplicableISAs: x86ISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-IBT-P03": {ID: "INV-IBT-P03", ApplicableISAs: x86ISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-IBT-P04": {ID: "INV-IBT-P04", ApplicableISAs: x86ISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-IBT-P05": {ID: "INV-IBT-P05", ApplicableISAs: x86ISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-IBT-P06": {ID: "INV-IBT-P06", ApplicableISAs: x86ISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-IBT-N01": {ID: "INV-IBT-N01", ApplicableISAs: x86ISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-IBT-N02": {ID: "INV-IBT-N02", ApplicableISAs: x86ISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},

	// ── _FORTIFY_SOURCE (docs/tech-docs/invariants/fortify-source.md) ─────
	// libc-level object-size checking, architecture-generic, single mode.

	// Static, symbol-level (cheapest):
	"INV-FORT-W01": {ID: "INV-FORT-W01", ApplicableISAs: fortifyISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-FORT-C02": {ID: "INV-FORT-C02", ApplicableISAs: fortifyISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	// Static, disasm-based:
	"INV-FORT-O01": {ID: "INV-FORT-O01", ApplicableISAs: fortifyISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-FORT-O02": {ID: "INV-FORT-O02", ApplicableISAs: fortifyISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	"INV-FORT-O03": {ID: "INV-FORT-O03", ApplicableISAs: fortifyISAs, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic},
	// Dynamic (execute the binary): expensive, agent-routed.
	"INV-FORT-R01": {ID: "INV-FORT-R01", ApplicableISAs: fortifyISAs, Mode: ModeSingle, Cost: CostExpensive, Category: CategoryDynamic},
	"INV-FORT-R02": {ID: "INV-FORT-R02", ApplicableISAs: fortifyISAs, Mode: ModeSingle, Cost: CostExpensive, Category: CategoryDynamic},
	"INV-FORT-C01": {ID: "INV-FORT-C01", ApplicableISAs: fortifyISAs, Mode: ModeSingle, Cost: CostExpensive, Category: CategoryDynamic},
}

// AllCheckerMetadata returns every checker's metadata in deterministic
// (ID-sorted) order. Consumed by CheckerMetadataService and query_invariants.
func AllCheckerMetadata() []CheckerMetadata {
	ids := make([]string, 0, len(checkerMetadata))
	for id := range checkerMetadata {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]CheckerMetadata, 0, len(ids))
	for _, id := range ids {
		out = append(out, checkerMetadata[id])
	}
	return out
}

// LookupCheckerMetadata returns the metadata for a checker ID, if registered.
func LookupCheckerMetadata(id string) (CheckerMetadata, bool) {
	m, ok := checkerMetadata[id]
	return m, ok
}
