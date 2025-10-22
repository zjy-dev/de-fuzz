package analysis

import (
	"fmt"
	"strings"

	"defuzz/internal/llm"
	"defuzz/internal/prompt"
	"defuzz/internal/seed"
	executor "defuzz/internal/seed_executor"
)

// Bug represents a discovered vulnerability.
type Bug struct {
	Seed        *seed.Seed
	Results     []executor.ExecutionResult
	Description string
}

// Analyzer defines the interface for analyzing execution feedback.
type Analyzer interface {
	// AnalyzeResult interprets the execution results to determine if a bug was found.
	AnalyzeResult(s *seed.Seed, results []executor.ExecutionResult, l llm.LLM, pb *prompt.Builder, ctx string) (*Bug, error)
}

// LLMAnalyzer implements the Analyzer interface using an LLM to analyze results.
type LLMAnalyzer struct{}

// NewLLMAnalyzer creates a new LLM-based analyzer.
func NewLLMAnalyzer() *LLMAnalyzer {
	return &LLMAnalyzer{}
}

// AnalyzeResult compares expected vs actual results and uses LLM for deeper analysis.
func (a *LLMAnalyzer) AnalyzeResult(s *seed.Seed, results []executor.ExecutionResult, l llm.LLM, pb *prompt.Builder, ctx string) (*Bug, error) {
	if len(results) != len(s.TestCases) {
		return nil, fmt.Errorf("result count (%d) does not match test case count (%d)", len(results), len(s.TestCases))
	}

	var anomalies []string
	var hasAnomalies bool

	// Compare expected vs actual results for each test case
	for i, result := range results {
		testCase := s.TestCases[i]
		expectedResult := testCase.ExpectedResult

		// Check for obvious anomalies
		if result.ExitCode != 0 {
			anomalies = append(anomalies, fmt.Sprintf("Test case %d: unexpected exit code %d", i+1, result.ExitCode))
			hasAnomalies = true
		}

		// Check if output contains crash indicators
		if containsCrashIndicators(result.Stdout) || containsCrashIndicators(result.Stderr) {
			anomalies = append(anomalies, fmt.Sprintf("Test case %d: crash detected in output", i+1))
			hasAnomalies = true
		}

		// Simple string comparison for expected result (could be enhanced with regex/pattern matching)
		if expectedResult != "any" && !strings.Contains(result.Stdout, expectedResult) && !strings.Contains(result.Stderr, expectedResult) {
			anomalies = append(anomalies, fmt.Sprintf("Test case %d: expected '%s' but got stdout='%s' stderr='%s'",
				i+1, expectedResult, result.Stdout, result.Stderr))
			hasAnomalies = true
		}
	}

	// If no anomalies detected, return nil (no bug found)
	if !hasAnomalies {
		return nil, nil
	}

	// Use LLM for deeper analysis of the anomalies
	feedback := strings.Join(anomalies, "\n")
	analysisPrompt, err := pb.BuildAnalyzePrompt(s, feedback)
	if err != nil {
		return nil, fmt.Errorf("failed to build analysis prompt: %w", err)
	}

	description, err := l.Analyze(ctx, analysisPrompt, s, feedback)
	if err != nil {
		// Fallback to basic description if LLM analysis fails
		description = fmt.Sprintf("Execution anomalies detected:\n%s", feedback)
	}

	return &Bug{
		Seed:        s,
		Results:     results,
		Description: description,
	}, nil
}

// containsCrashIndicators checks if the output contains common crash indicators.
func containsCrashIndicators(output string) bool {
	crashKeywords := []string{
		"segmentation fault", "segfault", "core dumped",
		"stack overflow", "buffer overflow", "heap corruption",
		"double free", "use after free", "invalid memory",
		"abort", "assertion failed", "bus error",
	}

	lowerOutput := strings.ToLower(output)
	for _, keyword := range crashKeywords {
		if strings.Contains(lowerOutput, keyword) {
			return true
		}
	}
	return false
}
