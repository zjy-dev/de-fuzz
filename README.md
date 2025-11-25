# DeFuzz

A fuzzer for software defense startegy.

Written in golang.

## Idea

### Seed Definition:

A seed is a self-contained test case consisting of:

```go
// internal/seed/seed.go
type Seed struct {
	ID        string // Unique identifier for the seed
	Content   string // C source code
	TestCases []TestCase // Multiple TestCases for one seed
}
```

The compile commands are mannually written.

### Mutation Strategy

Based on Coverage increasement.

### Test Oracle

Dynamical Testing.

Run seed -> get feedback(return code + stdout + stderr) -> let LLM judge if there're bugs.

### Fuzzing Algorithm

For each defense startegy and ISA:

1. Build initial prompt `ip`:

   - current environment and toolchain
   - manually summarize defense startegy and stack layout of the ISA
   - manually summarize pesudo-code of the compiler source code about that startegy and ISA
   - also reserve source code as an "attachment" below

2. Feed `ip` to llm and store its "understanding" as memory
   <!-- if llm does't understand your demands, then how to fuzz with llm? -->

3. Initialize seed pool:

   - let llm generate initial seed(s)
   - adjust init seeds mannualy

4. Pop seed `s` from seed pool

5. Compile `s` and record coverage info

   - if coverage rate increased then mutate `s` to `s'` and push `s'` to seed pool

6. Oracle(`s`):
   - record if found a bug

**Note:** All compilation and execution happens directly on the host machine. Ensure you have the required toolchain (GCC, QEMU, etc.) installed and available in your system PATH.

## Implementation

### Coverage

#### 1. GCC

When fuzzing gcc, use gcc's gcov for coverage info generation, and gcovr for coverage info statistics.

The simplified workflow is below:

1. Customize target compiler `tc` with gcov coverage compile options
   Ensure `tc` only produce _.gcda/_.gcno files for specific files and functions
2. Clean `gcovr_exec_path` (configured in compiler-isa-strategy.yaml)
3. Use `tc` to compile a <seed>, this will generate \*.gcda files needed
4. `cd gcovr_exec_path(configured in compiler-isa-strategy.yaml) && gcovr --exclude '.*\.(h|hpp|hxx)$' --gcov-executable "gcov-14 --demangled-names"  -r .. --json-pretty --json /root/project/de-fuzz/cov/gcc/<seed>.json`
5. Diff with total.json(if exist) to see if there're coverage increase in <seed> using https://github.com/zjy-dev/gcovr-json-util/v2, that go project exposed some apis. Document is below:

```go
import "github.com/zjy-dev/gcovr-json-util/v2/pkg/gcovr"

// Parse coverage reports
baseReport, err := gcovr.ParseReport("/root/project/de-fuzz/cov/gcc/total.json")
if err != nil {
    log.Fatal(err)
}

newReport, err := gcovr.ParseReport("/root/project/de-fuzz/cov/gcc/<seed>.json")
if err != nil {
    log.Fatal(err)
}

// Apply filtering
filterConfig, err := gcovr.ParseFilterConfig("/root/project/de-fuzz/configs/gcc-x64-canary.yaml")
if err != nil {
    log.Fatal(err)
}
baseReport = gcovr.ApplyFilter(baseReport, filterConfig)
newReport = gcovr.ApplyFilter(newReport, filterConfig)

// Compute coverage increase
// if len(report) == 0 then nothing has increased
report, err := gcovr.ComputeCoverageIncrease(baseReport, newReport)
if err != nil {
    log.Fatal(err)
}

// (Optional)Format and display results
output := gcovr.FormatReport(report)
fmt.Print(output)
```

if total.json not exist, then copy <seed>.json as total.json, and coverage is considered to be increased.

5. Merge <seed>.json and total.json using `mv total.json tmp.json && gcovr --json-pretty --json  -a tmp.json -a <seed>.json -o total.json && rm tmp.json`

## Module Architecture

The `internal` directory contains the core logic, organized into modular components:

### Core Data Structures

- **`seed`**: Defines the `Seed` structure (Source + TestCases + Metadata). It is the central data type passed between modules. Also provides file I/O utilities for seed persistence.
- **`state`**: Manages the global persistent state of the fuzzing session (e.g., unique IDs, global coverage stats). Supports resume functionality.
- **`corpus`**: Manages the queue of seeds to be fuzzed, handling prioritization, selection, and persistence. Integrates with `state` for ID allocation and with `seed` for storage.

### Execution & Environment

- **`exec`**: A low-level wrapper around `os/exec` to facilitate testing and mocking of shell commands.
- **`vm`**: Abstracts the execution environment. Implementations include `LocalVM` (host execution) and `QEMUVM` (user-mode emulation for cross-architecture fuzzing).
- **`compiler`**: Abstracts the compilation process. `GCCCompiler` handles invoking GCC with specific flags and coverage instrumentation.
- **`seed_executor`**: Orchestrates the execution of a single seed. It uses the `vm` module to run the compiled binary against defined test cases.

### Analysis & Feedback

- **`coverage`**: Handles coverage measurement. It parses reports (e.g., from `gcovr`) and determines if a seed has increased code coverage.
- **`oracle`**: The test oracle that determines if a seed execution has found a bug. `LLMOracle` uses an LLM to analyze execution results (crashes, unexpected output, etc.) and identify vulnerabilities.
- **`report`**: Handles the persistence of bug reports in Markdown format.

### LLM Integration

- **`llm`**: Provides a unified interface for Large Language Models (e.g., DeepSeek). Handles generation, mutation, and analysis requests.
- **`prompt`**: Constructs context-aware prompts for the LLM, incorporating ISA details, defense strategies, and source code.

### Orchestration

- **`fuzz`**: The central engine that ties everything together. It implements the main loop:
  1.  Pick seed from `corpus`.
  2.  `compiler` -> Binary.
  3.  `seed_executor` -> Result.
  4.  `coverage` -> Feedback.
  5.  `oracle` -> Bug detection.
  6.  `llm` -> Mutation (if interesting).
- **`config`**: Centralized configuration management using Viper.

## Usage

This section describes how to configure and run DeFuzz to fuzz a specific ISA and defense strategy.

### Prerequisites

Before running DeFuzz, ensure you have:

1. **Go 1.21+** installed
2. **GCC with gcov support** (for coverage measurement)
3. **gcovr** installed (`pip install gcovr`)
4. **QEMU user-mode** (optional, for cross-architecture fuzzing)
5. **LLM API access** (e.g., DeepSeek API key)

### Configuration Files

DeFuzz uses a layered configuration system with three main configuration files in the `configs/` directory:

#### 1. Main Configuration (`configs/config.yaml`)

This file specifies the target ISA, defense strategy, compiler info, and which LLM provider to use:

```yaml
config:
  # LLM provider name (must match an entry in llm.yaml)
  llm: "deepseek"

  # Target ISA (e.g., x64, aarch64, riscv64)
  isa: "x64"

  # Defense strategy to fuzz (e.g., canary, aslr, cfi)
  strategy: "canary"

  # Compiler identification (used to locate compiler config file)
  compiler:
    name: "gcc"
    version: "12.2.0"
```

The `isa` and `strategy` values determine:

- Where initial seeds are loaded from: `initial_seeds/{isa}/{strategy}/`
- Which compiler config file to load: `configs/{name}-v{version}-{isa}-{strategy}.yaml`

#### 2. LLM Configuration (`configs/llm.yaml`)

This file contains LLM provider configurations. You can define multiple providers:

```yaml
llms:
  - provider: "deepseek"
    model: "deepseek-coder"
    api_key: "sk-your-api-key-here" # Replace with your actual API key
    endpoint: "" # Leave empty for default endpoint
    temperature: 0.7

  - provider: "openai"
    model: "gpt-4"
    api_key: "sk-your-openai-key"
    endpoint: ""
    temperature: 0.7
```

**Important**: Keep your API keys secure. Consider using environment variables in production.

#### 3. Compiler Configuration (`configs/{compiler}-v{version}-{isa}-{strategy}.yaml`)

This file contains compiler-specific settings for the target combination. The filename follows the pattern:
`{compiler.name}-v{compiler.version}-{isa}-{strategy}.yaml`

Example: `configs/gcc-v12.2.0-x64-canary.yaml`

```yaml
compiler:
  # Path to the compiler executable (can be a custom build with coverage support)
  path: "/root/fuzz-coverage/gcc-build-selective/gcc/xgcc"

  # Working directory for gcovr execution (where .gcda/.gcno files are generated)
  gcovr_exec_path: "/root/fuzz-coverage/gcc-build-selective"

  # Parent path for source code
  source_parent_path: "/root/fuzz-coverage"

# Target functions for fine-grained coverage tracking
# Used by gcovr-json-util to filter coverage data
targets:
  - file: "gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc"
    functions:
      - "stack_protect_classify_type"
      - "expand_one_stack_var_at"
```

### Directory Structure

```
de-fuzz/
├── configs/
│   ├── config.yaml                        # Main configuration
│   ├── llm.yaml                           # LLM provider configs
│   └── gcc-v12.2.0-x64-canary.yaml       # Compiler-specific config
├── initial_seeds/
│   └── {isa}/
│       └── {strategy}/
│           ├── understanding.md           # LLM's understanding (generated)
│           ├── stack_layout.md            # Manual: ISA stack layout docs
│           ├── defense_strategy.c         # Manual: Defense strategy code
│           └── *.seed                     # Initial seed files
└── fuzz_out/                              # Default output directory
    ├── corpus/                            # Active seed corpus
    ├── coverage/                          # Coverage reports
    ├── build/                             # Compiled binaries
    └── state/                             # Fuzzing state (for resume)
```

### Step-by-Step Workflow

#### Step 1: Prepare Initial Context

Create the initial seeds directory structure for your target:

```bash
mkdir -p initial_seeds/{isa}/{strategy}
```

Add manual context files:

- `stack_layout.md`: Document the stack layout for the target ISA
- `defense_strategy.c`: Include relevant compiler source code for the defense strategy

Example for `x64/canary`:

```bash
mkdir -p initial_seeds/x64/canary
# Add stack_layout.md and defense_strategy.c
```

#### Step 2: Configure DeFuzz

1. Edit `configs/config.yaml` to set your target:

   ```yaml
   config:
     llm: "deepseek"
     isa: "x64"
     strategy: "canary"
     compiler:
       name: "gcc"
       version: "12.2.0"
   ```

2. Add your LLM API key to `configs/llm.yaml`

3. Create the compiler config file `configs/gcc-v12.2.0-x64-canary.yaml` with paths to your coverage-enabled compiler

#### Step 3: Generate Initial Seeds

Use the `generate` command to create initial seeds:

```bash
# Generate 5 initial seeds
./defuzz generate --isa x64 --strategy canary --count 5

# Or specify a custom output directory
./defuzz generate --isa x64 --strategy canary --count 5 -o my_seeds
```

This will:

1. Generate an LLM understanding (`understanding.md`) if not exists
2. Create initial seed files (`.seed` format)

#### Step 4: Start Fuzzing

Run the fuzzing loop:

```bash
# Basic fuzzing
./defuzz fuzz

# With options
./defuzz fuzz --max-iterations 100 --max-new-seeds 3 --timeout 30

# For cross-architecture fuzzing with QEMU
./defuzz fuzz --use-qemu --qemu-path qemu-aarch64 --qemu-sysroot /usr/aarch64-linux-gnu
```

**Command-line Options for `fuzz`:**

| Flag               | Default         | Description                                        |
| ------------------ | --------------- | -------------------------------------------------- |
| `-o, --output`     | `fuzz_out`      | Output directory for fuzzing artifacts             |
| `--max-iterations` | `0` (unlimited) | Maximum number of fuzzing iterations               |
| `--max-new-seeds`  | `3`             | Maximum new seeds to generate per interesting seed |
| `--timeout`        | `30`            | Execution timeout in seconds                       |
| `--use-qemu`       | `false`         | Use QEMU for cross-architecture execution          |
| `--qemu-path`      | `qemu-aarch64`  | Path to QEMU user-mode executable                  |
| `--qemu-sysroot`   | `""`            | Sysroot path for QEMU (-L argument)                |

### Fuzzing Workflow Summary

The fuzzing process follows this flow:

1. **Load Configuration**: Read `config.yaml` → locate LLM config → locate compiler config
2. **Initialize Corpus**: Create/recover corpus from `fuzz_out/corpus/`
3. **Load Initial Seeds**: If corpus is empty, load from `initial_seeds/{isa}/{strategy}/`
4. **Main Loop** (for each seed):
   - Compile with coverage instrumentation
   - Execute test cases
   - Measure code coverage
   - If coverage increased:
     - Merge coverage report
     - Generate new mutated seeds using LLM
   - Analyze execution results using Oracle
   - Report any discovered bugs
5. **Save State**: Periodically save state for resume capability

### Resume Capability

DeFuzz automatically saves its state to `fuzz_out/state/`. If interrupted, simply run `defuzz fuzz` again to resume from where it left off. The corpus and coverage data are preserved.

## Testing

### Unit Tests

Run unit tests for all modules:

```bash
go test ./...
```

### Integration Tests

The VM module includes comprehensive integration tests for multiple architectures using QEMU user-mode emulation:

| Architecture                       | QEMU Binary    | Cross-Compiler            |
| ---------------------------------- | -------------- | ------------------------- |
| aarch64 (ARM64)                    | `qemu-aarch64` | `aarch64-linux-gnu-gcc`   |
| riscv64 (RISC-V 64-bit)            | `qemu-riscv64` | `riscv64-linux-gnu-gcc`   |
| arm (ARM 32-bit)                   | `qemu-arm`     | `arm-linux-gnueabihf-gcc` |
| ppc64 (PowerPC 64-bit, Big-Endian) | `qemu-ppc64`   | `powerpc64-linux-gnu-gcc` |
| s390x (IBM Z)                      | `qemu-s390x`   | `s390x-linux-gnu-gcc`     |

**Prerequisites for integration tests:**

```bash
# Install QEMU user-mode emulators
apt-get install -y qemu-user qemu-user-static

# Install cross-compilers
apt-get install -y \
    gcc-aarch64-linux-gnu \
    gcc-riscv64-linux-gnu \
    gcc-arm-linux-gnueabihf \
    gcc-powerpc64-linux-gnu \
    gcc-s390x-linux-gnu
```

**Run integration tests:**

```bash
# Run all integration tests
go test -tags=integration ./internal/vm/... -v

# Run tests for specific architecture
go test -tags=integration ./internal/vm/... -v -run "Aarch64"
go test -tags=integration ./internal/vm/... -v -run "Riscv64"

# Run all architectures hello world test
go test -tags=integration ./internal/vm/... -v -run "AllArchitectures_HelloWorld"
```

The integration tests verify:

- Hello World execution on each architecture
- Exit code handling (0, 1, 42, 255)
- Command line argument passing
- Stdout/Stderr separation
- Timeout functionality
- Memory allocation
- Architecture-specific features (e.g., big-endian on ppc64)
