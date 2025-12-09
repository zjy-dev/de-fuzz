package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

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

// OracleExecutorAdapter adapts a LocalExecutor to the oracle.Executor interface.
// This allows oracles to execute binaries with custom stdin input.
type OracleExecutorAdapter struct {
	timeoutSec int
}

// NewOracleExecutorAdapter creates a new OracleExecutorAdapter.
func NewOracleExecutorAdapter(timeoutSec int) *OracleExecutorAdapter {
	return &OracleExecutorAdapter{
		timeoutSec: timeoutSec,
	}
}

// ExecuteWithInput runs the binary with the given stdin input and returns the exit code.
func (a *OracleExecutorAdapter) ExecuteWithInput(binaryPath string, stdin string) (exitCode int, stdout string, stderr string, err error) {
	ctx := context.Background()
	if a.timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(a.timeoutSec)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, binaryPath)

	// Set up stdin
	cmd.Stdin = strings.NewReader(stdin)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()

	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if runErr != nil {
		// If we couldn't get the exit code and there was an error, return -1
		exitCode = -1
	}

	// cmd.Run() returns an error for non-zero exit codes, but we handle
	// the exit code explicitly. So, we only return other kinds of errors.
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			if ctx.Err() == context.DeadlineExceeded {
				return 124, stdout, stderr, nil // Timeout exit code
			}
			return exitCode, stdout, stderr, fmt.Errorf("failed to execute: %w", runErr)
		}
	}

	return exitCode, stdout, stderr, nil
}

// ExecuteWithArgs runs the binary with the given command line arguments and returns the exit code.
func (a *OracleExecutorAdapter) ExecuteWithArgs(binaryPath string, args ...string) (exitCode int, stdout string, stderr string, err error) {
	ctx := context.Background()
	if a.timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(a.timeoutSec)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, binaryPath, args...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()

	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	// Get exit code, handling both normal exits and signal terminations
	exitCode = getExitCode(cmd.ProcessState, runErr)

	// cmd.Run() returns an error for non-zero exit codes, but we handle
	// the exit code explicitly. So, we only return other kinds of errors.
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			if ctx.Err() == context.DeadlineExceeded {
				return 124, stdout, stderr, nil // Timeout exit code
			}
			return exitCode, stdout, stderr, fmt.Errorf("failed to execute: %w", runErr)
		}
	}

	return exitCode, stdout, stderr, nil
}

// getExitCode extracts the exit code from ProcessState, handling both normal
// exits and signal terminations. For signal terminations, returns 128 + signal.
func getExitCode(ps *os.ProcessState, runErr error) int {
	if ps == nil {
		if runErr != nil {
			return -1
		}
		return 0
	}

	// Try the standard ExitCode() first
	exitCode := ps.ExitCode()
	if exitCode != -1 {
		return exitCode
	}

	// ExitCode() returns -1 for signal terminations, use syscall to get actual status
	if status, ok := ps.Sys().(syscall.WaitStatus); ok {
		if status.Signaled() {
			// Convention: signal exit codes are 128 + signal number
			return 128 + int(status.Signal())
		}
		if status.Exited() {
			return status.ExitStatus()
		}
	}

	return exitCode
}
