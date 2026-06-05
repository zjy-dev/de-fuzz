package oracle

import (
	"fmt"
	"strings"
)

// FortifyChkNoreturnChecker asserts INV-FORT-R02:
//
//	"glibc / libssp's `__chk_fail` and `__fortify_fail` must be
//	`noreturn`; calling them must terminate the process. A handler that
//	returns lets the caller's tail run with corrupt state — the silent
//	bypass shape mirroring `__stack_chk_fail`."
//
// See `docs/tech-docs/invariants/fortify-source.md` §INV-FORT-R02.
//
// Dynamic protocol: the seed template's `chkfail` argv mode deliberately
// triggers a `__memcpy_chk` failure and, after the call, prints either
// of two markers:
//
//	"FORTIFY_R02_RETURNED\n"   -> Fail (handler returned)
//	"FORTIFY_R02_TRAPPED\n"    -> Pass (process ended via signal/abort)
//
// The "Pass" path also includes any non-zero exit code with the trap
// marker absent because the template _exit()s immediately upon trap.
// We treat `exit_code != 0 && stdout has no R02_RETURNED` as
// "trapped" — the fail handler did not fall through.
type FortifyChkNoreturnChecker struct {
	Argv string
}

const (
	defaultR02Argv           = "chkfail"
	defaultR02ReturnedMarker = "FORTIFY_R02_RETURNED"
	defaultR02TrappedMarker  = "FORTIFY_R02_TRAPPED"
)

func (c *FortifyChkNoreturnChecker) ID() string                  { return "INV-FORT-R02" }
func (c *FortifyChkNoreturnChecker) Category() InvariantCategory { return CategoryDynamic }

func (c *FortifyChkNoreturnChecker) Check(ctx *CheckContext) InvariantResult {
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
	argv := c.Argv
	if argv == "" {
		argv = defaultR02Argv
	}
	exit, stdout, stderr, err := ctx.Executor.ExecuteWithArgs(ctx.BinaryPath, argv)
	if err != nil {
		r.Verdict = VerdictError
		r.Reason = fmt.Sprintf("executor failed: %v", err)
		r.Detail["stderr"] = truncateForDetail(stderr)
		return r
	}
	r.Detail["exit_code"] = exit
	r.Detail["argv"] = argv

	if strings.Contains(stdout, defaultR02ReturnedMarker) {
		line := extractMarkerLine(stdout, defaultR02ReturnedMarker)
		r.Verdict = VerdictFail
		r.Evidence = fmt.Sprintf(
			"control returned past `__chk_fail`/`__fortify_fail`; handler is not noreturn: %s",
			line)
		r.Detail["returned_line"] = line
		return r
	}
	if strings.Contains(stdout, defaultR02TrappedMarker) {
		line := extractMarkerLine(stdout, defaultR02TrappedMarker)
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("fortify-trap probe trapped before reaching the post-call print: %s", line)
		return r
	}

	// No explicit marker. If the process terminated abnormally (signal
	// or non-zero exit), the handler did NOT fall through — Pass with
	// a note capturing the exit code. This is the canonical happy path
	// for a `noreturn` handler that abort()s.
	if exit != 0 && IsCrashExit(exit) || (exit != 0 && exit != 1) {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("fortify-trap probe terminated with exit=%d (no FORTIFY_R02_RETURNED marker)", exit)
		return r
	}

	r.Verdict = VerdictNotApplicable
	r.Reason = fmt.Sprintf(
		"R02 probe produced no recognizable marker and exit=%d; template / runtime mismatch",
		exit)
	r.Detail["stdout_excerpt"] = truncateForDetail(stdout)
	r.Detail["stderr_excerpt"] = truncateForDetail(stderr)
	return r
}
