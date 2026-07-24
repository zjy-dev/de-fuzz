package oracle

import (
	"fmt"
)

// VLAAllocaInstrumentationChecker implements `INV-SP-H01` from
// `@/home/yall/project/de-fuzz/docs/tech-docs/invariants/stack-canary.md` §4:
//
//	"Functions containing a VLA or __builtin_alloca must be instrumented
//	with a canary under any non-off setting of -fstack-protector{,-strong,
//	-all,-explicit}. This is a documentation-level strong guarantee — a
//	violation means the process believes it has SP coverage on its most
//	dangerous stack-allocation pattern, but actually does not, which is
//	a whole-function silent bypass."
//
// Detection model. The compiled binary is required to *import*
// `__stack_chk_fail` (or `__stack_chk_fail_local`) whenever the seed
// source contains any VLA or alloca. We use the seed-source heuristic
// (`SeedShape.HasDynamicAlloc`) as the trigger because the binary
// itself does not preserve "this function had a VLA" information after
// the optimizer is done — a missing import on a VLA seed is precisely
// the silent-bypass condition we want to flag.
//
// Verdict mapping (positive build, default):
//   - seed has VLA / alloca AND binary imports __stack_chk_fail   → Pass
//   - seed has VLA / alloca AND binary does NOT import the symbol → Fail
//   - seed has neither VLA nor alloca                             → NotApplicable
//   - inspector unavailable / non-ELF                             → NotApplicable
type VLAAllocaInstrumentationChecker struct {
	// InvariantID survey-anchored ID; defaults to "INV-SP-H01".
	InvariantID string
}

// ID implements InvariantChecker.
func (c *VLAAllocaInstrumentationChecker) ID() string {
	if c.InvariantID == "" {
		return "INV-SP-H01"
	}
	return c.InvariantID
}

// Category implements InvariantChecker. H01 is a static binary-vs-source
// cross-check; no execution is needed.
func (c *VLAAllocaInstrumentationChecker) Category() InvariantCategory {
	return CategoryStatic
}

// Check implements InvariantChecker.
func (c *VLAAllocaInstrumentationChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:       c.ID(),
		Category: CategoryStatic,
		Detail:   map[string]any{},
	}

	if ctx == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no check context"
		return r
	}

	shape := classifySeedShape(ctx.Seed)
	r.Detail["has_vla"] = shape.HasVLA
	r.Detail["has_alloca"] = shape.HasAlloca

	if !shape.HasDynamicAlloc() {
		r.Verdict = VerdictNotApplicable
		r.Reason = "seed has no VLA / alloca; INV-SP-H01 only applies to dynamic stack allocation"
		return r
	}

	if ctx.Inspector == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no inspector available (missing binary path)"
		return r
	}

	imports, err := ctx.Inspector.ImportedFunctions()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector failed: %v", err)
		return r
	}

	hasFail := false
	for _, s := range imports {
		if s == "__stack_chk_fail" || s == "__stack_chk_fail_local" {
			hasFail = true
			break
		}
	}
	r.Detail["has_stack_chk_fail"] = hasFail

	if hasFail {
		r.Verdict = VerdictPass
		r.Evidence = "seed has VLA/alloca AND binary imports __stack_chk_fail; canary instrumentation present as required"
		return r
	}

	r.Verdict = VerdictFail
	r.Evidence = "seed has VLA/alloca but binary does NOT import __stack_chk_fail; whole-function silent bypass on the most dangerous stack-allocation pattern"
	return r
}
