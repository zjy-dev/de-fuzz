package exec

import (
	"bytes"
	"os/exec"
)

// ExecutionResult holds the outcome of a command execution.
type ExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Executor defines an interface for running external commands.
// This allows for mocking in tests.
type Executor interface {
	Run(command string, args ...string) (*ExecutionResult, error)
}

// CommandExecutor is a concrete implementation of the Executor interface
// that runs actual commands on the host system.
type CommandExecutor struct{}

// NewCommandExecutor creates a new CommandExecutor.
func NewCommandExecutor() *CommandExecutor {
	return &CommandExecutor{}
}

// Run executes the given command and returns its result.
func (e *CommandExecutor) Run(command string, args ...string) (*ExecutionResult, error) {
	cmd := exec.Command(command, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := &ExecutionResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}

	// cmd.Run() returns an error for non-zero exit codes, but we handle
	// the exit code explicitly. So, we only return other kinds of errors
	// (e.g., command not found).
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, err
		}
	}

	return result, nil
}
