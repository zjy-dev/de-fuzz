# DeFuzz Code Plan

This document outlines the comprehensive architectural plan for the DeFuzz application. It details the responsibilities of each module and the interactions between them.

## 1. Core Principle: Separation of Concerns

The application is divided into distinct modules, each with a single, well-defined responsibility. This is achieved through the extensive use of interfaces for dependencies, allowing for modularity, testability, and swappable implementations.

## 2. Module Breakdown (Logical Dependency Order)

### `internal/config`

- **Responsibility**: Loads and manages all external configurations (e.g., `llm.yaml`).
- **Core Component**: A generic `Load` function that uses `viper` to parse YAML files from the `/configs` directory into Go structs.
- **Key Interface**: None. Provides concrete structs.

### `internal/exec`

- **Responsibility**: Provides a robust and testable interface for executing external shell commands.
- **Core Component**: `CommandExecutor` which is a concrete implementation of the `Executor` interface.
- **Key Interface**: `Executor`, allowing consumers like the `vm` module to be tested with mocks.

### `internal/vm`

- **Responsibility**: Manages a sandboxed environment for compiling and executing seeds.
- **Core Component**: `PodmanVM`, which uses the `exec.Executor` to run `podman` commands.
- **Key Interface**: `VM`, which abstracts container operations like `Create`, `Run`, and `Stop`.

### `internal/llm`

- **Responsibility**: Abstracts all interactions with Large Language Models.
- **Core Component**: `DeepSeekClient` (or other provider clients) that implements the `LLM` interface.
- **Key Interface**: `LLM`, which defines a foundational `GetCompletion` method and higher-level methods like `Generate` and `Mutate`.

### `internal/prompt`

- **Responsibility**: Centralizes all prompt engineering.
- **Core Component**: `Builder`, which constructs specific prompts for different tasks (understanding, generation, mutation, analysis).
- **Key Interface**: None. It is a concrete utility.

### `internal/seed_executor`

- **Responsibility**: Executes a seed within the VM. It prepares the environment, runs the seed's command, and returns the result.
- **Core Component**: `DefaultExecutor`, which implements the `Executor` interface.
- **Key Interface**: `Executor`, which defines the `Execute` method.

### `internal/seed`

- **Responsibility**: Manages the lifecycle of seeds, including their data structure, in-memory pool, and persistence to disk.
- **Core Component**: `Seed` struct, `InMemoryPool` (implements `Pool`), and storage functions (`SaveSeed`, `LoadSeeds`, etc.).
- **Key Interface**: `Pool`, for managing the collection of active seeds.

### `internal/analysis`

- **Responsibility**: Analyzes the result of a seed's execution to determine if a bug was found.
- **Core Component**: `LLMAnalyzer`, which uses the `prompt` and `llm` modules to make a determination.
- **Key Interface**: `Analyzer`, which defines the `AnalyzeResult` method.

### `internal/report`

- **Responsibility**: Generates and saves detailed bug reports.
- **Core Component**: `FileReporter`, which creates Markdown reports from `analysis.Bug` objects.
- **Key Interface**: `Reporter`, which defines the `Save` method.

### `internal/fuzz`

- **Responsibility**: The central orchestrator. Manages the high-level workflow for `generate` and `fuzz` modes.
- **Core Component**: `Fuzzer`, which holds instances of all other modules and wires them together.
- **Key Interface**: None. It is the top-level internal component.

## 3. High-Level Workflow (`fuzz` mode)

1.  **Initialization**: The `main` function in `cmd/defuzz` instantiates all modules (reading `config`, creating `executors`, `vms`, `llms`, etc.) and injects them into a new `Fuzzer` instance.
2.  **VM Setup**: `Fuzzer` calls `vm.Create()` to start the container.
3.  **Load Context**: `Fuzzer` calls `seed.LoadUnderstanding()` and `seed.LoadSeeds()` to prepare the initial state.
4.  **Fuzzing Loop**:
    a. `Fuzzer` gets the next seed from the `seed.Pool`.
    b. `Fuzzer` calls `compiler.Compile(seed, vm)`.
    c. If compilation is successful, `Fuzzer` calls `vm.Run()` to execute the binary.
    d. `Fuzzer` calls `analyzer.AnalyzeResult()` with the execution feedback.
    e. If a bug is found, `Fuzzer` calls `reporter.Save()` and then uses the `llm` to `Mutate` the buggy seed into a new one, which is added to the pool.
5.  **Cleanup**: `Fuzzer` calls `vm.Stop()` to destroy the container.
