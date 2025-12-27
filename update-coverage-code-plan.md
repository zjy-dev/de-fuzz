# Update Coverage Code Plan

Based on `update-coverage-plan.md`, this document outlines the code changes required to implement the new coverage-guided fuzzing logic.

## 1. Configuration (`internal/config`)

We need to parse the `targets` section from the YAML configuration to know which files/functions to focus on.

```go
// internal/config/config.go

type TargetFunction struct {
    File      string   `mapstructure:"file"`
    Functions []string `mapstructure:"functions"`
}

type CompilerConfig struct {
    // ... existing fields ...
    
    // Targets specifies the files and functions to focus on
    Targets []TargetFunction `mapstructure:"targets"`
}
```

## 2. Coverage Mapping (`internal/coverage`)

We need a component to maintain the `Line -> FirstSeedID` mapping.

```go
// internal/coverage/mapping.go

// LineID uniquely identifies a line of code
type LineID struct {
    File string
    Line int
}

// CoverageMapper handles the persistence and retrieval of coverage-to-seed mapping.
type CoverageMapper interface {
    // RecordCoverage updates the mapping with new coverage from a seed.
    // Returns true if any new lines were covered.
    RecordCoverage(seedID int64, report Report) (bool, error)

    // GetSeedForLine returns the ID of the first seed that covered the given line.
    GetSeedForLine(line LineID) (int64, bool)

    // Save persists the mapping to disk.
    Save(path string) error

    // Load loads the mapping from disk.
    Load(path string) error
}
```

## 3. CFG Analysis (`internal/coverage`)

We need a component to parse GCC CFG dumps and select the best target basic block.

```go
// internal/coverage/cfg.go

// BasicBlock represents a node in the Control Flow Graph.
type BasicBlock struct {
    ID        int
    File      string
    Lines     []int // Lines contained in this BB
    Successors []int // IDs of successor BBs
}

// CFGAnalyzer handles loading and querying the Control Flow Graph.
type CFGAnalyzer interface {
    // LoadCFGs loads .cfg files for the specified target files.
    // searchPath is where the .cfg files are located (usually build dir).
    LoadCFGs(targets []config.TargetFunction, searchPath string) error

    // GetBasicBlock returns the BasicBlock containing the given line.
    GetBasicBlock(file string, line int) (*BasicBlock, bool)

    // SelectTargetBB selects an uncovered basic block with the most successors.
    // It considers only blocks reachable from currently covered code (frontier).
    // coveredLines is a set of all currently covered lines.
    SelectTargetBB(coveredLines map[LineID]bool) (*BasicBlock, error)
}
```

## 4. Divergence Analysis (`internal/coverage`)

使用 uftrace 进行 **函数级** 发散分析（不做行号级别）。

### 实现位置

新建文件 `internal/coverage/divergence.go`

### 数据结构

```go
// internal/coverage/divergence.go

// FunctionCall represents a single function call in the trace
type FunctionCall struct {
    Name  string // Function name (e.g., "gen_addsi3", "c_parser_peek_token")
    Depth int    // Call stack depth (indentation level)
}

// DivergencePoint represents where two executions diverged (function-level only)
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
```

### 接口定义

```go
// DivergenceAnalyzer handles trace recording and comparison using uftrace
type DivergenceAnalyzer interface {
    // Analyze compares execution traces of two seeds and finds the first divergence point.
    // Returns nil if no divergence found (identical traces).
    Analyze(baseSeedPath, mutatedSeedPath string, compilerPath string) (*DivergencePoint, error)
    
    // Cleanup removes temporary trace directories
    Cleanup() error
}
```

### 实现细节

```go
// UftraceAnalyzer implements DivergenceAnalyzer using uftrace
type UftraceAnalyzer struct {
    workDir     string // Temporary directory for trace files (e.g., /tmp/defuzz-traces-xxx)
    uftraceBin  string // Path to uftrace binary (default: "uftrace")
    contextSize int    // Number of functions to include in context (default: 5)
}

// NewUftraceAnalyzer creates a new analyzer
func NewUftraceAnalyzer() (*UftraceAnalyzer, error) {
    // 1. Check uftrace is installed: exec.LookPath("uftrace")
    // 2. Create temp directory: os.MkdirTemp("", "defuzz-traces-")
    // 3. Return configured analyzer
}

func (a *UftraceAnalyzer) Analyze(baseSeedPath, mutatedSeedPath, compilerPath string) (*DivergencePoint, error) {
    // Step 1: Record traces
    trace1Dir := filepath.Join(a.workDir, "trace1")
    trace2Dir := filepath.Join(a.workDir, "trace2")
    
    // uftrace record -P '.*' -d trace1 gcc -c base.c -o /dev/null
    if err := a.recordTrace(compilerPath, baseSeedPath, trace1Dir); err != nil {
        return nil, fmt.Errorf("recording base trace: %w", err)
    }
    if err := a.recordTrace(compilerPath, mutatedSeedPath, trace2Dir); err != nil {
        return nil, fmt.Errorf("recording mutated trace: %w", err)
    }
    
    // Step 2: Extract cc1 PIDs from task.txt
    pid1, err := a.extractCC1PID(trace1Dir)
    pid2, err := a.extractCC1PID(trace2Dir)
    
    // Step 3: Export and filter call sequences
    calls1, err := a.exportCalls(trace1Dir, pid1)
    calls2, err := a.exportCalls(trace2Dir, pid2)
    
    // Step 4: Skip initialization, find parser start
    start1 := a.findParserStart(calls1)
    start2 := a.findParserStart(calls2)
    calls1 = calls1[start1:]
    calls2 = calls2[start2:]
    
    // Step 5: Find divergence point
    return a.findDivergence(calls1, calls2)
}

// recordTrace runs: uftrace record -P '.*' -d traceDir compiler -c seedPath -o /dev/null
func (a *UftraceAnalyzer) recordTrace(compilerPath, seedPath, traceDir string) error {
    cmd := exec.Command("uftrace", "record",
        "-P", ".*",           // Dynamic tracing for all functions
        "-d", traceDir,       // Output directory
        compilerPath,         // gcc path
        "-c", seedPath,       // Compile only
        "-o", "/dev/null",    // Discard output
    )
    return cmd.Run()
}

// extractCC1PID parses task.txt to find cc1 process ID
func (a *UftraceAnalyzer) extractCC1PID(traceDir string) (string, error) {
    // Read traceDir/task.txt
    // Find line containing "cc1"
    // Extract PID from "pid=XXXXXX"
    data, err := os.ReadFile(filepath.Join(traceDir, "task.txt"))
    if err != nil {
        return "", err
    }
    
    for _, line := range strings.Split(string(data), "\n") {
        if strings.Contains(line, "cc1") {
            // Line format: "TIMESTAMP cc1 pid=852229 ppid=852228"
            parts := strings.Fields(line)
            for _, part := range parts {
                if strings.HasPrefix(part, "pid=") {
                    return strings.TrimPrefix(part, "pid="), nil
                }
            }
        }
    }
    return "", fmt.Errorf("cc1 process not found in task.txt")
}

// exportCalls runs uftrace replay and parses output
func (a *UftraceAnalyzer) exportCalls(traceDir, pid string) ([]FunctionCall, error) {
    // uftrace replay -d traceDir --no-libcall
    cmd := exec.Command("uftrace", "replay", "-d", traceDir, "--no-libcall")
    output, err := cmd.Output()
    if err != nil {
        return nil, err
    }
    
    return a.parseReplayOutput(string(output), pid)
}

// parseReplayOutput extracts function calls for a specific PID
func (a *UftraceAnalyzer) parseReplayOutput(output, pid string) ([]FunctionCall, error) {
    var calls []FunctionCall
    pidFilter := fmt.Sprintf("[%s]", pid)
    
    for _, line := range strings.Split(output, "\n") {
        // Filter by PID
        if !strings.Contains(line, pidFilter) {
            continue
        }
        // Skip scheduling noise
        if strings.Contains(line, "linux:schedule") {
            continue
        }
        // Only function entries (lines with '{' or '()' without '}')
        if !strings.Contains(line, "{") && !(strings.Contains(line, "()") && !strings.Contains(line, "}")) {
            continue
        }
        
        // Extract function name using regex
        // Pattern: "| functionName(" or "|   functionName {"
        re := regexp.MustCompile(`\|\s*(\S+)\s*\(`)
        match := re.FindStringSubmatch(line)
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
        depth := (len(afterPipe) - len(strings.TrimLeft(afterPipe, " "))) / 2
        
        calls = append(calls, FunctionCall{Name: funcName, Depth: depth})
    }
    
    return calls, nil
}

// findParserStart finds the index of first parser-related function
func (a *UftraceAnalyzer) findParserStart(calls []FunctionCall) int {
    for i, call := range calls {
        if strings.Contains(call.Name, "c_parser") || 
           strings.Contains(strings.ToLower(call.Name), "parse") {
            return i
        }
    }
    return 0 // Fallback to start
}

// findDivergence compares two call sequences and returns the divergence point
func (a *UftraceAnalyzer) findDivergence(calls1, calls2 []FunctionCall) (*DivergencePoint, error) {
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
            return nil, nil // Identical traces
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
    
    return result, nil
}

func (a *UftraceAnalyzer) Cleanup() error {
    return os.RemoveAll(a.workDir)
}
```

### 使用示例

```go
analyzer, err := coverage.NewUftraceAnalyzer()
if err != nil {
    log.Fatal(err)
}
defer analyzer.Cleanup()

divPoint, err := analyzer.Analyze(
    "/path/to/base.c",
    "/path/to/mutated.c", 
    "/path/to/gcc",
)

if divPoint != nil {
    fmt.Printf("Divergence at index %d:\n", divPoint.Index)
    fmt.Printf("  Base seed called: %s\n", divPoint.Function1)
    fmt.Printf("  Mutated seed called: %s\n", divPoint.Function2)
    fmt.Printf("  Common prefix: %v\n", divPoint.CommonPrefix)
}
```

## 5. ~~Tree-sitter Integration~~ (已移除)

由于只做函数级分析，不需要 Tree-sitter 进行源码定位。函数名本身已经提供了足够的语义信息给 LLM。

## 6. Fuzz Engine Logic (`internal/fuzz`)

The `Engine` struct needs to be updated to hold these new components and implement the new loop.

```go
// internal/fuzz/engine.go

type EngineConfig struct {
    // ... existing fields ...
    Mapper      coverage.CoverageMapper
    CFG         coverage.CFGAnalyzer
    Divergence  coverage.DivergenceAnalyzer  // Function-level divergence analysis
}

// New Run Loop Logic (Pseudo-code)
/*
func (e *Engine) Run() {
    // 1. Load Initial Seeds & Build Initial Mapping
    e.loadInitialSeeds()

    for {
        // 2. Select Target
        // Find an uncovered BB with most successors that is close to covered code
        targetBB := e.cfg.SelectTargetBB(e.mapper.GetCoveredLines())
        
        // 3. Construct Prompt
        // Find the seed that covers the line "closest" to the target BB in the same function
        baseSeedID := e.mapper.FindClosestSeed(targetBB)
        baseSeed := e.corpus.Get(baseSeedID)
        
        prompt := e.promptBuilder.BuildConstraintSolvingPrompt(baseSeed, targetBB)

        // 4. LLM Mutation
        newSeedContent := e.llm.Generate(prompt)
        newSeed := e.createSeed(newSeedContent)

        // 5. Compile & Measure
        report, err := e.coverage.Measure(newSeed)
        
        // 6. Check Success
        if e.isCovered(targetBB, report) {
            e.mapper.RecordCoverage(newSeed.ID, report)
            e.corpus.Add(newSeed)
            continue // Success! Loop back to pick new target.
        }

        // 7. Divergence Analysis (if failed) - FUNCTION LEVEL ONLY
        divPoint, err := e.divergence.Analyze(baseSeed.Path, newSeed.Path, e.compilerPath)
        if err != nil {
            log.Printf("Divergence analysis failed: %v", err)
            continue
        }
        
        if divPoint != nil {
            // 8. Refine Prompt & Retry with divergence info
            // Include: divergent function names, common prefix, divergent paths
            refinedPrompt := e.promptBuilder.BuildRefinedPrompt(baseSeed, newSeed, divPoint)
            // ... send to LLM again ...
        }
    }
}
*/
```

## 7. Prompt Builder (`internal/prompt`)

Update `PromptBuilder` to support the new prompt strategies.

```go
// internal/prompt/prompt.go

type Builder interface {
    // ... existing methods ...

    // BuildConstraintSolvingPrompt creates a prompt to guide LLM to cover a specific BB.
    BuildConstraintSolvingPrompt(baseSeed *seed.Seed, targetBB *coverage.BasicBlock, contextCode string) string

    // BuildRefinedPrompt creates a prompt with function-level divergence info.
    // Uses DivergencePoint which contains:
    // - Function1/Function2: the divergent function names
    // - CommonPrefix: functions called before divergence  
    // - Path1/Path2: divergent execution paths
    BuildRefinedPrompt(baseSeed, mutatedSeed *seed.Seed, div *coverage.DivergencePoint) string
}
```

### Example Refined Prompt Template

```
The mutation did not achieve the target coverage. Here's what happened:

## Divergence Analysis (Function-Level)
- Divergence Point: After {{len .CommonPrefix}} common function calls
- Base seed executed: {{.Function1}}
- Mutated seed executed: {{.Function2}}

## Context Before Divergence
The following functions were called by both executions before diverging:
{{range .CommonPrefix}}
- {{.}}
{{end}}

## Execution Path After Divergence
Base seed path: {{join .Path1 " → "}}
Mutated seed path: {{join .Path2 " → "}}

## Task
Please modify the seed to take the path through {{.Function1}} instead of {{.Function2}}.
The divergence occurred at function call #{{.Index}} in the parsing phase.
```

## 8. Implementation Checklist

| # | Component | File | Priority | Status |
|---|-----------|------|----------|--------|
| 1 | Config targets | `internal/config/config.go` | High | TODO |
| 2 | Coverage mapping | `internal/coverage/mapping.go` | High | ✅ DONE |
| 3 | CFG analysis | `internal/coverage/cfg.go` | High | ✅ DONE |
| 4 | **Divergence analysis** | `internal/coverage/divergence.go` | Medium | ✅ DONE (2024-12-24) |
| 5 | Engine integration | `internal/fuzz/engine.go` | High | ✅ DONE (2024-12-24) |
| 6 | Prompt builder | `internal/prompt/prompt.go` | Medium | ✅ DONE (2024-12-24) |

### Dependencies

```
uftrace (v0.15+) - Must be installed on system
  - Used by: DivergenceAnalyzer
  - Install: apt install uftrace
```

### Implemented Files (2024-12-24)

#### `internal/coverage/divergence.go`
- `FunctionCall` struct - represents a function call in trace
- `DivergencePoint` struct - represents where two executions diverged
- `DivergenceAnalyzer` interface
- `UftraceAnalyzer` implementation with methods:
  - `NewUftraceAnalyzer()` - creates analyzer, checks uftrace installed
  - `Analyze(basePath, mutatedPath, compilerPath)` - finds divergence point
  - `Cleanup()` - removes temp files
  - Helper: `ForLLM()` - formats divergence for LLM prompt

#### `internal/fuzz/engine.go`
- Added `DivergenceAnalyzer` and `CompilerPath` to `EngineConfig`
- Added `MaxDivergenceRetries` config option
- `TryDivergenceRefinedMutation()` - refines mutation using divergence feedback

#### `internal/prompt/prompt.go`
- Added `DivergenceContext` struct
- `BuildDivergenceRefinedPrompt()` - builds prompt with divergence info
