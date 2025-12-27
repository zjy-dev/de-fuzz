package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/logger"
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
}

// Builder is responsible for constructing prompts for the LLM.
type Builder struct {
	// MaxTestCases specifies the maximum number of test cases to generate per seed
	// If 0, test case generation will be disabled in prompts
	MaxTestCases int

	// FunctionTemplate contains the C code template where LLM fills in function bodies
	// If empty, LLM generates complete programs
	FunctionTemplate string
}

// NewBuilder creates a new prompt builder.
// maxTestCases specifies the maximum number of test cases to generate per seed.
// If maxTestCases is 0, test case generation will be disabled in prompts.
// functionTemplate is optional - if provided, LLM only generates function bodies.
func NewBuilder(maxTestCases int, functionTemplate string) *Builder {
	return &Builder{
		MaxTestCases:     maxTestCases,
		FunctionTemplate: functionTemplate,
	}
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

	// Build the output format example based on MaxTestCases and FunctionTemplate
	var outputFormatExample string
	if b.FunctionTemplate != "" && b.MaxTestCases > 0 {
		// Function template + test cases mode
		outputFormatExample = "[function implementation here]\n" +
			"// ||||| JSON_TESTCASES_START |||||\n" +
			"[\n" +
			"  {\n" +
			"    \"running command\": \"[command to execute the compiled binary]\",\n" +
			"    \"expected result\": \"[expected output or behavior]\"\n" +
			"  }\n" +
			"]"
	} else if b.FunctionTemplate != "" {
		// Function-only mode (no test cases)
		outputFormatExample = "[function implementation here]"
	} else if b.MaxTestCases > 0 {
		// With test cases
		outputFormatExample = "[C source code here]\n" +
			"// ||||| JSON_TESTCASES_START |||||\n" +
			"[\n" +
			"  {\n" +
			"    \"running command\": \"[command to execute the compiled binary]\",\n" +
			"    \"expected result\": \"[expected output or behavior]\"\n" +
			"  }\n" +
			"]"
	} else {
		// Without test cases
		outputFormatExample = "[C source code here]"
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

	// Read template if configured
	var templateInstruction string
	if b.FunctionTemplate != "" {
		templateContent, err := os.ReadFile(b.FunctionTemplate)
		if err != nil {
			return "", fmt.Errorf("failed to read function template: %w", err)
		}
		templateInstruction = fmt.Sprintf(`
**Code Template:**
You will generate code based on this template. Only implement the function body marked with FUNCTION_PLACEHOLDER.

%s

**Template Instructions:**
- The template provides the main() function and program structure.
- You ONLY need to implement the function body where you see // FUNCTION_PLACEHOLDER: function_name
- Do NOT modify or include the template code in your output.
- Output ONLY the function implementation.
`, string(templateContent))
	}

	// Build output format based on MaxTestCases and FunctionTemplate
	var outputFormat string
	if b.FunctionTemplate != "" && b.MaxTestCases > 0 {
		// Function template + test cases mode
		outputFormat = fmt.Sprintf(`**Output Format (MUST follow exactly):**

Your response must be in this exact format - function implementation followed by the separator and JSON test cases:

void function_name(parameters) {
    // Your function implementation here
}
// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./prog arg1",
    "expected result": "expected output or behavior"
  }
]

**IMPORTANT:** 
- Output ONLY the function code, then the separator "// ||||| JSON_TESTCASES_START |||||", then the JSON array.
- Do NOT include the template or any other code besides the function.
- Do NOT include any markdown code blocks, headers, or other formatting.
- Include %d to %d test cases with running commands and expected results.`, 1, b.MaxTestCases)
	} else if b.FunctionTemplate != "" {
		// Function-only mode (no test cases)
		outputFormat = `**Output Format (MUST follow exactly):**

Your response must contain only the function implementation:

void vulnerable_function(char *input) {
    // Your function implementation here
}

**IMPORTANT:** 
- Output ONLY the function code.
- Do NOT include the template or any other code.
- Do NOT include any markdown code blocks, headers, or other formatting.`
	} else if b.MaxTestCases > 0 {
		outputFormat = `**Output Format (MUST follow exactly):**

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
- The separator must appear exactly as shown: // ||||| JSON_TESTCASES_START |||||`
	} else {
		outputFormat = `**Output Format (MUST follow exactly):**

Your response must contain only C source code:

[Your C source code here]

**IMPORTANT:** 
- Output ONLY the C source code.
- Do NOT include any markdown code blocks, headers, or other formatting.
- Do NOT include test cases - they are not needed for this configuration.`
	}

	prompt := fmt.Sprintf(`Generate a new, complete, and valid seed for fuzzing.
The seed must contain C source code%s.
Please ensure the code has potential vulnerabilities that can be discovered through fuzzing.

**Requirements:**
- Provide complete, compilable C source code%s.
- The code will be saved as 'source.c' and compiled using the target compiler.%s
- The code should be minimal but demonstrate a potential vulnerability.
- Focus on the specific ISA and defense strategy from the system context.

**ISA Stack Layout:**
%s
%s
%s
`,
		func() string {
			if b.MaxTestCases > 0 {
				return " and test cases"
			}
			return ""
		}(),
		func() string {
			if b.FunctionTemplate != "" {
				return " (function body only)"
			}
			return ""
		}(),
		func() string {
			if b.MaxTestCases > 0 {
				return fmt.Sprintf("\n- Include at least one test case (up to %d test cases) with a running command and expected result.\n- The running command should execute the compiled binary (e.g., \"./prog\", \"./prog arg1 arg2\").\n- The expected result describes the expected output or behavior.", b.MaxTestCases)
			}
			return ""
		}(),
		stackLayout,
		templateInstruction,
		outputFormat,
	)
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
		coverageSection = fmt.Sprintf(`
[COVERAGE CONTEXT]
Current Total Coverage: %.1f%% (%d/%d lines covered)

Coverage Increase from this seed:
%s

%s
[/COVERAGE CONTEXT]

Based on the coverage increase above, focus your mutation on:
1. Exploring similar code paths that led to the coverage increase
2. Varying the inputs that triggered the newly covered code
3. Trying edge cases around the newly covered functionality
`,
			mutationCtx.TotalCoveragePercentage,
			mutationCtx.TotalCoveredLines,
			mutationCtx.TotalLines,
			mutationCtx.CoverageIncreaseSummary,
			mutationCtx.CoverageIncreaseDetails,
		)
	}

	// Build output format based on MaxTestCases and FunctionTemplate
	var outputFormat string
	if b.FunctionTemplate != "" && b.MaxTestCases > 0 {
		// Function template + test cases mode
		outputFormat = fmt.Sprintf(`**Output Format (MUST follow exactly):**

Your response must be in this exact format - mutated function implementation followed by the separator and JSON test cases:

void function_name(parameters) {
    // Your mutated function implementation here
}
// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./prog arg1",
    "expected result": "expected output or behavior"
  }
]

**IMPORTANT:** 
- Output ONLY the function code, then the separator "// ||||| JSON_TESTCASES_START |||||", then the JSON array.
- Do NOT include the template or any other code besides the function.
- Do NOT include any markdown code blocks, headers, or other formatting.
- Include %d to %d test cases with running commands and expected results.`, 1, b.MaxTestCases)
	} else if b.FunctionTemplate != "" {
		// Function-only mode (no test cases)
		outputFormat = `**Output Format (MUST follow exactly):**

Your response must contain only the mutated function implementation:

void vulnerable_function(char *input) {
    // Your mutated function implementation here
}

**IMPORTANT:** 
- Output ONLY the function code.
- Do NOT include the template or any other code.
- Do NOT include any markdown code blocks, headers, or other formatting.`
	} else if b.MaxTestCases > 0 {
		outputFormat = `**Output Format (MUST follow exactly):**

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
- Do NOT include any markdown code blocks, headers, or other formatting.`
	} else {
		outputFormat = `**Output Format (MUST follow exactly):**

Your response must contain only mutated C source code:

[mutated C source code here]

**IMPORTANT:** 
- Output ONLY the mutated C source code.
- Do NOT include any markdown code blocks, headers, or other formatting.
- Do NOT include test cases - they are not needed for this configuration.`
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

%s
`, s.Content, testCasesJSON, coverageSection, outputFormat)

	// DEBUG: Print the generated mutate prompt at debug level
	logger.Debug("\n%s", strings.Repeat("=", 80))
	logger.Debug("BuildMutatePrompt - Generated Prompt:")
	logger.Debug("%s", strings.Repeat("-", 80))
	logger.Debug("%s", prompt)
	logger.Debug("%s\n", strings.Repeat("=", 80))

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

// DivergenceContext holds information about execution divergence for refined mutation.
type DivergenceContext struct {
	// Function names at the divergence point
	BaseFunction    string
	MutatedFunction string

	// Index in call sequence where divergence occurred
	DivergenceIndex int

	// Common function calls before divergence
	CommonPrefix []string

	// Divergent paths after the divergence point
	BasePath    []string
	MutatedPath []string

	// Formatted string for LLM (from DivergencePoint.ForLLM())
	FormattedReport string
}

// BuildDivergenceRefinedPrompt constructs a prompt for refined mutation based on divergence analysis.
// This is used when a mutated seed doesn't achieve the target coverage, and we want to guide
// the LLM using function-level divergence information.
func (b *Builder) BuildDivergenceRefinedPrompt(
	baseSeed *seed.Seed,
	mutatedSeed *seed.Seed,
	divCtx *DivergenceContext,
) (string, error) {
	if baseSeed == nil || mutatedSeed == nil {
		return "", fmt.Errorf("both baseSeed and mutatedSeed must be provided")
	}

	// Build output format based on MaxTestCases and FunctionTemplate
	var outputFormat string
	if b.FunctionTemplate != "" && b.MaxTestCases > 0 {
		outputFormat = fmt.Sprintf(`**Output Format:**
Output ONLY the function code, then "// ||||| JSON_TESTCASES_START |||||", then %d-%d test cases in JSON format.`, 1, b.MaxTestCases)
	} else if b.FunctionTemplate != "" {
		outputFormat = `**Output Format:**
Output ONLY the function implementation code.`
	} else if b.MaxTestCases > 0 {
		outputFormat = `**Output Format:**
Output C source code, then "// ||||| JSON_TESTCASES_START |||||", then JSON test cases.`
	} else {
		outputFormat = `**Output Format:**
Output ONLY the mutated C source code.`
	}

	// Build divergence section
	divergenceSection := ""
	if divCtx != nil && divCtx.FormattedReport != "" {
		divergenceSection = fmt.Sprintf(`
[DIVERGENCE ANALYSIS]
The previous mutation did not achieve the target coverage. Here's where the execution paths diverged:

%s

**What this means:**
- The base seed (which covered our target) called function '%s'
- Your mutated seed called function '%s' instead
- To reach the target coverage, your mutation needs to take the same path as the base seed

**Hint:** Look at the common prefix functions - these represent the shared execution path. 
The divergence function names often indicate what kind of code pattern is being compiled differently.
[/DIVERGENCE ANALYSIS]
`, divCtx.FormattedReport, divCtx.BaseFunction, divCtx.MutatedFunction)
	}

	prompt := fmt.Sprintf(`
[BASE SEED - This seed achieved the target coverage]
%s
[/BASE SEED]

[YOUR PREVIOUS MUTATION - This did NOT achieve target coverage]
%s
[/YOUR PREVIOUS MUTATION]
%s
[TASK]
Your previous mutation took a different execution path than the base seed. 
Please create a NEW mutation that:
1. Stays closer to the structure of the BASE SEED
2. Makes smaller, more conservative changes
3. Aims to trigger the same compiler code path as the base seed

Think about what syntax or code patterns in the base seed caused it to reach the target.
Your mutation should preserve those patterns while still introducing variation.

%s

**IMPORTANT:** Do NOT include markdown code blocks or explanations. Output only the code.
`, baseSeed.Content, mutatedSeed.Content, divergenceSection, outputFormat)

	logger.Debug("\n%s", strings.Repeat("=", 80))
	logger.Debug("BuildDivergenceRefinedPrompt - Generated Prompt:")
	logger.Debug("%s", strings.Repeat("-", 80))
	logger.Debug("%s", prompt)
	logger.Debug("%s\n", strings.Repeat("=", 80))

	return prompt, nil
}

// ParseLLMResponse parses the LLM response based on the Builder's configuration.
// It handles four modes:
// 1. FunctionTemplate + TestCases mode: Extracts function code with test cases, merges into template
// 2. FunctionTemplate only mode: Extracts function code and merges it into the template
// 3. No test cases mode (MaxTestCases == 0): Extracts code without test cases
// 4. Standard mode: Extracts code with test cases using ParseSeedFromLLMResponse
//
// Returns a Seed with Content and TestCases populated appropriately.
func (b *Builder) ParseLLMResponse(response string) (*seed.Seed, error) {
	// Mode 1: Function template + test cases mode
	if b.FunctionTemplate != "" && b.MaxTestCases > 0 {
		// Read the template
		templateContent, err := os.ReadFile(b.FunctionTemplate)
		if err != nil {
			return nil, fmt.Errorf("failed to read function template: %w", err)
		}

		// Parse function code and test cases from response
		functionCode, testCases, err := seed.ParseFunctionWithTestCasesFromLLMResponse(response)
		if err != nil {
			return nil, fmt.Errorf("failed to parse function with test cases from response: %w", err)
		}

		// Merge function into template
		mergedCode, err := seed.MergeTemplate(string(templateContent), functionCode)
		if err != nil {
			return nil, fmt.Errorf("failed to merge function into template: %w", err)
		}

		return &seed.Seed{
			Content:   mergedCode,
			TestCases: testCases,
		}, nil
	}

	// Mode 2: Function template only mode (no test cases)
	if b.FunctionTemplate != "" {
		// Read the template
		templateContent, err := os.ReadFile(b.FunctionTemplate)
		if err != nil {
			return nil, fmt.Errorf("failed to read function template: %w", err)
		}

		// Parse function code from response
		functionCode, err := seed.ParseFunctionFromLLMResponse(response)
		if err != nil {
			return nil, fmt.Errorf("failed to parse function from response: %w", err)
		}

		// Merge function into template
		mergedCode, err := seed.MergeTemplate(string(templateContent), functionCode)
		if err != nil {
			return nil, fmt.Errorf("failed to merge function into template: %w", err)
		}

		return &seed.Seed{
			Content:   mergedCode,
			TestCases: []seed.TestCase{}, // No test cases in template-only mode
		}, nil
	}

	// Mode 2: No test cases mode
	if b.MaxTestCases == 0 {
		sourceCode, err := seed.ParseCodeOnlyFromLLMResponse(response)
		if err != nil {
			return nil, fmt.Errorf("failed to parse code from response: %w", err)
		}

		return &seed.Seed{
			Content:   sourceCode,
			TestCases: []seed.TestCase{},
		}, nil
	}

	// Mode 3: Standard mode with test cases
	sourceCode, testCases, err := seed.ParseSeedFromLLMResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse seed from response: %w", err)
	}

	return &seed.Seed{
		Content:   sourceCode,
		TestCases: testCases,
	}, nil
}

// IsFunctionTemplateMode returns true if the builder is configured for function template mode
func (b *Builder) IsFunctionTemplateMode() bool {
	return b.FunctionTemplate != ""
}

// RequiresTestCases returns true if the builder requires test cases in responses
func (b *Builder) RequiresTestCases() bool {
	return b.MaxTestCases > 0
}
