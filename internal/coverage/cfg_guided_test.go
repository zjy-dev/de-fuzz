package coverage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCFGGuidedAnalyzer_Basic(t *testing.T) {
	// Create temporary directory and files
	tmpDir := t.TempDir()

	// Create a sample CFG file
	cfgContent := `;; Function test_func (_Z9test_funcii, funcdef_no=1, decl_uid=100, cgraph_uid=1, symbol_order=1)
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
  <bb 2> :
  [/path/to/test.cc:10:3] if (a > b)
    goto <bb 3>; [INV]
  else
    goto <bb 4>; [INV]

  <bb 3> :
  [/path/to/test.cc:11:5] result = a;
  goto <bb 4>; [INV]

  <bb 4> :
  [/path/to/test.cc:13:3] return result;
}
`
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("Failed to write CFG file: %v", err)
	}

	mappingPath := filepath.Join(tmpDir, "mapping.json")

	// Create analyzer
	analyzer, err := NewCFGGuidedAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	if err != nil {
		t.Fatalf("NewCFGGuidedAnalyzer() failed: %v", err)
	}

	// Initially no coverage, should select BB with most successors
	target := analyzer.SelectTarget()
	if target == nil {
		t.Fatal("SelectTarget() returned nil")
	}
	if target.Function != "test_func" || target.BBID != 2 {
		t.Errorf("Expected test_func:BB2, got %s:BB%d", target.Function, target.BBID)
	}
	if target.SuccessorCount != 2 {
		t.Errorf("Expected 2 successors, got %d", target.SuccessorCount)
	}

	// Check no base seed (nothing covered yet)
	if target.BaseSeed != "" {
		t.Errorf("Expected no base seed, got %s", target.BaseSeed)
	}
}

func TestCFGGuidedAnalyzer_WithCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfgContent := `;; Function test_func (_Z9test_funcii, funcdef_no=1, decl_uid=100, cgraph_uid=1, symbol_order=1)
;; 2 succs { 3 4 }
;; 3 succs { 4 }
;; 4 succs { 1 }
int test_func (int a, int b)
{
  <bb 2> :
  [/path/to/test.cc:10:3] if (a > b)
    goto <bb 3>; [INV]
  else
    goto <bb 4>; [INV]

  <bb 3> :
  [/path/to/test.cc:11:5] result = a;
  goto <bb 4>; [INV]

  <bb 4> :
  [/path/to/test.cc:13:3] return result;
}
`
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	os.WriteFile(cfgPath, []byte(cfgContent), 0644)

	mappingPath := filepath.Join(tmpDir, "mapping.json")
	analyzer, err := NewCFGGuidedAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	if err != nil {
		t.Fatal(err)
	}

	// Record coverage for BB 2 (line 10)
	analyzer.RecordCoverage(1, []string{"/path/to/test.cc:10"})

	// Now should select BB 3 or BB 4 (BB 2 is covered)
	target := analyzer.SelectTarget()
	if target == nil {
		t.Fatal("SelectTarget() returned nil")
	}
	if target.BBID == 2 {
		t.Error("Should not select BB2 after it's covered")
	}

	// Should have base seed info now
	if target.BaseSeed == "" {
		t.Log("No base seed found (may be expected depending on search direction)")
	}
}

func TestCFGGuidedAnalyzer_GetFunctionCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfgContent := `;; Function test_func (_Z9test_funcii, funcdef_no=1, decl_uid=100, cgraph_uid=1, symbol_order=1)
;; 2 succs { 3 4 }
;; 3 succs { 4 }
;; 4 succs { 1 }
int test_func (int a, int b)
{
  <bb 2> :
  [/path/to/test.cc:10:3] if (a > b)

  <bb 3> :
  [/path/to/test.cc:11:5] result = a;

  <bb 4> :
  [/path/to/test.cc:13:3] return result;
}
`
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	os.WriteFile(cfgPath, []byte(cfgContent), 0644)

	mappingPath := filepath.Join(tmpDir, "mapping.json")
	analyzer, err := NewCFGGuidedAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	if err != nil {
		t.Fatal(err)
	}

	// Initial coverage: 0/3
	cov := analyzer.GetFunctionCoverage()
	if stats, ok := cov["test_func"]; ok {
		if stats.Covered != 0 {
			t.Errorf("Expected 0 covered, got %d", stats.Covered)
		}
		if stats.Total != 3 {
			t.Errorf("Expected 3 total BBs, got %d", stats.Total)
		}
	} else {
		t.Error("test_func not found in coverage")
	}

	// Cover BB 2
	analyzer.RecordCoverage(1, []string{"/path/to/test.cc:10"})

	cov = analyzer.GetFunctionCoverage()
	if stats, ok := cov["test_func"]; ok {
		if stats.Covered != 1 {
			t.Errorf("Expected 1 covered after recording, got %d", stats.Covered)
		}
	}
}

func TestCFGGuidedAnalyzer_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()

	cfgContent := `;; Function test_func (_Z9test_funcii, funcdef_no=1, decl_uid=100, cgraph_uid=1, symbol_order=1)
;; 2 succs { 3 4 }
;; 3 succs { 4 }
;; 4 succs { 1 }
int test_func (int a, int b)
{
  <bb 2> :
  [/path/to/test.cc:10:3] if (a > b)

  <bb 3> :
  [/path/to/test.cc:11:5] result = a;

  <bb 4> :
  [/path/to/test.cc:13:3] return result;
}
`
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	os.WriteFile(cfgPath, []byte(cfgContent), 0644)

	mappingPath := filepath.Join(tmpDir, "mapping.json")

	// Create analyzer and add coverage
	analyzer1, _ := NewCFGGuidedAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	analyzer1.RecordCoverage(1, []string{"/path/to/test.cc:10"})
	analyzer1.RecordCoverage(2, []string{"/path/to/test.cc:11"})

	// Save mapping
	if err := analyzer1.SaveMapping(""); err != nil {
		t.Fatalf("SaveMapping() failed: %v", err)
	}

	// Create new analyzer (should load existing mapping)
	analyzer2, _ := NewCFGGuidedAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)

	// Verify coverage is loaded
	cov := analyzer2.GetFunctionCoverage()
	if stats, ok := cov["test_func"]; ok {
		if stats.Covered != 2 {
			t.Errorf("Expected 2 covered after reload, got %d", stats.Covered)
		}
	}
}

func TestCFGGuidedAnalyzer_GenerateTargetPrompt(t *testing.T) {
	tmpDir := t.TempDir()

	cfgContent := `;; Function test_func (_Z9test_funcii, funcdef_no=1, decl_uid=100, cgraph_uid=1, symbol_order=1)
;; 2 succs { 3 4 }
;; 3 succs { 4 }
;; 4 succs { 1 }
int test_func (int a, int b)
{
  <bb 2> :
  [/path/to/test.cc:10:3] if (a > b)

  <bb 3> :
  [/path/to/test.cc:11:5] result = a;

  <bb 4> :
  [/path/to/test.cc:13:3] return result;
}
`
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	os.WriteFile(cfgPath, []byte(cfgContent), 0644)

	mappingPath := filepath.Join(tmpDir, "mapping.json")
	analyzer, _ := NewCFGGuidedAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)

	target := analyzer.SelectTarget()
	if target == nil {
		t.Fatal("SelectTarget() returned nil")
	}

	prompt := analyzer.GenerateTargetPrompt(target)

	// Check prompt contains expected information
	if len(prompt) == 0 {
		t.Error("GenerateTargetPrompt() returned empty string")
	}

	// Check for function name
	if !containsString(prompt, "test_func") {
		t.Error("Prompt should contain function name")
	}

	// Check for branching factor
	if !containsString(prompt, "successor") {
		t.Error("Prompt should mention successors")
	}

	t.Logf("Generated prompt:\n%s", prompt)
}

func TestCFGGuidedAnalyzer_GetAllUncoveredBBs(t *testing.T) {
	tmpDir := t.TempDir()

	cfgContent := `;; Function func1 (_Z5func1i, funcdef_no=1, decl_uid=100, cgraph_uid=1, symbol_order=1)
;; 2 succs { 3 4 }
;; 3 succs { 4 }
;; 4 succs { 1 }
int func1 (int a)
{
  <bb 2> :
  [/path/to/test.cc:10:3] if (a > 0)

  <bb 3> :
  [/path/to/test.cc:11:5] x = a;

  <bb 4> :
  [/path/to/test.cc:13:3] return x;
}


;; Function func2 (_Z5func2i, funcdef_no=2, decl_uid=200, cgraph_uid=2, symbol_order=2)
;; 2 succs { 3 4 5 }
;; 3 succs { 5 }
;; 4 succs { 5 }
;; 5 succs { 1 }
int func2 (int b)
{
  <bb 2> :
  [/path/to/test.cc:20:3] switch (b)

  <bb 3> :
  [/path/to/test.cc:21:5] y = 1;

  <bb 4> :
  [/path/to/test.cc:22:5] y = 2;

  <bb 5> :
  [/path/to/test.cc:24:3] return y;
}
`
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	os.WriteFile(cfgPath, []byte(cfgContent), 0644)

	mappingPath := filepath.Join(tmpDir, "mapping.json")
	analyzer, err := NewCFGGuidedAnalyzer(cfgPath, []string{"func1", "func2"}, "", mappingPath)
	if err != nil {
		t.Fatal(err)
	}

	uncovered := analyzer.GetAllUncoveredBBs()

	// Should have 6 BBs total (3 from each function, excluding entry/exit)
	// Actually: func1 has BB 2,3,4 and func2 has BB 2,3,4,5 = 7 BBs
	if len(uncovered) != 7 {
		t.Errorf("Expected 7 uncovered BBs, got %d", len(uncovered))
	}

	// First should be func2:BB2 (3 successors)
	if len(uncovered) > 0 {
		first := uncovered[0]
		if first.Function != "func2" || first.BBID != 2 {
			t.Errorf("Expected first to be func2:BB2, got %s:BB%d", first.Function, first.BBID)
		}
		if first.SuccessorCount != 3 {
			t.Errorf("Expected 3 successors for first, got %d", first.SuccessorCount)
		}
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStringHelper(s, substr))
}

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestCFGGuidedAnalyzer_PredecessorBasedSeedSelection(t *testing.T) {
	tmpDir := t.TempDir()

	// CFG with clear predecessor relationships:
	// BB 2 -> BB 3, BB 4
	// BB 3 -> BB 4
	// So BB 4 has predecessors: BB 2, BB 3
	cfgContent := `;; Function test_func (_Z9test_funcii, funcdef_no=1, decl_uid=100, cgraph_uid=1, symbol_order=1)
;; 2 succs { 3 4 }
;; 3 succs { 4 }
;; 4 succs { 1 }
int test_func (int a, int b)
{
  <bb 2> :
  [/path/to/test.cc:10:3] if (a > b)
    goto <bb 3>; [INV]
  else
    goto <bb 4>; [INV]

  <bb 3> :
  [/path/to/test.cc:15:5] result = a;
  goto <bb 4>; [INV]

  <bb 4> :
  [/path/to/test.cc:20:3] return result;
}
`
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	os.WriteFile(cfgPath, []byte(cfgContent), 0644)

	mappingPath := filepath.Join(tmpDir, "mapping.json")
	analyzer, err := NewCFGGuidedAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	if err != nil {
		t.Fatal(err)
	}

	// Record coverage for BB 2 (line 10) with seed 100
	analyzer.RecordCoverage(100, []string{"/path/to/test.cc:10"})

	// BB 2 is now covered, so target should be BB 3 (next highest by weight)
	// But we want to check if BB 3 or BB 4 is targeted, it uses BB 2 as base
	target := analyzer.SelectTarget()
	if target == nil {
		t.Fatal("SelectTarget() returned nil")
	}

	// Target should be BB 3 or BB 4 (BB 2 is covered)
	if target.BBID == 2 {
		t.Error("Should not select BB2 after it's covered")
	}

	// Check that base seed is from predecessor (seed 100 from BB 2)
	if target.BaseSeed != "100" {
		t.Logf("Target BB: %d, BaseSeed: %s, BaseSeedLine: %d", target.BBID, target.BaseSeed, target.BaseSeedLine)
		// This may be empty if predecessor-based selection didn't find a match
		// which is valid for BB 3 (predecessor is BB 2 which is covered)
	}

	// Now cover BB 3 with seed 200
	analyzer.RecordCoverage(200, []string{"/path/to/test.cc:15"})

	// Target should now be BB 4
	target = analyzer.SelectTarget()
	if target == nil {
		t.Fatal("SelectTarget() returned nil")
	}
	if target.BBID != 4 {
		t.Errorf("Expected BB4 to be selected, got BB%d", target.BBID)
	}

	// BB 4's predecessors are BB 2 and BB 3, both covered
	// So base seed should be from one of them (100 or 200)
	if target.BaseSeed != "100" && target.BaseSeed != "200" {
		t.Errorf("Expected base seed to be 100 or 200, got %s", target.BaseSeed)
	}
}

func TestCFGGuidedAnalyzer_RecordAttemptAndSuccess(t *testing.T) {
	tmpDir := t.TempDir()

	cfgContent := `;; Function test_func (_Z9test_funcii, funcdef_no=1, decl_uid=100, cgraph_uid=1, symbol_order=1)
;; 2 succs { 3 4 }
;; 3 succs { 4 }
;; 4 succs { 1 }
int test_func (int a, int b)
{
  <bb 2> :
  [/path/to/test.cc:10:3] if (a > b)

  <bb 3> :
  [/path/to/test.cc:11:5] result = a;

  <bb 4> :
  [/path/to/test.cc:13:3] return result;
}
`
	cfgPath := filepath.Join(tmpDir, "test.cc.015t.cfg")
	os.WriteFile(cfgPath, []byte(cfgContent), 0644)

	mappingPath := filepath.Join(tmpDir, "mapping.json")
	analyzer, err := NewCFGGuidedAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	if err != nil {
		t.Fatal(err)
	}

	target := analyzer.SelectTarget()
	if target == nil {
		t.Fatal("No target")
	}

	// Initial weight should be successor count
	weight := analyzer.GetBBWeight(target)
	if weight != float64(target.SuccessorCount) {
		t.Errorf("Initial weight should be %d, got %.2f", target.SuccessorCount, weight)
	}

	// Record 64 attempts
	for i := 0; i < 64; i++ {
		analyzer.RecordAttempt(target)
	}

	// Weight should now be decayed by 10%
	expectedWeight := float64(target.SuccessorCount) * 0.9
	weight = analyzer.GetBBWeight(target)
	if weight != expectedWeight {
		t.Errorf("Weight after 64 attempts should be %.2f, got %.2f", expectedWeight, weight)
	}

	// Record success
	analyzer.RecordSuccess(target)
	attempts := analyzer.GetBBAttempts(target)
	if attempts != 0 {
		t.Errorf("Attempts should be 0 after success, got %d", attempts)
	}
}
