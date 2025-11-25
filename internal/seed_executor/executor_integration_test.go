//go:build integration

package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	"github.com/zjy-dev/de-fuzz/internal/vm"
)

// TestLocalExecutor_Integration_Execute tests local execution with real binaries.
func TestLocalExecutor_Integration_Execute(t *testing.T) {
	// Check if gcc is available
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_local_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a simple C program
	sourceCode := `
#include <stdio.h>
int main(int argc, char *argv[]) {
    if (argc > 1) {
        printf("Arg: %s\n", argv[1]);
    } else {
        printf("Hello, World!\n");
    }
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "test.c")
	binaryPath := filepath.Join(tempDir, "test")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Compile the program
	cmd := exec.Command("gcc", "-o", binaryPath, sourcePath)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Compilation failed: %s", string(output))

	// Create seed with test cases
	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 1},
		Content: sourceCode,
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "Hello, World!"},
			{RunningCommand: "./a.out foo", ExpectedResult: "Arg: foo"},
		},
	}

	executor := NewLocalExecutor(5)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)
	require.Equal(t, 2, len(results))

	assert.Contains(t, results[0].Stdout, "Hello, World!")
	assert.Equal(t, 0, results[0].ExitCode)

	assert.Contains(t, results[1].Stdout, "Arg: foo")
	assert.Equal(t, 0, results[1].ExitCode)
}

// TestLocalExecutor_Integration_ExitCode tests exit code handling.
func TestLocalExecutor_Integration_ExitCode(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_exit_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourceCode := `
int main() {
    return 42;
}
`
	sourcePath := filepath.Join(tempDir, "exit.c")
	binaryPath := filepath.Join(tempDir, "exit")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	cmd := exec.Command("gcc", "-o", binaryPath, sourcePath)
	_, err = cmd.CombinedOutput()
	require.NoError(t, err)

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: ""},
		},
	}

	executor := NewLocalExecutor(5)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)
	require.Equal(t, 1, len(results))

	assert.Equal(t, 42, results[0].ExitCode)
}

// TestLocalExecutor_Integration_Stderr tests stderr capture.
func TestLocalExecutor_Integration_Stderr(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_stderr_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourceCode := `
#include <stdio.h>
int main() {
    fprintf(stderr, "Error message\n");
    printf("Normal output\n");
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "stderr.c")
	binaryPath := filepath.Join(tempDir, "stderr")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	cmd := exec.Command("gcc", "-o", binaryPath, sourcePath)
	_, err = cmd.CombinedOutput()
	require.NoError(t, err)

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: ""},
		},
	}

	executor := NewLocalExecutor(5)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)

	assert.Contains(t, results[0].Stdout, "Normal output")
	assert.Contains(t, results[0].Stderr, "Error message")
}

// TestLocalExecutor_Integration_Timeout tests timeout handling.
func TestLocalExecutor_Integration_Timeout(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_timeout_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Program that sleeps forever
	sourceCode := `
#include <unistd.h>
int main() {
    while(1) { sleep(1); }
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "timeout.c")
	binaryPath := filepath.Join(tempDir, "timeout")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	cmd := exec.Command("gcc", "-o", binaryPath, sourcePath)
	_, err = cmd.CombinedOutput()
	require.NoError(t, err)

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: ""},
		},
	}

	// Use 1 second timeout
	executor := NewLocalExecutor(1)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)

	// Should have been killed (exit code 137 = 128 + 9 (SIGKILL))
	assert.NotEqual(t, 0, results[0].ExitCode)
}

// TestLocalExecutor_Integration_StackSmashing tests stack smashing detection.
func TestLocalExecutor_Integration_StackSmashing(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_stack_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourceCode := `
#include <string.h>
void vulnerable() {
    char buf[8];
    strcpy(buf, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
}
int main() {
    vulnerable();
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "stack.c")
	binaryPath := filepath.Join(tempDir, "stack")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Compile with stack protection
	cmd := exec.Command("gcc", "-fstack-protector-all", "-o", binaryPath, sourcePath)
	_, err = cmd.CombinedOutput()
	require.NoError(t, err)

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: ""},
		},
	}

	executor := NewLocalExecutor(5)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)

	// Should detect stack smashing (SIGABRT = 134) or crash (SIGSEGV = 139)
	// Note: Some systems may handle buffer overflow differently
	assert.NotEqual(t, 0, results[0].ExitCode,
		"Expected non-zero exit code for stack smashing, got %d", results[0].ExitCode)
	t.Logf("Stack smashing test exit code: %d", results[0].ExitCode)
}

// TestQEMUExecutor_Integration_AArch64 tests QEMU executor with AArch64.
func TestQEMUExecutor_Integration_AArch64(t *testing.T) {
	// Check for QEMU and cross-compiler
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not available")
	}
	if _, err := exec.LookPath("aarch64-linux-gnu-gcc"); err != nil {
		t.Skip("aarch64-linux-gnu-gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_qemu_aarch64_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourceCode := `
#include <stdio.h>
int main() {
    printf("Hello from AArch64!\n");
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "hello.c")
	binaryPath := filepath.Join(tempDir, "hello")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Cross-compile for AArch64
	cmd := exec.Command("aarch64-linux-gnu-gcc", "-static", "-o", binaryPath, sourcePath)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Cross-compilation failed: %s", string(output))

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "Hello from AArch64!"},
		},
	}

	cfg := vm.QEMUConfig{
		QEMUPath: "qemu-aarch64",
	}
	executor := NewQEMUExecutor(cfg, 10)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)
	require.Equal(t, 1, len(results))

	assert.Contains(t, results[0].Stdout, "Hello from AArch64!")
	assert.Equal(t, 0, results[0].ExitCode)
}

// TestQEMUExecutor_Integration_RISCV64 tests QEMU executor with RISC-V 64.
func TestQEMUExecutor_Integration_RISCV64(t *testing.T) {
	if _, err := exec.LookPath("qemu-riscv64"); err != nil {
		t.Skip("qemu-riscv64 not available")
	}
	if _, err := exec.LookPath("riscv64-linux-gnu-gcc"); err != nil {
		t.Skip("riscv64-linux-gnu-gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_qemu_riscv64_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourceCode := `
#include <stdio.h>
int main() {
    printf("Hello from RISC-V 64!\n");
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "hello.c")
	binaryPath := filepath.Join(tempDir, "hello")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	cmd := exec.Command("riscv64-linux-gnu-gcc", "-static", "-o", binaryPath, sourcePath)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Cross-compilation failed: %s", string(output))

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "Hello from RISC-V 64!"},
		},
	}

	cfg := vm.QEMUConfig{
		QEMUPath: "qemu-riscv64",
	}
	executor := NewQEMUExecutor(cfg, 10)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)

	assert.Contains(t, results[0].Stdout, "Hello from RISC-V 64!")
	assert.Equal(t, 0, results[0].ExitCode)
}

// TestParseCommand_Integration tests command parsing.
func TestParseCommand_Integration(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		binaryPath string
		expected   []string
	}{
		{
			name:       "Simple ./a.out",
			command:    "./a.out",
			binaryPath: "/tmp/test",
			expected:   []string{},
		},
		{
			name:       "With arguments",
			command:    "./a.out arg1 arg2 arg3",
			binaryPath: "/tmp/test",
			expected:   []string{"arg1", "arg2", "arg3"},
		},
		{
			name:       "With $BINARY placeholder",
			command:    "$BINARY -v",
			binaryPath: "/tmp/test",
			expected:   []string{"-v"},
		},
		{
			name:       "With ./program placeholder",
			command:    "./program --help",
			binaryPath: "/tmp/test",
			expected:   []string{"--help"},
		},
		{
			name:       "Empty command",
			command:    "",
			binaryPath: "/tmp/test",
			expected:   []string{},
		},
		{
			name:       "Just binary path",
			command:    "/tmp/test",
			binaryPath: "/tmp/test",
			expected:   []string{},
		},
		{
			name:       "Multiple arguments with spaces",
			command:    "./a.out foo bar baz",
			binaryPath: "/tmp/test",
			expected:   []string{"foo", "bar", "baz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCommand(tt.command, tt.binaryPath)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestExecutorInterface_Integration tests Executor interface implementation.
func TestExecutorInterface_Integration(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_interface_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourceCode := `
#include <stdio.h>
int main() {
    printf("Interface test\n");
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "test.c")
	binaryPath := filepath.Join(tempDir, "test")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	cmd := exec.Command("gcc", "-o", binaryPath, sourcePath)
	_, err = cmd.CombinedOutput()
	require.NoError(t, err)

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "Interface test"},
		},
	}

	// Test with Executor interface
	var executor Executor = NewLocalExecutor(5)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)
	assert.Contains(t, results[0].Stdout, "Interface test")
}

// TestLocalExecutor_Integration_MultipleTestCases tests multiple test cases execution.
func TestLocalExecutor_Integration_MultipleTestCases(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_multi_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourceCode := `
#include <stdio.h>
#include <stdlib.h>
int main(int argc, char *argv[]) {
    if (argc < 2) {
        return 1;
    }
    int n = atoi(argv[1]);
    printf("%d\n", n * 2);
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "mult.c")
	binaryPath := filepath.Join(tempDir, "mult")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	cmd := exec.Command("gcc", "-o", binaryPath, sourcePath)
	_, err = cmd.CombinedOutput()
	require.NoError(t, err)

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out 5", ExpectedResult: "10"},
			{RunningCommand: "./a.out 10", ExpectedResult: "20"},
			{RunningCommand: "./a.out 0", ExpectedResult: "0"},
			{RunningCommand: "./a.out 100", ExpectedResult: "200"},
		},
	}

	executor := NewLocalExecutor(5)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)
	require.Equal(t, 4, len(results))

	assert.Contains(t, results[0].Stdout, "10")
	assert.Contains(t, results[1].Stdout, "20")
	assert.Contains(t, results[2].Stdout, "0")
	assert.Contains(t, results[3].Stdout, "200")
}

// TestLocalExecutor_Integration_NoTestCases tests execution with no test cases.
func TestLocalExecutor_Integration_NoTestCases(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_notest_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourceCode := `int main() { return 0; }`
	sourcePath := filepath.Join(tempDir, "empty.c")
	binaryPath := filepath.Join(tempDir, "empty")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	cmd := exec.Command("gcc", "-o", binaryPath, sourcePath)
	_, err = cmd.CombinedOutput()
	require.NoError(t, err)

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{}, // No test cases
	}

	executor := NewLocalExecutor(5)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)
	assert.Equal(t, 0, len(results))
}

// TestLocalExecutor_Integration_NoTimeout tests execution without timeout.
func TestLocalExecutor_Integration_NoTimeout(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tempDir, err := os.MkdirTemp("", "executor_notimeout_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourceCode := `
#include <stdio.h>
int main() {
    printf("No timeout\n");
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "quick.c")
	binaryPath := filepath.Join(tempDir, "quick")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	cmd := exec.Command("gcc", "-o", binaryPath, sourcePath)
	_, err = cmd.CombinedOutput()
	require.NoError(t, err)

	testSeed := &seed.Seed{
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "No timeout"},
		},
	}

	// Use 0 for no timeout
	executor := NewLocalExecutor(0)
	results, err := executor.Execute(testSeed, binaryPath)
	require.NoError(t, err)

	assert.Contains(t, results[0].Stdout, "No timeout")
}
