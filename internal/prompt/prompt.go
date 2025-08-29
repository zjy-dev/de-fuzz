package prompt

import (
	"fmt"
	"os"
	"path/filepath"

	"defuzz/internal/seed"
)

// Builder is responsible for constructing prompts for the LLM.
type Builder struct {
	// In the future, this could hold paths to template files or other configuration.
}

// NewBuilder creates a new prompt builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// readFileOrDefault safely reads auxiliary context files.
// If the file doesn't exist, it returns a default message and no error.
// If any other error occurs, it returns the error.
func readFileOrDefault(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "Not available for now", nil
		}
		return "", err
	}
	return string(content), nil
}

// BuildUnderstandPrompt constructs the initial prompt for a given ISA and defense strategy.
// It now includes additional context from auxiliary files if available.
func (b *Builder) BuildUnderstandPrompt(isa, strategy, basePath string) (string, error) {
	if isa == "" || strategy == "" {
		return "", fmt.Errorf("isa and strategy must be provided")
	}

	// Read auxiliary context files
	stackLayoutPath := filepath.Join(basePath, "stack_layout.md")
	sourceCodePath := filepath.Join(basePath, "defense_strategy.c")

	stackLayout, err := readFileOrDefault(stackLayoutPath)
	if err != nil {
		return "", fmt.Errorf("failed to read stack layout file: %w", err)
	}

	sourceCode, err := readFileOrDefault(sourceCodePath)
	if err != nil {
		return "", fmt.Errorf("failed to read defense strategy source code: %w", err)
	}

	prompt := fmt.Sprintf(`You are an expert security researcher specializing in compiler fuzzing.
Your goal is to find bugs in a compiler's defense strategy implementation.

[TARGET CONFIGURATION]
Target ISA: %s
Defense Strategy: %s

[ISA Stack Layout]
%s

[Defense Strategy Source Code]
%s

[TASK]
Based on the above information, provide a detailed analysis and understanding of how to generate effective test cases that would reveal vulnerabilities or corner cases in the "%s" defense strategy on the "%s" architecture.

Your understanding should include:
1. Key vulnerabilities or edge cases to target
2. Specific code patterns that might bypass the defense
3. Assembly-level considerations for the target ISA
4. Compilation flags and techniques that might be relevant
5. Expected behavior vs potential failure modes

This understanding will be used as context for generating and mutating test seeds, so be thorough and technical.
`, isa, strategy, stackLayout, sourceCode, strategy, isa)

	return prompt, nil
}

// BuildGeneratePrompt constructs a prompt to generate a new seed.
func (b *Builder) BuildGeneratePrompt(ctx, seedType string) (string, error) {
	if ctx == "" || seedType == "" {
		return "", fmt.Errorf("context and seedType must be provided")
	}

	prompt := fmt.Sprintf(`
[CONTEXT]
%s
[/CONTEXT]

Based on the context provided, generate a new, complete, and valid seed of type "%s".
The seed must contain both the source code and a Makefile for compilation.
Respond with only the seed content in the format specified in the context.
`, ctx, seedType)
	return prompt, nil
}

// BuildMutatePrompt constructs a prompt to mutate an existing seed.
func (b *Builder) BuildMutatePrompt(ctx string, s *seed.Seed) (string, error) {
	if ctx == "" || s == nil {
		return "", fmt.Errorf("context and seed must be provided")
	}

	prompt := fmt.Sprintf(`
[CONTEXT]
%s
[/CONTEXT]

[EXISTING SEED]
Source (%s):
---
%s
---

Makefile:
---
%s
---
[/EXISTING SEED]

Based on the context, mutate the existing seed to create a new variant that is more likely to find a bug.
Respond with only the mutated seed content.
`, ctx, s.Type, s.Content, s.Makefile)
	return prompt, nil
}

// BuildAnalyzePrompt constructs a prompt to analyze execution feedback.
func (b *Builder) BuildAnalyzePrompt(ctx string, s *seed.Seed, feedback string) (string, error) {
	if ctx == "" || s == nil || feedback == "" {
		return "", fmt.Errorf("context, seed, and feedback must be provided")
	}

	prompt := fmt.Sprintf(`
[CONTEXT]
%s
[/CONTEXT]

[SEED]
Source (%s):
---
%s
---

Makefile:
---
%s
---
[/SEED]

[EXECUTION FEEDBACK]
%s
[/EXECUTION FEEDBACK]

Analyze the execution feedback in the provided context.
Determine if a bug was found.
Respond with "BUG" if a bug is present, or "NO_BUG" if not.
`, ctx, s.Type, s.Content, s.Makefile, feedback)
	return prompt, nil
}

/*
--- UPDATE CODE PLAN ---

1.  **Create `readFileOrDefault` helper:**
    -   Create a private helper function `readFileOrDefault(path string) (string, error)`.
    -   This function will read the file at `path`.
    -   If `os.IsNotExist(err)`, it will return `"Not available for now"` and `nil`.
    -   For other errors, it will return `""` and the error.
    -   If successful, it returns the file content as a string.

2.  **Modify `BuildUnderstandPrompt`:**
    -   Change the signature to `BuildUnderstandPrompt(isa, strategy, basePath string) (string, error)`.
    -   Define paths for context files:
        -   `stackLayoutPath := filepath.Join(basePath, "stack_layout.md")`
        -   `sourceCodePath := filepath.Join(basePath, "defense_strategy.c")`
    -   Call `readFileOrDefault` for both paths to get their content.
    -   Update the prompt template to include new sections for "ISA Stack Layout" and "Defense Strategy Source Code", populating them with the content read from the files.
*/
