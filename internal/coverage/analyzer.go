// Package coverage provides coverage analysis for compiler fuzzing.
package coverage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/zjy-dev/de-fuzz/internal/logger"
)

// randIntn returns a random int in [0, n). Thread-safe wrapper for rand.Intn.
func randIntn(n int) int {
	if n <= 1 {
		return 0
	}
	return rand.Intn(n)
}

// LineID uniquely identifies a line of code.
type LineID struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// String returns a string representation of LineID for use as map keys.
func (l LineID) String() string {
	return fmt.Sprintf("%s:%d", l.File, l.Line)
}

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

// Analyzer parses and analyzes GCC CFG dump files for fuzzing guidance.
type Analyzer struct {
	cfgPath       string                   // Path to the .cfg file
	functions     map[string]*CFGFunction  // Parsed functions by name
	lineToBB      map[LineID][]int         // Map of File:Line -> list of BB IDs
	bbToSuccCount map[string]int           // Map of "FuncName:BBID" -> successor count
	bbWeights     map[string]*BBWeightInfo // Map of "FuncName:BBID" -> weight info

	// CFG-guided specific
	mapping           *CoverageMapping // Line-to-seed mapping
	targetFunctions   []string         // Functions to focus on
	sourceDir         string           // Directory containing source files
	weightDecayFactor float64          // Decay factor for BB weights after failed iterations
}

// NewAnalyzer creates a new analyzer for the given CFG file.
// weightDecayFactor should be in range (0, 1], default 0.8 if invalid.
func NewAnalyzer(cfgPath string, targetFunctions []string, sourceDir string, mappingPath string, weightDecayFactor float64) (*Analyzer, error) {
	// Validate and set default for weightDecayFactor
	if weightDecayFactor <= 0 || weightDecayFactor > 1 {
		weightDecayFactor = 0.8
	}

	cfgAnalyzer := &Analyzer{
		cfgPath:           cfgPath,
		functions:         make(map[string]*CFGFunction),
		lineToBB:          make(map[LineID][]int),
		bbToSuccCount:     make(map[string]int),
		bbWeights:         make(map[string]*BBWeightInfo),
		targetFunctions:   targetFunctions,
		sourceDir:         sourceDir,
		weightDecayFactor: weightDecayFactor,
	}

	if err := cfgAnalyzer.Parse(); err != nil {
		return nil, fmt.Errorf("failed to parse CFG file: %w", err)
	}

	// Validate target functions exist
	for _, fn := range targetFunctions {
		if _, ok := cfgAnalyzer.functions[fn]; !ok {
			return nil, fmt.Errorf("target function %s not found in CFG", fn)
		}
	}

	// Create or load coverage mapping
	mapping, err := NewCoverageMapping(mappingPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create coverage mapping: %w", err)
	}
	cfgAnalyzer.mapping = mapping

	return cfgAnalyzer, nil
}

// Regular expressions for parsing CFG
var (
	// Match function headers including C++ anonymous namespace names like {anonymous}::pass_expand::execute
	reFunctionHeader = regexp.MustCompile(`^;; Function ([^\s(]+) \(([^,]+),`)
	reSuccSummary    = regexp.MustCompile(`^;; (\d+) succs \{ ([^}]*) \}`)
	reBBStart        = regexp.MustCompile(`^\s*<bb (\d+)>\s*:?`)
	reLineInfo       = regexp.MustCompile(`\[([^:\]]+):(\d+):\d+(?:\s+discrim\s+\d+)?\]`)
)

// Parse parses the CFG file and builds internal data structures.
func (c *Analyzer) Parse() error {
	file, err := os.Open(c.cfgPath)
	if err != nil {
		return fmt.Errorf("failed to open CFG file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var currentFunc *CFGFunction
	var currentBB *BasicBlock
	parsingFunctionBody := false

	for scanner.Scan() {
		line := scanner.Text()

		if matches := reFunctionHeader.FindStringSubmatch(line); matches != nil {
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

		if !parsingFunctionBody && strings.Contains(line, currentFunc.Name) &&
			strings.Contains(line, "(") && !strings.HasPrefix(line, ";;") {
			parsingFunctionBody = true
			continue
		}

		if !parsingFunctionBody {
			continue
		}

		if matches := reBBStart.FindStringSubmatch(line); matches != nil {
			bbID, _ := strconv.Atoi(matches[1])
			currentBB = &BasicBlock{
				ID:       bbID,
				Function: currentFunc.Name,
				Lines:    []int{},
			}
			if succs, ok := currentFunc.SuccsMap[bbID]; ok {
				currentBB.Successors = succs
			}
			currentFunc.Blocks[bbID] = currentBB
			continue
		}

		if currentBB != nil {
			matches := reLineInfo.FindAllStringSubmatch(line, -1)
			for _, m := range matches {
				filePath := m[1]
				lineNum, _ := strconv.Atoi(m[2])
				if currentBB.File == "" {
					currentBB.File = filePath
				}
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

		if line == "}" {
			if currentBB != nil {
				currentBB = nil
			}
		}
	}

	if currentFunc != nil {
		c.functions[currentFunc.Name] = currentFunc
		c.indexFunction(currentFunc)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading CFG file: %w", err)
	}

	c.buildPredecessorMaps()
	return nil
}

func (c *Analyzer) buildPredecessorMaps() {
	for _, fn := range c.functions {
		fn.PredsMap = make(map[int][]int)
		for bbID := range fn.Blocks {
			fn.PredsMap[bbID] = []int{}
		}
		for bbID, succs := range fn.SuccsMap {
			for _, succID := range succs {
				fn.PredsMap[succID] = append(fn.PredsMap[succID], bbID)
			}
		}
		for bbID, bb := range fn.Blocks {
			bb.Predecessors = fn.PredsMap[bbID]
		}
	}
}

func (c *Analyzer) indexFunction(fn *CFGFunction) {
	for bbID, bb := range fn.Blocks {
		for _, lineNum := range bb.Lines {
			lid := LineID{File: bb.File, Line: lineNum}
			c.lineToBB[lid] = append(c.lineToBB[lid], bbID)
		}
		key := fmt.Sprintf("%s:%d", fn.Name, bbID)
		c.bbToSuccCount[key] = len(bb.Successors)
		c.bbWeights[key] = &BBWeightInfo{
			Attempts: 0,
			Weight:   float64(len(bb.Successors)),
		}
	}
}

// GetFunction returns a parsed function by name.
func (c *Analyzer) GetFunction(name string) (*CFGFunction, bool) {
	fn, ok := c.functions[name]
	return fn, ok
}

// GetAllFunctions returns all parsed function names.
func (c *Analyzer) GetAllFunctions() []string {
	names := make([]string, 0, len(c.functions))
	for name := range c.functions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetBasicBlocksForLine returns the basic block IDs that cover a given source line.
func (c *Analyzer) GetBasicBlocksForLine(file string, line int) []int {
	lid := LineID{File: file, Line: line}
	return c.lineToBB[lid]
}

// GetSuccessorCount returns the number of successors for a basic block.
func (c *Analyzer) GetSuccessorCount(funcName string, bbID int) int {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	return c.bbToSuccCount[key]
}

// TargetInfo represents a target basic block with context for fuzzing.
type TargetInfo struct {
	Function         string
	BBID             int
	SuccessorCount   int
	Lines            []int
	File             string
	BaseSeed         string
	BaseSeedLine     int
	DistanceFromBase int
}

// BBCandidate represents a candidate basic block for targeting.
type BBCandidate struct {
	Function       string
	BBID           int
	SuccessorCount int
	Lines          []int
	File           string
	Weight         float64
	Predecessors   []int
}

// SelectTarget selects the best uncovered basic block to target.
func (c *Analyzer) SelectTarget() *TargetInfo {
	coveredLines := c.mapping.GetCoveredLines()

	candidate := c.selectTargetBB(c.targetFunctions, coveredLines)
	if candidate == nil {
		logger.Debug("[Analyzer] No uncovered BBs found - all covered!")
		return nil
	}

	logger.Debug("[Analyzer] Selected candidate: %s:BB%d (weight=%.2f, succs=%d, preds=%v)",
		candidate.Function, candidate.BBID, candidate.Weight, candidate.SuccessorCount, candidate.Predecessors)

	info := &TargetInfo{
		Function:       candidate.Function,
		BBID:           candidate.BBID,
		SuccessorCount: candidate.SuccessorCount,
		Lines:          candidate.Lines,
		File:           candidate.File,
	}

	baseSeedID, baseLine, found := c.findCoveredPredecessorSeed(candidate, coveredLines)
	if found {
		info.BaseSeed = fmt.Sprintf("%d", baseSeedID)
		info.BaseSeedLine = baseLine.Line
		info.DistanceFromBase = 1
		logger.Debug("[Analyzer] Found predecessor-based base seed: %d (line %d)", baseSeedID, baseLine.Line)
	} else if len(candidate.Predecessors) == 0 {
		// Function entry BB (no predecessors) - use any covered seed from this function
		// Try to find any covered line in this function to use as base
		fn, ok := c.functions[candidate.Function]
		if ok {
			for _, bb := range fn.Blocks {
				for _, lineNum := range bb.Lines {
					lid := LineID{File: bb.File, Line: lineNum}
					if coveredLines[lid] {
						seedID, seedFound := c.mapping.GetSeedForLine(lid)
						if seedFound {
							info.BaseSeed = fmt.Sprintf("%d", seedID)
							info.BaseSeedLine = lineNum
							info.DistanceFromBase = abs(candidate.Lines[0] - lineNum)
							logger.Debug("[Analyzer] Using function entry base seed: %d (line %d)", seedID, lineNum)
							return info
						}
					}
				}
			}
		}
		// No covered seed found in this function - can still target since it's an entry BB
		logger.Debug("[Analyzer] Function entry BB with no covered predecessor, using first available seed")
	}
	// Note: BBs with predecessors but no covered predecessor are filtered out in selectTargetBB

	return info
}

func (c *Analyzer) selectTargetBB(targetFunctions []string, coveredLines map[LineID]bool) *BBCandidate {
	var candidates []BBCandidate

	for _, funcName := range targetFunctions {
		fn, ok := c.functions[funcName]
		if !ok {
			continue
		}

		for bbID, bb := range fn.Blocks {
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

			// Check reachability: BB must have no predecessors (function entry) OR
			// at least one predecessor that has been covered
			isReachable := len(bb.Predecessors) == 0 // No predecessors = entry point (like BB2)
			if !isReachable {
				for _, predID := range bb.Predecessors {
					predBB, ok := fn.Blocks[predID]
					if !ok {
						continue
					}
					// Check if any line in predecessor is covered
					for _, lineNum := range predBB.Lines {
						lid := LineID{File: predBB.File, Line: lineNum}
						if coveredLines[lid] {
							isReachable = true
							break
						}
					}
					if isReachable {
						break
					}
				}
			}

			if hasUncoveredLine && len(bb.Lines) > 0 && isReachable {
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

	// Sort by weight descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Weight > candidates[j].Weight
	})

	// Find all candidates with the maximum weight
	maxWeight := candidates[0].Weight
	var topCandidates []BBCandidate
	for _, c := range candidates {
		if c.Weight == maxWeight {
			topCandidates = append(topCandidates, c)
		} else {
			break // Since sorted, no more max weight candidates
		}
	}

	// Randomly select from top candidates
	idx := randIntn(len(topCandidates))
	return &topCandidates[idx]
}

func (c *Analyzer) findCoveredPredecessorSeed(candidate *BBCandidate, coveredLines map[LineID]bool) (int64, LineID, bool) {
	coveredPreds := c.GetCoveredPredecessors(candidate.Function, candidate.BBID, coveredLines)
	if len(coveredPreds) == 0 {
		return 0, LineID{}, false
	}

	fn, ok := c.functions[candidate.Function]
	if !ok {
		return 0, LineID{}, false
	}

	for _, predID := range coveredPreds {
		predBB, ok := fn.Blocks[predID]
		if !ok {
			continue
		}

		for _, lineNum := range predBB.Lines {
			lid := LineID{File: predBB.File, Line: lineNum}
			if coveredLines[lid] {
				seedID, found := c.mapping.GetSeedForLine(lid)
				if found {
					return seedID, lid, true
				}
			}
		}
	}

	return 0, LineID{}, false
}

// GetCoveredPredecessors returns the list of covered predecessor BB IDs.
func (c *Analyzer) GetCoveredPredecessors(funcName string, bbID int, coveredLines map[LineID]bool) []int {
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

// Coverage tracking methods

// RecordCoverage records covered lines for a seed. Should only be called for qualified seeds
// (initial seeds, seeds with new coverage, or seeds that found bugs).
func (c *Analyzer) RecordCoverage(seedID int64, coveredLines []string) {
	lineIDs := c.parseLinesToIDs(coveredLines)
	c.mapping.RecordLines(lineIDs, seedID)
}

// CheckNewCoverage checks if the given lines would increase BB coverage without recording.
// Returns true if any new BB would be covered.
func (c *Analyzer) CheckNewCoverage(coveredLines []string) bool {
	lineIDs := c.parseLinesToIDs(coveredLines)
	currentCovered := c.mapping.GetCoveredLines()

	for _, lid := range lineIDs {
		if !currentCovered[lid] {
			return true
		}
	}
	return false
}

// parseLinesToIDs converts "file:line" strings to LineID structs.
func (c *Analyzer) parseLinesToIDs(coveredLines []string) []LineID {
	lineIDs := make([]LineID, 0, len(coveredLines))
	for _, line := range coveredLines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			var lineNum int
			fmt.Sscanf(parts[1], "%d", &lineNum)
			if lineNum > 0 {
				filePath := parts[0]
				if c.sourceDir != "" && !filepath.IsAbs(filePath) {
					filePath = filepath.Join(c.sourceDir, filePath)
				}
				lineIDs = append(lineIDs, LineID{File: filePath, Line: lineNum})
			}
		}
	}
	return lineIDs
}

func (c *Analyzer) GetCoveredLines() map[LineID]bool {
	return c.mapping.GetCoveredLines()
}

// GetFunctionCoverage returns BB coverage statistics for target functions.
func (c *Analyzer) GetFunctionCoverage() map[string]struct{ Covered, Total int } {
	coveredLines := c.GetCoveredLines()
	result := make(map[string]struct{ Covered, Total int })

	for _, funcName := range c.targetFunctions {
		covered, total := c.getFunctionCoverage(funcName, coveredLines)
		result[funcName] = struct{ Covered, Total int }{covered, total}
	}

	return result
}

// GetTotalBBCoverage returns the total BB coverage across all target functions.
// Returns (coveredBBs, totalBBs).
func (c *Analyzer) GetTotalBBCoverage() (int, int) {
	coveredLines := c.GetCoveredLines()
	totalCovered := 0
	totalBBs := 0

	for _, funcName := range c.targetFunctions {
		covered, total := c.getFunctionCoverage(funcName, coveredLines)
		totalCovered += covered
		totalBBs += total
	}

	return totalCovered, totalBBs
}

// GetBBCoverageBasisPoints returns BB coverage as basis points (万分比).
// E.g., 1234 means 12.34% coverage.
func (c *Analyzer) GetBBCoverageBasisPoints() uint64 {
	covered, total := c.GetTotalBBCoverage()
	if total == 0 {
		return 0
	}
	return uint64(covered * 10000 / total)
}

func (c *Analyzer) getFunctionCoverage(funcName string, coveredLines map[LineID]bool) (covered, total int) {
	fn, ok := c.functions[funcName]
	if !ok {
		return 0, 0
	}

	coveredBBs := make(map[int]bool)
	for bbID, bb := range fn.Blocks {
		if bbID <= 1 {
			continue
		}
		total++
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

// GetFunctionLineCoverage returns line coverage statistics.
func (c *Analyzer) GetFunctionLineCoverage() map[string]struct{ Covered, Total int } {
	coveredLines := c.GetCoveredLines()
	result := make(map[string]struct{ Covered, Total int })

	for _, funcName := range c.targetFunctions {
		covered, total := c.getFunctionLineCoverage(funcName, coveredLines)
		result[funcName] = struct{ Covered, Total int }{covered, total}
	}

	return result
}

func (c *Analyzer) getFunctionLineCoverage(funcName string, coveredLines map[LineID]bool) (covered, total int) {
	fn, ok := c.functions[funcName]
	if !ok {
		return 0, 0
	}

	allLines := make(map[LineID]bool)
	for bbID, bb := range fn.Blocks {
		if bbID <= 1 {
			continue
		}
		for _, lineNum := range bb.Lines {
			lid := LineID{File: bb.File, Line: lineNum}
			allLines[lid] = true
		}
	}

	coveredCount := 0
	for lid := range allLines {
		if coveredLines[lid] {
			coveredCount++
		}
	}

	return coveredCount, len(allLines)
}

// GetTotalTargetLines returns the total number of unique source lines in target functions.
func (c *Analyzer) GetTotalTargetLines() int {
	total := 0
	for _, funcName := range c.targetFunctions {
		total += c.getFunctionTotalLines(funcName)
	}
	return total
}

func (c *Analyzer) getFunctionTotalLines(funcName string) int {
	fn, ok := c.functions[funcName]
	if !ok {
		return 0
	}

	allLines := make(map[LineID]bool)
	for bbID, bb := range fn.Blocks {
		if bbID <= 1 {
			continue
		}
		for _, lineNum := range bb.Lines {
			lid := LineID{File: bb.File, Line: lineNum}
			allLines[lid] = true
		}
	}

	return len(allLines)
}

// GetTotalCoveredTargetLines returns the total number of covered lines in target functions.
func (c *Analyzer) GetTotalCoveredTargetLines() int {
	coveredLines := c.GetCoveredLines()
	total := 0
	for _, funcName := range c.targetFunctions {
		covered, _ := c.getFunctionLineCoverage(funcName, coveredLines)
		total += covered
	}
	return total
}

// Persistence
func (c *Analyzer) LoadMapping(path string) error {
	loaded, err := NewCoverageMapping(path)
	if err != nil {
		return err
	}
	c.mapping = loaded
	return nil
}

func (c *Analyzer) SaveMapping(path string) error {
	return c.mapping.Save(path)
}

func (c *Analyzer) GetMapping() *CoverageMapping {
	return c.mapping
}

// Weight management

// DecayBBWeight reduces the weight of a BB after a failed iteration.
// The weight is multiplied by the configured decay factor.
func (c *Analyzer) DecayBBWeight(funcName string, bbID int) {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	wi, ok := c.bbWeights[key]
	if !ok {
		succCount := c.bbToSuccCount[key]
		wi = &BBWeightInfo{Attempts: 0, Weight: float64(succCount)}
		c.bbWeights[key] = wi
	}

	wi.Attempts++
	oldWeight := wi.Weight
	wi.Weight *= c.weightDecayFactor
	logger.Debug("BB %s weight decayed: %.2f -> %.2f (attempts=%d, factor=%.2f)",
		key, oldWeight, wi.Weight, wi.Attempts, c.weightDecayFactor)
}

// RecordSuccess is called when a BB is successfully covered.
// It resets the attempt counter (weight is NOT restored to allow continued decay if retargeted).
func (c *Analyzer) RecordSuccess(funcName string, bbID int) {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	if wi, ok := c.bbWeights[key]; ok {
		logger.Debug("BB %s successfully covered after %d attempts", key, wi.Attempts)
		wi.Attempts = 0
	}
}

func (c *Analyzer) GetBBWeight(funcName string, bbID int) float64 {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	if wi, ok := c.bbWeights[key]; ok {
		return wi.Weight
	}
	return float64(c.bbToSuccCount[key])
}

func (c *Analyzer) GetBBAttempts(funcName string, bbID int) int {
	key := fmt.Sprintf("%s:%d", funcName, bbID)
	if wi, ok := c.bbWeights[key]; ok {
		return wi.Attempts
	}
	return 0
}

// GetSourceFile extracts the source file path from the CFG file path.
func GetSourceFile(cfgPath string) string {
	base := filepath.Base(cfgPath)
	parts := strings.Split(base, ".")
	if len(parts) >= 3 {
		return strings.Join(parts[:len(parts)-2], ".")
	}
	return base
}

// FindCFGFiles finds all CFG files matching the pattern in a directory.
func FindCFGFiles(buildDir string, sourceFile string) ([]string, error) {
	pattern := filepath.Join(buildDir, "gcc", sourceFile+"*.cfg")
	return filepath.Glob(pattern)
}

// PrintFunctionSummary prints a summary of a parsed function for debugging.
func (c *Analyzer) PrintFunctionSummary(funcName string) {
	fn, ok := c.functions[funcName]
	if !ok {
		logger.Debug("Function %s not found", funcName)
		return
	}

	logger.Debug("Function: %s (mangled: %s)", fn.Name, fn.MangledName)
	logger.Debug("  Basic blocks: %d", len(fn.Blocks))

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

// CoverageMapping maintains the mapping between source lines and all seeds that covered them.
// Multiple seeds can be mapped to the same line for fairer base seed selection.
type CoverageMapping struct {
	mu          sync.RWMutex
	LineToSeeds map[string][]int64 `json:"line_to_seeds"`
	path        string
}

// NewCoverageMapping creates a new CoverageMapping instance.
func NewCoverageMapping(path string) (*CoverageMapping, error) {
	cm := &CoverageMapping{
		LineToSeeds: make(map[string][]int64),
		path:        path,
	}

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if err := cm.Load(path); err != nil {
				return nil, fmt.Errorf("failed to load existing mapping: %w", err)
			}
		}
	}

	return cm, nil
}

// RecordLine adds a seed to the line's seed list (no duplicates).
// Returns true if this seed is newly added to this line.
func (cm *CoverageMapping) RecordLine(line LineID, seedID int64) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	key := line.String()
	seeds := cm.LineToSeeds[key]

	// Check if seed already recorded for this line
	for _, s := range seeds {
		if s == seedID {
			return false
		}
	}

	cm.LineToSeeds[key] = append(seeds, seedID)
	return true
}

// RecordLines adds a seed to multiple lines' seed lists.
// Returns the count of lines where this seed was newly added.
func (cm *CoverageMapping) RecordLines(lines []LineID, seedID int64) int {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	newCount := 0
	for _, line := range lines {
		key := line.String()
		seeds := cm.LineToSeeds[key]

		// Check if seed already recorded for this line
		found := false
		for _, s := range seeds {
			if s == seedID {
				found = true
				break
			}
		}

		if !found {
			cm.LineToSeeds[key] = append(seeds, seedID)
			if len(seeds) == 0 {
				// This is a newly covered line
				newCount++
			}
		}
	}
	return newCount
}

// GetSeedForLine returns a randomly selected seed from the seeds that covered this line.
func (cm *CoverageMapping) GetSeedForLine(line LineID) (int64, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	seeds, exists := cm.LineToSeeds[line.String()]
	if !exists || len(seeds) == 0 {
		return 0, false
	}

	// Random selection from available seeds
	idx := randIntn(len(seeds))
	return seeds[idx], true
}

// GetSeedsForLine returns all seeds that covered this line.
func (cm *CoverageMapping) GetSeedsForLine(line LineID) []int64 {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	seeds, exists := cm.LineToSeeds[line.String()]
	if !exists {
		return nil
	}

	// Return a copy to avoid race conditions
	result := make([]int64, len(seeds))
	copy(result, seeds)
	return result
}

func (cm *CoverageMapping) IsCovered(line LineID) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	seeds, exists := cm.LineToSeeds[line.String()]
	return exists && len(seeds) > 0
}

func (cm *CoverageMapping) GetCoveredLines() map[LineID]bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make(map[LineID]bool, len(cm.LineToSeeds))
	for key, seeds := range cm.LineToSeeds {
		// Only count lines with at least one seed
		if len(seeds) == 0 {
			continue
		}
		var file string
		var line int
		for i := len(key) - 1; i >= 0; i-- {
			if key[i] == ':' {
				file = key[:i]
				fmt.Sscanf(key[i+1:], "%d", &line)
				break
			}
		}
		result[LineID{File: file, Line: line}] = true
	}
	return result
}

func (cm *CoverageMapping) GetCoveredLinesForFile(file string) []int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var lines []int
	prefix := file + ":"
	for key, seeds := range cm.LineToSeeds {
		// Only count lines with at least one seed
		if len(seeds) == 0 {
			continue
		}
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			var line int
			fmt.Sscanf(key[len(prefix):], "%d", &line)
			lines = append(lines, line)
		}
	}
	return lines
}

func (cm *CoverageMapping) TotalCoveredLines() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	count := 0
	for _, seeds := range cm.LineToSeeds {
		if len(seeds) > 0 {
			count++
		}
	}
	return count
}

func (cm *CoverageMapping) Save(path string) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if path == "" {
		path = cm.path
	}
	if path == "" {
		return fmt.Errorf("no path specified for saving")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(cm, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal mapping: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write mapping file: %w", err)
	}

	return nil
}

func (cm *CoverageMapping) Load(path string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read mapping file: %w", err)
	}

	if err := json.Unmarshal(data, cm); err != nil {
		return fmt.Errorf("failed to unmarshal mapping: %w", err)
	}

	cm.path = path
	return nil
}

func (cm *CoverageMapping) FindClosestCoveredLine(file string, targetLine int) (LineID, int64, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	closestLine := -1
	var closestSeeds []int64

	prefix := file + ":"
	for key, seeds := range cm.LineToSeeds {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix && len(seeds) > 0 {
			var line int
			fmt.Sscanf(key[len(prefix):], "%d", &line)

			if line <= targetLine && line > closestLine {
				closestLine = line
				closestSeeds = seeds
			}
		}
	}

	if closestLine == -1 || len(closestSeeds) == 0 {
		return LineID{}, 0, false
	}

	// Random selection from available seeds
	idx := randIntn(len(closestSeeds))
	return LineID{File: file, Line: closestLine}, closestSeeds[idx], true
}
