package oracle

import (
	"fmt"
	"strings"
)

// FortifyReadonlyAreaChecker asserts INV-FORT-R01:
//
//	"glibc `__readonly_area` must NOT return `safe == 1` when
//	`/proc/self/maps` is unavailable (chroot / seccomp / no /proc).
//	Otherwise `vfprintf` under `_FORTIFY_SOURCE>=2` lets `%n` writes
//	silently land on writable memory."
//
// See `docs/tech-docs/invariants/fortify-source.md` §INV-FORT-R01.
//
// The dynamic protocol is: the seed template's `procmaps` argv mode
// performs a `%n` write at a known location and emits one of:
//
//	"FORTIFY_R01_TRAPPED reason=<short-reason>\n"     -> Pass
//	"FORTIFY_R01_BYPASS reason=<short-reason>\n"      -> Fail
//	"FORTIFY_R01_NA reason=<short-reason>\n"          -> NotApplicable
//
// In typical hosted Linux environments the `/proc/self/maps`
// availability test cannot be faked from inside the binary itself
// (would need seccomp / chroot privileges); accordingly the template
// declines with NA and we honor that. This is the fallback the user
// explicitly accepted in the implementation discussion.
type FortifyReadonlyAreaChecker struct {
	// Argv is the single argv token that selects R01 mode in the seed
	// template. Default "procmaps" via ID().
	Argv string
}

const (
	defaultR01Argv          = "procmaps"
	defaultR01TrappedMarker = "FORTIFY_R01_TRAPPED"
	defaultR01BypassMarker  = "FORTIFY_R01_BYPASS"
	defaultR01NAMarker      = "FORTIFY_R01_NA"
)

func (c *FortifyReadonlyAreaChecker) ID() string                  { return "INV-FORT-R01" }
func (c *FortifyReadonlyAreaChecker) Category() InvariantCategory { return CategoryDynamic }

func (c *FortifyReadonlyAreaChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryDynamic,
		SourceURL:   "https://codebrowser.dev/glibc/glibc/sysdeps/unix/sysv/linux/readonly-area.c.html",
		Sensitivity: "stable",
		Detail:      map[string]any{},
	}
	if ctx == nil || ctx.Executor == nil || ctx.BinaryPath == "" {
		r.Verdict = VerdictNotApplicable
		r.Reason = "executor or binary path missing"
		return r
	}
	argv := c.Argv
	if argv == "" {
		argv = defaultR01Argv
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

	switch {
	case strings.Contains(stdout, defaultR01BypassMarker):
		line := extractMarkerLine(stdout, defaultR01BypassMarker)
		r.Verdict = VerdictFail
		r.Evidence = fmt.Sprintf(
			"`%%n` write reached writable memory under simulated `/proc` outage; __readonly_area returned safe=1: %s",
			line)
		r.Detail["bypass_line"] = line
		return r
	case strings.Contains(stdout, defaultR01TrappedMarker):
		line := extractMarkerLine(stdout, defaultR01TrappedMarker)
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("__chk_fail tripped under simulated `/proc` outage: %s", line)
		return r
	case strings.Contains(stdout, defaultR01NAMarker):
		line := extractMarkerLine(stdout, defaultR01NAMarker)
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf("R01 probe declined: %s", line)
		r.Detail["na_line"] = line
		return r
	default:
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf(
			"R01 probe produced no recognizable marker (exit=%d); QEMU/seccomp environment likely cannot fake /proc outage",
			exit)
		r.Detail["stdout_excerpt"] = truncateForDetail(stdout)
		r.Detail["stderr_excerpt"] = truncateForDetail(stderr)
		return r
	}
}
