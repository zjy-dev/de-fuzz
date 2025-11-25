// go:build integration
//go:build integration

package vm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestArchConfig holds configuration for a specific architecture's integration test.
type TestArchConfig struct {
	Name       string   // Architecture name (e.g., "aarch64")
	CrossCC    string   // Cross compiler (e.g., "aarch64-linux-gnu-gcc")
	QEMUBinary string   // QEMU binary (e.g., "qemu-aarch64")
	Sysroot    string   // Sysroot path (e.g., "/usr/aarch64-linux-gnu")
	ExtraQEMU  []string // Extra QEMU arguments
	ExtraCC    []string // Extra compiler flags
}

// architectures defines the test configurations for all supported architectures.
var architectures = []TestArchConfig{
	{
		Name:       "aarch64",
		CrossCC:    "aarch64-linux-gnu-gcc",
		QEMUBinary: "qemu-aarch64",
		Sysroot:    "/usr/aarch64-linux-gnu",
	},
	{
		Name:       "riscv64",
		CrossCC:    "riscv64-linux-gnu-gcc",
		QEMUBinary: "qemu-riscv64",
		Sysroot:    "/usr/riscv64-linux-gnu",
	},
	{
		Name:       "arm",
		CrossCC:    "arm-linux-gnueabihf-gcc",
		QEMUBinary: "qemu-arm",
		Sysroot:    "/usr/arm-linux-gnueabihf",
	},
	{
		Name:       "ppc64",
		CrossCC:    "powerpc64-linux-gnu-gcc",
		QEMUBinary: "qemu-ppc64",
		Sysroot:    "/usr/powerpc64-linux-gnu",
	},
	{
		Name:       "s390x",
		CrossCC:    "s390x-linux-gnu-gcc",
		QEMUBinary: "qemu-s390x",
		Sysroot:    "/usr/s390x-linux-gnu",
	},
}

// checkToolchainAvailable checks if the cross-compiler and QEMU are available.
func checkToolchainAvailable(t *testing.T, cfg TestArchConfig) bool {
	// Check cross-compiler
	_, err := exec.LookPath(cfg.CrossCC)
	if err != nil {
		t.Logf("Skipping %s: cross-compiler %s not found", cfg.Name, cfg.CrossCC)
		return false
	}

	// Check QEMU
	_, err = exec.LookPath(cfg.QEMUBinary)
	if err != nil {
		t.Logf("Skipping %s: QEMU %s not found", cfg.Name, cfg.QEMUBinary)
		return false
	}

	// Check sysroot exists
	if _, err := os.Stat(cfg.Sysroot); os.IsNotExist(err) {
		t.Logf("Skipping %s: sysroot %s not found", cfg.Name, cfg.Sysroot)
		return false
	}

	return true
}

// compileCrossProgram compiles a C program for the target architecture.
func compileCrossProgram(cfg TestArchConfig, sourceCode, outputPath string) error {
	// Create a temporary source file
	srcFile := outputPath + ".c"
	if err := os.WriteFile(srcFile, []byte(sourceCode), 0644); err != nil {
		return fmt.Errorf("failed to write source: %w", err)
	}
	defer os.Remove(srcFile)

	// Build compile command
	args := []string{"-static", "-o", outputPath, srcFile}
	args = append(cfg.ExtraCC, args...)

	cmd := exec.Command(cfg.CrossCC, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compilation failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// TestQEMUVM_Aarch64_HelloWorld tests running a simple aarch64 binary.
func TestQEMUVM_Aarch64_HelloWorld(t *testing.T) {
	cfg := findArchConfig("aarch64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("aarch64 toolchain not available")
	}

	runHelloWorldTest(t, *cfg)
}

// TestQEMUVM_Riscv64_HelloWorld tests running a simple riscv64 binary.
func TestQEMUVM_Riscv64_HelloWorld(t *testing.T) {
	cfg := findArchConfig("riscv64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("riscv64 toolchain not available")
	}

	runHelloWorldTest(t, *cfg)
}

// TestQEMUVM_Arm_HelloWorld tests running a simple ARM 32-bit binary.
func TestQEMUVM_Arm_HelloWorld(t *testing.T) {
	cfg := findArchConfig("arm")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("arm toolchain not available")
	}

	runHelloWorldTest(t, *cfg)
}

// TestQEMUVM_Ppc64_HelloWorld tests running a simple PowerPC 64-bit binary.
func TestQEMUVM_Ppc64_HelloWorld(t *testing.T) {
	cfg := findArchConfig("ppc64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("ppc64 toolchain not available")
	}

	runHelloWorldTest(t, *cfg)
}

// TestQEMUVM_S390x_HelloWorld tests running a simple IBM Z binary.
func TestQEMUVM_S390x_HelloWorld(t *testing.T) {
	cfg := findArchConfig("s390x")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("s390x toolchain not available")
	}

	runHelloWorldTest(t, *cfg)
}

// runHelloWorldTest runs the hello world test for a given architecture.
func runHelloWorldTest(t *testing.T, cfg TestArchConfig) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vm_test_%s_", cfg.Name))
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "hello")

	sourceCode := `
#include <stdio.h>
int main() {
    printf("Hello from %s!\n", "` + cfg.Name + `");
    return 0;
}
`

	err = compileCrossProgram(cfg, sourceCode, binaryPath)
	require.NoError(t, err, "Failed to compile for %s", cfg.Name)

	// Create QEMU VM
	vm := NewQEMUVM(QEMUConfig{
		QEMUPath:  cfg.QEMUBinary,
		Sysroot:   cfg.Sysroot,
		ExtraArgs: cfg.ExtraQEMU,
	})

	result, err := vm.Run(binaryPath)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode, "Expected exit code 0 for %s", cfg.Name)
	assert.Contains(t, result.Stdout, "Hello from "+cfg.Name)
}

// TestQEMUVM_Aarch64_ExitCode tests exit code handling on aarch64.
func TestQEMUVM_Aarch64_ExitCode(t *testing.T) {
	cfg := findArchConfig("aarch64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("aarch64 toolchain not available")
	}

	runExitCodeTest(t, *cfg)
}

// TestQEMUVM_Riscv64_ExitCode tests exit code handling on riscv64.
func TestQEMUVM_Riscv64_ExitCode(t *testing.T) {
	cfg := findArchConfig("riscv64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("riscv64 toolchain not available")
	}

	runExitCodeTest(t, *cfg)
}

// runExitCodeTest tests various exit codes for a given architecture.
func runExitCodeTest(t *testing.T, cfg TestArchConfig) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vm_exit_%s_", cfg.Name))
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testCases := []struct {
		name     string
		exitCode int
	}{
		{"exit_0", 0},
		{"exit_1", 1},
		{"exit_42", 42},
		{"exit_255", 255},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s_%s", cfg.Name, tc.name), func(t *testing.T) {
			binaryPath := filepath.Join(tempDir, tc.name)
			sourceCode := fmt.Sprintf(`
int main() {
    return %d;
}
`, tc.exitCode)

			err := compileCrossProgram(cfg, sourceCode, binaryPath)
			require.NoError(t, err)

			vm := NewQEMUVM(QEMUConfig{
				QEMUPath: cfg.QEMUBinary,
				Sysroot:  cfg.Sysroot,
			})

			result, err := vm.Run(binaryPath)
			require.NoError(t, err)
			assert.Equal(t, tc.exitCode, result.ExitCode)
		})
	}
}

// TestQEMUVM_Aarch64_Arguments tests passing command line arguments on aarch64.
func TestQEMUVM_Aarch64_Arguments(t *testing.T) {
	cfg := findArchConfig("aarch64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("aarch64 toolchain not available")
	}

	runArgumentsTest(t, *cfg)
}

// TestQEMUVM_Riscv64_Arguments tests passing command line arguments on riscv64.
func TestQEMUVM_Riscv64_Arguments(t *testing.T) {
	cfg := findArchConfig("riscv64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("riscv64 toolchain not available")
	}

	runArgumentsTest(t, *cfg)
}

// runArgumentsTest tests command line argument passing for a given architecture.
func runArgumentsTest(t *testing.T, cfg TestArchConfig) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vm_args_%s_", cfg.Name))
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "echo_args")
	sourceCode := `
#include <stdio.h>
int main(int argc, char *argv[]) {
    for (int i = 1; i < argc; i++) {
        printf("%s\n", argv[i]);
    }
    return 0;
}
`

	err = compileCrossProgram(cfg, sourceCode, binaryPath)
	require.NoError(t, err)

	vm := NewQEMUVM(QEMUConfig{
		QEMUPath: cfg.QEMUBinary,
		Sysroot:  cfg.Sysroot,
	})

	result, err := vm.Run(binaryPath, "arg1", "arg2", "test argument")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	assert.Equal(t, []string{"arg1", "arg2", "test argument"}, lines)
}

// TestQEMUVM_Aarch64_Stderr tests stderr output on aarch64.
func TestQEMUVM_Aarch64_Stderr(t *testing.T) {
	cfg := findArchConfig("aarch64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("aarch64 toolchain not available")
	}

	runStderrTest(t, *cfg)
}

// runStderrTest tests stderr capture for a given architecture.
func runStderrTest(t *testing.T, cfg TestArchConfig) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vm_stderr_%s_", cfg.Name))
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "stderr_test")
	sourceCode := `
#include <stdio.h>
int main() {
    fprintf(stdout, "stdout message\n");
    fprintf(stderr, "stderr message\n");
    return 0;
}
`

	err = compileCrossProgram(cfg, sourceCode, binaryPath)
	require.NoError(t, err)

	vm := NewQEMUVM(QEMUConfig{
		QEMUPath: cfg.QEMUBinary,
		Sysroot:  cfg.Sysroot,
	})

	result, err := vm.Run(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "stdout message")
	assert.Contains(t, result.Stderr, "stderr message")
}

// TestQEMUVM_Aarch64_Timeout tests timeout functionality on aarch64.
func TestQEMUVM_Aarch64_Timeout(t *testing.T) {
	cfg := findArchConfig("aarch64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("aarch64 toolchain not available")
	}

	runTimeoutTest(t, *cfg)
}

// runTimeoutTest tests timeout functionality for a given architecture.
func runTimeoutTest(t *testing.T, cfg TestArchConfig) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vm_timeout_%s_", cfg.Name))
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "infinite_loop")
	sourceCode := `
int main() {
    while(1) {}
    return 0;
}
`

	err = compileCrossProgram(cfg, sourceCode, binaryPath)
	require.NoError(t, err)

	vm := NewQEMUVM(QEMUConfig{
		QEMUPath: cfg.QEMUBinary,
		Sysroot:  cfg.Sysroot,
	})

	result, err := vm.RunWithTimeout(binaryPath, 2)
	require.NoError(t, err)

	// timeout command returns 124 when the command times out
	assert.Equal(t, 124, result.ExitCode, "Expected timeout exit code 124")
}

// TestQEMUVM_Aarch64_StackCanary tests stack canary detection on aarch64.
func TestQEMUVM_Aarch64_StackCanary(t *testing.T) {
	cfg := findArchConfig("aarch64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("aarch64 toolchain not available")
	}

	runStackCanaryTest(t, *cfg)
}

// TestQEMUVM_Riscv64_StackCanary tests stack canary detection on riscv64.
func TestQEMUVM_Riscv64_StackCanary(t *testing.T) {
	cfg := findArchConfig("riscv64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("riscv64 toolchain not available")
	}

	runStackCanaryTest(t, *cfg)
}

// runStackCanaryTest tests stack smashing detection for a given architecture.
func runStackCanaryTest(t *testing.T, cfg TestArchConfig) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vm_canary_%s_", cfg.Name))
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test 1: Program with stack protection that doesn't overflow (should succeed)
	t.Run("no_overflow", func(t *testing.T) {
		binaryPath := filepath.Join(tempDir, "no_overflow")
		sourceCode := `
#include <stdio.h>
#include <string.h>

void safe_function() {
    char buffer[32];
    strcpy(buffer, "safe");
    printf("Buffer: %s\n", buffer);
}

int main() {
    safe_function();
    return 0;
}
`
		// Compile with stack protection
		oldCC := cfg.ExtraCC
		cfg.ExtraCC = append(cfg.ExtraCC, "-fstack-protector-strong")
		defer func() { cfg.ExtraCC = oldCC }()

		err := compileCrossProgram(cfg, sourceCode, binaryPath)
		require.NoError(t, err)

		vm := NewQEMUVM(QEMUConfig{
			QEMUPath: cfg.QEMUBinary,
			Sysroot:  cfg.Sysroot,
		})

		result, err := vm.Run(binaryPath)
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode)
		assert.Contains(t, result.Stdout, "Buffer: safe")
	})
}

// TestQEMUVM_Aarch64_LargeOutput tests handling of large output on aarch64.
func TestQEMUVM_Aarch64_LargeOutput(t *testing.T) {
	cfg := findArchConfig("aarch64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("aarch64 toolchain not available")
	}

	runLargeOutputTest(t, *cfg)
}

// runLargeOutputTest tests handling of large stdout output.
func runLargeOutputTest(t *testing.T, cfg TestArchConfig) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vm_large_%s_", cfg.Name))
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "large_output")
	sourceCode := `
#include <stdio.h>
int main() {
    for (int i = 0; i < 1000; i++) {
        printf("Line %04d: This is a test output line to generate larger output.\n", i);
    }
    return 0;
}
`

	err = compileCrossProgram(cfg, sourceCode, binaryPath)
	require.NoError(t, err)

	vm := NewQEMUVM(QEMUConfig{
		QEMUPath: cfg.QEMUBinary,
		Sysroot:  cfg.Sysroot,
	})

	result, err := vm.Run(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	assert.Equal(t, 1000, len(lines), "Expected 1000 lines of output")
	assert.Contains(t, lines[0], "Line 0000")
	assert.Contains(t, lines[999], "Line 0999")
}

// TestQEMUVM_AllArchitectures_HelloWorld tests hello world on all available architectures.
func TestQEMUVM_AllArchitectures_HelloWorld(t *testing.T) {
	for _, cfg := range architectures {
		t.Run(cfg.Name, func(t *testing.T) {
			if !checkToolchainAvailable(t, cfg) {
				t.Skipf("Toolchain for %s not available", cfg.Name)
			}
			runHelloWorldTest(t, cfg)
		})
	}
}

// TestQEMUVM_AllArchitectures_ExitCodes tests exit codes on all available architectures.
func TestQEMUVM_AllArchitectures_ExitCodes(t *testing.T) {
	for _, cfg := range architectures {
		t.Run(cfg.Name, func(t *testing.T) {
			if !checkToolchainAvailable(t, cfg) {
				t.Skipf("Toolchain for %s not available", cfg.Name)
			}
			runExitCodeTest(t, cfg)
		})
	}
}

// findArchConfig returns the configuration for a given architecture name.
func findArchConfig(name string) *TestArchConfig {
	for _, cfg := range architectures {
		if cfg.Name == name {
			return &cfg
		}
	}
	return nil
}

// TestQEMUVM_Aarch64_MemoryAllocations tests memory allocation on aarch64.
func TestQEMUVM_Aarch64_MemoryAllocations(t *testing.T) {
	cfg := findArchConfig("aarch64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("aarch64 toolchain not available")
	}

	tempDir, err := os.MkdirTemp("", "vm_mem_aarch64_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "mem_test")
	sourceCode := `
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main() {
    // Allocate and use memory
    char *buf = malloc(1024);
    if (buf == NULL) {
        fprintf(stderr, "malloc failed\n");
        return 1;
    }
    
    memset(buf, 'A', 1023);
    buf[1023] = '\0';
    
    printf("Allocated %zu bytes\n", strlen(buf));
    
    free(buf);
    return 0;
}
`

	err = compileCrossProgram(*cfg, sourceCode, binaryPath)
	require.NoError(t, err)

	vm := NewQEMUVM(QEMUConfig{
		QEMUPath: cfg.QEMUBinary,
		Sysroot:  cfg.Sysroot,
	})

	result, err := vm.Run(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "Allocated 1023 bytes")
}

// TestQEMUVM_Riscv64_Arithmetic tests arithmetic operations on riscv64.
func TestQEMUVM_Riscv64_Arithmetic(t *testing.T) {
	cfg := findArchConfig("riscv64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("riscv64 toolchain not available")
	}

	tempDir, err := os.MkdirTemp("", "vm_arith_riscv64_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "arith_test")
	sourceCode := `
#include <stdio.h>
#include <stdint.h>

int main() {
    int64_t a = 1234567890123456789LL;
    int64_t b = 987654321098765432LL;
    int64_t sum = a + b;
    int64_t diff = a - b;
    
    printf("a = %ld\n", a);
    printf("b = %ld\n", b);
    printf("sum = %ld\n", sum);
    printf("diff = %ld\n", diff);
    
    return 0;
}
`

	err = compileCrossProgram(*cfg, sourceCode, binaryPath)
	require.NoError(t, err)

	vm := NewQEMUVM(QEMUConfig{
		QEMUPath: cfg.QEMUBinary,
		Sysroot:  cfg.Sysroot,
	})

	result, err := vm.Run(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "a = 1234567890123456789")
	assert.Contains(t, result.Stdout, "b = 987654321098765432")
}

// TestQEMUVM_Arm_Fibonacci tests recursive computation on ARM 32-bit.
func TestQEMUVM_Arm_Fibonacci(t *testing.T) {
	cfg := findArchConfig("arm")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("arm toolchain not available")
	}

	tempDir, err := os.MkdirTemp("", "vm_fib_arm_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "fib_test")
	sourceCode := `
#include <stdio.h>

int fibonacci(int n) {
    if (n <= 1) return n;
    return fibonacci(n-1) + fibonacci(n-2);
}

int main() {
    printf("fib(10) = %d\n", fibonacci(10));
    printf("fib(20) = %d\n", fibonacci(20));
    return 0;
}
`

	err = compileCrossProgram(*cfg, sourceCode, binaryPath)
	require.NoError(t, err)

	vm := NewQEMUVM(QEMUConfig{
		QEMUPath: cfg.QEMUBinary,
		Sysroot:  cfg.Sysroot,
	})

	result, err := vm.Run(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "fib(10) = 55")
	assert.Contains(t, result.Stdout, "fib(20) = 6765")
}

// TestQEMUVM_Ppc64_ByteOrder tests byte ordering on PowerPC 64 (big-endian).
func TestQEMUVM_Ppc64_ByteOrder(t *testing.T) {
	cfg := findArchConfig("ppc64")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("ppc64 toolchain not available")
	}

	tempDir, err := os.MkdirTemp("", "vm_endian_ppc64_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "endian_test")
	sourceCode := `
#include <stdio.h>
#include <stdint.h>

int main() {
    uint32_t value = 0x12345678;
    unsigned char *bytes = (unsigned char *)&value;
    
    printf("Value: 0x%08X\n", value);
    printf("Byte 0: 0x%02X\n", bytes[0]);
    printf("Byte 1: 0x%02X\n", bytes[1]);
    printf("Byte 2: 0x%02X\n", bytes[2]);
    printf("Byte 3: 0x%02X\n", bytes[3]);
    
    #if __BYTE_ORDER__ == __ORDER_BIG_ENDIAN__
    printf("Endianness: Big\n");
    #else
    printf("Endianness: Little\n");
    #endif
    
    return 0;
}
`

	err = compileCrossProgram(*cfg, sourceCode, binaryPath)
	require.NoError(t, err)

	vm := NewQEMUVM(QEMUConfig{
		QEMUPath: cfg.QEMUBinary,
		Sysroot:  cfg.Sysroot,
	})

	result, err := vm.Run(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "Value: 0x12345678")
	// PowerPC 64 is big-endian by default
	assert.Contains(t, result.Stdout, "Endianness: Big")
}

// TestQEMUVM_S390x_CharacterSet tests character handling on IBM Z.
func TestQEMUVM_S390x_CharacterSet(t *testing.T) {
	cfg := findArchConfig("s390x")
	if cfg == nil || !checkToolchainAvailable(t, *cfg) {
		t.Skip("s390x toolchain not available")
	}

	tempDir, err := os.MkdirTemp("", "vm_char_s390x_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "char_test")
	sourceCode := `
#include <stdio.h>
#include <ctype.h>

int main() {
    const char *test = "Hello, World! 123";
    int letters = 0, digits = 0, spaces = 0;
    
    for (const char *p = test; *p; p++) {
        if (isalpha(*p)) letters++;
        else if (isdigit(*p)) digits++;
        else if (isspace(*p)) spaces++;
    }
    
    printf("String: %s\n", test);
    printf("Letters: %d\n", letters);
    printf("Digits: %d\n", digits);
    printf("Spaces: %d\n", spaces);
    
    return 0;
}
`

	err = compileCrossProgram(*cfg, sourceCode, binaryPath)
	require.NoError(t, err)

	vm := NewQEMUVM(QEMUConfig{
		QEMUPath: cfg.QEMUBinary,
		Sysroot:  cfg.Sysroot,
	})

	result, err := vm.Run(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "Letters: 10") // HelloWorld
	assert.Contains(t, result.Stdout, "Digits: 3")   // 123
	assert.Contains(t, result.Stdout, "Spaces: 2")   // two spaces
}
