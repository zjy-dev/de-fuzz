# Plan: Remove Container-Based Execution

**Goal:** Refactor the project to remove the dependency on a containerized execution environment (Podman). All fuzzing, compilation, and execution tasks will run directly on the host machine. This simplifies the setup and workflow.

---

## 1. Deletion Plan

The following files and directories, which are exclusively for container management, will be deleted:

-   **`internal/vm/`**: The entire `vm` package, including:
    -   `vm.go`
    -   `vm_test.go`
    -   `vm_integration_test.go`
    -   `plan.md`
    -   `README.md`
-   **`scripts/build-container.sh`**: The script for building the Podman container.
-   **`docker/Dockerfile.fuzzing`**: The Dockerfile used to define the container image.

---

## 2. Modification Plan

The following files will be modified to replace containerized execution with local execution.

### `internal/seed_executor/executor.go`

-   **Current:** This package likely depends on the `internal/vm` package to execute commands inside the container.
-   **Change:**
    -   Remove any dependencies on the `vm` package.
    -   Refactor the `Execute` function (or equivalent) to use the `internal/exec` package directly.
    -   Commands that were previously run with `podman exec` will now be run as standard shell commands on the host.
    -   The logic for preparing the environment and running the seed's command will be preserved, but adapted for local execution.

### `internal/seed_executor/qemu.go`

-   **Current:** May have dependencies on the `vm` package.
-   **Change:**
    -   If this file is used for running seeds under QEMU, its core logic will be kept.
    -   Remove any `vm` package dependencies. The QEMU command will be executed directly on the host via the `internal/exec` package.

### `cmd/defuzz/app/generate.go`

-   **Current:** The command's help text references the container setup script.
-   **Change:**
    -   In `NewGenerateCommand()`, find the `Long` description string.
    -   Remove the final note: `Note: Use './scripts/build-container.sh' to set up the fuzzing environment container.`

---

## 3. Documentation Update Plan

### `README.md`

-   **Current:** The documentation describes a workflow centered around Podman and QEMU within a container.
-   **Change:**
    -   Remove the "Prepare environment with podman and qemu" step from the "Fuzzing Algorithm" section.
    -   Update the workflow to state that fuzzing tools (GCC, QEMU, etc.) must be installed and available in the host's `PATH`.
    -   Remove any sections that describe how to build or use the container.

---

## 4. Verification Steps

1.  After deleting and modifying the files, run `go mod tidy` to clean up dependencies.
2.  Run all unit and integration tests with `go test ./...` to ensure no regressions were introduced.
3.  Execute the `defuzz generate` command. Verify that it completes successfully without any container-related errors.
4.  Manually inspect the `initial_seeds` directory to confirm that seeds are generated correctly.
5.  Proceed with the implementation of the `defuzz fuzz` command, ensuring it uses the refactored, local-execution-based `seed_executor`.
