// Package coverage provides CFG analysis for GCC-generated control flow graph files.
package coverage

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/logger"
)

// BasicBlock represents a basic block in the control flow graph.
type BasicBlock struct {
	ID           int    // Basic block number (e.g., 2, 3, 4...)
	Function     string // Function name this BB belongs to
	File         string // Source file path
	Lines        []int  // Source line numbers covered by this BB
	Successors   []int  // Successor basic block IDs
	Predecessors []int  // Predecessor basic block IDs (computed from successors)
}

// CFGFunction represents a function in the CFG with its basic blocks.
type CFGFunction struct {
	Name        string              // Function name
	MangledName string              // Mangled name for C++ functions
	Blocks      map[int]*BasicBlock // Map of BB ID to BasicBlock
	SuccsMap    map[int][]int       // Map of BB ID to successors (from summary section)
	PredsMap    map[int][]int       // Map of BB ID to predecessors (computed)
}

// BBWeightInfo tracks attempts and weight for a basic block.
type BBWeightInfo struct {
	Attempts int     // Number of fuzz attempts
	Weight   float64 // Current weight (starts as successor count, decays after failures)
}

// CFGAnalyzer parses and analyzes GCC CFG dump files.
type CFGAnalyzer struct {
	cfgPath       string                   // Path to the .cfg file
	functions     map[string]*CFGFunction  // Parsed functions by name
	lineToBB      map[LineID][]int         // Map of File:Line -> list of BB IDs
	bbToSuccCount map[string]int           // Map of "FuncName:BBID" -> successor count
	bbWeights     map[string]*BBWeightInfo // Map of "FuncName:BBID" -> weight info
}

// NewCFGAnalyzer creates a new CFG analyzer for the given file.
func NewCFGAnalyzer(cfgPath string) *CFGAnalyzer {
	return &CFGAnalyzer{
		cfgPath:       cfgPath,
		functions:     make(map[string]*CFGFunction),
		lineToBB:      make(map[LineID][]int),
		bbToSuccCount: make(map[string]int), bbWeights: make(map[string]*BBWeightInfo)}
}

// Regular expressions for parsing CFG
var (
	// Matches function header: ;; Function name (mangled, funcdef_no=N, ...)
	reFunctionHeader = regexp.MustCompile(`^;; Function (\w+) \(([^,]+),`)

	// Matches successor summary: ;; N succs { M1 M2 ... }
	reSuccSummary = regexp.MustCompile(`^;; (\d+) succs \{ ([^}]*) \}`)

	// Matches basic block start: <bb N> : or <bb N>:
	reBBStart = regexp.MustCompile(`^\s*<bb (\d+)>\s*:?`)

	// Matches line info: [/path/to/file.cc:LINE:COL] or [/path/to/file.cc:LINE:COL discrim N]
	reLineInfo = regexp.MustCompile(`\[([^:\]]+):(\d+):\d+(?:\s+discrim\s+\d+)?\]`)
)

// Parse parses the CFG file and builds internal data structures.
func (c *CFGAnalyzer) Parse() error {
	file, err := os.Open(c.cfgPath)
	if err != nil {
		return fmt.Errorf("failed to open CFG file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var currentFunc *CFGFunction
	var currentBB *BasicBlock
	parsingFunctionBody := false

	for scanner.Scan() {
		line := scanner.Text()

		// Check for function header
		if matches := reFunctionHeader.FindStringSubmatch(line); matches != nil {
			// Save current function if exists
			if currentFunc != nil {
				c.functions[currentFunc.Name] = currentFunc
				c.indexFunction(currentFunc)
			}

			currentFunc = &CFGFunction{
				Name:        matches[1],
				MangledName: matches[2],
				Blocks:      make(map[int]*BasicBlock),
				SuccsMap:    make(map[int][]int),
			}
			currentBB = nil
			parsingFunctionBody = false
			continue
		}

		if currentFunc == nil {
			continue
		}

		// Parse successor summary (appears before function body)
		if matches := reSuccSummary.FindStringSubmatch(line); matches != nil {
			bbID, _ := strconv.Atoi(matches[1])
			succStr := strings.TrimSpace(matches[2])
			var succs []int
			if succStr != "" {
				for _, s := range strings.Fields(succStr) {
					if id, err := strconv.Atoi(s); err == nil {
						succs = append(succs, id)
					}
				}
			}
			currentFunc.SuccsMap[bbID] = succs
			continue
		}

		// Detect start of function body (signature line)
		if !parsingFunctionBody && strings.Contains(line, currentFunc.Name) &&
			strings.Contains(line, "(") && !strings.HasPrefix(line, ";;") {
			parsingFunctionBody = true
			continue
		}

		if !parsingFunctionBody {
			continue
		}

		// Check for basic block start
		if matches := reBBStart.FindStringSubmatch(line); matches != nil {
			bbID, _ := strconv.Atoi(matches[1])

			// Create new basic block
			currentBB = &BasicBlock{
				ID:       bbID,
				Function: currentFunc.Name,
				Lines:    []int{},
			}

			// Get successors from the pre-parsed summary
			if succs, ok := currentFunc.SuccsMap[bbID]; ok {
				currentBB.Successors = succs
			}

			currentFunc.Blocks[bbID] = currentBB
			continue
		}

		// Parse line info within current basic block
		if currentBB != nil {
			matches := reLineInfo.FindAllStringSubmatch(line, -1)
			for _, m := range matches {
				filePath := m[1]
				lineNum, _ := strconv.Atoi(m[2])

				// Set file if not set
				if currentBB.File == "" {
					currentBB.File = filePath
				}

				// Add line if not already present
				found := false
				for _, l := range currentBB.Lines {
					if l == lineNum {
						found = true
						break
					}
				}
				if !found {
					currentBB.Lines = append(currentBB.Lines, lineNum)
				}
			}
		}

		// Check for function end (closing brace at start of line)
		if line == "}" {
			if currentBB != nil {
				currentBB = nil
			}
		}
	}

	// Save last function
	if currentFunc != nil {
		c.functions[currentFunc.Name] = currentFunc
		c.indexFunction(currentFunc)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading CFG file: %w", err)
	}

	// Build predecessor maps for all functions
	c.buildPredecessorMaps()

	return nil
}

// buildPredecessorMaps computes predecessor relationships from successor maps.
func (c *CFGAnalyzer) buildPredecessorMaps() {
	for _, fn := range c.functions {
		fn.PredsMap = make(map[int][]int)

		// Initialize all blocks in PredsMap
		for bbID := range fn.Blocks {
			fn.PredsMap[bbID] = []int{}
		}

		// Build predecessors by reversing successor edges
		for bbID, succs := range fn.SuccsMap {
			for _, succID := range succs {
				fn.PredsMap[succID] = append(fn.PredsMap[succID], bbID)
			}
		}

		// Also set Predecessors field in BasicBlock structs
		for bbID, bb := range fn.Blocks {
			bb.Predecessors = fn.PredsMap[bbID]
		}
	}
}

// indexFunction builds the line-to-BB and BB-to-succ-count indices for a function.
func (c *CFGAnalyzer) indexFunction(fn *CFGFunction) {
	for bbID, bb := range fn.Blocks {
		// Index line -> BB mapping
		for _, lineNum := range bb.Lines {
			lid := LineID{File: bb.File, Line: lineNum}
			c.lineToBB[lid] = append(c.lineToBB[lid], bbID)
		}

		// Index BB -> successor count and initialize weight
		key := fmt.Sprintf("%s:%d", fn.Name, bbID)
		c.bbToSuccCount[key] = len(bb.Successors)

		// Initialize weight info with successor count as base weight
		c.bbWeights[key] = &BBWeightInfo{
			Attempts: 0,
			Weight:   float64(len(bb.Successors)),
		}
	}
}

// GetFunction returns a parsed function by name.
func (c *CFGAnalyzer) GetFunction(name string) (*CFGFunction, bool) {
	fn, ok := c.functions[name]
	return fn, ok
}

// GetAllFunctions returns all parsed function names.
func (c *CFGAnalyzer) GetAllFunctions() []string {
	names := make([]string, 0, len(c.functions))
	for name := range c.functions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetBasicBlocksForLine returns the basic block IDs that cover a given source line.
func (c *CFGAnalyzer) GetBasicBlocksForLine(file string, line int) []int {
	lid := LineID{File: file, Line: line}
	return c.lineToBB[lid]
}

// GetSuccessorCount returns the number of successors for a basic block.
func (c *CFGAnalyzer) GetSuccessorCount(funcName string, bbID int) int {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	return c.bbToSuccCount[key]
}

// BBCandidate represents a candidate basic block for targeting.
type BBCandidate struct {
	Function       string
	BBID           int
	SuccessorCount int
	Lines          []int
	File           string
	Weight         float64 // Current weight with decay applied
	Predecessors   []int   // Predecessor BB IDs
}

// SelectTargetBB selects the best uncovered basic block to target.
// It prioritizes basic blocks with higher weight (successor count * decay factor).
// The coveredLines set contains lines that are already covered.
func (c *CFGAnalyzer) SelectTargetBB(targetFunctions []string, coveredLines map[LineID]bool) *BBCandidate {
	var candidates []BBCandidate

	for _, funcName := range targetFunctions {
		fn, ok := c.functions[funcName]
		if !ok {
			continue
		}

		for bbID, bb := range fn.Blocks {
			// Skip entry (0) and exit (1) blocks
			if bbID <= 1 {
				continue
			}

			// Check if this BB has any uncovered lines
			hasUncoveredLine := false
			for _, lineNum := range bb.Lines {
				lid := LineID{File: bb.File, Line: lineNum}
				if !coveredLines[lid] {
					hasUncoveredLine = true
					break
				}
			}

			if hasUncoveredLine && len(bb.Lines) > 0 {
				// Get current weight with decay
				key := fmt.Sprintf("%s:%d", funcName, bbID)
				weight := float64(len(bb.Successors))
				if wi, ok := c.bbWeights[key]; ok {
					weight = wi.Weight
				}

				candidates = append(candidates, BBCandidate{
					Function:       funcName,
					BBID:           bbID,
					SuccessorCount: len(bb.Successors),
					Lines:          bb.Lines,
					File:           bb.File,
					Weight:         weight,
					Predecessors:   bb.Predecessors,
				})
			}
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort candidates: higher weight first, then lower BB ID for determinism
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Weight != candidates[j].Weight {
			return candidates[i].Weight > candidates[j].Weight
		}
		return candidates[i].BBID < candidates[j].BBID
	})

	return &candidates[0]
}

// GetUncoveredBBsInFunction returns all uncovered basic blocks in a function.
func (c *CFGAnalyzer) GetUncoveredBBsInFunction(funcName string, coveredLines map[LineID]bool) []BBCandidate {
	var results []BBCandidate

	fn, ok := c.functions[funcName]
	if !ok {
		return results
	}

	for bbID, bb := range fn.Blocks {
		// Skip entry (0) and exit (1) blocks
		if bbID <= 1 {
			continue
		}

		hasUncoveredLine := false
		for _, lineNum := range bb.Lines {
			lid := LineID{File: bb.File, Line: lineNum}
			if !coveredLines[lid] {
				hasUncoveredLine = true
				break
			}
		}

		if hasUncoveredLine && len(bb.Lines) > 0 {
			// Get current weight with decay
			key := fmt.Sprintf("%s:%d", funcName, bbID)
			weight := float64(len(bb.Successors))
			if wi, ok := c.bbWeights[key]; ok {
				weight = wi.Weight
			}

			results = append(results, BBCandidate{
				Function:       funcName,
				BBID:           bbID,
				SuccessorCount: len(bb.Successors),
				Lines:          bb.Lines,
				File:           bb.File,
				Weight:         weight,
				Predecessors:   bb.Predecessors,
			})
		}
	}

	// Sort by weight descending (instead of just successor count)
	sort.Slice(results, func(i, j int) bool {
		if results[i].Weight != results[j].Weight {
			return results[i].Weight > results[j].Weight
		}
		return results[i].BBID < results[j].BBID
	})

	return results
}

// GetFunctionCoverage calculates BB coverage statistics for a function.
// Returns (covered BB count, total BB count) - not line counts.
func (c *CFGAnalyzer) GetFunctionCoverage(funcName string, coveredLines map[LineID]bool) (covered, total int) {
	fn, ok := c.functions[funcName]
	if !ok {
		return 0, 0
	}

	coveredBBs := make(map[int]bool)
	for bbID, bb := range fn.Blocks {
		if bbID <= 1 {
			continue // Skip entry/exit
		}
		total++

		// A BB is covered if any of its lines are covered
		for _, lineNum := range bb.Lines {
			lid := LineID{File: bb.File, Line: lineNum}
			if coveredLines[lid] {
				coveredBBs[bbID] = true
				break
			}
		}
	}

	return len(coveredBBs), total
}

// GetFunctionLineCoverage calculates line coverage statistics for a function.
// Returns (covered line count, total unique line count).
func (c *CFGAnalyzer) GetFunctionLineCoverage(funcName string, coveredLines map[LineID]bool) (covered, total int) {
	fn, ok := c.functions[funcName]
	if !ok {
		return 0, 0
	}

	// Collect all unique lines in this function
	allLines := make(map[LineID]bool)
	for bbID, bb := range fn.Blocks {
		if bbID <= 1 {
			continue // Skip entry/exit
		}
		for _, lineNum := range bb.Lines {
			lid := LineID{File: bb.File, Line: lineNum}
			allLines[lid] = true
		}
	}

	// Count covered lines
	coveredCount := 0
	for lid := range allLines {
		if coveredLines[lid] {
			coveredCount++
		}
	}

	return coveredCount, len(allLines)
}

// GetFunctionTotalLines returns the total number of unique source lines in a function.
func (c *CFGAnalyzer) GetFunctionTotalLines(funcName string) int {
	fn, ok := c.functions[funcName]
	if !ok {
		return 0
	}

	// Collect all unique lines
	allLines := make(map[LineID]bool)
	for bbID, bb := range fn.Blocks {
		if bbID <= 1 {
			continue // Skip entry/exit
		}
		for _, lineNum := range bb.Lines {
			lid := LineID{File: bb.File, Line: lineNum}
			allLines[lid] = true
		}
	}

	return len(allLines)
}

// GetSourceFile extracts the source file path from the CFG file path.
// CFG files are named like "cfgexpand.cc.015t.cfg", source is "cfgexpand.cc".
func GetSourceFile(cfgPath string) string {
	base := filepath.Base(cfgPath)
	// Remove .XXXt.cfg suffix
	parts := strings.Split(base, ".")
	if len(parts) >= 3 {
		// Reconstruct source filename (e.g., "cfgexpand.cc" from "cfgexpand.cc.015t.cfg")
		return strings.Join(parts[:len(parts)-2], ".")
	}
	return base
}

// FindCFGFiles finds all CFG files matching the pattern in a directory.
func FindCFGFiles(buildDir string, sourceFile string) ([]string, error) {
	pattern := filepath.Join(buildDir, "gcc", sourceFile+"*.cfg")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// PrintFunctionSummary prints a summary of a parsed function for debugging.
func (c *CFGAnalyzer) PrintFunctionSummary(funcName string) {
	fn, ok := c.functions[funcName]
	if !ok {
		logger.Debug("Function %s not found", funcName)
		return
	}

	logger.Debug("Function: %s (mangled: %s)", fn.Name, fn.MangledName)
	logger.Debug("  Basic blocks: %d", len(fn.Blocks))

	// Sort BBs by ID for consistent output
	bbIDs := make([]int, 0, len(fn.Blocks))
	for id := range fn.Blocks {
		bbIDs = append(bbIDs, id)
	}
	sort.Ints(bbIDs)

	for _, bbID := range bbIDs {
		bb := fn.Blocks[bbID]
		info := fmt.Sprintf("  BB %d: %d successors, %d lines", bbID, len(bb.Successors), len(bb.Lines))
		if len(bb.Successors) > 0 {
			info += fmt.Sprintf(", succs: %v", bb.Successors)
		}
		if len(bb.Lines) > 0 {
			info += fmt.Sprintf(", lines: %v", bb.Lines)
		}
		logger.Debug("%s", info)
	}
}

// WeightDecayThreshold is the number of failed attempts before weight decay is applied.
const WeightDecayThreshold = 64

// WeightDecayFactor is the multiplier applied to weight after each decay (10% reduction).
const WeightDecayFactor = 0.9

// RecordAttempt records a fuzz attempt for a basic block.
// After every WeightDecayThreshold failed attempts, the weight is reduced by WeightDecayFactor.
func (c *CFGAnalyzer) RecordAttempt(funcName string, bbID int) {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	wi, ok := c.bbWeights[key]
	if !ok {
		// Initialize if not present
		succCount := c.bbToSuccCount[key]
		wi = &BBWeightInfo{
			Attempts: 0,
			Weight:   float64(succCount),
		}
		c.bbWeights[key] = wi
	}

	wi.Attempts++

	// Apply decay every WeightDecayThreshold attempts
	if wi.Attempts%WeightDecayThreshold == 0 {
		wi.Weight *= WeightDecayFactor
		logger.Debug("Weight decay for %s: attempts=%d, new weight=%.2f", key, wi.Attempts, wi.Weight)
	}
}

// RecordSuccess records a successful coverage of a basic block.
// This resets the attempt counter for the BB since it's now covered.
func (c *CFGAnalyzer) RecordSuccess(funcName string, bbID int) {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	if wi, ok := c.bbWeights[key]; ok {
		logger.Debug("BB %s successfully covered after %d attempts", key, wi.Attempts)
		// Reset for potential future re-targeting
		wi.Attempts = 0
	}
}

// GetBBWeight returns the current weight for a basic block.
func (c *CFGAnalyzer) GetBBWeight(funcName string, bbID int) float64 {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	if wi, ok := c.bbWeights[key]; ok {
		return wi.Weight
	}
	return float64(c.bbToSuccCount[key])
}

// GetBBAttempts returns the current attempt count for a basic block.
func (c *CFGAnalyzer) GetBBAttempts(funcName string, bbID int) int {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	if wi, ok := c.bbWeights[key]; ok {
		return wi.Attempts
	}
	return 0
}

// GetCoveredPredecessors returns the list of covered predecessor BB IDs for a given BB.
func (c *CFGAnalyzer) GetCoveredPredecessors(funcName string, bbID int, coveredLines map[LineID]bool) []int {
	fn, ok := c.functions[funcName]
	if !ok {
		return nil
	}

	bb, ok := fn.Blocks[bbID]
	if !ok {
		return nil
	}

	var coveredPreds []int
	for _, predID := range bb.Predecessors {
		predBB, ok := fn.Blocks[predID]
		if !ok {
			continue
		}

		// Check if any line in predecessor is covered
		for _, lineNum := range predBB.Lines {
			lid := LineID{File: predBB.File, Line: lineNum}
			if coveredLines[lid] {
				coveredPreds = append(coveredPreds, predID)
				break
			}
		}
	}

	return coveredPreds
}
