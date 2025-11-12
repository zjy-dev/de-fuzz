package analysis

import (
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

// BasicAnalyzer is a simple implementation of the Analyzer interface.
type BasicAnalyzer struct{}

// NewBasicAnalyzer creates a new BasicAnalyzer.
func NewBasicAnalyzer() *BasicAnalyzer {
	return &BasicAnalyzer{}
}

// AnalyzeResult interprets the execution result to determine if a bug was found.
func (a *BasicAnalyzer) AnalyzeResult(s *seed.Seed, results []executor.ExecutionResult, l llm.LLM, pb *prompt.Builder, ctx string) (*Bug, error) {
	// Analyze the first result for simplicity in basic analyzer
	if len(results) == 0 {
		return nil, nil
	}

	result := results[0]
	if result.ExitCode != 0 || strings.Contains(result.Stderr, "error") {
		// Potential bug found, use LLM to confirm
		analysisPrompt, err := pb.BuildAnalyzePrompt(s, result.Stderr)
		if err != nil {
			return nil, fmt.Errorf("failed to build analysis prompt: %w", err)
		}

		analysis, err := l.Analyze(ctx, analysisPrompt, s, result.Stderr)
		if err != nil {
			return nil, fmt.Errorf("failed to analyze result with LLM: %w", err)
		}

		if strings.Contains(strings.ToLower(analysis), "vulnerability") {
			return &Bug{
				Seed:        s,
				Results:     results,
				Description: analysis,
			}, nil
		}
	}

	return nil, nil
}
