package vm

import (
	"fmt"
	"os"
	"strings"

	"defuzz/internal/exec"
)

// ExecutionResult holds the outcome of a command run inside the VM.
type ExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// VM defines the interface for a virtual machine manager.
type VM interface {
	// Create sets up and starts the containerized environment.
	Create() error
	// Run executes an arbitrary command inside the running container.
	Run(command ...string) (*ExecutionResult, error)
	// Stop halts and removes the container.
	Stop() error
}

// PodmanVM is a VM implementation that uses Podman to manage containers.
type PodmanVM struct {
	image       string
	executor    exec.Executor
	containerID string
	workDir     string // Host working directory to mount
}

// NewPodmanVM creates a new Podman-based VM manager.
func NewPodmanVM(image string, executor exec.Executor) *PodmanVM {
	workDir, _ := os.Getwd() // Get current working directory
	return &PodmanVM{
		image:    image,
		executor: executor,
		workDir:  workDir,
	}
}

// Create starts a new Podman container.
func (p *PodmanVM) Create() error {
	// Mount the current working directory to make seeds accessible
	mountArg := fmt.Sprintf("%s:/workspace", p.workDir)
	args := []string{"run", "-d", "--rm", "-v", mountArg, "-w", "/workspace", p.image, "sleep", "infinity"}
	res, err := p.executor.Run("podman", args...)
	if err != nil {
		return fmt.Errorf("failed to create podman container: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("failed to create podman container, exit code %d: %s", res.ExitCode, res.Stderr)
	}
	p.containerID = strings.TrimSpace(res.Stdout)
	return nil
}

// Run executes a command inside the Podman container.
func (p *PodmanVM) Run(command ...string) (*ExecutionResult, error) {
	if p.containerID == "" {
		return nil, fmt.Errorf("vm is not created, cannot run command")
	}
	args := append([]string{"exec", p.containerID}, command...)
	res, err := p.executor.Run("podman", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute command in podman: %w", err)
	}
	return &ExecutionResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
	}, nil
}

// Stop stops and removes the Podman container.
func (p *PodmanVM) Stop() error {
	if p.containerID == "" {
		// If there's no container, there's nothing to do.
		return nil
	}
	_, err := p.executor.Run("podman", "stop", p.containerID)
	// We don't check the exit code here, as the container might already be stopped.
	// The '--rm' flag in Create() handles removal.
	return err
}
