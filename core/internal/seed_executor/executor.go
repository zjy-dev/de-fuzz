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
)

// ExecutionResult holds the outcome of a single command execution.
type ExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
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

// QEMUOracleExecutorAdapter adapts QEMU execution to the oracle.Executor interface.
// This allows oracles to execute cross-architecture binaries via QEMU user-mode emulation.
type QEMUOracleExecutorAdapter struct {
	qemuPath   string
	sysroot    string
	timeoutSec int
}

// NewQEMUOracleExecutorAdapter creates a new QEMUOracleExecutorAdapter.
func NewQEMUOracleExecutorAdapter(qemuPath, sysroot string, timeoutSec int) *QEMUOracleExecutorAdapter {
	return &QEMUOracleExecutorAdapter{
		qemuPath:   qemuPath,
		sysroot:    sysroot,
		timeoutSec: timeoutSec,
	}
}

// ExecuteWithInput runs the binary via QEMU with the given stdin input.
func (a *QEMUOracleExecutorAdapter) ExecuteWithInput(binaryPath string, stdin string) (exitCode int, stdout string, stderr string, err error) {
	ctx := context.Background()
	if a.timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(a.timeoutSec)*time.Second)
		defer cancel()
	}

	// Build QEMU command: qemu-aarch64 -L <sysroot> <binary>
	args := []string{}
	if a.sysroot != "" {
		args = append(args, "-L", a.sysroot)
	}
	args = append(args, binaryPath)

	cmd := exec.CommandContext(ctx, a.qemuPath, args...)
	cmd.Stdin = strings.NewReader(stdin)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()

	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	exitCode = getExitCode(cmd.ProcessState, runErr)

	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			if ctx.Err() == context.DeadlineExceeded {
				return 124, stdout, stderr, nil
			}
			return exitCode, stdout, stderr, fmt.Errorf("failed to execute via QEMU: %w", runErr)
		}
	}

	return exitCode, stdout, stderr, nil
}

// ExecuteWithArgs runs the binary via QEMU with the given command line arguments.
func (a *QEMUOracleExecutorAdapter) ExecuteWithArgs(binaryPath string, args ...string) (exitCode int, stdout string, stderr string, err error) {
	ctx := context.Background()
	if a.timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(a.timeoutSec)*time.Second)
		defer cancel()
	}

	// Build QEMU command: qemu-aarch64 -L <sysroot> <binary> <args...>
	qemuArgs := []string{}
	if a.sysroot != "" {
		qemuArgs = append(qemuArgs, "-L", a.sysroot)
	}
	qemuArgs = append(qemuArgs, binaryPath)
	qemuArgs = append(qemuArgs, args...)

	cmd := exec.CommandContext(ctx, a.qemuPath, qemuArgs...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()

	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	exitCode = getExitCode(cmd.ProcessState, runErr)

	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			if ctx.Err() == context.DeadlineExceeded {
				return 124, stdout, stderr, nil
			}
			return exitCode, stdout, stderr, fmt.Errorf("failed to execute via QEMU: %w", runErr)
		}
	}

	return exitCode, stdout, stderr, nil
}
