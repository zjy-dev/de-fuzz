package oracle

import (
	"fmt"
)

// ProtectorSlotRelocationChecker implements `INV-SP-L04` from
// `@/home/yall/project/de-fuzz/docs/tech-docs/invariants/stack-canary.md` §1:
//
//	"No backend / RA / frame-elimination phase may relocate the
//	protector slot's actual offset above vulnerable locals. CERT/CC
//	VU#129209 (LLVM Arm backend) is exactly this kind of violation:
//	the protector was reallocated after LocalStackSlotAllocation,
//	leaving its offset above the locals and rendering the canary
//	check unable to intercept the overflow."
//
// Detection model. L04's failure mode is observationally identical to
// the L01 bypass pattern (SIGSEGV / SIGBUS with sentinel after seed()
// returned), but the *reason* differs: in L04 the protector slot is
// physically misplaced even though the mid-end conflict graph
// (INV-SP-L03) was correct. Without disassembly we cannot decompose
// the two; what we *can* do is fire L04 whenever the seed has at
// least one fixed-size vulnerable buffer (enough to exercise the
// protector slot relocation path) and report a distinct ID so a
// downstream root-cause classifier can correlate observation with
// toolchain version (Arm Compiler 6.12 / ACfL 19.0–19.2 / LLVM Arm
// 9.x are the known violators per the survey).
//
// We reuse the L01 cache rather than running a new search. The same
// fill_size sweep is the right probe; L04 is a re-interpretation of
// that signal under a target-specific lens.
//
// Verdict mapping (positive build):
//   - seed has no fixed-size vulnerable buffer            → NotApplicable
//   - L01 cache absent                                    → NotApplicable
//   - L01 cache: no crash within bound                    → NotApplicable
//   - L01 cache: crash @ SIGABRT (134)                    → Pass
//   - L01 cache: SIGSEGV/SIGBUS + sentinel                → Fail
//   - L01 cache: SIGSEGV/SIGBUS without sentinel          → NotApplicable
//   - other crash exit                                    → NotApplicable
//
// Polarity. The Fail verdict only makes sense when SP is enabled.
// Under `-fno-stack-protector` the same SIGSEGV+sentinel will appear
// but no protector slot exists to be relocated. We tag
// `polarity_sensitive: true` so the aggregator inverts on the
// negative control.
//
// L04 deliberately overlaps with L01 / L03 on the observable signal.
// Each checker is intended to anchor a different *root cause* in the
// final report; the aggregator already deduplicates raw bug-equivalent
// signals at the report layer.
type ProtectorSlotRelocationChecker struct {
	InvariantID string
	SourceURL   string
	Sensitivity string
}

// ID implements InvariantChecker.
func (c *ProtectorSlotRelocationChecker) ID() string {
	if c.InvariantID == "" {
		return "INV-SP-L04"
	}
	return c.InvariantID
}

// Category implements InvariantChecker.
func (c *ProtectorSlotRelocationChecker) Category() InvariantCategory {
	return CategoryDynamic
}

// Check implements InvariantChecker.
func (c *ProtectorSlotRelocationChecker) Check(ctx *CheckContext) InvariantResult {
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

	// L04 needs at least one vulnerable object that lives in a
	// frame-index slot — i.e., a fixed-size buffer subject to the
	// LocalStackSlotAllocation pass. Pure VLA/alloca seeds go to L02.
	if !shape.HasFixedBuffer {
		r.Verdict = VerdictNotApplicable
		r.Reason = "seed has no fixed-size vulnerable buffer; INV-SP-L04 only applies to frame-indexed locals"
		return r
	}

	v, ok := ctx.CacheGet(dynamicSearchCacheKey)
	if !ok {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no cached binary-search result; INV-SP-L01 did not run before L04"
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
		r.Reason = "no crash observed in binary search; protector slot placement could not be exercised"
		return r
	}

	switch dyn.CrashExitCode {
	case ExitCodeSIGABRT:
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf(
			"fixed-buffer seed reached canary trap at fill_size=%d via SIGABRT (134); protector slot stayed below locals",
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
			"protector-slot relocation suspected: fill_size=%d caused exit %d after seed() returned; protector slot may sit above the vulnerable buffer (CERT VU#129209 pattern)",
			dyn.MinCrashSize, dyn.CrashExitCode)
		return r
	default:
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf(
			"crash at fill_size=%d via unexpected exit %d; cannot attribute to L04 protector-slot relocation",
			dyn.MinCrashSize, dyn.CrashExitCode)
		return r
	}
}

func (c *ProtectorSlotRelocationChecker) sourceURL() string {
	if c.SourceURL != "" {
		return c.SourceURL
	}
	return "https://kb.cert.org/vuls/id/129209/"
}

func (c *ProtectorSlotRelocationChecker) sensitivity() string {
	if c.Sensitivity != "" {
		return c.Sensitivity
	}
	return "target-specific"
}
