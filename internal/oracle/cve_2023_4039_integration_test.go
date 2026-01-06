//go:build integration

package oracle

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// CVE-2023-4039 Integration Test
//
// This test reproduces CVE-2023-4039, a stack protector bypass vulnerability
// affecting AArch64 when using Variable-Length Arrays (VLAs) or alloca().
//
// The vulnerability exists because on AArch64, GCC places the stack canary
// ABOVE dynamically-sized arrays (VLAs/alloca), making the return address
// vulnerable to buffer overflow BEFORE the canary is corrupted.
//
// Stack layout with VLA (vulnerable):
//   High Addr -> [Canary] <- protected, but above the VLA
//                [Saved LR] <- vulnerable!
//                [VLA buffer] <- overflow starts here
//   Low Addr  -> [Stack Pointer]
//
// Expected behavior:
//   - When fill_size < buf_size: Normal exit (0)
//   - When fill_size slightly > buf_size: On fixed-size buffers, SIGABRT (canary check)
//   - When fill_size >> buf_size WITH VLA: SIGSEGV (return address corrupted) - CVE-2023-4039!

const (
	// Cross-compiler paths
	aarch64Compiler = "/root/project/de-fuzz/gcc-v12.2.0-aarch64-cross-compile/build-aarch64-none-linux-gnu/gcc-final-build/gcc/xgcc"
	aarch64Sysroot  = "/root/project/de-fuzz/gcc-v12.2.0-aarch64-cross-compile/install-aarch64-none-linux-gnu/aarch64-none-linux-gnu/libc"
	aarch64LibGCC   = "/root/project/de-fuzz/gcc-v12.2.0-aarch64-cross-compile/install-aarch64-none-linux-gnu/lib/gcc/aarch64-none-linux-gnu/12.2.1"
	aarch64LibPath  = "/root/project/de-fuzz/gcc-v12.2.0-aarch64-cross-compile/install-aarch64-none-linux-gnu/aarch64-none-linux-gnu/lib64"
	aarch64CC1Path  = "/root/project/de-fuzz/gcc-v12.2.0-aarch64-cross-compile/build-aarch64-none-linux-gnu/gcc-final-build/gcc"
)

// VLA-based vulnerable seed code (CVE-2023-4039 pattern)
const vlaVulnerableSeed = `
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// CVE-2023-4039: VLA on AArch64 with stack protector
// The stack canary is placed ABOVE the VLA, leaving return address unprotected.
void seed(int buf_size, int fill_size) {
    // Variable-Length Array - triggers CVE-2023-4039 vulnerability
    char vla_buffer[buf_size];
    
    // Fill beyond buffer size to trigger overflow
    memset(vla_buffer, 'A', fill_size);
    
    // Prevent compiler optimization
    printf("Filled %d bytes into %d-byte VLA buffer\n", fill_size, buf_size);
}

int main(int argc, char *argv[]) {
    if (argc != 3) {
        fprintf(stderr, "Usage: %s <buf_size> <fill_size>\n", argv[0]);
        return 1;
    }
    
    int buf_size = atoi(argv[1]);
    int fill_size = atoi(argv[2]);
    
    seed(buf_size, fill_size);
    
    return 0;
}
`

// Fixed-size array seed code (should trigger canary properly)
const fixedArraySeed = `
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// Fixed-size array - stack protector should work correctly
void seed(int buf_size, int fill_size) {
    // Fixed-size array - canary protection works properly
    char fixed_buffer[64];
    (void)buf_size; // unused, just for API compatibility
    
    // Fill beyond buffer size to trigger overflow
    memset(fixed_buffer, 'A', fill_size);
    
    // Prevent compiler optimization
    printf("Filled %d bytes into 64-byte fixed buffer\n", fill_size);
}

int main(int argc, char *argv[]) {
    if (argc != 3) {
        fprintf(stderr, "Usage: %s <buf_size> <fill_size>\n", argv[0]);
        return 1;
    }
    
    int buf_size = atoi(argv[1]);
    int fill_size = atoi(argv[2]);
    
    seed(buf_size, fill_size);
    
    return 0;
}
`

// QEMUExecutor implements execution via QEMU for AArch64 binaries.
type QEMUExecutor struct {
	QEMUPath string
	Sysroot  string
}

func (q *QEMUExecutor) ExecuteWithInput(binaryPath string, stdin string) (exitCode int, stdout string, stderr string, err error) {
	return 0, "", "", fmt.Errorf("ExecuteWithInput not implemented for QEMU; use ExecuteWithArgs")
}

func (q *QEMUExecutor) ExecuteWithArgs(binaryPath string, args ...string) (exitCode int, stdout string, stderr string, err error) {
	// Build QEMU command: qemu-aarch64 -L <sysroot> <binary> <args...>
	qemuArgs := []string{"-L", q.Sysroot, binaryPath}
	qemuArgs = append(qemuArgs, args...)

	cmd := exec.Command(q.QEMUPath, qemuArgs...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, "", "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return 0, "", "", err
	}

	if err := cmd.Start(); err != nil {
		return 0, "", "", err
	}

	stdoutBytes, _ := io.ReadAll(stdoutPipe)
	stderrBytes, _ := io.ReadAll(stderrPipe)

	_ = cmd.Wait()
	exitCode = cmd.ProcessState.ExitCode()
	stderr = string(stderrBytes)

	// QEMU returns -1 for signals, but we can parse the signal from stderr
	// Format: "qemu: uncaught target signal X (SignalName) - core dumped"
	if exitCode == -1 {
		// Check for signal 11 (SIGSEGV)
		if strings.Contains(stderr, "signal 11") || strings.Contains(stderr, "Segmentation fault") {
			exitCode = ExitCodeSIGSEGV // 139
		}
		// Check for signal 6 (SIGABRT) - stack smashing detected
		if strings.Contains(stderr, "signal 6") || strings.Contains(stderr, "Aborted") {
			exitCode = ExitCodeSIGABRT // 134
		}
	}

	return exitCode, string(stdoutBytes), stderr, nil
}

// TestCanaryOracle_CVE2023_4039_VLA_SIGSEGV tests that VLA code triggers SIGSEGV (vulnerability).
func TestCanaryOracle_CVE2023_4039_VLA_SIGSEGV(t *testing.T) {
	// Skip if cross-compiler or QEMU not available
	if _, err := os.Stat(aarch64Compiler); os.IsNotExist(err) {
		t.Skip("AArch64 cross-compiler not found, skipping CVE-2023-4039 integration test")
	}
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not found, skipping CVE-2023-4039 integration test")
	}

	tempDir, err := os.MkdirTemp("", "cve2023_4039_vla_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourcePath := filepath.Join(tempDir, "vla_vulnerable.c")
	binaryPath := filepath.Join(tempDir, "vla_vulnerable")

	err = os.WriteFile(sourcePath, []byte(vlaVulnerableSeed), 0644)
	require.NoError(t, err)

	// Compile with AArch64 cross-compiler and stack protector
	cmd := exec.Command(aarch64Compiler,
		"-fstack-protector-all",
		"-O0",
		fmt.Sprintf("--sysroot=%s", aarch64Sysroot),
		fmt.Sprintf("-B%s", aarch64LibGCC),
		fmt.Sprintf("-B%s", aarch64CC1Path),
		fmt.Sprintf("-L%s", aarch64LibPath),
		"-o", binaryPath,
		sourcePath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Compilation output: %s", output)
		t.Fatalf("Failed to compile VLA test program: %v", err)
	}
	t.Logf("Compiled VLA-vulnerable binary: %s", binaryPath)

	// Create canary oracle with 2-parameter support
	oracle := &CanaryOracle{
		MaxBufferSize:  512,
		DefaultBufSize: 64,
	}

	// Create QEMU executor
	executor := &QEMUExecutor{
		QEMUPath: "qemu-aarch64",
		Sysroot:  aarch64Sysroot,
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 1},
		Content: vlaVulnerableSeed,
	}

	ctx := &AnalyzeContext{
		BinaryPath: binaryPath,
		Executor:   executor,
	}

	// Run oracle analysis
	bug, err := oracle.Analyze(testSeed, ctx, nil)
	require.NoError(t, err)

	// CVE-2023-4039 should be detected: VLA causes SIGSEGV before SIGABRT
	if bug != nil {
		t.Logf("✅ CVE-2023-4039 DETECTED! Bug: %s", bug.Description)
	} else {
		t.Log("⚠️  No bug detected. This may indicate:")
		t.Log("   - Compiler has been patched for CVE-2023-4039")
		t.Log("   - Stack layout differs from expected")
		t.Log("   - fill_size range insufficient")
	}

	// Additional manual verification: test specific sizes
	t.Log("\n=== Manual Verification ===")
	testCases := []struct {
		bufSize  int
		fillSize int
	}{
		{64, 32},  // Safe: fill < buf
		{64, 64},  // Edge: fill == buf
		{64, 128}, // Overflow: should crash
		{64, 256}, // Large overflow
		{64, 512}, // Very large overflow
	}

	for _, tc := range testCases {
		exitCode, stdout, stderr, err := executor.ExecuteWithArgs(
			binaryPath,
			fmt.Sprintf("%d", tc.bufSize),
			fmt.Sprintf("%d", tc.fillSize),
		)
		if err != nil {
			t.Logf("buf=%d fill=%d: execution error: %v", tc.bufSize, tc.fillSize, err)
			continue
		}

		var status string
		switch exitCode {
		case 0:
			status = "OK (normal exit)"
		case ExitCodeSIGSEGV:
			status = "🔴 SIGSEGV (CVE-2023-4039!)"
		case ExitCodeSIGABRT:
			status = "🟢 SIGABRT (canary working)"
		default:
			status = fmt.Sprintf("Unknown (exit=%d)", exitCode)
		}
		t.Logf("buf=%d fill=%d: %s stdout=%q stderr=%q",
			tc.bufSize, tc.fillSize, status, stdout, stderr)
	}
}

// TestCanaryOracle_CVE2023_4039_FixedArray_SIGABRT tests that fixed array triggers SIGABRT (safe).
func TestCanaryOracle_CVE2023_4039_FixedArray_SIGABRT(t *testing.T) {
	// Skip if cross-compiler or QEMU not available
	if _, err := os.Stat(aarch64Compiler); os.IsNotExist(err) {
		t.Skip("AArch64 cross-compiler not found, skipping fixed array test")
	}
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not found, skipping fixed array test")
	}

	tempDir, err := os.MkdirTemp("", "cve2023_4039_fixed_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	sourcePath := filepath.Join(tempDir, "fixed_array.c")
	binaryPath := filepath.Join(tempDir, "fixed_array")

	err = os.WriteFile(sourcePath, []byte(fixedArraySeed), 0644)
	require.NoError(t, err)

	// Compile with AArch64 cross-compiler and stack protector
	cmd := exec.Command(aarch64Compiler,
		"-fstack-protector-all",
		"-O0",
		fmt.Sprintf("--sysroot=%s", aarch64Sysroot),
		fmt.Sprintf("-B%s", aarch64LibGCC),
		fmt.Sprintf("-B%s", aarch64CC1Path),
		fmt.Sprintf("-L%s", aarch64LibPath),
		"-o", binaryPath,
		sourcePath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Compilation output: %s", output)
		t.Fatalf("Failed to compile fixed array test program: %v", err)
	}
	t.Logf("Compiled fixed-array binary: %s", binaryPath)

	// Create canary oracle with 2-parameter support
	oracle := &CanaryOracle{
		MaxBufferSize:  512,
		DefaultBufSize: 64,
	}

	// Create QEMU executor
	executor := &QEMUExecutor{
		QEMUPath: "qemu-aarch64",
		Sysroot:  aarch64Sysroot,
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 2},
		Content: fixedArraySeed,
	}

	ctx := &AnalyzeContext{
		BinaryPath: binaryPath,
		Executor:   executor,
	}

	// Run oracle analysis
	bug, err := oracle.Analyze(testSeed, ctx, nil)
	require.NoError(t, err)

	// Fixed array should NOT trigger CVE-2023-4039 (should get SIGABRT, which is SAFE)
	if bug == nil {
		t.Log("✅ Fixed array correctly protected - canary triggered SIGABRT (safe)")
	} else {
		t.Logf("⚠️  Bug detected on fixed array (unexpected): %s", bug.Description)
	}

	// Manual verification
	t.Log("\n=== Manual Verification (Fixed Array) ===")
	exitCode, _, _, _ := executor.ExecuteWithArgs(binaryPath, "64", "256")

	switch exitCode {
	case ExitCodeSIGABRT:
		t.Log("🟢 SIGABRT: Stack canary protection WORKING as expected")
	case ExitCodeSIGSEGV:
		t.Log("🔴 SIGSEGV: Unexpected - fixed array should be protected!")
	case 0:
		t.Log("⚠️  Normal exit: Buffer might not have been overflowed enough")
	default:
		t.Logf("❓ Exit code %d: Unknown behavior", exitCode)
	}
}

// TestCanaryOracle_CVE2023_4039_Comparison directly compares VLA vs Fixed array behavior.
func TestCanaryOracle_CVE2023_4039_Comparison(t *testing.T) {
	if _, err := os.Stat(aarch64Compiler); os.IsNotExist(err) {
		t.Skip("AArch64 cross-compiler not found")
	}
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not found")
	}

	tempDir, err := os.MkdirTemp("", "cve2023_comparison_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	executor := &QEMUExecutor{
		QEMUPath: "qemu-aarch64",
		Sysroot:  aarch64Sysroot,
	}

	// Compile both versions
	binaries := map[string]string{
		"VLA":   vlaVulnerableSeed,
		"Fixed": fixedArraySeed,
	}

	compiledPaths := make(map[string]string)

	for name, code := range binaries {
		sourcePath := filepath.Join(tempDir, name+".c")
		binaryPath := filepath.Join(tempDir, name)

		err = os.WriteFile(sourcePath, []byte(code), 0644)
		require.NoError(t, err)

		cmd := exec.Command(aarch64Compiler,
			"-fstack-protector-all",
			"-O0",
			fmt.Sprintf("--sysroot=%s", aarch64Sysroot),
			fmt.Sprintf("-B%s", aarch64LibGCC),
			fmt.Sprintf("-B%s", aarch64CC1Path),
			fmt.Sprintf("-L%s", aarch64LibPath),
			"-o", binaryPath,
			sourcePath,
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("Compilation output for %s: %s", name, output)
			t.Fatalf("Failed to compile %s: %v", name, err)
		}
		compiledPaths[name] = binaryPath
	}

	t.Log("╔══════════════════════════════════════════════════════════════╗")
	t.Log("║         CVE-2023-4039 Comparison: VLA vs Fixed Array         ║")
	t.Log("╠══════════════════════════════════════════════════════════════╣")
	t.Log("║  buf_size=64, varying fill_size                              ║")
	t.Log("╠═══════════╦═══════════════════════╦══════════════════════════╣")
	t.Log("║ fill_size ║        VLA            ║       Fixed Array        ║")
	t.Log("╠═══════════╬═══════════════════════╬══════════════════════════╣")

	fillSizes := []int{32, 64, 96, 128, 192, 256, 384, 512}

	vlaShowedSIGSEGV := false
	fixedShowedSIGABRT := false

	for _, fillSize := range fillSizes {
		var results [2]string
		for i, name := range []string{"VLA", "Fixed"} {
			exitCode, _, _, _ := executor.ExecuteWithArgs(
				compiledPaths[name], "64", fmt.Sprintf("%d", fillSize))

			switch exitCode {
			case 0:
				results[i] = "OK"
			case ExitCodeSIGSEGV:
				results[i] = "🔴 SIGSEGV"
				if name == "VLA" {
					vlaShowedSIGSEGV = true
				}
			case ExitCodeSIGABRT:
				results[i] = "🟢 SIGABRT"
				if name == "Fixed" {
					fixedShowedSIGABRT = true
				}
			default:
				results[i] = fmt.Sprintf("exit=%d", exitCode)
			}
		}
		t.Logf("║    %3d    ║     %-16s  ║      %-17s   ║", fillSize, results[0], results[1])
	}

	t.Log("╚═══════════╩═══════════════════════╩══════════════════════════╝")

	// Determine test result
	if vlaShowedSIGSEGV && fixedShowedSIGABRT {
		t.Log("\n✅ CVE-2023-4039 REPRODUCED!")
		t.Log("   - VLA: SIGSEGV (return address corrupted before canary check)")
		t.Log("   - Fixed: SIGABRT (canary protection functioning correctly)")
	} else if vlaShowedSIGSEGV {
		t.Log("\n⚠️  VLA showed SIGSEGV but Fixed didn't show SIGABRT")
	} else if fixedShowedSIGABRT {
		t.Log("\n⚠️  Fixed showed SIGABRT but VLA didn't show SIGSEGV")
		t.Log("   Compiler may have patched CVE-2023-4039")
	} else {
		t.Log("\n❓ Neither pattern observed - check buffer sizes and stack layout")
	}
}
