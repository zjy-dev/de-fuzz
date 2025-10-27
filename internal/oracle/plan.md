# Oracle Module Plan

## Overview

The oracle module provides an abstraction for bug detection during fuzzing. It analyzes execution results and determines whether a bug has been found, replacing the previous analysis module for this specific concern.

## Interface

```go
type Oracle interface {
    Analyze(s *seed.Seed, results []Result) (bugFound bool, description string, err error)
}

type Result struct {
    Stdout   string
    Stderr   string
    ExitCode int
}
```

## Implementations

### LLMOracle

Uses a Large Language Model to analyze execution results and detect bugs.

**Key Features:**

- Performs initial heuristic checks for obvious crashes
- Detects crash indicators in output (segfaults, stack smashing, etc.)
- Filters expected/benign error messages
- Uses LLM for deeper analysis of anomalies
- Returns detailed bug descriptions

**Detection Strategy:**

1. **Heuristic Checks:**

   - Non-zero exit codes
   - Crash keywords in stdout/stderr
   - Unexpected stderr output

2. **LLM Analysis:**
   - If anomalies detected, build analysis prompt
   - LLM provides detailed bug description
   - Falls back to basic description if LLM fails

**Crash Indicators:**

- Segmentation fault
- Stack smashing detected
- Buffer overflow
- Heap corruption
- Use after free
- Double free
- Assertion failures

**Expected Errors (ignored):**

- Compiler warnings
- Compiler notes

## Usage in Fuzzer

The fuzzer uses the oracle after executing a seed:

```go
// Execute test cases
runRes := executeTestCases(currentSeed)

// Use oracle to detect bugs
bugFound, description, err := f.oracle.Analyze(currentSeed, runRes)

if bugFound {
    // Save bug report with description
    bug := &analysis.Bug{
        Seed:        currentSeed,
        Results:     runRes,
        Description: description,
    }
    f.reporter.Save(bug)
}
```

## Integration with Analysis Module

The oracle complements the existing analysis module:

- **Oracle**: Determines IF a bug exists (test oracle)
- **Analysis**: Provides deeper analysis of program behavior

## Future Work

- Add support for custom crash patterns
- Implement statistical anomaly detection
- Add support for assertion-based oracles
- Integrate with symbolic execution results
- Support for differential testing oracles
