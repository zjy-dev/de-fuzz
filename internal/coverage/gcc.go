package coverage

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"defuzz/internal/vm"
)

// FunctionCoverage represents coverage info for a single function
type FunctionCoverage struct {
	Name       string
	LinesCov   int // Lines covered
	LinesTotal int // Total lines
}

// GCCCoverage implements the Coverage interface using GCC's gcov/lcov toolchain.
type GCCCoverage struct {
	vm              vm.VM
	buildDir        string   // Build directory where GCC was compiled with coverage flags
	srcDir          string   // GCC source directory
	mergedInfoPath  string   // Path to merged.info file
	targetFiles     []string // Specific files to track (e.g., "*/gcc/config/i386/i386.c")
	targetFunctions []string // Specific functions to track

	// Internal state
	totalCoverage map[string]*FunctionCoverage // Function name -> coverage info
}

// NewGCCCoverage creates a new GCC coverage tracker.
// targetFiles: patterns for files to track (e.g., "*/gcc/config/i386/*.c")
// targetFunctions: list of specific function names to track
func NewGCCCoverage(
	v vm.VM,
	buildDir string,
	srcDir string,
	targetFiles []string,
	targetFunctions []string,
) *GCCCoverage {
	mergedInfoPath := filepath.Join(buildDir, "merged.info")

	return &GCCCoverage{
		vm:              v,
		buildDir:        buildDir,
		srcDir:          srcDir,
		mergedInfoPath:  mergedInfoPath,
		targetFiles:     targetFiles,
		targetFunctions: targetFunctions,
		totalCoverage:   make(map[string]*FunctionCoverage),
	}
}

// Measure compiles the seed and captures the new coverage information.
// The seedPath should point to the seed directory containing source.c and Makefile.
func (g *GCCCoverage) Measure(seedPath string) ([]byte, error) {
	// Step 1: Clean previous .gcda files to get fresh coverage
	cleanCmd := fmt.Sprintf("find %s -name '*.gcda' -delete", g.buildDir)
	_, err := g.vm.Run("", cleanCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to clean .gcda files: %w", err)
	}

	// Step 2: Compile the seed using the instrumented GCC
	// The seed's Makefile should use the instrumented compiler from buildDir
	makeCmd := fmt.Sprintf("cd %s && make", seedPath)
	_, err = g.vm.Run("", makeCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to compile seed: %w", err)
	}

	// Step 3: Capture coverage with lcov
	newInfoPath := filepath.Join(g.buildDir, "new.info")
	captureCmd := fmt.Sprintf("cd %s && lcov --capture --directory . --output-file %s", g.buildDir, newInfoPath)
	_, err = g.vm.Run("", captureCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to capture coverage: %w", err)
	}

	// Step 4: Extract only the target files
	extractedInfoPath := filepath.Join(g.buildDir, "extracted.info")
	extractArgs := []string{"--extract", newInfoPath}
	extractArgs = append(extractArgs, g.targetFiles...)
	extractArgs = append(extractArgs, "-o", extractedInfoPath)
	extractCmd := fmt.Sprintf("cd %s && lcov %s", g.buildDir, strings.Join(extractArgs, " "))
	_, err = g.vm.Run("", extractCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to extract target files: %w", err)
	}

	// Step 5: Extract function-level coverage and filter by target functions
	filteredInfo, err := g.extractFunctions(extractedInfoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract functions: %w", err)
	}

	return filteredInfo, nil
}

// extractFunctions parses the lcov info file and extracts coverage for target functions.
// Returns a serialized representation of function coverage.
func (g *GCCCoverage) extractFunctions(infoPath string) ([]byte, error) {
	// Read the info file from VM
	catCmd := fmt.Sprintf("cat %s", infoPath)
	result, err := g.vm.Run("", catCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to read info file: %w", err)
	}

	// Parse the lcov info format
	// Format example:
	// SF:/path/to/source.c
	// FN:10,function_name
	// FNDA:5,function_name
	// FNF:1
	// FNH:1
	// DA:10,5
	// LH:1
	// LF:1
	// end_of_record

	functionCoverage := make(map[string]*FunctionCoverage)
	scanner := bufio.NewScanner(strings.NewReader(result.Stdout))

	var currentFunc string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "FN:") {
			// FN:line,function_name
			parts := strings.SplitN(strings.TrimPrefix(line, "FN:"), ",", 2)
			if len(parts) == 2 {
				currentFunc = parts[1]
				// Check if this function is in our target list
				if len(g.targetFunctions) == 0 || contains(g.targetFunctions, currentFunc) {
					if _, ok := functionCoverage[currentFunc]; !ok {
						functionCoverage[currentFunc] = &FunctionCoverage{
							Name: currentFunc,
						}
					}
				}
			}
		}
	}

	// Calculate coverage for each function
	// This is a simplified approach - in reality, you'd need to map lines to functions
	// For now, we'll use a heuristic based on FNDA records
	scanner = bufio.NewScanner(strings.NewReader(result.Stdout))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "FNDA:") {
			// FNDA:hit_count,function_name
			parts := strings.SplitN(strings.TrimPrefix(line, "FNDA:"), ",", 2)
			if len(parts) == 2 {
				funcName := parts[1]
				hitCount, _ := strconv.Atoi(parts[0])

				if fc, ok := functionCoverage[funcName]; ok {
					fc.LinesCov = hitCount
					fc.LinesTotal = hitCount // Simplified - would need actual line count
				}
			}
		} else if strings.HasPrefix(line, "LF:") {
			// Total lines in function
			count, _ := strconv.Atoi(strings.TrimPrefix(line, "LF:"))
			if currentFunc != "" {
				if fc, ok := functionCoverage[currentFunc]; ok {
					fc.LinesTotal = count
				}
			}
		} else if strings.HasPrefix(line, "LH:") {
			// Lines hit in function
			count, _ := strconv.Atoi(strings.TrimPrefix(line, "LH:"))
			if currentFunc != "" {
				if fc, ok := functionCoverage[currentFunc]; ok {
					fc.LinesCov = count
				}
			}
		}
	}

	// Serialize the coverage data
	var buf bytes.Buffer
	for funcName, fc := range functionCoverage {
		buf.WriteString(fmt.Sprintf("%s:%d/%d\n", funcName, fc.LinesCov, fc.LinesTotal))
	}

	return buf.Bytes(), nil
}

// HasIncreased checks if the new coverage has increased compared to the total.
func (g *GCCCoverage) HasIncreased(newCoverageInfo []byte) (bool, error) {
	newCov, err := parseCoverageData(newCoverageInfo)
	if err != nil {
		return false, err
	}

	// Check if any function has increased coverage
	for funcName, newFC := range newCov {
		if oldFC, exists := g.totalCoverage[funcName]; exists {
			// If this function has more lines covered, it's an increase
			if newFC.LinesCov > oldFC.LinesCov {
				return true, nil
			}
		} else {
			// New function covered
			return true, nil
		}
	}

	return false, nil
}

// Merge merges the new coverage information into the total coverage.
func (g *GCCCoverage) Merge(newCoverageInfo []byte) error {
	newCov, err := parseCoverageData(newCoverageInfo)
	if err != nil {
		return err
	}

	// Merge: take the maximum coverage for each function
	for funcName, newFC := range newCov {
		if oldFC, exists := g.totalCoverage[funcName]; exists {
			if newFC.LinesCov > oldFC.LinesCov {
				g.totalCoverage[funcName] = newFC
			}
		} else {
			g.totalCoverage[funcName] = newFC
		}
	}

	// Also merge the .info files on disk using lcov
	if _, err := os.Stat(g.mergedInfoPath); os.IsNotExist(err) {
		// First time, just save new coverage as merged
		return g.saveNewCoverage(newCoverageInfo)
	}

	// Merge using lcov -a command
	newInfoPath := filepath.Join(g.buildDir, "new.info")
	mergeCmd := fmt.Sprintf("cd %s && lcov -a %s -a %s -o %s",
		g.buildDir, g.mergedInfoPath, newInfoPath, g.mergedInfoPath)
	_, err = g.vm.Run("", mergeCmd)
	if err != nil {
		return fmt.Errorf("failed to merge coverage: %w", err)
	}

	return nil
}

// saveNewCoverage saves new coverage as the initial merged coverage.
func (g *GCCCoverage) saveNewCoverage(newCoverageInfo []byte) error {
	newInfoPath := filepath.Join(g.buildDir, "new.info")
	cpCmd := fmt.Sprintf("cp %s %s", newInfoPath, g.mergedInfoPath)
	_, err := g.vm.Run("", cpCmd)
	return err
}

// parseCoverageData parses the serialized coverage data.
func parseCoverageData(data []byte) (map[string]*FunctionCoverage, error) {
	result := make(map[string]*FunctionCoverage)
	scanner := bufio.NewScanner(bytes.NewReader(data))

	// Format: function_name:covered/total
	re := regexp.MustCompile(`^(.+):(\d+)/(\d+)$`)

	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)
		if len(matches) != 4 {
			continue
		}

		funcName := matches[1]
		linesCov, _ := strconv.Atoi(matches[2])
		linesTotal, _ := strconv.Atoi(matches[3])

		result[funcName] = &FunctionCoverage{
			Name:       funcName,
			LinesCov:   linesCov,
			LinesTotal: linesTotal,
		}
	}

	return result, scanner.Err()
}

// contains checks if a string slice contains a specific string.
func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}
