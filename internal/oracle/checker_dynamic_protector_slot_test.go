package oracle

import (
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TestProtectorSlotRelocationChecker_FixedBufferBypassIsFail — fixed
// buffer seed, SIGSEGV+sentinel at L01 boundary → Fail (suspected
// protector-slot relocation, CERT VU#129209).
func TestProtectorSlotRelocationChecker_FixedBufferBypassIsFail(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed: fixedSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  100,
				CrashExitCode: ExitCodeSIGSEGV,
				HasSentinel:   true,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail on fixed-buffer + SIGSEGV+sentinel, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "VU#129209") {
		t.Errorf("Evidence should reference VU#129209; got %q", r.Evidence)
	}
}

// TestProtectorSlotRelocationChecker_MixedSeedAlsoFires — L04 fires for
// any fixed-buffer-bearing seed, including mixed (L03 will also flag
// the same observation under a different ID; deduplication is the
// aggregator's job, not the individual checker's).
func TestProtectorSlotRelocationChecker_MixedSeedAlsoFires(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed: mixedSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  100,
				CrashExitCode: ExitCodeSIGSEGV,
				HasSentinel:   true,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail on mixed seed (has fixed buffer), got %s", r.Verdict)
	}
}

// TestProtectorSlotRelocationChecker_FixedSIGABRTIsPass — fixed buffer
// + SIGABRT means canary trapped → Pass.
func TestProtectorSlotRelocationChecker_FixedSIGABRTIsPass(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed: fixedSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  64,
				CrashExitCode: ExitCodeSIGABRT,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on fixed + SIGABRT, got %s", r.Verdict)
	}
	if !strings.Contains(r.Evidence, "below locals") {
		t.Errorf("Evidence should mention slot below locals; got %q", r.Evidence)
	}
}

// TestProtectorSlotRelocationChecker_VLAOnlyIsNA — pure VLA seed has no
// frame-indexed local; L02 owns that root cause, not L04.
func TestProtectorSlotRelocationChecker_VLAOnlyIsNA(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed: vlaSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  100,
				CrashExitCode: ExitCodeSIGSEGV,
				HasSentinel:   true,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on VLA-only seed, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "fixed-size vulnerable buffer") {
		t.Errorf("Reason should explain inapplicability; got %q", r.Reason)
	}
}

// TestProtectorSlotRelocationChecker_AllocaOnlyIsNA — alloca-only seed
// also lacks the fixed-buffer precondition.
func TestProtectorSlotRelocationChecker_AllocaOnlyIsNA(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed: allocaSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  100,
				CrashExitCode: ExitCodeSIGSEGV,
				HasSentinel:   true,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on alloca-only seed, got %s", r.Verdict)
	}
}

// TestProtectorSlotRelocationChecker_NoCacheIsNA.
func TestProtectorSlotRelocationChecker_NoCacheIsNA(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed:  fixedSeed(),
		Cache: map[string]any{},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on missing cache, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "L01 did not run") {
		t.Errorf("Reason should mention L01; got %q", r.Reason)
	}
}

// TestProtectorSlotRelocationChecker_NoCrashIsNA.
func TestProtectorSlotRelocationChecker_NoCrashIsNA(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed: fixedSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{MinCrashSize: -1},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on no-crash, got %s", r.Verdict)
	}
}

// TestProtectorSlotRelocationChecker_SIGSEGVNoSentinelIsNA.
func TestProtectorSlotRelocationChecker_SIGSEGVNoSentinelIsNA(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed: fixedSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  74,
				CrashExitCode: ExitCodeSIGSEGV,
				HasSentinel:   false,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on SIGSEGV w/o sentinel, got %s", r.Verdict)
	}
}

// TestProtectorSlotRelocationChecker_CacheTypeMismatch.
func TestProtectorSlotRelocationChecker_CacheTypeMismatch(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed:  fixedSeed(),
		Cache: map[string]any{dynamicSearchCacheKey: 42},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictError {
		t.Fatalf("expected Error on cache type mismatch, got %s", r.Verdict)
	}
}

// TestProtectorSlotRelocationChecker_UnexpectedExitIsNA.
func TestProtectorSlotRelocationChecker_UnexpectedExitIsNA(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed: fixedSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  200,
				CrashExitCode: 137, // SIGKILL
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on unexpected exit, got %s", r.Verdict)
	}
}

// TestProtectorSlotRelocationChecker_PolarityTagged.
func TestProtectorSlotRelocationChecker_PolarityTagged(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed: fixedSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  64,
				CrashExitCode: ExitCodeSIGABRT,
			},
		},
	}
	r := c.Check(ctx)
	if v, ok := r.Detail["polarity_sensitive"].(bool); !ok || !v {
		t.Errorf("Detail[polarity_sensitive] must be true; got %v", r.Detail["polarity_sensitive"])
	}
}

// TestProtectorSlotRelocationChecker_NilContextIsNA.
func TestProtectorSlotRelocationChecker_NilContextIsNA(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	r := c.Check(nil)
	if r.Verdict != VerdictNotApplicable {
		t.Errorf("expected NotApplicable on nil ctx, got %s", r.Verdict)
	}
}

// TestProtectorSlotRelocationChecker_NilSeedIsNA — empty seed, classify
// returns zero shape, no fixed buffer present.
func TestProtectorSlotRelocationChecker_NilSeedIsNA(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	ctx := &CheckContext{
		Seed:  &seed.Seed{},
		Cache: map[string]any{},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Errorf("expected NotApplicable on empty seed, got %s", r.Verdict)
	}
}

// TestProtectorSlotRelocationChecker_DefaultsAreSane.
func TestProtectorSlotRelocationChecker_DefaultsAreSane(t *testing.T) {
	c := &ProtectorSlotRelocationChecker{}
	if c.ID() != "INV-SP-L04" {
		t.Errorf("default ID() = %q, want INV-SP-L04", c.ID())
	}
	if c.Category() != CategoryDynamic {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryDynamic)
	}
	if c.sourceURL() == "" {
		t.Error("default sourceURL() must be non-empty")
	}
	if c.sensitivity() == "" {
		t.Error("default sensitivity() must be non-empty")
	}
}
