package oracle

import (
	"fmt"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

func init() {
	Register("crash", NewCrashOracle)
}

// NewCrashOracle creates a new crash-detection oracle.
func NewCrashOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
	return &CrashOracle{}, nil
}

// CrashOracle implements a simple oracle that only detects crashes.
type CrashOracle struct{}

// Analyze checks if any execution resulted in a crash.
// ctx is not used by CrashOracle as it's a passive oracle.
func (o *CrashOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
	for i, res := range results {
		if IsCrashExit(res.ExitCode) {
			return &Bug{
				Seed:        s,
				Results:     results,
				Description: fmt.Sprintf("Crash detected in test case %d via exit code %d", i+1, res.ExitCode),
			}, nil
		}
	}
	return nil, nil
}
