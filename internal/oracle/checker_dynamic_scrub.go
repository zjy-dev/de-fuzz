package oracle

import (
	"fmt"
	"strings"
)

// EpilogueCanaryScrubChecker implements `INV-SP-S02` from
// `@/home/yall/project/de-fuzz/docs/tech-docs/invariants/stack-canary.md` §3:
//
//	"In an SP-protected function's epilogue, after the canary check passes
//	but before the return instruction, every GPR that has transiently held
//	the guard value must be explicitly clobbered. Otherwise the residual
//	guard value can be observed by the caller's caller-saved registers and
//	used to forge canaries elsewhere in the process."
//
// The classical signal for this invariant — `__stack_chk_fail` triggering
// SIGABRT (134) on overflow — does NOT change when this invariant is
// violated, so `DynamicBufferSearchChecker` (INV-SP-L01) cannot observe it.
// The detection requires inspecting the actual register file at the
// callsite of `seed()` from a no-canary `main`. The seed template provides
// that observation channel via a "scrub mode": when the binary is invoked
// as `<binary> scrub`, `main` dispatches to a `NO_CANARY` helper that:
//
//  1. calls `seed()` from a single inline-asm block,
//  2. immediately stores every caller-saved GPR to a stack buffer (no C
//     code between the call and the snapshot),
//  3. compares each slot to `__stack_chk_guard`,
//  4. emits one of three stdout markers and `_exit`s.
//
// Stdout grammar (the only side-channel this checker consumes):
//
//	"GUARD_LEAKED reg=<idx> name=<reg-name>\n"     -> Verdict Fail
//	"CANARY_SCRUB_OK\n"                            -> Verdict Pass
//	"CANARY_SCRUB_NA reason=<short-reason>\n"      -> Verdict NotApplicable
//
// Anything else (empty stdout, unrelated noise) is treated as
// NotApplicable with a Reason that captures a truncated stdout, because
// it indicates a template / build failure rather than a real assertion
// outcome.
type EpilogueCanaryScrubChecker struct {
	// InvariantID is the survey-anchored ID this instance asserts. Almost
	// always "INV-SP-S02"; parameterized so future per-ISA / per-version
	// variants can register distinct IDs without forking the type.
	InvariantID string
	// ScrubArgv is the single argv token that selects scrub mode in the
	// seed template. Defaults to "scrub" via ID().
	ScrubArgv string
	// Marker strings — parameterized so tests can inject deterministic
	// values without depending on the template's exact wording.
	LeakMarker    string // "GUARD_LEAKED"
	ScrubOKMarker string // "CANARY_SCRUB_OK"
	NAMarker      string // "CANARY_SCRUB_NA"
}

// Default marker constants kept in lockstep with the seed templates.
const (
	defaultScrubArgv     = "scrub"
	defaultLeakMarker    = "GUARD_LEAKED"
	defaultScrubOKMarker = "CANARY_SCRUB_OK"
	defaultNAMarker      = "CANARY_SCRUB_NA"
)

// ID implements InvariantChecker.
func (c *EpilogueCanaryScrubChecker) ID() string {
	if c.InvariantID == "" {
		return "INV-SP-S02"
	}
	return c.InvariantID
}

// Category implements InvariantChecker.
func (c *EpilogueCanaryScrubChecker) Category() InvariantCategory {
	return CategoryDynamic
}

// Check implements InvariantChecker.
//
// The single execution costs one QEMU/native run; binary-search caching is
// NOT applicable here because scrub mode probes a different argv pattern
// than INV-SP-L01. We deliberately do not write into the
// `dynamic_buffer_search.result` cache slot used by
// `DynamicBufferSearchChecker`; the two checkers coexist in the same
// MechanismOracle by design.
func (c *EpilogueCanaryScrubChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:       c.ID(),
		Category: CategoryDynamic,
		Detail:   map[string]any{},
	}

	if ctx == nil || ctx.Executor == nil || ctx.BinaryPath == "" {
		r.Verdict = VerdictNotApplicable
		r.Reason = "executor or binary path missing"
		return r
	}

	argv := c.ScrubArgv
	if argv == "" {
		argv = defaultScrubArgv
	}
	exitCode, stdout, stderr, err := ctx.Executor.ExecuteWithArgs(ctx.BinaryPath, argv)
	if err != nil {
		r.Verdict = VerdictError
		r.Reason = fmt.Sprintf("executor failed: %v", err)
		r.Detail["stderr"] = truncateForDetail(stderr)
		return r
	}
	r.Detail["scrub_exit_code"] = exitCode
	r.Detail["scrub_argv"] = argv

	leakMarker := stringOr(c.LeakMarker, defaultLeakMarker)
	okMarker := stringOr(c.ScrubOKMarker, defaultScrubOKMarker)
	naMarker := stringOr(c.NAMarker, defaultNAMarker)

	switch {
	case strings.Contains(stdout, leakMarker):
		// Fail wins over OK / NA: a single leak line is the strongest
		// possible signal and must dominate trailing diagnostics.
		line := extractMarkerLine(stdout, leakMarker)
		r.Verdict = VerdictFail
		r.Evidence = fmt.Sprintf(
			"caller-saved register holds __stack_chk_guard after seed() returned: %s",
			line)
		r.Detail["leak_line"] = line
		return r

	case strings.Contains(stdout, naMarker):
		line := extractMarkerLine(stdout, naMarker)
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf("scrub probe declined: %s", line)
		r.Detail["na_line"] = line
		return r

	case strings.Contains(stdout, okMarker):
		r.Verdict = VerdictPass
		r.Evidence = "no caller-saved register equals __stack_chk_guard after seed() returned"
		return r

	default:
		// No recognized marker. Either the scrub helper crashed before
		// reaching its printf, or the template is out of sync with the
		// checker. Surface stdout / stderr / exit code so a developer
		// can debug, but do not promote to Fail — that would create
		// noisy false positives during template churn.
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf(
			"scrub probe produced no recognizable marker (exit=%d); template / runtime mismatch",
			exitCode)
		r.Detail["stdout_excerpt"] = truncateForDetail(stdout)
		r.Detail["stderr_excerpt"] = truncateForDetail(stderr)
		return r
	}
}

// extractMarkerLine returns the first line of `stdout` that contains
// `marker`, trimmed of trailing whitespace. Used for evidence / reason
// strings; if multiple lines match (only possible for malformed templates)
// we take the first.
func extractMarkerLine(stdout, marker string) string {
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, marker) {
			return strings.TrimRight(line, "\r\n\t ")
		}
	}
	return marker
}

// truncateForDetail caps a stdout/stderr excerpt to a fixed budget so that
// `Bug.Description` and metadata don't balloon when the binary spews
// kilobytes of unrelated output.
func truncateForDetail(s string) string {
	const max = 256
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// stringOr returns `s` if non-empty, otherwise `dflt`. Tiny helper to
// keep the marker-resolution code in Check() readable.
func stringOr(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}
