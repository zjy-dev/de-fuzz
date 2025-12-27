package coverage

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/logger"
)

// FunctionCall represents a single function call in the trace.
type FunctionCall struct {
	Name  string // Function name (e.g., "gen_addsi3", "c_parser_peek_token")
	Depth int    // Call stack depth (indentation level)
}

// DivergencePoint represents where two executions diverged (function-level only).
type DivergencePoint struct {
	// Divergent function names
	Function1 string // Function called by base seed at divergence point
	Function2 string // Function called by mutated seed at divergence point

	// Index in the call sequence (relative to parser start)
	Index int

	// Context before divergence (last N common function calls)
	CommonPrefix []string

	// Divergent paths (next N function calls after divergence)
	Path1 []string // Base seed's path
	Path2 []string // Mutated seed's path
}

// DivergenceAnalyzer handles trace recording and comparison using uftrace.
type DivergenceAnalyzer interface {
	// Analyze compares execution traces of two seeds and finds the first divergence point.
	// Returns nil if no divergence found (identical traces).
	Analyze(baseSeedPath, mutatedSeedPath string, compilerPath string) (*DivergencePoint, error)

	// Cleanup removes temporary trace directories.
	Cleanup() error
}

// UftraceAnalyzer implements DivergenceAnalyzer using uftrace.
type UftraceAnalyzer struct {
	workDir     string // Temporary directory for trace files
	uftraceBin  string // Path to uftrace binary
	contextSize int    // Number of functions to include in context
}

// NewUftraceAnalyzer creates a new analyzer.
// Returns an error if uftrace is not installed.
func NewUftraceAnalyzer() (*UftraceAnalyzer, error) {
	// Check if uftrace is installed
	uftracePath, err := exec.LookPath("uftrace")
	if err != nil {
		return nil, fmt.Errorf("uftrace not found in PATH: %w (install with: apt install uftrace)", err)
	}

	// Create temporary directory
	workDir, err := os.MkdirTemp("", "defuzz-traces-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	return &UftraceAnalyzer{
		workDir:     workDir,
		uftraceBin:  uftracePath,
		contextSize: 5,
	}, nil
}

// NewUftraceAnalyzerWithWorkDir creates an analyzer with a specific work directory.
// Useful for testing.
func NewUftraceAnalyzerWithWorkDir(workDir string) (*UftraceAnalyzer, error) {
	uftracePath, err := exec.LookPath("uftrace")
	if err != nil {
		return nil, fmt.Errorf("uftrace not found in PATH: %w", err)
	}

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}

	return &UftraceAnalyzer{
		workDir:     workDir,
		uftraceBin:  uftracePath,
		contextSize: 5,
	}, nil
}

// Analyze compares execution traces of two seeds and finds the first divergence point.
func (a *UftraceAnalyzer) Analyze(baseSeedPath, mutatedSeedPath, compilerPath string) (*DivergencePoint, error) {
	logger.Debug("[Divergence] Starting analysis: base=%s, mutated=%s", baseSeedPath, mutatedSeedPath)

	trace1Dir := filepath.Join(a.workDir, "trace1")
	trace2Dir := filepath.Join(a.workDir, "trace2")

	// Clean up any existing trace directories
	os.RemoveAll(trace1Dir)
	os.RemoveAll(trace2Dir)

	// Step 1: Record traces
	logger.Debug("[Divergence] Recording base trace...")
	if err := a.recordTrace(compilerPath, baseSeedPath, trace1Dir); err != nil {
		return nil, fmt.Errorf("recording base trace: %w", err)
	}
	logger.Debug("[Divergence] Recording mutated trace...")
	if err := a.recordTrace(compilerPath, mutatedSeedPath, trace2Dir); err != nil {
		return nil, fmt.Errorf("recording mutated trace: %w", err)
	}

	// Step 2: Extract cc1 PIDs from task.txt
	pid1, err := a.extractCC1PID(trace1Dir)
	if err != nil {
		return nil, fmt.Errorf("extracting cc1 PID from trace1: %w", err)
	}
	pid2, err := a.extractCC1PID(trace2Dir)
	if err != nil {
		return nil, fmt.Errorf("extracting cc1 PID from trace2: %w", err)
	}
	logger.Debug("[Divergence] cc1 PIDs: trace1=%s, trace2=%s", pid1, pid2)

	// Step 3: Export and filter call sequences
	calls1, err := a.exportCalls(trace1Dir, pid1)
	if err != nil {
		return nil, fmt.Errorf("exporting calls from trace1: %w", err)
	}
	calls2, err := a.exportCalls(trace2Dir, pid2)
	if err != nil {
		return nil, fmt.Errorf("exporting calls from trace2: %w", err)
	}
	logger.Debug("[Divergence] Exported calls: trace1=%d, trace2=%d", len(calls1), len(calls2))

	// Step 4: Skip initialization, find parser start
	start1 := a.findParserStart(calls1)
	start2 := a.findParserStart(calls2)
	calls1 = calls1[start1:]
	calls2 = calls2[start2:]
	logger.Debug("[Divergence] After parser start: trace1=%d, trace2=%d", len(calls1), len(calls2))

	// Step 5: Find divergence point
	divergence := a.findDivergence(calls1, calls2)
	if divergence != nil {
		logger.Debug("[Divergence] Found divergence at index %d: %s vs %s",
			divergence.Index, divergence.Function1, divergence.Function2)
	} else {
		logger.Debug("[Divergence] No divergence found (identical traces)")
	}

	return divergence, nil
}

// recordTrace runs uftrace record to capture function calls.
func (a *UftraceAnalyzer) recordTrace(compilerPath, seedPath, traceDir string) error {
	// uftrace record -P '.*' -d traceDir compiler -c seedPath -o /dev/null
	cmd := exec.Command(a.uftraceBin, "record",
		"-P", ".*", // Dynamic tracing for all functions
		"-d", traceDir, // Output directory
		compilerPath,   // gcc path
		"-c", seedPath, // Compile only
		"-o", "/dev/null", // Discard output
	)

	// Capture stderr for debugging
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uftrace record failed: %w, output: %s", err, string(output))
	}

	return nil
}

// extractCC1PID parses task.txt to find cc1 process ID.
func (a *UftraceAnalyzer) extractCC1PID(traceDir string) (string, error) {
	taskFile := filepath.Join(traceDir, "task.txt")
	data, err := os.ReadFile(taskFile)
	if err != nil {
		return "", fmt.Errorf("reading task.txt: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "cc1") {
			// Line format: "TIMESTAMP cc1 pid=852229 ppid=852228"
			// or newer format with different field order
			fields := strings.Fields(line)
			for _, field := range fields {
				if strings.HasPrefix(field, "pid=") {
					return strings.TrimPrefix(field, "pid="), nil
				}
			}
		}
	}
	return "", fmt.Errorf("cc1 process not found in task.txt")
}

// exportCalls runs uftrace replay and parses output.
func (a *UftraceAnalyzer) exportCalls(traceDir, pid string) ([]FunctionCall, error) {
	// uftrace replay -d traceDir --no-libcall
	cmd := exec.Command(a.uftraceBin, "replay", "-d", traceDir, "--no-libcall")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("uftrace replay failed: %w", err)
	}

	return a.parseReplayOutput(string(output), pid)
}

// parseReplayOutput extracts function calls for a specific PID.
func (a *UftraceAnalyzer) parseReplayOutput(output, pid string) ([]FunctionCall, error) {
	var calls []FunctionCall
	pidFilter := fmt.Sprintf("[%s]", pid)

	// Regex to extract function name
	// Pattern matches: "| functionName(" or "|   functionName {"
	funcRe := regexp.MustCompile(`\|\s*([a-zA-Z_][a-zA-Z0-9_:~<>]*)\s*[\({]`)

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		// Filter by PID
		if !strings.Contains(line, pidFilter) {
			continue
		}

		// Skip scheduling noise
		if strings.Contains(line, "linux:schedule") {
			continue
		}

		// Only function entries (lines with '{' or '()' without '}')
		isEntry := strings.Contains(line, "{") ||
			(strings.Contains(line, "()") && !strings.Contains(line, "}"))
		if !isEntry {
			continue
		}

		// Extract function name using regex
		match := funcRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		funcName := match[1]
		if strings.HasPrefix(funcName, "}") {
			continue
		}

		// Calculate depth from indentation after '|'
		pipeIdx := strings.Index(line, "|")
		if pipeIdx < 0 {
			continue
		}
		afterPipe := line[pipeIdx+1:]
		spaces := len(afterPipe) - len(strings.TrimLeft(afterPipe, " "))
		depth := spaces / 2

		calls = append(calls, FunctionCall{Name: funcName, Depth: depth})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning output: %w", err)
	}

	return calls, nil
}

// findParserStart finds the index of first parser-related function.
func (a *UftraceAnalyzer) findParserStart(calls []FunctionCall) int {
	for i, call := range calls {
		name := strings.ToLower(call.Name)
		if strings.Contains(call.Name, "c_parser") ||
			strings.Contains(name, "parse") {
			return i
		}
	}
	return 0 // Fallback to start
}

// findDivergence compares two call sequences and returns the divergence point.
func (a *UftraceAnalyzer) findDivergence(calls1, calls2 []FunctionCall) *DivergencePoint {
	minLen := len(calls1)
	if len(calls2) < minLen {
		minLen = len(calls2)
	}

	divergeIdx := -1
	for i := 0; i < minLen; i++ {
		if calls1[i].Name != calls2[i].Name {
			divergeIdx = i
			break
		}
	}

	if divergeIdx < 0 {
		// No divergence in common prefix
		if len(calls1) != len(calls2) {
			// Different lengths - divergence at end of shorter
			divergeIdx = minLen
		} else {
			return nil // Identical traces
		}
	}

	// Build result
	result := &DivergencePoint{
		Index: divergeIdx,
	}

	// Divergent functions
	if divergeIdx < len(calls1) {
		result.Function1 = calls1[divergeIdx].Name
	}
	if divergeIdx < len(calls2) {
		result.Function2 = calls2[divergeIdx].Name
	}

	// Common prefix (last N functions before divergence)
	prefixStart := divergeIdx - a.contextSize
	if prefixStart < 0 {
		prefixStart = 0
	}
	for i := prefixStart; i < divergeIdx; i++ {
		result.CommonPrefix = append(result.CommonPrefix, calls1[i].Name)
	}

	// Divergent paths (next N functions after divergence)
	pathEnd := divergeIdx + a.contextSize
	for i := divergeIdx; i < pathEnd && i < len(calls1); i++ {
		result.Path1 = append(result.Path1, calls1[i].Name)
	}
	for i := divergeIdx; i < pathEnd && i < len(calls2); i++ {
		result.Path2 = append(result.Path2, calls2[i].Name)
	}

	return result
}

// Cleanup removes temporary trace directories.
func (a *UftraceAnalyzer) Cleanup() error {
	return os.RemoveAll(a.workDir)
}

// SetContextSize sets the number of functions to include in context.
func (a *UftraceAnalyzer) SetContextSize(size int) {
	if size > 0 {
		a.contextSize = size
	}
}

// GetWorkDir returns the work directory path.
func (a *UftraceAnalyzer) GetWorkDir() string {
	return a.workDir
}

// String returns a human-readable description of the divergence point.
func (d *DivergencePoint) String() string {
	if d == nil {
		return "no divergence"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Divergence at index %d:\n", d.Index))
	sb.WriteString(fmt.Sprintf("  Base seed called: %s\n", d.Function1))
	sb.WriteString(fmt.Sprintf("  Mutated seed called: %s\n", d.Function2))

	if len(d.CommonPrefix) > 0 {
		sb.WriteString("  Common prefix: ")
		sb.WriteString(strings.Join(d.CommonPrefix, " → "))
		sb.WriteString("\n")
	}

	if len(d.Path1) > 0 {
		sb.WriteString("  Base path: ")
		sb.WriteString(strings.Join(d.Path1, " → "))
		sb.WriteString("\n")
	}

	if len(d.Path2) > 0 {
		sb.WriteString("  Mutated path: ")
		sb.WriteString(strings.Join(d.Path2, " → "))
		sb.WriteString("\n")
	}

	return sb.String()
}

// ForLLM returns a formatted string suitable for including in LLM prompts.
func (d *DivergencePoint) ForLLM() string {
	if d == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Divergence Analysis (Function-Level)\n\n")
	sb.WriteString(fmt.Sprintf("The execution paths diverged at function call #%d in the parsing phase.\n\n", d.Index))

	sb.WriteString("### Divergence Point\n")
	sb.WriteString(fmt.Sprintf("- Base seed executed: `%s`\n", d.Function1))
	sb.WriteString(fmt.Sprintf("- Mutated seed executed: `%s`\n\n", d.Function2))

	if len(d.CommonPrefix) > 0 {
		sb.WriteString("### Context Before Divergence\n")
		sb.WriteString("The following functions were called by both executions before diverging:\n")
		for _, fn := range d.CommonPrefix {
			sb.WriteString(fmt.Sprintf("- `%s`\n", fn))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Execution Path After Divergence\n")
	if len(d.Path1) > 0 {
		sb.WriteString(fmt.Sprintf("Base seed path: `%s`\n", strings.Join(d.Path1, "` → `")))
	}
	if len(d.Path2) > 0 {
		sb.WriteString(fmt.Sprintf("Mutated seed path: `%s`\n", strings.Join(d.Path2, "` → `")))
	}

	return sb.String()
}
