package oracle

import (
	"fmt"
	"strings"
)

// DynamicBufferSearchChecker implements the dynamic-execution invariants
// detected by binary-searching `fill_size` against a binary that takes
// `<buf_size> <fill_size>` argv.
//
// It is parameterized to cover the dynamic invariants of any mechanism that
// uses the same binary-search protocol:
//
//   - SIGABRT (134) ⇒ in-function guard tripped (e.g. __stack_chk_fail).
//   - SIGSEGV (139) / SIGBUS (135) with sentinel ⇒ return-address overwrite
//     without guard trip ⇒ violation.
//
// Reuse rationale: extracting this checker centralizes the binary-search
// logic so each mechanism oracle only needs to supply InvariantID and labels
// (see `docs/architecture/oracle-multi-invariant-redesign.md` §1.1, §3.4).
type DynamicBufferSearchChecker struct {
	// InvariantID is the survey-anchored ID this instance asserts (e.g.,
	// "INV-SP-L01" for the stack-canary mechanism).
	InvariantID string
	// MechanismLabel is a short human label used in evidence strings
	// (e.g., "Stack canary").
	MechanismLabel string
	// SourceURL is copied verbatim into the InvariantResult so reports
	// can backlink to the survey.
	SourceURL string
	// Sensitivity mirrors the survey's `version_sensitivity` field.
	Sensitivity string

	// MaxFillSize bounds the binary search upper end. Search is on `[0, MaxFillSize]`.
	MaxFillSize int
	// DefaultBufSize is passed as argv[1] (the "buf_size" parameter) for
	// every probe; only argv[2] (fill_size) varies.
	DefaultBufSize int
	// SentinelMarker is the substring that must appear in stdout to
	// distinguish "seed() returned then crashed" (true bypass) from
	// "seed() crashed internally" (likely false positive due to spill
	// corruption). See `docs/oracles/canary-oracle.md` §"假阳性修复".
	SentinelMarker string
}

// dynamicSearchResult is the cached fact set produced by a single binary
// search. Stored under a fixed key in `CheckContext.Cache` so multiple
// dynamic checkers can reuse the search instead of re-running QEMU N times.
type dynamicSearchResult struct {
	// MinCrashSize is the smallest fill_size that produced a non-zero
	// exit, or -1 if the search never observed a crash.
	MinCrashSize int
	// CrashExitCode is the exit code at MinCrashSize (0 when no crash).
	CrashExitCode int
	// HasSentinel records whether the sentinel marker was present in
	// stdout at MinCrashSize (always false when no crash).
	HasSentinel bool
	// Probes counts the number of executions, for cost diagnostics.
	Probes int
}

const dynamicSearchCacheKey = "oracle.dynamic_buffer_search.result"

// ID implements InvariantChecker.
func (c *DynamicBufferSearchChecker) ID() string {
	if c.InvariantID == "" {
		return "INV-DYNAMIC-BUFFER-SEARCH"
	}
	return c.InvariantID
}

// Category implements InvariantChecker. Always Dynamic — this checker is
// the canonical example of "needs to run the binary".
func (c *DynamicBufferSearchChecker) Category() InvariantCategory { return CategoryDynamic }

// Check implements InvariantChecker.
//
// Verdict mapping (positive polarity):
//   - no crash within [0, MaxFillSize]      → NotApplicable (search bound too tight, or mechanism prevents reach)
//   - SIGABRT (134)                          → Pass (canary / fortify caught it)
//   - SIGSEGV (139) / SIGBUS (135) + sentinel→ Fail (mechanism bypassed)
//   - SIGSEGV / SIGBUS + no sentinel         → NotApplicable (likely indirect crash inside seed())
//   - other non-zero exit + sentinel         → Fail (suspicious; flag for manual review)
//   - other non-zero exit + no sentinel      → NotApplicable
//
// Polarity inversion is handled by the aggregator (see `applyPolarity`);
// this checker tags itself polarity-sensitive via Detail.
func (c *DynamicBufferSearchChecker) Check(ctx *CheckContext) InvariantResult {
	result := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryDynamic,
		SourceURL:   c.SourceURL,
		Sensitivity: c.Sensitivity,
		Detail: map[string]any{
			"polarity_sensitive": true,
		},
	}

	if ctx == nil || ctx.Executor == nil || ctx.BinaryPath == "" {
		result.Verdict = VerdictNotApplicable
		result.Reason = "executor or binary path missing"
		return result
	}
	if c.MaxFillSize <= 0 {
		result.Verdict = VerdictError
		result.Reason = "MaxFillSize must be > 0"
		return result
	}

	dyn := c.runOrLoadSearch(ctx)
	result.Detail["min_crash_size"] = dyn.MinCrashSize
	result.Detail["crash_exit_code"] = dyn.CrashExitCode
	result.Detail["has_sentinel"] = dyn.HasSentinel
	result.Detail["probes"] = dyn.Probes
	result.Detail["max_fill_size"] = c.MaxFillSize
	result.Detail["default_buf_size"] = c.DefaultBufSize

	// No crash observed at all in the search range.
	if dyn.MinCrashSize < 0 {
		result.Verdict = VerdictNotApplicable
		result.Reason = fmt.Sprintf(
			"no crash observed within fill_size [0, %d]; either mechanism prevents reach or search bound is too tight",
			c.MaxFillSize)
		return result
	}

	switch dyn.CrashExitCode {
	case ExitCodeSIGABRT:
		// Mechanism's __*_chk_fail caught the overflow. Pass.
		result.Verdict = VerdictPass
		result.Evidence = fmt.Sprintf(
			"%s aborted at fill_size=%d via SIGABRT (134); mechanism trap intact",
			c.MechanismLabel, dyn.MinCrashSize)
		return result

	case ExitCodeSIGSEGV, ExitCodeSIGBUS:
		if !dyn.HasSentinel {
			result.Verdict = VerdictNotApplicable
			result.Reason = fmt.Sprintf(
				"crash at fill_size=%d (exit %d) without sentinel — likely intra-seed() crash, not return-address overwrite",
				dyn.MinCrashSize, dyn.CrashExitCode)
			return result
		}
		result.Verdict = VerdictFail
		signal := "SIGSEGV"
		extra := ""
		if dyn.CrashExitCode == ExitCodeSIGBUS {
			signal = "SIGBUS"
			extra = " (unaligned return address)"
		}
		result.Evidence = fmt.Sprintf(
			"%s bypass detected: fill_size=%d caused %s (exit %d) after seed() returned%s; mechanism failed to trap before return",
			c.MechanismLabel, dyn.MinCrashSize, signal, dyn.CrashExitCode, extra)
		return result

	default:
		if !dyn.HasSentinel {
			result.Verdict = VerdictNotApplicable
			result.Reason = fmt.Sprintf(
				"crash at fill_size=%d with unexpected exit %d and no sentinel",
				dyn.MinCrashSize, dyn.CrashExitCode)
			return result
		}
		result.Verdict = VerdictFail
		result.Evidence = fmt.Sprintf(
			"%s suspicious bypass detected: fill_size=%d caused unexpected exit %d after seed() returned",
			c.MechanismLabel, dyn.MinCrashSize, dyn.CrashExitCode)
		return result
	}
}

// runOrLoadSearch returns a dynamicSearchResult, reusing the cached one if a
// sibling checker already produced it within the same Analyze call.
func (c *DynamicBufferSearchChecker) runOrLoadSearch(ctx *CheckContext) *dynamicSearchResult {
	if v, ok := ctx.CacheGet(dynamicSearchCacheKey); ok {
		if cached, isResult := v.(*dynamicSearchResult); isResult {
			return cached
		}
	}
	dyn := c.binarySearchCrash(ctx)
	ctx.CacheSet(dynamicSearchCacheKey, dyn)
	return dyn
}

// binarySearchCrash is the core search loop, lifted with minor changes from
// the legacy `(*CanaryOracle).binarySearchCrash` so behavior is preserved
// bit-for-bit (existing tests check exact crash sizes and exit codes).
//
// Algorithm:
//  1. Binary search for the smallest mid in `[0, MaxFillSize]` whose probe
//     produces a non-zero exit code.
//  2. After the loop, re-execute at the found `ans` to refresh exit code
//     and sentinel presence (the loop may have transiently observed a
//     different exit due to flakiness near the boundary).
//
// Probes count is reported in the result for diagnostics.
func (c *DynamicBufferSearchChecker) binarySearchCrash(ctx *CheckContext) *dynamicSearchResult {
	res := &dynamicSearchResult{MinCrashSize: -1}

	L, R := 0, c.MaxFillSize
	for L <= R {
		mid := (L + R) / 2
		exitCode, stdout, _, err := ctx.Executor.ExecuteWithArgs(
			ctx.BinaryPath,
			fmt.Sprintf("%d", c.DefaultBufSize),
			fmt.Sprintf("%d", mid),
		)
		res.Probes++
		if err != nil {
			// Execution error — try larger size; matches legacy behavior.
			L = mid + 1
			continue
		}
		if exitCode != 0 {
			res.MinCrashSize = mid
			res.CrashExitCode = exitCode
			res.HasSentinel = strings.Contains(stdout, c.SentinelMarker)
			R = mid - 1
		} else {
			L = mid + 1
		}
	}

	// Re-verify at the found boundary.
	if res.MinCrashSize >= 0 {
		exitCode, stdout, _, err := ctx.Executor.ExecuteWithArgs(
			ctx.BinaryPath,
			fmt.Sprintf("%d", c.DefaultBufSize),
			fmt.Sprintf("%d", res.MinCrashSize),
		)
		res.Probes++
		if err == nil && exitCode != 0 {
			res.CrashExitCode = exitCode
			res.HasSentinel = strings.Contains(stdout, c.SentinelMarker)
		}
	}

	return res
}
