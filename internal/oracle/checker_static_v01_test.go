package oracle

import (
	"debug/elf"
	"strings"
	"testing"
)

// armText turns a sequence of 32-bit ARM A32 instruction words (already
// hand-verified against `golang.org/x/arch/arm/armasm` to decode to the
// shape we want) into a little-endian byte buffer.
func armText(words ...uint32) []byte {
	out := make([]byte, 4*len(words))
	for i, w := range words {
		out[i*4+0] = byte(w)
		out[i*4+1] = byte(w >> 8)
		out[i*4+2] = byte(w >> 16)
		out[i*4+3] = byte(w >> 24)
	}
	return out
}

// passShape is a healthy ARM SP epilogue:
//
//	ldr r3, [pc, #16]   ; load &__stack_chk_guard from literal pool
//	ldr r2, [r3]        ; dereference → guard *value*
//	cmp r2, r4          ; compare against canary slot value
//	bx  lr
//
// The dereference is the key — V01 must classify this as Pass.
func passShape() []byte {
	return armText(0xe59f3010, 0xe5932000, 0xe1520004, 0xe12fff1e)
}

// failShape is the PR85434 fingerprint:
//
//	ldr r3, [pc, #16]   ; load &__stack_chk_guard
//	ldr r2, [pc, #20]   ; load &__stack_chk_guard AGAIN (also an address)
//	cmp r2, r3          ; XOR/compare two addresses → tautology
//	bx  lr
//
// No register-indirect dereference — V01 must classify as Fail.
func failShape() []byte {
	return armText(0xe59f3010, 0xe59f2014, 0xe1520003, 0xe12fff1e)
}

// armELFCtx builds a CheckContext with a fakeInspector pre-populated to
// look like a tiny statically linked ARM ELF. The single function symbol
// `seed` covers the entire `.text` section.
func armELFCtx(t *testing.T, code []byte, imports []string) *CheckContext {
	t.Helper()
	insp := &fakeInspector{
		path:    "/fake/arm-binary",
		exists:  true,
		isELF:   true,
		machine: elf.EM_ARM,
		class:   elf.ELFCLASS32,
		imports: imports,
		funcs: []FunctionSymbol{{
			Name:       "seed",
			Addr:       0,
			Size:       uint64(len(code)),
			SectionIdx: 1,
		}},
		execs: []ExecSection{{
			Name:       ".text",
			Addr:       0,
			Data:       code,
			SectionIdx: 1,
		}},
	}
	return &CheckContext{Inspector: insp}
}

// TestEpilogueGuardCompareChecker_PassShape — the well-formed epilogue
// dereferences the guard address, so V01 reports Pass.
func TestEpilogueGuardCompareChecker_PassShape(t *testing.T) {
	c := &EpilogueGuardCompareChecker{}
	r := c.Check(armELFCtx(t, passShape(), []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on healthy epilogue, got %s (reason=%s, evidence=%s)",
			r.Verdict, r.Reason, r.Evidence)
	}
	if !strings.Contains(r.Evidence, "dereference") {
		t.Errorf("Evidence should mention dereference; got %q", r.Evidence)
	}
}

// TestEpilogueGuardCompareChecker_FailShape — PR85434 fingerprint:
// two PC-relative loads, zero register-indirect dereferences, but a
// compare exists. V01 must report Fail.
func TestEpilogueGuardCompareChecker_FailShape(t *testing.T) {
	c := &EpilogueGuardCompareChecker{}
	r := c.Check(armELFCtx(t, failShape(), []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail on PR85434 shape, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "PR85434") {
		t.Errorf("Evidence should reference PR85434; got %q", r.Evidence)
	}
	violators, ok := r.Detail["violator_functions"].([]string)
	if !ok || len(violators) != 1 || violators[0] != "seed" {
		t.Errorf("violator_functions = %v, want [seed]", r.Detail["violator_functions"])
	}
}

// TestEpilogueGuardCompareChecker_NonARMIsNA — V01 only screens ARM /
// Thumb. An x86_64 binary must short-circuit to NotApplicable.
func TestEpilogueGuardCompareChecker_NonARMIsNA(t *testing.T) {
	c := &EpilogueGuardCompareChecker{}
	insp := &fakeInspector{
		exists: true, isELF: true,
		machine: elf.EM_X86_64,
		class:   elf.ELFCLASS64,
		imports: []string{"__stack_chk_fail"},
	}
	r := c.Check(&CheckContext{Inspector: insp})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on x86_64, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "ARM") {
		t.Errorf("Reason should mention ARM; got %q", r.Reason)
	}
}

// TestEpilogueGuardCompareChecker_NoStackChkFailIsNA — a binary
// without __stack_chk_fail has nothing to screen.
func TestEpilogueGuardCompareChecker_NoStackChkFailIsNA(t *testing.T) {
	c := &EpilogueGuardCompareChecker{}
	r := c.Check(armELFCtx(t, passShape(), []string{"printf"}))
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable when SP runtime missing, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "__stack_chk_fail") {
		t.Errorf("Reason should explain missing import; got %q", r.Reason)
	}
}

// TestEpilogueGuardCompareChecker_NoFunctionsIsNA — stripped binary
// (no STT_FUNC) — cannot bound the epilogue → NotApplicable.
func TestEpilogueGuardCompareChecker_NoFunctionsIsNA(t *testing.T) {
	c := &EpilogueGuardCompareChecker{}
	insp := &fakeInspector{
		exists: true, isELF: true,
		machine: elf.EM_ARM, class: elf.ELFCLASS32,
		imports: []string{"__stack_chk_fail"},
		// No funcs.
		execs: []ExecSection{{Name: ".text", Data: passShape()}},
	}
	r := c.Check(&CheckContext{Inspector: insp})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on stripped binary, got %s", r.Verdict)
	}
}

// TestEpilogueGuardCompareChecker_NoPCLoadsIsNA — a function that has
// no PC-relative loads at all is not exercising the literal-pool path,
// so we cannot conclude either way.
func TestEpilogueGuardCompareChecker_NoPCLoadsIsNA(t *testing.T) {
	// `bx lr` only — trivial leaf with no code.
	code := armText(0xe12fff1e)
	c := &EpilogueGuardCompareChecker{}
	r := c.Check(armELFCtx(t, code, []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on trivial epilogue, got %s", r.Verdict)
	}
}

// TestEpilogueGuardCompareChecker_FunctionFilter — only matching
// functions are inspected; non-matching ones are skipped.
func TestEpilogueGuardCompareChecker_FunctionFilter(t *testing.T) {
	c := &EpilogueGuardCompareChecker{FunctionFilter: []string{"seed"}}
	r := c.Check(armELFCtx(t, failShape(), []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail with matching filter, got %s", r.Verdict)
	}

	c = &EpilogueGuardCompareChecker{FunctionFilter: []string{"unknown"}}
	r = c.Check(armELFCtx(t, failShape(), []string{"__stack_chk_fail"}))
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable with non-matching filter, got %s", r.Verdict)
	}
}

// TestEpilogueGuardCompareChecker_NilContextIsNA — defensive.
func TestEpilogueGuardCompareChecker_NilContextIsNA(t *testing.T) {
	c := &EpilogueGuardCompareChecker{}
	r := c.Check(nil)
	if r.Verdict != VerdictNotApplicable {
		t.Errorf("expected NotApplicable on nil context, got %s", r.Verdict)
	}
}

// TestEpilogueGuardCompareChecker_DefaultsAreSane.
func TestEpilogueGuardCompareChecker_DefaultsAreSane(t *testing.T) {
	c := &EpilogueGuardCompareChecker{}
	if c.ID() != "INV-SP-V01" {
		t.Errorf("default ID() = %q, want INV-SP-V01", c.ID())
	}
	if c.Category() != CategoryStatic {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryStatic)
	}
}
