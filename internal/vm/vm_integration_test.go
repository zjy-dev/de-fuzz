package vm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"defuzz/internal/exec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPodmanVM_Integration(t *testing.T) {
	// Skip integration test if INTEGRATION_TEST environment variable is not set
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	// Use our fuzzing environment image, fallback to Alpine if not available
	executor := exec.NewCommandExecutor()
	imageName := "defuzz-env:latest"

	// Check if our custom image exists, fallback to Alpine
	checkCmd := exec.NewCommandExecutor()
	if result, err := checkCmd.Run("podman", "images", "-q", imageName); err != nil || result.Stdout == "" {
		t.Logf("Custom fuzzing image not found, using Alpine Linux")
		imageName = "alpine:latest"
	} else {
		t.Logf("Using custom fuzzing environment: %s", imageName)
	}

	vm := NewPodmanVM(imageName, executor)

	// Test VM lifecycle
	t.Run("Full VM Lifecycle", func(t *testing.T) {
		// Create VM
		err := vm.Create()
		require.NoError(t, err, "Failed to create VM")
		assert.NotEmpty(t, vm.containerID, "Container ID should not be empty")

		// Give container time to start
		time.Sleep(2 * time.Second)

		// Create a test workspace directory and files
		tempDir := t.TempDir()

		// Create a simple C program
		cFile := filepath.Join(tempDir, "test.c")
		cContent := "#include <stdio.h>\nint main() {\n    printf(\"Hello from container!\");\n    return 0;\n}"
		err = os.WriteFile(cFile, []byte(cContent), 0644)
		require.NoError(t, err)

		// Create a Makefile
		makeFile := filepath.Join(tempDir, "Makefile")
		makeContent := "all:\n\tgcc -o test test.c\n\nclean:\n\trm -f test\n"
		err = os.WriteFile(makeFile, []byte(makeContent), 0644)
		require.NoError(t, err)

		// Create a run script
		runScript := filepath.Join(tempDir, "run.sh")
		runContent := "#!/bin/bash\nmake all\n./test\n"
		err = os.WriteFile(runScript, []byte(runContent), 0755)
		require.NoError(t, err)

		// Run the script in VM
		result, err := vm.Run("./test", "./run.sh")
		require.NoError(t, err, "Failed to run script in VM")
		assert.NotNil(t, result, "Result should not be nil")
		assert.Contains(t, result.Stdout, "Hello from container!", "Expected output not found")

		// Stop VM
		err = vm.Stop()
		assert.NoError(t, err, "Failed to stop VM")
	})
}

func TestPodmanVM_Integration_ErrorHandling(t *testing.T) {
	// Skip integration test if INTEGRATION_TEST environment variable is not set
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	executor := exec.NewCommandExecutor()
	vm := NewPodmanVM("alpine:latest", executor)

	t.Run("Run Without Create", func(t *testing.T) {
		_, err := vm.Run("./nonexistent", "./nonexistent.sh")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vm is not created")
	})

	t.Run("Run Nonexistent Script", func(t *testing.T) {
		// Create VM first
		err := vm.Create()
		require.NoError(t, err)
		defer vm.Stop()

		// Give container time to start
		time.Sleep(2 * time.Second)

		// Try to run a nonexistent script
		result, err := vm.Run("./nonexistent", "./nonexistent.sh")
		// This should not return an error, but the execution result should show the failure
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotEqual(t, 0, result.ExitCode, "Exit code should be non-zero for failed execution")
	})
}

func TestPodmanVM_Integration_ARMFuzzing(t *testing.T) {
	// Skip integration test if INTEGRATION_TEST environment variable is not set
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	executor := exec.NewCommandExecutor()

	// Only run this test if our fuzzing image is available
	imageName := "defuzz-env:latest"
	if result, err := executor.Run("podman", "images", "-q", imageName); err != nil || result.Stdout == "" {
		t.Skip("Skipping ARM fuzzing test - custom fuzzing image not available. Run ./scripts/build-container.sh first.")
	}

	vm := NewPodmanVM(imageName, executor)

	t.Run("ARM Cross Compilation and Emulation", func(t *testing.T) {
		// Create VM
		err := vm.Create()
		require.NoError(t, err)
		defer vm.Stop()

		// Give container time to start
		time.Sleep(2 * time.Second)

		// Create a test workspace directory and files
		tempDir := t.TempDir()

		// Create a simple C program with potential vulnerability
		cFile := filepath.Join(tempDir, "vuln.c")
		cContent := `#include <stdio.h>
#include <string.h>

int main(int argc, char *argv[]) {
    char buffer[64];
    if (argc > 1) {
        strcpy(buffer, argv[1]); // Potential buffer overflow
        printf("Input: %s\n", buffer);
    }
    return 0;
}`
		err = os.WriteFile(cFile, []byte(cContent), 0644)
		require.NoError(t, err)

		// Create a Makefile for ARM compilation
		makeFile := filepath.Join(tempDir, "Makefile")
		makeContent := `all: vuln-x86 vuln-arm vuln-aarch64

vuln-x86: vuln.c
	gcc -o vuln-x86 vuln.c

vuln-arm: vuln.c
	arm-linux-gnueabi-gcc -static -o vuln-arm vuln.c

vuln-aarch64: vuln.c
	aarch64-linux-gnu-gcc -static -o vuln-aarch64 vuln.c

test-arm: vuln-arm
	qemu-arm-static ./vuln-arm "test input"

test-aarch64: vuln-aarch64
	qemu-aarch64-static ./vuln-aarch64 "test input"

clean:
	rm -f vuln-x86 vuln-arm vuln-aarch64
`
		err = os.WriteFile(makeFile, []byte(makeContent), 0644)
		require.NoError(t, err)

		// Create a run script
		runScript := filepath.Join(tempDir, "run.sh")
		runContent := `#!/bin/bash
set -e
echo "=== Building for multiple architectures ==="
make all

echo "=== Testing x86 binary ==="
./vuln-x86 "hello world"

echo "=== Testing ARM binary with QEMU ==="
make test-arm

echo "=== Testing AARCH64 binary with QEMU ==="
make test-aarch64

echo "=== File information ==="
file vuln-*

echo "=== ARM fuzzing environment ready ==="
`
		err = os.WriteFile(runScript, []byte(runContent), 0755)
		require.NoError(t, err)

		// Run the script in VM
		result, err := vm.Run("./vuln-x86", "./run.sh")
		require.NoError(t, err, "Failed to run ARM fuzzing test in VM")
		assert.NotNil(t, result, "Result should not be nil")
		assert.Contains(t, result.Stdout, "ARM fuzzing environment ready", "Expected completion message not found")
		assert.Contains(t, result.Stdout, "ARM", "ARM compilation should be mentioned")
		assert.Contains(t, result.Stdout, "AARCH64", "AARCH64 compilation should be mentioned")
	})
}
