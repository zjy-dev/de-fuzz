## Plan for `internal/prompt` Module

### 1. Objective

This module is the central hub for all prompt engineering. It is responsible for constructing every prompt sent to the LLM, ensuring that the instructions are clear, consistent, and tailored to the specific task at hand. This centralization makes it easier to manage, version, and refine the prompts as the fuzzer evolves.

### 2. Core Responsibilities

The `Builder` in this module will be responsible for creating four distinct types of prompts:

1.  **Understanding Prompt**: An initial, detailed prompt that provides the LLM with the necessary context about the target ISA, defense strategy, and overall goal. This is built by `BuildUnderstandPrompt`.
2.  **Generation Prompt**: A prompt that asks the LLM to generate a new seed from scratch, based on the established context. This is built by `BuildGeneratePrompt`.
3.  **Mutation Prompt**: A prompt that provides the LLM with an existing seed and asks it to create a new, mutated variant. This is built by `BuildMutatePrompt`.
4.  **Analysis Prompt**: A prompt that gives the LLM an existing seed plus the feedback from its execution (e.g., stdout, stderr, exit code) and asks it to determine if a bug was found. This is built by `BuildAnalyzePrompt`.

### 3. `Builder` Implementation (`prompt.go`)

The `Builder` struct will have the following methods:

```go
package prompt

import "defuzz/internal/seed"

type Builder struct {
    // Configuration for prompt templates can be added here.
}

func NewBuilder() *Builder { /* ... */ }

// BuildUnderstandPrompt constructs the initial context-setting prompt.
func (b *Builder) BuildUnderstandPrompt(isa, strategy string) (string, error) { /* ... */ }

// BuildGeneratePrompt constructs a prompt to generate a new seed.
func (b *Builder) BuildGeneratePrompt(ctx, seedType string) (string, error) { /* ... */ }

// BuildMutatePrompt constructs a prompt to mutate an existing seed.
func (b *Builder) BuildMutatePrompt(ctx string, s *seed.Seed) (string, error) { /* ... */ }

// BuildAnalyzePrompt constructs a prompt to analyze execution feedback.
func (b *Builder) BuildAnalyzePrompt(ctx string, s *seed.Seed, feedback string) (string, error) { /* ... */ }
```

### 4. Testing (`prompt_test.go`)

Each of the four `Build` methods will have its own unit test to ensure that:

- It returns an error for invalid input (e.g., empty strings, nil seeds).
- The generated prompt string contains the correct key information (e.g., ISA, strategy, seed content, feedback).

## Update Plan for `internal/prompt`

This plan outlines the enhancements for the `BuildUnderstandPrompt` function to create more detailed and context-rich prompts for the LLM.

## 1. Helper Function: `readFileOrDefault`

A private helper function will be added to the `prompt` package to safely read auxiliary context files.

- **Function Signature:** `readFileOrDefault(path string) (string, error)`
- **Behavior:**
  - Attempts to read the file at the given `path`.
  - If the file does not exist (`os.IsNotExist`), it returns the default string `"Not available for now"` and a `nil` error.
  - If any other error occurs during reading, it returns an empty string and the error.
  - On successful read, it returns the file's content as a string and a `nil` error.

## 2. `BuildUnderstandPrompt` Enhancement

The `BuildUnderstandPrompt` function will be updated to incorporate the additional context.

- **New Signature:** `BuildUnderstandPrompt(isa, strategy, basePath string) (string, error)`
- **Logic:**
  1.  It will construct the paths to the context files within the `basePath` directory (e.g., `initial_seeds/<isa>/<defense_strategy>/`).
      - `stackLayoutPath := filepath.Join(basePath, "stack_layout.md")`
      - `sourceCodePath := filepath.Join(basePath, "defense_strategy.c")`
  2.  It will call the `readFileOrDefault` helper for each of these paths to get their content.
  3.  The `fmt.Sprintf` template for the prompt will be expanded to include new sections:
      - `[ISA Stack Layout]`
      - `[Defense Strategy Source Code]`
  4.  These new sections will be populated with the content read from the files.

This change will produce a much more detailed initial prompt, enabling the LLM to generate a higher quality "understanding" and, subsequently, better seeds.
