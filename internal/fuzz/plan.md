## Plan for `internal/fuzz` Module

### 1. Objective

The `fuzz` module is the central orchestrator of the DeFuzz application. It is responsible for managing the high-level workflow for both the `generate` and `fuzz` modes. It connects all the other modules (`config`, `prompt`, `llm`, `seed`, `compiler`, `vm`, `analysis`, `report`, `coverage`, `oracle`) to execute the end-to-end fuzzing process.

### 2. Core Component: `Fuzzer`

The primary component is the `Fuzzer` struct. It will hold instances of all the modules it depends on, which will be provided to it via a constructor. This use of dependency injection makes the `Fuzzer` highly testable.

```go
package fuzz

import (
    "defuzz/internal/analysis"
    "defuzz/internal/compiler"
    "defuzz/internal/config"
    "defuzz/internal/coverage"
    "defuzz/internal/llm"
    "defuzz/internal/oracle"
    "defuzz/internal/prompt"
    "defuzz/internal/report"
    "defuzz/internal/seed"
    "defuzz/internal/seed_executor"
    "defuzz/internal/vm"
)

// Fuzzer orchestrates the main fuzzing logic.
type Fuzzer struct {
    // Dependencies (provided via constructor)
    cfg      *config.Config
    prompt   *prompt.Builder
    llm      llm.LLM
    seedPool seed.Pool
    compiler compiler.Compiler
    vm       vm.VM
    executor seed_executor.Executor
    analyzer analysis.Analyzer
    reporter report.Reporter
    coverage coverage.Coverage // NEW: Coverage tracker
    oracle   oracle.Oracle     // NEW: Bug oracle

    // Internal state
    llmContext string // The "understanding" from the LLM
    bugCount   int
}

// NewFuzzer creates and initializes a new Fuzzer instance.
func NewFuzzer(
    cfg *config.Config,
    prompt *prompt.Builder,
    llm llm.LLM,
    seedPool seed.Pool,
    compiler compiler.Compiler,
    vm vm.VM,
    executor seed_executor.Executor,
    analyzer analysis.Analyzer,
    reporter report.Reporter,
    coverage coverage.Coverage,
    oracle oracle.Oracle,
) *Fuzzer {
    // ...
}
```

### 3. Workflow Implementation

The `Fuzzer` will have two main public methods corresponding to the application's modes.

#### a. `Generate()` Method

- **Responsibility**: Creates the initial `understanding.md` and a set of initial seeds.
- **Workflow**:
  1.  Call `prompt.BuildUnderstandPrompt()` to create the initial prompt.
  2.  Call `llm.Understand()` with the prompt to get the context.
  3.  **TODO**: Save the context to `understanding.md` using a `seed` module function (this function needs to be added to the `seed` package).
  4.  Loop a configured number of times:
      a. Call `prompt.BuildGeneratePrompt()` with the context.
      b. Call `llm.Generate()` to create a new seed with multiple test cases.
      c. **TODO**: Save the new seed to disk using a `seed` module function (this will create `source.c` and `inputs.json`).

#### b. `Fuzz()` Method (Updated with Coverage and Oracle)

- **Responsibility**: Runs the main fuzzing loop with coverage-guided mutation and oracle-based bug detection.
- **Workflow**:
  1.  **Setup**:
      a. **TODO**: Load the `understanding.md` context from disk via the `seed` module.
      b. **TODO**: Load the initial seeds from disk into the `seedPool` via the `seed` module.
      c. Call `vm.Create()` to start the execution container.
  2.  **Loop**: Continue as long as `seedPool.Next()` returns a seed and the bug quota is not met.
      a. Get the next `seed` from the pool.
      b. **NEW**: Call `coverage.Measure(seedPath)` to measure coverage for this seed.
      c. **NEW**: Call `coverage.HasIncreased(newCovInfo)` to check if coverage increased.
      d. If coverage did NOT increase, skip this seed and continue to next iteration.
      e. If coverage increased:
      i. Call `coverage.Merge(newCovInfo)` to update total coverage.
      ii. Execute all test cases for the seed via `vm.Run()`.
      iii. **NEW**: Call `oracle.Analyze(seed, results)` to check for bugs.
      iv. If a bug is found: - Increment `bugCount`. - Call `reporter.Save(bug)`.
      v. Since coverage increased, mutate the seed: - Call `prompt.BuildMutatePrompt()` and `llm.Mutate()`. - Add the mutated seed back to the `seedPool`.
  3.  **Cleanup**: Call `vm.Stop()` to destroy the container.

### 4. Key Changes from Original Plan

**Coverage-Guided Fuzzing:**

- Seeds are only kept and mutated if they increase code coverage
- Coverage is tracked using the `coverage.Coverage` interface
- This reduces the size of the seed pool and focuses on interesting inputs

**Oracle-Based Bug Detection:**

- Bug detection is now handled by the `oracle.Oracle` interface
- Separates the concern of "is this a bug?" from general analysis
- The `LLMOracle` implementation uses both heuristics and LLM analysis
- More modular and testable than the previous approach

**Mutation Strategy:**

- Previously: Mutate on bug discovery
- Now: Mutate when coverage increases (regardless of bugs)
- This explores more of the code space systematically

### 5. Testing

The `Fuzzer` will be tested using mocks for all its dependencies. This will allow for testing the orchestration logic in isolation without needing real LLM calls, file system access, or container management. Additional mocks will be needed for `coverage.Coverage` and `oracle.Oracle`.
