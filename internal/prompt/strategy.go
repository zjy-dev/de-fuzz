package prompt

import (
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// PromptContext holds all context information needed to build a mutation prompt.
// Different strategies can choose which fields to use.
type PromptContext struct {
	// ExistingSeed is the seed to mutate
	ExistingSeed *seed.Seed

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
	UncoveredAbstract string
}

// Strategy defines how to build a mutation prompt for the LLM.
// Different strategies implement different ablation experiment configurations.
type Strategy interface {
	// Name returns the unique identifier for this strategy
	Name() string

	// Build constructs the prompt string from the given context
	Build(ctx *PromptContext) (string, error)
}

// NewStrategy creates a Strategy based on the strategy name.
// Valid names: "standard", "no-abstract", "no-coverage", "random"
func NewStrategy(name string) Strategy {
	switch name {
	case "standard", "":
		return &StandardStrategy{}
	case "no-abstract":
		return &NoAbstractStrategy{}
	case "no-coverage":
		return &NoCoverageStrategy{}
	case "random":
		return &RandomMutationStrategy{}
	default:
		// Default to standard if unknown
		return &StandardStrategy{}
	}
}

// buildSeedSection builds the common seed display section
func buildSeedSection(s *seed.Seed) string {
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

	return fmt.Sprintf(`[EXISTING SEED]
%s
// ||||| JSON_TESTCASES_START |||||
%s
[/EXISTING SEED]`, s.Content, testCasesJSON)
}

// buildOutputFormatSection builds the common output format instructions
func buildOutputFormatSection() string {
	return `**Output Format (MUST follow exactly):**

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
}

// debugPrintPrompt prints the generated prompt for debugging
func debugPrintPrompt(strategyName, prompt string) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("[DEBUG] %s Strategy - Generated Prompt:\n", strategyName)
	fmt.Println(strings.Repeat("-", 80))
	fmt.Println(prompt)
	fmt.Println(strings.Repeat("=", 80) + "\n")
}

// =============================================================================
// StandardStrategy - Full context including coverage and abstract
// =============================================================================

// StandardStrategy is the baseline strategy that includes all available context:
// seed code, coverage information, and uncovered code abstract.
type StandardStrategy struct{}

// Name returns the strategy name
func (s *StandardStrategy) Name() string {
	return "standard"
}

// Build constructs a full-context mutation prompt
func (s *StandardStrategy) Build(ctx *PromptContext) (string, error) {
	if ctx.ExistingSeed == nil {
		return "", fmt.Errorf("seed must be provided")
	}

	seedSection := buildSeedSection(ctx.ExistingSeed)

	// Build uncovered abstract section if available
	uncoveredSection := ""
	if ctx.UncoveredAbstract != "" {
		uncoveredSection = fmt.Sprintf(`
[UNCOVERED CODE PATHS]
The following shows abstracted code with uncovered paths (lines marked with full code are NOT covered yet):

%s
[/UNCOVERED CODE PATHS]
`, ctx.UncoveredAbstract)
	}

	// Build coverage context section
	coverageSection := fmt.Sprintf(`
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
		ctx.TotalCoveragePercentage,
		ctx.TotalCoveredLines,
		ctx.TotalLines,
		ctx.CoverageIncreaseSummary,
		ctx.CoverageIncreaseDetails,
		uncoveredSection,
	)

	prompt := fmt.Sprintf(`%s
%s
Based on the system context, mutate the existing seed to create a new variant that is more likely to find a bug or increase coverage.
Please make focused changes that could expose different vulnerability patterns.

%s
`, seedSection, coverageSection, buildOutputFormatSection())

	debugPrintPrompt("Standard", prompt)
	return prompt, nil
}

// =============================================================================
// NoAbstractStrategy - Coverage info but no uncovered code abstract
// =============================================================================

// NoAbstractStrategy includes coverage information but excludes the uncovered
// code abstract. Used to measure the contribution of code abstraction.
type NoAbstractStrategy struct{}

// Name returns the strategy name
func (s *NoAbstractStrategy) Name() string {
	return "no-abstract"
}

// Build constructs a prompt with coverage but without abstract
func (s *NoAbstractStrategy) Build(ctx *PromptContext) (string, error) {
	if ctx.ExistingSeed == nil {
		return "", fmt.Errorf("seed must be provided")
	}

	seedSection := buildSeedSection(ctx.ExistingSeed)

	// Build coverage context section WITHOUT abstract
	coverageSection := fmt.Sprintf(`
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
		ctx.TotalCoveragePercentage,
		ctx.TotalCoveredLines,
		ctx.TotalLines,
		ctx.CoverageIncreaseSummary,
		ctx.CoverageIncreaseDetails,
	)

	prompt := fmt.Sprintf(`%s
%s
Based on the system context, mutate the existing seed to create a new variant that is more likely to find a bug or increase coverage.
Please make focused changes that could expose different vulnerability patterns.

%s
`, seedSection, coverageSection, buildOutputFormatSection())

	debugPrintPrompt("NoAbstract", prompt)
	return prompt, nil
}

// =============================================================================
// NoCoverageStrategy - Only seed code, no coverage feedback
// =============================================================================

// NoCoverageStrategy includes only the seed code without any coverage
// information. Used to measure the contribution of coverage-guided feedback.
type NoCoverageStrategy struct{}

// Name returns the strategy name
func (s *NoCoverageStrategy) Name() string {
	return "no-coverage"
}

// Build constructs a prompt with only seed code
func (s *NoCoverageStrategy) Build(ctx *PromptContext) (string, error) {
	if ctx.ExistingSeed == nil {
		return "", fmt.Errorf("seed must be provided")
	}

	seedSection := buildSeedSection(ctx.ExistingSeed)

	prompt := fmt.Sprintf(`%s

Mutate the existing seed to create a new variant that is more likely to find a bug.
Please make focused changes that could expose different vulnerability patterns.

%s
`, seedSection, buildOutputFormatSection())

	debugPrintPrompt("NoCoverage", prompt)
	return prompt, nil
}

// =============================================================================
// RandomMutationStrategy - Minimal guidance, purely random-like mutation
// =============================================================================

// RandomMutationStrategy provides minimal guidance to the LLM, simulating
// a random mutation baseline. Used as the zero-baseline for comparison.
type RandomMutationStrategy struct{}

// Name returns the strategy name
func (s *RandomMutationStrategy) Name() string {
	return "random"
}

// Build constructs a minimal prompt for random-like mutation
func (s *RandomMutationStrategy) Build(ctx *PromptContext) (string, error) {
	if ctx.ExistingSeed == nil {
		return "", fmt.Errorf("seed must be provided")
	}

	seedSection := buildSeedSection(ctx.ExistingSeed)

	prompt := fmt.Sprintf(`%s

Generate a random variation of the above C code. Make arbitrary changes to the code structure, variables, or logic.

%s
`, seedSection, buildOutputFormatSection())

	debugPrintPrompt("Random", prompt)
	return prompt, nil
}
