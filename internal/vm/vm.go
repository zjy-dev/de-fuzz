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
	// Run executes a seed by running the binary with the provided run script.
	Run(binaryPath, runScriptPath string) (*ExecutionResult, error)
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

// Run executes the run script inside the running container.
func (p *PodmanVM) Run(binaryPath, runScriptPath string) (*ExecutionResult, error) {
	if p.containerID == "" {
		return nil, fmt.Errorf("vm is not created, cannot run command")
	}
	// The working directory is mounted, so we can directly execute the script.
	// We need to make the script executable first.
	chmodCmd := []string{"exec", p.containerID, "chmod", "+x", runScriptPath}
	_, err := p.executor.Run("podman", chmodCmd...)
	if err != nil {
		return nil, fmt.Errorf("failed to make run script executable: %w", err)
	}

	args := []string{"exec", p.containerID, runScriptPath}
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
