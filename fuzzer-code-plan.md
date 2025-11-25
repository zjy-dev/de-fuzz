# Fuzzer Code Implementation Plan

Based on `fuzzer-plan.md`, this document outlines the code structure, interfaces, and data types required to implement the robust seed storage, lineage tracking, and resume functionality.

## 1. Architecture Overview

The system will be divided into three main components to handle data persistence and fuzzing state:

1.  **`internal/seed`**: Defines the core data structures (`Seed`, `Metadata`) and handles file I/O for individual seed files (parsing/generating filenames).
2.  **`internal/state`**: Manages the global fuzzing state (`global_state.json`), including ID allocation and global coverage metrics.
3.  **`internal/corpus`**: Orchestrates the seed queue, manages the `fuzz_out` directory structure, and handles the "Resume" logic by scanning existing files.

## 2. Module: `internal/seed`

This module needs to be expanded to support metadata and the specific filename convention.

### 2.1 Data Structures

```go
package seed

import "time"

// SeedState represents the processing status of a seed.
type SeedState string

const (
    SeedStatePending   SeedState = "PENDING"
    SeedStateProcessed SeedState = "PROCESSED"
    SeedStateCrash     SeedState = "CRASH"
    SeedStateTimeout   SeedState = "TIMEOUT"
)

// Metadata contains all meta-information about a seed.
type Metadata struct {
    // Basic Info
    ID        uint64    `json:"id"`
    FilePath  string    `json:"file_path"` // Relative path in corpus
    FileSize  int64     `json:"file_size"`
    CreatedAt time.Time `json:"created_at"`

    // Lineage
    ParentID uint64 `json:"parent_id"`
    Depth    int    `json:"depth"`

    // State
    State SeedState `json:"state"`

    // Metrics
    OldCoverage uint64 `json:"old_cov"`  // Basis points (e.g. 12.34% = 1234)
    NewCoverage uint64 `json:"new_cov"`
    CovIncrease uint64 `json:"cov_incr"`
    ExecTimeUs  int64  `json:"exec_us"`
}

// Seed represents a single unit of work for the fuzzer.
type Seed struct {
    Meta      Metadata
    Content   string     // C Source Code
    TestCases []TestCase // JSON Test Cases
}
```

### 2.2 Filename Utilities

We need utilities to generate and parse the specific filename format:
`id-[ID]-src-[ParentID]-cov-[CovIncrement]-[Hash].seed`

```go
// NamingStrategy defines how seed filenames are generated and parsed.
type NamingStrategy interface {
    // GenerateFilename creates the filename string based on metadata.
    GenerateFilename(meta Metadata) string

    // ParseFilename extracts metadata (ID, ParentID, CovIncr) from a filename.
    ParseFilename(filename string) (Metadata, error)
}

// DefaultNamingStrategy implements the format defined in fuzzer-plan.md.
type DefaultNamingStrategy struct{}
```

### 2.3 Storage Interface

Refactor `SaveSeed` to accept metadata and use the naming strategy.

```go
// Saver handles writing the seed content to disk.
// It should write the .seed file format:
// <C Source Code>
// // ||||| JSON_TESTCASES_START |||||
// <JSON Test Cases>
func Save(dir string, s *Seed, namer NamingStrategy) (string, error) {
    // Implementation details:
    // 1. Generate filename using namer
    // 2. Combine Content and TestCases
    // 3. Write to dir/filename
    // 4. Return full path
}
```

## 3. Module: `internal/state` (New)

This module manages the `global_state.json`.

### 3.1 Data Structures

```go
package state

// QueueStats holds simple statistics about the queue.
type QueueStats struct {
    PoolSize       int `json:"pool_size"`
    ProcessedCount int `json:"processed_count"`
}

// GlobalState represents the persistent state of the fuzzing session.
type GlobalState struct {
    LastAllocatedID  uint64     `json:"last_allocated_id"`
    CurrentFuzzingID uint64     `json:"current_fuzzing_id"`
    TotalCoverage    uint64     `json:"total_coverage"` // In basis points
    Stats            QueueStats `json:"queue_stats"`
}
```

### 3.2 State Manager Interface

```go
// Manager handles the persistence and modification of the global state.
type Manager interface {
    // Load reads the state from disk.
    Load() error

    // Save writes the state to disk.
    Save() error

    // NextID increments and returns the next unique seed ID.
    NextID() uint64

    // UpdateCurrentID sets the ID currently being fuzzing.
    UpdateCurrentID(id uint64)

    // UpdateCoverage updates the global coverage metric.
    UpdateCoverage(newCov uint64)

    // GetState returns a copy of the current state.
    GetState() GlobalState
}
```

## 4. Module: `internal/corpus` (New)

This module acts as the high-level manager for seeds, integrating `seed` and `state`.

### 4.1 Directory Structure

The `Corpus` manager will enforce the following structure:

```text
fuzz_out/
├── corpus/    # .seed files
├── metadata/  # .json files (optional/sidecar)
└── state/     # global_state.json
```

### 4.2 Corpus Manager Interface

```go
package corpus

import (
    "github.com/zjy-dev/de-fuzz/internal/seed"
    "github.com/zjy-dev/de-fuzz/internal/state"
)

// Manager manages the lifecycle of seeds on disk and in memory.
type Manager interface {
    // Initialize prepares the directory structure.
    Initialize() error

    // Recover scans the corpus directory to rebuild the in-memory queue
    // and restore the GlobalState if necessary.
    Recover() error

    // Add persists a new seed to disk and adds it to the processing queue.
    // It handles ID allocation via the State Manager.
    Add(s *seed.Seed) error

    // Next retrieves the next seed to process from the queue.
    Next() (*seed.Seed, error)

    // ReportResult updates a seed's metadata after fuzzing (e.g., execution time, new coverage)
    // and updates the GlobalState.
    ReportResult(id uint64, result FuzzResult) error
}

// FuzzResult contains the outcome of a fuzzing iteration.
type FuzzResult struct {
    State       seed.SeedState
    ExecTimeUs  int64
    NewCoverage uint64
}
```

## 5. Implementation Steps

1.  **Refactor `internal/seed`**:

    - Create `metadata.go` to define `Metadata` and `SeedState`.
    - Implement `DefaultNamingStrategy` in `naming.go`.
    - Update `storage.go` to use the new naming strategy and file format.

2.  **Implement `internal/state`**:

    - Create `state.go` with `GlobalState` struct and `Manager` implementation (JSON file backed).

3.  **Implement `internal/corpus`**:

    - Create `manager.go`.
    - Implement `Recover()` logic:
      - Read `global_state.json`.
      - Scan `fuzz_out/corpus/`.
      - Parse filenames to reconstruct the priority queue.
      - Verify consistency between file count and `LastAllocatedID`.

4.  **Integration**:
    - Update the main fuzzer loop (`cmd/defuzz` or `internal/fuzz`) to initialize `state.Manager` and `corpus.Manager`.
    - Replace direct calls to `seed.SaveSeed` with `corpus.Add()`.

## 6. Notes

- **Performance**: The `Recover` phase might be slow if the corpus is huge. Consider caching the file list or only scanning if `global_state.json` is missing or corrupted.
- **Concurrency**: The `GlobalState` and `Corpus` operations should be thread-safe if we plan to run parallel fuzzing instances in the future.
- **Metadata Storage**: While the plan mentions a `metadata/` directory, for now, we can rely on the filename for critical info and the `.seed` file content for the rest. We can implement the separate JSON metadata dump in `ReportResult` if needed for detailed analysis.
