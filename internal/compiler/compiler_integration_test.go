//go:build integration

package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TestGCCCompiler_Integration_CompileSimpleProgram tests compiling a simple C program.
func TestGCCCompiler_Integration_CompileSimpleProgram(t *testing.T) {
	// Check if GCC is available
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "compiler_integration_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	compiler := NewGCCCompiler(GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: tempDir,
	})

	testSeed := &seed.Seed{
		Meta: seed.Metadata{ID: 1},
		Content: `
#include <stdio.h>
int main() {
    printf("Hello, World!\n");
    return 0;
}
`,
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	assert.True(t, result.Success, "Compilation should succeed")
	assert.FileExists(t, result.BinaryPath)

	// Run the compiled binary to verify it works
	cmd := exec.Command(result.BinaryPath)
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "Hello, World!")
}

// TestGCCCompiler_Integration_CompileWithWarnings tests that warnings don't cause failure.
func TestGCCCompiler_Integration_CompileWithWarnings(t *testing.T) {
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "compiler_warnings_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	compiler := NewGCCCompiler(GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: tempDir,
		CFlags:  "-Wall",
	})

	// Code with unused variable warning
	testSeed := &seed.Seed{
		Meta: seed.Metadata{ID: 2},
		Content: `
#include <stdio.h>
int main() {
    int unused_var = 42;
    printf("Warnings test\n");
    return 0;
}
`,
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	assert.True(t, result.Success, "Compilation should succeed despite warnings")
	assert.Contains(t, result.Stderr, "unused", "Should have unused variable warning")
}

// TestGCCCompiler_Integration_CompileError tests handling of compilation errors.
func TestGCCCompiler_Integration_CompileError(t *testing.T) {
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "compiler_error_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	compiler := NewGCCCompiler(GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: tempDir,
	})

	// Invalid C code
	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 3},
		Content: `this is not valid C code at all;`,
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err) // Should not return error, just report failure
	assert.False(t, result.Success, "Compilation should fail")
	assert.NotEmpty(t, result.Stderr, "Should have error message")
}

// TestGCCCompiler_Integration_CompileWithCoverage tests coverage instrumentation.
func TestGCCCompiler_Integration_CompileWithCoverage(t *testing.T) {
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "compiler_coverage_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Don't use CoverageDir to avoid the -fprofile-dir flag issue
	compiler := NewGCCCompiler(GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: tempDir,
	})

	testSeed := &seed.Seed{
		Meta: seed.Metadata{ID: 4},
		Content: `
#include <stdio.h>
int add(int a, int b) { return a + b; }
int main() {
    printf("%d\n", add(1, 2));
    return 0;
}
`,
	}

	result, err := compiler.CompileWithCoverage(testSeed)
	require.NoError(t, err)

	if !result.Success {
		t.Logf("Compilation stderr: %s", result.Stderr)
	}
	assert.True(t, result.Success, "Compilation with coverage should succeed")
	assert.FileExists(t, result.BinaryPath)

	// Run the binary to generate coverage data
	cmd := exec.Command(result.BinaryPath)
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "3")

	// Check that .gcno file was created (compile-time coverage notes)
	gcnoFiles, _ := filepath.Glob(filepath.Join(tempDir, "*.gcno"))
	assert.NotEmpty(t, gcnoFiles, "Should have .gcno coverage file")
}

// TestGCCCompiler_Integration_MultipleSeeds tests compiling multiple seeds.
func TestGCCCompiler_Integration_MultipleSeeds(t *testing.T) {
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "compiler_multi_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	compiler := NewGCCCompiler(GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: tempDir,
	})

	seeds := []*seed.Seed{
		{
			Meta:    seed.Metadata{ID: 10},
			Content: `int main() { return 0; }`,
		},
		{
			Meta:    seed.Metadata{ID: 11},
			Content: `int main() { return 1; }`,
		},
		{
			Meta:    seed.Metadata{ID: 12},
			Content: `int main() { return 42; }`,
		},
	}

	for _, s := range seeds {
		result, err := compiler.Compile(s)
		require.NoError(t, err)
		assert.True(t, result.Success)

		// Verify exit code matches
		cmd := exec.Command(result.BinaryPath)
		cmd.Run()
		expectedExitCode := int(s.Meta.ID) - 10
		if s.Meta.ID == 12 {
			expectedExitCode = 42
		}
		assert.Equal(t, expectedExitCode, cmd.ProcessState.ExitCode())
	}
}

// TestGCCCompiler_Integration_ComplexProgram tests compiling a more complex program.
func TestGCCCompiler_Integration_ComplexProgram(t *testing.T) {
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "compiler_complex_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	compiler := NewGCCCompiler(GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: tempDir,
	})

	testSeed := &seed.Seed{
		Meta: seed.Metadata{ID: 100},
		Content: `
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    int id;
    char name[32];
} Person;

int compare_persons(const void *a, const void *b) {
    return ((Person*)a)->id - ((Person*)b)->id;
}

int main() {
    Person people[3] = {
        {3, "Charlie"},
        {1, "Alice"},
        {2, "Bob"}
    };
    
    qsort(people, 3, sizeof(Person), compare_persons);
    
    for (int i = 0; i < 3; i++) {
        printf("%d: %s\n", people[i].id, people[i].name);
    }
    
    return 0;
}
`,
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	assert.True(t, result.Success)

	cmd := exec.Command(result.BinaryPath)
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "1: Alice")
	assert.Contains(t, string(output), "2: Bob")
	assert.Contains(t, string(output), "3: Charlie")
}

// TestCrossGCCCompiler_Integration_Aarch64 tests cross-compilation for aarch64.
func TestCrossGCCCompiler_Integration_Aarch64(t *testing.T) {
	_, err := exec.LookPath("aarch64-linux-gnu-gcc")
	if err != nil {
		t.Skip("aarch64-linux-gnu-gcc not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "compiler_cross_aarch64_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	compiler := NewCrossGCCCompiler(CrossGCCCompilerConfig{
		GCCCompilerConfig: GCCCompilerConfig{
			GCCPath: "aarch64-linux-gnu-gcc",
			WorkDir: tempDir,
			CFlags:  "-static",
		},
		TargetArch: "aarch64",
		Sysroot:    "/usr/aarch64-linux-gnu",
	})

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 200},
		Content: `int main() { return 0; }`,
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.FileExists(t, result.BinaryPath)
	assert.Equal(t, "aarch64", compiler.GetTargetArch())

	// Verify it's an aarch64 binary using file command
	fileCmd := exec.Command("file", result.BinaryPath)
	fileOutput, err := fileCmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(fileOutput), "ARM aarch64")
}

// TestCrossGCCCompiler_Integration_Riscv64 tests cross-compilation for riscv64.
func TestCrossGCCCompiler_Integration_Riscv64(t *testing.T) {
	_, err := exec.LookPath("riscv64-linux-gnu-gcc")
	if err != nil {
		t.Skip("riscv64-linux-gnu-gcc not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "compiler_cross_riscv64_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	compiler := NewCrossGCCCompiler(CrossGCCCompilerConfig{
		GCCCompilerConfig: GCCCompilerConfig{
			GCCPath: "riscv64-linux-gnu-gcc",
			WorkDir: tempDir,
			CFlags:  "-static",
		},
		TargetArch: "riscv64",
		Sysroot:    "/usr/riscv64-linux-gnu",
	})

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 201},
		Content: `int main() { return 0; }`,
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	assert.True(t, result.Success)

	// Verify it's a riscv64 binary
	fileCmd := exec.Command("file", result.BinaryPath)
	fileOutput, err := fileCmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(fileOutput), "RISC-V")
}

// TestGCCCompiler_Integration_StackProtection tests stack protection compilation.
func TestGCCCompiler_Integration_StackProtection(t *testing.T) {
	_, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("GCC not found, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "compiler_stack_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	compiler := NewGCCCompiler(GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: tempDir,
		CFlags:  "-fstack-protector-strong",
	})

	testSeed := &seed.Seed{
		Meta: seed.Metadata{ID: 300},
		Content: `
#include <stdio.h>
#include <string.h>

void vulnerable_function(char *input) {
    char buffer[64];
    strcpy(buffer, input);
    printf("Buffer: %s\n", buffer);
}

int main() {
    vulnerable_function("safe input");
    return 0;
}
`,
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	assert.True(t, result.Success)

	// Run with safe input - should succeed
	cmd := exec.Command(result.BinaryPath)
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "safe input")
}
