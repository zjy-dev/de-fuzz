# DeFuzz

A fuzzer for software defense startegy.

Written in golang.

## Idea

### Seed Defination:

Seeds in DeFuzz have three types

1. probably buggy c file + compile command that from c to binary
2. probably buggy c file + compile command that from c to asm(then literally compile it to asm) + llm fine-tuning asm + compile command that from asm to binary
3. probably buggy asm file + compile command that from asm to binary

### Fuzzing Algorithm

For each defense startegy and ISA:

1. prepare environment with podman and qemu
2. build initial prompt `ip`:
   - current environment and toolchain
   - manually summarize defense startegy and stack layout of the ISA
   - manually summarize pesudo-code of the compiler source code about that startegy and ISA
   - also reserve source code as an "attachment" below
3. feed `ip` to llm and store its "understanding" memory
4. initialize seed pool:
   - let llm generate initial seeds
   - adjust init seed pool mannualy
5. pop a seed `s` from seed pool
6. run s and record feedback `fb`(return code + stdout/stderr + logfile)
7. let llm analyze info of `s` + `fb` and act accordingly:
   <!-- TODO: May change to Multi-armed bandit later -->
   1. is a bug😊!!! -> record in detail -> `bug_cnt++` -> llm mutate `s` and push to seed pool
   2. not a bug -> let llm decide whether to discard `s` (is `s` meaningless?)
      - if not to discard, then mutate `s` and push to seed pool
8. if
   - `bug_cnt >= 3`, exit with success🤗!!!
   - `seed pool is empty`, exit with fail😢.
   - back to step 5😾.

## Usage

DeFuzz is a command-line tool with two primary modes of operation, controlled by the `--mode` flag.

### Modes

1.  **Generate Mode (`--mode=generate`)**
    This mode is used to generate the initial seed pool for a specific ISA and defense strategy. The generated seeds are saved to disk for manual review and adjustment before being used in the fuzzing mode.

    ```bash
    go run ./cmd/defuzz --mode=generate --isa=<target-isa> --strategy=<target-strategy>
    ```

2.  **Fuzzing Mode (`--mode=fuzz`)**
    This is the default mode. It loads the manually approved seeds from disk and starts the main fuzzing loop.

    ```bash
    go run ./cmd/defuzz --mode=fuzz --isa=<target-isa> --strategy=<target-strategy>
    ```

### Seed Storage

The `initial_seeds/` directory stores all data related to a specific fuzzing target (a combination of ISA and defense strategy). This includes the LLM's cached understanding of the target and the individual seeds.

```
initial_seeds/<isa>/<defense_strategy>/
├── understanding.md
└── <id>_<seed_type>/
    ├── Makefile
    └── source.c (or source.s)
```

- **`<isa>`**: The target Instruction Set Architecture (e.g., `x86_64`).
- **`<defense_strategy>`**: The defense strategy being fuzzed (e.g., `stackguard`).
- **`understanding.md`**: A cached file containing the LLM's summary and understanding of the initial prompt. This is generated on the first run and reused to save time and API calls.
- **`<id>_<seed_type>`**: A directory for each individual seed, containing its source code and build instructions.

## Project Structure

The project is structured to separate different logical components of the fuzzer, following standard Go project layout conventions. This makes the codebase easier to understand, maintain, and test.

- **`cmd/defuzz/`**: This is the main entry point for the application. The `main.go` file in this directory is responsible for parsing command-line arguments, handling the different execution modes (`generate` and `fuzz`), and starting the appropriate process.

- **`internal/`**: This directory contains all the core logic of the fuzzer. As it's `internal`, this code is not meant to be imported by other external projects.

  - **`analysis/`**: Handles the analysis of fuzzing feedback. It will interpret the results of a seed execution to determine if a bug was found.
  - **`compiler/`**: Manages the compilation of source code (C or assembly) into executable binaries as defined by the seeds.
  - **`config/`**: Provides a generic way to load configurations (e.g., for the LLM) from YAML files stored in the `configs/` directory. It uses the Viper library to automatically find and parse files by name (e.g., `llm.yaml`) and includes robust error handling for malformed or missing files.
  - **`exec/`**: A low-level utility package that provides robust helper functions for executing external shell commands on the host system.
  - **`fuzz/`**: Contains the high-level orchestration logic. In `generate` mode, it coordinates `prompt`, `llm`, and `seed` to create the initial seed pool. In `fuzz` mode, it runs the main fuzzing loop, manages the bug count, and determines when to exit.
  - **`llm/`**: Responsible for all interactions with the Large Language Model, including sending prompts and receiving generated code or analysis.
  - **`prompt/`**: Focuses on constructing the detailed initial prompts for the LLM, including environment details and defense strategy summaries.
  - **`report/`**: Handles the saving of buggy seeds and their associated feedback as reports.
  - **`seed/`**: Defines the data structures for seeds and manages the seed pool (e.g., adding, saving, and loading seeds).
  - **`vm/`**: Manages the containerized execution environment. It handles creating, starting, and stopping the Podman container. It provides functions to run commands _inside_ the container (for compiling and executing seeds) by using the `exec` package to call `podman exec`.

- **`pkg/`**: Intended for code that can be safely imported and used by external applications. It is currently empty but reserved for future use.

- **`configs/`**: A designated place for configuration files, such as settings for the LLM or different fuzzing targets.

- **`scripts/`**: For storing helper scripts, for instance, to automate builds, run tests, or set up environments.

- **`testdata/`**: Contains sample files and data required for running tests, such as example C/assembly source files.
