# Code Plan for `internal/config`

This document outlines the plan for the `internal/config` package.

## 1. Purpose

The `config` package is responsible for loading and managing all external configurations for the de-fuzz application. It uses the `viper` library to read YAML files from the `/configs` directory.

## 2. Core Functionality

- **Generic Configuration Loading:** Provides a single, generic `Load` function to read any configuration file from the `configs/` directory and unmarshal it into a corresponding Go struct.
- **Convention-based:** The `Load` function finds configuration files by name, expecting them to be in `configs/<name>.yaml`.
- **Extensibility:** New configurations can be added by simply defining a new struct and creating a corresponding YAML file in the `configs/` directory. No code changes are needed in the `config` package itself.

## 3. Structure

- `config.go`: Contains the generic `Load` function and the definitions for various configuration structs (e.g., `LLMConfig`).
- `config_test.go`: Contains unit tests for the configuration loading logic.

## 4. Configuration Structs

### `LLMConfig`

```go
// LLMConfig holds the configuration for the Large Language Model.
type LLMConfig struct {
    Provider string `mapstructure:"provider"` // e.g., "openai", "anthropic"
    Model    string `mapstructure:"model"`    // e.g., "gpt-4", "claude-2"
    APIKey   string `mapstructure:"api_key"`  // The API key for the LLM provider
    Endpoint string `mapstructure:"endpoint"` // The API endpoint for the LLM provider
}
```

## 5. Usage Example

```go
// In another package, to load the LLM config:
import "defuzz/internal/config"

func main() {
    var llmConfig config.LLMConfig
    if err := config.Load("llm", &llmConfig); err != nil {
        log.Fatalf("Failed to load LLM config: %v", err)
    }
    // ... use llmConfig
}
```