package oracle

import (
	"fmt"
)

// LastMemberObjectSizeChecker asserts INV-FORT-O01:
//
//	"BOS for an array that is the last member of a struct must return the
//	field's static byte size, not `(size_t)-1`."
//
// See `docs/tech-docs/invariants/fortify-source.md` §INV-FORT-O01.
//
// Detection is disasm-based: every `call __memcpy_chk` (and friends in
// the memcpy/strcpy/snprintf family) recovered by
// `FindFortifyChkCallSites` is inspected for the dstlen immediate. If
// any one of those resolves to `(size_t)-1` (0xFFFF_FFFF_FFFF_FFFF or
// the int64-domain value -1), the wrapper has been fed the BOS-fallback
// sentinel — exactly the GCC PR 101836 silent-bypass shape.
type LastMemberObjectSizeChecker struct{}

func (c *LastMemberObjectSizeChecker) ID() string                 { return "INV-FORT-O01" }
func (c *LastMemberObjectSizeChecker) Category() InvariantCategory { return CategoryStatic }

func (c *LastMemberObjectSizeChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryStatic,
		SourceURL:   "https://gcc.gnu.org/bugzilla/show_bug.cgi?id=101836",
		Sensitivity: "likely-to-drift",
		Detail:      map[string]any{},
	}
	sites, verdict, reason := loadFortifyCallSites(ctx, &r)
	if verdict != verdictUndecided {
		r.Verdict = verdict
		r.Reason = reason
		return r
	}

	var hits []string
	for _, s := range sites {
		if s.DstlenIsAllOnes {
			hits = append(hits, fmt.Sprintf("%s -> %s @0x%x dstlen=(size_t)-1",
				s.CallerFunc, s.ChkSymbol, s.SiteAddr))
		}
	}
	r.Detail["chk_callsites_examined"] = len(sites)
	r.Detail["bos_fallback_hits"] = hits

	if len(hits) > 0 {
		r.Verdict = VerdictFail
		r.Evidence = fmt.Sprintf(
			"BOS fell back to (size_t)-1 at %d call site(s); first: %s",
			len(hits), hits[0])
		return r
	}
	r.Verdict = VerdictPass
	r.Evidence = fmt.Sprintf(
		"examined %d fortify chk call site(s); no `(size_t)-1` dstlen observed",
		len(sites))
	return r
}

// CountedByObjectSizeChecker asserts INV-FORT-O02:
//
//	"For struct members marked `counted_by(n)`, BDOS at fortify chk call
//	sites must equal `n * sizeof(elem)`; specifically, BDOS must not
//	silently produce 0 (LLVM PR #110497 nested-pointer bug) or be off by
//	exactly 4 / `sizeof(struct) - offsetof(struct, arr)` (LLVM PR
//	#112636 whole-struct bug)."
//
// Static encoding: the seed template's `counted_by` argv mode embeds
// the *expected* dstlen as a magic constant the binary must match. The
// checker looks for the two failure shapes documented in those PRs:
//   - dstlen == 0     → Fail (PR #110497)
//   - dstlen == 4     → Fail (PR #112636 minimal off-by-sizeof(int))
//
// Any other value (including the expected count*sizeof) → Pass. The
// 0-and-4 set is intentionally narrow; widening it requires concrete
// evidence and would risk false positives on code paths that
// legitimately pass small dstlens.
type CountedByObjectSizeChecker struct{}

func (c *CountedByObjectSizeChecker) ID() string                 { return "INV-FORT-O02" }
func (c *CountedByObjectSizeChecker) Category() InvariantCategory { return CategoryStatic }

func (c *CountedByObjectSizeChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryStatic,
		SourceURL:   "https://github.com/llvm/llvm-project/pull/110497",
		Sensitivity: "target-specific",
		Detail:      map[string]any{},
	}
	sites, verdict, reason := loadFortifyCallSites(ctx, &r)
	if verdict != verdictUndecided {
		r.Verdict = verdict
		r.Reason = reason
		return r
	}

	var zeroHits, off4Hits []string
	for _, s := range sites {
		if s.DstlenImmediate < 0 {
			continue // not statically recovered
		}
		switch s.DstlenImmediate {
		case 0:
			zeroHits = append(zeroHits, fmt.Sprintf("%s -> %s @0x%x dstlen=0",
				s.CallerFunc, s.ChkSymbol, s.SiteAddr))
		case 4:
			off4Hits = append(off4Hits, fmt.Sprintf("%s -> %s @0x%x dstlen=4",
				s.CallerFunc, s.ChkSymbol, s.SiteAddr))
		}
	}
	r.Detail["chk_callsites_examined"] = len(sites)
	r.Detail["bdos_zero_hits"] = zeroHits
	r.Detail["bdos_off4_hits"] = off4Hits

	if len(zeroHits)+len(off4Hits) > 0 {
		r.Verdict = VerdictFail
		hit := ""
		if len(zeroHits) > 0 {
			hit = zeroHits[0]
		} else {
			hit = off4Hits[0]
		}
		r.Evidence = fmt.Sprintf(
			"counted_by BDOS produced known-bad dstlen at %d site(s); first: %s",
			len(zeroHits)+len(off4Hits), hit)
		return r
	}
	r.Verdict = VerdictPass
	r.Evidence = fmt.Sprintf(
		"examined %d fortify chk call site(s); no PR-110497 / PR-112636 BDOS shape observed",
		len(sites))
	return r
}

// StaleBDOSSizeChecker asserts INV-FORT-O03:
//
//	"BDOS at a fortify chk call site must use the SSA-latest value of
//	any size variable; reading a stale def from a predecessor BB is the
//	GCC PR 113514 silent-bypass shape."
//
// Detection: the GCC 14 PR 113514 reduced case produces a dstlen that
// is exactly 8 bytes larger than the correct value. The static signal
// we rely on is "the dstlen does not look like a multiple of common
// element sizes (1, 2, 4, 8) — it looks like `correct + 8`". Without
// explicit ground truth from the seed template we cannot prove staleness
// from the binary alone, so this checker is conservative: it scans for
// dstlens that are exactly 8 *more* than any other dstlen seen at a
// neighbouring call site, which is the clearest fingerprint of the PR.
//
// When no two call sites share a base value, the verdict is Pass with a
// note that the static signal is weak — this is acknowledged in the
// fortify-source.md plan as `disasm_confidence` low.
type StaleBDOSSizeChecker struct{}

func (c *StaleBDOSSizeChecker) ID() string                 { return "INV-FORT-O03" }
func (c *StaleBDOSSizeChecker) Category() InvariantCategory { return CategoryStatic }

func (c *StaleBDOSSizeChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryStatic,
		SourceURL:   "https://gcc.gnu.org/bugzilla/show_bug.cgi?id=113514",
		Sensitivity: "likely-to-drift",
		Detail:      map[string]any{},
	}
	sites, verdict, reason := loadFortifyCallSites(ctx, &r)
	if verdict != verdictUndecided {
		r.Verdict = verdict
		r.Reason = reason
		return r
	}

	// Group by family + caller; within a group, look for two call sites
	// whose dstlens differ by exactly 8. This is the public PR shape
	// (BDOS returned `correct + 8` once and `correct` the next time).
	type key struct {
		caller, family string
	}
	groups := make(map[key][]int64)
	for _, s := range sites {
		if s.DstlenImmediate < 0 {
			continue
		}
		k := key{s.CallerFunc, s.Family}
		groups[k] = append(groups[k], s.DstlenImmediate)
	}

	var hits []string
	for k, vs := range groups {
		for i := 0; i < len(vs); i++ {
			for j := i + 1; j < len(vs); j++ {
				diff := vs[i] - vs[j]
				if diff == 8 || diff == -8 {
					hits = append(hits, fmt.Sprintf(
						"%s -> __%s_chk dstlens={%d,%d} delta=8",
						k.caller, k.family, vs[i], vs[j]))
				}
			}
		}
	}
	r.Detail["chk_callsites_examined"] = len(sites)
	r.Detail["delta8_hits"] = hits

	if len(hits) > 0 {
		r.Verdict = VerdictFail
		r.Evidence = fmt.Sprintf(
			"BDOS-delta-8 fingerprint observed at %d site pair(s); first: %s",
			len(hits), hits[0])
		return r
	}
	// No paired delta-8: not enough signal to fail. Stay positive.
	r.Verdict = VerdictPass
	r.Evidence = fmt.Sprintf(
		"examined %d fortify chk call site(s); no PR-113514 delta-8 BDOS shape observed",
		len(sites))
	return r
}

// loadFortifyCallSites is the shared head used by O01/O02/O03. It maps
// inspector / disasm-backend errors to the right verdict and returns
// the call site list when the checker should proceed. The middle
// return value is `verdictUndecided` when the checker should examine
// `sites`, and the actual verdict otherwise.
const verdictUndecided = InvariantVerdict(99)

func loadFortifyCallSites(ctx *CheckContext, r *InvariantResult) ([]FortifyChkCallSite, InvariantVerdict, string) {
	if ctx == nil || ctx.Inspector == nil {
		return nil, VerdictNotApplicable, "no inspector available (missing binary path)"
	}
	machine, err := ctx.Inspector.Machine()
	if err != nil {
		return nil, naOrError(err), fmt.Sprintf("inspector.Machine failed: %v", err)
	}
	class, err := ctx.Inspector.Class()
	if err != nil {
		return nil, naOrError(err), fmt.Sprintf("inspector.Class failed: %v", err)
	}
	if r.Detail == nil {
		r.Detail = map[string]any{}
	}
	r.Detail["machine"] = machine.String()
	r.Detail["class"] = class.String()
	if !SupportsFortifyDisasm(machine, class) {
		return nil, VerdictNotApplicable, fmt.Sprintf(
			"FORTIFY disasm backend not implemented for (machine=%s, class=%s); only x86_64 today",
			machine, class)
	}
	sites, err := FindFortifyChkCallSites(ctx.Inspector)
	if err != nil {
		return nil, naOrError(err), fmt.Sprintf("disasm scan failed: %v", err)
	}
	if len(sites) == 0 {
		return nil, VerdictNotApplicable, "binary has no `__<family>_chk` call sites; checker has nothing to assert"
	}
	return sites, verdictUndecided, ""
}
