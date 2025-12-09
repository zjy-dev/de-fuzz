package coverage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	sourceParentPath string                 // Parent path for source files (for abstract generation)

	// Cached filter config (loaded once)
	filterConfig *gcovr.FilterConfig

	// Cache for last computed increase (to avoid recomputing in GetIncrease)
	lastIncreaseReport *gcovr.CoverageIncreaseReport
}

// NewGCCCoverage creates a new GCC coverage tracker using gcovr.
func NewGCCCoverage(
	executor exec.Executor,
	compileFunc func(*seed.Seed) error,
	gcovrExecPath string,
	gcovrCommand string,
	totalReportPath string,
	filterConfigPath string,
	sourceParentPath string,
) *GCCCoverage {
	// Ensure totalReportPath is absolute for consistent file operations
	// This is critical because gcovr runs in a different directory (gcovrExecPath)
	absTotalReportPath := totalReportPath
	if !filepath.IsAbs(totalReportPath) {
		if abs, err := filepath.Abs(totalReportPath); err == nil {
			absTotalReportPath = abs
		}
	}

	g := &GCCCoverage{
		executor:         executor,
		compileFunc:      compileFunc,
		gcovrExecPath:    gcovrExecPath,
		gcovrCommand:     gcovrCommand,
		totalReportPath:  absTotalReportPath,
		filterConfigPath: filterConfigPath,
		seedReportDir:    filepath.Dir(absTotalReportPath), // Store seed reports in same dir as total.json
		sourceParentPath: sourceParentPath,
	}

	// Pre-load filter config if available
	if filterConfigPath != "" {
		if fc, err := gcovr.ParseFilterConfig(filterConfigPath); err == nil {
			g.filterConfig = fc
		}
	}

	return g
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
	seedReportPath := filepath.Join(g.seedReportDir, fmt.Sprintf("%d.json", s.Meta.ID))

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
	// Reset cached increase report
	g.lastIncreaseReport = nil

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

	// Apply filtering if filter config is available
	if g.filterConfig != nil {
		baseReport = gcovr.ApplyFilter(baseReport, g.filterConfig)
		newReportParsed = gcovr.ApplyFilter(newReportParsed, g.filterConfig)
	}

	// Compute coverage increase
	increaseReport, err := gcovr.ComputeCoverageIncrease(baseReport, newReportParsed)
	if err != nil {
		return false, fmt.Errorf("failed to compute coverage increase: %w", err)
	}

	// Cache the increase report for GetIncrease
	g.lastIncreaseReport = increaseReport

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

// GetIncrease returns detailed information about the coverage increase.
// Should be called after HasIncreased returns true to get the details.
func (g *GCCCoverage) GetIncrease(newReport Report) (*CoverageIncrease, error) {
	// If we have a cached increase report from HasIncreased, use it
	if g.lastIncreaseReport == nil {
		// Need to recompute - call HasIncreased first
		_, err := g.HasIncreased(newReport)
		if err != nil {
			return nil, fmt.Errorf("failed to compute increase: %w", err)
		}
	}

	// If still nil (first seed case), return a special increase
	if g.lastIncreaseReport == nil {
		// For first seed, generate uncovered abstract from the new report
		uncoveredAbstract := g.generateUncoveredAbstract(newReport)
		return &CoverageIncrease{
			Summary:           "First seed - initial coverage established",
			FormattedReport:   "This is the first seed, establishing baseline coverage.",
			UncoveredAbstract: uncoveredAbstract,
		}, nil
	}

	// Build the formatted report for LLM
	var sb strings.Builder
	sb.WriteString("## Coverage Increase Summary\n\n")

	totalNewLines := 0
	totalNewFunctions := 0

	for _, inc := range g.lastIncreaseReport.Increases {
		totalNewLines += inc.LinesIncreased
		if inc.OldCoveredLines == 0 && inc.NewCoveredLines > 0 {
			totalNewFunctions++
		}

		sb.WriteString(fmt.Sprintf("### File: %s\n", inc.File))
		sb.WriteString(fmt.Sprintf("- Function: `%s`\n", inc.DemangledName))
		sb.WriteString(fmt.Sprintf("- New lines covered: %d (lines: %v)\n", inc.LinesIncreased, inc.IncreasedLineNumbers))
		sb.WriteString(fmt.Sprintf("- Coverage: %d/%d lines\n\n", inc.NewCoveredLines, inc.TotalLines))
	}

	summary := fmt.Sprintf("Covered %d new lines across %d functions", totalNewLines, len(g.lastIncreaseReport.Increases))
	if totalNewFunctions > 0 {
		summary += fmt.Sprintf(" (%d newly reached functions)", totalNewFunctions)
	}

	// Generate uncovered abstract from total coverage
	uncoveredAbstract := g.generateUncoveredAbstractFromTotal()

	return &CoverageIncrease{
		Summary:               summary,
		FormattedReport:       sb.String(),
		NewlyCoveredLines:     totalNewLines,
		NewlyCoveredFunctions: totalNewFunctions,
		UncoveredAbstract:     uncoveredAbstract,
	}, nil
}

// GetStats returns the current total coverage statistics.
func (g *GCCCoverage) GetStats() (*CoverageStats, error) {
	// Check if total report exists
	if _, err := os.Stat(g.totalReportPath); os.IsNotExist(err) {
		return &CoverageStats{}, nil // Return zero stats if no coverage yet
	}

	// Parse the total report
	totalReport, err := gcovr.ParseReport(g.totalReportPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse total report: %w", err)
	}

	// Apply filtering if available
	if g.filterConfig != nil {
		totalReport = gcovr.ApplyFilter(totalReport, g.filterConfig)
	}

	// Calculate coverage statistics using gcovr-json-util
	coverageReport, err := gcovr.CalculateCoverage(totalReport)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate coverage: %w", err)
	}

	return &CoverageStats{
		CoveragePercentage:    coverageReport.CoveragePercentage,
		TotalLines:            coverageReport.TotalLines,
		TotalCoveredLines:     coverageReport.TotalCoveredLines,
		TotalFunctions:        len(coverageReport.Functions),
		TotalCoveredFunctions: countCoveredFunctions(coverageReport.Functions),
	}, nil
}

// countCoveredFunctions counts functions with at least one covered line.
func countCoveredFunctions(functions []gcovr.FunctionCoverage) int {
	count := 0
	for _, f := range functions {
		if f.CoveredLines > 0 {
			count++
		}
	}
	return count
}

// addMissingFilteredFunctions adds functions from filter config that are completely missing from the report.
// These are functions with 0% coverage that were never executed - they don't appear in gcovr output at all.
// This is critical because 0% coverage functions are the most important targets for the LLM to focus on.
func (g *GCCCoverage) addMissingFilteredFunctions(report *gcovr.GcovrReport, uncoveredReport *gcovr.UncoveredReport) {
	if g.filterConfig == nil {
		return
	}

	// Build a set of files present in the report
	reportFiles := make(map[string]bool)
	for _, file := range report.Files {
		reportFiles[file.FilePath] = true
	}

	// Build a set of (file, function) pairs already in uncovered report
	existingUncovered := make(map[string]map[string]bool)
	for _, file := range uncoveredReport.Files {
		if existingUncovered[file.FilePath] == nil {
			existingUncovered[file.FilePath] = make(map[string]bool)
		}
		for _, fn := range file.UncoveredFunctions {
			existingUncovered[file.FilePath][fn.FunctionName] = true
		}
	}

	// Check each target in filter config
	for _, target := range g.filterConfig.Targets {
		// If file is completely missing from report, ALL its functions are 0% coverage
		if !reportFiles[target.File] {
			// Add all functions from this target as 0% coverage
			fileUncovered := gcovr.FileUncovered{
				FilePath:           target.File,
				UncoveredFunctions: make([]gcovr.FunctionUncovered, 0, len(target.Functions)),
			}

			for _, funcName := range target.Functions {
				fileUncovered.UncoveredFunctions = append(fileUncovered.UncoveredFunctions, gcovr.FunctionUncovered{
					FunctionName:         funcName,
					DemangledName:        funcName, // Will be updated by abstractor if possible
					UncoveredLineNumbers: nil,      // Unknown - function was never executed
					TotalLines:           0,        // Unknown
					CoveredLines:         0,        // 0% coverage
				})
			}

			uncoveredReport.Files = append(uncoveredReport.Files, fileUncovered)
		} else {
			// File exists in report, but some functions might still be missing
			// (e.g., functions that were never called)
			for _, funcName := range target.Functions {
				// Check if this function is already in the uncovered report
				if existingUncovered[target.File] != nil && existingUncovered[target.File][funcName] {
					continue // Already tracked
				}

				// Check if function exists in the report with any coverage
				funcInReport := false
				for _, file := range report.Files {
					if file.FilePath == target.File {
						for _, line := range file.Lines {
							if line.FunctionName == funcName {
								funcInReport = true
								break
							}
						}
						break
					}
				}

				// If function is not in report at all, it's 0% coverage
				if !funcInReport {
					// Find or create the file entry in uncovered report
					var fileEntry *gcovr.FileUncovered
					for i := range uncoveredReport.Files {
						if uncoveredReport.Files[i].FilePath == target.File {
							fileEntry = &uncoveredReport.Files[i]
							break
						}
					}

					if fileEntry == nil {
						uncoveredReport.Files = append(uncoveredReport.Files, gcovr.FileUncovered{
							FilePath:           target.File,
							UncoveredFunctions: make([]gcovr.FunctionUncovered, 0),
						})
						fileEntry = &uncoveredReport.Files[len(uncoveredReport.Files)-1]
					}

					fileEntry.UncoveredFunctions = append(fileEntry.UncoveredFunctions, gcovr.FunctionUncovered{
						FunctionName:         funcName,
						DemangledName:        funcName,
						UncoveredLineNumbers: nil,
						TotalLines:           0,
						CoveredLines:         0,
					})
				}
			}
		}
	}
}

// generateUncoveredAbstract generates abstracted code for uncovered paths from a report.
func (g *GCCCoverage) generateUncoveredAbstract(report Report) string {
	gcovrRep, ok := report.(*GcovrReport)
	if !ok {
		return ""
	}

	// Parse the report
	parsedReport, err := gcovr.ParseReport(gcovrRep.path)
	if err != nil {
		return ""
	}

	// Apply filtering if available
	if g.filterConfig != nil {
		parsedReport = gcovr.ApplyFilter(parsedReport, g.filterConfig)
	}

	return g.generateAbstractFromReport(parsedReport)
}

// generateUncoveredAbstractFromTotal generates abstracted code for uncovered paths from total coverage.
func (g *GCCCoverage) generateUncoveredAbstractFromTotal() string {
	// Check if total report exists
	if _, err := os.Stat(g.totalReportPath); os.IsNotExist(err) {
		return ""
	}

	// Parse the total report
	totalReport, err := gcovr.ParseReport(g.totalReportPath)
	if err != nil {
		return ""
	}

	// Apply filtering if available
	if g.filterConfig != nil {
		totalReport = gcovr.ApplyFilter(totalReport, g.filterConfig)
	}

	return g.generateAbstractFromReport(totalReport)
}

// generateAbstractFromReport generates abstracted code from a parsed gcovr report.
func (g *GCCCoverage) generateAbstractFromReport(report *gcovr.GcovrReport) string {
	// Find uncovered lines using gcovr-json-util
	uncoveredReport, err := gcovr.FindUncoveredLines(report)
	if err != nil {
		return ""
	}

	// Initialize uncovered report if nil
	if uncoveredReport == nil {
		uncoveredReport = &gcovr.UncoveredReport{
			Files: make([]gcovr.FileUncovered, 0),
		}
	}

	// IMPORTANT: Add functions from filter config that are completely missing from the report
	// These are functions with 0% coverage - never executed at all
	if g.filterConfig != nil {
		g.addMissingFilteredFunctions(report, uncoveredReport)
	}

	// If still no uncovered lines after adding missing functions, return empty
	if len(uncoveredReport.Files) == 0 {
		return ""
	}

	// Convert gcovr uncovered report to our UncoveredInput format
	input := &UncoveredInput{
		Files: make([]UncoveredFile, 0, len(uncoveredReport.Files)),
	}

	for _, file := range uncoveredReport.Files {
		// Construct full path by joining sourceParentPath with FilePath
		fullPath := file.FilePath
		if g.sourceParentPath != "" {
			fullPath = filepath.Join(g.sourceParentPath, file.FilePath)
		}

		uncoveredFile := UncoveredFile{
			FilePath:  fullPath,
			Functions: make([]UncoveredFunction, 0, len(file.UncoveredFunctions)),
		}

		for _, fn := range file.UncoveredFunctions {
			uncoveredFile.Functions = append(uncoveredFile.Functions, UncoveredFunction{
				FunctionName:   fn.FunctionName,
				DemangledName:  fn.DemangledName,
				UncoveredLines: fn.UncoveredLineNumbers,
				TotalLines:     fn.TotalLines,
				CoveredLines:   fn.CoveredLines,
			})
		}

		input.Files = append(input.Files, uncoveredFile)
	}

	// Use CppAbstractor to generate abstracted code
	abstractor := NewCppAbstractor()
	abstractedOutput, err := abstractor.Abstract(input)
	if err != nil || abstractedOutput == nil {
		return ""
	}

	// Build the formatted abstract output
	var sb strings.Builder
	for _, fn := range abstractedOutput.Functions {
		// Skip functions with errors (e.g., source file not found)
		if fn.Error != nil {
			continue
		}
		if fn.AbstractedCode == "" {
			continue
		}

		// Use short filename for display
		shortPath := fn.FilePath
		if idx := strings.LastIndex(fn.FilePath, "/"); idx != -1 {
			shortPath = fn.FilePath[idx+1:]
		}

		sb.WriteString(fmt.Sprintf("### %s::%s\n", shortPath, fn.DemangledName))
		sb.WriteString(fmt.Sprintf("Uncovered lines: %v\n\n", fn.UncoveredLines))
		sb.WriteString("```cpp\n")
		sb.WriteString(fn.AbstractedCode)
		sb.WriteString("\n```\n\n")
	}

	return sb.String()
}
