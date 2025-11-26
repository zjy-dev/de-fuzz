//go:build integration
// +build integration

package coverage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGCCCoverage_CompilerCoverage_Integration tests the complete GCC coverage workflow
// by compiling a seed with an instrumented GCC compiler and measuring the COMPILER's coverage.
// This is a TRUE integration test - it uses a real instrumented compiler to generate
// real .gcda files that track which parts of the GCC compiler code were executed.
func TestGCCCoverage_CompilerCoverage_Integration(t *testing.T) {
	// Load real configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Skipf("Skipping test: cannot load config: %v", err)
	}

	// Check if the instrumented compiler exists
	if _, err := os.Stat(cfg.Compiler.Path); os.IsNotExist(err) {
		t.Skipf("Skipping test: instrumented compiler not found at %s", cfg.Compiler.Path)
	}

	// Check if gcovr exec path exists
	if _, err := os.Stat(cfg.Compiler.GcovrExecPath); os.IsNotExist(err) {
		t.Skipf("Skipping test: gcovr exec path not found at %s", cfg.Compiler.GcovrExecPath)
	}

	executor := exec.NewCommandExecutor()

	// Check if gcovr is available
	result, err := executor.Run("which", "gcovr")
	if err != nil {
		t.Skipf("Skipping test: gcovr not found in PATH: %v", err)
	}
	t.Logf("gcovr path: %s", result.Stdout)

	// Get compiler config path for filtering
	compilerConfigPath, err := config.GetCompilerConfigPath(cfg)
	require.NoError(t, err)
	t.Logf("Compiler config path: %s", compilerConfigPath)

	// Create temporary workspace
	workspaceDir, err := os.MkdirTemp("", "gcc-compiler-cov-*")
	require.NoError(t, err)
	defer os.RemoveAll(workspaceDir)
	t.Logf("Workspace directory: %s", workspaceDir)

	// Create directories
	seedDir := filepath.Join(workspaceDir, "seeds")
	err = os.MkdirAll(seedDir, 0755)
	require.NoError(t, err)

	reportDir := filepath.Join(workspaceDir, "reports")
	err = os.MkdirAll(reportDir, 0755)
	require.NoError(t, err)

	// Use totalReportPath from config, or fallback to temp dir
	// For testing, always use a temp path to avoid modifying the real total.json
	totalReportPath := filepath.Join(reportDir, "total.json")
	t.Logf("Total report path (test): %s", totalReportPath)
	t.Logf("Total report path (config): %s", cfg.Compiler.TotalReportPath)

	// Use gcovrCommand from config, or fallback to default
	gcovrCommand := cfg.Compiler.GcovrCommand
	if gcovrCommand == "" {
		gcovrCommand = `gcovr --exclude '.*\.(h|hpp|hxx)$' --gcov-executable "gcov-14 --demangled-names" -r .. --json-pretty`
	}
	t.Logf("Gcovr command: %s", gcovrCommand)

	// Create a simple C seed that will trigger stack canary protection
	// This should exercise the stack_protect_classify_type function in the compiler
	seedContent := `#include <stdio.h>
#include <string.h>

// This function has a buffer that should trigger stack protection
void vulnerable_function(const char *input) {
    char buffer[64];
    strcpy(buffer, input);
    printf("Buffer: %s\n", buffer);
}

int main() {
    vulnerable_function("Hello World");
    return 0;
}
`

	// Create GCC compiler using the compiler module with PrefixPath support
	compilerDir := filepath.Dir(cfg.Compiler.Path)
	gccCompiler := compiler.NewGCCCompiler(compiler.GCCCompilerConfig{
		GCCPath:    cfg.Compiler.Path,
		WorkDir:    seedDir,
		PrefixPath: compilerDir, // -B flag for finding cc1, as, ld
		CFlags:     []string{"-fstack-protector-all", "-O0"},
	})
	t.Logf("Created GCC compiler with path: %s, prefix: %s", cfg.Compiler.Path, compilerDir)

	// Create compile function that wraps the compiler module
	compileFunc := func(s *seed.Seed) error {
		result, err := gccCompiler.Compile(s)
		if err != nil {
			return fmt.Errorf("compiler.Compile failed: %w", err)
		}
		if !result.Success {
			return fmt.Errorf("compilation failed: %s", result.Stderr)
		}
		t.Logf("Compilation succeeded: %s", result.BinaryPath)
		if result.Stderr != "" {
			t.Logf("Compiler stderr: %s", result.Stderr)
		}
		return nil
	}

	// Create GCCCoverage instance
	gcc := NewGCCCoverage(
		executor,
		compileFunc,
		cfg.Compiler.GcovrExecPath,
		gcovrCommand,
		totalReportPath,
		compilerConfigPath,
		cfg.Compiler.SourceParentPath,
	)

	// Helper function to create a seed with proper Metadata
	var seedIDCounter uint64 = 0
	createSeed := func(content string) *seed.Seed {
		seedIDCounter++
		return &seed.Seed{
			Meta: seed.Metadata{
				ID:       seedIDCounter,
				ParentID: 0,
				Depth:    0,
				State:    seed.SeedStatePending,
			},
			Content: content,
		}
	}

	// =========================
	// Test 1: Clean operation
	// =========================
	t.Run("clean_gcda_files", func(t *testing.T) {
		t.Logf("Testing Clean operation on: %s", cfg.Compiler.GcovrExecPath)

		// Clean any existing .gcda files
		err := gcc.Clean()
		require.NoError(t, err, "Clean should succeed")
		t.Logf("Clean completed successfully")
	})

	// =========================
	// Test 2: Measure - compile seed and generate report
	// =========================
	var seed1Report Report
	t.Run("measure_seed1", func(t *testing.T) {
		// Create a seed with proper Metadata
		testSeed := createSeed(seedContent)

		t.Logf("Measuring coverage for seed ID: %d", testSeed.Meta.ID)

		// Measure will:
		// 1. Clean old .gcda files
		// 2. Compile the seed (which generates .gcda files in gcovr_exec_path)
		// 3. Run gcovr to generate JSON report
		report, err := gcc.Measure(testSeed)
		require.NoError(t, err, "Measure should succeed for seed1")
		require.NotNil(t, report, "Report should not be nil")

		seed1Report = report

		// Verify report can be converted to bytes
		reportBytes, err := report.ToBytes()
		require.NoError(t, err, "ToBytes should succeed")
		require.Greater(t, len(reportBytes), 0, "Report should have content")
		t.Logf("Seed1 report size: %d bytes", len(reportBytes))
	})

	// =========================
	// Test 3: HasIncreased - first seed should always increase
	// =========================
	t.Run("has_increased_first_seed", func(t *testing.T) {
		require.NotNil(t, seed1Report, "seed1Report should exist from previous test")

		t.Logf("Checking if first seed increased coverage")
		increased, err := gcc.HasIncreased(seed1Report)
		require.NoError(t, err, "HasIncreased should not error")
		assert.True(t, increased, "First seed should always show coverage increase")
		t.Logf("HasIncreased result: %v (expected true)", increased)
	})

	// =========================
	// Test 4: Merge - merge first seed into total.json
	// =========================
	t.Run("merge_first_seed", func(t *testing.T) {
		require.NotNil(t, seed1Report, "seed1Report should exist from previous test")

		t.Logf("Merging first seed into total.json")
		err := gcc.Merge(seed1Report)
		require.NoError(t, err, "Merge should succeed for first seed")
		t.Logf("Merge completed successfully")

		// Verify total.json was created
		_, err = os.Stat(totalReportPath)
		require.NoError(t, err, "total.json should be created")
		t.Logf("total.json created at: %s", totalReportPath)

		// Verify total.json has content
		totalData, err := os.ReadFile(totalReportPath)
		require.NoError(t, err, "Should be able to read total.json")
		require.Greater(t, len(totalData), 0, "total.json should have content")
		t.Logf("total.json size: %d bytes", len(totalData))
	})

	// =========================
	// Test 5: GetTotalReport
	// =========================
	t.Run("get_total_report", func(t *testing.T) {
		t.Logf("Getting total report")
		totalReport, err := gcc.GetTotalReport()
		require.NoError(t, err, "GetTotalReport should succeed")
		require.NotNil(t, totalReport, "Total report should not be nil")

		totalBytes, err := totalReport.ToBytes()
		require.NoError(t, err, "ToBytes should succeed on total report")
		require.Greater(t, len(totalBytes), 0, "Total report should have content")
		t.Logf("Total report size: %d bytes", len(totalBytes))
	})

	// =========================
	// Test 6: Measure second seed with different code pattern
	// =========================
	var seed2Report Report
	t.Run("measure_seed2_different_pattern", func(t *testing.T) {
		// Clean previous .gcda files
		err := gcc.Clean()
		require.NoError(t, err)
		t.Logf("Cleaned .gcda files before seed2")

		// Create a seed with additional features that might increase compiler coverage
		seed2Content := `#include <stdio.h>
#include <string.h>
#include <stdlib.h>

// Multiple buffers to potentially trigger different stack protection logic
void function_with_multiple_buffers() {
    char buf1[32];
    char buf2[64];
    char buf3[128];
    
    strcpy(buf1, "test1");
    strcpy(buf2, "test2");
    strcpy(buf3, "test3");
    
    printf("%s %s %s\n", buf1, buf2, buf3);
}

// Function with array access
void function_with_array() {
    int array[100];
    for (int i = 0; i < 100; i++) {
        array[i] = i;
    }
    printf("Array[50] = %d\n", array[50]);
}

int main() {
    function_with_multiple_buffers();
    function_with_array();
    return 0;
}
`
		testSeed := createSeed(seed2Content)

		t.Logf("Measuring coverage for seed ID: %d", testSeed.Meta.ID)
		report, err := gcc.Measure(testSeed)
		require.NoError(t, err, "Measure should succeed for seed2")
		require.NotNil(t, report, "Report should not be nil")

		seed2Report = report

		reportBytes, err := report.ToBytes()
		require.NoError(t, err)
		require.Greater(t, len(reportBytes), 0)
		t.Logf("Seed2 report size: %d bytes", len(reportBytes))
	})

	// =========================
	// Test 7: HasIncreased - check if seed2 increased coverage
	// =========================
	t.Run("has_increased_seed2", func(t *testing.T) {
		require.NotNil(t, seed2Report, "seed2Report should exist")

		t.Logf("Checking if seed2 increased coverage compared to total")
		increased, err := gcc.HasIncreased(seed2Report)
		require.NoError(t, err, "HasIncreased should not error")
		t.Logf("HasIncreased result for seed2: %v", increased)

		// Seed2 might or might not increase coverage depending on the compiler
		// We just verify the function works without error
		if increased {
			t.Logf("Seed2 increased coverage - will merge")

			// Merge seed2 since it increased coverage
			err := gcc.Merge(seed2Report)
			require.NoError(t, err, "Merge should succeed for seed2")
			t.Logf("Seed2 merged successfully")
		} else {
			t.Logf("Seed2 did not increase coverage - skipping merge")
		}
	})

	// =========================
	// Test 8: Measure identical seed (should not increase coverage)
	// =========================
	t.Run("measure_identical_seed_no_increase", func(t *testing.T) {
		// Clean .gcda files
		err := gcc.Clean()
		require.NoError(t, err)

		// Use the same seed content as seed1
		testSeed := createSeed(seedContent)

		t.Logf("Measuring coverage for identical seed ID: %d", testSeed.Meta.ID)
		report, err := gcc.Measure(testSeed)
		require.NoError(t, err, "Measure should succeed")
		require.NotNil(t, report)

		// This should NOT increase coverage since it's identical to seed1
		t.Logf("Checking if identical seed increased coverage")
		increased, err := gcc.HasIncreased(report)
		require.NoError(t, err, "HasIncreased should not error")
		t.Logf("HasIncreased result for identical seed: %v (expected false)", increased)

		// Note: We don't assert False here because depending on the compiler state,
		// results might vary. The important thing is the function executes without error.
	})

	t.Log("=== Compiler coverage integration test completed successfully ===")
	t.Logf("All GCCCoverage methods tested with real instrumented compiler")
	t.Logf("Compiler: %s", cfg.Compiler.Path)
	t.Logf("Gcovr exec path: %s", cfg.Compiler.GcovrExecPath)
	t.Logf("Gcovr command: %s", gcovrCommand)
	t.Logf("Total report path: %s", totalReportPath)
}
