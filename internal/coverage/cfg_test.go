package coverage

import (
	"os"
	"path/filepath"
	"testing"
)

// Sample CFG content for testing
const sampleCFG = `;; Function test_func (_Z9test_funcii, funcdef_no=1, decl_uid=100, cgraph_uid=1, symbol_order=1)
Removing basic block 5
;; 1 loops found
;;
;; Loop 0
;;  header 0, latch 1
;;  depth 0, outer -1
;;  nodes: 0 1 2 3 4
;; 2 succs { 3 4 }
;; 3 succs { 4 }
;; 4 succs { 1 }
int test_func (int a, int b)
{
  int result;

  <bb 2> :
  [/path/to/test.cc:10:3] # DEBUG BEGIN_STMT
  [/path/to/test.cc:10:7] if (a > b)
    goto <bb 3>; [INV]
  else
    goto <bb 4>; [INV]

  <bb 3> :
  [/path/to/test.cc:11:5] # DEBUG BEGIN_STMT
  [/path/to/test.cc:11:12] result = a;
  goto <bb 4>; [INV]

  <bb 4> :
  [/path/to/test.cc:13:3] # DEBUG BEGIN_STMT
  [/path/to/test.cc:13:10] return result;
}


;; Function another_func (_Z12another_funcv, funcdef_no=2, decl_uid=200, cgraph_uid=2, symbol_order=2)
;; 1 loops found
;;
;; Loop 0
;;  header 0, latch 1
;;  depth 0, outer -1
;;  nodes: 0 1 2 3 4 5
;; 2 succs { 3 4 5 }
;; 3 succs { 5 }
;; 4 succs { 5 }
;; 5 succs { 1 }
void another_func ()
{
  int x;

  <bb 2> :
  [/path/to/test.cc:20:3] # DEBUG BEGIN_STMT
  [/path/to/test.cc:20:7] x = 1;
  [/path/to/test.cc:21:3] switch (x) <default: <L2>, case 1: <L0>, case 2: <L1>>

  <bb 3> :
  [/path/to/test.cc:22:5] <L0>:
  [/path/to/test.cc:22:5] # DEBUG BEGIN_STMT
  goto <bb 5>; [INV]

  <bb 4> :
  [/path/to/test.cc:24:5] <L1>:
  [/path/to/test.cc:24:5] # DEBUG BEGIN_STMT
  goto <bb 5>; [INV]

  <bb 5> :
  [/path/to/test.cc:26:3] <L2>:
  [/path/to/test.cc:26:3] # DEBUG BEGIN_STMT
  return;
}
`

func TestCFGAnalyzer_Parse(t *testing.T) {
	// Create temporary CFG file
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// Check that functions were parsed
	funcs := analyzer.GetAllFunctions()
	if len(funcs) != 2 {
		t.Errorf("Expected 2 functions, got %d: %v", len(funcs), funcs)
	}

	// Check test_func
	fn, ok := analyzer.GetFunction("test_func")
	if !ok {
		t.Fatal("test_func not found")
	}
	if fn.MangledName != "_Z9test_funcii" {
		t.Errorf("Expected mangled name _Z9test_funcii, got %s", fn.MangledName)
	}
	if len(fn.Blocks) != 3 {
		t.Errorf("Expected 3 basic blocks in test_func, got %d", len(fn.Blocks))
	}

	// Check BB 2 has 2 successors
	bb2 := fn.Blocks[2]
	if bb2 == nil {
		t.Fatal("BB 2 not found in test_func")
	}
	if len(bb2.Successors) != 2 {
		t.Errorf("Expected BB 2 to have 2 successors, got %d", len(bb2.Successors))
	}

	// Check another_func has BB 2 with 3 successors (switch statement)
	fn2, _ := analyzer.GetFunction("another_func")
	if fn2 == nil {
		t.Fatal("another_func not found")
	}
	bb2_2 := fn2.Blocks[2]
	if bb2_2 == nil {
		t.Fatal("BB 2 not found in another_func")
	}
	if len(bb2_2.Successors) != 3 {
		t.Errorf("Expected BB 2 in another_func to have 3 successors, got %d", len(bb2_2.Successors))
	}
}

func TestCFGAnalyzer_GetBasicBlocksForLine(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// Line 10 should be in BB 2
	bbs := analyzer.GetBasicBlocksForLine("/path/to/test.cc", 10)
	if len(bbs) == 0 {
		t.Error("Expected to find BB for line 10")
	}
	found := false
	for _, bb := range bbs {
		if bb == 2 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected BB 2 for line 10, got %v", bbs)
	}

	// Line 11 should be in BB 3
	bbs = analyzer.GetBasicBlocksForLine("/path/to/test.cc", 11)
	found = false
	for _, bb := range bbs {
		if bb == 3 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected BB 3 for line 11, got %v", bbs)
	}
}

func TestCFGAnalyzer_GetSuccessorCount(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// BB 2 in test_func has 2 successors
	count := analyzer.GetSuccessorCount("test_func", 2)
	if count != 2 {
		t.Errorf("Expected 2 successors for test_func:BB2, got %d", count)
	}

	// BB 2 in another_func has 3 successors
	count = analyzer.GetSuccessorCount("another_func", 2)
	if count != 3 {
		t.Errorf("Expected 3 successors for another_func:BB2, got %d", count)
	}

	// Non-existent function
	count = analyzer.GetSuccessorCount("nonexistent", 2)
	if count != 0 {
		t.Errorf("Expected 0 successors for non-existent function, got %d", count)
	}
}

func TestCFGAnalyzer_SelectTargetBB(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// With no covered lines, should select BB with most successors
	coveredLines := make(map[LineID]bool)
	target := analyzer.SelectTargetBB([]string{"test_func", "another_func"}, coveredLines)

	if target == nil {
		t.Fatal("SelectTargetBB returned nil")
	}

	// Should select BB 2 from another_func (3 successors) over BB 2 from test_func (2 successors)
	if target.Function != "another_func" || target.BBID != 2 {
		t.Errorf("Expected another_func:BB2 (3 succs), got %s:BB%d (%d succs)",
			target.Function, target.BBID, target.SuccessorCount)
	}

	// Mark lines in another_func:BB2 as covered
	for _, line := range target.Lines {
		coveredLines[LineID{File: target.File, Line: line}] = true
	}

	// Now should select test_func:BB2 (2 successors)
	target = analyzer.SelectTargetBB([]string{"test_func", "another_func"}, coveredLines)
	if target == nil {
		t.Fatal("SelectTargetBB returned nil after covering some lines")
	}
	if target.Function != "test_func" || target.BBID != 2 {
		t.Errorf("Expected test_func:BB2 after covering another_func:BB2, got %s:BB%d",
			target.Function, target.BBID)
	}
}

func TestCFGAnalyzer_GetFunctionCoverage(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// Initially no coverage
	coveredLines := make(map[LineID]bool)
	covered, total := analyzer.GetFunctionCoverage("test_func", coveredLines)
	if covered != 0 {
		t.Errorf("Expected 0 covered BBs initially, got %d", covered)
	}
	if total != 3 { // BB 2, 3, 4 (excluding entry/exit)
		t.Errorf("Expected 3 total BBs, got %d", total)
	}

	// Cover line 10 (BB 2)
	coveredLines[LineID{File: "/path/to/test.cc", Line: 10}] = true
	covered, total = analyzer.GetFunctionCoverage("test_func", coveredLines)
	if covered != 1 {
		t.Errorf("Expected 1 covered BB after covering line 10, got %d", covered)
	}

	// Cover line 11 (BB 3)
	coveredLines[LineID{File: "/path/to/test.cc", Line: 11}] = true
	covered, total = analyzer.GetFunctionCoverage("test_func", coveredLines)
	if covered != 2 {
		t.Errorf("Expected 2 covered BBs after covering line 11, got %d", covered)
	}
}

func TestCFGAnalyzer_GetFunctionLineCoverage(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// Test total lines count
	totalLines := analyzer.GetFunctionTotalLines("test_func")
	// test_func has lines: 10 (BB2), 11 (BB3), 13 (BB4) = 3 unique lines
	if totalLines != 3 {
		t.Errorf("Expected 3 total lines in test_func, got %d", totalLines)
	}

	// another_func has lines: 20, 21 (BB2), 22 (BB3), 24 (BB4), 26 (BB5) = 5 unique lines
	totalLines = analyzer.GetFunctionTotalLines("another_func")
	if totalLines != 5 {
		t.Errorf("Expected 5 total lines in another_func, got %d", totalLines)
	}

	// Test line coverage
	coveredLines := make(map[LineID]bool)
	covered, total := analyzer.GetFunctionLineCoverage("test_func", coveredLines)
	if covered != 0 {
		t.Errorf("Expected 0 covered lines initially, got %d", covered)
	}
	if total != 3 {
		t.Errorf("Expected 3 total lines, got %d", total)
	}

	// Cover line 10
	coveredLines[LineID{File: "/path/to/test.cc", Line: 10}] = true
	covered, total = analyzer.GetFunctionLineCoverage("test_func", coveredLines)
	if covered != 1 {
		t.Errorf("Expected 1 covered line, got %d", covered)
	}

	// Cover line 11 and 13
	coveredLines[LineID{File: "/path/to/test.cc", Line: 11}] = true
	coveredLines[LineID{File: "/path/to/test.cc", Line: 13}] = true
	covered, total = analyzer.GetFunctionLineCoverage("test_func", coveredLines)
	if covered != 3 {
		t.Errorf("Expected 3 covered lines, got %d", covered)
	}
}

func TestGetSourceFile(t *testing.T) {
	tests := []struct {
		cfgPath  string
		expected string
	}{
		{"cfgexpand.cc.015t.cfg", "cfgexpand.cc"},
		{"/path/to/cfgexpand.cc.015t.cfg", "cfgexpand.cc"},
		{"test.c.001t.cfg", "test.c"},
		{"foo.cpp.042t.cfg", "foo.cpp"},
	}

	for _, tt := range tests {
		result := GetSourceFile(tt.cfgPath)
		if result != tt.expected {
			t.Errorf("GetSourceFile(%q) = %q, want %q", tt.cfgPath, result, tt.expected)
		}
	}
}

func TestCFGAnalyzer_GetUncoveredBBsInFunction(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	coveredLines := make(map[LineID]bool)
	uncovered := analyzer.GetUncoveredBBsInFunction("test_func", coveredLines)

	if len(uncovered) != 3 {
		t.Errorf("Expected 3 uncovered BBs, got %d", len(uncovered))
	}

	// First should be BB 2 (most successors)
	if uncovered[0].BBID != 2 {
		t.Errorf("Expected first uncovered BB to be BB2, got BB%d", uncovered[0].BBID)
	}
	if uncovered[0].SuccessorCount != 2 {
		t.Errorf("Expected BB2 to have 2 successors, got %d", uncovered[0].SuccessorCount)
	}

	// Cover BB 2
	coveredLines[LineID{File: "/path/to/test.cc", Line: 10}] = true
	uncovered = analyzer.GetUncoveredBBsInFunction("test_func", coveredLines)
	if len(uncovered) != 2 {
		t.Errorf("Expected 2 uncovered BBs after covering BB2, got %d", len(uncovered))
	}
}

func TestCFGAnalyzer_Predecessors(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// Check test_func: BB 2 -> BB 3, BB 4; BB 3 -> BB 4
	fn, ok := analyzer.GetFunction("test_func")
	if !ok {
		t.Fatal("Failed to get test_func")
	}

	// BB 3 should have predecessor BB 2
	bb3 := fn.Blocks[3]
	if len(bb3.Predecessors) != 1 || bb3.Predecessors[0] != 2 {
		t.Errorf("BB3 predecessors: expected [2], got %v", bb3.Predecessors)
	}

	// BB 4 should have predecessors BB 2 and BB 3
	bb4 := fn.Blocks[4]
	if len(bb4.Predecessors) != 2 {
		t.Errorf("BB4 predecessors: expected 2, got %d: %v", len(bb4.Predecessors), bb4.Predecessors)
	}
	hasBB2, hasBB3 := false, false
	for _, pred := range bb4.Predecessors {
		if pred == 2 {
			hasBB2 = true
		}
		if pred == 3 {
			hasBB3 = true
		}
	}
	if !hasBB2 || !hasBB3 {
		t.Errorf("BB4 should have predecessors 2 and 3, got %v", bb4.Predecessors)
	}
}

func TestCFGAnalyzer_GetCoveredPredecessors(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// No coverage: no covered predecessors
	coveredLines := make(map[LineID]bool)
	coveredPreds := analyzer.GetCoveredPredecessors("test_func", 4, coveredLines)
	if len(coveredPreds) != 0 {
		t.Errorf("Expected no covered predecessors, got %v", coveredPreds)
	}

	// Cover BB 2 (line 10)
	coveredLines[LineID{File: "/path/to/test.cc", Line: 10}] = true
	coveredPreds = analyzer.GetCoveredPredecessors("test_func", 4, coveredLines)
	if len(coveredPreds) != 1 || coveredPreds[0] != 2 {
		t.Errorf("Expected covered predecessor [2], got %v", coveredPreds)
	}

	// Cover BB 3 (line 11) too
	coveredLines[LineID{File: "/path/to/test.cc", Line: 11}] = true
	coveredPreds = analyzer.GetCoveredPredecessors("test_func", 4, coveredLines)
	if len(coveredPreds) != 2 {
		t.Errorf("Expected 2 covered predecessors, got %v", coveredPreds)
	}
}

func TestCFGAnalyzer_WeightDecay(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// BB 2 has 2 successors, so initial weight = 2.0
	initialWeight := analyzer.GetBBWeight("test_func", 2)
	if initialWeight != 2.0 {
		t.Errorf("Expected initial weight 2.0, got %.2f", initialWeight)
	}

	// 63 attempts: no decay yet
	for i := 0; i < 63; i++ {
		analyzer.RecordAttempt("test_func", 2)
	}
	weight := analyzer.GetBBWeight("test_func", 2)
	if weight != 2.0 {
		t.Errorf("Weight should not decay after 63 attempts, got %.2f", weight)
	}

	// 64th attempt triggers decay
	analyzer.RecordAttempt("test_func", 2)
	weight = analyzer.GetBBWeight("test_func", 2)
	expectedWeight := 2.0 * WeightDecayFactor // 1.8
	if weight != expectedWeight {
		t.Errorf("Expected weight %.2f after 64 attempts, got %.2f", expectedWeight, weight)
	}

	// Record success: should reset attempts
	analyzer.RecordSuccess("test_func", 2)
	attempts := analyzer.GetBBAttempts("test_func", 2)
	if attempts != 0 {
		t.Errorf("Expected 0 attempts after success, got %d", attempts)
	}
}

func TestCFGAnalyzer_SelectTargetBB_WithWeightDecay(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(sampleCFG), 0644); err != nil {
		t.Fatalf("Failed to write test CFG file: %v", err)
	}

	analyzer := NewCFGAnalyzer(cfgPath)
	if err := analyzer.Parse(); err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	coveredLines := make(map[LineID]bool)

	// Initially BB 2 should be selected (2 successors)
	target := analyzer.SelectTargetBB([]string{"test_func"}, coveredLines)
	if target.BBID != 2 {
		t.Errorf("Expected BB 2 to be selected initially, got BB %d", target.BBID)
	}

	// Decay BB 2's weight heavily (128 attempts = 2 decays = 0.81)
	for i := 0; i < 128; i++ {
		analyzer.RecordAttempt("test_func", 2)
	}

	// BB 2 weight: 2.0 * 0.9 * 0.9 = 1.62
	// BB 3 weight: 1.0 (1 successor)
	// BB 4 weight: 1.0 (1 successor)
	// BB 2 should still be selected (1.62 > 1.0)
	target = analyzer.SelectTargetBB([]string{"test_func"}, coveredLines)
	if target.BBID != 2 {
		t.Errorf("BB 2 should still be selected (weight 1.62 > 1.0), got BB %d", target.BBID)
	}

	// Heavy decay on BB 2 (256 more attempts = 4 more decays)
	for i := 0; i < 256; i++ {
		analyzer.RecordAttempt("test_func", 2)
	}
	// BB 2 weight now: 2.0 * 0.9^6 ≈ 1.06

	// Still BB 2 since it still has higher weight
	target = analyzer.SelectTargetBB([]string{"test_func"}, coveredLines)
	weight := analyzer.GetBBWeight("test_func", 2)
	t.Logf("BB 2 weight after heavy decay: %.4f", weight)

	// Continue decay until BB 2's weight drops below 1.0
	for analyzer.GetBBWeight("test_func", 2) >= 1.0 {
		for i := 0; i < 64; i++ {
			analyzer.RecordAttempt("test_func", 2)
		}
	}

	// Now BB 3 or BB 4 should be selected (weight 1.0 > BB2's weight)
	target = analyzer.SelectTargetBB([]string{"test_func"}, coveredLines)
	if target.BBID == 2 {
		t.Errorf("BB 2 should not be selected when its weight (%.4f) < 1.0", analyzer.GetBBWeight("test_func", 2))
	}
}
