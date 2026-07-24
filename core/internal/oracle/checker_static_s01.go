package oracle

import (
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/oracle/disasm"
)

// GuardSpillChecker implements `INV-SP-S01` from
// `docs/tech-docs/invariants/stack-canary.md`:
//
//	"The backend must not spill an intermediate register holding either
//	the *value* of `__stack_chk_guard` or its *address* to a stack slot
//	that lives in the same frame as vulnerable objects. If the attacker
//	can overwrite the spilled copy, they can either feed a forged
//	guard value directly into the compare or redirect the dereference
//	to attacker-controlled memory."
//
// Originally observed on ARM/AArch32 PIC builds (GCC PR 85434
// scheduling discussion) and addressed in LLVM by D64759, which
// rewrites the protector frame access to lock onto a frame index
// instead of a virtual register.
//
// Detection model. We reuse the register-taint analyser shared with
// V01 (`analyzeGuardUsage`). Its `AddressSpills` count already records
// `STR rN, [SP, ...]` instructions whose source rN is currently
// tagged as a `__stack_chk_guard` address (because rN was just the
// destination of a `LDR rN, [PC, ...]` literal-pool load).
//
// Verdict mapping (ARM / Thumb only — S01 is arm-specific in the
// disclosed cases; AArch64 / x86_64 spill paths are tracked but not
// known to violate without an explicit reproducer):
//   - arch ∉ {ARM, Thumb}                          → NotApplicable
//   - binary does not import __stack_chk_fail      → NotApplicable
//   - no STT_FUNC symbols                          → NotApplicable
//   - no PC-relative loads observed                → NotApplicable
//   - any candidate function spills a tagged       → Fail
//     address register to an SP-relative slot
//   - otherwise                                    → Pass
type GuardSpillChecker struct {
	InvariantID string
	// FunctionFilter mirrors V01: empty → scan everything except
	// boilerplate; non-empty → only listed names.
	FunctionFilter []string
}

// ID implements InvariantChecker.
func (c *GuardSpillChecker) ID() string {
	if c.InvariantID == "" {
		return "INV-SP-S01"
	}
	return c.InvariantID
}

// Category implements InvariantChecker. Pure binary inspection.
func (c *GuardSpillChecker) Category() InvariantCategory { return CategoryStatic }

// Check implements InvariantChecker.
func (c *GuardSpillChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:       c.ID(),
		Category: CategoryStatic,
		Detail:   map[string]any{},
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
		r.Reason = fmt.Sprintf("INV-SP-S01 only screens ARM / Thumb codegen today (arch=%s)", arch)
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
		r.Reason = "binary does not import __stack_chk_fail; no SP runtime to screen"
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
		r.Reason = "binary has no STT_FUNC symbols; cannot localize the SP code"
		return r
	}

	candidates := selectCandidateFunctions(funcs, c.FunctionFilter)
	if len(candidates) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no candidate functions matched the filter"
		return r
	}

	totalPC, totalSpills := 0, 0
	violators := []string{}

	for _, fn := range candidates {
		insts, _, derr := decodeFunction(ctx.Inspector, fn, arch)
		if derr != nil {
			continue
		}
		usage := analyzeGuardUsage(insts)
		totalPC += usage.PCLoads
		totalSpills += usage.AddressSpills
		if usage.AddressSpills > 0 {
			violators = append(violators, fn.Name)
		}
	}

	r.Detail["candidate_functions"] = len(candidates)
	r.Detail["pc_relative_loads"] = totalPC
	r.Detail["address_spills"] = totalSpills
	if len(violators) > 0 {
		r.Detail["violator_functions"] = violators
	}

	if totalPC == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no PC-relative literal loads observed in candidate functions; nothing to spill"
		return r
	}

	if totalSpills > 0 {
		r.Verdict = VerdictFail
		r.Evidence = fmt.Sprintf("function(s) %s spill a __stack_chk_guard address register to the stack frame; matches GCC PR85434 / LLVM D64759 silent-bypass shape (attacker-writable spill slot can redirect the canary compare)",
			strings.Join(violators, ", "))
		return r
	}

	r.Verdict = VerdictPass
	r.Evidence = fmt.Sprintf("scanned %d candidate function(s); %d PC-relative guard-address load(s) observed, none spilled to SP-relative slots",
		len(candidates), totalPC)
	return r
}
