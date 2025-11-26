package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// MutationContext holds context information for seed mutation.
type MutationContext struct {
	// CoverageIncreaseSummary is a brief description of what coverage increased
	CoverageIncreaseSummary string

	// CoverageIncreaseDetails is the detailed formatted report of coverage increase
	CoverageIncreaseDetails string

	// TotalCoveragePercentage is the current total coverage percentage
	TotalCoveragePercentage float64

	// TotalCoveredLines is the number of lines currently covered
	TotalCoveredLines int

	// TotalLines is the total number of lines to cover
	TotalLines int

	// UncoveredAbstract is the abstracted code showing uncovered paths
	// This helps LLM understand what code paths are not yet covered
	UncoveredAbstract string
}

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

	// Build the output format example - use the same format as storage.Separator
	// Format: <C source code> + "// ||||| JSON_TESTCASES_START |||||" + <JSON test cases>
	outputFormatExample := "[C source code here]\n" +
		"// ||||| JSON_TESTCASES_START |||||\n" +
		"[\n" +
		"  {\n" +
		"    \"running command\": \"[command to execute the compiled binary]\",\n" +
		"    \"expected result\": \"[expected output or behavior]\"\n" +
		"  }\n" +
		"]"

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

1.  **## Goal**: A clear and concise statement of the objective. (e.g., "Your goal is to generate and mutate C code to discover vulnerabilities in the '%s' defense strategy on the '%s' architecture.")

2.  **## Core Concepts**: A detailed explanation of the defense strategy and the ISA's stack layout. You must synthesize the provided context, not just copy it. Explain *how* the defense works and what its theoretical weaknesses are.

3.  **## Attack Vectors & Vulnerability Patterns**: This is the most critical section. Provide a bulleted list of specific, actionable attack ideas and C code patterns to try. Be creative and think like an attacker. Examples:
    *   Integer overflows to bypass bounds checks.
    *   Tricky pointer arithmetic to confuse alias analysis.
    *   Using longjmp or other control-flow manipulation to skip security checks.
    *   Exploiting format string vulnerabilities in novel ways.

4.  **## Seed Generation & Mutation Rules**: Clear instructions for the AI on how to format its output. The AI must produce:
    *   Complete C source code that compiles with the target compiler.
    *   Test cases in JSON format, each containing a "running command" and "expected result".
    *   Mutations should be small, focused, and intelligent.

**[OUTPUT FORMAT]**

The generated System Prompt must instruct the AI to format its output as follows:

%s

**[OUTPUT INSTRUCTIONS]**

-   You must only output the generated **System Prompt**.
-   Do not include any other text, conversation, or explanations.
-   The output should be formatted in Markdown.
`, isa, strategy, stackLayout, strategy, isa, outputFormatExample)

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

	prompt := fmt.Sprintf(`Generate a new, complete, and valid seed for fuzzing.
The seed must contain C source code and test cases.
Please ensure the code has potential vulnerabilities that can be discovered through fuzzing.

**Requirements:**
- Provide complete, compilable C source code.
- The code will be saved as 'source.c' and compiled using the target compiler.
- Include at least one test case with a running command and expected result.
- The running command should execute the compiled binary (e.g., "./prog", "./prog arg1 arg2").
- The expected result describes the expected output or behavior.
- The code should be minimal but demonstrate a potential vulnerability.
- Focus on the specific ISA and defense strategy from the system context.

**ISA Stack Layout:**
%s

**Output Format (MUST follow exactly):**

Your response must be in this exact format - C source code followed by the separator and JSON test cases:

[Your C source code here]
// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./prog",
    "expected result": "expected output or behavior"
  }
]

**IMPORTANT:** 
- Output ONLY the C source code, then the separator "// ||||| JSON_TESTCASES_START |||||", then the JSON array.
- Do NOT include any markdown code blocks, headers, or other formatting.
- The separator must appear exactly as shown: // ||||| JSON_TESTCASES_START |||||
`, stackLayout)
	return prompt, nil
}

// BuildMutatePrompt constructs a prompt to mutate an existing seed.
// If mutationCtx is provided, it includes coverage information for smarter mutation.
func (b *Builder) BuildMutatePrompt(s *seed.Seed, mutationCtx *MutationContext) (string, error) {
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

	// Build coverage context section if available
	coverageSection := ""
	if mutationCtx != nil {
		// Build uncovered abstract section if available
		uncoveredSection := ""
		if mutationCtx.UncoveredAbstract != "" {
			uncoveredSection = fmt.Sprintf(`
[UNCOVERED CODE PATHS]
The following shows abstracted code with uncovered paths (lines marked with full code are NOT covered yet):

%s
[/UNCOVERED CODE PATHS]
`, mutationCtx.UncoveredAbstract)
		}

		coverageSection = fmt.Sprintf(`
[COVERAGE CONTEXT]
Current Total Coverage: %.1f%% (%d/%d lines covered)

Coverage Increase from this seed:
%s

%s
[/COVERAGE CONTEXT]
%s
Based on the coverage increase above, focus your mutation on:
1. Exploring similar code paths that led to the coverage increase
2. Varying the inputs that triggered the newly covered code
3. Trying edge cases around the newly covered functionality
4. Targeting the UNCOVERED CODE PATHS shown above to increase coverage
`,
			mutationCtx.TotalCoveragePercentage,
			mutationCtx.TotalCoveredLines,
			mutationCtx.TotalLines,
			mutationCtx.CoverageIncreaseSummary,
			mutationCtx.CoverageIncreaseDetails,
			uncoveredSection,
		)
	}

	prompt := fmt.Sprintf(`
[EXISTING SEED]
%s
// ||||| JSON_TESTCASES_START |||||
%s
[/EXISTING SEED]
%s
Based on the system context, mutate the existing seed to create a new variant that is more likely to find a bug or increase coverage.
Please make focused changes that could expose different vulnerability patterns.

**Output Format (MUST follow exactly):**

Your response must be in this exact format - C source code followed by the separator and JSON test cases:

[mutated C source code here]
// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./prog",
    "expected result": "expected output or behavior"
  }
]

**IMPORTANT:** 
- Output ONLY the C source code, then the separator "// ||||| JSON_TESTCASES_START |||||", then the JSON array.
- Do NOT include any markdown code blocks, headers, or other formatting.
`, s.Content, testCasesJSON, coverageSection)

	// DEBUG: Print the generated mutate prompt
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("[DEBUG] BuildMutatePrompt - Generated Prompt:")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Println(prompt)
	fmt.Println(strings.Repeat("=", 80) + "\n")

	return prompt, nil
}

// BuildMutatePromptSimple constructs a simple mutation prompt without coverage context.
// This is provided for backward compatibility.
func (b *Builder) BuildMutatePromptSimple(s *seed.Seed) (string, error) {
	return b.BuildMutatePrompt(s, nil)
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
%s
// ||||| JSON_TESTCASES_START |||||
%s
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
