//go:build integration
// +build integration

package coverage

import (
	"testing"
)

const realCFGPath = "/root/project/de-fuzz/gcc-v12.2.0-x64/gcc-build/gcc/cfgexpand.cc.015t.cfg"

func TestCFGAnalyzer_RealCFGFile(t *testing.T) {
	analyzer := NewCFGAnalyzer(realCFGPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Failed to parse real CFG file: %v", err)
	}

	// Check that we found target functions
	targetFunctions := []string{
		"stack_protect_classify_type",
		"stack_protect_decl_phase",
		"stack_protect_prologue",
	}

	for _, funcName := range targetFunctions {
		fn, ok := analyzer.GetFunction(funcName)
		if !ok {
			t.Errorf("Expected to find function %s", funcName)
			continue
		}

		t.Logf("Function %s:", funcName)
		t.Logf("  Basic blocks: %d", len(fn.Blocks))

		// Count total successors
		totalSuccs := 0
		for _, bb := range fn.Blocks {
			totalSuccs += len(bb.Successors)
		}
		t.Logf("  Total successors: %d", totalSuccs)

		// Print BB summary
		for bbID, bb := range fn.Blocks {
			if len(bb.Successors) > 1 {
				t.Logf("  BB %d: %d successors, lines: %v", bbID, len(bb.Successors), bb.Lines)
			}
		}
	}

	// Test SelectTargetBB with no coverage
	coveredLines := make(map[LineID]bool)
	target := analyzer.SelectTargetBB(targetFunctions, coveredLines)
	if target == nil {
		t.Error("SelectTargetBB returned nil")
	} else {
		t.Logf("Selected target BB: %s:BB%d with %d successors, lines: %v",
			target.Function, target.BBID, target.SuccessorCount, target.Lines)
	}

	// Test GetFunctionCoverage
	for _, funcName := range targetFunctions {
		covered, total := analyzer.GetFunctionCoverage(funcName, coveredLines)
		t.Logf("Function %s coverage: %d/%d BBs", funcName, covered, total)
	}
}

func TestCFGAnalyzer_PrintFunctionSummary(t *testing.T) {
	analyzer := NewCFGAnalyzer(realCFGPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Failed to parse real CFG file: %v", err)
	}

	// Print summary of stack_protect_classify_type
	t.Log("Summary of stack_protect_classify_type:")
	analyzer.PrintFunctionSummary("stack_protect_classify_type")
}

func TestCFGAnalyzer_AllFunctions(t *testing.T) {
	analyzer := NewCFGAnalyzer(realCFGPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Failed to parse real CFG file: %v", err)
	}

	funcs := analyzer.GetAllFunctions()
	t.Logf("Total functions parsed: %d", len(funcs))

	// Print first 20 function names
	for i, name := range funcs {
		if i >= 20 {
			t.Log("... (truncated)")
			break
		}
		t.Logf("  %s", name)
	}
}
