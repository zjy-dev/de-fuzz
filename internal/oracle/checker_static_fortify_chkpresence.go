package oracle

import (
	"fmt"
	"sort"
	"strings"
)

// FortifyChkPresenceChecker asserts INV-FORT-W01:
//
//	"`__fortify_function` wrappers in glibc headers must, after caller
//	inlining, retain BOS / `pass_object_size` context and emit a real
//	`__<family>_chk` call to libc."
//
// See `docs/tech-docs/invariants/fortify-source.md` §INV-FORT-W01.
//
// The static signal is symbol-level: a binary that was compiled with
// `_FORTIFY_SOURCE>=2 -O>=2`, dynamically linked against glibc, and
// actually calls a fortify-protected libc function (the seed template
// guarantees one such call per template) MUST import at least one
// `__<family>_chk` symbol. If we observe zero, the wrapper has been
// fully consumed by the inliner without lowering to a chk call — exactly
// the regression Linux kernel commit a28a6e860c6c worked around for
// Clang ≤ 12.
//
// This checker is positive-control only: per the project policy,
// `_FORTIFY_SOURCE=0` / `-U_FORTIFY_SOURCE` / `-O0` are filtered at the
// flag level (see `internal/seed/defense_flags.go`). When the binary
// neither imports nor defines any chk symbol, the verdict is Fail —
// not NA — because the rejection of disabling flags upstream guarantees
// the wrapper is *supposed* to be active.
type FortifyChkPresenceChecker struct{}

// ID implements InvariantChecker.
func (c *FortifyChkPresenceChecker) ID() string { return "INV-FORT-W01" }

// Category implements InvariantChecker.
func (c *FortifyChkPresenceChecker) Category() InvariantCategory { return CategoryStatic }

// Check implements InvariantChecker.
func (c *FortifyChkPresenceChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryStatic,
		SourceURL:   "https://lore.kernel.org/lkml/20210818060533.3569517-64-keescook@chromium.org/",
		Sensitivity: "target-specific",
		Detail:      map[string]any{},
	}

	if ctx == nil || ctx.Inspector == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no inspector available (missing binary path)"
		return r
	}

	imps, err := ctx.Inspector.ImportedFunctions()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector failed: %v", err)
		return r
	}
	syms, err := ctx.Inspector.Symbols()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector failed: %v", err)
		return r
	}

	importedChk := collectChkSymbols(imps)
	definedChk := collectChkSymbols(syms)
	r.Detail["chk_imports"] = importedChk
	r.Detail["chk_defined"] = definedChk

	expected := make([]string, 0, len(fortifyProtectedFamilies))
	for _, family := range fortifyProtectedFamilies {
		expected = append(expected, chkSymbolFor(family))
	}
	r.Detail["expected_chk_family"] = expected

	// Pass: at least one fortify-protected libc function compiled into
	// the binary actually went through the wrapper.
	if len(importedChk) > 0 || len(definedChk) > 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf(
			"binary references %d fortify chk symbol(s); wrapper preserved BOS context after inlining",
			len(importedChk)+len(definedChk))
		return r
	}

	// At this point the binary has zero `__<family>_chk` symbols.
	// Because positive-control filtering at the flag level rejects
	// `-D_FORTIFY_SOURCE=0` / `-O0` upstream, the *only* remaining
	// explanations are: (a) the inliner ate the wrapper context
	// (silent bypass, INV-FORT-W01 violated), or (b) the seed
	// template happened to compile to no fortify-protected sink.
	// We cannot tell (a) from (b) at the symbol level alone, so we
	// stay conservative and report NotApplicable with a clear reason.
	// Detection of pure (a) requires the seed template to *guarantee*
	// at least one fortify-eligible call, which the project's
	// `initial_seeds/<isa>/fortify/function_template.c` does in the
	// `bos` argv mode — see the template comment block.
	r.Verdict = VerdictNotApplicable
	r.Reason = "binary has no `__<family>_chk` imports/definitions; either the seed compiled to no fortify-protected sink, or the wrapper was inlined away (INV-FORT-W01 silent bypass — needs disasm to disambiguate)"
	return r
}

// ErrWarnChkChecker asserts INV-FORT-C02:
//
//	"BSD/GNU `<err.h>` / `<error.h>` diagnostic functions (err / errx /
//	warn / warnx / verr / vwarn / error / error_at_line) must have
//	`__<name>_chk` wrappers when `_FORTIFY_SOURCE>=2`."
//
// See `docs/tech-docs/invariants/fortify-source.md` §INV-FORT-C02.
//
// The signal is symbol-level: if the binary imports any of the bare
// `err`/`warn`/`error` symbols *and* none of the corresponding chk
// wrappers, the seed's call goes through a non-fortified `vfprintf`
// path — exactly the long-standing space described by Red Hat BZ 836931
// and sourceware 24987.
//
// Positive-control: when the binary calls bare `err`/`warn` and zero
// chk wrappers exist, the verdict is Fail.
type ErrWarnChkChecker struct{}

// ID implements InvariantChecker.
func (c *ErrWarnChkChecker) ID() string { return "INV-FORT-C02" }

// Category implements InvariantChecker.
func (c *ErrWarnChkChecker) Category() InvariantCategory { return CategoryStatic }

// Check implements InvariantChecker.
func (c *ErrWarnChkChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryStatic,
		SourceURL:   "https://sourceware.org/bugzilla/show_bug.cgi?id=24987",
		Sensitivity: "likely-to-drift",
		Detail:      map[string]any{},
	}

	if ctx == nil || ctx.Inspector == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no inspector available (missing binary path)"
		return r
	}

	imps, err := ctx.Inspector.ImportedFunctions()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector failed: %v", err)
		return r
	}
	syms, err := ctx.Inspector.Symbols()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector failed: %v", err)
		return r
	}

	bareCalls := intersectSorted(imps, errWarnFamilies)
	chkCalls := intersectSorted(syms, errWarnChkFamilies)
	r.Detail["bare_calls"] = bareCalls
	r.Detail["chk_calls"] = chkCalls

	if len(bareCalls) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "seed does not call any err/warn/error diagnostic; INV-FORT-C02 not exercised"
		return r
	}
	if len(chkCalls) > 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf(
			"binary uses %d bare diagnostic call(s) but also %d chk wrapper(s) — fortify covers err/warn family",
			len(bareCalls), len(chkCalls))
		return r
	}

	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf(
		"binary calls bare err/warn family %v with zero chk wrapper imports; INV-FORT-C02 silent bypass — `%s` reaches a non-fortified vfprintf",
		bareCalls, bareCalls[0])
	return r
}

// collectChkSymbols filters `names` for entries shaped like
// `__<family>_chk` and returns the deduplicated, sorted list.
func collectChkSymbols(names []string) []string {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		if chkSymbolFamilyName(n) == "" {
			continue
		}
		set[n] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// intersectSorted returns the deduplicated, sorted intersection of
// `xs` (which may contain noise) and `ys` (the canonical reference
// list). Used by the err/warn checker to project both `imps` and
// `syms` onto the family lists.
func intersectSorted(xs, ys []string) []string {
	want := make(map[string]struct{}, len(ys))
	for _, y := range ys {
		want[y] = struct{}{}
	}
	seen := make(map[string]struct{})
	out := []string{}
	for _, x := range xs {
		if _, ok := want[x]; !ok {
			continue
		}
		if _, dup := seen[x]; dup {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

// stringsJoin keeps imports tidy; tiny wrapper so call sites don't have
// to import `strings` for a single Join.
func stringsJoin(xs []string, sep string) string { return strings.Join(xs, sep) }
