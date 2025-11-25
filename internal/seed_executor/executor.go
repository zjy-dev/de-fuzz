package executor

import (
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/seed"
	"github.com/zjy-dev/de-fuzz/internal/vm"
)

// ExecutionResult holds the outcome of a single command execution.
type ExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Executor defines the interface for executing a seed.
type Executor interface {
	// Execute runs all test cases for the seed.
	Execute(s *seed.Seed, binaryPath string) ([]ExecutionResult, error)
}

// LocalExecutor executes seeds on the local machine.
type LocalExecutor struct {
	vm         vm.VM
	timeoutSec int
}

// NewLocalExecutor creates a new local executor.
func NewLocalExecutor(timeoutSec int) *LocalExecutor {
	return &LocalExecutor{
		vm:         vm.NewLocalVM(),
		timeoutSec: timeoutSec,
	}
}

// Execute runs all test cases for the seed locally.
func (e *LocalExecutor) Execute(s *seed.Seed, binaryPath string) ([]ExecutionResult, error) {
	results := make([]ExecutionResult, 0, len(s.TestCases))

	for _, tc := range s.TestCases {
		// Parse the running command to extract arguments
		args := parseCommand(tc.RunningCommand, binaryPath)

		var vmResult *vm.ExecutionResult
		var err error

		if e.timeoutSec > 0 {
			vmResult, err = e.vm.RunWithTimeout(binaryPath, e.timeoutSec, args...)
		} else {
			vmResult, err = e.vm.Run(binaryPath, args...)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to execute test case: %w", err)
		}

		results = append(results, ExecutionResult{
			Stdout:   vmResult.Stdout,
			Stderr:   vmResult.Stderr,
			ExitCode: vmResult.ExitCode,
		})
	}

	return results, nil
}

// QEMUExecutor executes seeds using QEMU emulation.
type QEMUExecutor struct {
	vm         vm.VM
	timeoutSec int
}

// NewQEMUExecutor creates a new QEMU executor.
func NewQEMUExecutor(cfg vm.QEMUConfig, timeoutSec int) *QEMUExecutor {
	return &QEMUExecutor{
		vm:         vm.NewQEMUVM(cfg),
		timeoutSec: timeoutSec,
	}
}

// Execute runs all test cases for the seed using QEMU.
func (e *QEMUExecutor) Execute(s *seed.Seed, binaryPath string) ([]ExecutionResult, error) {
	results := make([]ExecutionResult, 0, len(s.TestCases))

	for _, tc := range s.TestCases {
		// Parse the running command to extract arguments
		args := parseCommand(tc.RunningCommand, binaryPath)

		var vmResult *vm.ExecutionResult
		var err error

		if e.timeoutSec > 0 {
			vmResult, err = e.vm.RunWithTimeout(binaryPath, e.timeoutSec, args...)
		} else {
			vmResult, err = e.vm.Run(binaryPath, args...)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to execute test case: %w", err)
		}

		results = append(results, ExecutionResult{
			Stdout:   vmResult.Stdout,
			Stderr:   vmResult.Stderr,
			ExitCode: vmResult.ExitCode,
		})
	}

	return results, nil
}

// parseCommand parses the running command and extracts arguments.
// It replaces common placeholders like "./a.out" with the actual binary path.
func parseCommand(runningCommand, binaryPath string) []string {
	// Replace common binary placeholders
	cmd := runningCommand
	cmd = strings.ReplaceAll(cmd, "./a.out", binaryPath)
	cmd = strings.ReplaceAll(cmd, "./program", binaryPath)
	cmd = strings.ReplaceAll(cmd, "$BINARY", binaryPath)

	// If the command is just the binary name, return empty args
	if cmd == binaryPath || cmd == "" {
		return []string{}
	}

	// Split the command to extract arguments
	parts := strings.Fields(cmd)
	if len(parts) <= 1 {
		return []string{}
	}

	// Return everything after the first part (which is the binary)
	return parts[1:]
}
