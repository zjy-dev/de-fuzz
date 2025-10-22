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

### Mutation Principle

Based on Coverage increasement.

### Test Oracle

Dynamical Testing.

Run seeds -> get feedback(return codes + stdout/stderr(s) + logfiles) -> let LLM judge if there're bugs.

### Fuzzing Algorithm

For each defense startegy and ISA:

1. Prepare environment with podman and qemu

2. Build initial prompt `ip`:

   - current environment and toolchain
   - manually summarize defense startegy and stack layout of the ISA
   - manually summarize pesudo-code of the compiler source code about that startegy and ISA
   - also reserve source code as an "attachment" below

3. Feed `ip` to llm and store its "understanding" as memory
   <!-- if llm does't understand your demands, then how to fuzz with llm? -->

4. Initialize seed pool:

   - let llm generate initial seed(s) || use official unit tests
   - adjust init seeds mannualy
   - run init seeds and record their coverage info

5. Pop seed `s` from seed pool

6. Compile `s` and record coverage info

   - if coverage rate increased then mutate `s` to `s'` and push `s'` to seed pool

7. Oracle(s):
   <!-- TODO: May use Multi-armed bandit for mutation later -->
   - record if found a bug

## Implementation

### Coverage

Use gcc's gcov ecosystem.

The simplified workflow is below:

```bash
#!/bin/bash

# --- Initial Folder Setup ---
SRCDIR=/root/fuzz-coverage/gcc-release-gcc-12.2.0
BUILDDIR=/root/fuzz-coverage/gcc-build
REPORTDIR=/root/fuzz-coverage/coverage_report

# --- Step 1: Recompile target compiler `tc` with coverage flags ---
echo "=== [1/4] Configuring and Compiling GCC with coverage flags... ==="
mkdir -p $BUILDDIR $REPORTDIR
cd $BUILDDIR
$SRCDIR/configure \
    --enable-coverage \
    --disable-bootstrap \
    --enable-languages=c,c++
make -j$(nproc)

# --- Step 2: Run `tc` for .gcda ---
echo "=== [2/4] Running instrumented GCC to generate coverage data... ==="
echo 'int main() { return 0; }' > /tmp/test.c
$BUILDDIR/gcc/xgcc -fstack-protector-strong -o /tmp/test.o -c /tmp/test.c

# --- Step 3: Use lcov to handle coverage info ---
echo "=== [3/4] Capturing coverage data with lcov... ==="
cd $BUILDDIR
lcov --capture --directory . --output-file coverage.info

# --- Step 4(optional): Gen HTML report ---
echo "=== [4/4] Generating HTML report... ==="
genhtml coverage.info --output-directory $REPORTDIR
```

<!-- ## Usage

DeFuzz is a command-line tool with multiple subcommands.

### `generate`

This command is used to generate the initial seed pool for a specific ISA and defense strategy.

**Usage:**

```bash
go run ./cmd/defuzz generate --isa <target-isa> --strategy <target-strategy> [flags]
```

**Flags:**

- `--isa`: (Required) Target ISA (e.g., `x86_64`).
- `--strategy`: (Required) Defense strategy (e.g., `stackguard`).
- `-o, --output`: Output directory for seeds (default: `initial_seeds`).
- `-c, --count`: Number of seeds to generate (default: `1`).

**Note:** Before running the generate command, ensure you have set up the fuzzing environment using the provided container script: `./scripts/build-container.sh`

### Seed Storage

The `initial_seeds/` directory stores all data related to a specific fuzzing target (a combination of ISA and defense strategy). This includes the LLM's cached understanding of the target and the individual seeds.

```
initial_seeds/<isa>/<defense_strategy>/
├── understanding.md
└── <id>/
    ├── source.c
    ├── Makefile
    └── run.sh
```

- **`<isa>`**: The target Instruction Set Architecture (e.g., `x86_64`).
- **`<defense_strategy>`**: The defense strategy being fuzzed (e.g., `stackguard`).
- **`understanding.md`**: A cached file containing the LLM's summary and understanding of the initial prompt. This is generated on the first run and reused to save time and API calls.
- **`<id>`**: A directory for each individual seed, containing:
  - **`source.c`**: The C source code for the seed
  - **`Makefile`**: Build instructions and compilation flags
  - **`run.sh`**: Execution script for testing the compiled binary

## Project Structure

The project is structured to separate different logical components of the fuzzer, following standard Go project layout conventions. This makes the codebase easier to understand, maintain, and test.

- **`cmd/defuzz/`**: This is the main entry point for the application. The `main.go` file in this directory is responsible for parsing command-line arguments, handling the different execution modes (`generate` and `fuzz`), and starting the appropriate process.

- **`internal/`**: This directory contains all the core logic of the fuzzer. As it's `internal`, this code is not meant to be imported by other external projects.

  - **`config/`**: Provides a generic way to load configurations (e.g., for the LLM) from YAML files stored in the `configs/` directory. It uses the Viper library to automatically find and parse files by name (e.g., `llm.yaml`) and includes robust error handling for malformed or missing files.
  - **`exec/`**: A low-level utility package that provides robust helper functions for executing external shell commands on the host system.
  - **`vm/`**: Manages the containerized execution environment. It handles creating, starting, and stopping the Podman container. It provides functions to run commands _inside_ the container (for compiling and executing seeds) by using the `exec` package to call `podman exec`.
  - **`llm/`**: Responsible for all interactions with the Large Language Model. It features a modular design with an `LLM` interface to support different providers. The `New()` factory function initializes the client (e.g., `DeepSeekClient`) based on `configs/llm.yaml`, allowing for easy extension and testing. Its duties include processing initial prompts, generating and mutating seeds, and analyzing feedback.
  - **`prompt/`**: Focuses on constructing the detailed initial prompts for the LLM, including environment details and defense strategy summaries.
  - **`seed_executor/`**: Executes a seed within the VM. It prepares the environment, runs the seed's command, and returns the result.
  - **`seed/`**: Defines the data structures for seeds and manages the seed pool (e.g., adding, saving, and loading seeds).
  - **`analysis/`**: Handles the analysis of fuzzing feedback. It will interpret the results of a seed execution to determine if a bug was found.
  - **`report/`**: Handles the saving of buggy seeds and their associated feedback as reports.
  - **`fuzz/`**: Contains the high-level orchestration logic. In `generate` mode, it coordinates `prompt`, `llm`, and `seed` to create the initial seed pool. In `fuzz` mode, it runs the main fuzzing loop, manages the bug count, and determines when to exit.

- **`pkg/`**: Intended for code that can be safely imported and used by external applications. It is currently empty but reserved for future use.

- **`configs/`**: A designated place for configuration files, such as settings for the LLM or different fuzzing targets.

- **`scripts/`**: For storing helper scripts, for instance, to automate builds, run tests, or set up environments.

- **`testdata/`**: Contains sample files and data required for running tests, such as example C/assembly source files.

## Work Flow

- 2025-01-23: Updated documentation to reflect unified seed structure (C + Makefile + run.sh) and removed deprecated seed type parameter.
- 2025-08-01: Updated seed plan to reflect the three seed types.
- 2025-07-31: Created plan for the report module.
- 2025-07-31: Reviewed and updated all module plans. -->
