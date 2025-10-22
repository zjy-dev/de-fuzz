package vm

import (
	"errors"
	"testing"

	"defuzz/internal/exec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockExecutor for testing
type mockExecutor struct {
	resultToReturn *exec.ExecutionResult
	errorToReturn  error
	lastCommand    string
	lastArgs       []string
}

func (m *mockExecutor) Run(command string, args ...string) (*exec.ExecutionResult, error) {
	m.lastCommand = command
	m.lastArgs = args
	return m.resultToReturn, m.errorToReturn
}

func TestPodmanVM_Run_NotCreated(t *testing.T) {
	vm := NewPodmanVM("test-image", &mockExecutor{})
	_, err := vm.Run("/path/to/binary", "/path/to/run.sh")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "vm is not created")
}

func TestPodmanVM_Create(t *testing.T) {
	executor := &mockExecutor{
		resultToReturn: &exec.ExecutionResult{
			Stdout:   "container-id",
			ExitCode: 0,
		},
	}
	vm := NewPodmanVM("test-image", executor)

	err := vm.Create()
	assert.NoError(t, err)
	assert.Equal(t, "container-id", vm.containerID)

	// Verify correct podman command was called
	assert.Equal(t, "podman", executor.lastCommand)
	assert.Contains(t, executor.lastArgs, "run")
	assert.Contains(t, executor.lastArgs, "-d")
	assert.Contains(t, executor.lastArgs, "--rm")
	assert.Contains(t, executor.lastArgs, "test-image")
}

func TestPodmanVM_Create_Failure(t *testing.T) {
	executor := &mockExecutor{
		resultToReturn: &exec.ExecutionResult{
			Stderr:   "podman: error creating container",
			ExitCode: 1,
		},
	}
	vm := NewPodmanVM("test-image", executor)

	err := vm.Create()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create podman container")
	assert.Contains(t, err.Error(), "podman: error creating container")
}

func TestPodmanVM_Run_Success(t *testing.T) {
	executor := &mockExecutor{
		resultToReturn: &exec.ExecutionResult{
			Stdout:   "program output",
			ExitCode: 0,
		},
	}
	vm := NewPodmanVM("test-image", executor)
	// Simulate container is created
	vm.containerID = "test-container-id"

	result, err := vm.Run("/workspace/binary", "/workspace/run.sh")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "program output", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

	// The last command should be the execution, not chmod
	// (mockExecutor only captures the last command)
	assert.Equal(t, "podman", executor.lastCommand)
	expectedArgs := []string{"exec", "test-container-id", "/workspace/run.sh"}
	assert.Equal(t, expectedArgs, executor.lastArgs)
}

func TestPodmanVM_Run_ExecutionFailure(t *testing.T) {
	executor := &mockExecutor{
		resultToReturn: &exec.ExecutionResult{
			Stderr:   "bash: /workspace/run.sh: No such file or directory",
			ExitCode: 127,
		},
	}
	vm := NewPodmanVM("test-image", executor)
	vm.containerID = "test-container-id"

	result, err := vm.Run("/workspace/binary", "/workspace/run.sh")
	assert.NoError(t, err) // VM.Run doesn't return exec errors, just the result
	assert.Equal(t, 127, result.ExitCode)
	assert.Contains(t, result.Stderr, "bash: /workspace/run.sh: No such file or directory")
}

func TestPodmanVM_Stop_Success(t *testing.T) {
	executor := &mockExecutor{
		resultToReturn: &exec.ExecutionResult{
			ExitCode: 0,
		},
	}
	vm := NewPodmanVM("test-image", executor)
	vm.containerID = "test-container-id"

	err := vm.Stop()
	assert.NoError(t, err)

	// Verify correct stop command was called
	assert.Equal(t, "podman", executor.lastCommand)
	assert.Equal(t, []string{"stop", "test-container-id"}, executor.lastArgs)
}

func TestPodmanVM_Stop_Failure(t *testing.T) {
	executor := &mockExecutor{
		errorToReturn: errors.New("no such container"),
	}
	vm := NewPodmanVM("test-image", executor)
	vm.containerID = "test-container-id"

	err := vm.Stop()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such container")
}
