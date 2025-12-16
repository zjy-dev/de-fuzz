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

func TestCoverageMapping_Integration_PersistenceAndRecovery(t *testing.T) {
	// Create temp directory for testing
	tmpDir, err := os.MkdirTemp("", "mapping-integration-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mappingPath := filepath.Join(tmpDir, "coverage_mapping.json")

	// Test 1: Create new mapping and record some lines
	t.Run("create_and_record", func(t *testing.T) {
		mapping, err := NewCoverageMapping(mappingPath)
		require.NoError(t, err)
		require.NotNil(t, mapping)

		// Record lines from different seeds
		line1 := LineID{File: "gcc/cfgexpand.cc", Line: 1234}
		line2 := LineID{File: "gcc/cfgexpand.cc", Line: 1235}
		line3 := LineID{File: "gcc/tree.cc", Line: 456}

		// First seed covers lines 1 and 2
		isNew := mapping.RecordLine(line1, 1)
		assert.True(t, isNew, "First line should be new")

		isNew = mapping.RecordLine(line2, 1)
		assert.True(t, isNew, "Second line should be new")

		// Second seed covers line 3
		isNew = mapping.RecordLine(line3, 2)
		assert.True(t, isNew, "Third line should be new")

		// Try to record line1 again with different seed
		isNew = mapping.RecordLine(line1, 3)
		assert.False(t, isNew, "Line1 should already be covered")

		// Verify seed retrieval
		seedID, found := mapping.GetSeedForLine(line1)
		assert.True(t, found)
		assert.Equal(t, int64(1), seedID)

		seedID, found = mapping.GetSeedForLine(line3)
		assert.True(t, found)
		assert.Equal(t, int64(2), seedID)

		// Save to disk
		err = mapping.Save(mappingPath)
		require.NoError(t, err)

		// Verify file exists
		_, err = os.Stat(mappingPath)
		require.NoError(t, err)
	})

	// Test 2: Load persisted mapping and verify data
	t.Run("load_and_verify", func(t *testing.T) {
		mapping, err := NewCoverageMapping(mappingPath)
		require.NoError(t, err)

		// Verify previously recorded lines
		line1 := LineID{File: "gcc/cfgexpand.cc", Line: 1234}
		line2 := LineID{File: "gcc/cfgexpand.cc", Line: 1235}
		line3 := LineID{File: "gcc/tree.cc", Line: 456}

		seedID, found := mapping.GetSeedForLine(line1)
		assert.True(t, found)
		assert.Equal(t, int64(1), seedID)

		seedID, found = mapping.GetSeedForLine(line2)
		assert.True(t, found)
		assert.Equal(t, int64(1), seedID)

		seedID, found = mapping.GetSeedForLine(line3)
		assert.True(t, found)
		assert.Equal(t, int64(2), seedID)

		// Check non-existent line
		nonExistent := LineID{File: "gcc/other.cc", Line: 999}
		_, found = mapping.GetSeedForLine(nonExistent)
		assert.False(t, found)
	})

	// Test 3: Concurrent recording
	t.Run("concurrent_recording", func(t *testing.T) {
		mapping, err := NewCoverageMapping("")
		require.NoError(t, err)

		// Simulate concurrent coverage recording
		done := make(chan bool)
		for i := 0; i < 10; i++ {
			go func(seedID int) {
				for j := 0; j < 100; j++ {
					line := LineID{
						File: fmt.Sprintf("file%d.cc", seedID),
						Line: j,
					}
					mapping.RecordLine(line, int64(seedID))
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		// Verify all lines were recorded
		totalLines := mapping.TotalCoveredLines()
		assert.Equal(t, 1000, totalLines) // 10 files * 100 lines each
	})
}

func TestCoverageMapping_Integration_BatchRecording(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mapping-batch-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mappingPath := filepath.Join(tmpDir, "batch_mapping.json")

	mapping, err := NewCoverageMapping(mappingPath)
	require.NoError(t, err)

	// Record multiple lines from string format (file:line)
	lines := []string{
		"gcc/cfgexpand.cc:1234",
		"gcc/cfgexpand.cc:1235",
		"gcc/cfgexpand.cc:1236",
		"gcc/tree.cc:456",
		"gcc/tree.cc:457",
	}

	// Convert string lines to LineID
	lineIDs := make([]LineID, len(lines))
	for i, line := range lines {
		var file string
		var lineNum int
		fmt.Sscanf(line, "%[^:]:%d", &file, &lineNum)
		lineIDs[i] = LineID{File: file, Line: lineNum}
	}
	newCount := mapping.RecordLines(lineIDs, 10)
	assert.Equal(t, 5, newCount, "All lines should be new")

	// Record overlapping lines
	lines2 := []string{
		"gcc/cfgexpand.cc:1235", // Already covered
		"gcc/cfgexpand.cc:1237", // New
		"gcc/tree.cc:456",       // Already covered
		"gcc/tree.cc:458",       // New
	}

	// Convert lines2 to LineID
	lineIDs2 := make([]LineID, len(lines2))
	for i, line := range lines2 {
		var file string
		var lineNum int
		fmt.Sscanf(line, "%[^:]:%d", &file, &lineNum)
		lineIDs2[i] = LineID{File: file, Line: lineNum}
	}
	newCount = mapping.RecordLines(lineIDs2, 11)
	assert.Equal(t, 2, newCount, "Only 2 lines should be new")

	// Verify total stats
	totalLines := mapping.TotalCoveredLines()
	assert.Equal(t, 7, totalLines) // 5 + 2 new lines

	// Save and verify persistence
	err = mapping.Save(mappingPath)
	require.NoError(t, err)

	// Load and check
	mapping2, err := NewCoverageMapping(mappingPath)
	require.NoError(t, err)

	totalLines2 := mapping2.TotalCoveredLines()
	assert.Equal(t, totalLines, totalLines2)
}

func TestCoverageMapping_Integration_FindClosestCoveredLine(t *testing.T) {
	mapping, err := NewCoverageMapping("")
	require.NoError(t, err)

	// Setup: Record lines at specific locations
	lines := []LineID{
		{File: "gcc/cfgexpand.cc", Line: 100},
		{File: "gcc/cfgexpand.cc", Line: 200},
		{File: "gcc/cfgexpand.cc", Line: 300},
		{File: "gcc/tree.cc", Line: 150},
	}

	for i, line := range lines {
		mapping.RecordLine(line, int64(i+1))
	}

	// Test: Find closest covered line above a target
	t.Run("find_closest_above", func(t *testing.T) {
		closest, seedID, found := mapping.FindClosestCoveredLine("gcc/cfgexpand.cc", 250)

		assert.True(t, found)
		assert.Equal(t, 200, closest.Line) // Closest line below 250
		assert.Equal(t, int64(2), seedID)
	})

	t.Run("find_closest_same_file_only", func(t *testing.T) {
		closest, seedID, found := mapping.FindClosestCoveredLine("gcc/cfgexpand.cc", 350)

		assert.True(t, found)
		assert.Equal(t, 300, closest.Line) // Closest line in same file
		assert.Equal(t, int64(3), seedID)
	})

	t.Run("no_lines_in_file", func(t *testing.T) {
		_, _, found := mapping.FindClosestCoveredLine("gcc/unknown.cc", 100)

		assert.False(t, found)
	})

	t.Run("no_lines_before_target", func(t *testing.T) {
		_, _, found := mapping.FindClosestCoveredLine("gcc/cfgexpand.cc", 50)

		assert.False(t, found, "No lines before line 50")
	})
}

func TestCoverageMapping_Integration_LargeScale(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large scale test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "mapping-large-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mappingPath := filepath.Join(tmpDir, "large_mapping.json")
	mapping, err := NewCoverageMapping(mappingPath)
	require.NoError(t, err)

	// Simulate large-scale fuzzing: 100 files, 1000 lines each, 1000 seeds
	const numFiles = 100
	const linesPerFile = 1000
	const numSeeds = 1000

	t.Logf("Recording %d lines across %d seeds...", numFiles*linesPerFile, numSeeds)

	for seedID := 0; seedID < numSeeds; seedID++ {
		lines := make([]string, 0, linesPerFile/numSeeds)
		for file := 0; file < numFiles; file++ {
			// Each seed covers a subset of lines
			for line := seedID * (linesPerFile / numSeeds); line < (seedID+1)*(linesPerFile/numSeeds); line++ {
				lines = append(lines, fmt.Sprintf("file%d.cc:%d", file, line))
			}
		}
		// Convert to LineID and record
		lineIDs := make([]LineID, len(lines))
		for i, line := range lines {
			var file string
			var lineNum int
			fmt.Sscanf(line, "%[^:]:%d", &file, &lineNum)
			lineIDs[i] = LineID{File: file, Line: lineNum}
		}
		mapping.RecordLines(lineIDs, int64(seedID))
	}

	totalLines := mapping.TotalCoveredLines()
	t.Logf("Coverage stats: %d covered lines, %d files", totalLines, len(mapping.GetCoveredLines()))

	// Save to disk
	err = mapping.Save(mappingPath)
	require.NoError(t, err)

	// Check file size
	fileInfo, err := os.Stat(mappingPath)
	require.NoError(t, err)
	t.Logf("Mapping file size: %d bytes (%.2f MB)", fileInfo.Size(), float64(fileInfo.Size())/1024/1024)

	// Load and verify
	mapping2, err := NewCoverageMapping(mappingPath)
	require.NoError(t, err)

	totalLines2 := mapping2.TotalCoveredLines()
	assert.Equal(t, totalLines, totalLines2)
}
