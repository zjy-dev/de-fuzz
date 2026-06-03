package oracle

import (
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// vlaSeed returns a seed whose source has a VLA (so SeedShape.HasVLA
// is true). Used to satisfy L02's precondition.
func vlaSeed() *seed.Seed {
	return &seed.Seed{Content: `void seed(int n) { char buf[n]; (void)buf; }`}
}

// allocaSeed returns a seed whose source uses __builtin_alloca.
func allocaSeed() *seed.Seed {
	return &seed.Seed{Content: `void seed(int n) { void *p = __builtin_alloca(n); (void)p; }`}
}

// fixedSeed returns a seed with only a fixed-size buffer (no VLA / alloca).
func fixedSeed() *seed.Seed {
	return &seed.Seed{Content: `void seed(void) { char buf[64]; (void)buf; }`}
}

// TestDynamicAllocLayoutChecker_VLABypassIsFail — the canonical CVE-2023-4039
// pattern: VLA seed, L01 reports SIGSEGV with sentinel → Fail.
func TestDynamicAllocLayoutChecker_VLABypassIsFail(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
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
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail on VLA + SIGSEGV+sentinel, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "CVE-2023-4039") {
		t.Errorf("Evidence should reference CVE-2023-4039; got %q", r.Evidence)
	}
	if !strings.Contains(r.Evidence, "fill_size=100") {
		t.Errorf("Evidence should preserve crash size; got %q", r.Evidence)
	}
}

// TestDynamicAllocLayoutChecker_AllocaSIGBUSIsFail — alloca path, SIGBUS
// with sentinel still flagged as Fail.
func TestDynamicAllocLayoutChecker_AllocaSIGBUSIsFail(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	ctx := &CheckContext{
		Seed: allocaSeed(),
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
		t.Fatalf("expected Fail on alloca + SIGBUS+sentinel, got %s", r.Verdict)
	}
}

// TestDynamicAllocLayoutChecker_VLASIGABRTIsPass — VLA seed with the
// runtime aborting via SIGABRT means the layout invariant held: the
// canary trapped before retaddr was reached.
func TestDynamicAllocLayoutChecker_VLASIGABRTIsPass(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	ctx := &CheckContext{
		Seed: vlaSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  64,
				CrashExitCode: ExitCodeSIGABRT,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on VLA + SIGABRT, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "SIGABRT") {
		t.Errorf("Evidence should mention SIGABRT; got %q", r.Evidence)
	}
}

// TestDynamicAllocLayoutChecker_NoVLAIsNA — a seed without VLA / alloca
// is outside L02's scope; must report NA, never Fail.
func TestDynamicAllocLayoutChecker_NoVLAIsNA(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	ctx := &CheckContext{
		Seed: fixedSeed(),
		Cache: map[string]any{
			// Even with a juicy SIGSEGV+sentinel, L02 must not fire on
			// fixed-only seeds — that's L01's territory.
			dynamicSearchCacheKey: &dynamicSearchResult{
				MinCrashSize:  100,
				CrashExitCode: ExitCodeSIGSEGV,
				HasSentinel:   true,
			},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on fixed-buffer seed, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "VLA / alloca") {
		t.Errorf("Reason should explain inapplicability; got %q", r.Reason)
	}
}

// TestDynamicAllocLayoutChecker_SIGSEGVNoSentinelIsNA — VLA seed, SIGSEGV
// but no sentinel ⇒ probably intra-seed crash, not retaddr overwrite.
func TestDynamicAllocLayoutChecker_SIGSEGVNoSentinelIsNA(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	ctx := &CheckContext{
		Seed: vlaSeed(),
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
		t.Fatalf("expected NotApplicable on SIGSEGV without sentinel, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "intra-seed") {
		t.Errorf("Reason should label crash as intra-seed; got %q", r.Reason)
	}
}

// TestDynamicAllocLayoutChecker_NoCacheIsNA — VLA seed but L01 didn't run.
func TestDynamicAllocLayoutChecker_NoCacheIsNA(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	ctx := &CheckContext{
		Seed:  vlaSeed(),
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

// TestDynamicAllocLayoutChecker_NoCrashIsNA — VLA seed, search bound
// too small to observe any crash.
func TestDynamicAllocLayoutChecker_NoCrashIsNA(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	ctx := &CheckContext{
		Seed: vlaSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: &dynamicSearchResult{MinCrashSize: -1},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on no-crash, got %s", r.Verdict)
	}
}

// TestDynamicAllocLayoutChecker_CacheTypeMismatch — defensive guard.
func TestDynamicAllocLayoutChecker_CacheTypeMismatch(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	ctx := &CheckContext{
		Seed: vlaSeed(),
		Cache: map[string]any{
			dynamicSearchCacheKey: 42,
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictError {
		t.Fatalf("expected Error on cache type mismatch, got %s", r.Verdict)
	}
}

// TestDynamicAllocLayoutChecker_PolarityTagged — Detail must include
// polarity_sensitive=true so the aggregator inverts on negative control.
func TestDynamicAllocLayoutChecker_PolarityTagged(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	ctx := &CheckContext{
		Seed: vlaSeed(),
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

// TestDynamicAllocLayoutChecker_NilContextIsNA — must not panic.
func TestDynamicAllocLayoutChecker_NilContextIsNA(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	r := c.Check(nil)
	if r.Verdict != VerdictNotApplicable {
		t.Errorf("expected NotApplicable on nil ctx, got %s", r.Verdict)
	}
}

// TestDynamicAllocLayoutChecker_DefaultsAreSane.
func TestDynamicAllocLayoutChecker_DefaultsAreSane(t *testing.T) {
	c := &DynamicAllocLayoutChecker{}
	if c.ID() != "INV-SP-L02" {
		t.Errorf("default ID() = %q, want INV-SP-L02", c.ID())
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
