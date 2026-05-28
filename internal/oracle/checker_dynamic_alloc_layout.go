package oracle

import (
	"fmt"
)

// DynamicAllocLayoutChecker implements `INV-SP-L02` from
// `@/home/yall/project/de-fuzz/docs/tech-docs/invariants/stack-canary.md` §1:
//
//	"VLA / alloca regions must be located on the stack-low side of the
//	canary (i.e., farther from saved regs); they must not be inserted
//	between canary and saved regs. Otherwise a precisely sized dynamic
//	allocation overflow can cross the canary directly to LR / FP. CVE-
//	2023-4039 is the canonical violation (GCC ≤ 13.2 on AArch64)."
//
// Detection model. L02 is the VLA/alloca specialization of L01: when
// the dynamic-allocation region is mis-placed above the canary, a
// crafted overflow returns through the corrupted retaddr, producing a
// SIGSEGV/SIGBUS *after* seed() has returned (sentinel marker present).
// We therefore reuse the `dynamicSearchCacheKey` produced by INV-SP-L01
// rather than running a second binary search; the difference is the
// preconditioning: L02 only fires when the seed actually contains a
// VLA or alloca.
//
// Verdict mapping (positive build):
//   - seed has no VLA / alloca                              → NotApplicable
//   - L01 cache absent                                       → NotApplicable
//   - L01 cache: no crash within bound                       → NotApplicable
//   - L01 cache: crash @ SIGABRT (134)                       → Pass
//   - L01 cache: crash @ SIGSEGV/SIGBUS + sentinel           → Fail
//   - L01 cache: crash @ SIGSEGV/SIGBUS without sentinel     → NotApplicable
//   - other crash exit                                       → NotApplicable
//
// Polarity. The Fail verdict is meaningful only when SP is enabled.
// Under `-fno-stack-protector` the VLA seed will likely also crash via
// SIGSEGV-with-sentinel, but that is not a violation — there is no
// canary to be crossed. We tag `polarity_sensitive: true` so the
// aggregator inverts the verdict on the negative control.
type DynamicAllocLayoutChecker struct {
	InvariantID string
	SourceURL   string
	Sensitivity string
}

// ID implements InvariantChecker.
func (c *DynamicAllocLayoutChecker) ID() string {
	if c.InvariantID == "" {
		return "INV-SP-L02"
	}
	return c.InvariantID
}

// Category implements InvariantChecker.
func (c *DynamicAllocLayoutChecker) Category() InvariantCategory {
	return CategoryDynamic
}

// Check implements InvariantChecker.
func (c *DynamicAllocLayoutChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryDynamic,
		SourceURL:   c.sourceURL(),
		Sensitivity: c.sensitivity(),
		Detail: map[string]any{
			"polarity_sensitive": true,
		},
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
		r.Reason = "seed has no VLA / alloca; INV-SP-L02 only applies to dynamic stack allocation"
		return r
	}

	v, ok := ctx.CacheGet(dynamicSearchCacheKey)
	if !ok {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no cached binary-search result; INV-SP-L01 did not run before L02"
		return r
	}
	dyn, isResult := v.(*dynamicSearchResult)
	if !isResult || dyn == nil {
		r.Verdict = VerdictError
		r.Reason = "binary-search cache value is not a *dynamicSearchResult"
		return r
	}
	r.Detail["min_crash_size"] = dyn.MinCrashSize
	r.Detail["crash_exit_code"] = dyn.CrashExitCode
	r.Detail["has_sentinel"] = dyn.HasSentinel

	if dyn.MinCrashSize < 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no crash observed in binary search; cannot confirm or refute L02 layout invariant"
		return r
	}

	switch dyn.CrashExitCode {
	case ExitCodeSIGABRT:
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf(
			"VLA/alloca seed reached canary trap at fill_size=%d via SIGABRT (134); dynamic alloc layout below canary held",
			dyn.MinCrashSize)
		return r
	case ExitCodeSIGSEGV, ExitCodeSIGBUS:
		if !dyn.HasSentinel {
			r.Verdict = VerdictNotApplicable
			r.Reason = fmt.Sprintf(
				"crash at fill_size=%d (exit %d) without sentinel — likely intra-seed() crash, not retaddr overwrite",
				dyn.MinCrashSize, dyn.CrashExitCode)
			return r
		}
		r.Verdict = VerdictFail
		r.Evidence = fmt.Sprintf(
			"VLA/alloca bypass detected: fill_size=%d caused exit %d after seed() returned; dynamic alloc region may sit above canary (CVE-2023-4039 pattern)",
			dyn.MinCrashSize, dyn.CrashExitCode)
		return r
	default:
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf(
			"crash at fill_size=%d via unexpected exit %d; cannot attribute to L02 layout violation",
			dyn.MinCrashSize, dyn.CrashExitCode)
		return r
	}
}

func (c *DynamicAllocLayoutChecker) sourceURL() string {
	if c.SourceURL != "" {
		return c.SourceURL
	}
	return "https://rtx.meta.security/mitigation/2023/09/12/CVE-2023-4039.html"
}

func (c *DynamicAllocLayoutChecker) sensitivity() string {
	if c.Sensitivity != "" {
		return c.Sensitivity
	}
	return "stable since fix"
}
