package oracle

import (
	"errors"
	"fmt"
	"strings"
)

// StackChkSymbolsChecker asserts INV-SP-G01:
//
//	"Default guard is the external variable __stack_chk_guard; failure handler
//	is __stack_chk_fail (must be noreturn). When the binary is dynamically
//	linked against glibc with stack canary enabled, both symbols (or at least
//	__stack_chk_fail) must be present as undefined imports."
//
// See `@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md:174-186`.
//
// This is a *static* invariant: it inspects the ELF symbol table without
// executing the binary. It is the smoke test for "did `-fstack-protector*`
// actually take effect at link time?". A binary lacking `__stack_chk_fail`
// after compiling with `-fstack-protector-strong` either:
//   - was linked against a libc that doesn't supply it (config bug), or
//   - had the canary completely optimized away (regression), or
//   - the seed function had no vulnerable objects at all (legitimate).
//
// We can't distinguish (1)(2)(3) from a single binary; the verdict is
// therefore Pass (symbol present) / NotApplicable (symbol absent, ambiguous
// reason). A future companion checker can promote some NA cases to Fail by
// cross-checking the seed source for `char buf[N]` patterns.
type StackChkSymbolsChecker struct{}

// ID implements InvariantChecker.
func (c *StackChkSymbolsChecker) ID() string { return "INV-SP-G01" }

// Category implements InvariantChecker.
func (c *StackChkSymbolsChecker) Category() InvariantCategory { return CategoryStatic }

// Check implements InvariantChecker.
func (c *StackChkSymbolsChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryStatic,
		SourceURL:   "https://gcc.gnu.org/onlinedocs/gccint/Stack-Smashing-Protection.html",
		Sensitivity: "stable",
		Detail:      map[string]any{},
	}

	if ctx == nil || ctx.Inspector == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no inspector available (missing binary path)"
		return r
	}

	imports, err := ctx.Inspector.ImportedFunctions()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector failed: %v", err)
		return r
	}

	hasFail := false
	hasGuard := false
	syms, _ := ctx.Inspector.Symbols()
	for _, s := range imports {
		if s == "__stack_chk_fail" || s == "__stack_chk_fail_local" {
			hasFail = true
		}
	}
	for _, s := range syms {
		if s == "__stack_chk_guard" {
			hasGuard = true
		}
	}
	r.Detail["has_stack_chk_fail"] = hasFail
	r.Detail["has_stack_chk_guard"] = hasGuard

	if hasFail {
		r.Verdict = VerdictPass
		r.Evidence = "binary references __stack_chk_fail; SP runtime contract is wired up at link time"
		return r
	}

	r.Verdict = VerdictNotApplicable
	r.Reason = "no __stack_chk_fail import: SP either disabled, optimized away, or seed has no vulnerable objects"
	return r
}

// MainNoCanaryChecker asserts INV-SP-A01:
//
//	"`__attribute__((no_stack_protector))` overrides every -fstack-protector*;
//	the function does not insert a canary nor call __stack_chk_fail."
//
// See `@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md:268-277`.
//
// In the Canary oracle template (`docs/oracles/canary-oracle.md` §"caller 最大恶意"),
// `main` is annotated with `NO_CANARY = __attribute__((no_stack_protector))`
// to guarantee the seed-callee is the only function that can trigger the
// canary trap. This checker validates that requirement at the binary level:
// when we look at imports, the *presence* of `__stack_chk_fail` is fine
// (callee may need it), but `main` itself should not be the caller of it.
//
// A precise check would disassemble `main` and look for a call to
// `__stack_chk_fail`. We avoid disassembly here (cross-arch cost too high
// for a first cut) and instead use a coarser heuristic:
//
//   - If the binary defines `main` (it should), and there exists any call
//     to `__stack_chk_fail` from the binary, we cannot tell from symbols
//     alone whether `main` itself is a caller. We report
//     VerdictNotApplicable with a Reason that names the limitation,
//     leaving room for a future disassembly-based companion checker.
//   - If the binary doesn't import `__stack_chk_fail` at all, INV-SP-A01
//     trivially holds (no canary = no caller = main is fine). Pass.
//
// This is honest about its current limitation rather than producing
// false-positive Passes; see
// `docs/architecture/oracle-multi-invariant-redesign.md` §2.3.D on the
// importance of NotApplicable transparency.
type MainNoCanaryChecker struct{}

// ID implements InvariantChecker.
func (c *MainNoCanaryChecker) ID() string { return "INV-SP-A01" }

// Category implements InvariantChecker.
func (c *MainNoCanaryChecker) Category() InvariantCategory { return CategoryStatic }

// Check implements InvariantChecker.
func (c *MainNoCanaryChecker) Check(ctx *CheckContext) InvariantResult {
	r := InvariantResult{
		ID:          c.ID(),
		Category:    CategoryStatic,
		SourceURL:   "https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html",
		Sensitivity: "stable",
		Detail:      map[string]any{},
	}

	if ctx == nil || ctx.Inspector == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no inspector available (missing binary path)"
		return r
	}

	syms, err := ctx.Inspector.Symbols()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector failed: %v", err)
		return r
	}

	hasMain := false
	for _, s := range syms {
		if s == "main" {
			hasMain = true
			break
		}
	}
	r.Detail["has_main_symbol"] = hasMain
	if !hasMain {
		r.Verdict = VerdictNotApplicable
		r.Reason = "binary does not define a 'main' symbol; cannot verify NO_CANARY attribute"
		return r
	}

	imports, err := ctx.Inspector.ImportedFunctions()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector failed: %v", err)
		return r
	}
	importsFail := false
	for _, s := range imports {
		if strings.HasPrefix(s, "__stack_chk_") {
			importsFail = true
			break
		}
	}
	r.Detail["binary_imports_stack_chk"] = importsFail

	if !importsFail {
		// Whole binary has no stack_chk_* imports → main can't possibly
		// call them → INV-SP-A01 holds.
		r.Verdict = VerdictPass
		r.Evidence = "binary has no __stack_chk_* imports; main cannot call SP runtime"
		return r
	}

	// Some function in the binary imports __stack_chk_*. Without
	// per-function disassembly we cannot prove main is or isn't a caller.
	r.Verdict = VerdictNotApplicable
	r.Reason = "binary imports __stack_chk_*; need per-function disassembly to verify main itself does not call SP runtime (follow-up checker)"
	return r
}

// naOrError maps inspector errors to NotApplicable for missing-binary cases
// and Error for actual parse failures.
func naOrError(err error) InvariantVerdict {
	if errors.Is(err, ErrBinaryMissing) || errors.Is(err, ErrNotELF) {
		return VerdictNotApplicable
	}
	return VerdictError
}
