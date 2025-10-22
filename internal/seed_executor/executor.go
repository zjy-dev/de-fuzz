package executor

import (
	"defuzz/internal/seed"
	"defuzz/internal/vm"
)

// ExecutionResult holds the outcome of a single command execution.
type ExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Executor defines the interface for executing a seed.
type Executor interface {
	// Execute uses the provided VM to run all test cases for the seed.
	Execute(s *seed.Seed, v vm.VM) ([]ExecutionResult, error)
}
