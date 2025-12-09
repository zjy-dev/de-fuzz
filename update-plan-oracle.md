# Oracle Module Plugin-based Refactoring Plan

This document outlines the plan to refactor the `oracle` module into a plugin-based architecture. This will allow different defense strategies to use different oracle implementations (e.g., LLM-based, differential testing, static analysis, or simple crash detection).

## 1. Configuration Updates

We need to update the configuration to allow selecting an oracle type and providing specific options.

### `internal/config/config.go`

Add an `Oracle` section to the main `Config` struct.

```go
type Config struct {
    // ... existing fields
    Oracle OracleConfig `mapstructure:"oracle"`
}

type OracleConfig struct {
    // Type specifies the name of the oracle plugin to use (e.g., "llm", "crash", "diff")
    Type string `mapstructure:"type"`

    // Options holds arbitrary configuration for the specific oracle implementation
    Options map[string]interface{} `mapstructure:"options"`
}
```

### `configs/config.yaml`

Example configuration:

```yaml
config:
  # ... existing config
  oracle:
    type: "llm" # or "crash", "diff", etc.
    options:
      # Options specific to the chosen oracle
      model: "deepseek"
```

## 2. Oracle Interface and Registry

We will introduce a registry to manage oracle plugins.

### `internal/oracle/registry.go` (New File)

```go
package oracle

import (
    "fmt"
    "github.com/zjy-dev/de-fuzz/internal/llm"
    "github.com/zjy-dev/de-fuzz/internal/prompt"
)

// OracleFactory is a function that creates a new Oracle instance.
// It receives the configuration options and necessary dependencies.
// Note: Dependencies like LLM and PromptBuilder might be nil if the oracle doesn't need them.
type OracleFactory func(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error)

var (
    registry = make(map[string]OracleFactory)
)

// Register adds an oracle factory to the registry.
func Register(name string, factory OracleFactory) {
    registry[name] = factory
}

// New creates an oracle instance by name.
func New(name string, options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
    factory, ok := registry[name]
    if !ok {
        return nil, fmt.Errorf("oracle plugin not found: %s", name)
    }
    return factory(options, l, prompter, context)
}
```

### `internal/oracle/oracle.go`

The `Oracle` interface remains largely the same, but we might want to ensure it's flexible enough.

```go
// Oracle determines if a seed execution has found a bug.
type Oracle interface {
    // Analyze analyzes the execution result of a seed and returns a Bug if found, nil otherwise.
    Analyze(s *seed.Seed, results []Result) (*Bug, error)
}
```

## 3. Implementations

We will refactor the existing `LLMOracle` and add a simple `CrashOracle` as examples.

### `internal/oracle/plugins/llm.go` (Refactored from `llm_oracle.go`)

```go
package plugins

import (
    "github.com/zjy-dev/de-fuzz/internal/oracle"
    // ... imports
)

func init() {
    oracle.Register("llm", NewLLMOracle)
}

func NewLLMOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (oracle.Oracle, error) {
    // Parse options if needed
    return &LLMOracle{
        llm:        l,
        prompter:   prompter,
        llmContext: context,
    }, nil
}

type LLMOracle struct {
    // ... existing fields
}

// ... existing Analyze implementation
```

### `internal/oracle/plugins/crash.go` (New Simple Oracle)

```go
package plugins

import (
    "github.com/zjy-dev/de-fuzz/internal/oracle"
    // ... imports
)

func init() {
    oracle.Register("crash", NewCrashOracle)
}

func NewCrashOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (oracle.Oracle, error) {
    return &CrashOracle{}, nil
}

type CrashOracle struct{}

func (o *CrashOracle) Analyze(s *seed.Seed, results []Result) (*oracle.Bug, error) {
    for _, res := range results {
        if oracle.IsCrashExit(res.ExitCode) {
             return &oracle.Bug{
                Seed: s,
                Results: results,
                Description: "Crash detected via exit code",
            }, nil
        }
    }
    return nil, nil
}
```

## 4. Integration with Engine

### `internal/fuzz/engine.go`

Update `NewEngine` or the caller to use `oracle.New`.

```go
// In NewEngine or where EngineConfig is constructed:

// ...
orc, err := oracle.New(cfg.Oracle.Type, cfg.Oracle.Options, llmClient, promptBuilder, understanding)
if err != nil {
    // handle error
}
// ...
```

## 5. Default Behavior

To maintain backward compatibility or ease of use:

- If `config.Oracle.Type` is empty, default to `"llm"`.
- Ensure `LLMOracle` is registered by default (e.g., by importing the plugins package in `main.go`).

## 6. Future Plugins

This structure allows adding:

- **DiffOracle**: Compares outputs from multiple compilers/optimizations.
- **StaticOracle**: Runs static analysis tools on the source code.
- **CustomOracle**: User-defined logic via scripts or compiled plugins (if needed later).
