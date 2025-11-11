package executor

import (
	"defuzz/internal/seed"
)

// ExecutionResult holds the outcome of a single command execution.
type ExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Executor defines the interface for executing a seed.
type Executor interface {
	// Execute runs all test cases for the seed on the host machine.
	Execute(s *seed.Seed) ([]ExecutionResult, error)
}
