# DeFuzz

A fuzzer for software defense startegy.

Written in golang.

## Idea

### Seed Defination:

A seed is a self-contained test case. It consists of source code (`c`, `asm`, or `go`) and an execution command.

There are three initial types of seeds:
1.  A C source file and a command to compile it to a binary.
2.  A C source file and a command to compile it to assembly. The resulting assembly code can then be fine-tuned by the LLM and assembled into a binary.
3.  An assembly source file and a command to assemble it to a binary.

After manual review and fine-tuning, the initial seeds are consolidated into two primary types for fuzzing efficiency: `c` and `asm`.

### Fuzzing Algorithm

For each defense startegy and ISA:

1. prepare environment with podman and qemu
2. build initial prompt `ip`:

   - current environment and toolchain
   - manually summarize defense startegy and stack layout of the ISA
   - manually summarize pesudo-code of the compiler source code about that startegy and ISA
   - also reserve source code as an "attachment" below

   - concrate constrains
   <!-- As a templete, stack layout can be json/struct... -->

3. feed `ip` to llm and store its "understanding" memory
   <!-- TODO: 引导llm生成能触发编译器片段的seed pattern(c or asm flip) + 自然语言形式的对 pattern 的描述 -->
   <!-- 验证 pattern 总结的可行性 -->
4. initialize seed pool:

   - let llm generate a valid initial seed <!-- TODO: Should be ?-shots ? -->
   - adjust init seed mannualy

5. pop a seed `s` from seed pool
6. run s and record feedback `fb`(return code + stdout/stderr + logfile)
7. let llm analyze info of `s` + `fb` and act accordingly:
   <!-- TODO: May use Multi-armed bandit for mutation later -->
   1. is a bug😊!!! -> record in detail -> `bug_cnt++` -> llm mutate `s` and push to seed pool
   2. not a bug -> let llm decide whether to discard `s` (is `s` meaningless?)
      - if not to discard, then mutate `s` and push to seed pool
8. if
   - `bug_cnt >= 3`, exit with success🤗!!!
   - `seed pool is empty`, exit with fail😢.
   - back to step 5😾.

## Usage

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
- `-t, --type`: Type of seed to generate (`c` or `asm`) (default: `c`).


### Seed Storage

The `initial_seeds/` directory stores all data related to a specific fuzzing target (a combination of ISA and defense strategy). This includes the LLM's cached understanding of the target and the individual seeds.

```
initial_seeds/<isa>/<defense_strategy>/
├── understanding.md
└── <id>_<seed_type>/
    ├── exec.sh
    └── source.c (or source.s, source.go)
```

- **`<isa>`**: The target Instruction Set Architecture (e.g., `x86_64`).
- **`<defense_strategy>`**: The defense strategy being fuzzed (e.g., `stackguard`).
- **`understanding.md`**: A cached file containing the LLM's summary and understanding of the initial prompt. This is generated on the first run and reused to save time and API calls.
- **`<id>_<seed_type>`**: A directory for each individual seed, containing its source code and execution command.

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
- 2025-08-01: Updated seed plan to reflect the three seed types.
- 2025-07-31: Created plan for the report module.
- 2025-07-31: Reviewed and updated all module plans.
