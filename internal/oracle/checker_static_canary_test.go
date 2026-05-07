package oracle

import (
	"strings"
	"testing"
)

// fakeInspector is a hand-rolled BinaryInspector for unit testing static
// checkers without touching the filesystem or ELF parser.
type fakeInspector struct {
	path    string
	exists  bool
	isELF   bool
	syms    []string
	imports []string
	err     error
}

func (f *fakeInspector) Path() string { return f.path }
func (f *fakeInspector) Exists() bool { return f.exists }
func (f *fakeInspector) IsELF() bool  { return f.isELF }
func (f *fakeInspector) Symbols() ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]string, len(f.syms))
	copy(out, f.syms)
	return out, nil
}
func (f *fakeInspector) HasSymbol(name string) (bool, error) {
	syms, err := f.Symbols()
	if err != nil {
		return false, err
	}
	for _, s := range syms {
		if s == name {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeInspector) ImportedFunctions() ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]string, len(f.imports))
	copy(out, f.imports)
	return out, nil
}

// TestStackChkSymbolsChecker_Pass: __stack_chk_fail import present → Pass.
func TestStackChkSymbolsChecker_Pass(t *testing.T) {
	c := &StackChkSymbolsChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists:  true,
			isELF:   true,
			syms:    []string{"main", "vulnerable", "__stack_chk_fail", "__stack_chk_guard"},
			imports: []string{"__stack_chk_fail", "puts"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if r.ID != "INV-SP-G01" {
		t.Errorf("wrong ID: %s", r.ID)
	}
	if got := r.Detail["has_stack_chk_fail"]; got != true {
		t.Errorf("Detail[has_stack_chk_fail] = %v, want true", got)
	}
}

// TestStackChkSymbolsChecker_NA: no canary symbols → NotApplicable
// (cannot distinguish "SP off" from "no vulnerable objects").
func TestStackChkSymbolsChecker_NA(t *testing.T) {
	c := &StackChkSymbolsChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists:  true,
			isELF:   true,
			syms:    []string{"main", "puts"},
			imports: []string{"puts"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable, got %s", r.Verdict)
	}
	if r.Reason == "" {
		t.Error("NA verdict must include a Reason")
	}
}

// TestStackChkSymbolsChecker_NoInspector: missing inspector → NA, not panic.
func TestStackChkSymbolsChecker_NoInspector(t *testing.T) {
	c := &StackChkSymbolsChecker{}
	r := c.Check(&CheckContext{})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable, got %s", r.Verdict)
	}
}

// TestMainNoCanaryChecker_PassWhenNoStackChk: binary has main + no
// __stack_chk_* imports anywhere → A01 trivially holds.
func TestMainNoCanaryChecker_PassWhenNoStackChk(t *testing.T) {
	c := &MainNoCanaryChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists:  true,
			isELF:   true,
			syms:    []string{"main", "seed"},
			imports: []string{"puts"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass, got %s (reason=%s)", r.Verdict, r.Reason)
	}
}

// TestMainNoCanaryChecker_NAWhenStackChkPresent: binary imports
// __stack_chk_fail somewhere; symbol-only inspection cannot prove main
// itself is not the caller → NA with explanatory Reason.
func TestMainNoCanaryChecker_NAWhenStackChkPresent(t *testing.T) {
	c := &MainNoCanaryChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists:  true,
			isELF:   true,
			syms:    []string{"main", "seed", "__stack_chk_fail"},
			imports: []string{"puts", "__stack_chk_fail"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "disassembly") {
		t.Errorf("Reason should mention disassembly limitation, got: %s", r.Reason)
	}
}

// TestMainNoCanaryChecker_NoMain: binary has no main symbol → NA
// (not Fail; e.g., a shared library legitimately has no main).
func TestMainNoCanaryChecker_NoMain(t *testing.T) {
	c := &MainNoCanaryChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists:  true,
			isELF:   true,
			syms:    []string{"seed"},
			imports: []string{"puts"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable for no-main binary, got %s", r.Verdict)
	}
}
