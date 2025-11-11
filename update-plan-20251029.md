## Refined Plan

### 1. Create a utility to clean coverage files

We will create a function to clean `.gcda` and `.gcno` files. This function will be used to ensure a clean state before collecting new coverage information.

**`internal/coverage/gcc.go`**

```go
// Add a new function to GCCCoverage
```

### 2. Update Configuration Structure

We need to add a new `CompilerConfig` to our configuration to handle compiler-specific settings. This involves updating `internal/config/config.go`.

**`internal/config/config.go`**

```go
package config

// ... existing LLMConfig and FuzzerConfig structs

// Config holds the top-level configuration for the application.
type Config struct {
	LLM      LLMConfig      `mapstructure:"llm"`
	Fuzzer   FuzzerConfig   `mapstructure:"fuzzer"`
	Compiler CompilerConfig `mapstructure:"compiler"`
}

// CompilerConfig holds the configuration for the target compiler.
type CompilerConfig struct {
	Path            string   `mapstructure:"path"`
	CoveragePath    string   `mapstructure:"coverage_path"`
	TargetFolder    string   `mapstructure:"target_folder"`
	TargetFiles     []string `mapstructure:"target_files"`
	TargetFunctions []string `mapstructure:"target_functions"`
}

// ... existing Load and LoadConfig functions
```

### 3. Update Configuration Files

We will create a new configuration file `configs/compiler.yaml` to store the compiler settings. This keeps the configuration modular.

**`configs/compiler.yaml`**

```yaml
compiler:
  path: "/root/project/de-fuzz/target-compilers/install-aarch64-none-linux-gnu/bin/aarch64-none-linux-gnu-gcc"
  coverage_path: "/root/project/de-fuzz/target-compilers/build-aarch64-none-linux-gnu/gcc2-build/gcc"
  target_folder: "" # e.g., "src/main"
  target_files: [] # e.g., ["main.c", "utils.c"]
  target_functions: [] # e.g., ["parse_input", "calculate_sum"]
```

### 4. Update Configuration Loading Logic

The `LoadConfig` function in `internal/config/config.go` needs to be updated to load both `llm.yaml` and the new `compiler.yaml`.

**`internal/config/config.go` (updated `LoadConfig`)**

```go
// LoadConfig loads the entire application configuration from all sources.
func LoadConfig() (*Config, error) {
	var cfg Config
	if err := Load("llm", &cfg); err != nil {
		return nil, err
	}
	if err := Load("compiler", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
```
