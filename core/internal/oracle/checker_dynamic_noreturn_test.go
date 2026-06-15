package oracle

import (
	"strings"
	"testing"
)

// TestStackChkFailNoreturnChecker_SIGABRTIsPass — the canonical positive
// case: L01 search ended on SIGABRT (exit 134) → V02 reports Pass with
// evidence that __stack_chk_fail terminated the process.
func TestStackChkFailNoreturnChecker_SIGABRTIsPass(t *testing.T) {
	c := &StackChkFailNoreturnChecker{}
	ctx := &CheckContext{Cache: map[string]any{
		dynamicSearchCacheKey: &dynamicSearchResult{
			MinCrashSize:  64,
			CrashExitCode: ExitCodeSIGABRT,
			HasSentinel:   false,
		},
	}}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on SIGABRT, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "SIGABRT") {
		t.Errorf("Evidence should mention SIGABRT; got %q", r.Evidence)
	}
	if !strings.Contains(r.Evidence, "fill_size=64") {
		t.Errorf("Evidence should preserve crash size; got %q", r.Evidence)
	}
}

// TestStackChkFailNoreturnChecker_SIGSEGVIsNA — when L01 itself detects
// a bypass (SIGSEGV with sentinel), V02 cannot confirm or refute the
// noreturn contract because the fail handler was never reached.
func TestStackChkFailNoreturnChecker_SIGSEGVIsNA(t *testing.T) {
	c := &StackChkFailNoreturnChecker{}
	ctx := &CheckContext{Cache: map[string]any{
		dynamicSearchCacheKey: &dynamicSearchResult{
			MinCrashSize:  100,
			CrashExitCode: ExitCodeSIGSEGV,
			HasSentinel:   true,
		},
	}}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on SIGSEGV, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "fail handler was never reached") {
		t.Errorf("Reason should explain the gap; got %q", r.Reason)
	}
}

// TestStackChkFailNoreturnChecker_NoCrashIsNA — search finished without
// ever observing a crash → NA, no claim either way.
func TestStackChkFailNoreturnChecker_NoCrashIsNA(t *testing.T) {
	c := &StackChkFailNoreturnChecker{}
	ctx := &CheckContext{Cache: map[string]any{
		dynamicSearchCacheKey: &dynamicSearchResult{
			MinCrashSize: -1,
		},
	}}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on no-crash, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "no crash observed") {
		t.Errorf("Reason should mention no crash; got %q", r.Reason)
	}
}

// TestStackChkFailNoreturnChecker_NoCacheIsNA — V02 must run AFTER L01
// in the mechanism's Checkers slice. If the cache key is absent (e.g.,
// an oracle wires V02 without L01) the result is NA, never Fail.
func TestStackChkFailNoreturnChecker_NoCacheIsNA(t *testing.T) {
	c := &StackChkFailNoreturnChecker{}
	ctx := &CheckContext{Cache: map[string]any{}}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on missing cache, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "L01 did not run") {
		t.Errorf("Reason should explain missing cache; got %q", r.Reason)
	}
}

// TestStackChkFailNoreturnChecker_CacheTypeMismatch — defensive: if some
// other code stored a non-result under the same key, surface as Error
// rather than silently passing.
func TestStackChkFailNoreturnChecker_CacheTypeMismatch(t *testing.T) {
	c := &StackChkFailNoreturnChecker{}
	ctx := &CheckContext{Cache: map[string]any{
		dynamicSearchCacheKey: "not a result",
	}}
	r := c.Check(ctx)
	if r.Verdict != VerdictError {
		t.Fatalf("expected Error on type mismatch, got %s", r.Verdict)
	}
}

// TestStackChkFailNoreturnChecker_UnexpectedExitIsNA — exotic exit code
// (e.g., signal 9 / 15 from external kill) → NA, not Pass and not Fail.
func TestStackChkFailNoreturnChecker_UnexpectedExitIsNA(t *testing.T) {
	c := &StackChkFailNoreturnChecker{}
	ctx := &CheckContext{Cache: map[string]any{
		dynamicSearchCacheKey: &dynamicSearchResult{
			MinCrashSize:  200,
			CrashExitCode: 137, // SIGKILL
		},
	}}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on unexpected exit, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "unexpected exit") {
		t.Errorf("Reason should label the exit unexpected; got %q", r.Reason)
	}
}

// TestStackChkFailNoreturnChecker_DefaultsAreSane — zero-value checker
// must self-identify as INV-SP-V02 / Dynamic.
func TestStackChkFailNoreturnChecker_DefaultsAreSane(t *testing.T) {
	c := &StackChkFailNoreturnChecker{}
	if c.ID() != "INV-SP-V02" {
		t.Errorf("default ID() = %q, want INV-SP-V02", c.ID())
	}
	if c.Category() != CategoryDynamic {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryDynamic)
	}
}

// TestStackChkFailNoreturnChecker_NilContextIsNA — must not panic.
func TestStackChkFailNoreturnChecker_NilContextIsNA(t *testing.T) {
	c := &StackChkFailNoreturnChecker{}
	r := c.Check(nil)
	if r.Verdict != VerdictNotApplicable {
		t.Errorf("expected NotApplicable on nil context, got %s", r.Verdict)
	}
}
