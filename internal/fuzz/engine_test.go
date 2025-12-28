package fuzz

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/coverage"
)

func TestCFGGuidedEngine_NewEngine(t *testing.T) {
	// Create a minimal config
	cfg := CFGGuidedConfig{
		MaxIterations: 10,
		MaxRetries:    3,
	}

	engine := NewCFGGuidedEngine(cfg)

	if engine == nil {
		t.Fatal("NewCFGGuidedEngine returned nil")
	}

	if engine.cfg.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries=3, got %d", engine.cfg.MaxRetries)
	}
}

func TestCFGGuidedEngine_DefaultMaxRetries(t *testing.T) {
	cfg := CFGGuidedConfig{
		MaxRetries: 0, // Should default to 3
	}

	engine := NewCFGGuidedEngine(cfg)

	if engine.cfg.MaxRetries != 3 {
		t.Errorf("Expected default MaxRetries=3, got %d", engine.cfg.MaxRetries)
	}
}

func TestCFGGuidedEngine_GetBugs(t *testing.T) {
	engine := NewCFGGuidedEngine(CFGGuidedConfig{})

	bugs := engine.GetBugs()
	if bugs == nil {
		t.Error("GetBugs should return empty slice, not nil")
	}
	if len(bugs) != 0 {
		t.Errorf("Expected 0 bugs initially, got %d", len(bugs))
	}
}

func TestCFGGuidedEngine_GetIterationCount(t *testing.T) {
	engine := NewCFGGuidedEngine(CFGGuidedConfig{})

	if engine.GetIterationCount() != 0 {
		t.Error("Initial iteration count should be 0")
	}
}

func TestCFGGuidedEngine_GetTargetHits(t *testing.T) {
	engine := NewCFGGuidedEngine(CFGGuidedConfig{})

	if engine.GetTargetHits() != 0 {
		t.Error("Initial target hits should be 0")
	}
}

func TestCFGGuidedEngine_ExtractCoveredLines(t *testing.T) {
	engine := NewCFGGuidedEngine(CFGGuidedConfig{})

	// Currently returns empty - this is a placeholder test
	lines := engine.extractCoveredLines(nil)
	if lines == nil {
		t.Error("extractCoveredLines should return empty slice, not nil")
	}
}

// Integration test - requires real CFG file
func TestCFGGuidedEngine_WithAnalyzer(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Create sample CFG
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
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("Failed to write CFG file: %v", err)
	}

	mappingPath := filepath.Join(tmpDir, "mapping.json")

	// Create CFG analyzer
	analyzer, err := coverage.NewAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	if err != nil {
		t.Fatalf("Failed to create analyzer: %v", err)
	}

	// Create engine with analyzer
	cfg := CFGGuidedConfig{
		Analyzer:   analyzer,
		MaxIterations: 1,
		MappingPath:   mappingPath,
	}

	engine := NewCFGGuidedEngine(cfg)

	// Verify analyzer is set
	if engine.cfg.Analyzer == nil {
		t.Error("Analyzer should be set")
	}

	// Verify we can get function coverage
	funcCov := engine.cfg.Analyzer.GetFunctionCoverage()
	if stats, ok := funcCov["test_func"]; ok {
		if stats.Total != 3 {
			t.Errorf("Expected 3 total BBs, got %d", stats.Total)
		}
	} else {
		t.Error("test_func should be in coverage map")
	}
}
