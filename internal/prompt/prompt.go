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

// BuildGeneratePrompt constructs a prompt to generate a new seed.
func (b *Builder) BuildGeneratePrompt(basePath string) (string, error) {
	// Read stack layout if available (optional)
	stackLayoutSection := ""
	stackLayoutPath := filepath.Join(basePath, "stack_layout.md")
	if stackLayout, err := os.ReadFile(stackLayoutPath); err == nil {
		stackLayoutSection = fmt.Sprintf("\n**Stack Layout Reference:**\n%s\n", string(stackLayout))
	}

	// Read template if configured
	var templateSection string
	if b.FunctionTemplate != "" {
		templateContent, err := os.ReadFile(b.FunctionTemplate)
		if err != nil {
			return "", fmt.Errorf("failed to read function template: %w", err)
		}
		templateSection = fmt.Sprintf(`
**Code Template:**
Implement ONLY the function marked with FUNCTION_PLACEHOLDER. Do NOT include the template.

%s
`, string(templateContent))
	}

	// Build output format
	outputFormat := b.buildOutputFormat()

	// Build the prompt
	var prompt strings.Builder
	prompt.WriteString("Generate C code for compiler fuzzing.\n\n")

	if b.FunctionTemplate != "" {
		prompt.WriteString("**Task:** Implement the function body that tests compiler security features.\n\n")
	} else {
		prompt.WriteString("**Task:** Generate complete C source code that tests compiler security features.\n\n")
	}

	prompt.WriteString(`**Requirements:**
- Complete, compilable C99/C11 code
- Focus on patterns that may trigger compiler bugs: buffer/integer overflows, format strings, pointer manipulation
- Output ONLY code, no explanations
`)

	if b.MaxTestCases > 0 {
		prompt.WriteString(fmt.Sprintf("- Include 1-%d test cases after the code\n", b.MaxTestCases))
	}

	prompt.WriteString(stackLayoutSection)
	prompt.WriteString(templateSection)
	prompt.WriteString("\n")
	prompt.WriteString(outputFormat)

	return prompt.String(), nil
}

// buildOutputFormat returns the output format instructions based on configuration.
func (b *Builder) buildOutputFormat() string {
	if b.FunctionTemplate != "" && b.MaxTestCases > 0 {
		return fmt.Sprintf(`**Output Format:**
[function_code]
// ||||| JSON_TESTCASES_START |||||
[{"running command": "./prog", "expected result": "..."}]

Output ONLY function code, then separator, then %d-%d JSON test cases. No markdown.`, 1, b.MaxTestCases)
	}
	if b.FunctionTemplate != "" {
		return `**Output Format:**
[function_code]

Output ONLY the function implementation. No markdown, no explanations.`
	}
	if b.MaxTestCases > 0 {
		return `**Output Format:**
[C source code]
// ||||| JSON_TESTCASES_START |||||
[{"running command": "./prog", "expected result": "..."}]

Output code, separator, then JSON test cases. No markdown.`
	}
	return `**Output Format:**
[C source code]

Output ONLY C source code. No markdown, no explanations.`
}

// BuildMutatePrompt constructs a prompt to mutate an existing seed.
// If mutationCtx is provided, it includes coverage information for smarter mutation.
func (b *Builder) BuildMutatePrompt(s *seed.Seed, mutationCtx *MutationContext) (string, error) {
	if s == nil {
		return "", fmt.Errorf("seed must be provided")
	}

	var prompt strings.Builder

	// Include the existing seed
	prompt.WriteString("**Existing Seed to Mutate:**\n```c\n")
	prompt.WriteString(s.Content)
	prompt.WriteString("\n```\n\n")

	// Include test cases if any
	if len(s.TestCases) > 0 {
		prompt.WriteString("**Test Cases:**\n")
		for _, tc := range s.TestCases {
			prompt.WriteString(fmt.Sprintf("- Command: `%s` → Expected: %s\n", tc.RunningCommand, tc.ExpectedResult))
		}
		prompt.WriteString("\n")
	}

	// Build coverage context section if available
	if mutationCtx != nil && mutationCtx.TotalCoveragePercentage > 0 {
		prompt.WriteString(fmt.Sprintf(`**Coverage Context:**
- Current coverage: %.1f%% (%d/%d lines)
%s

Focus mutations on:
1. Similar patterns that increased coverage
2. Edge cases around newly covered code

`, mutationCtx.TotalCoveragePercentage, mutationCtx.TotalCoveredLines, mutationCtx.TotalLines, mutationCtx.CoverageIncreaseSummary))
	}

	prompt.WriteString(`**Task:** Mutate this seed to explore different compiler code paths.

**Requirements:**
- Make focused, meaningful changes
- Preserve overall structure and main()
- Target different compiler optimizations or security checks
- Output ONLY code, no explanations

`)
	prompt.WriteString(b.buildOutputFormat())

	return prompt.String(), nil
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
