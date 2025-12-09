package plugins

import (
	"fmt"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

func init() {
	oracle.Register("crash", NewCrashOracle)
}

// NewCrashOracle creates a new crash-detection oracle.
func NewCrashOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (oracle.Oracle, error) {
	return &CrashOracle{}, nil
}

// CrashOracle implements a simple oracle that only detects crashes.
type CrashOracle struct{}

// Analyze checks if any execution resulted in a crash.
func (o *CrashOracle) Analyze(s *seed.Seed, results []oracle.Result) (*oracle.Bug, error) {
	for i, res := range results {
		if oracle.IsCrashExit(res.ExitCode) {
			return &oracle.Bug{
				Seed:        s,
				Results:     results,
				Description: fmt.Sprintf("Crash detected in test case %d via exit code %d", i+1, res.ExitCode),
			}, nil
		}
	}
	return nil, nil
}
