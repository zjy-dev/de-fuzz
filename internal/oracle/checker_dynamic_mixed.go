package oracle

import (
	"fmt"
)

// MixedVulnerableObjectsChecker implements `INV-SP-L03` from
// `@/home/yall/project/de-fuzz/docs/tech-docs/invariants/stack-canary.md` §1:
//
//	"add_stack_protection_conflicts forces every vulnerable object
//	(SPCT_HAS_*) to share the same canary protection plane: each
//	conflicts with the canary slot, so all of them must sit on the
//	overflow-source side of the canary. If any vulnerable object
//	gets ranked on the wrong side, that one's overflow is silent."
//
// Detection model. L03 is a per-shape specialization of L01 that fires
// only when a seed actually contains *more than one* flavor of
// vulnerable object (fixed-size byte buffer + VLA + alloca). With
// mixed shapes the conflict-graph contract is non-trivial: the
// compiler must keep all of them below the canary. A SIGSEGV + sentinel
// at the L01 boundary on a mixed seed indicates one of the objects sat
// on the wrong side and bypassed the canary plane.
//
// We reuse the L01 cache rather than running a new search: the same
// fill_size sweep that L01 performed is already the right probe
// surface. L02 covers the VLA/alloca-only pattern (CVE-2023-4039);
// L03 is the genuine mixed pattern.
//
// Verdict mapping (positive build):
//   - seed not mixed (≤ 1 vulnerable flavor)            → NotApplicable
//   - L01 cache absent                                   → NotApplicable
//   - L01 cache: no crash within bound                   → NotApplicable
//   - L01 cache: crash @ SIGABRT (134)                   → Pass
//   - L01 cache: SIGSEGV/SIGBUS + sentinel               → Fail
//   - L01 cache: SIGSEGV/SIGBUS without sentinel         → NotApplicable
//   - other crash exit                                   → NotApplicable
//
// Polarity. Under `-fno-stack-protector` the mixed seed will likely
// also produce SIGSEGV+sentinel, but that is not a violation — there
// is no canary plane to be subverted. We tag `polarity_sensitive:
// true` so the aggregator inverts the verdict on the negative control.
type MixedVulnerableObjectsChecker struct {
	InvariantID string
	SourceURL   string
	Sensitivity string
}

// ID implements InvariantChecker.
func (c *MixedVulnerableObjectsChecker) ID() string {
	if c.InvariantID == "" {
		return "INV-SP-L03"
	}
	return c.InvariantID
}

// Category implements InvariantChecker.
func (c *MixedVulnerableObjectsChecker) Category() InvariantCategory {
	return CategoryDynamic
}

// Check implements InvariantChecker.
func (c *MixedVulnerableObjectsChecker) Check(ctx *CheckContext) InvariantResult {
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
	r.Detail["has_fixed_buffer"] = shape.HasFixedBuffer
	r.Detail["has_vla"] = shape.HasVLA
	r.Detail["has_alloca"] = shape.HasAlloca
	r.Detail["is_mixed"] = shape.IsMixed()

	if !shape.IsMixed() {
		r.Verdict = VerdictNotApplicable
		r.Reason = "seed has only one flavor of vulnerable object; INV-SP-L03 requires mixed (fixed+VLA / fixed+alloca / VLA+alloca)"
		return r
	}

	v, ok := ctx.CacheGet(dynamicSearchCacheKey)
	if !ok {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no cached binary-search result; INV-SP-L01 did not run before L03"
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
		r.Reason = "no crash observed in binary search; cannot confirm or refute the mixed-object protection plane"
		return r
	}

	switch dyn.CrashExitCode {
	case ExitCodeSIGABRT:
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf(
			"mixed-object seed reached canary trap at fill_size=%d via SIGABRT (134); shared canary plane held",
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
			"mixed-object bypass detected: fill_size=%d caused exit %d after seed() returned; one vulnerable object likely sat on the wrong side of the canary plane",
			dyn.MinCrashSize, dyn.CrashExitCode)
		return r
	default:
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf(
			"crash at fill_size=%d via unexpected exit %d; cannot attribute to L03 protection-plane violation",
			dyn.MinCrashSize, dyn.CrashExitCode)
		return r
	}
}

func (c *MixedVulnerableObjectsChecker) sourceURL() string {
	if c.SourceURL != "" {
		return c.SourceURL
	}
	return "https://gcc.gnu.org/git/?p=gcc.git;a=blob;f=gcc/cfgexpand.cc"
}

func (c *MixedVulnerableObjectsChecker) sensitivity() string {
	if c.Sensitivity != "" {
		return c.Sensitivity
	}
	return "stable"
}
