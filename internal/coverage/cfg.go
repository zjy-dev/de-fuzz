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
)

// BasicBlock represents a basic block in the control flow graph.
type BasicBlock struct {
	ID         int    // Basic block number (e.g., 2, 3, 4...)
	Function   string // Function name this BB belongs to
	File       string // Source file path
	Lines      []int  // Source line numbers covered by this BB
	Successors []int  // Successor basic block IDs
}

// CFGFunction represents a function in the CFG with its basic blocks.
type CFGFunction struct {
	Name        string              // Function name
	MangledName string              // Mangled name for C++ functions
	Blocks      map[int]*BasicBlock // Map of BB ID to BasicBlock
	SuccsMap    map[int][]int       // Map of BB ID to successors (from summary section)
}

// CFGAnalyzer parses and analyzes GCC CFG dump files.
type CFGAnalyzer struct {
	cfgPath       string                  // Path to the .cfg file
	functions     map[string]*CFGFunction // Parsed functions by name
	lineToBB      map[LineID][]int        // Map of File:Line -> list of BB IDs
	bbToSuccCount map[string]int          // Map of "FuncName:BBID" -> successor count
}

// NewCFGAnalyzer creates a new CFG analyzer for the given file.
func NewCFGAnalyzer(cfgPath string) *CFGAnalyzer {
	return &CFGAnalyzer{
		cfgPath:       cfgPath,
		functions:     make(map[string]*CFGFunction),
		lineToBB:      make(map[LineID][]int),
		bbToSuccCount: make(map[string]int),
	}
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

	return nil
}

// indexFunction builds the line-to-BB and BB-to-succ-count indices for a function.
func (c *CFGAnalyzer) indexFunction(fn *CFGFunction) {
	for bbID, bb := range fn.Blocks {
		// Index line -> BB mapping
		for _, lineNum := range bb.Lines {
			lid := LineID{File: bb.File, Line: lineNum}
			c.lineToBB[lid] = append(c.lineToBB[lid], bbID)
		}

		// Index BB -> successor count
		key := fmt.Sprintf("%s:%d", fn.Name, bbID)
		c.bbToSuccCount[key] = len(bb.Successors)
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
}

// SelectTargetBB selects the best uncovered basic block to target.
// It prioritizes basic blocks with more successors (higher branching factor).
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
				candidates = append(candidates, BBCandidate{
					Function:       funcName,
					BBID:           bbID,
					SuccessorCount: len(bb.Successors),
					Lines:          bb.Lines,
					File:           bb.File,
				})
			}
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort candidates: higher successor count first, then lower BB ID for determinism
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].SuccessorCount != candidates[j].SuccessorCount {
			return candidates[i].SuccessorCount > candidates[j].SuccessorCount
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
			results = append(results, BBCandidate{
				Function:       funcName,
				BBID:           bbID,
				SuccessorCount: len(bb.Successors),
				Lines:          bb.Lines,
				File:           bb.File,
			})
		}
	}

	// Sort by successor count descending
	sort.Slice(results, func(i, j int) bool {
		if results[i].SuccessorCount != results[j].SuccessorCount {
			return results[i].SuccessorCount > results[j].SuccessorCount
		}
		return results[i].BBID < results[j].BBID
	})

	return results
}

// GetFunctionCoverage calculates coverage statistics for a function.
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
		fmt.Printf("Function %s not found\n", funcName)
		return
	}

	fmt.Printf("Function: %s (mangled: %s)\n", fn.Name, fn.MangledName)
	fmt.Printf("  Basic blocks: %d\n", len(fn.Blocks))

	// Sort BBs by ID for consistent output
	bbIDs := make([]int, 0, len(fn.Blocks))
	for id := range fn.Blocks {
		bbIDs = append(bbIDs, id)
	}
	sort.Ints(bbIDs)

	for _, bbID := range bbIDs {
		bb := fn.Blocks[bbID]
		fmt.Printf("  BB %d: %d successors, %d lines", bbID, len(bb.Successors), len(bb.Lines))
		if len(bb.Successors) > 0 {
			fmt.Printf(", succs: %v", bb.Successors)
		}
		if len(bb.Lines) > 0 {
			fmt.Printf(", lines: %v", bb.Lines)
		}
		fmt.Println()
	}
}
