package executor

import (
	"defuzz/internal/seed"
	"defuzz/internal/vm"
)

// Executor defines the interface for executing a seed.
type Executor interface {
	// Execute uses the provided VM to execute the seed.
	Execute(s *seed.Seed, v vm.VM) (*vm.ExecutionResult, error)
}
