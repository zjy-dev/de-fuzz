//go:build integration

package exec

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommandExecutor_Integration_Echo tests running echo command.
func TestCommandExecutor_Integration_Echo(t *testing.T) {
	executor := NewCommandExecutor()

	result, err := executor.Run("echo", "Hello, World!")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "Hello, World!")
}

// TestCommandExecutor_Integration_Cat tests reading file content.
func TestCommandExecutor_Integration_Cat(t *testing.T) {
	tempFile, err := os.CreateTemp("", "exec_test_")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	content := "Test content for cat command"
	_, err = tempFile.WriteString(content)
	require.NoError(t, err)
	tempFile.Close()

	executor := NewCommandExecutor()
	result, err := executor.Run("cat", tempFile.Name())
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, content, result.Stdout)
}

// TestCommandExecutor_Integration_Ls tests listing directory.
func TestCommandExecutor_Integration_Ls(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "exec_test_ls_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create some files
	files := []string{"file1.txt", "file2.txt", "file3.txt"}
	for _, f := range files {
		path := filepath.Join(tempDir, f)
		err := os.WriteFile(path, []byte("content"), 0644)
		require.NoError(t, err)
	}

	executor := NewCommandExecutor()
	result, err := executor.Run("ls", tempDir)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	for _, f := range files {
		assert.Contains(t, result.Stdout, f)
	}
}

// TestCommandExecutor_Integration_NonZeroExit tests non-zero exit code.
func TestCommandExecutor_Integration_NonZeroExit(t *testing.T) {
	executor := NewCommandExecutor()

	// Use false command which always returns 1
	result, err := executor.Run("false")
	require.NoError(t, err) // Should not return error for non-zero exit
	assert.Equal(t, 1, result.ExitCode)
}

// TestCommandExecutor_Integration_Stderr tests stderr capture.
func TestCommandExecutor_Integration_Stderr(t *testing.T) {
	executor := NewCommandExecutor()

	// ls on non-existent file should produce stderr
	result, err := executor.Run("ls", "/nonexistent/path/that/does/not/exist")
	require.NoError(t, err)
	assert.NotEqual(t, 0, result.ExitCode)
	assert.NotEmpty(t, result.Stderr)
}

// TestCommandExecutor_Integration_CommandNotFound tests handling of non-existent command.
func TestCommandExecutor_Integration_CommandNotFound(t *testing.T) {
	executor := NewCommandExecutor()

	result, err := executor.Run("this_command_definitely_does_not_exist_12345")
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestCommandExecutor_Integration_Grep tests grep command.
func TestCommandExecutor_Integration_Grep(t *testing.T) {
	tempFile, err := os.CreateTemp("", "exec_test_grep_")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	content := "line1\nfind this line\nline3\nanother find here\n"
	_, err = tempFile.WriteString(content)
	require.NoError(t, err)
	tempFile.Close()

	executor := NewCommandExecutor()
	result, err := executor.Run("grep", "find", tempFile.Name())
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "find this line")
	assert.Contains(t, result.Stdout, "another find here")
}

// TestCommandExecutor_Integration_Wc tests word count command.
func TestCommandExecutor_Integration_Wc(t *testing.T) {
	tempFile, err := os.CreateTemp("", "exec_test_wc_")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	content := "one two three\nfour five\n"
	_, err = tempFile.WriteString(content)
	require.NoError(t, err)
	tempFile.Close()

	executor := NewCommandExecutor()
	result, err := executor.Run("wc", "-w", tempFile.Name())
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "5") // 5 words
}

// TestCommandExecutor_Integration_Pwd tests getting current directory.
func TestCommandExecutor_Integration_Pwd(t *testing.T) {
	executor := NewCommandExecutor()

	result, err := executor.Run("pwd")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.NotEmpty(t, result.Stdout)
}

// TestCommandExecutor_Integration_Env tests environment variables.
func TestCommandExecutor_Integration_Env(t *testing.T) {
	executor := NewCommandExecutor()

	result, err := executor.Run("printenv", "PATH")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.NotEmpty(t, result.Stdout)
}

// TestCommandExecutor_Integration_Sh tests running shell commands.
func TestCommandExecutor_Integration_Sh(t *testing.T) {
	executor := NewCommandExecutor()

	result, err := executor.Run("sh", "-c", "echo $((2 + 3))")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "5")
}

// TestCommandExecutor_Integration_LargeOutput tests handling large output.
func TestCommandExecutor_Integration_LargeOutput(t *testing.T) {
	executor := NewCommandExecutor()

	// Generate large output using seq
	result, err := executor.Run("seq", "1", "10000")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "1\n")
	assert.Contains(t, result.Stdout, "10000")
}

// TestCommandExecutor_Integration_Timeout tests command with sleep.
func TestCommandExecutor_Integration_Sleep(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping sleep test in short mode")
	}

	executor := NewCommandExecutor()

	// Use timeout to wrap sleep
	result, err := executor.Run("timeout", "1", "sleep", "0.1")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
}

// TestCommandExecutor_Integration_Head tests head command.
func TestCommandExecutor_Integration_Head(t *testing.T) {
	tempFile, err := os.CreateTemp("", "exec_test_head_")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	for i := 1; i <= 20; i++ {
		tempFile.WriteString("line\n")
	}
	tempFile.Close()

	executor := NewCommandExecutor()
	result, err := executor.Run("head", "-n", "5", tempFile.Name())
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Count lines in output
	lines := 0
	for _, c := range result.Stdout {
		if c == '\n' {
			lines++
		}
	}
	assert.Equal(t, 5, lines)
}

// TestCommandExecutor_Integration_Uname tests getting system info.
func TestCommandExecutor_Integration_Uname(t *testing.T) {
	executor := NewCommandExecutor()

	result, err := executor.Run("uname", "-s")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	if runtime.GOOS == "linux" {
		assert.Contains(t, result.Stdout, "Linux")
	}
}

// TestCommandExecutor_Integration_CompileAndRun tests compiling and running C code.
func TestCommandExecutor_Integration_CompileAndRun(t *testing.T) {
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping test")
	}

	tempDir, err := os.MkdirTemp("", "exec_compile_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Write source file
	srcPath := filepath.Join(tempDir, "test.c")
	binPath := filepath.Join(tempDir, "test")
	err = os.WriteFile(srcPath, []byte(`
#include <stdio.h>
int main() {
    printf("Compiled and run successfully!\n");
    return 0;
}
`), 0644)
	require.NoError(t, err)

	executor := NewCommandExecutor()

	// Compile
	result, err := executor.Run("gcc", "-o", binPath, srcPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "Compilation failed: %s", result.Stderr)

	// Run
	result, err = executor.Run(binPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "Compiled and run successfully!")
}

// TestCommandExecutor_Integration_Pipeline tests simulating pipeline behavior.
func TestCommandExecutor_Integration_Pipeline(t *testing.T) {
	executor := NewCommandExecutor()

	// Use sh -c to run a pipeline
	result, err := executor.Run("sh", "-c", "echo 'hello world' | tr 'h' 'H'")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "Hello world")
}
