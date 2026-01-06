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

// Tests for parseQEMUExitCode function

func TestParseQEMUExitCode_NormalExit(t *testing.T) {
	// Normal exit codes should pass through unchanged
	tests := []struct {
		exitCode int
		stderr   string
		expected int
	}{
		{0, "", 0},
		{1, "", 1},
		{42, "some error", 42},
		{139, "", 139}, // Already correct SIGSEGV code
	}

	for _, tc := range tests {
		result := parseQEMUExitCode(tc.exitCode, tc.stderr)
		assert.Equal(t, tc.expected, result, "exitCode=%d stderr=%q", tc.exitCode, tc.stderr)
	}
}

func TestParseQEMUExitCode_SIGSEGV(t *testing.T) {
	// Test SIGSEGV detection (signal 11 -> exit code 139)
	tests := []struct {
		name   string
		stderr string
	}{
		{"signal number", "qemu: uncaught target signal 11 (Segmentation fault) - core dumped"},
		{"signal name only", "Segmentation fault"},
		{"with prefix", "error: Segmentation fault occurred"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseQEMUExitCode(-1, tc.stderr)
			assert.Equal(t, 139, result, "SIGSEGV should return 139")
		})
	}
}

func TestParseQEMUExitCode_SIGABRT(t *testing.T) {
	// Test SIGABRT detection (signal 6 -> exit code 134)
	tests := []struct {
		name   string
		stderr string
	}{
		{"signal number", "qemu: uncaught target signal 6 (Aborted) - core dumped"},
		{"signal name only", "Aborted"},
		{"stack smashing", "*** stack smashing detected ***: terminated\nqemu: uncaught target signal 6 (Aborted) - core dumped"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseQEMUExitCode(-1, tc.stderr)
			assert.Equal(t, 134, result, "SIGABRT should return 134")
		})
	}
}

func TestParseQEMUExitCode_OtherSignals(t *testing.T) {
	// Test other signal detection
	tests := []struct {
		name     string
		stderr   string
		expected int
	}{
		{"SIGFPE", "qemu: uncaught target signal 8 (Floating point exception) - core dumped", 136},
		{"SIGILL", "qemu: uncaught target signal 4 (Illegal instruction) - core dumped", 132},
		{"SIGBUS", "qemu: uncaught target signal 7 (Bus error) - core dumped", 135},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseQEMUExitCode(-1, tc.stderr)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestParseQEMUExitCode_UnknownSignal(t *testing.T) {
	// Unknown signals should return -1 unchanged
	result := parseQEMUExitCode(-1, "some unknown error")
	assert.Equal(t, -1, result)
}

func TestQEMUVM_RunWithSignal(t *testing.T) {
	// Test that QEMU VM correctly parses signals from stderr
	cfg := QEMUConfig{
		QEMUPath: "qemu-aarch64",
		Sysroot:  "/usr/aarch64-linux-gnu",
	}
	vm := NewQEMUVM(cfg)

	vm.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			// Simulate QEMU reporting a SIGSEGV
			return &exec.ExecutionResult{
				Stdout:   "",
				Stderr:   "qemu: uncaught target signal 11 (Segmentation fault) - core dumped",
				ExitCode: -1, // QEMU returns -1 for signals
			}, nil
		},
	}

	result, err := vm.Run("/path/to/binary")

	require.NoError(t, err)
	assert.Equal(t, 139, result.ExitCode, "Should parse SIGSEGV from stderr")
}

func TestQEMUVM_RunWithStackSmashing(t *testing.T) {
	// Test detection of stack canary violation (SIGABRT)
	cfg := QEMUConfig{
		QEMUPath: "qemu-aarch64",
	}
	vm := NewQEMUVM(cfg)

	vm.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			return &exec.ExecutionResult{
				Stdout:   "",
				Stderr:   "*** stack smashing detected ***: terminated\nqemu: uncaught target signal 6 (Aborted) - core dumped",
				ExitCode: -1,
			}, nil
		},
	}

	result, err := vm.Run("/path/to/binary")

	require.NoError(t, err)
	assert.Equal(t, 134, result.ExitCode, "Should parse SIGABRT (stack smashing) from stderr")
}
