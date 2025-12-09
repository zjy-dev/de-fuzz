package oracle

import (
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

func init() {
	Register("llm", NewLLMOracle)
}

// NewLLMOracle creates a new LLM-based oracle.
func NewLLMOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
	if l == nil {
		return nil, fmt.Errorf("LLM client is required for LLM oracle")
	}
	if prompter == nil {
		return nil, fmt.Errorf("prompt builder is required for LLM oracle")
	}
	return &LLMOracle{
		llm:        l,
		prompter:   prompter,
		llmContext: context,
	}, nil
}

// LLMOracle implements the Oracle interface using an LLM for analysis.
type LLMOracle struct {
	llm        llm.LLM
	prompter   *prompt.Builder
	llmContext string // The "understanding" context from the LLM
}

// Analyze uses the LLM to determine if the execution results indicate a bug.
// ctx is not used by LLMOracle as it's a passive oracle.
func (o *LLMOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
	if len(results) == 0 {
		return nil, fmt.Errorf("no execution results to analyze")
	}

	// First, perform basic checks for obvious crashes or anomalies
	var anomalies []string
	hasAnomaly := false

	for i, result := range results {
		// Check for non-zero exit code
		if result.ExitCode != 0 {
			anomalies = append(anomalies, fmt.Sprintf("Test case %d: non-zero exit code %d", i+1, result.ExitCode))
			hasAnomaly = true
		}

		// Check for crash indicators in output
		if containsCrashIndicators(result.Stdout) || containsCrashIndicators(result.Stderr) {
			anomalies = append(anomalies, fmt.Sprintf("Test case %d: crash detected in output", i+1))
			hasAnomaly = true
		}

		// Check for unexpected stderr output
		if result.Stderr != "" && !isExpectedError(result.Stderr) {
			anomalies = append(anomalies, fmt.Sprintf("Test case %d: unexpected stderr: %s", i+1, result.Stderr))
			hasAnomaly = true
		}
	}

	// If no obvious anomalies, no bug
	if !hasAnomaly {
		return nil, nil
	}

	// Use LLM for deeper analysis
	feedback := strings.Join(anomalies, "\n")
	analysisPrompt, err := o.prompter.BuildAnalyzePrompt(s, feedback)
	if err != nil {
		return nil, fmt.Errorf("failed to build analysis prompt: %w", err)
	}

	description, err := o.llm.Analyze(o.llmContext, analysisPrompt, s, feedback)
	if err != nil {
		// If LLM analysis fails, fall back to basic description
		description = fmt.Sprintf("Execution anomalies detected:\n%s", feedback)
	}

	return &Bug{
		Seed:        s,
		Results:     results,
		Description: description,
	}, nil
}

// containsCrashIndicators checks if output contains common crash indicators.
func containsCrashIndicators(output string) bool {
	crashKeywords := []string{
		"segmentation fault", "segfault", "core dumped",
		"stack overflow", "buffer overflow", "heap corruption",
		"double free", "use after free", "invalid memory",
		"abort", "assertion failed", "bus error",
		"stack smashing detected", "*** stack smashing detected ***",
	}

	lowerOutput := strings.ToLower(output)
	for _, keyword := range crashKeywords {
		if strings.Contains(lowerOutput, keyword) {
			return true
		}
	}
	return false
}

// isExpectedError checks if stderr output is expected/benign.
func isExpectedError(stderr string) bool {
	// Common benign stderr messages
	benignPatterns := []string{
		"warning:", // Compiler warnings are usually OK
		"note:",    // Compiler notes are informational
	}

	lowerStderr := strings.ToLower(stderr)
	for _, pattern := range benignPatterns {
		if strings.Contains(lowerStderr, pattern) {
			return true
		}
	}
	return false
}
