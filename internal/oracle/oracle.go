package oracle

import (
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// Result represents the execution result that needs to be analyzed.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Bug represents a discovered vulnerability.
type Bug struct {
	Seed        *seed.Seed
	Results     []Result
	Description string
}

// AnalyzeContext provides context for Oracle analysis.
// Some oracles (like CanaryOracle) need to execute the binary themselves.
type AnalyzeContext struct {
	// BinaryPath is the path to the compiled binary
	BinaryPath string
	// Executor is an interface to run the binary (optional, can be nil for passive oracles)
	Executor Executor
}

// Executor is a minimal interface for running binaries.
// This allows oracles to execute binaries with custom inputs.
type Executor interface {
	// ExecuteWithInput runs the binary with the given stdin input and returns the exit code.
	ExecuteWithInput(binaryPath string, stdin string) (exitCode int, stdout string, stderr string, err error)
	// ExecuteWithArgs runs the binary with the given command line arguments and returns the exit code.
	ExecuteWithArgs(binaryPath string, args ...string) (exitCode int, stdout string, stderr string, err error)
}

// Oracle determines if a seed execution has found a bug.
type Oracle interface {
	// Analyze analyzes the execution result of a seed and returns a Bug if found, nil otherwise.
	// ctx provides optional context for active oracles that need to execute the binary.
	// results contains the initial execution results from the fuzzing engine.
	Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error)
}

// IsCrashExit determines if an exit code indicates a crash.
// Common crash signals: SIGSEGV (11), SIGBUS (7), SIGABRT (6), SIGFPE (8), SIGILL (4)
// On Unix, signal exits are typically 128 + signal number.
func IsCrashExit(exitCode int) bool {
	crashSignals := map[int]bool{
		128 + 4:  true, // SIGILL
		128 + 6:  true, // SIGABRT
		128 + 7:  true, // SIGBUS
		128 + 8:  true, // SIGFPE
		128 + 11: true, // SIGSEGV
	}
	return crashSignals[exitCode]
}
