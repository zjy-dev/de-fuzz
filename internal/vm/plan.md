## Plan for `internal/vm` Module

### 1. Objective

To provide a clean, OS-agnostic interface for managing a sandboxed environment where seeds can be safely compiled and executed. The concrete implementation will use Podman to create, manage, and destroy containers, abstracting away the specific shell commands from the rest of the application.

### 2. Core Component: `PodmanVM`

-   A `PodmanVM` struct will implement the `VM` interface.
-   It will hold configuration such as the container image name and the ID of the running container.
-   It will depend on an `Executor` interface (to be defined in the `exec` package) to run host commands. This allows for mocking during tests.

### 3. `PodmanVM` Implementation (`vm.go`)

-   **`NewPodmanVM(image string, executor exec.Executor) *PodmanVM`**: The constructor will accept the container image and an executor.
-   **`Create()`**:
    -   Generates and executes a `podman run -d --rm ...` command.
    -   The `--rm` flag will ensure the container is cleaned up on stop.
    -   It will mount the project's working directory into the container to make seeds accessible.
    -   It will store the container ID returned by the `podman run` command.
-   **`Run(command ...string)`**:
    -   Generates and executes a `podman exec <container_id> <command>` command.
    -   It will capture `stdout`, `stderr`, and the exit code from the `exec` result and return them in a `vm.ExecutionResult`.
-   **`Stop()`**:
    -   Generates and executes a `podman stop <container_id>` command.

### 4. Dependency Refactoring: `internal/exec`

To make the `vm` module testable, the `exec` module must be refactored to use an interface.

-   **`exec.go`**:
    -   Define an `Executor` interface:
        ```go
        type Executor interface {
            Run(command string, args ...string) (*ExecutionResult, error)
        }
        ```
    -   Define a `CommandExecutor` struct that implements the `Executor` interface and contains the actual `os/exec` logic.
    -   `NewCommandExecutor()` will be its constructor.
-   **`vm.go`**:
    -   The `PodmanVM` will hold an `exec.Executor` instance.
-   **`vm_test.go`**:
    -   A `mockExecutor` will be created that implements the `exec.Executor` interface.
    -   This mock will be injected into the `PodmanVM` during tests to assert that the correct `podman` commands are being generated and "executed" without actually running any shell commands.

### 5. Testing (`vm_test.go`)

-   Tests will be written to verify that each method of `PodmanVM` (`Create`, `Run`, `Stop`) generates the expected `podman` command string.
-   The `mockExecutor` will allow tests to simulate different outcomes (success, failure, specific output) from the `podman` commands and assert that the `PodmanVM` handles them correctly.
