# Configuration Guide

## Configuration File Structure

DeFuzz uses a two-level configuration structure:

### 1. Main Configuration (`config.yaml`)

Located at `configs/config.yaml`, this file contains:

- LLM provider selection
- Target ISA (Instruction Set Architecture)
- Target defense strategy
- Compiler identification (name and version)

Example:

```yaml
config:
  llm: "deepseek"
  isa: "x64"
  strategy: "canary"
  compiler:
    name: "gcc"
    version: "12.2.0"
```

### 2. Compiler-Specific Configuration

Pattern: `configs/{compiler}-v{version}-{isa}-{strategy}.yaml`

Example: `configs/gcc-v12.2.0-x64-canary.yaml`

This file contains:

- Compiler executable path
- Coverage tool configuration (gcovr)
- **Fuzzing parameters** (output directory, iterations, timeouts, etc.)
- **Oracle configuration** (type and options)
- Target functions for coverage tracking

## Why This Structure?

Different compiler/ISA/strategy combinations may require:

- Different fuzzing parameters (e.g., longer timeouts for cross-architecture)
- Different oracle types (e.g., crash detection vs. LLM analysis)
- Different QEMU configurations for cross-architecture fuzzing

## Creating a New Configuration

1. Copy the template:

   ```bash
   cp configs/compiler-config-template.yaml configs/your-compiler-config.yaml
   ```

2. Update the main `config.yaml` to reference your compiler

3. Customize the compiler-specific configuration:
   - Set compiler path
   - Configure coverage tool
   - Adjust fuzzing parameters
   - Select oracle type

## Oracle Types

### LLM Oracle (Default)

Uses Large Language Model to analyze execution results:

```yaml
compiler:
  oracle:
    type: "llm"
    options: {}
```

### Crash Oracle

Simple crash detection without LLM:

```yaml
compiler:
  oracle:
    type: "crash"
    options: {}
```

### Future Oracle Types

- `diff`: Differential testing between compiler versions
- `static`: Static analysis integration
- `custom`: User-defined oracle scripts

## Command Line Override

Fuzzing parameters can be overridden via command line:

```bash
defuzz fuzz --max-iterations 100 --timeout 60 --use-qemu
```

Command line flags take precedence over configuration file values.
