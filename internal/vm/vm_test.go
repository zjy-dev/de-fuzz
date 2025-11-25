package vm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/exec"
)

// MockExecutor is a mock implementation of exec.Executor for testing.
type MockExecutor struct {
	RunFunc func(command string, args ...string) (*exec.ExecutionResult, error)
}

func (m *MockExecutor) Run(command string, args ...string) (*exec.ExecutionResult, error) {
	if m.RunFunc != nil {
		return m.RunFunc(command, args...)
	}
	return &exec.ExecutionResult{ExitCode: 0}, nil
}

func TestNewLocalVM(t *testing.T) {
	vm := NewLocalVM()
	assert.NotNil(t, vm)
	assert.NotNil(t, vm.executor)
}

func TestLocalVM_Run(t *testing.T) {
	vm := &LocalVM{}
	vm.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			assert.Equal(t, "/path/to/binary", command)
			return &exec.ExecutionResult{
				Stdout:   "hello world",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		},
	}

	result, err := vm.Run("/path/to/binary")

	require.NoError(t, err)
	assert.Equal(t, "hello world", result.Stdout)
	assert.Equal(t, "", result.Stderr)
	assert.Equal(t, 0, result.ExitCode)
}

func TestLocalVM_RunWithArgs(t *testing.T) {
	vm := &LocalVM{}
	var capturedArgs []string
	vm.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedArgs = args
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	_, err := vm.Run("/path/to/binary", "arg1", "arg2")

	require.NoError(t, err)
	assert.Equal(t, []string{"arg1", "arg2"}, capturedArgs)
}

func TestLocalVM_RunWithTimeout(t *testing.T) {
	vm := &LocalVM{}
	var capturedCmd string
	var capturedArgs []string
	vm.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedCmd = command
			capturedArgs = args
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	_, err := vm.RunWithTimeout("/path/to/binary", 10, "arg1")

	require.NoError(t, err)
	assert.Equal(t, "timeout", capturedCmd)
	assert.Contains(t, capturedArgs, "10")
	assert.Contains(t, capturedArgs, "/path/to/binary")
}

func TestLocalVM_RunNonZeroExit(t *testing.T) {
	vm := &LocalVM{}
	vm.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			return &exec.ExecutionResult{
				Stdout:   "",
				Stderr:   "segmentation fault",
				ExitCode: 139, // SIGSEGV
			}, nil
		},
	}

	result, err := vm.Run("/path/to/binary")

	require.NoError(t, err)
	assert.Equal(t, 139, result.ExitCode)
	assert.Equal(t, "segmentation fault", result.Stderr)
}

func TestNewQEMUVM(t *testing.T) {
	cfg := QEMUConfig{
		QEMUPath:  "qemu-aarch64",
		Sysroot:   "/usr/aarch64-linux-gnu",
		ExtraArgs: []string{"-cpu", "max"},
	}

	vm := NewQEMUVM(cfg)

	assert.NotNil(t, vm)
	assert.Equal(t, "qemu-aarch64", vm.qemuPath)
	assert.Equal(t, "/usr/aarch64-linux-gnu", vm.sysroot)
	assert.Equal(t, []string{"-cpu", "max"}, vm.extraArgs)
}

func TestQEMUVM_Run(t *testing.T) {
	cfg := QEMUConfig{
		QEMUPath: "qemu-aarch64",
		Sysroot:  "/usr/aarch64-linux-gnu",
	}
	vm := NewQEMUVM(cfg)

	var capturedCmd string
	var capturedArgs []string
	vm.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedCmd = command
			capturedArgs = args
			return &exec.ExecutionResult{
				Stdout:   "output",
				ExitCode: 0,
			}, nil
		},
	}

	result, err := vm.Run("/path/to/binary", "arg1")

	require.NoError(t, err)
	assert.Equal(t, "qemu-aarch64", capturedCmd)
	assert.Contains(t, capturedArgs, "-L")
	assert.Contains(t, capturedArgs, "/usr/aarch64-linux-gnu")
	assert.Contains(t, capturedArgs, "/path/to/binary")
	assert.Contains(t, capturedArgs, "arg1")
	assert.Equal(t, "output", result.Stdout)
}

func TestQEMUVM_RunWithTimeout(t *testing.T) {
	cfg := QEMUConfig{
		QEMUPath: "qemu-aarch64",
	}
	vm := NewQEMUVM(cfg)

	var capturedCmd string
	vm.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedCmd = command
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	_, err := vm.RunWithTimeout("/path/to/binary", 5)

	require.NoError(t, err)
	// Should use "sh -c" to wrap the timeout command
	assert.Equal(t, "sh", capturedCmd)
}

func TestQEMUVM_RunWithExtraArgs(t *testing.T) {
	cfg := QEMUConfig{
		QEMUPath:  "qemu-aarch64",
		ExtraArgs: []string{"-cpu", "cortex-a72"},
	}
	vm := NewQEMUVM(cfg)

	var capturedArgs []string
	vm.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedArgs = args
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	_, err := vm.Run("/path/to/binary")

	require.NoError(t, err)
	assert.Contains(t, capturedArgs, "-cpu")
	assert.Contains(t, capturedArgs, "cortex-a72")
}
