## Plan for `internal/exec` Module

### 1. Objective

To provide a robust, testable, and OS-agnostic interface for executing external shell commands on the host system. This module will be a low-level utility used by other parts of the application, such as the `vm` module, that need to interact with the command line.

### 2. Core Components

To make any consumer of this module testable, the core of this module will be an interface.

-   **`Executor` Interface**:
    ```go
    package exec

    // ExecutionResult holds the outcome of a command execution.
    type ExecutionResult struct {
        Stdout   string
        Stderr   string
        ExitCode int
    }

    // Executor defines the interface for running external commands.
    type Executor interface {
        Run(command string, args ...string) (*ExecutionResult, error)
    }
    ```

-   **`CommandExecutor` Struct**:
    -   A concrete implementation of the `Executor` interface.
    -   This struct will contain the actual logic for using the `os/exec` package to run commands.
    -   It will be stateless.

### 3. Implementation (`exec.go`)

-   **`NewCommandExecutor()`**: A constructor that returns a new `CommandExecutor`.
-   **`Run(command string, args ...string)`**:
    -   This method will create a new `exec.Cmd`.
    -   It will capture `stdout` and `stderr` using buffers (`bytes.Buffer`).
    -   It will run the command and wait for it to complete.
    -   It will parse the exit code, even in cases where the command exits with an error.
    -   It will populate and return an `ExecutionResult` struct.

### 4. Testing (`exec_test.go`)

-   The `CommandExecutor` will be tested by running simple, non-destructive shell commands (e.g., `echo`, `ls`) and asserting that:
    -   `Stdout` is captured correctly.
    -   `Stderr` is captured correctly.
    -   The `ExitCode` is captured correctly for both successful and failing commands.
-   No mocking is required to test the `CommandExecutor` itself, as it's the boundary to the operating system. The key is that other modules will mock the `Executor` interface.
