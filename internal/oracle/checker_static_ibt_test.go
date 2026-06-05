package oracle

import (
	"debug/elf"
	"testing"
)

// ---- selectEndbrPattern ----

func TestSelectEndbrPattern_X86_64(t *testing.T) {
	pat, ok := selectEndbrPattern(elf.EM_X86_64, elf.ELFCLASS64)
	if !ok {
		t.Fatal("expected ok for EM_X86_64")
	}
	if string(pat) != string(endbr64Pattern) {
		t.Errorf("wrong pattern for EM_X86_64: %x", pat)
	}
}

func TestSelectEndbrPattern_X86_32(t *testing.T) {
	pat, ok := selectEndbrPattern(elf.EM_386, elf.ELFCLASS32)
	if !ok {
		t.Fatal("expected ok for EM_386")
	}
	if string(pat) != string(endbr32Pattern) {
		t.Errorf("wrong pattern for EM_386: %x", pat)
	}
}

func TestSelectEndbrPattern_NonX86(t *testing.T) {
	for _, machine := range []elf.Machine{elf.EM_AARCH64, elf.EM_RISCV, elf.EM_MIPS} {
		_, ok := selectEndbrPattern(machine, elf.ELFCLASS64)
		if ok {
			t.Errorf("expected !ok for non-x86 machine %v", machine)
		}
	}
}

// ---- findEnclosingFunction ----

func TestFindEnclosingFunction_InsideFirst(t *testing.T) {
	funcs := []FunctionSymbol{
		{Name: "a", Addr: 0x10, Size: 0x10},
		{Name: "b", Addr: 0x30, Size: 0x10},
	}
	fn, ok := findEnclosingFunction(funcs, 0x15)
	if !ok || fn.Name != "a" {
		t.Errorf("0x15 should be in 'a', got ok=%v fn=%v", ok, fn.Name)
	}
}

func TestFindEnclosingFunction_AtStart(t *testing.T) {
	funcs := []FunctionSymbol{{Name: "f", Addr: 0x10, Size: 0x10}}
	fn, ok := findEnclosingFunction(funcs, 0x10)
	if !ok || fn.Name != "f" {
		t.Errorf("0x10 (entry) should be in 'f', got ok=%v fn=%v", ok, fn.Name)
	}
}

func TestFindEnclosingFunction_AtExclusiveEnd(t *testing.T) {
	funcs := []FunctionSymbol{{Name: "f", Addr: 0x10, Size: 0x10}}
	_, ok := findEnclosingFunction(funcs, 0x20)
	if ok {
		t.Error("0x20 is past the end of 'f', should not match")
	}
}

func TestFindEnclosingFunction_InGap(t *testing.T) {
	funcs := []FunctionSymbol{
		{Name: "a", Addr: 0x10, Size: 0x10},
		{Name: "b", Addr: 0x30, Size: 0x10},
	}
	_, ok := findEnclosingFunction(funcs, 0x25)
	if ok {
		t.Error("0x25 is in gap between a and b")
	}
}

func TestFindEnclosingFunction_BeforeAll(t *testing.T) {
	funcs := []FunctionSymbol{{Name: "f", Addr: 0x10, Size: 0x10}}
	_, ok := findEnclosingFunction(funcs, 0x05)
	if ok {
		t.Error("0x05 is before all functions, should not match")
	}
}

func TestFindEnclosingFunction_Empty(t *testing.T) {
	_, ok := findEnclosingFunction(nil, 0x100)
	if ok {
		t.Error("empty slice should never match")
	}
}

// ---- moreSuffix ----

func TestMoreSuffix_NoTruncation(t *testing.T) {
	if moreSuffix(5, 5) != "" {
		t.Error("no truncation: want empty string")
	}
}

func TestMoreSuffix_Truncated(t *testing.T) {
	got := moreSuffix(10, 5)
	want := " (+5 more)"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

// ---- UnintendedEndbrChecker ----

func TestUnintendedEndbrChecker_IDAndCategory(t *testing.T) {
	c := &UnintendedEndbrChecker{}
	if c.ID() != "INV-IBT-B01" {
		t.Errorf("ID: got %q, want INV-IBT-B01", c.ID())
	}
	if c.Category() != CategoryStatic {
		t.Errorf("Category: got %v, want CategoryStatic", c.Category())
	}
}

func TestUnintendedEndbrChecker_NilContext_NA(t *testing.T) {
	c := &UnintendedEndbrChecker{}
	r := c.Check(nil)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("nil context: want NA, got %s", r.Verdict)
	}
}

func TestUnintendedEndbrChecker_NoInspector_NA(t *testing.T) {
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("no inspector: want NA, got %s", r.Verdict)
	}
}

func TestUnintendedEndbrChecker_NonX86_NA(t *testing.T) {
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{
		Inspector: &fakeInspector{
			machine: elf.EM_AARCH64,
			class:   elf.ELFCLASS64,
		},
	})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("non-x86: want NA, got %s: %s", r.Verdict, r.Reason)
	}
}

func TestUnintendedEndbrChecker_NoFunctionSymbols_NA(t *testing.T) {
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{
		Inspector: &fakeInspector{
			machine: elf.EM_X86_64,
			class:   elf.ELFCLASS64,
			execs:   []ExecSection{{Name: ".text", Addr: 0, Data: endbr64Pattern}},
		},
	})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("no STT_FUNC: want NA, got %s: %s", r.Verdict, r.Reason)
	}
}

func TestUnintendedEndbrChecker_NoExecSections_NA(t *testing.T) {
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{
		Inspector: &fakeInspector{
			machine: elf.EM_X86_64,
			class:   elf.ELFCLASS64,
			funcs:   []FunctionSymbol{{Name: "f", Addr: 0, Size: 16}},
		},
	})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("no exec sections: want NA, got %s: %s", r.Verdict, r.Reason)
	}
}

func TestUnintendedEndbrChecker_Pass_EndbrOnlyAtEntry(t *testing.T) {
	data := make([]byte, 16)
	copy(data[0:], endbr64Pattern)
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{
		Inspector: &fakeInspector{
			machine: elf.EM_X86_64,
			class:   elf.ELFCLASS64,
			funcs:   []FunctionSymbol{{Name: "f", Addr: 0, Size: 16}},
			execs:   []ExecSection{{Name: ".text", Addr: 0, Data: data}},
		},
	})
	if r.Verdict != VerdictPass {
		t.Fatalf("ENDBR only at entry: want Pass, got %s: %s", r.Verdict, r.Evidence)
	}
	if r.Detail["endbr_at_function_entry"].(int) != 1 {
		t.Errorf("expected 1 at-entry ENDBR, got %v", r.Detail["endbr_at_function_entry"])
	}
}

func TestUnintendedEndbrChecker_Pass_NoEndbrAtAll(t *testing.T) {
	data := make([]byte, 16)
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{
		Inspector: &fakeInspector{
			machine: elf.EM_X86_64,
			class:   elf.ELFCLASS64,
			funcs:   []FunctionSymbol{{Name: "pure", Addr: 0, Size: 16}},
			execs:   []ExecSection{{Name: ".text", Addr: 0, Data: data}},
		},
	})
	if r.Verdict != VerdictPass {
		t.Fatalf("no ENDBR at all: want Pass, got %s", r.Verdict)
	}
}

func TestUnintendedEndbrChecker_Fail_UnintendedInBody(t *testing.T) {
	data := make([]byte, 16)
	copy(data[0:], endbr64Pattern)
	copy(data[4:], endbr64Pattern)
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{
		Inspector: &fakeInspector{
			machine: elf.EM_X86_64,
			class:   elf.ELFCLASS64,
			funcs:   []FunctionSymbol{{Name: "gadget", Addr: 0, Size: 16}},
			execs:   []ExecSection{{Name: ".text", Addr: 0, Data: data}},
		},
	})
	if r.Verdict != VerdictFail {
		t.Fatalf("unintended ENDBR in body: want Fail, got %s", r.Verdict)
	}
	if r.Detail["endbr_unintended_in_function_body"].(int) != 1 {
		t.Errorf("expected 1 violation, got %v", r.Detail["endbr_unintended_in_function_body"])
	}
}

func TestUnintendedEndbrChecker_Pass_EndbrInGap(t *testing.T) {
	data := make([]byte, 24)
	copy(data[0:], endbr64Pattern)
	copy(data[16:], endbr64Pattern)
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{
		Inspector: &fakeInspector{
			machine: elf.EM_X86_64,
			class:   elf.ELFCLASS64,
			funcs:   []FunctionSymbol{{Name: "f", Addr: 0, Size: 8}},
			execs:   []ExecSection{{Name: ".text", Addr: 0, Data: data}},
		},
	})
	if r.Verdict != VerdictPass {
		t.Fatalf("gap ENDBR: want Pass, got %s", r.Verdict)
	}
	if r.Detail["endbr_in_inter_function_gap"].(int) != 1 {
		t.Errorf("expected 1 gap hit, got %v", r.Detail["endbr_in_inter_function_gap"])
	}
}

func TestUnintendedEndbrChecker_Fail_X86_32(t *testing.T) {
	data := make([]byte, 16)
	copy(data[0:], endbr32Pattern)
	copy(data[4:], endbr32Pattern)
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{
		Inspector: &fakeInspector{
			machine: elf.EM_386,
			class:   elf.ELFCLASS32,
			funcs:   []FunctionSymbol{{Name: "f32", Addr: 0, Size: 16}},
			execs:   []ExecSection{{Name: ".text", Addr: 0, Data: data}},
		},
	})
	if r.Verdict != VerdictFail {
		t.Fatalf("endbr32 violation on EM_386: want Fail, got %s", r.Verdict)
	}
}

func TestUnintendedEndbrChecker_ViolationsCapped(t *testing.T) {
	total := MaxReportedViolations + 3
	stride := 5
	size := 1 + total*stride
	data := make([]byte, size)
	copy(data[0:], endbr64Pattern)
	for i := 0; i < total; i++ {
		copy(data[1+i*stride:], endbr64Pattern)
	}
	c := &UnintendedEndbrChecker{}
	r := c.Check(&CheckContext{
		Inspector: &fakeInspector{
			machine: elf.EM_X86_64,
			class:   elf.ELFCLASS64,
			funcs:   []FunctionSymbol{{Name: "h", Addr: 0, Size: uint64(size)}},
			execs:   []ExecSection{{Name: ".text", Addr: 0, Data: data}},
		},
	})
	if r.Verdict != VerdictFail {
		t.Fatalf("many violations: want Fail, got %s", r.Verdict)
	}
	viols, ok := r.Detail["violations"].([]string)
	if !ok {
		t.Fatal("Detail[violations] must be []string")
	}
	if len(viols) > MaxReportedViolations {
		t.Errorf("violations capped at %d, got %d", MaxReportedViolations, len(viols))
	}
	if r.Detail["endbr_unintended_in_function_body"].(int) != total {
		t.Errorf("total count: want %d, got %v", total, r.Detail["endbr_unintended_in_function_body"])
	}
}
