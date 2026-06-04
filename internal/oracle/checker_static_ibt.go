package oracle

import (
	"bytes"
	"debug/elf"
	"fmt"
	"sort"
)

// UnintendedEndbrChecker asserts INV-IBT-B01:
//
//	"Under -fcf-protection=branch / full, no byte sequence equivalent to
//	ENDBR32 (0xfb 0x1e 0x0f 0xf3) or ENDBR64 (0xfa 0x1e 0x0f 0xf3) may
//	appear inside a function body except as the deliberate prologue
//	emitted at the function entry. The check must cover all 8 possible
//	1-byte alignment shifts of the 4-byte sequence within an immediate /
//	displacement — implemented here as an exhaustive byte-by-byte scan
//	over every executable section."
//
// See `docs/tech-docs/invariants/endbr-ibt.md` §INV-IBT-B01 and
// `findings/DREV-2026-004/` for a concrete bypass produced by GCC's
// defective `ix86_endbr_immediate_operand` predicate when a 64-bit
// immediate has the ENDBR pattern at a non-lowest byte offset *and*
// non-zero bytes above it.
//
// Algorithm (purely static, no execution):
//
//  1. Architecture gate: ELF e_machine ∈ {EM_386, EM_X86_64} and
//     EI_CLASS picks the matching ENDBR pattern (endbr32 vs endbr64).
//     Any other arch → VerdictNotApplicable("non-x86 binary").
//  2. Build the function map from STT_FUNC symbols with non-zero size.
//     No function symbols → NotApplicable (stripped binary, can't make
//     a precise call without per-function ranges).
//  3. For every executable (`SHF_EXECINSTR`) section, scan its bytes for
//     occurrences of the 4-byte ENDBR pattern. For each hit at address
//     `A`:
//     - If `A == fn.Addr` for some function `fn` → legitimate prologue.
//     - If `A ∈ (fn.Addr, fn.Addr + fn.Size)` and is *not* the entry →
//     this is an unintended landing pad → record as a violation.
//     - If `A` falls in an inter-function gap (no enclosing function) →
//     skip; padding bytes between functions are common and not a bug.
//  4. ≥1 violation → VerdictFail (mechanism violated). Otherwise Pass.
//
// Polarity: this checker is `polarity_sensitive: true`. Under
// PolarityInverted (e.g., a seed compiled with `-fcf-protection=none`),
// any "unintended ENDBR" is meaningless and the aggregator collapses
// Fail → Pass via `applyPolarity`.
//
// False-positive caveat: setjmp returns, EH landing pads, and
// computed-goto targets are *also* legitimate ENDBR positions but are
// not at any STT_FUNC's `st_value`. The current trigger set
// (DREV-2026-004 and similar `movabs` immediate-leak shapes) does not
// invoke any of those, so we accept this as a known limitation. A
// future companion checker can use `.eh_frame` / DWARF to extend the
// whitelist.
type UnintendedEndbrChecker struct{}

// ID implements InvariantChecker.
func (c *UnintendedEndbrChecker) ID() string { return "INV-IBT-B01" }

// Category implements InvariantChecker.
func (c *UnintendedEndbrChecker) Category() InvariantCategory { return CategoryStatic }

// MaxReportedViolations caps the size of `Detail["violations"]` so a
// pathological binary doesn't blow up the bug description.
const MaxReportedViolations = 8

// endbr64Pattern / endbr32Pattern are the 4-byte little-endian encodings.
// See Intel SDM Vol.2 entries for `ENDBR64` / `ENDBR32`.
var (
	endbr64Pattern = []byte{0xF3, 0x0F, 0x1E, 0xFA}
	endbr32Pattern = []byte{0xF3, 0x0F, 0x1E, 0xFB}
)

// Check implements InvariantChecker.
func (c *UnintendedEndbrChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryStatic,
		SourceURL:   "https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html",
		Sensitivity: "likely-to-drift",
		Detail: map[string]any{
			"polarity_sensitive": true,
		},
	}

	if ctx == nil || ctx.Inspector == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no inspector available (missing binary path)"
		return r
	}

	machine, err := ctx.Inspector.Machine()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.Machine failed: %v", err)
		return r
	}
	class, err := ctx.Inspector.Class()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.Class failed: %v", err)
		return r
	}

	pattern, ok := selectEndbrPattern(machine, class)
	if !ok {
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf("non-x86 binary (machine=%v, class=%v); IBT/ENDBR not applicable", machine, class)
		r.Detail["machine"] = machine.String()
		r.Detail["class"] = class.String()
		return r
	}
	r.Detail["machine"] = machine.String()
	r.Detail["class"] = class.String()

	funcs, err := ctx.Inspector.FunctionSymbols()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.FunctionSymbols failed: %v", err)
		return r
	}
	if len(funcs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "binary has no STT_FUNC symbols with non-zero size; cannot bound function bodies"
		return r
	}
	// FunctionSymbols already returns sorted; ensure for binary search.
	sort.Slice(funcs, func(i, j int) bool { return funcs[i].Addr < funcs[j].Addr })
	entrySet := make(map[uint64]struct{}, len(funcs))
	for _, fn := range funcs {
		entrySet[fn.Addr] = struct{}{}
	}

	execs, err := ctx.Inspector.ExecutableSections()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.ExecutableSections failed: %v", err)
		return r
	}
	if len(execs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "binary has no executable sections (SHF_EXECINSTR)"
		return r
	}

	type violation struct {
		Func    string `json:"func"`
		Addr    uint64 `json:"addr"`
		Offset  uint64 `json:"offset_in_func"`
		Section string `json:"section"`
	}
	var violations []violation
	totalEndbr := 0
	totalAtEntry := 0
	totalInGap := 0

	for _, sec := range execs {
		// Each candidate match starts at `sec.Addr + i` for i in [0,
		// len(sec.Data)-len(pattern)]. We use bytes.Index repeatedly to
		// step through all occurrences, which is O(N) thanks to the
		// stdlib's optimised search.
		//
		// NOTE: this is a *whole-section sweep* for every ENDBR
		// occurrence, deliberately distinct from IsEndbrAt (x86dasm.go),
		// which tests a single known offset. They are not interchangeable.
		buf := sec.Data
		base := sec.Addr
		off := 0
		for {
			idx := bytes.Index(buf[off:], pattern)
			if idx < 0 {
				break
			}
			absoluteOff := off + idx
			addr := base + uint64(absoluteOff)
			totalEndbr++

			if _, isEntry := entrySet[addr]; isEntry {
				totalAtEntry++
			} else if fn, inside := findEnclosingFunction(funcs, addr); inside {
				violations = append(violations, violation{
					Func:    fn.Name,
					Addr:    addr,
					Offset:  addr - fn.Addr,
					Section: sec.Name,
				})
			} else {
				// In a gap between functions, treat as padding.
				totalInGap++
			}

			// Advance by one byte: ENDBR patterns can theoretically
			// overlap (they cannot overlap each other byte-for-byte
			// because the pattern is fixed, but using +1 is the safe
			// cheapest scan).
			off = absoluteOff + 1
		}
	}

	r.Detail["endbr_total"] = totalEndbr
	r.Detail["endbr_at_function_entry"] = totalAtEntry
	r.Detail["endbr_in_inter_function_gap"] = totalInGap
	r.Detail["endbr_unintended_in_function_body"] = len(violations)

	if len(violations) == 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("scanned %d ENDBR occurrence(s); all are at deliberate function entries (%d) or in inter-function padding (%d)",
			totalEndbr, totalAtEntry, totalInGap)
		return r
	}

	// Cap reported violations.
	reported := violations
	if len(reported) > MaxReportedViolations {
		reported = reported[:MaxReportedViolations]
	}
	formatted := make([]string, 0, len(reported))
	for _, v := range reported {
		formatted = append(formatted, fmt.Sprintf("%s+0x%x@0x%x[%s]", v.Func, v.Offset, v.Addr, v.Section))
	}
	r.Detail["violations"] = formatted

	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf("found %d unintended ENDBR opcode(s) inside function bodies: %s%s",
		len(violations),
		formatted[0],
		moreSuffix(len(violations), len(reported)))
	return r
}

// selectEndbrPattern returns the canonical ENDBR encoding for the target
// (machine, class) pair, plus a bool indicating x86 applicability.
func selectEndbrPattern(machine elf.Machine, class elf.Class) ([]byte, bool) {
	switch machine {
	case elf.EM_X86_64:
		// On x86_64 the deliberate prologue is endbr64. We do NOT scan
		// for endbr32 here because the assembler will not legitimately
		// emit it on x86_64 targets.
		return endbr64Pattern, true
	case elf.EM_386:
		return endbr32Pattern, true
	default:
		_ = class // kept for future arch refinements
		return nil, false
	}
}

// findEnclosingFunction returns the function whose [Addr, Addr+Size)
// range contains addr, plus a bool indicating whether such a function
// was found. The funcs slice MUST be sorted by Addr ascending.
func findEnclosingFunction(funcs []FunctionSymbol, addr uint64) (FunctionSymbol, bool) {
	// Binary search for the rightmost function whose Addr <= addr.
	lo, hi := 0, len(funcs)
	for lo < hi {
		mid := (lo + hi) / 2
		if funcs[mid].Addr <= addr {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return FunctionSymbol{}, false
	}
	candidate := funcs[lo-1]
	if addr >= candidate.Addr && addr < candidate.Addr+candidate.Size {
		return candidate, true
	}
	return FunctionSymbol{}, false
}

// moreSuffix renders "(+N more)" when violations were truncated, or
// empty otherwise.
func moreSuffix(total, shown int) string {
	if total <= shown {
		return ""
	}
	return fmt.Sprintf(" (+%d more)", total-shown)
}
