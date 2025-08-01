package vm

import (
	"os"
	"testing"
	"time"

	"defuzz/internal/exec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPodmanVMIntegration tests the actual PodmanVM with real Podman commands
// This test requires Podman to be installed and running
func TestPodmanVMIntegration(t *testing.T) {
	// Skip integration test if running in CI or if SKIP_INTEGRATION is set
	if os.Getenv("CI") != "" || os.Getenv("SKIP_INTEGRATION") != "" {
		t.Skip("Skipping integration test")
	}

	// Create a real executor
	executor := exec.NewCommandExecutor()

	// Use Ubuntu 20.04 image for testing
	vm := NewPodmanVM("ubuntu:20.04", executor)

	t.Run("should create and manage a container lifecycle", func(t *testing.T) {
		// Create the container
		err := vm.Create()
		require.NoError(t, err, "Failed to create container")
		assert.NotEmpty(t, vm.containerID, "Container ID should not be empty")

		// Ensure cleanup happens
		defer func() {
			if vm.containerID != "" {
				vm.Stop()
			}
		}()

		// Test basic command execution
		result, err := vm.Run("echo", "hello world")
		require.NoError(t, err, "Failed to run echo command")
		assert.Equal(t, 0, result.ExitCode, "Echo command should exit with 0")
		assert.Equal(t, "hello world\n", result.Stdout, "Echo output should match")

		// Test working directory is mounted and accessible
		result, err = vm.Run("pwd")
		require.NoError(t, err, "Failed to run pwd command")
		assert.Equal(t, 0, result.ExitCode, "pwd should exit with 0")
		assert.Equal(t, "/workspace\n", result.Stdout, "Working directory should be /workspace")

		// Test that project files are accessible (check current directory content)
		result, err = vm.Run("ls", "-la")
		require.NoError(t, err, "Failed to list directory contents")
		assert.Equal(t, 0, result.ExitCode, "ls should succeed")
		// Just verify that we can list the directory - the actual content will vary
		assert.NotEmpty(t, result.Stdout, "Directory listing should not be empty")

		// Test command with non-zero exit code
		result, err = vm.Run("ls", "/nonexistent")
		require.NoError(t, err, "Command execution should not fail even with non-zero exit")
		assert.NotEqual(t, 0, result.ExitCode, "ls on non-existent path should have non-zero exit code")
		assert.Contains(t, result.Stderr, "No such file or directory", "stderr should contain error message")

		// Test multiple argument command
		result, err = vm.Run("sh", "-c", "echo 'test' > /tmp/testfile && cat /tmp/testfile")
		require.NoError(t, err, "Failed to run shell command")
		assert.Equal(t, 0, result.ExitCode, "shell command should succeed")
		assert.Equal(t, "test\n", result.Stdout, "shell command output should match")

		// Stop the container
		err = vm.Stop()
		require.NoError(t, err, "Failed to stop container")
	})

	t.Run("should fail to run commands when container is not created", func(t *testing.T) {
		vm := NewPodmanVM("ubuntu:20.04", executor)

		_, err := vm.Run("echo", "test")
		require.Error(t, err, "Should fail when container is not created")
		assert.Contains(t, err.Error(), "vm is not created", "Error should mention VM not created")
	})

	t.Run("should handle container creation failure gracefully", func(t *testing.T) {
		// Use a non-existent image to trigger failure
		vm := NewPodmanVM("nonexistent-image:latest", executor)

		err := vm.Create()
		require.Error(t, err, "Should fail with non-existent image")
		assert.Contains(t, err.Error(), "failed to create podman container", "Error should mention container creation failure")
	})
}

// TestPodmanVMWithGCCIntegration tests compilation within the container
func TestPodmanVMWithGCCIntegration(t *testing.T) {
	// Skip integration test if running in CI or if SKIP_INTEGRATION is set
	if os.Getenv("CI") != "" || os.Getenv("SKIP_INTEGRATION") != "" {
		t.Skip("Skipping integration test")
	}

	// Create a real executor
	executor := exec.NewCommandExecutor()

	// Use GCC image for compilation testing
	vm := NewPodmanVM("docker.io/library/gcc:latest", executor)

	t.Run("should compile and run C code", func(t *testing.T) {
		// Create the container
		err := vm.Create()
		require.NoError(t, err, "Failed to create container")

		// Ensure cleanup happens
		defer func() {
			if vm.containerID != "" {
				vm.Stop()
			}
		}()

		// Create a simple C program
		cCode := `#include <stdio.h>
int main() {
    printf("Hello from container!\n");
    return 0;
}`

		// Write C code to a file in the container - use printf to handle newlines properly
		result, err := vm.Run("sh", "-c", `printf '%s' '`+cCode+`' > /tmp/test.c`)
		require.NoError(t, err, "Failed to write C code")
		assert.Equal(t, 0, result.ExitCode, "Write should succeed")

		// Compile the C program
		result, err = vm.Run("gcc", "/tmp/test.c", "-o", "/tmp/test")
		require.NoError(t, err, "Failed to compile C code")
		if result.ExitCode != 0 {
			t.Logf("Compilation failed with exit code %d", result.ExitCode)
			t.Logf("Compilation stderr: %s", result.Stderr)
			t.Logf("Compilation stdout: %s", result.Stdout)
		}
		assert.Equal(t, 0, result.ExitCode, "Compilation should succeed")

		// Run the compiled program
		result, err = vm.Run("/tmp/test")
		require.NoError(t, err, "Failed to run compiled program")
		if result.ExitCode != 0 {
			t.Logf("Program failed with exit code %d", result.ExitCode)
			t.Logf("Program stderr: %s", result.Stderr)
			t.Logf("Program stdout: %s", result.Stdout)
		}
		assert.Equal(t, 0, result.ExitCode, "Program should run successfully")
		assert.Equal(t, "Hello from container!\n", result.Stdout, "Program output should match")

		// Test compilation error handling
		badCode := `#include <stdio.h>
int main() {
    undeclared_function();  // This will cause compilation error
    return 0;
}`

		// Write bad C code - use printf to handle newlines properly
		result, err = vm.Run("sh", "-c", `printf '%s' '`+badCode+`' > /tmp/bad.c`)
		require.NoError(t, err, "Failed to write bad C code")
		assert.Equal(t, 0, result.ExitCode, "Write bad code should succeed")

		// Try to compile the bad code
		result, err = vm.Run("gcc", "/tmp/bad.c", "-o", "/tmp/bad")
		require.NoError(t, err, "Command execution should not fail")
		assert.NotEqual(t, 0, result.ExitCode, "Compilation should fail")
		assert.Contains(t, result.Stderr, "undeclared", "stderr should contain compilation error")

		// Stop the container
		err = vm.Stop()
		require.NoError(t, err, "Failed to stop container")
	})
}

// TestPodmanVMConcurrency tests that multiple containers can be managed concurrently
func TestPodmanVMConcurrency(t *testing.T) {
	// Skip integration test if running in CI or if SKIP_INTEGRATION is set
	if os.Getenv("CI") != "" || os.Getenv("SKIP_INTEGRATION") != "" {
		t.Skip("Skipping integration test")
	}

	executor := exec.NewCommandExecutor()

	t.Run("should handle multiple containers concurrently", func(t *testing.T) {
		const numContainers = 3
		vms := make([]*PodmanVM, numContainers)

		// Create multiple VMs
		for i := 0; i < numContainers; i++ {
			vms[i] = NewPodmanVM("ubuntu:20.04", executor)
		}

		// Create all containers
		for i, vm := range vms {
			err := vm.Create()
			require.NoError(t, err, "Failed to create container %d", i)

			// Ensure cleanup
			defer func(v *PodmanVM) {
				if v.containerID != "" {
					v.Stop()
				}
			}(vm)
		}

		// Run commands in all containers concurrently
		done := make(chan bool, numContainers)

		for i, vm := range vms {
			go func(index int, v *PodmanVM) {
				defer func() { done <- true }()

				// Run a unique command in each container
				result, err := v.Run("echo", "container", string(rune('A'+index)))
				assert.NoError(t, err, "Failed to run command in container %d", index)
				assert.Equal(t, 0, result.ExitCode, "Command should succeed in container %d", index)
				expected := "container " + string(rune('A'+index)) + "\n"
				assert.Equal(t, expected, result.Stdout, "Output should match for container %d", index)
			}(i, vm)
		}

		// Wait for all goroutines to complete
		for i := 0; i < numContainers; i++ {
			select {
			case <-done:
				// Success
			case <-time.After(30 * time.Second):
				t.Fatal("Timeout waiting for concurrent container operations")
			}
		}

		// Stop all containers
		for i, vm := range vms {
			err := vm.Stop()
			assert.NoError(t, err, "Failed to stop container %d", i)
		}
	})
}

// BenchmarkPodmanVMOperations benchmarks VM operations
func BenchmarkPodmanVMOperations(b *testing.B) {
	// Skip benchmark if running in CI or if SKIP_INTEGRATION is set
	if os.Getenv("CI") != "" || os.Getenv("SKIP_INTEGRATION") != "" {
		b.Skip("Skipping integration benchmark")
	}

	executor := exec.NewCommandExecutor()
	vm := NewPodmanVM("ubuntu:20.04", executor)

	// Create container once for all benchmark iterations
	err := vm.Create()
	require.NoError(b, err, "Failed to create container for benchmark")

	defer func() {
		if vm.containerID != "" {
			vm.Stop()
		}
	}()

	b.ResetTimer()

	b.Run("echo_command", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := vm.Run("echo", "benchmark")
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("pwd_command", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := vm.Run("pwd")
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestPodmanVMLongRunningProcess tests handling of long-running processes
func TestPodmanVMLongRunningProcess(t *testing.T) {
	// Skip integration test if running in CI or if SKIP_INTEGRATION is set
	if os.Getenv("CI") != "" || os.Getenv("SKIP_INTEGRATION") != "" {
		t.Skip("Skipping integration test")
	}

	executor := exec.NewCommandExecutor()
	vm := NewPodmanVM("ubuntu:20.04", executor)

	t.Run("should handle commands that take time", func(t *testing.T) {
		err := vm.Create()
		require.NoError(t, err, "Failed to create container")

		defer func() {
			if vm.containerID != "" {
				vm.Stop()
			}
		}()

		// Run a command that takes a few seconds
		result, err := vm.Run("sh", "-c", "sleep 2 && echo 'done'")
		require.NoError(t, err, "Failed to run sleep command")
		assert.Equal(t, 0, result.ExitCode, "Sleep command should succeed")
		assert.Equal(t, "done\n", result.Stdout, "Output should match")

		err = vm.Stop()
		require.NoError(t, err, "Failed to stop container")
	})
}
