package prompt

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zjy-dev/de-fuzz/internal/seed"
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

	stackLayout, err := readFileOrDefault(stackLayoutPath)
	if err != nil {
		return "", fmt.Errorf("failed to read stack layout file: %w", err)
	}

	prompt := fmt.Sprintf(`You are a world-class expert in cybersecurity, specializing in low-level exploitation and compiler security. You have a deep understanding of how compilers work, how security mitigations are implemented, and how they can be bypassed.

Your mission is to craft a high-quality, detailed **System Prompt**. This prompt will be given to another AI assistant whose sole job is to generate and mutate C code to fuzz a compiler's security features. The effectiveness of the entire fuzzing process depends on the quality of your system prompt.

**[CONTEXT]**

Target ISA: %s
Defense Strategy: %s

[ISA Stack Layout of the target compiler]
%s

**[YOUR TASK]**

Analyze the provided context and generate a **System Prompt**. This prompt must be a comprehensive guide for the other AI. It should be structured to make the AI an expert on this specific fuzzing task.

The generated System Prompt **MUST** contain the following sections:

1.  **`+"`## Goal`"+`**: A clear and concise statement of the objective. (e.g., "Your goal is to generate and mutate C code to discover vulnerabilities in the '%s' defense strategy on the '%s' architecture.")

2.  **`+"`## Core Concepts`"+`**: A detailed explanation of the defense strategy and the ISA's stack layout. You must synthesize the provided context, not just copy it. Explain *how* the defense works and what its theoretical weaknesses are.

3.  **`+"`## Attack Vectors & Vulnerability Patterns`"+`**: This is the most critical section. Provide a bulleted list of specific, actionable attack ideas and C code patterns to try. Be creative and think like an attacker. Examples:
    *   Integer overflows to bypass bounds checks.
    *   Tricky pointer arithmetic to confuse alias analysis.
    *   Using `+"`longjmp`"+` or other control-flow manipulation to skip security checks.
    *   Exploiting format string vulnerabilities in novel ways.

4.  **`+"`## Seed Generation & Mutation Rules`"+`**: Clear instructions for the AI on how to format its output. It must produce a complete C source file and a Makefile, as well as a run.sh, and mutations should be small and intelligent.

**[OUTPUT INSTRUCTIONS]**

-   You must only output the generated **System Prompt**.
-   Do not include any other text, conversation, or explanations.
-   The output should be formatted in Markdown.
`, isa, strategy, stackLayout, strategy, isa)

	return prompt, nil
}

// BuildGeneratePrompt constructs a prompt to generate a new seed.
func (b *Builder) BuildGeneratePrompt(basePath string) (string, error) {

	// Read auxiliary context files
	stackLayoutPath := filepath.Join(basePath, "stack_layout.md")
	stackLayout, err := readFileOrDefault(stackLayoutPath)
	if err != nil {
		return "", fmt.Errorf("failed to read stack layout file: %w", err)
	}

	prompt := fmt.Sprintf(`Generate a new, complete, and valid seed.
The seed must contain C source code and test cases.
Please ensure the code has potential vulnerabilities that can be discovered through fuzzing.

Requirements:
- Provide complete C source code that compiles successfully.
- The code will be saved as 'source.c' and compiled using a predefined command.
- Include test cases in JSON format with running commands and expected results.
- The code should be minimal but demonstrate a potential vulnerability.
- Focus on the specific ISA and defense strategy from the system context.

And the ISA Stack Layout for the target compiler:
%s

Format your response as:
Source (c):
---
[source code here]
---

Test Cases (json):
---
[
  {
    "running command": "[command to run]",
    "expected result": "[expected outcome]"
  },
  {
    "running command": "[another command]",
    "expected result": "[another expected outcome]"
  }
]
---
`, stackLayout)
	return prompt, nil
}

// BuildMutatePrompt constructs a prompt to mutate an existing seed.
func (b *Builder) BuildMutatePrompt(s *seed.Seed) (string, error) {
	if s == nil {
		return "", fmt.Errorf("seed must be provided")
	}

	// Convert test cases to JSON for display
	testCasesJSON := "[\n"
	for i, tc := range s.TestCases {
		if i > 0 {
			testCasesJSON += ",\n"
		}
		testCasesJSON += fmt.Sprintf(`  {
    "running command": "%s",
    "expected result": "%s"
  }`, tc.RunningCommand, tc.ExpectedResult)
	}
	testCasesJSON += "\n]"

	prompt := fmt.Sprintf(`
[EXISTING SEED]
Source (c):
---
%s
---

Test Cases (json):
---
%s
---
[/EXISTING SEED]

Based on the system context, mutate the existing seed to create a new variant that is more likely to find a bug.
Please make focused changes that could expose different vulnerability patterns.

Format your response as:
Source (c):
---
[mutated source code here]
---

Test Cases (json):
---
[
  {
    "running command": "[command to run]",
    "expected result": "[expected outcome]"
  }
]
---
`, s.Content, testCasesJSON)
	return prompt, nil
}

// BuildAnalyzePrompt constructs a prompt to analyze execution feedback.
func (b *Builder) BuildAnalyzePrompt(s *seed.Seed, feedback string) (string, error) {
	if s == nil || feedback == "" {
		return "", fmt.Errorf("seed and feedback must be provided")
	}

	// Convert test cases to JSON for display
	testCasesJSON := "[\n"
	for i, tc := range s.TestCases {
		if i > 0 {
			testCasesJSON += ",\n"
		}
		testCasesJSON += fmt.Sprintf(`  {
    "running command": "%s",
    "expected result": "%s"
  }`, tc.RunningCommand, tc.ExpectedResult)
	}
	testCasesJSON += "\n]"

	prompt := fmt.Sprintf(`
[SEED]
Source (c):
---
%s
---

Test Cases (json):
---
%s
---
[/SEED]

[EXECUTION FEEDBACK]
%s
[/EXECUTION FEEDBACK]

Based on the system context and the execution feedback above, analyze what happened during the execution.
Provide insights about:
1. What vulnerability or behavior was triggered
2. Whether this is the expected result
3. Suggestions for further exploration

Please provide a concise but informative analysis.
`, s.Content, testCasesJSON, feedback)
	return prompt, nil
}
