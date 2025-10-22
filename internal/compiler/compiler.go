package compiler

import "defuzz/internal/seed"

// Compiler defines the interface for a compiler.
type Compiler interface {
	// Compile compiles the given seed and returns the path to the compiled binary.
	Compile(s *seed.Seed, commandPath string) (string, error)
}
