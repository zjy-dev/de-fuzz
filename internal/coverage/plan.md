# Coverage Module Plan

## Overview

The coverage module provides an abstraction for tracking code coverage during fuzzing. It enables the fuzzer to make decisions about which seeds to keep based on whether they increase coverage.

## Interface

```go
type Coverage interface {
    Measure(seedPath string) (newCoverageInfo []byte, err error)
    HasIncreased(newCoverageInfo []byte) (bool, error)
    Merge(newCoverageInfo []byte) error
}
```

## Implementations

### GCCCoverage

Implements coverage tracking for GCC using the gcov/lcov toolchain.

**Key Features:**

- Compiles seeds using an instrumented GCC compiler
- Captures coverage data using lcov
- Extracts coverage for specific target files and functions
- Tracks function-level coverage (lines covered vs total lines)
- Merges coverage data to maintain cumulative coverage

**Workflow:**

1. Clean previous .gcda files
2. Compile seed with instrumented compiler
3. Capture coverage with lcov
4. Extract target files/functions
5. Parse and serialize coverage data
6. Compare with accumulated coverage
7. Merge if coverage increased

**Configuration:**

- `buildDir`: Where the instrumented GCC was built
- `srcDir`: GCC source directory
- `targetFiles`: File patterns to track (e.g., `"*/gcc/config/i386/*.c"`)
- `targetFunctions`: Specific functions to track (empty = all functions)

## Usage in Fuzzer

The fuzzer uses coverage in the main loop:

```go
// Measure coverage for current seed
newCovInfo, err := f.coverage.Measure(seedPath)

// Check if coverage increased
coverageIncreased, err := f.coverage.HasIncreased(newCovInfo)

if coverageIncreased {
    // Merge coverage and mutate seed
    f.coverage.Merge(newCovInfo)
    // ... mutation logic
}
```

## Future Work

- Add support for other compilers (Clang/LLVM)
- Implement edge coverage (not just line coverage)
- Add support for coverage visualization
- Optimize coverage comparison for large codebases
