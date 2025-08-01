package vm

import (
	"fmt"
	"testing"

	"defuzz/internal/exec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockExecutor is a mock implementation of the exec.Executor interface for testing.
type mockExecutor struct {
	// The result to return when the command is called.
	resultToReturn *exec.ExecutionResult
	// The error to return
	errorToReturn error
	// Capture the last command and args that were called
	lastCommand string
	lastArgs    []string
}

func (m *mockExecutor) Run(command string, args ...string) (*exec.ExecutionResult, error) {
	m.lastCommand = command
	m.lastArgs = args
	return m.resultToReturn, m.errorToReturn
}

func TestPodmanVM_Create(t *testing.T) {
	mockExec := &mockExecutor{
		resultToReturn: &exec.ExecutionResult{ExitCode: 0, Stdout: "test-container-id"},
	}
	vm := NewPodmanVM("test-image", mockExec)

	err := vm.Create()
	require.NoError(t, err)
	assert.Equal(t, "test-container-id", vm.containerID)

	// Verify the correct podman command was called
	assert.Equal(t, "podman", mockExec.lastCommand)
	assert.Contains(t, mockExec.lastArgs, "run")
	assert.Contains(t, mockExec.lastArgs, "-d")
	assert.Contains(t, mockExec.lastArgs, "--rm")
	assert.Contains(t, mockExec.lastArgs, "-v")
	assert.Contains(t, mockExec.lastArgs, "-w")
	assert.Contains(t, mockExec.lastArgs, "/workspace")
	assert.Contains(t, mockExec.lastArgs, "test-image")
}

func TestPodmanVM_Create_Fails(t *testing.T) {
	mockExec := &mockExecutor{
		resultToReturn: &exec.ExecutionResult{ExitCode: 1, Stderr: "podman error"},
	}
	vm := NewPodmanVM("test-image", mockExec)

	err := vm.Create()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "podman error")
}

func TestPodmanVM_Run(t *testing.T) {
	mockExec := &mockExecutor{
		resultToReturn: &exec.ExecutionResult{ExitCode: 0, Stdout: "hello"},
	}
	vm := &PodmanVM{
		image:       "test-image",
		executor:    mockExec,
		containerID: "test-container-id",
	}

	res, err := vm.Run("echo", "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", res.Stdout)
	assert.Equal(t, 0, res.ExitCode)

	// Verify the correct podman exec command was called
	assert.Equal(t, "podman", mockExec.lastCommand)
	expectedArgs := []string{"exec", "test-container-id", "echo", "hello"}
	assert.Equal(t, expectedArgs, mockExec.lastArgs)
}

func TestPodmanVM_Run_Fails_When_Not_Created(t *testing.T) {
	vm := NewPodmanVM("test-image", &mockExecutor{})
	_, err := vm.Run("echo", "hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "vm is not created")
}

func TestPodmanVM_Stop(t *testing.T) {
	mockExec := &mockExecutor{
		resultToReturn: &exec.ExecutionResult{ExitCode: 0},
	}
	vm := &PodmanVM{
		image:       "test-image",
		executor:    mockExec,
		containerID: "test-container-id",
	}

	err := vm.Stop()
	require.NoError(t, err)

	// Verify the correct podman stop command was called
	assert.Equal(t, "podman", mockExec.lastCommand)
	expectedArgs := []string{"stop", "test-container-id"}
	assert.Equal(t, expectedArgs, mockExec.lastArgs)
}

func TestPodmanVM_Stop_Does_Nothing_If_No_Container(t *testing.T) {
	// This mock will fail the test if Run is ever called.
	mockExec := &mockExecutor{errorToReturn: fmt.Errorf("should not be called")}
	vm := NewPodmanVM("test-image", mockExec)
	err := vm.Stop()
	assert.NoError(t, err)
}
