package oracle

import (
	"fmt"
)

// StackChkFailNoreturnChecker implements `INV-SP-V02` from
// `@/home/yall/project/de-fuzz/docs/tech-docs/invariants/stack-canary.md` §2:
//
//	"__stack_chk_fail must be noreturn. GCC TARGET_STACK_PROTECT_FAIL
//	hook and LLVM both treat __stack_chk_fail as noreturn; glibc / libssp
//	implementations end the call in abort(). If a freestanding /
//	embedded stub returns, the function return path will continue
//	executing the polluted retaddr ⇒ silent bypass."
//
// Detection model. The canonical positive signal that the runtime
// honors `noreturn` is: when a vulnerable function's canary slot is
// overwritten, the process is terminated by SIGABRT (exit 134). The
// `DynamicBufferSearchChecker` (INV-SP-L01) already observes this
// signal as a side effect of its binary search; V02 reuses the cache
// instead of running a second QEMU sweep.
//
// Verdict mapping:
//   - L01 cache: crash @ SIGABRT (134)         → Pass (fail handler aborted as required)
//   - L01 cache: crash @ SIGSEGV/SIGBUS + sent → NotApplicable (this is an L01 bypass; the fail handler was never reached, so V02 cannot be confirmed or refuted)
//   - L01 cache: no crash within bound          → NotApplicable (search bound too tight, or seed has no overflow path; fail handler was never exercised)
//   - L01 cache absent (no L01 in mechanism)    → NotApplicable
//
// We deliberately do NOT promote any state to Fail without a positive
// "fail handler returned" signal. A precise Fail would require a custom
// probe that triggers `__stack_chk_fail` directly and observes whether
// the process continues; that lives behind a separate, future checker.
type StackChkFailNoreturnChecker struct {
	// InvariantID survey-anchored ID; defaults to "INV-SP-V02".
	InvariantID string
}

// ID implements InvariantChecker.
func (c *StackChkFailNoreturnChecker) ID() string {
	if c.InvariantID == "" {
		return "INV-SP-V02"
	}
	return c.InvariantID
}

// Category implements InvariantChecker. V02 is observed via dynamic
// execution (the L01 binary search), even though its conclusion is
// "fail handler aborted, as the runtime contract requires".
func (c *StackChkFailNoreturnChecker) Category() InvariantCategory {
	return CategoryDynamic
}

// Check implements InvariantChecker. It does NOT execute the binary;
// it inspects the cached `dynamicSearchResult` produced by an earlier
// `DynamicBufferSearchChecker` run within the same Analyze call.
func (c *StackChkFailNoreturnChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:       c.ID(),
		Category: CategoryDynamic,
		Detail:   map[string]any{},
	}

	if ctx == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no check context"
		return r
	}

	v, ok := ctx.CacheGet(dynamicSearchCacheKey)
	if !ok {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no cached binary-search result; INV-SP-L01 did not run before V02"
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
		r.Reason = "no crash observed in binary search; __stack_chk_fail noreturn behavior could not be exercised"
		return r
	}

	switch dyn.CrashExitCode {
	case ExitCodeSIGABRT:
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf(
			"canary trip at fill_size=%d produced SIGABRT (134); __stack_chk_fail terminated the process as required by noreturn",
			dyn.MinCrashSize)
		return r
	case ExitCodeSIGSEGV, ExitCodeSIGBUS:
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf(
			"crash at fill_size=%d via exit %d (not SIGABRT); the fail handler was never reached, so V02 cannot be confirmed via this channel",
			dyn.MinCrashSize, dyn.CrashExitCode)
		return r
	default:
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf(
			"crash at fill_size=%d via unexpected exit %d; cannot attribute to __stack_chk_fail noreturn behavior",
			dyn.MinCrashSize, dyn.CrashExitCode)
		return r
	}
}
