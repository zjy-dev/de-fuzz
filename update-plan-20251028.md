# Update Plan 2025-10-28

This document outlines the plan for updating the mutation strategy and fuzzing algorithm as described in the `README.md`.

## 1. Mutation Strategy Update

The current mutation strategy is based on coverage increasement. To support different compilers and their specific coverage mechanisms, we will introduce a `Coverage` interface.

### High-Level Plan

1.  **Define `Coverage` Interface:** Create a new interface in a new `coverage` package (`internal/coverage/coverage.go`).
2.  **Implement GCC Coverage:** Create a `GCCCoverage` struct that implements the `Coverage` interface. This implementation will be based on the `gcov` and `lcov` workflow described in `README.md` and will execute commands within the provided container. This sturct should have an extractFunctions() method.
3.  **Integrate into Fuzzer:** The `fuzz` package will be updated to use the `Coverage` interface to decide whether to keep a mutated seed.

### Struct/Interface Update

```go
// internal/coverage/coverage.go
package coverage

// Coverage handles the coverage information for a given compiler.
type Coverage interface {
	// Measure compiles the seed and returns the new coverage info.
	Measure(seedPath string) (newCoverageInfo []byte, err error)

	// HasIncreased checks if the new coverage information has increased
	// compared to the total accumulated coverage.
	HasIncreased(newCoverageInfo []byte) (bool, error)

    // Merge merges the new coverage information into the total coverage.
    Merge(newCoverageInfo []byte) error
}
```

## 2. Fuzzing Algorithm and Test Oracle Update

The fuzzing algorithm will be updated to use a dedicated `Oracle` module for bug detection.

### High-Level Plan

1.  **Create `oracle` package:** A new package `internal/oracle` will be created.
2.  **Define `Oracle` interface:** An `Oracle` interface will be defined to abstract the bug detection logic.
3.  **Implement LLM-based Oracle:** A struct that implements the `Oracle` interface will be created. It will use the LLM to analyze the execution results of a seed.
4.  **Integrate into Fuzzing Loop:** The main fuzzing loop in the `fuzz` package will use the `Oracle` interface to check for bugs.

### Struct/Interface Update

```go
// internal/oracle/oracle.go
package oracle

import (
	"defuzz/internal/exec"
	"defuzz/internal/seed"
)

// Oracle determines if a seed execution has found a bug.
type Oracle interface {
	// Analyze analyzes the execution result of a seed and returns true if a bug is found.
	Analyze(s *seed.Seed, result *exec.Result) (bool, error)
}
```
