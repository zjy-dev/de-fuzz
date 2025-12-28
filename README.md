# DeFuzz

A LLM-driven constraint solving fuzzer for software defense strategies.

Written in golang.

## Idea

DeFuzz is inspired by the HLPFuzz paper. It uses **LLM-driven progressive constraint solving** to systematically explore hard-to-reach code paths in compilers' defense implementations.

### Core Concept: LLM-Based Constraint Solving

Unlike traditional coverage-guided fuzzing (random mutation), DeFuzz actively selects targets and guides the LLM to generate inputs that satisfy specific path constraints.

### Seed Definition

A seed is a self-contained test case consisting of:

```go
// internal/seed/seed.go
type Seed struct {
	ID        uint64      // Unique identifier
	Content   string      // C source code
	TestCases []TestCase  // Multiple test cases
	Meta      SeedMetadata
}

type SeedMetadata struct {
	FilePath   string    // Path to seed directory
	ParentID   uint64    // Parent seed ID (0 for initial seeds)
	Depth      int       // Generation depth
	State      string    // Seed state
}
```

### Algorithm Flowchart

```
      ┌──────────────────────────────────────────────────────────────────┐
      │  1. Maintain mapping: Line -> FirstSeedID that covered it        │
      └──────────────────────────────────────────────────────────────────┘
                                       ↓
      ┌──────────────────────────────────────────────────────────────────┐
      │  2. Run initial seeds, establish mapping, persist to disk        │
      └──────────────────────────────────────────────────────────────────┘
                                       ↓
                               ┌───────────────┐
                               │ Constraint    │
                               │   Solving     │
                               │   Loop        │
                               └───────────────┘
                                       ↓
      ┌──────────────────────────────────────────────────────────────────┐
      │  3. Select Target: Uncovered BB with most successors             │
      └──────────────────────────────────────────────────────────────────┘
                                       ↓
      ┌──────────────────────────────────────────────────────────────────┐
      │  4. Build Prompt:                                                │
      │     - Target function code (annotated: covered/uncovered/target) │
      │     - Shot: Seed covering target's predecessor                   │
      └──────────────────────────────────────────────────────────────────┘
                                       ↓
      ┌──────────────────────────────────────────────────────────────────┐
      │  5. LLM Mutation: Mutate shot to cover target BB                 │
      └──────────────────────────────────────────────────────────────────┘
                                       ↓
      ┌──────────────────────────────────────────────────────────────────┐
      │  6. Compile and Test Seed                                        │
      └──────────────────────────────────────────────────────────────────┘
                                       ↓
                       ┌───────────────┴───────────────┐
                       ↓                               ↓
             ┌───────────────────┐           ┌───────────────────┐
             │  Covered Target?  │           │   Not Covered     │
             └───────────────────┘           └───────────────────┘
                       ↓                               ↓
             ┌───────────────────┐           ┌─────────────────────┐
             │  Update Mapping   │           │ Divergence (uftrace)│
             │  Feed to Oracle   │           │ Locate call trace   │
             │  Return to Step 3 │           │ Divergent function  │
             └───────────────────┘           └─────────────────────┘
                                                       ↓
                                           ┌────────────────────────┐
                                           │ Send divergence to LLM │
                                           │ Refine mutation        │
                                           │ (Return to Step 6)     │
                                           └────────────────────────┘
```

### Algorithm Overview

DeFuzz implements the following constraint solving process:

1. **Maintain Mapping**: Track which seed first covered each line of code.

2. **Initialize**: Run initial seeds to establish the mapping, then persist seeds to disk.

3. **Target Selection**: In each loop, select the uncovered basic block with the most successors (CFG-guided targeting).

4. **Prompt Construction**:
   - Provide the target function code with annotations (covered/uncovered/target lines)
   - Include the seed that covered the target's predecessor as a shot (example)

5. **LLM Mutation**: Send the prompt to LLM, asking it to mutate the shot to cover the target BB.

6. **Compile and Test**: Compile the mutated seed and test if it covers the target.

7. **Coverage Check**:
   - If covered: Update mapping, feed to Oracle, persist only if new coverage gained
   - If not covered: Run divergence analysis (uftrace), send results to LLM for refined mutation

8. **Divergence Analysis**: Compare call traces of base and mutated seeds to find where they diverge, then send this information back to LLM for another mutation attempt.

### Test Oracle (Plugin-Based)

Oracles are implemented as plugins. Prefer hand-written traditional oracles over LLM-based ones.

#### Traditional Oracle (Canary Example)

For stack canary, a binary search oracle detects bypasses:

```
Run with buffer size N (fill with 'A' = 0x41)
  - Exit 0: Normal execution
  - Exit 134 (SIGABRT): Canary caught overflow (SAFE)
  - Exit 139 (SIGSEGV): Return address modified before check (BUG!)

Binary search in [0, MaxBufferSize] to find minimum crash size:
  - Min crash exits with 139 -> Canary bypass detected
  - Min crash exits with 134 -> Canary working correctly
```

#### LLM Oracle (Fallback)

When no traditional oracle exists, use LLM-based analysis:

```
Run seed -> Get feedback (exit code + stdout + stderr) -> LLM judges if bugs exist
```

**Note:** All compilation and execution happens directly on the host machine. Ensure you have the required toolchain (GCC, QEMU, etc.) installed and available in your system PATH.
