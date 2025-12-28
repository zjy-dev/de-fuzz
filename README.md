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
      - GOTO step 3a
   ELSE:
      - Run divergence analysis (uftrace) to find call trace difference
      - Send divergence info to LLM for refined mutation
      - GOTO step 3d
```

### Test Oracle

Dynamic testing with LLM analysis:

```
Run seed → Get feedback (return code + stdout + stderr) → LLM judges if bugs exist
```

**Note:** All compilation and execution happens directly on the host machine. Ensure you have the required toolchain (GCC, QEMU, etc.) installed and available in your system PATH.

## Implementation

### Coverage & CFG Analysis

#### 1. GCC CFG Dump

Used to select targets (basic blocks with most successors):

1. Build target compiler with `-fdump-tree-cfg-lineno` for target files
2. Collect `.cfg` files (e.g., `cfgexpand.cc.015t.cfg`)
3. Build mapping: `File:Line -> BasicBlockID` and `BasicBlockID -> SuccessorCount`
4. Query at runtime to select target BB

#### 2. gcov for Coverage Measurement

When fuzzing gcc, use gcc's gcov for coverage info generation, and gcovr for coverage stats:

1. Customize target compiler `tc` with gcov coverage compile options
2. Clean `gcovr_exec_path` (configured in compiler-isa-strategy.yaml)
3. Compile seed → generates `*.gcda` files
4. Run gcovr to generate JSON report
5. Diff with total.json to check coverage increase
6. Merge reports

### Mutation with Coverage Context

When a seed causes coverage to increase:

1. Get the coverage increase details (which functions/lines were newly covered)
2. Get the current total coverage statistics
3. Build a mutation prompt that includes:
   - The original seed source code
   - Coverage increase summary
   - Detailed coverage increase report
   - Current total coverage percentage

### Divergence Analysis

When mutation fails to cover the target, use uftrace for function-level divergence analysis:

1. Record traces for both base and mutated seeds:
   ```bash
   uftrace record -P '.*' -d trace_dir gcc -c seed.c -o /dev/null
   ```
2. Extract cc1 process ID from task.txt
3. Export call sequences (filtering noise)
4. Find first divergent function call
5. Return divergence info to LLM for refined mutation

## Module Architecture

The `internal` directory contains the core logic:

### Core Data Structures

- **`seed`**: Seed structure (Source + TestCases + Metadata). Central data type passed between modules.
- **`state`**: Global persistent state (unique IDs, global coverage stats). Supports resume.
- **`corpus`**: Manages seed queue, handling prioritization, selection, and persistence.

### Execution & Environment

- **`exec`**: Low-level wrapper around `os/exec` for shell command execution.
- **`vm`**: Abstracts execution environment: `LocalVM` (host) and `QEMUVM` (cross-architecture).
- **`compiler`**: GCC compiler wrapper with coverage instrumentation support.
- **`seed_executor`**: Orchestrates seed execution using the `vm` module.

### Analysis & Feedback

- **`coverage`**: Coverage measurement and analysis:
  - `Measure()`: Compile seed and generate coverage report
  - `HasIncreased()`: Check if coverage increased
  - `GetIncrease()`: Get detailed coverage increase info
  - `Merge()`: Incorporate new coverage into total
  - `CFGGuidedAnalyzer`: CFG-guided target selection
  - `DivergenceAnalyzer`: uftrace-based call trace analysis
- **`oracle`**: Test oracle using LLM to analyze execution results
- **`report`**: Bug report persistence in Markdown format

### LLM Integration

- **`llm`**: Unified interface for LLM providers (DeepSeek, etc.)
- **`prompt`**: Constructs prompts for LLM with:
  - ISA details and defense strategy
  - Source code and coverage information
  - Shots (example seeds)
  - Divergence analysis results

### Orchestration

- **`fuzz`**: Central engine implementing the constraint solving loop:
  1. Select target BB (CFG-guided)
  2. Build prompt with shot
  3. LLM mutation
  4. Compile and test
  5. If failed → divergence analysis → refined mutation
  6. If success → update mapping → Oracle analysis
- **`config`**: Centralized configuration using Viper

## Usage

### Prerequisites

1. **Go 1.21+** installed
2. **GCC with gcov support** (for coverage measurement)
3. **gcovr** installed (`pip install gcovr`)
4. **uftrace** installed (for divergence analysis)
5. **QEMU user-mode** (optional, for cross-architecture fuzzing)
6. **LLM API access** (e.g., DeepSeek API key)

### Configuration Files

#### 1. Main Configuration (`configs/config.yaml`)

```yaml
config:
  llm: "deepseek"
  isa: "x64"
  strategy: "canary"
  compiler:
    name: "gcc"
    version: "12.2.0"
```

#### 2. LLM Configuration (`configs/llm.yaml`)

```yaml
llms:
  - provider: "deepseek"
    model: "deepseek-coder"
    api_key: "sk-your-api-key-here"
    temperature: 0.7
```

#### 3. Compiler Configuration (`configs/gcc-v{version}-{isa}-{strategy}.yaml`)

```yaml
compiler:
  path: "/path/to/gcc/xgcc"
  gcovr_exec_path: "/path/to/gcc-build"
  source_parent_path: "/path/to/source"

targets:
  - file: "gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc"
    functions:
      - "stack_protect_classify_type"
```

### Directory Structure

```
de-fuzz/
├── configs/
│   ├── config.yaml
│   ├── llm.yaml
│   └── gcc-v12.2.0-x64-canary.yaml
├── initial_seeds/
│   └── {isa}/
│       └── {strategy}/
│           ├── understanding.md
│           └── *.seed
└── fuzz_out/
    ├── corpus/
    ├── coverage/
    ├── build/
    └── state/
```

### Commands

#### Generate Initial Seeds

```bash
./defuzz generate --count 5
```

#### Start Fuzzing

```bash
./defuzz fuzz
./defuzz fuzz --max-iterations 100 --max-new-seeds 3 --timeout 30
./defuzz fuzz --use-qemu --qemu-path qemu-aarch64 --qemu-sysroot /usr/aarch64-linux-gnu
```

### Testing

```bash
# Unit tests
go test -v -short ./internal/...

# Integration tests (requires QEMU and cross-compilers)
go test -v -tags=integration ./internal/...
```
