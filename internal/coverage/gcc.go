package coverage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/seed"

	"github.com/zjy-dev/gcovr-json-util/v2/pkg/gcovr"
)

// GcovrReport represents a gcovr JSON coverage report.
// It stores only the path to the report file, not the actual data.
type GcovrReport struct {
	path string // Path to the gcovr JSON report file
}

// ToBytes reads and returns the JSON report data from the file.
func (r *GcovrReport) ToBytes() ([]byte, error) {
	if r.path == "" {
		return nil, fmt.Errorf("report path is empty")
	}

	data, err := os.ReadFile(r.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read report file %s: %w", r.path, err)
	}

	return data, nil
}

// GCCCoverage implements the Coverage interface using GCC's gcov/gcovr toolchain.
type GCCCoverage struct {
	executor         exec.Executor
	compileFunc      func(*seed.Seed) error // Function to compile a seed
	gcovrExecPath    string                 // Working directory for gcovr execution (e.g., build/gcc)
	gcovrCommand     string                 // Base gcovr command with options (without --json output path)
	totalReportPath  string                 // Path to total.json
	filterConfigPath string                 // Path to filter config YAML (from compiler-isa-strategy.yaml)
	seedReportDir    string                 // Directory to store individual seed reports
}

// NewGCCCoverage creates a new GCC coverage tracker using gcovr.
func NewGCCCoverage(
	executor exec.Executor,
	compileFunc func(*seed.Seed) error,
	gcovrExecPath string,
	gcovrCommand string,
	totalReportPath string,
	filterConfigPath string,
) *GCCCoverage {
	return &GCCCoverage{
		executor:         executor,
		compileFunc:      compileFunc,
		gcovrExecPath:    gcovrExecPath,
		gcovrCommand:     gcovrCommand,
		totalReportPath:  totalReportPath,
		filterConfigPath: filterConfigPath,
		seedReportDir:    filepath.Dir(totalReportPath), // Store seed reports in same dir as total.json
	}
}

// Clean removes all .gcda files from the gcovr execution path.
// Note: .gcno files (compile-time coverage notes) are NOT deleted because they
// contain structural information about the source code and are reused across runs.
// Only .gcda files (runtime coverage data) need to be cleaned before each measurement.
func (g *GCCCoverage) Clean() error {
	// Remove .gcda files (runtime coverage data)
	cleanGcdaCmd := fmt.Sprintf("find %s -name '*.gcda' -delete", g.gcovrExecPath)
	if _, err := g.executor.Run("sh", "-c", cleanGcdaCmd); err != nil {
		return fmt.Errorf("failed to clean .gcda files: %w", err)
	}

	cleanGcdaCmd = fmt.Sprintf("find %s -name '*.gcov' -delete", g.gcovrExecPath)
	if _, err := g.executor.Run("sh", "-c", cleanGcdaCmd); err != nil {
		return fmt.Errorf("failed to clean .gcov files: %w", err)
	}

	return nil
}

// Measure compiles the seed and generates a coverage report using gcovr.
// Returns a GcovrReport containing the path to the generated report file.
func (g *GCCCoverage) Measure(s *seed.Seed) (Report, error) {
	// Step 1: Clean previous coverage data (.gcda files)
	if err := g.Clean(); err != nil {
		return nil, fmt.Errorf("failed to clean coverage files: %w", err)
	}

	// Step 2: Compile the seed using the provided compile function
	// This will generate .gcda files in the gcovr execution path
	if g.compileFunc != nil {
		if err := g.compileFunc(s); err != nil {
			return nil, fmt.Errorf("failed to compile seed: %w", err)
		}
	}

	// Step 3: Generate coverage report using gcovr
	// The output path is determined from the seed ID
	seedReportPath := filepath.Join(g.seedReportDir, fmt.Sprintf("%s.json", s.ID))

	// Ensure the output directory exists
	if err := os.MkdirAll(g.seedReportDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create seed report directory: %w", err)
	}

	// Build the full gcovr command
	// Example: cd /build/gcc && gcovr --exclude '.*\.(h|hpp|hxx)$' --gcov-executable "gcov-14 --demangled-names" -r .. --json-pretty --json /path/to/<seed>.json
	fullCommand := fmt.Sprintf("cd %s && %s --json %s",
		g.gcovrExecPath,
		g.gcovrCommand,
		seedReportPath,
	)

	result, err := g.executor.Run("sh", "-c", fullCommand)
	if err != nil {
		return nil, fmt.Errorf("failed to run gcovr: %w (stdout: %s, stderr: %s)",
			err, result.Stdout, result.Stderr)
	}

	// Step 4: Verify the report file was created
	if _, err := os.Stat(seedReportPath); err != nil {
		return nil, fmt.Errorf("gcovr report file not created: %w", err)
	}

	return &GcovrReport{path: seedReportPath}, nil
}

// HasIncreased checks if the new report has increased coverage compared to the total.
// If total.json doesn't exist, this is considered the first seed and returns true.
func (g *GCCCoverage) HasIncreased(newReport Report) (bool, error) {
	// Get the path to the new report
	gcovrRep, ok := newReport.(*GcovrReport)
	if !ok {
		return false, fmt.Errorf("expected GcovrReport, got %T", newReport)
	}

	// If total report doesn't exist, this is the first seed
	if _, err := os.Stat(g.totalReportPath); os.IsNotExist(err) {
		return true, nil
	}

	// Parse the base (total) report using gcovr-json-util
	baseReport, err := gcovr.ParseReport(g.totalReportPath)
	if err != nil {
		return false, fmt.Errorf("failed to parse base report: %w", err)
	}

	// Parse the new report using gcovr-json-util
	newReportParsed, err := gcovr.ParseReport(gcovrRep.path)
	if err != nil {
		return false, fmt.Errorf("failed to parse new report: %w", err)
	}

	// Apply filtering if filter config is provided
	if g.filterConfigPath != "" {
		filterConfig, err := gcovr.ParseFilterConfig(g.filterConfigPath)
		if err != nil {
			return false, fmt.Errorf("failed to parse filter config: %w", err)
		}
		baseReport = gcovr.ApplyFilter(baseReport, filterConfig)
		newReportParsed = gcovr.ApplyFilter(newReportParsed, filterConfig)
	}

	// Compute coverage increase
	increaseReport, err := gcovr.ComputeCoverageIncrease(baseReport, newReportParsed)
	if err != nil {
		return false, fmt.Errorf("failed to compute coverage increase: %w", err)
	}

	fmt.Println(gcovr.FormatReport(increaseReport))

	// If the increase report has no increases, there's no coverage increase
	return len(increaseReport.Increases) > 0, nil
}

// Merge merges the new coverage report into the total report.
// If total.json doesn't exist, copies the new report as total.json.
// Otherwise, uses gcovr to merge: mv total.json tmp.json && gcovr -a tmp.json -a <seed>.json -o total.json && rm tmp.json
func (g *GCCCoverage) Merge(newReport Report) error {
	// Get the path to the new report
	gcovrRep, ok := newReport.(*GcovrReport)
	if !ok {
		return fmt.Errorf("expected GcovrReport, got %T", newReport)
	}

	// If total report doesn't exist, just copy the new report as total
	if _, err := os.Stat(g.totalReportPath); os.IsNotExist(err) {
		// Ensure the directory exists
		if err := os.MkdirAll(filepath.Dir(g.totalReportPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for total report: %w", err)
		}

		// Copy the seed report to total.json
		data, err := os.ReadFile(gcovrRep.path)
		if err != nil {
			return fmt.Errorf("failed to read new report: %w", err)
		}

		if err := os.WriteFile(g.totalReportPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write total report: %w", err)
		}
		return nil
	}

	// Merge using gcovr command as described in README:
	// mv total.json tmp.json && gcovr --json-pretty --json total.json -a tmp.json -a <seed>.json && rm tmp.json
	tmpReportPath := g.totalReportPath + ".tmp.json"

	// Rename current total to tmp
	if err := os.Rename(g.totalReportPath, tmpReportPath); err != nil {
		return fmt.Errorf("failed to rename total report to tmp: %w", err)
	}

	// Run gcovr merge command
	mergeCmd := fmt.Sprintf("gcovr -a %s -a %s --json-pretty --json %s",
		tmpReportPath,
		gcovrRep.path,
		g.totalReportPath,
	)

	result, err := g.executor.Run("sh", "-c", mergeCmd)
	if err != nil {
		// Try to restore the original total.json if merge fails
		os.Rename(tmpReportPath, g.totalReportPath)
		return fmt.Errorf("failed to merge reports: %w (stdout: %s, stderr: %s)",
			err, result.Stdout, result.Stderr)
	}

	// Remove tmp file
	os.Remove(tmpReportPath)

	return nil
}

// GetTotalReport returns the current total accumulated coverage report.
func (g *GCCCoverage) GetTotalReport() (Report, error) {
	// Check if total report exists
	if _, err := os.Stat(g.totalReportPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("total report does not exist: %s", g.totalReportPath)
	}

	// Validate it's valid JSON by attempting to parse it
	var js json.RawMessage
	data, err := os.ReadFile(g.totalReportPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read total report: %w", err)
	}

	if err := json.Unmarshal(data, &js); err != nil {
		return nil, fmt.Errorf("total report is not valid JSON: %w", err)
	}

	return &GcovrReport{path: g.totalReportPath}, nil
}
