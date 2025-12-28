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

### Algorithm

```
+------------------------------------------------------------------+
|  1. Maintain mapping: Line -> FirstSeedID that covered it        |
+------------------------------------------------------------------+
                                  |
                                  v
+------------------------------------------------------------------+
|  2. Run initial seeds, establish mapping, persist to disk       |
+------------------------------------------------------------------+
                                  |
                                  v
                        +-------------------+
                        | Constraint Solving|
                        |      Loop         |
                        +-------------------+
                                  |
                                  v
+------------------------------------------------------------------+
|  3. Select Target: Uncovered BB with most successors             |
+------------------------------------------------------------------+
                                  |
                                  v
+------------------------------------------------------------------+
|  4. Build Prompt:                                               |
|     - Target function code (annotated: covered/uncovered/target) |
|     - Shot: Seed covering target's predecessor                   |
+------------------------------------------------------------------+
                                  |
                                  v
+------------------------------------------------------------------+
|  5. LLM Mutation: Mutate shot to cover target BB                 |
+------------------------------------------------------------------+
                                  |
                                  v
+------------------------------------------------------------------+
|  6. Compile and Test Seed                                        |
+------------------------------------------------------------------+
                                  |
                    +-------------+-------------+
                    |                           |
                    v                           v
           +----------------+          +----------------+
           | Covered Target?|          | Not Covered    |
           +----------------+          +----------------+
                    |                           |
           +--------+--------+                |
           |                 |                v
           v                 v        +----------------+
    +------------+   +------------+    | Divergence     |
    | Update     |   | Feed to    |    | Analysis       |
    | Mapping    |   | Oracle     |    | (uftrace)      |
    +------------+   +------------+    +----------------+
           |                 |                |
           +--------+--------+                |
                    |                         |
                    v                         v
           +------------------+    +----------------------+
           | No Coverage Gain?|    | Send to LLM for     |
           | -> Skip Persist  |    | Refined Mutation    |
           | Else -> Persist  |    +----------------------+
           +------------------+              |
                    |                         |
                    +-----------+-------------+
                                |
                                v
                    +-----------------------+
                    | Return to Step 6      |
                    +-----------------------+
```

### Fuzzing Algorithm (Constraint Solving Loop)

For each defense strategy and ISA:

```
1. Maintain a mapping: Line -> FirstSeedID that covered it

2. Run initial seeds, establish mapping, persist seeds to disk

3. CONSTRAINT SOLVING LOOP:
   a. Select target: Uncovered basic block with most successors (CFG-guided)
   b. Build prompt:
      - Target function code with annotations (covered/uncovered/target lines)
      - Shot: Seed that covered the target's predecessor
   c. Send to LLM: Mutate shot to cover target BB
   d. Compile and test mutated seed

   IF mutated seed covers target BB:
      - Update mapping
      - Feed to Oracle
      - If no new coverage -> skip persist
      - If has new coverage -> persist
      - Return to step 3a
   ELSE:
      - Run divergence analysis (uftrace) to find call trace difference
      - Send divergence info to LLM for refined mutation
      - Return to step 3d
```

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
  - If min crash exits with 139 -> Canary bypass detected
  - If min crash exits with 134 -> Canary working correctly
```

#### LLM Oracle (Fallback)

When no traditional oracle exists, use LLM-based analysis:

```
Run seed -> Get feedback (exit code + stdout + stderr) -> LLM judges if bugs exist
```

**Note:** All compilation and execution happens directly on the host machine. Ensure you have the required toolchain (GCC, QEMU, etc.) installed and available in your system PATH.
