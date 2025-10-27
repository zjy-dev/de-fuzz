package oracle

import (
	"defuzz/internal/seed"
)

// Result represents the execution result that needs to be analyzed.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Oracle determines if a seed execution has found a bug.
type Oracle interface {
	// Analyze analyzes the execution result of a seed and returns true if a bug is found.
	// If a bug is found, it returns a description of the bug.
	Analyze(s *seed.Seed, results []Result) (bugFound bool, description string, err error)
}
