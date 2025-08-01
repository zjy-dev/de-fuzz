package analysis

import (
	"defuzz/internal/llm"
	"defuzz/internal/prompt"
	"defuzz/internal/seed"
	"defuzz/internal/vm"
)

// Bug represents a discovered vulnerability.
type Bug struct {
	Seed   *seed.Seed
	Result *vm.ExecutionResult
	Description string
}

// Analyzer defines the interface for analyzing execution feedback.
type Analyzer interface {
	// AnalyzeResult interprets the execution result to determine if a bug was found.
	AnalyzeResult(s *seed.Seed, result *vm.ExecutionResult, l llm.LLM, pb *prompt.Builder, ctx string) (*Bug, error)
}