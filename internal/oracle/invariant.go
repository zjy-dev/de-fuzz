package oracle

import (
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// InvariantVerdict is the per-checker outcome of a single invariant assertion.
//
// The aggregator (`MechanismOracle`) collapses many `InvariantVerdict`s into a
// single `*Bug` for the existing `Oracle.Analyze` contract, but each
// invariant's individual result is preserved in `Bug.Description` so that
// research consumers can trace back to the source `docs/invariants/*.md` entry.
type InvariantVerdict int

const (
	// VerdictPass means the invariant held. Default zero value is intentionally
	// `Pass` so an unset / forgotten field never silently flips to Fail.
	VerdictPass InvariantVerdict = iota
	// VerdictFail means the invariant was violated; this is a bug candidate.
	VerdictFail
	// VerdictNotApplicable means the checker correctly skipped because its
	// preconditions were not met (e.g., binary path missing, wrong arch).
	// NA must NOT be reported as bug; aggregators may surface NA ratios as a quality signal.
	VerdictNotApplicable
	// VerdictError means the checker tried to run but encountered an
	// infrastructure error (executor failed, ELF parse failed, etc.). Treated
	// like NA for bug aggregation, but logged separately.
	VerdictError
)

// String returns a stable token for log / report output.
func (v InvariantVerdict) String() string {
	switch v {
	case VerdictPass:
		return "PASS"
	case VerdictFail:
		return "FAIL"
	case VerdictNotApplicable:
		return "N/A"
	case VerdictError:
		return "ERROR"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(v))
	}
}

// InvariantCategory classifies a checker by *what kind of evidence it
// inspects*, mirroring story_line.md §4 — every safety invariant is encoded
// either as a static property (assembly / binary feature) or a dynamic
// property (runtime behavior). The category also doubles as a scheduling
// phase inside `MechanismOracle`: cheap static checks run before expensive
// dynamic ones.
//
//   - CategoryStatic — pure binary inspection (symbols, sections, disasm).
//     No execution, safe to run unconditionally; cheap (ms scale).
//   - CategoryDynamic — requires running the binary (binary search, sentinel,
//     differential exec). Expensive; checkers in this phase share a
//     dynamic-result cache via `CheckContext.Cache` to avoid duplicating work.
//
// "Mechanism not active" is NOT a separate category; checkers whose
// pre-conditions are not met must return `VerdictNotApplicable` with a
// descriptive Reason (e.g. `StackChkSymbolsChecker` returns NA when the
// binary doesn't import `__stack_chk_fail`). The aggregator never treats
// NA as a bug, so configuration mismatches never produce false positives.
type InvariantCategory string

const (
	CategoryStatic  InvariantCategory = "static"
	CategoryDynamic InvariantCategory = "dynamic"
)

// InvariantResult is what a single `InvariantChecker` returns.
//
// The schema is deliberately close to the `oracle_mapping` rows in
// `docs/invariants/*.md` so reports can be auto-correlated.
type InvariantResult struct {
	// ID is the survey-anchored invariant ID, e.g. "INV-SP-L01".
	ID string
	// Category is the scheduling phase (static / dynamic).
	Category InvariantCategory
	// Verdict is the per-invariant outcome.
	Verdict InvariantVerdict
	// Evidence is a one-line human description of WHY this verdict was
	// produced, suitable for inclusion in a bug report.
	Evidence string
	// Detail is structured data (exit codes, sizes, symbol names) for
	// machine consumption.
	Detail map[string]any
	// Reason is a free-form explanation populated for NotApplicable / Error
	// verdicts. Empty for Pass / Fail (use Evidence instead).
	Reason string
}

// CheckContext is the per-Analyze input handed to every InvariantChecker.
//
// It carries the seed, binary path, executor, inspector, and a shared mutable
// Cache so that dynamic checkers can reuse the result of expensive operations
// (e.g., binary search) within a single Analyze call.
//
// The Cache is NOT cross-seed; each Analyze creates a fresh CheckContext.
type CheckContext struct {
	// Seed is the seed under analysis (may be nil in unit tests that only
	// exercise a checker directly).
	Seed *seed.Seed
	// BinaryPath is the path to the compiled binary (may be empty in tests).
	BinaryPath string
	// Executor runs the binary; may be nil if no dynamic checker is active.
	Executor Executor
	// Inspector is a cached binary inspector; may be nil if no static
	// checker is active. Lazy: file is opened on first method call.
	Inspector BinaryInspector
	// Cache is shared mutable storage for cross-checker memoization. Keys
	// should be namespaced (e.g., "dynamic_buffer_search.result"). Values
	// are typed assertions at the consumer's risk.
	Cache map[string]any
}

// CacheGet retrieves a value from the per-Analyze cache. Returns (nil, false)
// if absent. Convenience over manual map access so callers needn't nil-check
// the map.
func (c *CheckContext) CacheGet(key string) (any, bool) {
	if c == nil || c.Cache == nil {
		return nil, false
	}
	v, ok := c.Cache[key]
	return v, ok
}

// CacheSet stores a value in the per-Analyze cache, creating the map if
// needed. Idempotent.
func (c *CheckContext) CacheSet(key string, value any) {
	if c == nil {
		return
	}
	if c.Cache == nil {
		c.Cache = make(map[string]any)
	}
	c.Cache[key] = value
}

// InvariantChecker is the unit of oracle work corresponding to one row in
// `docs/invariants/*.md`.
//
// Every method must be safe to call repeatedly with the same context, since
// the aggregator may run a checker more than once. Implementations should put
// expensive state in CheckContext.Cache, not on the receiver.
type InvariantChecker interface {
	// ID returns the survey-anchored ID, e.g. "INV-SP-L01".
	ID() string
	// Category returns the scheduling phase.
	Category() InvariantCategory
	// Check executes the assertion and returns a structured result.
	// It must NOT panic on missing context fields; instead, return
	// VerdictNotApplicable with a Reason explaining the gap.
	Check(ctx *CheckContext) InvariantResult
}

// formatInvariantList produces a deterministic, multi-line summary for use in
// `Bug.Description`. Helper kept package-private so the wire format stays
// owned by the aggregator.
func formatInvariantList(results []InvariantResult, verdict InvariantVerdict) string {
	var ids []string
	for _, r := range results {
		if r.Verdict == verdict {
			ids = append(ids, r.ID)
		}
	}
	return strings.Join(ids, ", ")
}
