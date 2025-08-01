## Plan for `internal/fuzz` Module

### 1. Objective

The `fuzz` module is the central orchestrator of the DeFuzz application. It is responsible for managing the high-level workflow for both the `generate` and `fuzz` modes. It connects all the other modules (`config`, `prompt`, `llm`, `seed`, `compiler`, `vm`, `analysis`, `report`) to execute the end-to-end fuzzing process.

### 2. Core Component: `Fuzzer`

The primary component is the `Fuzzer` struct. It will hold instances of all the modules it depends on, which will be provided to it via a constructor. This use of dependency injection makes the `Fuzzer` highly testable.

```go
package fuzz

import (
    "defuzz/internal/analysis"
    "defuzz/internal/compiler"
    "defuzz/internal/config"
    "defuzz/internal/llm"
    "defuzz/internal/prompt"
    "defuzz/internal/report"
    "defuzz/internal/seed"
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
    analyzer analysis.Analyzer
    reporter report.Reporter

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
    analyzer analysis.Analyzer,
    reporter report.Reporter,
) *Fuzzer {
    // ...
}
```

### 3. Workflow Implementation

The `Fuzzer` will have two main public methods corresponding to the application's modes.

#### a. `Generate()` Method

-   **Responsibility**: Creates the initial `understanding.md` and a set of initial seeds.
-   **Workflow**:
    1.  Call `prompt.BuildUnderstandPrompt()` to create the initial prompt.
    2.  Call `llm.Understand()` with the prompt to get the context.
    3.  **TODO**: Save the context to `understanding.md` using a `seed` module function (this function needs to be added to the `seed` package).
    4.  Loop a configured number of times:
        a. Call `prompt.BuildGeneratePrompt()` with the context.
        b. Call `llm.Generate()` to create a new seed.
        c. **TODO**: Save the new seed to disk using a `seed` module function.

#### b. `Fuzz()` Method

-   **Responsibility**: Runs the main fuzzing loop.
-   **Workflow**:
    1.  **Setup**:
        a. **TODO**: Load the `understanding.md` context from disk via the `seed` module.
        b. **TODO**: Load the initial seeds from disk into the `seedPool` via the `seed` module.
        c. Call `vm.Create()` to start the execution container.
    2.  **Loop**: Continue as long as `seedPool.Next()` returns a seed and the bug quota is not met.
        a. Get the next `seed` from the pool.
        b. Call `compiler.Compile(seed, vm)`.
        c. If compilation succeeds, call `vm.Run()` to execute the binary.
        d. Call `analyzer.AnalyzeResult()` with the seed and execution feedback.
        e. If a `bug` is found:
            i. Increment `bugCount`.
            ii. Call `reporter.Save(bug)`.
            iii. Call `prompt.BuildMutatePrompt()` and `llm.Mutate()` to create a new seed.
            iv. Add the mutated seed back to the `seedPool`.
        f. If no bug is found, decide whether to mutate or discard the seed (this logic can be simple initially and refined later).
    3.  **Cleanup**: Call `vm.Stop()` to destroy the container.

### 4. Testing

The `Fuzzer` will be tested using mocks for all its dependencies. This will allow for testing the orchestration logic in isolation without needing real LLM calls, file system access, or container management.
