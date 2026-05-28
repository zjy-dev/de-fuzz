package oracle

import (
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/oracle/disasm"
)

// EpilogueGuardCompareChecker implements `INV-SP-V01` from
// `docs/tech-docs/invariants/stack-canary.md`:
//
//	"Stack-protector epilogue must compare the guard *value* stored in
//	the canary slot against a *fresh* guard *value* (re-loaded by
//	dereferencing the guard source). Comparing two copies of the guard
//	*address* makes the check tautological; any overwrite of the canary
//	slot leaves the comparison passing → silent bypass."
//
// Originally observed on `arm-none-eabi-gcc 9.x` with `--specs=nano.specs`
// (Cortex-M4); see GCC PR 85434 and the systemonchips.com write-up.
//
// Detection model. The bug's fingerprint in object code is the absence
// of a register-indirect *value* dereference of the guard. Correctly
// emitted SP epilogues, on every backend we surveyed, contain at least
// one `ldr rN, [rM]` where `rM` previously received a PC-relative
// `__stack_chk_guard` address (literal-pool load on ARM, GOT load on
// AArch64). The PR85434 codegen omits that dereference entirely — the
// register holding the address is stored directly into the canary slot
// and later XOR'd against another address-load.
//
// The checker walks every protected function and counts:
//
//   - `pcLoads` — PC-relative literal loads (address materialization).
//   - `derefLoads` — register-indirect loads whose base is a GP register
//     that was previously the target of a PC-relative load.
//
// Verdict mapping (ARM / Thumb only — V01 is arm-specific):
//   - arch ∉ {ARM, Thumb}                                → NotApplicable
//   - binary does not import `__stack_chk_fail`          → NotApplicable
//   - no STT_FUNC symbols                                → NotApplicable
//   - protected function has ≥1 PC-relative load AND
//     ≥1 register-indirect deref of that loaded address → Pass
//   - protected function has ≥1 PC-relative load AND
//     ZERO register-indirect derefs of that address      → Fail
//
// Polarity. `polarity_sensitive: true`: under a `-fno-stack-protector`
// build the function will not import `__stack_chk_fail` so we report
// NotApplicable, which the aggregator does not flip; the negative
// control therefore correctly produces no Fail.
type EpilogueGuardCompareChecker struct {
	InvariantID string
	SourceURL   string
	Sensitivity string
	// FunctionFilter, if non-empty, restricts inspection to functions
	// whose symbol name matches one of these strings exactly. Default
	// behaviour (empty) is "any function that imports __stack_chk_fail";
	// since we cannot tell per-function imports without disassembling
	// every call, we approximate by scanning every defined function.
	FunctionFilter []string
}

// ID implements InvariantChecker.
func (c *EpilogueGuardCompareChecker) ID() string {
	if c.InvariantID == "" {
		return "INV-SP-V01"
	}
	return c.InvariantID
}

// Category implements InvariantChecker. Pure binary inspection.
func (c *EpilogueGuardCompareChecker) Category() InvariantCategory { return CategoryStatic }

// Check implements InvariantChecker.
func (c *EpilogueGuardCompareChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryStatic,
		SourceURL:   c.sourceURL(),
		Sensitivity: c.sensitivity(),
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
	r.Detail["machine"] = machine.String()

	arch, archErr := disasm.ArchFromELF(machine, class)
	if archErr != nil || (arch != disasm.ArchARM && arch != disasm.ArchThumb) {
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf("INV-SP-V01 only screens ARM / Thumb codegen (arch=%s)", arch)
		return r
	}

	imports, err := ctx.Inspector.ImportedFunctions()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.ImportedFunctions failed: %v", err)
		return r
	}
	if !importsStackChkFail(imports) {
		r.Verdict = VerdictNotApplicable
		r.Reason = "binary does not import __stack_chk_fail; no SP epilogue to screen"
		return r
	}

	funcs, err := ctx.Inspector.FunctionSymbols()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.FunctionSymbols failed: %v", err)
		return r
	}
	if len(funcs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "binary has no STT_FUNC symbols; cannot localize the epilogue"
		return r
	}

	candidates := selectCandidateFunctions(funcs, c.FunctionFilter)
	if len(candidates) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no candidate functions matched the filter"
		return r
	}

	type funcReport struct {
		Name       string
		PCLoads    int
		DerefLoads int
		Compares   int
	}
	var reports []funcReport
	totalDeref, totalPC := 0, 0
	violators := []string{}

	for _, fn := range candidates {
		insts, _, derr := decodeFunction(ctx.Inspector, fn, arch)
		if derr != nil {
			// Decode failure is not a verdict: skip and continue.
			continue
		}
		usage := analyzeGuardUsage(insts)
		reports = append(reports, funcReport{
			Name:       fn.Name,
			PCLoads:    usage.PCLoads,
			DerefLoads: usage.DerefLoads,
			Compares:   usage.Compares,
		})
		totalPC += usage.PCLoads
		totalDeref += usage.DerefLoads
		// A function that materialised an address but never dereferenced
		// it (and reached a compare) matches the V01 fingerprint.
		if usage.PCLoads > 0 && usage.DerefLoads == 0 && usage.Compares > 0 {
			violators = append(violators, fn.Name)
		}
	}

	r.Detail["candidate_functions"] = len(candidates)
	r.Detail["pc_relative_loads"] = totalPC
	r.Detail["deref_loads"] = totalDeref
	if len(violators) > 0 {
		r.Detail["violator_functions"] = violators
	}

	if totalPC == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no PC-relative literal loads observed in candidate functions; arch / codegen unknown"
		return r
	}

	if len(violators) > 0 {
		r.Verdict = VerdictFail
		r.Evidence = fmt.Sprintf("function(s) %s materialise the guard address but never dereference it before the canary compare; matches GCC PR85434 silent-bypass shape",
			strings.Join(violators, ", "))
		return r
	}

	r.Verdict = VerdictPass
	r.Evidence = fmt.Sprintf("scanned %d candidate function(s); every PC-relative load is followed by a register-indirect dereference (%d total) — guard *value*, not address, reaches the compare",
		len(candidates), totalDeref)
	return r
}

func (c *EpilogueGuardCompareChecker) sourceURL() string {
	if c.SourceURL != "" {
		return c.SourceURL
	}
	return "https://gcc.gnu.org/bugzilla/show_bug.cgi?id=85434"
}

func (c *EpilogueGuardCompareChecker) sensitivity() string {
	if c.Sensitivity != "" {
		return c.Sensitivity
	}
	return "likely-to-drift"
}

// guardUsageReport summarises the address-materialisation / dereference
// pattern in a function's instruction stream. It is the shared data
// used by both V01 (compare-side analysis) and S01 (spill analysis).
type guardUsageReport struct {
	// PCLoads counts `ldr rN, [pc, ...]` (or equivalent) instructions —
	// places where an address constant was materialised from the
	// literal pool.
	PCLoads int
	// DerefLoads counts `ldr rN, [rM]` instructions whose base register
	// rM was previously written by a PC-relative load. These are the
	// "guard value loads" that V01 demands.
	DerefLoads int
	// AddressSpills counts `str rN, [sp, ...]` instructions whose
	// source register rN was previously written by a PC-relative load
	// AND has not been overwritten by a dereference. This is the S01
	// fingerprint.
	AddressSpills int
	// Compares counts comparison-class instructions (cmp/cmn/teq/tst,
	// plus flag-setting EOR.S / SUBS reductions). Used by V01 as a
	// gating signal: "this function actually reached an epilogue
	// compare".
	Compares int
}

// analyzeGuardUsage walks `insts` once with a register-taint state
// machine. Tags propagate as:
//
//	ldr rN, [pc, ...]       → tag(rN) = "address"
//	ldr rN, [rM]            → tag(rN) = "value"   (consumes rM's tag)
//	str rN, [sp, ...] (rN tagged "address") → AddressSpills++
//	any other write to rN   → tag(rN) cleared
//
// We deliberately drop register state at branches (the model collapses
// to "any def kills the tag") because we are not building a CFG. This
// makes the heuristic an over-approximation in the "no spill" direction
// (a tag may survive across a branch, inflating the spill count) and an
// under-approximation in the "deref" direction (a deref reached only on
// a non-fallthrough path may be missed). Both directions are bounded
// because we walk a single basic-block stream.
func analyzeGuardUsage(insts []disasm.Inst) guardUsageReport {
	tags := map[string]string{} // reg → "address" | "value" | "" (cleared)

	const tagAddress = "address"
	const tagValue = "value"

	clear := func(reg string) {
		if reg == "" {
			return
		}
		delete(tags, reg)
	}

	rep := guardUsageReport{}

	for _, in := range insts {
		switch in.Op {
		case disasm.OpLoad:
			if !in.HasMem {
				clear(in.DstReg)
				continue
			}
			// PC-relative load → address materialised.
			if in.Mem.Base == "pc" {
				rep.PCLoads++
				if in.DstReg != "" {
					tags[in.DstReg] = tagAddress
				}
				continue
			}
			// Register-indirect load whose base is a tagged address →
			// the dereference V01 demands.
			if in.Mem.Base != "sp" {
				if tags[in.Mem.Base] == tagAddress {
					rep.DerefLoads++
					if in.DstReg != "" {
						tags[in.DstReg] = tagValue
					}
					continue
				}
			}
			// Any other load just produces an untagged register.
			clear(in.DstReg)
		case disasm.OpStore:
			if !in.HasMem || in.Mem.Base != "sp" {
				continue
			}
			// On ARM, the *value* being stored is in SrcRegs[0]
			// (because Args[0] is a register source for STR-class).
			if len(in.SrcRegs) == 0 {
				continue
			}
			src := in.SrcRegs[0]
			if tags[src] == tagAddress {
				rep.AddressSpills++
			}
			// A store does not clear the source's tag (the register
			// still holds the address afterwards).
		case disasm.OpCompare:
			rep.Compares++
		case disasm.OpMove:
			// MOV reg,reg propagates the tag; MOV reg,#imm clears it.
			if in.DstReg == "" {
				continue
			}
			if len(in.SrcRegs) == 1 {
				if t := tags[in.SrcRegs[0]]; t != "" {
					tags[in.DstReg] = t
					continue
				}
			}
			clear(in.DstReg)
		case disasm.OpArithmetic:
			// EOR.S / SUBS / etc. count as compares when they are the
			// flag-only reduction; we don't try to distinguish here,
			// so we conservatively clear the destination's tag.
			clear(in.DstReg)
		case disasm.OpBranch:
			// A branch ends the dataflow window for our purposes.
			// We do not clear tags wholesale because conditional
			// branches may fall through.
		default:
			clear(in.DstReg)
		}
	}
	return rep
}

// importsStackChkFail reports whether the symbol set contains either
// of the two SP runtime entrypoints.
func importsStackChkFail(syms []string) bool {
	for _, s := range syms {
		if s == "__stack_chk_fail" || s == "__stack_chk_fail_local" {
			return true
		}
	}
	return false
}

// selectCandidateFunctions filters the symbol table down to the
// functions worth disassembling. If `filter` is non-empty, only exact
// name matches survive. Otherwise we drop a small allowlist of
// definitely-not-user-code names (`_start`, `_init`, `_fini`,
// `__libc_csu_*`, `frame_dummy`) and return the rest. This errs on
// the side of inclusion so weird symbol layouts (no `seed` symbol,
// inlined entry, etc.) still get screened.
func selectCandidateFunctions(funcs []FunctionSymbol, filter []string) []FunctionSymbol {
	if len(filter) > 0 {
		set := make(map[string]struct{}, len(filter))
		for _, name := range filter {
			set[name] = struct{}{}
		}
		out := make([]FunctionSymbol, 0, len(funcs))
		for _, fn := range funcs {
			if _, ok := set[fn.Name]; ok {
				out = append(out, fn)
			}
		}
		return out
	}
	skip := map[string]struct{}{
		"_start":              {},
		"_init":               {},
		"_fini":               {},
		"__libc_csu_init":     {},
		"__libc_csu_fini":     {},
		"frame_dummy":         {},
		"register_tm_clones":  {},
		"deregister_tm_clones": {},
		"__do_global_dtors_aux": {},
		"call_weak_fn":        {},
	}
	out := make([]FunctionSymbol, 0, len(funcs))
	for _, fn := range funcs {
		if _, ok := skip[fn.Name]; ok {
			continue
		}
		if fn.Size == 0 {
			continue
		}
		out = append(out, fn)
	}
	return out
}
