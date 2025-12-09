//go:build integration

package oracle

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TestCanaryOracle_Integration_NoCanaryProtection tests detection of buffer overflow without canary.
func TestCanaryOracle_Integration_NoCanaryProtection(t *testing.T) {
	// Check if GCC is available
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "canary_integration_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a vulnerable C program WITHOUT stack canary protection
	sourceCode := `
#include <stdio.h>
#include <string.h>

int vulnerable() {
    char buf[16];
    char input[256];
    fgets(input, 256, stdin);
    strcpy(buf, input);  // Dangerous - potential overflow
    return 0;
}

int main() {
    vulnerable();
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "vuln.c")
	binaryPath := filepath.Join(tempDir, "vuln")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Compile WITHOUT stack protector
	cmd := exec.Command("gcc", "-fno-stack-protector", "-z", "execstack",
		"-o", binaryPath, sourcePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Compilation output: %s", output)
		t.Skipf("Failed to compile test program: %v", err)
	}

	// Create canary oracle
	oracle, err := New("canary", map[string]interface{}{
		"max_buffer_size": 200,
	}, nil, nil, "")
	require.NoError(t, err)

	// Create test seed
	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 1},
		Content: sourceCode,
	}

	// Create real executor
	executor := &RealExecutor{}

	// Test with context
	ctx := &AnalyzeContext{
		BinaryPath: binaryPath,
		Executor:   executor,
	}

	// Analyze - should detect SIGSEGV when buffer is overflowed
	report, err := oracle.Analyze(testSeed, ctx, nil)
	require.NoError(t, err)

	// Without canary protection, large input should cause SIGSEGV
	// The oracle should detect this as a vulnerability
	t.Logf("Oracle report: %+v", report)

	// Note: This test is environment-dependent and may not always trigger
	// due to ASLR and other security features
}

// TestCanaryOracle_Integration_WithCanaryProtection tests that canary protection is detected.
func TestCanaryOracle_Integration_WithCanaryProtection(t *testing.T) {
	// Check if GCC is available
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "canary_integration_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a vulnerable C program WITH stack canary protection
	sourceCode := `
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

int vulnerable() {
    char buf[16];
    char *input = (char*)malloc(256);
    fgets(input, 256, stdin);
    if (strlen(input) > 100) {
        strcpy(buf, input);  // Intentional overflow
    }
    free(input);
    return 0;
}

int main() {
    vulnerable();
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "vuln_canary.c")
	binaryPath := filepath.Join(tempDir, "vuln_canary")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Compile WITH stack protector (default in most modern GCC)
	cmd := exec.Command("gcc", "-fstack-protector-all",
		"-o", binaryPath, sourcePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Compilation output: %s", output)
		t.Skipf("Failed to compile test program: %v", err)
	}

	// Create canary oracle
	oracle, err := New("canary", map[string]interface{}{
		"max_buffer_size": 300,
	}, nil, nil, "")
	require.NoError(t, err)

	// Create test seed
	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 2},
		Content: sourceCode,
	}

	// Create real executor
	executor := &RealExecutor{}

	// Test with context
	ctx := &AnalyzeContext{
		BinaryPath: binaryPath,
		Executor:   executor,
	}

	// Analyze - should detect SIGABRT from canary check
	report, err := oracle.Analyze(testSeed, ctx, nil)
	require.NoError(t, err)

	// With canary protection, large input should cause SIGABRT
	// The oracle should detect canary is working (SAFE)
	t.Logf("Oracle report: %+v", report)
}

// TestCanaryOracle_Integration_BinarySearch tests binary search accuracy.
func TestCanaryOracle_Integration_BinarySearch(t *testing.T) {
	// Check if GCC is available
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "canary_binarysearch_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a program with a known buffer size
	bufferSize := 32
	sourceCode := `
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

int main() {
    char buf[32];
    char *input = (char*)malloc(4096);
    
    // Read input
    size_t len = fread(input, 1, 4096, stdin);
    
    // Copy to buffer (intentional overflow possible)
    memcpy(buf, input, len);
    
    free(input);
    return 0;
}
`
	sourcePath := filepath.Join(tempDir, "binarysearch.c")
	binaryPath := filepath.Join(tempDir, "binarysearch")

	err = os.WriteFile(sourcePath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Compile without stack protector to get clean SIGSEGV
	cmd := exec.Command("gcc", "-fno-stack-protector", "-z", "execstack",
		"-o", binaryPath, sourcePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Compilation output: %s", output)
		t.Skipf("Failed to compile test program: %v", err)
	}

	// Create canary oracle
	oracle, err := New("canary", map[string]interface{}{
		"max_buffer_size": 200,
	}, nil, nil, "")
	require.NoError(t, err)

	// Create test seed
	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 3},
		Content: sourceCode,
	}

	// Create real executor
	executor := &RealExecutor{}

	// Test with context
	ctx := &AnalyzeContext{
		BinaryPath: binaryPath,
		Executor:   executor,
	}

	// Analyze - binary search should find crash point near buffer size
	report, err := oracle.Analyze(testSeed, ctx, nil)
	require.NoError(t, err)

	t.Logf("Oracle report: %+v", report)
	t.Logf("Expected crash around size %d", bufferSize)

	// Note: Exact crash size depends on stack layout, alignment, etc.
	// This test mainly verifies the oracle runs without errors
}

// RealExecutor implements the Executor interface for integration tests.
type RealExecutor struct{}

func (r *RealExecutor) ExecuteWithInput(binaryPath string, stdin string) (exitCode int, stdout string, stderr string, err error) {
	cmd := exec.Command(binaryPath)

	// Set stdin
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return 0, "", "", err
	}

	// Get stdout/stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, "", "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return 0, "", "", err
	}

	// Start command
	if err := cmd.Start(); err != nil {
		return 0, "", "", err
	}

	// Write stdin
	_, err = stdinPipe.Write([]byte(stdin))
	if err != nil {
		return 0, "", "", err
	}
	stdinPipe.Close()

	// Read outputs
	stdoutBytes, _ := io.ReadAll(stdoutPipe)
	stderrBytes, _ := io.ReadAll(stderrPipe)

	// Wait for completion
	err = cmd.Wait()
	exitCode = cmd.ProcessState.ExitCode()

	return exitCode, string(stdoutBytes), string(stderrBytes), nil
}

func (r *RealExecutor) ExecuteWithArgs(binaryPath string, args ...string) (exitCode int, stdout string, stderr string, err error) {
	cmd := exec.Command(binaryPath, args...)

	// Get stdout/stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, "", "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return 0, "", "", err
	}

	// Start command
	if err := cmd.Start(); err != nil {
		return 0, "", "", err
	}

	// Read outputs
	stdoutBytes, _ := io.ReadAll(stdoutPipe)
	stderrBytes, _ := io.ReadAll(stderrPipe)

	// Wait for completion
	err = cmd.Wait()
	exitCode = cmd.ProcessState.ExitCode()

	return exitCode, string(stdoutBytes), string(stderrBytes), nil
}
