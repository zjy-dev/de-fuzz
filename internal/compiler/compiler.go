package compiler

// Compiler defines the interface for a compiler.
type Compiler interface {
	// Compile compiles the given source code and returns the path to the compiled binary.
	Compile(sourceCode string) (string, error)
}