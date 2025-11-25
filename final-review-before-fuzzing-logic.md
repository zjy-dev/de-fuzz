# Final Review Before Fuzzing Logic Implementation

This document evaluates the current state of the codebase and outlines the necessary steps before implementing the main fuzzing loop.

## 1. Module Evaluation

### A. Modules to Delete/Repurpose

- **`internal/fuzz`**: Currently empty.
  - **Recommendation**: **Repurpose**. This module should contain the high-level `Engine` or `Fuzzer` struct that orchestrates the fuzzing loop. It should NOT be deleted, but populated. Implementing complex logic directly in `cmd/defuzz/app` is bad practice (separation of concerns). `cmd` should only handle CLI argument parsing and wiring.

### B. Missing Modules (Critical)

The following modules are referenced or logically required but appear to be missing or incomplete:

- **`internal/compiler`**:
  - **Status**: Missing.
  - **Need**: Critical. We need a module to handle compilation of C code to binaries (host or cross-compilation).
  - **Action**: Create `internal/compiler` with interfaces for `GCC` and `Clang`.
- **`internal/vm`** (or `internal/qemu`):
  - **Status**: Missing.
  - **Need**: Critical for cross-architecture fuzzing (running ARM/RISC-V binaries).
  - **Action**: Create `internal/vm` to wrap QEMU interactions.
- **`internal/seed_executor`**:
  - **Status**: Incomplete. Only `executor.go` (interface) exists. `qemu.go` is missing.
  - **Need**: Critical. Needs concrete implementations for `HostExecutor` and `QEMUExecutor`.
  - **Action**: Implement concrete executors.

### C. Existing Modules Status

- **`internal/corpus`**: ✅ Ready. Handles storage, retrieval, and resume logic.
- **`internal/state`**: ✅ Ready. Handles global state persistence.
- **`internal/seed`**: ✅ Ready. Defines data structures and metadata.
- **`internal/coverage`**: ⚠️ Partial. Exists but relies on an injected `compileFunc`. Needs to be integrated with the new `internal/compiler` module.
- **`internal/analysis`**: ✅ Ready. Handles LLM-based result analysis.
- **`internal/exec`**: ✅ Ready. Generic command execution helper.
- **`internal/llm`**: ✅ Ready.
- **`internal/config`**: ✅ Ready.

## 2. Modification Plan

### Step 1: Implement Low-Level Primitives

Before writing the main loop, we must build the foundation:

1.  **Create `internal/compiler`**:
    - Define `Compiler` interface (Compile(seed) -> binary_path).
    - Implement `GCCCompiler`.
2.  **Create `internal/vm`**:
    - Define `VM` interface (Run(binary) -> output).
    - Implement `QEMUVM`.
3.  **Complete `internal/seed_executor`**:
    - Implement `LocalExecutor` (uses `internal/exec` directly).
    - Implement `QEMUExecutor` (uses `internal/vm`).

### Step 2: Integrate Coverage

- Update `internal/coverage` to accept a `Compiler` interface instead of a raw function, or ensure the `compileFunc` adapter works seamlessly with the new `internal/compiler`.

### Step 3: The Fuzzing Engine (`internal/fuzz`)

Instead of putting logic in `cmd`, we will implement the `Engine` in `internal/fuzz`.

**Responsibilities of `Engine`:**

1.  **Initialization**: Load `Corpus`, `State`, `Compiler`, `Executor`, `Analyzer`.
2.  **Loop**:
    - `corpus.Next()` -> Get seed.
    - `compiler.Compile()` -> Binary.
    - `executor.Execute()` -> Result.
    - `coverage.Measure()` -> Coverage Delta.
    - `analysis.Analyze()` -> Bug?
    - `corpus.ReportResult()` -> Update Metadata.
    - **Mutation/Generation**: Use LLM to generate new seeds from the current one (if interesting).
    - `corpus.Add()` -> Add new seeds.

## 3. Decision on `cmd/defuzz/app` vs `internal/fuzz`

**Verdict**: **Keep `internal/fuzz`**.

- **Reasoning**: The fuzzing logic involves complex state management, error handling, and component coordination. Placing this in `cmd` makes the CLI code bloated and hard to test. `cmd` should just be:
  ```go
  // cmd/defuzz/app/fuzz.go
  func runFuzz(...) {
      engine := fuzz.NewEngine(cfg, ...)
      engine.Run()
  }
  ```

## 4. Todo List

1.  [ ] Create `internal/compiler` (Interface + GCC implementation).
2.  [ ] Create `internal/vm` (QEMU wrapper).
3.  [ ] Implement `internal/seed_executor` (Local + QEMU).
4.  [ ] Update `internal/coverage` to use `internal/compiler`.
5.  [ ] Implement `internal/fuzz/engine.go` (The Main Loop).
6.  [ ] Create `cmd/defuzz/app/fuzz.go` to wire everything up.
