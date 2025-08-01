## Plan for `internal/seed_executor` Module

### 1. Objective

This module is responsible for executing a single seed within a VM. It acts as a bridge between the `fuzz` orchestrator and the `vm`. It takes a `Seed`, prepares the execution environment, runs the seed's command, and returns the result. This replaces the previous, more specific `compiler` module.

### 2. Core Component: `Executor`

-   An `Executor` interface will define the contract for executing a seed.
-   A `DefaultExecutor` struct will provide a concrete implementation.

### 3. Implementation (`executor.go`)

-   **`ExecutionResult` struct**: This will be a wrapper around `vm.ExecutionResult` to potentially add more context in the future (e.g., execution time, coverage data). For now, it can be a direct alias or a struct containing it.

-   **`Executor` interface**:
    ```go
    package executor

    import (
    	"defuzz/internal/seed"
    	"defuzz/internal/vm"
    )

    type Executor interface {
    	Execute(s *seed.Seed, v vm.VM) (*vm.ExecutionResult, error)
    }
    ```

-   **`DefaultExecutor` struct**:
    -   Implements the `Executor` interface.
    -   `NewDefaultExecutor()`: A constructor for the executor.

-   **`Execute(s *seed.Seed, v vm.VM)` method**:
    -   This is the core logic.
    -   It will take a `seed.Seed` and a `vm.VM`.
    -   It will be responsible for setting up the necessary files for the seed within the VM's file system. This means creating a temporary directory and writing the `seed.Content` to a source file (e.g., `main.c`, `main.s`).
    -   It will then execute the `seed.ExecCmd` within that directory in the VM.
    -   It will capture and return the `vm.ExecutionResult`.
    -   It will handle cleanup of the temporary files/directory within the VM.

### 4. Testing (`executor_test.go`)

-   A `mockVM` that implements the `vm.VM` interface will be created.
-   This mock will be injected into the `DefaultExecutor` during tests.
-   Tests will verify:
    -   That `Execute` correctly calls the VM's methods to set up files.
    -   That `Execute` calls `vm.Run` with the correct command from `seed.ExecCmd`.
    -   That the result from `vm.Run` is passed through correctly.
    -   That errors from the VM are handled and propagated.
    -   That cleanup operations are called.
