package oracle

import (
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TestVLAAllocaInstrumentationChecker_VLAWithFailIsPass — the canonical
// positive: VLA seed compiled with stack protector → binary imports
// __stack_chk_fail → Pass.
func TestVLAAllocaInstrumentationChecker_VLAWithFailIsPass(t *testing.T) {
	c := &VLAAllocaInstrumentationChecker{}
	ctx := &CheckContext{
		Seed:      &seed.Seed{Content: `void seed(int n) { char buf[n]; (void)buf; }`},
		Inspector: &fakeInspector{exists: true, isELF: true, imports: []string{"__stack_chk_fail"}},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "VLA/alloca") {
		t.Errorf("Evidence should mention VLA/alloca; got %q", r.Evidence)
	}
}

// TestVLAAllocaInstrumentationChecker_VLAWithoutFailIsFail — the
// canonical bug: VLA seed but binary lacks __stack_chk_fail → Fail.
func TestVLAAllocaInstrumentationChecker_VLAWithoutFailIsFail(t *testing.T) {
	c := &VLAAllocaInstrumentationChecker{}
	ctx := &CheckContext{
		Seed:      &seed.Seed{Content: `void seed(int n) { char buf[n]; (void)buf; }`},
		Inspector: &fakeInspector{exists: true, isELF: true, imports: []string{"printf"}},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "silent bypass") {
		t.Errorf("Evidence should mention silent bypass; got %q", r.Evidence)
	}
}

// TestVLAAllocaInstrumentationChecker_AllocaWithFailIsPass — alloca
// path mirrors the VLA case.
func TestVLAAllocaInstrumentationChecker_AllocaWithFailIsPass(t *testing.T) {
	c := &VLAAllocaInstrumentationChecker{}
	ctx := &CheckContext{
		Seed:      &seed.Seed{Content: `void seed(int n) { void *p = __builtin_alloca(n); (void)p; }`},
		Inspector: &fakeInspector{exists: true, isELF: true, imports: []string{"__stack_chk_fail_local"}},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on alloca + __stack_chk_fail_local, got %s", r.Verdict)
	}
}

// TestVLAAllocaInstrumentationChecker_NoVLAOrAllocaIsNA — fixed-size
// buffers do not trigger H01.
func TestVLAAllocaInstrumentationChecker_NoVLAOrAllocaIsNA(t *testing.T) {
	c := &VLAAllocaInstrumentationChecker{}
	ctx := &CheckContext{
		Seed:      &seed.Seed{Content: `void seed(void) { char buf[64]; (void)buf; }`},
		Inspector: &fakeInspector{exists: true, isELF: true, imports: []string{}},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on fixed-buffer seed, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "VLA / alloca") {
		t.Errorf("Reason should explain inapplicability; got %q", r.Reason)
	}
}

// TestVLAAllocaInstrumentationChecker_NoInspectorIsNA — VLA seed but no
// inspector → NA, never Fail (avoid false positives in unit tests).
func TestVLAAllocaInstrumentationChecker_NoInspectorIsNA(t *testing.T) {
	c := &VLAAllocaInstrumentationChecker{}
	ctx := &CheckContext{
		Seed: &seed.Seed{Content: `void seed(int n) { char buf[n]; (void)buf; }`},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable when inspector missing, got %s", r.Verdict)
	}
}

// TestVLAAllocaInstrumentationChecker_NilContextIsNA — defensive.
func TestVLAAllocaInstrumentationChecker_NilContextIsNA(t *testing.T) {
	c := &VLAAllocaInstrumentationChecker{}
	r := c.Check(nil)
	if r.Verdict != VerdictNotApplicable {
		t.Errorf("expected NotApplicable on nil context, got %s", r.Verdict)
	}
}

// TestVLAAllocaInstrumentationChecker_NoSeedIsNA — without a seed
// source we cannot decide if VLA/alloca is in play.
func TestVLAAllocaInstrumentationChecker_NoSeedIsNA(t *testing.T) {
	c := &VLAAllocaInstrumentationChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{exists: true, isELF: true, imports: []string{"__stack_chk_fail"}},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Errorf("expected NotApplicable without seed, got %s", r.Verdict)
	}
}

// TestVLAAllocaInstrumentationChecker_DefaultsAreSane.
func TestVLAAllocaInstrumentationChecker_DefaultsAreSane(t *testing.T) {
	c := &VLAAllocaInstrumentationChecker{}
	if c.ID() != "INV-SP-H01" {
		t.Errorf("default ID() = %q, want INV-SP-H01", c.ID())
	}
	if c.Category() != CategoryStatic {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryStatic)
	}
	if c.sourceURL() == "" {
		t.Error("default sourceURL() must be non-empty")
	}
	if c.sensitivity() == "" {
		t.Error("default sensitivity() must be non-empty")
	}
}
