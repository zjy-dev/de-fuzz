package oracle

import (
	"debug/elf"
	"strings"
	"testing"
)

// spillShape is the PR85434 spill fingerprint:
//
//	ldr r3, [pc, #16]   ; r3 = &__stack_chk_guard
//	str r3, [sp, #4]    ; spill the address into the local frame ← S01 violation
//	ldr r2, [sp, #4]    ; reload spilled address (attacker may have rewritten it)
//	ldr r2, [r2]        ; deref → guard *value*
//	cmp r2, r3
//	bx  lr
//
// The dereference is present (so V01 is happy), but the address itself
// is parked on the attacker-writable stack — exactly what S01 forbids.
func spillShape() []byte {
	return armText(
		0xe59f3010, // ldr r3, [pc, #16]
		0xe58d3004, // str r3, [sp, #4]    ← spill
		0xe59d2004, // ldr r2, [sp, #4]
		0xe5922000, // ldr r2, [r2]
		0xe1520003, // cmp r2, r3
		0xe12fff1e, // bx lr
	)
}

// TestGuardSpillChecker_SpillIsFail — the canonical bug shape.
func TestGuardSpillChecker_SpillIsFail(t *testing.T) {
	c := &GuardSpillChecker{}
	r := c.Check(armELFCtx(t, spillShape(), []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail on spill shape, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "PR85434") && !strings.Contains(r.Evidence, "D64759") {
		t.Errorf("Evidence should reference PR85434 / D64759; got %q", r.Evidence)
	}
	violators, ok := r.Detail["violator_functions"].([]string)
	if !ok || len(violators) != 1 || violators[0] != "seed" {
		t.Errorf("violator_functions = %v, want [seed]", r.Detail["violator_functions"])
	}
	if got, _ := r.Detail["address_spills"].(int); got < 1 {
		t.Errorf("address_spills = %v, want ≥1", r.Detail["address_spills"])
	}
}

// TestGuardSpillChecker_PassShape — the V01-pass shape never stores the
// guard address to the stack, so S01 must report Pass.
func TestGuardSpillChecker_PassShape(t *testing.T) {
	c := &GuardSpillChecker{}
	r := c.Check(armELFCtx(t, passShape(), []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on healthy epilogue, got %s (reason=%s, evidence=%s)",
			r.Verdict, r.Reason, r.Evidence)
	}
	if got, _ := r.Detail["address_spills"].(int); got != 0 {
		t.Errorf("address_spills = %v, want 0", r.Detail["address_spills"])
	}
}

// TestGuardSpillChecker_NonARMIsNA — only ARM / Thumb in scope today.
func TestGuardSpillChecker_NonARMIsNA(t *testing.T) {
	c := &GuardSpillChecker{}
	insp := &fakeInspector{
		exists: true, isELF: true,
		machine: elf.EM_AARCH64,
		class:   elf.ELFCLASS64,
		imports: []string{"__stack_chk_fail"},
	}
	r := c.Check(&CheckContext{Inspector: insp})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on aarch64, got %s", r.Verdict)
	}
}

// TestGuardSpillChecker_NoStackChkFailIsNA.
func TestGuardSpillChecker_NoStackChkFailIsNA(t *testing.T) {
	c := &GuardSpillChecker{}
	r := c.Check(armELFCtx(t, spillShape(), []string{"printf"}))
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable when SP runtime missing, got %s", r.Verdict)
	}
}

// TestGuardSpillChecker_NoFunctionsIsNA — stripped binary.
func TestGuardSpillChecker_NoFunctionsIsNA(t *testing.T) {
	c := &GuardSpillChecker{}
	insp := &fakeInspector{
		exists: true, isELF: true,
		machine: elf.EM_ARM, class: elf.ELFCLASS32,
		imports: []string{"__stack_chk_fail"},
		execs:   []ExecSection{{Name: ".text", Data: spillShape()}},
	}
	r := c.Check(&CheckContext{Inspector: insp})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on stripped binary, got %s", r.Verdict)
	}
}

// TestGuardSpillChecker_NoPCLoadsIsNA — trivial leaf.
func TestGuardSpillChecker_NoPCLoadsIsNA(t *testing.T) {
	code := armText(0xe12fff1e) // bx lr
	c := &GuardSpillChecker{}
	r := c.Check(armELFCtx(t, code, []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on trivial epilogue, got %s", r.Verdict)
	}
}

// TestGuardSpillChecker_FunctionFilter — non-matching filter → NA.
func TestGuardSpillChecker_FunctionFilter(t *testing.T) {
	c := &GuardSpillChecker{FunctionFilter: []string{"unknown"}}
	r := c.Check(armELFCtx(t, spillShape(), []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable with non-matching filter, got %s", r.Verdict)
	}

	c = &GuardSpillChecker{FunctionFilter: []string{"seed"}}
	r = c.Check(armELFCtx(t, spillShape(), []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail with matching filter, got %s", r.Verdict)
	}
}

// TestGuardSpillChecker_PolarityTag — required for the aggregator.
func TestGuardSpillChecker_PolarityTag(t *testing.T) {
	c := &GuardSpillChecker{}
	r := c.Check(armELFCtx(t, spillShape(), []string{"__stack_chk_fail"}))
	if got, _ := r.Detail["polarity_sensitive"].(bool); !got {
		t.Errorf("Detail[polarity_sensitive] = %v, want true", r.Detail["polarity_sensitive"])
	}
}

// TestGuardSpillChecker_NilContextIsNA.
func TestGuardSpillChecker_NilContextIsNA(t *testing.T) {
	c := &GuardSpillChecker{}
	r := c.Check(nil)
	if r.Verdict != VerdictNotApplicable {
		t.Errorf("expected NotApplicable on nil context, got %s", r.Verdict)
	}
}

// TestGuardSpillChecker_DefaultsAreSane.
func TestGuardSpillChecker_DefaultsAreSane(t *testing.T) {
	c := &GuardSpillChecker{}
	if c.ID() != "INV-SP-S01" {
		t.Errorf("default ID() = %q, want INV-SP-S01", c.ID())
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
