// Package coverage provides CFG-guided coverage analysis.
package coverage

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/logger"
)

// TargetInfo represents a target basic block with context for fuzzing.
type TargetInfo struct {
	// Basic block information
	Function       string // Function name containing the target
	BBID           int    // Basic block ID within the function
	SuccessorCount int    // Number of successors (branching factor)
	Lines          []int  // Source lines in this basic block
	File           string // Source file path

	// Context for prompt generation
	BaseSeed         string // ID of the closest covered seed that can reach this BB
	BaseSeedFile     string // Path to the base seed file
	BaseSeedLine     int    // Closest covered line in the base seed
	DistanceFromBase int    // Estimated distance (in BBs) from base to target
}

// CFGGuidedAnalyzer provides CFG-guided coverage analysis and targeting.
type CFGGuidedAnalyzer struct {
	cfgAnalyzer *CFGAnalyzer     // Parsed CFG
	mapping     *CoverageMapping // Line-to-seed mapping

	// Configuration
	targetFunctions []string // Functions to focus on
	sourceDir       string   // Directory containing source files
}

// NewCFGGuidedAnalyzer creates a new CFG-guided analyzer.
func NewCFGGuidedAnalyzer(cfgPath string, targetFunctions []string, sourceDir string, mappingPath string) (*CFGGuidedAnalyzer, error) {
	cfgAnalyzer := NewCFGAnalyzer(cfgPath)
	if err := cfgAnalyzer.Parse(); err != nil {
		return nil, fmt.Errorf("failed to parse CFG file: %w", err)
	}

	// Validate target functions exist
	for _, fn := range targetFunctions {
		if _, ok := cfgAnalyzer.GetFunction(fn); !ok {
			return nil, fmt.Errorf("target function %s not found in CFG", fn)
		}
	}

	// Create or load coverage mapping
	mapping, err := NewCoverageMapping(mappingPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create coverage mapping: %w", err)
	}

	return &CFGGuidedAnalyzer{
		cfgAnalyzer:     cfgAnalyzer,
		mapping:         mapping,
		targetFunctions: targetFunctions,
		sourceDir:       sourceDir,
	}, nil
}

// LoadMapping loads an existing coverage mapping from disk.
func (a *CFGGuidedAnalyzer) LoadMapping(path string) error {
	loaded, err := NewCoverageMapping(path)
	if err != nil {
		return err
	}
	a.mapping = loaded
	return nil
}

// SaveMapping saves the current coverage mapping to disk.
func (a *CFGGuidedAnalyzer) SaveMapping(path string) error {
	return a.mapping.Save(path)
}

// GetMapping returns the internal coverage mapping.
func (a *CFGGuidedAnalyzer) GetMapping() *CoverageMapping {
	return a.mapping
}

// RecordCoverage records coverage for a seed based on covered lines.
// coveredLines is a list of "File:Line" strings from gcovr output.
// If sourceDir is set, relative paths from gcovr are converted to absolute paths
// to match the paths used in CFG files.
func (a *CFGGuidedAnalyzer) RecordCoverage(seedID int64, coveredLines []string) {
	lineIDs := make([]LineID, 0, len(coveredLines))
	for _, line := range coveredLines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			var lineNum int
			fmt.Sscanf(parts[1], "%d", &lineNum)
			if lineNum > 0 {
				filePath := parts[0]
				// Convert relative path to absolute if sourceDir is set
				// gcovr outputs paths like "gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc"
				// CFG files use absolute paths like "/root/.../gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc"
				if a.sourceDir != "" && !filepath.IsAbs(filePath) {
					filePath = filepath.Join(a.sourceDir, filePath)
				}
				lineIDs = append(lineIDs, LineID{File: filePath, Line: lineNum})
			}
		}
	}
	a.mapping.RecordLines(lineIDs, seedID)
}

// GetCoveredLines returns a map of covered LineIDs for use with CFGAnalyzer.
func (a *CFGGuidedAnalyzer) GetCoveredLines() map[LineID]bool {
	return a.mapping.GetCoveredLines()
}

// SelectTarget selects the best uncovered basic block to target.
// Returns nil if all basic blocks in target functions are covered.
// Uses predecessor-based base seed selection: finds a covered predecessor BB
// and uses the seed that covered it as the base.
func (a *CFGGuidedAnalyzer) SelectTarget() *TargetInfo {
	coveredLines := a.GetCoveredLines()
	logger.Debug("[CFGGuided] Selecting target with %d covered lines", len(coveredLines))

	candidate := a.cfgAnalyzer.SelectTargetBB(a.targetFunctions, coveredLines)
	if candidate == nil {
		logger.Debug("[CFGGuided] No uncovered BBs found - all covered!")
		return nil
	}

	logger.Debug("[CFGGuided] Selected candidate: %s:BB%d (weight=%.2f, succs=%d, preds=%v)",
		candidate.Function, candidate.BBID, candidate.Weight, candidate.SuccessorCount, candidate.Predecessors)

	info := &TargetInfo{
		Function:       candidate.Function,
		BBID:           candidate.BBID,
		SuccessorCount: candidate.SuccessorCount,
		Lines:          candidate.Lines,
		File:           candidate.File,
	}

	// Try to find a base seed from a covered predecessor BB
	baseSeedID, baseLine, found := a.findCoveredPredecessorSeed(candidate)

	if found {
		info.BaseSeed = fmt.Sprintf("%d", baseSeedID)
		info.BaseSeedLine = baseLine.Line
		info.DistanceFromBase = 1 // Direct predecessor
		logger.Debug("[CFGGuided] Found predecessor-based base seed: %d (line %d)", baseSeedID, baseLine.Line)
	} else {
		// Fallback: use closest covered line approach
		targetLine := candidate.Lines[0]
		baseLine, baseSeedID, found := a.mapping.FindClosestCoveredLine(candidate.File, targetLine)
		if found {
			info.BaseSeed = fmt.Sprintf("%d", baseSeedID)
			info.BaseSeedLine = baseLine.Line
			if baseLine.Line > 0 && targetLine > 0 {
				info.DistanceFromBase = abs(targetLine - baseLine.Line)
			}
			logger.Debug("[CFGGuided] Using fallback closest-line base seed: %d (line %d, distance=%d)",
				baseSeedID, baseLine.Line, info.DistanceFromBase)
		} else {
			logger.Debug("[CFGGuided] No base seed found")
		}
	}

	return info
}

// findCoveredPredecessorSeed finds a seed that covered a predecessor of the given BB.
// Returns the seed ID, the covered line in the predecessor, and whether a seed was found.
func (a *CFGGuidedAnalyzer) findCoveredPredecessorSeed(candidate *BBCandidate) (int64, LineID, bool) {
	coveredLines := a.GetCoveredLines()

	// Get covered predecessors
	coveredPreds := a.cfgAnalyzer.GetCoveredPredecessors(
		candidate.Function, candidate.BBID, coveredLines,
	)

	if len(coveredPreds) == 0 {
		return 0, LineID{}, false
	}

	// Get the function to access predecessor blocks
	fn, ok := a.cfgAnalyzer.GetFunction(candidate.Function)
	if !ok {
		return 0, LineID{}, false
	}

	// Find a seed that covered any line in any covered predecessor
	for _, predID := range coveredPreds {
		predBB, ok := fn.Blocks[predID]
		if !ok {
			continue
		}

		// Look for a covered line in this predecessor and its seed
		for _, lineNum := range predBB.Lines {
			lid := LineID{File: predBB.File, Line: lineNum}
			if coveredLines[lid] {
				seedID, found := a.mapping.GetSeedForLine(lid)
				if found {
					return seedID, lid, true
				}
			}
		}
	}

	return 0, LineID{}, false
}

// GetFunctionCoverage returns BB coverage statistics for target functions.
// GetFunctionCoverage returns BB coverage statistics for target functions.
// Note: This returns BB counts, not line counts. Use GetFunctionLineCoverage for lines.
func (a *CFGGuidedAnalyzer) GetFunctionCoverage() map[string]struct{ Covered, Total int } {
	coveredLines := a.GetCoveredLines()
	result := make(map[string]struct{ Covered, Total int })

	for _, funcName := range a.targetFunctions {
		covered, total := a.cfgAnalyzer.GetFunctionCoverage(funcName, coveredLines)
		result[funcName] = struct{ Covered, Total int }{covered, total}
	}

	return result
}

// GetFunctionLineCoverage returns line coverage statistics for target functions.
func (a *CFGGuidedAnalyzer) GetFunctionLineCoverage() map[string]struct{ Covered, Total int } {
	coveredLines := a.GetCoveredLines()
	result := make(map[string]struct{ Covered, Total int })

	for _, funcName := range a.targetFunctions {
		covered, total := a.cfgAnalyzer.GetFunctionLineCoverage(funcName, coveredLines)
		result[funcName] = struct{ Covered, Total int }{covered, total}
	}

	return result
}

// GetTotalTargetLines returns the total number of unique source lines across all target functions.
func (a *CFGGuidedAnalyzer) GetTotalTargetLines() int {
	total := 0
	for _, funcName := range a.targetFunctions {
		total += a.cfgAnalyzer.GetFunctionTotalLines(funcName)
	}
	return total
}

// GetTotalCoveredTargetLines returns the total number of covered lines across all target functions.
func (a *CFGGuidedAnalyzer) GetTotalCoveredTargetLines() int {
	coveredLines := a.GetCoveredLines()
	total := 0
	for _, funcName := range a.targetFunctions {
		covered, _ := a.cfgAnalyzer.GetFunctionLineCoverage(funcName, coveredLines)
		total += covered
	}
	return total
}

// GetAllUncoveredBBs returns all uncovered BBs across target functions, sorted by priority.
func (a *CFGGuidedAnalyzer) GetAllUncoveredBBs() []BBCandidate {
	coveredLines := a.GetCoveredLines()
	var allUncovered []BBCandidate

	for _, funcName := range a.targetFunctions {
		uncovered := a.cfgAnalyzer.GetUncoveredBBsInFunction(funcName, coveredLines)
		allUncovered = append(allUncovered, uncovered...)
	}

	// Sort by weight descending (includes decay factor)
	sort.Slice(allUncovered, func(i, j int) bool {
		if allUncovered[i].Weight != allUncovered[j].Weight {
			return allUncovered[i].Weight > allUncovered[j].Weight
		}
		return allUncovered[i].BBID < allUncovered[j].BBID
	})

	return allUncovered
}

// GenerateTargetPrompt generates a prompt describing the target for LLM.
func (a *CFGGuidedAnalyzer) GenerateTargetPrompt(target *TargetInfo) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("## Target: %s, Basic Block %d\n\n", target.Function, target.BBID))
	sb.WriteString(fmt.Sprintf("- **Branching factor**: %d successors\n", target.SuccessorCount))
	sb.WriteString(fmt.Sprintf("- **Source lines**: %v\n", target.Lines))
	sb.WriteString(fmt.Sprintf("- **File**: %s\n\n", filepath.Base(target.File)))

	if target.BaseSeed != "" {
		sb.WriteString(fmt.Sprintf("### Starting Point\n"))
		sb.WriteString(fmt.Sprintf("- **Base seed**: %s\n", target.BaseSeed))
		sb.WriteString(fmt.Sprintf("- **Closest covered line**: %d\n", target.BaseSeedLine))
		sb.WriteString(fmt.Sprintf("- **Estimated distance**: %d lines\n\n", target.DistanceFromBase))
	}

	// Read source code around target lines if possible
	if target.File != "" && len(target.Lines) > 0 {
		sourcePath := target.File
		if a.sourceDir != "" && !filepath.IsAbs(sourcePath) {
			sourcePath = filepath.Join(a.sourceDir, sourcePath)
		}

		sourceSnippet := a.readSourceLines(sourcePath, target.Lines)
		if sourceSnippet != "" {
			sb.WriteString("### Target Code\n")
			sb.WriteString("```cpp\n")
			sb.WriteString(sourceSnippet)
			sb.WriteString("\n```\n\n")
		}
	}

	return sb.String()
}

// readSourceLines reads the source code around the specified lines.
func (a *CFGGuidedAnalyzer) readSourceLines(filePath string, lines []int) string {
	if len(lines) == 0 {
		return ""
	}

	// Find min and max lines with context
	minLine := lines[0]
	maxLine := lines[0]
	for _, l := range lines {
		if l < minLine {
			minLine = l
		}
		if l > maxLine {
			maxLine = l
		}
	}

	// Add context (5 lines before and after)
	startLine := minLine - 5
	if startLine < 1 {
		startLine = 1
	}
	endLine := maxLine + 5

	// Read the file
	content, err := ReadSourceLines(filePath, startLine, endLine)
	if err != nil {
		return ""
	}

	return content
}

// ReadSourceLines reads a range of lines from a source file.
func ReadSourceLines(filePath string, startLine, endLine int) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		if lineNum >= startLine && lineNum <= endLine {
			lines = append(lines, fmt.Sprintf("%4d: %s", lineNum, scanner.Text()))
		}
		if lineNum > endLine {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return strings.Join(lines, "\n"), nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// RecordAttempt records a fuzz attempt for a target basic block.
// After 64 failed attempts, the BB's weight is reduced by 10%.
func (a *CFGGuidedAnalyzer) RecordAttempt(target *TargetInfo) {
	a.cfgAnalyzer.RecordAttempt(target.Function, target.BBID)
}

// RecordSuccess records a successful coverage of a target basic block.
func (a *CFGGuidedAnalyzer) RecordSuccess(target *TargetInfo) {
	a.cfgAnalyzer.RecordSuccess(target.Function, target.BBID)
}

// GetBBWeight returns the current weight for a target.
func (a *CFGGuidedAnalyzer) GetBBWeight(target *TargetInfo) float64 {
	return a.cfgAnalyzer.GetBBWeight(target.Function, target.BBID)
}

// GetBBAttempts returns the current attempt count for a target.
func (a *CFGGuidedAnalyzer) GetBBAttempts(target *TargetInfo) int {
	return a.cfgAnalyzer.GetBBAttempts(target.Function, target.BBID)
}

// GetCFGAnalyzer returns the underlying CFGAnalyzer.
func (a *CFGGuidedAnalyzer) GetCFGAnalyzer() *CFGAnalyzer {
	return a.cfgAnalyzer
}
