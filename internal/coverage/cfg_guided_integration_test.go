//go:build integration
// +build integration

package coverage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getSourceFileFromCFG extracts the actual source file path from CFG data
// The CFG contains full paths like /root/project/de-fuzz/gcc-v12.2.0-x64/gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc
const testSourceFile = "/root/project/de-fuzz/gcc-v12.2.0-x64/gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc"

func TestCFGGuidedAnalyzer_Integration_FullWorkflow(t *testing.T) {
	// Use the real CFG file from GCC build
	cfgPath := "/root/project/de-fuzz/gcc-v12.2.0-x64/gcc-build/gcc/cfgexpand.cc.015t.cfg"

	// Check if CFG file exists
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skipf("CFG file not found at %s, skipping integration test", cfgPath)
	}

	tmpDir, err := os.MkdirTemp("", "cfg-guided-integration-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mappingPath := filepath.Join(tmpDir, "mapping.json")
	sourceDir := "/root/project/de-fuzz/gcc-v12.2.0-x64/gcc"

	// Create analyzer focusing on stack_protect functions
	targetFunctions := []string{
		"stack_protect_classify_type",
		"stack_protect_decl_phase",
	}

	analyzer, err := NewCFGGuidedAnalyzer(cfgPath, targetFunctions, sourceDir, mappingPath)
	require.NoError(t, err)
	require.NotNil(t, analyzer)

	// Test 1: Initial state - no coverage
	t.Run("initial_no_coverage", func(t *testing.T) {
		target := analyzer.SelectTarget()
		require.NotNil(t, target, "Should have at least one uncovered BB")

		t.Logf("First target: %s:BB%d (succs=%d, lines=%v)",
			target.Function, target.BBID, target.SuccessorCount, target.Lines)

		assert.Contains(t, targetFunctions, target.Function)
		assert.Greater(t, target.SuccessorCount, 0, "Target should have successors")
		assert.Greater(t, len(target.Lines), 0, "Target should have line info")
	})

	// Test 2: Record initial coverage
	t.Run("record_initial_coverage", func(t *testing.T) {
		// Simulate covering some lines in stack_protect_classify_type
		// Use full path as it appears in CFG file
		coveredLines := []string{
			testSourceFile + ":1819",
			testSourceFile + ":1820",
			testSourceFile + ":1821",
			testSourceFile + ":1822",
		}

		analyzer.RecordCoverage(1, coveredLines)

		// Check coverage stats
		funcCov := analyzer.GetFunctionCoverage()
		assert.Contains(t, funcCov, "stack_protect_classify_type")

		coverage := funcCov["stack_protect_classify_type"]
		t.Logf("Coverage for stack_protect_classify_type: %d/%d BBs",
			coverage.Covered, coverage.Total)

		assert.Greater(t, coverage.Covered, 0, "Should have some coverage")
		assert.Greater(t, coverage.Total, coverage.Covered, "Should have uncovered BBs")
	})

	// Test 3: Progressive coverage recording
	t.Run("progressive_coverage", func(t *testing.T) {
		// Simulate multiple seeds progressively covering more code
		seeds := []struct {
			id    int64
			lines []string
		}{
			{
				id: 2,
				lines: []string{
					testSourceFile + ":1823",
					testSourceFile + ":1824",
				},
			},
			{
				id: 3,
				lines: []string{
					testSourceFile + ":1830",
					testSourceFile + ":1831",
					testSourceFile + ":1832",
				},
			},
			{
				id: 4,
				lines: []string{
					testSourceFile + ":1840",
					testSourceFile + ":1841",
				},
			},
		}

		for _, seed := range seeds {
			analyzer.RecordCoverage(seed.id, seed.lines)
			t.Logf("Seed %d: recorded lines", seed.id)
		}

		// Check updated coverage
		funcCov := analyzer.GetFunctionCoverage()
		coverage := funcCov["stack_protect_classify_type"]
		t.Logf("Updated coverage: %d/%d BBs (%.1f%%)",
			coverage.Covered, coverage.Total,
			float64(coverage.Covered)/float64(coverage.Total)*100)
	})

	// Test 4: Target selection prioritizes high-branching BBs
	t.Run("target_selection_prioritization", func(t *testing.T) {
		// Select next target
		target := analyzer.SelectTarget()
		if target == nil {
			t.Skip("All BBs already covered")
		}

		t.Logf("Next target: %s:BB%d (succs=%d)",
			target.Function, target.BBID, target.SuccessorCount)

		// Target should have reasonable successor count
		assert.GreaterOrEqual(t, target.SuccessorCount, 1)

		// Should provide context
		assert.NotEmpty(t, target.File)
		assert.NotEmpty(t, target.Lines)
	})

	// Test 5: Persistence and recovery
	t.Run("save_and_load_mapping", func(t *testing.T) {
		// Save current mapping
		err := analyzer.SaveMapping(mappingPath)
		require.NoError(t, err)

		// Verify file exists
		_, err = os.Stat(mappingPath)
		require.NoError(t, err)

		// Create new analyzer with same mapping path
		analyzer2, err := NewCFGGuidedAnalyzer(cfgPath, targetFunctions, sourceDir, mappingPath)
		require.NoError(t, err)

		// Verify coverage is preserved
		funcCov1 := analyzer.GetFunctionCoverage()
		funcCov2 := analyzer2.GetFunctionCoverage()

		assert.Equal(t, len(funcCov1), len(funcCov2))
		for fn, cov1 := range funcCov1 {
			cov2, ok := funcCov2[fn]
			require.True(t, ok)
			assert.Equal(t, cov1.Covered, cov2.Covered)
			assert.Equal(t, cov1.Total, cov2.Total)
		}
	})

	// Test 6: Generate target prompt context
	t.Run("generate_target_context", func(t *testing.T) {
		target := analyzer.SelectTarget()
		if target == nil {
			t.Skip("All BBs covered")
		}

		// Verify target info is complete
		assert.NotEmpty(t, target.Function)
		assert.NotEmpty(t, target.File)
		assert.NotEmpty(t, target.Lines)
		assert.Greater(t, target.BBID, 0)

		// If there's a base seed, verify it
		if target.BaseSeed != "" {
			t.Logf("Base seed: %s (line %d, distance %d)",
				target.BaseSeed, target.BaseSeedLine, target.DistanceFromBase)
		}
	})
}

func TestCFGGuidedAnalyzer_Integration_MultipleFunctions(t *testing.T) {
	cfgPath := "/root/project/de-fuzz/gcc-v12.2.0-x64/gcc-build/gcc/cfgexpand.cc.015t.cfg"

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skipf("CFG file not found at %s", cfgPath)
	}

	tmpDir, err := os.MkdirTemp("", "cfg-multi-func-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mappingPath := filepath.Join(tmpDir, "mapping.json")

	// Test with multiple functions
	targetFunctions := []string{
		"stack_protect_classify_type",
		"stack_protect_decl_phase",
		"stack_protect_decl_phase_1",
		"stack_protect_decl_phase_2",
	}

	analyzer, err := NewCFGGuidedAnalyzer(cfgPath, targetFunctions, "", mappingPath)
	require.NoError(t, err)

	// Get initial coverage for all functions
	funcCov := analyzer.GetFunctionCoverage()
	t.Logf("Analyzing %d functions:", len(funcCov))

	totalBBs := 0
	for fn, cov := range funcCov {
		t.Logf("  %s: 0/%d BBs", fn, cov.Total)
		totalBBs += cov.Total
	}
	t.Logf("Total BBs across all functions: %d", totalBBs)

	// Simulate fuzzing loop: select targets and record coverage
	for i := 0; i < 20; i++ {
		target := analyzer.SelectTarget()
		if target == nil {
			t.Logf("All BBs covered after %d iterations!", i)
			break
		}

		t.Logf("Iteration %d: target %s:BB%d (succs=%d)",
			i+1, target.Function, target.BBID, target.SuccessorCount)

		// Simulate covering the target BB by recording its lines
		if len(target.Lines) > 0 {
			coveredLines := make([]string, len(target.Lines))
			for j, targetLine := range target.Lines {
				coveredLines[j] = fmt.Sprintf("%s:%d", target.File, targetLine)
			}
			analyzer.RecordCoverage(int64(i+1), coveredLines)
		}
	}

	// Report final coverage
	funcCov = analyzer.GetFunctionCoverage()
	t.Log("\nFinal coverage:")
	coveredTotal := 0
	for fn, cov := range funcCov {
		pct := float64(0)
		if cov.Total > 0 {
			pct = float64(cov.Covered) / float64(cov.Total) * 100
		}
		t.Logf("  %s: %d/%d BBs (%.1f%%)", fn, cov.Covered, cov.Total, pct)
		coveredTotal += cov.Covered
	}
	t.Logf("Overall: %d/%d BBs covered (%.1f%%)",
		coveredTotal, totalBBs, float64(coveredTotal)/float64(totalBBs)*100)

	assert.Greater(t, coveredTotal, 0, "Should have covered some BBs")
}

func TestCFGGuidedAnalyzer_Integration_GetCoveredLines(t *testing.T) {
	cfgPath := "/root/project/de-fuzz/gcc-v12.2.0-x64/gcc-build/gcc/cfgexpand.cc.015t.cfg"

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skipf("CFG file not found")
	}

	tmpDir, err := os.MkdirTemp("", "cfg-lines-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	analyzer, err := NewCFGGuidedAnalyzer(
		cfgPath,
		[]string{"stack_protect_classify_type"},
		"",
		filepath.Join(tmpDir, "mapping.json"),
	)
	require.NoError(t, err)

	// Initially no covered lines
	coveredLines := analyzer.GetCoveredLines()
	assert.Empty(t, coveredLines)

	// Record some coverage - use full paths matching CFG format
	lines := []string{
		testSourceFile + ":1819",
		testSourceFile + ":1820",
		testSourceFile + ":1821",
		"/root/project/de-fuzz/gcc-v12.2.0-x64/gcc-releases-gcc-12.2.0/gcc/tree.cc:100",
		"/root/project/de-fuzz/gcc-v12.2.0-x64/gcc-releases-gcc-12.2.0/gcc/tree.cc:101",
	}
	analyzer.RecordCoverage(1, lines)

	// Check covered lines
	coveredLinesMap := analyzer.GetCoveredLines()
	assert.Equal(t, 5, len(coveredLinesMap))

	// Verify all recorded lines are present using proper string parsing
	for _, line := range lines {
		// Find last colon to split file and line
		lastColon := -1
		for i := len(line) - 1; i >= 0; i-- {
			if line[i] == ':' {
				lastColon = i
				break
			}
		}
		if lastColon > 0 {
			file := line[:lastColon]
			var lineNum int
			fmt.Sscanf(line[lastColon+1:], "%d", &lineNum)
			lineID := LineID{File: file, Line: lineNum}
			assert.True(t, coveredLinesMap[lineID], "Line %s should be covered", line)
		}
	}

	// Record more coverage
	lines2 := []string{
		testSourceFile + ":1822", // New
		testSourceFile + ":1820", // Duplicate
	}
	analyzer.RecordCoverage(2, lines2)

	coveredLinesMap2 := analyzer.GetCoveredLines()
	assert.Equal(t, 6, len(coveredLinesMap2), "Should have 6 unique lines now")
}
