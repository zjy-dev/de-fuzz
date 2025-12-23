package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewFileMetricsManager(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")

	mm := NewFileMetricsManager(stateDir)
	if mm == nil {
		t.Fatal("NewFileMetricsManager returned nil")
	}

	// Check initial state
	metrics := mm.GetMetrics()
	if metrics.TotalSeedsRun != 0 {
		t.Errorf("Expected TotalSeedsRun=0, got %d", metrics.TotalSeedsRun)
	}
	if metrics.StartTime.IsZero() {
		t.Error("StartTime should not be zero")
	}
}

func TestRecordSeedProcessed(t *testing.T) {
	tmpDir := t.TempDir()

	mm := NewFileMetricsManager(tmpDir)

	mm.RecordSeedProcessed()
	mm.RecordSeedProcessed()
	mm.RecordSeedProcessed()

	metrics := mm.GetMetrics()
	if metrics.TotalSeedsRun != 3 {
		t.Errorf("Expected TotalSeedsRun=3, got %d", metrics.TotalSeedsRun)
	}
}

func TestRecordCoverageIncrease(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)

	mm.RecordCoverageIncrease()
	mm.RecordCoverageIncrease()

	metrics := mm.GetMetrics()
	if metrics.CoverageIncrSeeds != 2 {
		t.Errorf("Expected CoverageIncrSeeds=2, got %d", metrics.CoverageIncrSeeds)
	}
}

func TestRecordOracleCheckAndFailure(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)

	// Record some checks
	mm.RecordOracleCheck()
	mm.RecordOracleCheck()
	mm.RecordOracleCheck()

	// Record a failure
	mm.RecordOracleFailure()

	metrics := mm.GetMetrics()
	if metrics.OracleChecks != 3 {
		t.Errorf("Expected OracleChecks=3, got %d", metrics.OracleChecks)
	}
	if metrics.OracleFailures != 1 {
		t.Errorf("Expected OracleFailures=1, got %d", metrics.OracleFailures)
	}
}

func TestRecordOracleError(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)

	mm.RecordOracleError()
	mm.RecordOracleError()

	metrics := mm.GetMetrics()
	if metrics.OracleErrors != 2 {
		t.Errorf("Expected OracleErrors=2, got %d", metrics.OracleErrors)
	}
}

func TestRecordCrash(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)

	mm.RecordCrash()

	metrics := mm.GetMetrics()
	if metrics.CrashedSeeds != 1 {
		t.Errorf("Expected CrashedSeeds=1, got %d", metrics.CrashedSeeds)
	}
}

func TestRecordCompileFailure(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)

	mm.RecordCompileFailure()
	mm.RecordCompileFailure()

	metrics := mm.GetMetrics()
	if metrics.CompileFailedSeeds != 2 {
		t.Errorf("Expected CompileFailedSeeds=2, got %d", metrics.CompileFailedSeeds)
	}
}

func TestRecordLLMStats(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)

	mm.RecordLLMCall()
	mm.RecordLLMCall()
	mm.RecordLLMError()
	mm.RecordSeedGenerated()
	mm.RecordSeedGenerated()
	mm.RecordSeedGenerated()

	metrics := mm.GetMetrics()
	if metrics.LLMCalls != 2 {
		t.Errorf("Expected LLMCalls=2, got %d", metrics.LLMCalls)
	}
	if metrics.LLMErrors != 1 {
		t.Errorf("Expected LLMErrors=1, got %d", metrics.LLMErrors)
	}
	if metrics.SeedsGenerated != 3 {
		t.Errorf("Expected SeedsGenerated=3, got %d", metrics.SeedsGenerated)
	}
}

func TestUpdateCoverageStats(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)

	mm.UpdateCoverageStats(50.0, 500, 1000)

	metrics := mm.GetMetrics()
	if metrics.TotalLines != 1000 {
		t.Errorf("Expected TotalLines=1000, got %d", metrics.TotalLines)
	}
	if metrics.TotalCoveredLines != 500 {
		t.Errorf("Expected TotalCoveredLines=500, got %d", metrics.TotalCoveredLines)
	}
	if metrics.CurrentCoverage != 50.0 {
		t.Errorf("Expected CurrentCoverage=50.0, got %f", metrics.CurrentCoverage)
	}

	// Update again
	mm.UpdateCoverageStats(60.0, 600, 1000)
	metrics = mm.GetMetrics()
	if metrics.TotalCoveredLines != 600 {
		t.Errorf("Expected TotalCoveredLines=600, got %d", metrics.TotalCoveredLines)
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Use temp directory
	tmpDir := t.TempDir()
	t.Logf("Temp dir: %s", tmpDir)

	// Create and populate metrics
	mm := NewFileMetricsManager(tmpDir)
	mm.RecordSeedProcessed()
	mm.RecordSeedProcessed()
	mm.RecordCoverageIncrease()
	mm.RecordOracleCheck()
	mm.RecordOracleFailure()
	mm.RecordLLMCall()

	// Get the actual file path
	filePath := mm.GetFilePath()
	t.Logf("File path: %s", filePath)

	// Save to file
	err := mm.Save()
	if err != nil {
		t.Fatalf("Failed to save metrics: %v", err)
	}

	// List directory contents for debugging
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		info, _ := e.Info()
		t.Logf("Entry: %s, IsDir: %v, Size: %d", e.Name(), e.IsDir(), info.Size())
	}

	// Verify file exists
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Failed to stat metrics file: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("File path is a directory, not a file")
	}
	t.Logf("File size: %d bytes", info.Size())

	// Read and verify JSON content
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read metrics file: %v", err)
	}

	var loadedMetrics FuzzMetrics
	err = json.Unmarshal(data, &loadedMetrics)
	if err != nil {
		t.Fatalf("Failed to unmarshal metrics: %v", err)
	}

	if loadedMetrics.TotalSeedsRun != 2 {
		t.Errorf("Expected TotalSeedsRun=2, got %d", loadedMetrics.TotalSeedsRun)
	}
	if loadedMetrics.CoverageIncrSeeds != 1 {
		t.Errorf("Expected CoverageIncrSeeds=1, got %d", loadedMetrics.CoverageIncrSeeds)
	}
	if loadedMetrics.OracleChecks != 1 {
		t.Errorf("Expected OracleChecks=1, got %d", loadedMetrics.OracleChecks)
	}
	if loadedMetrics.OracleFailures != 1 {
		t.Errorf("Expected OracleFailures=1, got %d", loadedMetrics.OracleFailures)
	}
	if loadedMetrics.LLMCalls != 1 {
		t.Errorf("Expected LLMCalls=1, got %d", loadedMetrics.LLMCalls)
	}
}

func TestFormatSummary(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)
	mm.RecordSeedProcessed()
	mm.RecordSeedProcessed()
	mm.RecordCoverageIncrease()
	mm.RecordOracleCheck()
	mm.RecordOracleFailure()
	mm.UpdateCoverageStats(50.0, 500, 1000)

	summary := mm.FormatSummary()

	// Check that key information is present
	expectedParts := []string{
		"FUZZING METRICS",
		"Seeds Processed:    2",
		"Coverage Increase:  1",
		"Oracle Checks:      1",
		"Oracle Failures:    1",
		"Coverage:           50.00%",
		"500/1000 lines",
	}

	for _, part := range expectedParts {
		if !strings.Contains(summary, part) {
			t.Errorf("Summary missing expected part: %s\nGot: %s", part, summary)
		}
	}
}

func TestFormatOneLine(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)
	mm.RecordSeedProcessed()
	mm.RecordSeedProcessed()
	mm.RecordCoverageIncrease()
	mm.RecordOracleCheck()
	mm.RecordOracleFailure()
	mm.UpdateCoverageStats(50.0, 500, 1000)

	oneLine := mm.FormatOneLine()

	// Check that key information is present (using actual format)
	expectedParts := []string{
		"seeds:2",
		"cov_incr:1",
		"oracle_fail:1",
		"cov:50.00%",
	}

	for _, part := range expectedParts {
		if !strings.Contains(oneLine, part) {
			t.Errorf("OneLine summary missing expected part: %s\nGot: %s", part, oneLine)
		}
	}
}

func TestRuntimeCalculation(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)

	// Wait a bit to have non-zero runtime
	time.Sleep(100 * time.Millisecond)

	summary := mm.FormatSummary()

	// Should contain duration information (format is like "0s" or "1s")
	if !strings.Contains(summary, "Duration:") {
		t.Error("Summary should contain Duration information")
	}
}

func TestConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	mm := NewFileMetricsManager(tmpDir)

	// Run concurrent operations
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				mm.RecordSeedProcessed()
				mm.RecordCoverageIncrease()
				mm.RecordOracleCheck()
				mm.RecordOracleFailure()
				mm.RecordLLMCall()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	metrics := mm.GetMetrics()

	// Each goroutine does 100 iterations, 10 goroutines = 1000 total per metric
	if metrics.TotalSeedsRun != 1000 {
		t.Errorf("Expected TotalSeedsRun=1000, got %d", metrics.TotalSeedsRun)
	}
	if metrics.CoverageIncrSeeds != 1000 {
		t.Errorf("Expected CoverageIncrSeeds=1000, got %d", metrics.CoverageIncrSeeds)
	}
	if metrics.OracleChecks != 1000 {
		t.Errorf("Expected OracleChecks=1000, got %d", metrics.OracleChecks)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	// Use nested path that doesn't exist
	metricsPath := filepath.Join(tmpDir, "nested", "dir")

	mm := NewFileMetricsManager(metricsPath)
	mm.RecordSeedProcessed()

	err := mm.Save()
	if err != nil {
		t.Fatalf("Save should create directory and succeed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(metricsPath); os.IsNotExist(err) {
		t.Fatal("Metrics file was not created")
	}
}
