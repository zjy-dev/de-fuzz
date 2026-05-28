package oracle

import (
	"fmt"
	"strings"
)

// FortifyVfprintfFlagChecker asserts INV-FORT-C01:
//
//	"Every `__*_chk` printf-family entry (printf / sprintf / snprintf /
//	vsnprintf / syslog / ...) must set the unified fortify flag before
//	dispatching to vfprintf, otherwise `%n` writes silently land on
//	writable segments — exactly the shape of CVE-2012-0864."
//
// See `docs/tech-docs/invariants/fortify-source.md` §INV-FORT-C01.
//
// Dynamic protocol: the seed template's `printf:<entry>` argv mode
// invokes the named entry with a `%n` payload aimed at a writable but
// non-readonly stack target. Per entry, stdout reports one of:
//
//	"FORTIFY_C01_TRAPPED entry=<name>\n"  -> Pass for this entry
//	"FORTIFY_C01_BYPASS  entry=<name>\n"  -> Fail (entry skipped fortify)
//	"FORTIFY_C01_NA      entry=<name> reason=<...>\n" -> NotApplicable
//
// The checker sweeps the configured `entries` list (default = all of
// `printfEntries`) and aggregates: any Fail dominates; otherwise
// trapping entries produce Pass; if none of the entries report any
// recognised marker the checker returns NA. Per-entry verdicts are
// stored in Detail for debugging.
type FortifyVfprintfFlagChecker struct {
	// ArgvPrefix is the argv token prefix; the template parses
	// `<prefix>:<entry>`. Default "printf".
	ArgvPrefix string
	// Entries narrows the swept printf entries; empty means use all
	// of `printfEntries`.
	Entries []string
}

const (
	defaultC01ArgvPrefix    = "printf"
	defaultC01TrappedMarker = "FORTIFY_C01_TRAPPED"
	defaultC01BypassMarker  = "FORTIFY_C01_BYPASS"
	defaultC01NAMarker      = "FORTIFY_C01_NA"
)

func (c *FortifyVfprintfFlagChecker) ID() string                  { return "INV-FORT-C01" }
func (c *FortifyVfprintfFlagChecker) Category() InvariantCategory { return CategoryDynamic }

func (c *FortifyVfprintfFlagChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryDynamic,
		SourceURL:   "https://nvd.nist.gov/vuln/detail/CVE-2012-0864",
		Sensitivity: "likely-to-drift",
		Detail:      map[string]any{},
	}
	if ctx == nil || ctx.Executor == nil || ctx.BinaryPath == "" {
		r.Verdict = VerdictNotApplicable
		r.Reason = "executor or binary path missing"
		return r
	}
	prefix := c.ArgvPrefix
	if prefix == "" {
		prefix = defaultC01ArgvPrefix
	}
	entries := c.Entries
	if len(entries) == 0 {
		entries = printfEntries
	}

	type perEntry struct {
		Verdict InvariantVerdict
		Line    string
		Exit    int
	}
	results := make(map[string]perEntry, len(entries))
	var fails []string
	var passes []string
	var nas []string

	for _, entry := range entries {
		argv := prefix + ":" + entry
		exit, stdout, _, err := ctx.Executor.ExecuteWithArgs(ctx.BinaryPath, argv)
		if err != nil {
			results[entry] = perEntry{Verdict: VerdictError, Exit: exit, Line: err.Error()}
			continue
		}
		switch {
		case strings.Contains(stdout, defaultC01BypassMarker):
			line := extractMarkerLine(stdout, defaultC01BypassMarker)
			results[entry] = perEntry{Verdict: VerdictFail, Line: line, Exit: exit}
			fails = append(fails, fmt.Sprintf("%s: %s", entry, line))
		case strings.Contains(stdout, defaultC01TrappedMarker):
			line := extractMarkerLine(stdout, defaultC01TrappedMarker)
			results[entry] = perEntry{Verdict: VerdictPass, Line: line, Exit: exit}
			passes = append(passes, entry)
		case strings.Contains(stdout, defaultC01NAMarker):
			line := extractMarkerLine(stdout, defaultC01NAMarker)
			results[entry] = perEntry{Verdict: VerdictNotApplicable, Line: line, Exit: exit}
			nas = append(nas, entry)
		default:
			results[entry] = perEntry{Verdict: VerdictNotApplicable, Exit: exit}
			nas = append(nas, entry)
		}
	}
	flat := make(map[string]any, len(results))
	for k, v := range results {
		flat[k] = fmt.Sprintf("verdict=%s exit=%d line=%q", v.Verdict, v.Exit, v.Line)
	}
	r.Detail["per_entry"] = flat
	r.Detail["entries_examined"] = entries

	if len(fails) > 0 {
		r.Verdict = VerdictFail
		r.Evidence = fmt.Sprintf(
			"%d printf entry/entries skipped fortify flag; first: %s",
			len(fails), fails[0])
		return r
	}
	if len(passes) > 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf(
			"%d printf entry/entries trapped on fortify check (%s); no bypass observed",
			len(passes), strings.Join(passes, ","))
		return r
	}
	r.Verdict = VerdictNotApplicable
	r.Reason = fmt.Sprintf(
		"no printf entry produced a recognizable C01 marker over %d probe(s)",
		len(entries))
	return r
}
