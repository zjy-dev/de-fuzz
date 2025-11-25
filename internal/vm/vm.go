package vm

import (
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/exec"
)

// ExecutionResult holds the outcome of running a binary in QEMU.
type ExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// VM defines the interface for running binaries in a virtual machine or emulator.
type VM interface {
	// Run executes a binary and returns the result.
	Run(binaryPath string, args ...string) (*ExecutionResult, error)

	// RunWithTimeout executes a binary with a timeout.
	RunWithTimeout(binaryPath string, timeoutSec int, args ...string) (*ExecutionResult, error)
}

// QEMUVM implements the VM interface using QEMU user-mode emulation.
type QEMUVM struct {
	executor  exec.Executor
	qemuPath  string   // Path to qemu-user executable (e.g., "qemu-aarch64")
	sysroot   string   // Sysroot for library path
	extraArgs []string // Additional QEMU arguments
}

// QEMUConfig holds the configuration for QEMU.
type QEMUConfig struct {
	QEMUPath  string   // Path to QEMU executable
	Sysroot   string   // Sysroot path for -L argument
	ExtraArgs []string // Additional QEMU arguments
}

// NewQEMUVM creates a new QEMU VM instance.
func NewQEMUVM(cfg QEMUConfig) *QEMUVM {
	return &QEMUVM{
		executor:  exec.NewCommandExecutor(),
		qemuPath:  cfg.QEMUPath,
		sysroot:   cfg.Sysroot,
		extraArgs: cfg.ExtraArgs,
	}
}

// Run executes a binary using QEMU user-mode emulation.
func (q *QEMUVM) Run(binaryPath string, args ...string) (*ExecutionResult, error) {
	return q.run(binaryPath, 0, args...)
}

// RunWithTimeout executes a binary with a timeout.
func (q *QEMUVM) RunWithTimeout(binaryPath string, timeoutSec int, args ...string) (*ExecutionResult, error) {
	return q.run(binaryPath, timeoutSec, args...)
}

func (q *QEMUVM) run(binaryPath string, timeoutSec int, args ...string) (*ExecutionResult, error) {
	// Build QEMU command arguments
	qemuArgs := make([]string, 0)

	// Add sysroot if specified
	if q.sysroot != "" {
		qemuArgs = append(qemuArgs, "-L", q.sysroot)
	}

	// Add extra QEMU arguments
	qemuArgs = append(qemuArgs, q.extraArgs...)

	// Add the binary path
	qemuArgs = append(qemuArgs, binaryPath)

	// Add binary arguments
	qemuArgs = append(qemuArgs, args...)

	var result *exec.ExecutionResult
	var err error

	if timeoutSec > 0 {
		// Use timeout command to wrap QEMU
		timeoutCmd := fmt.Sprintf("timeout %d %s %s", timeoutSec, q.qemuPath, strings.Join(qemuArgs, " "))
		result, err = q.executor.Run("sh", "-c", timeoutCmd)
	} else {
		result, err = q.executor.Run(q.qemuPath, qemuArgs...)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to run QEMU: %w", err)
	}

	return &ExecutionResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}, nil
}

// LocalVM implements VM interface for running native binaries directly.
type LocalVM struct {
	executor exec.Executor
}

// NewLocalVM creates a new LocalVM for running native binaries.
func NewLocalVM() *LocalVM {
	return &LocalVM{
		executor: exec.NewCommandExecutor(),
	}
}

// Run executes a native binary directly.
func (l *LocalVM) Run(binaryPath string, args ...string) (*ExecutionResult, error) {
	return l.run(binaryPath, 0, args...)
}

// RunWithTimeout executes a native binary with a timeout.
func (l *LocalVM) RunWithTimeout(binaryPath string, timeoutSec int, args ...string) (*ExecutionResult, error) {
	return l.run(binaryPath, timeoutSec, args...)
}

func (l *LocalVM) run(binaryPath string, timeoutSec int, args ...string) (*ExecutionResult, error) {
	var result *exec.ExecutionResult
	var err error

	if timeoutSec > 0 {
		// Use timeout command
		cmdArgs := append([]string{fmt.Sprintf("%d", timeoutSec), binaryPath}, args...)
		result, err = l.executor.Run("timeout", cmdArgs...)
	} else {
		result, err = l.executor.Run(binaryPath, args...)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to run binary: %w", err)
	}

	return &ExecutionResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}, nil
}
