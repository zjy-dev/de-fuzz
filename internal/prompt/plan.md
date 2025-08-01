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
