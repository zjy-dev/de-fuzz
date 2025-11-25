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

// Oracle determines if a seed execution has found a bug.
type Oracle interface {
	// Analyze analyzes the execution result of a seed and returns a Bug if found, nil otherwise.
	Analyze(s *seed.Seed, results []Result) (*Bug, error)
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
