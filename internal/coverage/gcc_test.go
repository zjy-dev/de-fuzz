package coverage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

func TestGcovrReport_ToBytes(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "gcovr-report-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name       string
		setupFile  bool
		fileData   []byte
		reportPath string
		wantErr    bool
	}{
		{
			name:       "valid report file",
			setupFile:  true,
			fileData:   []byte(`{"gcovr/format_version": "0.5"}`),
			reportPath: filepath.Join(tmpDir, "valid.json"),
			wantErr:    false,
		},
		{
			name:       "empty path",
			setupFile:  false,
			reportPath: "",
			wantErr:    true,
		},
		{
			name:       "file not exist",
			setupFile:  false,
			reportPath: filepath.Join(tmpDir, "nonexistent.json"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test file if needed
			if tt.setupFile {
				if err := os.WriteFile(tt.reportPath, tt.fileData, 0644); err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
			}

			r := &GcovrReport{path: tt.reportPath}
			got, err := r.ToBytes()
			if (err != nil) != tt.wantErr {
				t.Errorf("ToBytes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && string(got) != string(tt.fileData) {
				t.Errorf("ToBytes() = %v, want %v", string(got), string(tt.fileData))
			}
		})
	}
}

func TestGCCCoverage_Clean(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "gcc-coverage-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create some .gcda and .gcno files
	gcdaFile := filepath.Join(tmpDir, "test.gcda")
	gcnoFile := filepath.Join(tmpDir, "test.gcno")
	if err := os.WriteFile(gcdaFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test .gcda file: %v", err)
	}
	if err := os.WriteFile(gcnoFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test .gcno file: %v", err)
	}

	// Create GCCCoverage instance
	executor := exec.NewCommandExecutor()
	compileFunc := func(s *seed.Seed) error {
		return nil // Mock compile function
	}
	gcc := NewGCCCoverage(
		executor,
		compileFunc,
		tmpDir,
		"gcovr",
		filepath.Join(tmpDir, "total.json"),
		"",
	)

	// Test Clean method
	if err := gcc.Clean(); err != nil {
		t.Errorf("Clean() error = %v", err)
	}

	// Verify .gcda file is deleted (runtime coverage data should be cleaned)
	if _, err := os.Stat(gcdaFile); !os.IsNotExist(err) {
		t.Error(".gcda file was not deleted")
	}

	// Verify .gcno file still exists (compile-time notes should NOT be deleted)
	if _, err := os.Stat(gcnoFile); os.IsNotExist(err) {
		t.Error(".gcno file should not be deleted, it contains compile-time coverage notes")
	}
}

func TestGCCCoverage_GetTotalReport_NotExist(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gcc-coverage-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	executor := exec.NewCommandExecutor()
	compileFunc := func(s *seed.Seed) error {
		return nil // Mock compile function
	}
	gcc := NewGCCCoverage(
		executor,
		compileFunc,
		tmpDir,
		"gcovr",
		filepath.Join(tmpDir, "total.json"),
		"",
	)

	// Test GetTotalReport when file doesn't exist
	_, err = gcc.GetTotalReport()
	if err == nil {
		t.Error("GetTotalReport() should return error when file doesn't exist")
	}
}

func TestGCCCoverage_GetTotalReport_ValidJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gcc-coverage-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a valid JSON report file
	totalPath := filepath.Join(tmpDir, "total.json")
	validJSON := []byte(`{"gcovr/format_version": "0.5", "files": []}`)
	if err := os.WriteFile(totalPath, validJSON, 0644); err != nil {
		t.Fatalf("Failed to create total.json: %v", err)
	}

	executor := exec.NewCommandExecutor()
	compileFunc := func(s *seed.Seed) error {
		return nil // Mock compile function
	}
	gcc := NewGCCCoverage(
		executor,
		compileFunc,
		tmpDir,
		"gcovr",
		totalPath,
		"",
	)

	// Test GetTotalReport
	report, err := gcc.GetTotalReport()
	if err != nil {
		t.Errorf("GetTotalReport() error = %v", err)
		return
	}

	data, err := report.ToBytes()
	if err != nil {
		t.Errorf("ToBytes() error = %v", err)
		return
	}

	if string(data) != string(validJSON) {
		t.Errorf("GetTotalReport() returned incorrect data")
	}
}

func TestGCCCoverage_HasIncreased_FirstSeed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gcc-coverage-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	executor := exec.NewCommandExecutor()
	compileFunc := func(s *seed.Seed) error {
		return nil // Mock compile function
	}
	gcc := NewGCCCoverage(
		executor,
		compileFunc,
		tmpDir,
		"gcovr",
		filepath.Join(tmpDir, "total.json"),
		"",
	)

	// Create a new report file for testing
	newReportPath := filepath.Join(tmpDir, "new.json")
	newReportData := []byte(`{"gcovr/format_version": "0.5"}`)
	if err := os.WriteFile(newReportPath, newReportData, 0644); err != nil {
		t.Fatalf("Failed to create new report file: %v", err)
	}

	// Test HasIncreased when total.json doesn't exist (first seed)
	newReport := &GcovrReport{path: newReportPath}
	increased, err := gcc.HasIncreased(newReport)
	if err != nil {
		t.Errorf("HasIncreased() error = %v", err)
		return
	}

	if !increased {
		t.Error("HasIncreased() should return true for first seed")
	}
}
