package coverage

import (
	"path/filepath"
	"testing"
)

func TestParseLLVMIRFiles_BasicBlocks(t *testing.T) {
	irPath := filepath.Join("artifacts", "stackprotector_sample.ll")
	funcs, err := ParseLLVMIRFiles([]string{irPath}, nil, "")
	if err != nil {
		t.Fatalf("ParseLLVMIRFiles() error = %v", err)
	}

	// The fixture defines a single function (mangled name).
	const mangled = "_ZN13StackProtector13runOnFunctionEi"
	fn, ok := funcs[mangled]
	if !ok {
		t.Fatalf("expected function %q, got %v", mangled, keysOf(funcs))
	}

	// Expect blocks: entry, if.then, if.else, sw.bb, sw.bb2, sw.default, sw.epilog (7).
	if len(fn.Blocks) != 7 {
		t.Errorf("expected 7 basic blocks, got %d", len(fn.Blocks))
	}

	// BB IDs start at 2 (entry).
	entry, ok := fn.Blocks[2]
	if !ok {
		t.Fatalf("expected entry block with ID 2")
	}
	// entry has a conditional branch -> 2 successors (if.then, if.else).
	if len(entry.Successors) != 2 {
		t.Errorf("entry should have 2 successors, got %d: %v", len(entry.Successors), entry.Successors)
	}

	// entry should carry source lines 100 (alloca) and 101 (cmp/br).
	if !containsInt(entry.Lines, 100) || !containsInt(entry.Lines, 101) {
		t.Errorf("entry lines = %v, want to include 100 and 101", entry.Lines)
	}

	// File resolved from the single DIFile in the fixture.
	wantFile := "/src/llvm/lib/CodeGen/StackProtector.cpp"
	if entry.File != wantFile {
		t.Errorf("entry.File = %q, want %q", entry.File, wantFile)
	}

	// Find the switch block (if.else): should have 3 successors (default + 2 cases).
	var switchBB *BasicBlock
	for _, bb := range fn.Blocks {
		if len(bb.Successors) == 3 {
			switchBB = bb
		}
	}
	if switchBB == nil {
		t.Errorf("expected a block with 3 successors (switch), found none")
	}
}

func TestParseLLVMIRFiles_TargetFilter(t *testing.T) {
	irPath := filepath.Join("artifacts", "stackprotector_sample.ll")
	// Filter by mangled name directly (no demangler available in unit tests).
	funcs, err := ParseLLVMIRFiles([]string{irPath},
		[]string{"_ZN13StackProtector13runOnFunctionEi"}, "")
	if err != nil {
		t.Fatalf("ParseLLVMIRFiles() error = %v", err)
	}
	if len(funcs) != 1 {
		t.Errorf("expected 1 matched function, got %d", len(funcs))
	}
}

func TestNewAnalyzerFromCFGFunctions_SelectTarget(t *testing.T) {
	irPath := filepath.Join("artifacts", "stackprotector_sample.ll")
	const mangled = "_ZN13StackProtector13runOnFunctionEi"
	funcs, err := ParseLLVMIRFiles([]string{irPath}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	// Re-key by mangled name as the target function name for this test.
	an, err := NewAnalyzerFromCFGFunctions(funcs, []string{mangled}, "", "", 0.8)
	if err != nil {
		t.Fatalf("NewAnalyzerFromCFGFunctions() error = %v", err)
	}

	// No coverage yet -> SelectTarget should return a reachable BB in the target
	// function (entry has no predecessors, so it is reachable).
	target := an.SelectTarget()
	if target == nil {
		t.Fatal("SelectTarget() returned nil with uncovered blocks present")
	}
	if target.Function != mangled {
		t.Errorf("target.Function = %q, want %q", target.Function, mangled)
	}
	// The selected BB must have at least one source line.
	if len(target.Lines) == 0 {
		t.Error("selected target should have source lines")
	}
}

func TestNewAnalyzerFromCFGFunctions_MissingTarget(t *testing.T) {
	irPath := filepath.Join("artifacts", "stackprotector_sample.ll")
	funcs, err := ParseLLVMIRFiles([]string{irPath}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewAnalyzerFromCFGFunctions(funcs, []string{"does_not_exist"}, "", "", 0.8)
	if err == nil {
		t.Error("expected error when target function is missing")
	}
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
