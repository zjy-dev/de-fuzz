package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	"github.com/zjy-dev/de-fuzz/internal/vm"
)

// MockVM is a mock implementation of vm.VM for testing.
type MockVM struct {
	RunFunc            func(binaryPath string, args ...string) (*vm.ExecutionResult, error)
	RunWithTimeoutFunc func(binaryPath string, timeoutSec int, args ...string) (*vm.ExecutionResult, error)
}

func (m *MockVM) Run(binaryPath string, args ...string) (*vm.ExecutionResult, error) {
	if m.RunFunc != nil {
		return m.RunFunc(binaryPath, args...)
	}
	return &vm.ExecutionResult{ExitCode: 0}, nil
}

func (m *MockVM) RunWithTimeout(binaryPath string, timeoutSec int, args ...string) (*vm.ExecutionResult, error) {
	if m.RunWithTimeoutFunc != nil {
		return m.RunWithTimeoutFunc(binaryPath, timeoutSec, args...)
	}
	return &vm.ExecutionResult{ExitCode: 0}, nil
}

func TestNewLocalExecutor(t *testing.T) {
	executor := NewLocalExecutor(30)
	assert.NotNil(t, executor)
	assert.Equal(t, 30, executor.timeoutSec)
}

func TestLocalExecutor_Execute_SingleTestCase(t *testing.T) {
	executor := &LocalExecutor{
		vm: &MockVM{
			RunFunc: func(binaryPath string, args ...string) (*vm.ExecutionResult, error) {
				assert.Equal(t, "/path/to/binary", binaryPath)
				return &vm.ExecutionResult{
					Stdout:   "test output",
					Stderr:   "",
					ExitCode: 0,
				}, nil
			},
		},
		timeoutSec: 0,
	}

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "test output"},
		},
	}

	results, err := executor.Execute(testSeed, "/path/to/binary")

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "test output", results[0].Stdout)
	assert.Equal(t, 0, results[0].ExitCode)
}

func TestLocalExecutor_Execute_MultipleTestCases(t *testing.T) {
	callCount := 0
	executor := &LocalExecutor{
		vm: &MockVM{
			RunFunc: func(binaryPath string, args ...string) (*vm.ExecutionResult, error) {
				callCount++
				return &vm.ExecutionResult{
					Stdout:   "output " + string(rune('0'+callCount)),
					ExitCode: 0,
				}, nil
			},
		},
		timeoutSec: 0,
	}

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "output 1"},
			{RunningCommand: "./a.out arg1", ExpectedResult: "output 2"},
			{RunningCommand: "./a.out arg1 arg2", ExpectedResult: "output 3"},
		},
	}

	results, err := executor.Execute(testSeed, "/path/to/binary")

	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, 3, callCount)
}

func TestLocalExecutor_Execute_WithTimeout(t *testing.T) {
	executor := &LocalExecutor{
		vm: &MockVM{
			RunWithTimeoutFunc: func(binaryPath string, timeoutSec int, args ...string) (*vm.ExecutionResult, error) {
				assert.Equal(t, 10, timeoutSec)
				return &vm.ExecutionResult{ExitCode: 0}, nil
			},
		},
		timeoutSec: 10,
	}

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: ""},
		},
	}

	results, err := executor.Execute(testSeed, "/path/to/binary")

	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestLocalExecutor_Execute_CrashDetection(t *testing.T) {
	executor := &LocalExecutor{
		vm: &MockVM{
			RunFunc: func(binaryPath string, args ...string) (*vm.ExecutionResult, error) {
				return &vm.ExecutionResult{
					Stdout:   "",
					Stderr:   "Segmentation fault (core dumped)",
					ExitCode: 139, // 128 + SIGSEGV(11)
				}, nil
			},
		},
		timeoutSec: 0,
	}

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: ""},
		},
	}

	results, err := executor.Execute(testSeed, "/path/to/binary")

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 139, results[0].ExitCode)
	assert.Contains(t, results[0].Stderr, "Segmentation fault")
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name           string
		runningCommand string
		binaryPath     string
		expectedArgs   []string
	}{
		{
			name:           "simple binary placeholder",
			runningCommand: "./a.out",
			binaryPath:     "/tmp/test",
			expectedArgs:   []string{},
		},
		{
			name:           "binary with args",
			runningCommand: "./a.out arg1 arg2",
			binaryPath:     "/tmp/test",
			expectedArgs:   []string{"arg1", "arg2"},
		},
		{
			name:           "program placeholder",
			runningCommand: "./program --flag value",
			binaryPath:     "/tmp/test",
			expectedArgs:   []string{"--flag", "value"},
		},
		{
			name:           "$BINARY placeholder",
			runningCommand: "$BINARY -v",
			binaryPath:     "/tmp/test",
			expectedArgs:   []string{"-v"},
		},
		{
			name:           "empty command",
			runningCommand: "",
			binaryPath:     "/tmp/test",
			expectedArgs:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := parseCommand(tt.runningCommand, tt.binaryPath)
			assert.Equal(t, tt.expectedArgs, args)
		})
	}
}

func TestNewQEMUExecutor(t *testing.T) {
	cfg := vm.QEMUConfig{
		QEMUPath: "qemu-aarch64",
		Sysroot:  "/usr/aarch64-linux-gnu",
	}

	executor := NewQEMUExecutor(cfg, 30)

	assert.NotNil(t, executor)
	assert.Equal(t, 30, executor.timeoutSec)
}

func TestQEMUExecutor_Execute(t *testing.T) {
	executor := &QEMUExecutor{
		vm: &MockVM{
			RunFunc: func(binaryPath string, args ...string) (*vm.ExecutionResult, error) {
				return &vm.ExecutionResult{
					Stdout:   "qemu output",
					ExitCode: 0,
				}, nil
			},
		},
		timeoutSec: 0,
	}

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "qemu output"},
		},
	}

	results, err := executor.Execute(testSeed, "/path/to/binary")

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "qemu output", results[0].Stdout)
}
