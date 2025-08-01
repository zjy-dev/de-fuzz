## Plan for `internal/seed` Module

### 1. Objective

This module is responsible for the complete lifecycle of a seed. It defines the core `Seed` data structure, manages the in-memory `Pool` of seeds used during the fuzzing loop, and handles all file system interactions for persisting seeds.

### 2. File Structure

To maintain a clean separation of concerns, the module is split into two files:

-   **`seed.go`**: Contains the primary data structures (`Seed`, `Pool` interface) and the in-memory pool implementation (`InMemoryPool`).
-   **`storage.go`**: Contains all logic for reading from and writing to the file system.

### 3. Core Components

#### a. `seed.go`

-   **`SeedType` enum**: To represent the source language of the seed.
    ```go
    // SeedType defines the type of a seed's source content.
    type SeedType string

    const (
        // SeedTypeGo represents a Go source file.
        SeedTypeGo SeedType = "go"
        // SeedTypeC represents a C source file.
        SeedTypeC SeedType = "c"
        // SeedTypeAsm represents an assembly source file.
        SeedTypeAsm SeedType = "asm"
    )
    ```

-   **`Seed` struct**: The data representation of a single fuzzer input. The `ExecCmd` field makes each seed self-contained and executable.
    ```go
    type Seed struct {
        ID      string
        Type    SeedType
        Content string
        ExecCmd string // The command to compile and/or run the seed.
    }
    ```
-   **`Pool` interface**: An abstraction for the collection of seeds being actively fuzzed.
    ```go
    type Pool interface {
        Add(s *Seed)
        Next() *Seed
        Len() int
    }
    ```
-   **`InMemoryPool` struct**: A concrete, slice-based implementation of the `Pool` interface.

#### b. `storage.go`

This file provides functions to interact with the directory structure defined in the project's `README.md`: `initial_seeds/<isa>/<defense_strategy>/`.

-   **`SaveSeed(basePath string, s *Seed) error`**: Creates a new directory (e.g., `<basePath>/<id>_<type>`) and saves the source file (e.g., `source.c`) and a file containing the execution command (`exec.sh`).
-   **`LoadSeeds(basePath string) (Pool, error)`**: Scans the `basePath` for seed directories, reads their contents, constructs `Seed` objects, and returns them in a ready-to-use `Pool`.

### 4. Testing (`seed_test.go`)

-   The `InMemoryPool` will be tested for its core logic (add, next, length).
-   The storage functions will be tested against a temporary file system created with `os.MkdirTemp`. Tests will cover:
    -   Saving a seed and verifying the directory and file creation.
    -   Loading a directory of seeds and ensuring the `Pool` is populated correctly.
    -   Handling of edge cases like empty or non-existent directories.
