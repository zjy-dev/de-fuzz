package oracle

import (
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// mixedSeed returns a seed with both a fixed buffer and a VLA.
func mixedSeed() *seed.Seed {
	return &seed.Seed{Content: `void seed(int n) {
		char fixed[64];
		char vla[n];
		(void)fixed; (void)vla;
	}`}
}

// mixedFixedAllocaSeed returns a seed with a fixed buffer + alloca.
func mixedFixedAllocaSeed() *seed.Seed {
	return &seed.Seed{Content: `void seed(int n) {
		char fixed[32];
		void *p = __builtin_alloca(n);
		(void)fixed; (void)p;
	}`}
}

// TestMixedVulnerableObjectsChecker_BypassIsFail — mixed seed, SIGSEGV
// with sentinel at L01 boundary → Fail (L03 protection-plane violation).
func TestMixedVulnerableObjectsChecker_BypassIsFail(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
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
		t.Fatalf("expected Fail on mixed + SIGSEGV+sentinel, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "wrong side of the canary plane") {
		t.Errorf("Evidence should mention canary plane; got %q", r.Evidence)
	}
}

// TestMixedVulnerableObjectsChecker_FixedAllocaSIGBUSIsFail — fixed +
// alloca seed, SIGBUS+sentinel → Fail.
func TestMixedVulnerableObjectsChecker_FixedAllocaSIGBUSIsFail(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
	ctx := &CheckContext{
		Seed: mixedFixedAllocaSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  120,
				CrashExitCode: ExitCodeSIGBUS,
				HasSentinel:   true,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail on fixed+alloca + SIGBUS+sentinel, got %s", r.Verdict)
	}
}

// TestMixedVulnerableObjectsChecker_SIGABRTIsPass — mixed seed but
// runtime aborted via canary → Pass.
func TestMixedVulnerableObjectsChecker_SIGABRTIsPass(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
	ctx := &CheckContext{
		Seed: mixedSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  64,
				CrashExitCode: ExitCodeSIGABRT,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on mixed + SIGABRT, got %s", r.Verdict)
	}
	if !strings.Contains(r.Evidence, "shared canary plane held") {
		t.Errorf("Evidence should mention shared canary plane; got %q", r.Evidence)
	}
}

// TestMixedVulnerableObjectsChecker_FixedOnlyIsNA — pure fixed-buffer
// seed lacks the mixed-flavor precondition; must report NA.
func TestMixedVulnerableObjectsChecker_FixedOnlyIsNA(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
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
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on fixed-only seed, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "mixed") {
		t.Errorf("Reason should explain mixed precondition; got %q", r.Reason)
	}
}

// TestMixedVulnerableObjectsChecker_VLAOnlyIsNA — pure VLA seed (one
// flavor) → NA. L02 owns that case.
func TestMixedVulnerableObjectsChecker_VLAOnlyIsNA(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
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
}

// TestMixedVulnerableObjectsChecker_NoCacheIsNA — mixed seed but no L01.
func TestMixedVulnerableObjectsChecker_NoCacheIsNA(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
	ctx := &CheckContext{
		Seed:  mixedSeed(),
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

// TestMixedVulnerableObjectsChecker_NoCrashIsNA.
func TestMixedVulnerableObjectsChecker_NoCrashIsNA(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
	ctx := &CheckContext{
		Seed: mixedSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{MinCrashSize: -1},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on no-crash, got %s", r.Verdict)
	}
}

// TestMixedVulnerableObjectsChecker_SIGSEGVNoSentinelIsNA — intra-seed
// crash without sentinel must not Fail.
func TestMixedVulnerableObjectsChecker_SIGSEGVNoSentinelIsNA(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
	ctx := &CheckContext{
		Seed: mixedSeed(),
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

// TestMixedVulnerableObjectsChecker_CacheTypeMismatch.
func TestMixedVulnerableObjectsChecker_CacheTypeMismatch(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
	ctx := &CheckContext{
		Seed:  mixedSeed(),
		Cache: map[string]any{dynamicSearchCacheKey: "garbage"},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictError {
		t.Fatalf("expected Error on cache type mismatch, got %s", r.Verdict)
	}
}

// TestMixedVulnerableObjectsChecker_NilContextIsNA.
func TestMixedVulnerableObjectsChecker_NilContextIsNA(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
	r := c.Check(nil)
	if r.Verdict != VerdictNotApplicable {
		t.Errorf("expected NotApplicable on nil ctx, got %s", r.Verdict)
	}
}

// TestMixedVulnerableObjectsChecker_DefaultsAreSane.
func TestMixedVulnerableObjectsChecker_DefaultsAreSane(t *testing.T) {
	c := &MixedVulnerableObjectsChecker{}
	if c.ID() != "INV-SP-L03" {
		t.Errorf("default ID() = %q, want INV-SP-L03", c.ID())
	}
	if c.Category() != CategoryDynamic {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryDynamic)
	}
}
