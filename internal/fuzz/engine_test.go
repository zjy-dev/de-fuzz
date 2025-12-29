package fuzz

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/coverage"
)

func TestEngine_NewEngine(t *testing.T) {
	// Create a minimal config
	cfg := Config{
		MaxIterations: 10,
		MaxRetries:    3,
	}

	engine := NewEngine(cfg)

	if engine == nil {
		t.Fatal("NewEngine returned nil")
	}

	if engine.cfg.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries=3, got %d", engine.cfg.MaxRetries)
	}
}

func TestEngine_DefaultMaxRetries(t *testing.T) {
	cfg := Config{
		MaxRetries: 0, // Should default to 3
	}

	engine := NewEngine(cfg)

	if engine.cfg.MaxRetries != 3 {
		t.Errorf("Expected default MaxRetries=3, got %d", engine.cfg.MaxRetries)
	}
}

func TestEngine_GetBugs(t *testing.T) {
	engine := NewEngine(Config{})

	bugs := engine.GetBugs()
	if bugs == nil {
		t.Error("GetBugs should return empty slice, not nil")
	}
	if len(bugs) != 0 {
		t.Errorf("Expected 0 bugs initially, got %d", len(bugs))
	}
}

func TestEngine_GetIterationCount(t *testing.T) {
	engine := NewEngine(Config{})

	if engine.GetIterationCount() != 0 {
		t.Error("Initial iteration count should be 0")
	}
}

func TestEngine_GetTargetHits(t *testing.T) {
	engine := NewEngine(Config{})

	if engine.GetTargetHits() != 0 {
		t.Error("Initial target hits should be 0")
	}
}

func TestEngine_ExtractCoveredLines(t *testing.T) {
	engine := NewEngine(Config{})

	// Currently returns empty - this is a placeholder test
	lines := engine.extractCoveredLines(nil)
	if lines == nil {
		t.Error("extractCoveredLines should return empty slice, not nil")
	}
}

// Integration test - requires real CFG file
func TestEngine_WithAnalyzer(t *testing.T) {
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
	analyzer, err := coverage.NewAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath, 0.8)
	if err != nil {
		t.Fatalf("Failed to create analyzer: %v", err)
	}

	// Create engine with analyzer
	cfg := Config{
		Analyzer:      analyzer,
		MaxIterations: 1,
		MappingPath:   mappingPath,
	}

	engine := NewEngine(cfg)

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
