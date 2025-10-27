# Integration Guide - Coverage and Oracle Modules

## Overview

This guide explains how to integrate the new coverage and oracle modules into the main application.

## Prerequisites

Before using the coverage-guided fuzzing with oracle-based bug detection, ensure:

1. You have a VM instance configured
2. You have an LLM client configured
3. You have the target compiler built with coverage instrumentation (for GCC)
4. You have target files/functions identified for coverage tracking

## Step-by-Step Integration

### 1. Initialize Coverage Tracker

For GCC-based fuzzing:

```go
import "defuzz/internal/coverage"

// Define which files to track coverage for
targetFiles := []string{
    "*/gcc/config/i386/*.c",
    "*/gcc/config/aarch64/*.c",
}

// Define which functions to track (empty = all functions)
targetFunctions := []string{
    "ix86_stack_protect_guard",
    "aarch64_stack_protect_guard",
}

// Create GCC coverage tracker
coverageTracker := coverage.NewGCCCoverage(
    vm,                          // VM instance for running commands
    "/root/gcc-build",          // Build directory with instrumented GCC
    "/root/gcc-src",            // GCC source directory
    targetFiles,                // File patterns to track
    targetFunctions,            // Functions to track (or empty)
)
```

### 2. Initialize Oracle

For LLM-based bug detection:

```go
import "defuzz/internal/oracle"

// Create LLM oracle
bugOracle := oracle.NewLLMOracle(
    llmClient,      // Your LLM client instance
    promptBuilder,  // Prompt builder instance
    llmContext,     // The "understanding" context from initial prompt
)
```

### 3. Create Fuzzer with Coverage and Oracle

```go
import "defuzz/internal/fuzz"

fuzzer := fuzz.NewFuzzer(
    cfg,             // Configuration
    promptBuilder,   // Prompt builder
    llmClient,       // LLM client
    seedPool,        // Seed pool
    compiler,        // Compiler instance
    executor,        // Seed executor
    analyzer,        // Analyzer (still used for some tasks)
    reporter,        // Bug reporter
    vm,              // VM instance
    coverageTracker, // NEW: Coverage tracker
    bugOracle,       // NEW: Bug oracle
)
```

### 4. Run Fuzzing

```go
// Generate initial seeds (if needed)
if err := fuzzer.Generate(); err != nil {
    log.Fatalf("Failed to generate seeds: %v", err)
}

// Run fuzzing loop with coverage guidance and oracle
if err := fuzzer.Fuzz(); err != nil {
    log.Fatalf("Fuzzing failed: %v", err)
}
```

## Configuration Example

Update your configuration to include coverage and oracle settings:

```yaml
# configs/fuzzer.yaml
fuzzer:
  isa: x86_64
  strategy: stackguard
  initial_seeds: 5
  bug_quota: 10

coverage:
  type: gcc
  build_dir: /root/gcc-build
  src_dir: /root/gcc-src
  target_files:
    - "*/gcc/config/i386/*.c"
  target_functions:
    - "ix86_stack_protect_guard"

oracle:
  type: llm
  crash_keywords:
    - "segmentation fault"
    - "stack smashing"
    - "buffer overflow"
```

## Advanced Usage

### Custom Coverage Implementation

To implement coverage for a different compiler:

```go
type MyCompilerCoverage struct {
    // your fields
}

func (c *MyCompilerCoverage) Measure(seedPath string) ([]byte, error) {
    // Compile seed and capture coverage
    // Return serialized coverage data
}

func (c *MyCompilerCoverage) HasIncreased(newCoverageInfo []byte) (bool, error) {
    // Compare with accumulated coverage
}

func (c *MyCompilerCoverage) Merge(newCoverageInfo []byte) error {
    // Merge into accumulated coverage
}
```

### Custom Oracle Implementation

To implement a different bug detection strategy:

```go
type MyOracle struct {
    // your fields
}

func (o *MyOracle) Analyze(s *seed.Seed, results []oracle.Result) (bool, string, error) {
    // Analyze execution results
    // Return (bugFound, description, error)
}
```

## Troubleshooting

### Coverage Not Increasing

If coverage never increases:

1. Verify the instrumented compiler is being used
2. Check that .gcda files are being generated
3. Verify target files/functions are correct
4. Check lcov is installed and accessible in the VM

### Oracle Not Detecting Bugs

If bugs are not being detected:

1. Check that crash indicators are being captured in stderr/stdout
2. Verify LLM is properly configured and accessible
3. Review the crash keywords in oracle implementation
4. Check if execution is actually producing crashes

### Performance Issues

If fuzzing is slow:

1. Reduce the number of target files
2. Focus on specific functions instead of all functions
3. Use a faster VM (e.g., native instead of QEMU)
4. Optimize coverage merge operations

## Example: Complete Setup

```go
package main

import (
    "defuzz/internal/config"
    "defuzz/internal/coverage"
    "defuzz/internal/fuzz"
    "defuzz/internal/llm"
    "defuzz/internal/oracle"
    "defuzz/internal/prompt"
    "defuzz/internal/seed"
    "defuzz/internal/vm"
    "log"
)

func main() {
    // Load configuration
    cfg, err := config.Load("configs/fuzzer.yaml")
    if err != nil {
        log.Fatal(err)
    }

    // Initialize VM
    vm := vm.NewPodmanVM("defuzz:latest", exec.NewCommandExecutor())

    // Initialize LLM
    llmClient, err := llm.New("configs/llm.yaml")
    if err != nil {
        log.Fatal(err)
    }

    // Initialize coverage tracker
    coverageTracker := coverage.NewGCCCoverage(
        vm,
        cfg.Coverage.BuildDir,
        cfg.Coverage.SrcDir,
        cfg.Coverage.TargetFiles,
        cfg.Coverage.TargetFunctions,
    )

    // Initialize prompt builder
    promptBuilder := prompt.NewBuilder(cfg)

    // Get LLM understanding context
    understandPrompt, _ := promptBuilder.BuildUnderstandPrompt(
        cfg.Fuzzer.ISA,
        cfg.Fuzzer.Strategy,
        basePath,
    )
    llmContext, err := llmClient.Understand(understandPrompt)
    if err != nil {
        log.Fatal(err)
    }

    // Initialize oracle
    bugOracle := oracle.NewLLMOracle(llmClient, promptBuilder, llmContext)

    // Initialize seed pool
    seedPool := seed.NewInMemoryPool()

    // Initialize other components...
    compiler := // ... your compiler
    executor := // ... your executor
    analyzer := // ... your analyzer
    reporter := // ... your reporter

    // Create fuzzer
    fuzzer := fuzz.NewFuzzer(
        cfg,
        promptBuilder,
        llmClient,
        seedPool,
        compiler,
        executor,
        analyzer,
        reporter,
        vm,
        coverageTracker,
        bugOracle,
    )

    // Run fuzzing
    if err := fuzzer.Fuzz(); err != nil {
        log.Fatal(err)
    }
}
```

## Next Steps

1. Implement the configuration loading for coverage and oracle settings
2. Update the main command to create and pass coverage and oracle instances
3. Test with real fuzzing targets
4. Monitor coverage increase and bug detection rates
5. Tune parameters based on results
